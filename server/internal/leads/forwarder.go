// forwarder.go: reliable, revocable delivery of lead facts to the acquisition
// side via platform-notify directed events.
//
// 语义(票面 + HUI-1734):
//   - notify 持久接受(2xx)即确认源 → 线索 pending_sync("待同步");
//   - deliveries delivered → crm_received;dead / subscription_disabled → rejected;
//   - 传输失败/未确认 → 停留在待同步并记账,**不伪报入库**,不阻塞内容预览;
//   - 撤销是终态:forwarder 发现撤销行时把未发的 submit 事实标记 suppressed
//     (零网络);已发出的靠 revoke 事实(更高 source_version)收敛,
//     目标侧版本单调守卫保证重放旧事件不能恢复营销权限。
package leads

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/notifytask"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Poster is the notify transport the forwarder depends on (satisfied by
// notifytask.DirectedClient; an interface keeps tests stub-driven).
type Poster = notifytask.DirectedPoster

// MaxForwardAttempts bounds retries per outbox row before it stops being picked
// (kept small for T1; the notify side owns the real bounded-backoff ladder).
const MaxForwardAttempts = 8

// Forwarder drives the leads outbox.
type Forwarder struct {
	St       *store.Store
	Poster   Poster
	MaxTries int
	Log      *log.Logger
}

// NewForwarder assembles a forwarder over the store and a notify client.
func NewForwarder(st *store.Store, poster Poster, logger *log.Logger) *Forwarder {
	if logger == nil {
		logger = log.Default()
	}
	return &Forwarder{St: st, Poster: poster, MaxTries: MaxForwardAttempts, Log: logger}
}

// TickResult summarizes one pass (for tests and observability).
type TickResult struct {
	Published int // facts accepted by notify this pass
	Confirmed int // deliveries confirmed delivered this pass
	Rejected  int // dead/disabled deliveries this pass
	Suppressed int // facts withheld because their lead was revoked
	Failed    int // transport failures this pass
}

// Tick performs one outbox pass. It never returns early on row-level errors:
// one bad row must not starve the others.
func (f *Forwarder) Tick(ctx context.Context) TickResult {
	var res TickResult
	rows, err := f.St.ListLeadsOutboxDue(50)
	if err != nil {
		f.Log.Printf("leads forwarder: list outbox: %v", err)
		return res
	}
	for _, row := range rows {
		if row.Attempts >= f.MaxTries {
			// leave the row pending (visible debt) — notify owns retry policy upstream
			continue
		}
		lead, err := f.St.GetLeadSubmissionByRef(row.SubmissionRef)
		if err != nil {
			f.Log.Printf("leads forwarder: lead %s missing for outbox %s: %v", row.SubmissionRef, row.EventID, err)
			continue
		}
		// 撤销终态:本地未发的 submit 事实一律拦下(零网络,阻止未投递数据继续同步)。
		// revoke 事实恰恰是撤销语义的载体,必须照发(目标按版本守卫收敛)。
		if lead.SyncState == StateRevoked && row.Kind != "revoke" {
			if err := f.St.MarkOutboxSuppressed(row.EventID); err == nil {
				res.Suppressed++
				_ = f.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "suppressed", `{"reason":"revoked_before_send"}`, "system")
			}
			continue
		}
		if err := f.Poster.PostEvent(ctx, []byte(row.PayloadJSON)); err != nil {
			res.Failed++
			_ = f.St.RecordOutboxError(row.EventID, err.Error())
			_ = f.St.RecordLeadSyncError(lead.SubmissionRef, err.Error())
			f.Log.Printf("leads forwarder: publish %s failed (kept pending): %v", row.EventID, err)
			continue
		}
		if err := f.St.MarkOutboxForwarded(row.EventID); err != nil {
			f.Log.Printf("leads forwarder: mark forwarded %s: %v", row.EventID, err)
			continue
		}
		res.Published++
		if row.Kind == "submit" {
			if err := f.St.MarkLeadSyncPending(lead.SubmissionRef); err != nil {
				f.Log.Printf("leads forwarder: state pending %s: %v", lead.SubmissionRef, err)
			}
			_ = f.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "sync_pending", `{"via":"notify"}`, "system")
		}
	}
	// second phase: confirmations for facts already accepted by notify
	f.confirmDeliveries(ctx, &res)
	return res
}

// confirmDeliveries polls notify's deliveries endpoint for pending_sync leads
// and resolves them. Queries are read-only on the notify side (零副作用).
func (f *Forwarder) confirmDeliveries(ctx context.Context, res *TickResult) {
	pending, err := f.St.ListLeadSubmissionsByState(StatePendingSync, 50)
	if err != nil {
		f.Log.Printf("leads forwarder: list pending: %v", err)
		return
	}
	for _, lead := range pending {
		deliveries, err := f.Poster.Deliveries(ctx, lead.SubmissionRef)
		if err != nil {
			res.Failed++
			_ = f.St.RecordLeadSyncError(lead.SubmissionRef, err.Error())
			continue
		}
		verdict, reason := resolveDeliveries(deliveries)
		switch verdict {
		case "delivered":
			if err := f.St.MarkLeadDelivered(lead.SubmissionRef); err == nil {
				res.Confirmed++
				_ = f.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "sync_delivered", `{"by":"deliveries"}`, "system")
			}
		case "dead":
			if err := f.St.MarkLeadRejected(lead.SubmissionRef, reason); err == nil {
				res.Rejected++
				reasonJSON, _ := json.Marshal(reason)
				_ = f.St.AppendLeadAudit(lead.TenantID, lead.SubmissionRef, "sync_rejected", `{"reason":`+string(reasonJSON)+`}`, "system")
			}
		default:
			// still pending/in_flight: keep waiting, never fake receipt
		}
	}
}

// resolveDeliveries folds per-route delivery rows into one verdict. Rules:
// any delivered row wins (the fact reached the target); otherwise any dead /
// subscription_disabled row rejects; otherwise keep waiting.
func resolveDeliveries(items []notifytask.DeliveryStatus) (verdict, reason string) {
	deadReason := ""
	for _, d := range items {
		switch d.Status {
		case "delivered":
			return "delivered", ""
		case "dead", "subscription_disabled":
			if deadReason == "" {
				deadReason = d.Status
				if d.LastError != "" {
					deadReason += ": " + d.LastError
				}
			}
		}
	}
	if deadReason != "" {
		return "dead", deadReason
	}
	return "", ""
}

// Run drives Tick on an interval until ctx is done (production loop).
func (f *Forwarder) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.Tick(ctx)
		}
	}
}
