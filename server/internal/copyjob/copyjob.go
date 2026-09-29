// Package copyjob is the quote → confirm → generate → select → save
// state machine around the already merged copydraft generator.
//
// It does not invent copy. copydraft.Compose / ApplyModel stay the only
// fact filter. Without a real model result, Success stays false.
// This package does not create a GoBoost project and does not mutate a wallet.
package copyjob

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/activityspend"
	"github.com/bianjiefilm/touch-engine/server/internal/copydraft"
)

const (
	RealIncomplete = "incomplete"
	RealCompleted  = "completed"

	StateQuoted       = "quoted"
	StateConfirmed    = "confirmed"
	StateGenerating   = "generating"
	StateIncomplete   = "generation_incomplete"
	StateGenerated    = "generated"
	StateSelected     = "selected"
	StateSaved        = "saved"
	StateRevoked      = "revoked"
	StateTimedOut     = "timed_out"
	StateQuota        = "quota_exceeded"
	StateInputChanged = "input_changed"
	StateLate         = "late_discarded"

	SettlementNone     = "not_captured"
	SettlementHeld     = "held"
	SettlementCaptured = "captured"

	Capability = "touch.ai_copy"
)

// ErrNotSelectable means select was asked before a draft exists.
var ErrNotSelectable = errors.New("copyjob: draft is not selectable")

// Product is the merchant's product snapshot. Unconfirmed text is not copy.
type Product struct {
	Text   string `json:"text"`
	Status string `json:"status"`
}

// Facts are the merchant fields frozen at quote time.
type Facts struct {
	Product  Product           `json:"product"`
	Price    copydraft.Fact    `json:"price"`
	Address  copydraft.Fact    `json:"address"`
	Hours    copydraft.Fact    `json:"hours"`
	Claims   []copydraft.Claim `json:"claims"`
	POINames []string          `json:"poi_names"`
	AssetIDs []string          `json:"asset_ids"`
}

// Live is the campaign and store row at a moment in time.
type Live struct {
	StoreName     string
	StoreAddress  string
	CampaignTitle string
	PublicContent string
}

// Snapshot is the aligned input. Channel mount is never taken from the client.
type Snapshot struct {
	StoreName     string            `json:"store_name"`
	StoreAddress  string            `json:"store_address"`
	CampaignTitle string            `json:"campaign_title"`
	PublicContent string            `json:"public_content"`
	Product       Product           `json:"product"`
	Price         copydraft.Fact    `json:"price"`
	Address       copydraft.Fact    `json:"address"`
	Hours         copydraft.Fact    `json:"hours"`
	Claims        []copydraft.Claim `json:"claims"`
	POINames      []string          `json:"poi_names"`
	AssetIDs      []string          `json:"asset_ids"`
}

// Trace is the cost, model, and source record. Billed stays false until a
// captured settlement exists; a hold is not a spend.
type Trace struct {
	PricingBasis     string `json:"pricing_basis"`
	Priced           bool   `json:"priced"`
	AmountMinor      *int64 `json:"amount_minor"`
	Currency         string `json:"currency"`
	PayerAccountID   string `json:"payer_account_id"`
	ModelID          string `json:"model_id"`
	ModelVersion     string `json:"model_version"`
	RealGeneration   string `json:"real_generation"`
	Billed           bool   `json:"billed"`
	ChargeCount      int    `json:"charge_count"`
	BalanceMutated   bool   `json:"balance_mutated"`
	GoBoostProjectID string `json:"goboost_project_id"`
	Success          bool   `json:"success"`
	Reason           string `json:"quote_reason"`
	RewardsTriggered bool   `json:"rewards_triggered"`
	Settlement       string `json:"settlement"`
	HoldID           string `json:"hold_id,omitempty"`
	TaskID           string `json:"task_id,omitempty"`
}

// Job is one merchant copy run.
type Job struct {
	ID             string          `json:"id"`
	IdempotencyKey string          `json:"idempotency_key"`
	State          string          `json:"state"`
	Facts          Facts           `json:"facts"`
	Snapshot       Snapshot        `json:"snapshot"`
	SnapshotHash   string          `json:"snapshot_hash"`
	BaselineTitle  string          `json:"baseline_title"`
	BaselinePublic string          `json:"baseline_public"`
	Trace          Trace           `json:"trace"`
	Draft          copydraft.Draft `json:"draft"`
	DraftID        string          `json:"draft_id"`
	Notice         string          `json:"notice"`
	LateIgnored    bool            `json:"late_ignored"`
	Preserved      []string        `json:"preserved,omitempty"`
}

// QuoteInput starts a run. ModelReady only records whether a later generate
// could call out; it does not mark the quote as a finished copy.
type QuoteInput struct {
	Key            string
	Live           Live
	Facts          Facts
	AssetIDs       []string
	PayerAccountID string
	Charge         activityspend.ChargeDecision
	ModelReady     bool
}

// Plan is the next generate step. Busy means a run is already in flight.
type Plan struct {
	Job       Job
	Compose   bool
	CallModel bool
	Busy      bool
	Blocked   string
}

// ModelMeta is what a real transport proved. Empty model id cannot succeed.
type ModelMeta struct {
	ModelID      string
	ModelVersion string
	TaskID       string
	HoldID       string
	AmountMinor  int64
	Priced       bool
	Submitted    bool
}

// SavePlan says which campaign fields may change. Status is never in the plan.
type SavePlan struct {
	Allow            bool
	Reason           string
	WriteTitle       *string
	WriteIntro       *string
	Topics           []string
	Preserved        []string
	RewardsTriggered bool
	Mutates          bool
}

// BuildSnapshot aligns merchant facts with the current store and campaign.
// Place names stay suggestions: channel mount is always false.
func BuildSnapshot(live Live, facts Facts, assetIDs []string) Snapshot {
	facts = normalizeFacts(facts)
	addr := facts.Address
	if addr.Status == copydraft.StatusConfirmed {
		if strings.TrimSpace(live.StoreAddress) == "" || strings.TrimSpace(addr.Text) != strings.TrimSpace(live.StoreAddress) {
			addr.Status = copydraft.StatusUncertain
		}
	}
	return Snapshot{
		StoreName:     strings.TrimSpace(live.StoreName),
		StoreAddress:  strings.TrimSpace(live.StoreAddress),
		CampaignTitle: strings.TrimSpace(live.CampaignTitle),
		PublicContent: strings.TrimSpace(live.PublicContent),
		Product:       facts.Product,
		Price:         facts.Price,
		Address:       addr,
		Hours:         facts.Hours,
		Claims:        facts.Claims,
		POINames:      facts.POINames,
		AssetIDs:      nonEmpty(assetIDs),
	}
}

// Hash identifies one aligned snapshot.
func (s Snapshot) Hash() string {
	raw, _ := json.Marshal(s)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Input is the generator input. Product text is not copied into it.
func (s Snapshot) Input() copydraft.Input {
	return copydraft.Input{
		StoreName:             s.StoreName,
		CampaignTitle:         s.CampaignTitle,
		PublicContent:         s.PublicContent,
		Price:                 s.Price,
		Address:               s.Address,
		Hours:                 s.Hours,
		Claims:                append([]copydraft.Claim(nil), s.Claims...),
		POINames:              append([]string(nil), s.POINames...),
		ChannelMountAvailable: false,
		AssetIDs:              append([]string(nil), s.AssetIDs...),
		ModelAuthorized:       false,
	}
}

// Prompt lists only confirmed facts for a model call.
func Prompt(s Snapshot) string {
	var b strings.Builder
	b.WriteString("只根据下面已确认资料写短标题、简介和话题。不要补充未列出的价格、地址、营业时间、商品或宣传。\n")
	if s.StoreName != "" {
		b.WriteString("门店名:" + s.StoreName + "\n")
	}
	if s.CampaignTitle != "" {
		b.WriteString("活动标题:" + s.CampaignTitle + "\n")
	}
	if s.PublicContent != "" {
		b.WriteString("公开内容:" + s.PublicContent + "\n")
	}
	if s.Product.Status == copydraft.StatusConfirmed && strings.TrimSpace(s.Product.Text) != "" {
		b.WriteString("商品:" + strings.TrimSpace(s.Product.Text) + "\n")
	}
	if s.Price.Status == copydraft.StatusConfirmed && strings.TrimSpace(s.Price.Text) != "" {
		b.WriteString("价格:" + strings.TrimSpace(s.Price.Text) + "\n")
	}
	if s.Address.Status == copydraft.StatusConfirmed && strings.TrimSpace(s.Address.Text) != "" {
		b.WriteString("地址:" + strings.TrimSpace(s.Address.Text) + "\n")
	}
	if s.Hours.Status == copydraft.StatusConfirmed && strings.TrimSpace(s.Hours.Text) != "" {
		b.WriteString("营业时间:" + strings.TrimSpace(s.Hours.Text) + "\n")
	}
	for _, c := range s.Claims {
		if strings.TrimSpace(c.Evidence) == "" || strings.TrimSpace(c.Text) == "" {
			continue
		}
		b.WriteString("宣称:" + strings.TrimSpace(c.Text) + "\n")
	}
	return b.String()
}

// ProductGap asks for a product snapshot the generator is not allowed to invent.
func ProductGap(p Product) (copydraft.Gap, bool) {
	if p.Status == copydraft.StatusConfirmed && strings.TrimSpace(p.Text) != "" {
		return copydraft.Gap{}, false
	}
	switch p.Status {
	case copydraft.StatusExpired:
		return copydraft.Gap{Code: "product_expired", Message: "商品资料已过期，请补充。系统不会把过期商品写成文案。"}, true
	case copydraft.StatusAbsent:
		return copydraft.Gap{Code: "product_missing", Message: "没有已确认的商品资料，请补充。系统不会编造商品。"}, true
	default:
		if strings.TrimSpace(p.Text) == "" {
			return copydraft.Gap{Code: "product_missing", Message: "没有已确认的商品资料，请补充。系统不会编造商品。"}, true
		}
		return copydraft.Gap{Code: "product_unconfirmed", Message: "商品资料未确认，请补充。系统不会把未确认商品写成文案。"}, true
	}
}

// Quote records a price context and the confirmed-fact gaps. It does not
// generate usable copy and does not capture a payment.
func Quote(in QuoteInput) Job {
	facts := normalizeFacts(in.Facts)
	facts.AssetIDs = nonEmpty(in.AssetIDs)
	snap := BuildSnapshot(in.Live, facts, facts.AssetIDs)
	basis := "unavailable"
	if in.Charge.QuoteCreated && !in.Charge.BalanceMutated {
		basis = activityspend.ReasonQuoteWithoutDebit
	}
	notice := "已记录报价。没有可用的模型凭证，确认后也不会把文案标成成功，也不会扣费。"
	switch {
	case in.ModelReady && in.Charge.QuoteCreated:
		notice = "已记录报价。确认后才会生成。现在还没有扣费，也还没有写入活动。"
	case in.ModelReady && !in.Charge.QuoteCreated:
		notice = "已记录报价。付款方未确认，不会发起收费生成。"
	}
	job := Job{
		IdempotencyKey: strings.TrimSpace(in.Key),
		State:          StateQuoted,
		Facts:          facts,
		Snapshot:       snap,
		SnapshotHash:   snap.Hash(),
		BaselineTitle:  snap.CampaignTitle,
		BaselinePublic: snap.PublicContent,
		Trace: Trace{
			PricingBasis:     basis,
			Currency:         "CNY",
			PayerAccountID:   strings.TrimSpace(in.PayerAccountID),
			RealGeneration:   RealIncomplete,
			Reason:           in.Charge.Reason,
			Settlement:       SettlementNone,
			GoBoostProjectID: "",
		},
		Draft:  withProductGap(copydraft.Compose(snap.Input()), facts.Product),
		Notice: notice,
	}
	return seal(job)
}

// Confirm moves quoted → confirmed when the live snapshot still matches.
func Confirm(job Job, hash string) Job {
	if frozen(job.State) {
		return seal(job)
	}
	if hash != job.SnapshotHash {
		return MarkInputChanged(job)
	}
	if job.State == StateQuoted {
		job.State = StateConfirmed
		job.Notice = "报价已确认。还没有生成，也没有扣费。"
	}
	return seal(job)
}

// PlanGenerate decides the next step. A terminal job is returned unchanged
// so a retry cannot submit or compose a second time.
func PlanGenerate(job Job, hash string, quotaOK, modelReady bool) Plan {
	switch job.State {
	case StateGenerating:
		return Plan{Job: seal(job), Busy: true}
	case StateQuoted:
		return Plan{Job: seal(job), Blocked: "not_confirmed"}
	case StateConfirmed:
		if hash != job.SnapshotHash {
			return Plan{Job: MarkInputChanged(job)}
		}
		if !quotaOK {
			return Plan{Job: MarkQuota(job)}
		}
		if !modelReady {
			job.State = StateIncomplete
			return Plan{Job: seal(job), Compose: true}
		}
		job.State = StateGenerating
		return Plan{Job: seal(job), CallModel: true}
	default:
		return Plan{Job: seal(job)}
	}
}

// FinishIncomplete stores the generator's non-usable draft. Success stays false.
func FinishIncomplete(job Job, draft copydraft.Draft) Job {
	draft.Usable = false
	draft.Billed = false
	if draft.ModelStatus == "" {
		draft.ModelStatus = copydraft.ModelNotAuthorized
	}
	job.Draft = withProductGap(draft, job.Facts.Product)
	job.State = StateIncomplete
	job.Trace.RealGeneration = RealIncomplete
	job.Trace.Success = false
	job.Notice = "真实生成未完成。没有可用的模型结果，不能把这次当成成功文案，也没有扣费。"
	return seal(job)
}

// FinishModel applies a transport result. A late result for a revoked,
// timed-out, or already saved job is discarded and does not add a charge.
func FinishModel(job Job, draft copydraft.Draft, meta ModelMeta) Job {
	if job.State != StateGenerating {
		return RejectLate(job)
	}
	if meta.Submitted {
		job = noteSubmit(job)
	}
	draft.Billed = false
	job.Draft = withProductGap(draft, job.Facts.Product)
	job.Trace.ModelID = strings.TrimSpace(meta.ModelID)
	job.Trace.ModelVersion = strings.TrimSpace(meta.ModelVersion)
	job.Trace.TaskID = strings.TrimSpace(meta.TaskID)
	job.Trace.HoldID = strings.TrimSpace(meta.HoldID)
	if meta.Priced && meta.AmountMinor > 0 {
		job.Trace.Priced = true
		amt := meta.AmountMinor
		job.Trace.AmountMinor = &amt
	}
	if job.Trace.HoldID != "" {
		job.Trace.Settlement = SettlementHeld
	}
	if job.Draft.Usable && job.Draft.ModelStatus == copydraft.ModelAuthorized && job.Trace.ModelID != "" && job.Trace.ModelVersion != "" {
		job.State = StateGenerated
		job.Trace.RealGeneration = RealCompleted
		job.Trace.Success = true
		job.Notice = "模型输出已按已确认资料核对。这仍是草稿，尚未发布，也还没有写入活动。"
	} else {
		job.Draft.Usable = false
		job.State = StateIncomplete
		job.Trace.RealGeneration = RealIncomplete
		job.Trace.Success = false
		job.Notice = "真实生成未完成。模型输出不能当成成功文案。"
	}
	return seal(job)
}

// RejectLate drops a result that arrived after the merchant moved on.
func RejectLate(job Job) Job {
	job.LateIgnored = true
	job.Trace.Success = false
	if job.State != StateSaved && job.State != StateSelected && job.State != StateRevoked && job.State != StateTimedOut && job.State != StateInputChanged && job.State != StateQuota {
		job.State = StateLate
	}
	if job.Notice == "" || job.State == StateLate {
		job.Notice = "迟到的结果已丢弃，没有覆盖文字，也没有再次扣费。"
	}
	return seal(job)
}

// Timeout closes an in-flight generate. A second timeout does not add a charge.
func Timeout(job Job, submitted bool) Job {
	if job.State != StateGenerating {
		return seal(job)
	}
	if submitted {
		job = noteSubmit(job)
	}
	job.State = StateTimedOut
	job.Trace.Success = false
	job.Trace.RealGeneration = RealIncomplete
	job.Notice = "生成超时。没有重复扣费，未写入活动。"
	return seal(job)
}

// MarkQuota stops a run when the daily draft slot or the provider quota is gone.
func MarkQuota(job Job) Job {
	if frozen(job.State) {
		return seal(job)
	}
	job.State = StateQuota
	job.Trace.Success = false
	job.Trace.RealGeneration = RealIncomplete
	job.Notice = "额度不足。这次没有扣费，也没有写入活动。"
	return seal(job)
}

// MarkInputChanged refuses to continue a stale quote.
func MarkInputChanged(job Job) Job {
	if job.State == StateSaved || job.State == StateRevoked || job.State == StateTimedOut {
		return seal(job)
	}
	job.State = StateInputChanged
	job.Trace.Success = false
	job.Notice = "资料已变更。请重新报价。这次没有扣费，也不会覆盖已写的文字。"
	return seal(job)
}

// Revoke stops a run. A saved campaign is not rolled back and is not charged again.
func Revoke(job Job) Job {
	if job.State == StateSaved || job.State == StateRevoked {
		return seal(job)
	}
	job.State = StateRevoked
	job.Trace.Success = false
	job.Notice = "已撤销。迟到的结果不会写入活动，也不会再次扣费。"
	return seal(job)
}

// Select records the merchant's draft choice. It does not publish or save.
func Select(job Job) (Job, error) {
	switch job.State {
	case StateGenerated, StateIncomplete, StateSelected:
		if strings.TrimSpace(job.DraftID) == "" {
			return seal(job), ErrNotSelectable
		}
		job.State = StateSelected
		job.Trace.RewardsTriggered = false
		return seal(job), nil
	default:
		return seal(job), ErrNotSelectable
	}
}

// PlanSave writes title and intro only when generation really completed and
// the merchant has not typed over the baseline. Status is not changed.
func PlanSave(job Job, currentTitle, currentPublic string) SavePlan {
	plan := SavePlan{RewardsTriggered: false}
	if job.State != StateSelected {
		plan.Reason = "not_selected"
		return plan
	}
	if !job.Trace.Success || job.Trace.RealGeneration != RealCompleted || !job.Draft.Usable {
		plan.Reason = "generation_incomplete"
		return plan
	}
	if currentTitle == job.BaselineTitle && strings.TrimSpace(job.Draft.Title) != "" {
		title := job.Draft.Title
		plan.WriteTitle = &title
	} else if currentTitle != job.BaselineTitle {
		plan.Preserved = append(plan.Preserved, "title")
	}
	if currentPublic == job.BaselinePublic && strings.TrimSpace(job.Draft.Intro) != "" {
		intro := job.Draft.Intro
		plan.WriteIntro = &intro
	} else if currentPublic != job.BaselinePublic {
		plan.Preserved = append(plan.Preserved, "intro")
	}
	if len(job.Draft.Topics) > 0 {
		plan.Topics = append([]string(nil), job.Draft.Topics...)
	}
	plan.Mutates = plan.WriteTitle != nil || plan.WriteIntro != nil || len(plan.Topics) > 0
	if !plan.Mutates {
		plan.Reason = "handwritten_kept"
		return plan
	}
	plan.Allow = true
	plan.Reason = "saved"
	return plan
}

// MarkSaved records an explicit save. It does not change campaign status.
func MarkSaved(job Job, preserved []string) Job {
	job.State = StateSaved
	job.Preserved = append([]string(nil), preserved...)
	job.Trace.RewardsTriggered = false
	job.Notice = "已写入活动文案。活动状态未改变，没有发奖励。"
	if len(preserved) > 0 {
		job.Notice = "已写入未改动的栏位。手写的标题或简介没有被覆盖。活动状态未改变，没有发奖励。"
	}
	return seal(job)
}

func noteSubmit(job Job) Job {
	if job.Trace.ChargeCount < 1 {
		job.Trace.ChargeCount = 1
	}
	return job
}

func frozen(state string) bool {
	switch state {
	case StateSaved, StateRevoked, StateTimedOut, StateQuota, StateInputChanged, StateLate:
		return true
	default:
		return false
	}
}

func seal(job Job) Job {
	job.Trace.GoBoostProjectID = ""
	job.Trace.BalanceMutated = false
	job.Trace.RewardsTriggered = false
	if job.Trace.ChargeCount > 1 {
		job.Trace.ChargeCount = 1
	}
	if job.Trace.ChargeCount < 0 {
		job.Trace.ChargeCount = 0
	}
	if job.Trace.Currency == "" {
		job.Trace.Currency = "CNY"
	}
	if job.Trace.Settlement == "" {
		job.Trace.Settlement = SettlementNone
	}
	if job.Trace.Settlement != SettlementCaptured {
		job.Trace.Billed = false
	}
	if job.Draft.Topics == nil {
		job.Draft.Topics = []string{}
	}
	if job.Draft.Gaps == nil {
		job.Draft.Gaps = []copydraft.Gap{}
	}
	completed := job.Trace.RealGeneration == RealCompleted &&
		job.Draft.Usable &&
		job.Draft.ModelStatus == copydraft.ModelAuthorized &&
		strings.TrimSpace(job.Trace.ModelID) != "" &&
		strings.TrimSpace(job.Trace.ModelVersion) != ""
	if !completed {
		job.Trace.Success = false
		job.Trace.RealGeneration = RealIncomplete
	} else {
		job.Trace.Success = true
	}
	if !job.Trace.Priced {
		job.Trace.AmountMinor = nil
	}
	job.Draft.Billed = false
	return job
}

func withProductGap(d copydraft.Draft, p Product) copydraft.Draft {
	g, ok := ProductGap(p)
	if !ok {
		return d
	}
	for _, existing := range d.Gaps {
		if existing.Code == g.Code {
			return d
		}
	}
	gaps := append([]copydraft.Gap{}, d.Gaps...)
	d.Gaps = append(gaps, g)
	return d
}

func normalizeFacts(facts Facts) Facts {
	facts.Product.Text = strings.TrimSpace(facts.Product.Text)
	facts.Product.Status = normStatus(facts.Product.Status)
	facts.Price.Text = strings.TrimSpace(facts.Price.Text)
	facts.Price.Status = normStatus(facts.Price.Status)
	facts.Address.Text = strings.TrimSpace(facts.Address.Text)
	facts.Address.Status = normStatus(facts.Address.Status)
	facts.Hours.Text = strings.TrimSpace(facts.Hours.Text)
	facts.Hours.Status = normStatus(facts.Hours.Status)
	claims := make([]copydraft.Claim, 0, len(facts.Claims))
	for _, c := range facts.Claims {
		c.Text = strings.TrimSpace(c.Text)
		c.Evidence = strings.TrimSpace(c.Evidence)
		if c.Text == "" {
			continue
		}
		claims = append(claims, c)
	}
	facts.Claims = claims
	facts.POINames = nonEmpty(facts.POINames)
	facts.AssetIDs = nonEmpty(facts.AssetIDs)
	return facts
}

func normStatus(status string) string {
	switch status {
	case copydraft.StatusConfirmed, copydraft.StatusExpired, copydraft.StatusUncertain, copydraft.StatusAbsent:
		return status
	default:
		return copydraft.StatusUncertain
	}
}

func nonEmpty(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
