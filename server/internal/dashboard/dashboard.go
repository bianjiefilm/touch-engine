// Package dashboard assembles the FEAT-0178 数据统计看板 (HUI-1677): a
// read-only aggregation of REAL touch-domain facts plus an honest UNKNOWN
// inventory for external-platform metrics.
//
// 纪律(票面 2026-09-19「统计真实性与生态来源链」硬约束):
//   - v1 只汇总 touch 域已有真实事实,不新造事件源:碰/扫码触发(匿名浏览
//     beacon,canonical 短码 URL 命中)、留资访问(挂了启用表单的活动的访问)、
//     授权留资提交(dedup_key 幂等,分母=唯一联系人指纹去重)、CRM 接收
//     (directed-event 状态机 pending_sync→crm_received 的真实状态)。
//   - 拿不到的外部平台指标 = UNKNOWN:available=false + reason + 上游引用,
//     值永不为 0,不伪造、不推算、无演示数据。
//   - 原始事实与汇总分离:本包不落任何表,汇总按需计算;窗口 [start,end]
//     由调用方给出,同窗口重算幂等一致。晚到事实按事件时间落窗;重复事实
//     由幂等键不重复计;撤销事实不计正向(单独按状态披露)。
//   - 本包是纯函数层:不触网络、不写库;租户/门店作用域由 store 层 SQL
//     强制(fail-closed),客户端输入永不参与授权。
package dashboard

import (
	"fmt"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Metric keys, fixed catalog order (touch facts first, then the UNKNOWN
// external inventory in ticket order).
const (
	KeyTouchTriggers     = "touch_triggers"     // 碰/扫码触发次数
	KeyLeadPageVisits    = "lead_page_visits"   // 留资访问(挂载启用表单的活动页访问)
	KeyLeadSubmissions   = "lead_submissions"   // 授权留资提交
	KeyCRMReceived       = "crm_received"       // CRM 接收
	KeyPublishedVideos   = "published_videos"   // 发布视频数(UNKNOWN)
	KeyUGCPlays          = "ugc_plays"          // UGC 播放(UNKNOWN)
	KeyUGCLikes          = "ugc_likes"          // UGC 点赞(UNKNOWN)
	KeyPOIExposureDelta  = "poi_exposure_delta" // POI 曝光增量(UNKNOWN)
	KeyCouponRedemptions = "coupon_redemptions" // 优惠券核销(UNKNOWN)
)

// Window is the validated, UTC-normalized statistics window. Comparisons run
// at whole-second granularity (stored timestamps are RFC3339(Nano) UTC); both
// bounds are inclusive. View facts are day-granular (public_view_stats.day,
// the beacon arrival day): a day counts when its date falls inside the window
// dates, so a partially covered boundary day is included — deterministic and
// stated in every affected metric definition.
type Window struct {
	Start time.Time
	End   time.Time
}

// ParseWindow validates the raw query parameters. Both bounds are required,
// must be RFC3339, and end must not precede start. Bounds are normalized to
// UTC and truncated to whole seconds so recomputation is deterministic.
func ParseWindow(startRaw, endRaw string) (Window, error) {
	if startRaw == "" || endRaw == "" {
		return Window{}, fmt.Errorf("dashboard: window_start and window_end are required (RFC3339)")
	}
	start, err := time.Parse(time.RFC3339, startRaw)
	if err != nil {
		return Window{}, fmt.Errorf("dashboard: window_start is not RFC3339: %w", err)
	}
	end, err := time.Parse(time.RFC3339, endRaw)
	if err != nil {
		return Window{}, fmt.Errorf("dashboard: window_end is not RFC3339: %w", err)
	}
	start, end = start.UTC().Truncate(time.Second), end.UTC().Truncate(time.Second)
	if end.Before(start) {
		return Window{}, fmt.Errorf("dashboard: window_end precedes window_start")
	}
	return Window{Start: start, End: end}, nil
}

// StartDay / EndDay are the inclusive UTC date bounds for day-granular facts.
func (w Window) StartDay() string { return w.Start.Format("2006-01-02") }
func (w Window) EndDay() string   { return w.End.Format("2006-01-02") }

// Scope is the server-side authorization context for the aggregation. The
// store layer fail-closes on it; it never comes from request input.
type Scope struct {
	TenantID string
	// StoreScope: "" = 全门店(总部/租户级);非空 = 仅该门店(门店经理)。
	StoreScope string
}

// Definition is the mandatory per-metric provenance block (票面要求:每项有
// 事件来源、去重键、分母和统计窗口)。
type Definition struct {
	Source      string `json:"source"`
	DedupKey    string `json:"dedup_key"`
	Denominator string `json:"denominator"`
	EventTime   string `json:"event_time"`
	WindowNote  string `json:"window"`
}

// Bucket is one drill-down value. Key is the dimension id (or "" for
// 租户级/未绑定); the response's dimensions block carries the reference rows
// the keys point into (来源引用贯穿).
type Bucket struct {
	Key   string `json:"key"`
	Value int64  `json:"value"`
}

// Metric is one dashboard line. Value is a pointer so an UNKNOWN metric
// serializes as an ABSENT/null value — never a fabricated 0.
type Metric struct {
	Key       string `json:"key"`
	Title     string `json:"title"`
	Available bool   `json:"available"`
	Value     *int64 `json:"value"`
	// UniqueContacts is the deduplicated contact denominator of
	// lead_submissions (分母 = 唯一联系人指纹去重). Present on that metric only.
	UniqueContacts *int64              `json:"unique_contacts,omitempty"`
	Reason         string              `json:"reason,omitempty"`
	Upstream       []string            `json:"upstream,omitempty"`
	Definition     Definition          `json:"definition"`
	Breakdowns     map[string][]Bucket `json:"breakdowns,omitempty"`
}

// Response is the GET /api/v1/dashboard payload.
type Response struct {
	Window struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"window"`
	Scope struct {
		TenantID   string `json:"tenant_id"`
		StoreScope string `json:"store_scope,omitempty"`
	} `json:"scope"`
	Metrics    []Metric                  `json:"metrics"`
	Dimensions store.DashboardDimensions `json:"dimensions"`
}

// unknownDefs: the external-platform metrics this ticket must name but cannot
// measure. Each entry fixes the honest wording + upstream ticket references.
var unknownDefs = map[string]struct {
	title    string
	reason   string
	upstream []string
}{
	KeyPublishedVideos: {
		title: "发布视频数",
		reason: "touch 域尚无可核实的发布事实来源:发布以 HUI-1670(一键预览与发布)的可核实结果为准," +
			"该上游未接入;导出/预览/唤起编辑器/自报均不视为发布成功。保持 UNKNOWN,不展示 0、不推算。",
		upstream: []string{"HUI-1670"},
	},
	KeyUGCPlays: {
		title: "UGC 播放",
		reason: "外部平台播放数据无授权来源:HUI-1670/HUI-1680(多渠道对接)未接入平台侧可核实回执前" +
			"拿不到播放数。保持 UNKNOWN,不展示 0、不推算曝光。",
		upstream: []string{"HUI-1670", "HUI-1680"},
	},
	KeyUGCLikes: {
		title: "UGC 点赞",
		reason: "外部平台点赞数据无授权来源:HUI-1670/HUI-1680 未接入平台侧可核实回执前拿不到点赞数。" +
			"保持 UNKNOWN,不展示 0、不推算。",
		upstream: []string{"HUI-1670", "HUI-1680"},
	},
	KeyPOIExposureDelta: {
		title: "POI 曝光增量",
		reason: "POI 曝光是外部平台侧数据,touch 域没有任何该事实的事件源;曝光增量不得由本域数据推算。" +
			"保持 UNKNOWN。",
		upstream: []string{"HUI-1680"},
	},
	KeyCouponRedemptions: {
		title: "优惠券核销",
		reason: "优惠券领取/核销事实归发布奖励域(HUI-1671 未接入,touch 域无核销事件源);" +
			"保持 UNKNOWN,不展示 0、不推算。",
		upstream: []string{"HUI-1671"},
	},
}

// windowNoteSecond and windowNoteDay: the two documented window semantics.
const (
	windowNoteSecond = "事件时间按 UTC 整秒粒度与窗口比较,边界含(晚到事实按事件时间落窗;重算幂等)"
	windowNoteDay    = "按 UTC 日粒度落窗(公共页 beacon 记录到到达日),窗口覆盖到的日期整日计入,边界含;晚到事实落在其到达日"
)

// Catalog returns every metric in fixed order with the touch-domain metrics
// available (values filled by Build) and the external inventory marked UNKNOWN.
func Catalog() []Metric {
	touch := []struct {
		key, title string
		def        Definition
	}{
		{
			key: KeyTouchTriggers, title: "碰/扫码触发次数",
			def: Definition{
				Source: "public_view_stats(匿名浏览 beacon,纯聚合计数)JOIN campaign_links→campaigns(仅本租户自有短码)",
				DedupKey: "无幂等键:beacon 到达即 +1(H5 每次页面加载发一次);channel 列为 beacon 上报值" +
					"(现网 web/wecom)。NFC 触碰与扫码共用同一 canonical 短码 URL,物理入口(nfc/qr)未单独记录,不推算拆分",
				Denominator: "本租户(及门店作用域内)自有活动短码上的全部匿名浏览 beacon;按 (短码,日,渠道) 聚合",
				EventTime:   "public_view_stats.day(beacon 到达日,UTC)",
				WindowNote:  windowNoteDay,
			},
		},
		{
			key: KeyLeadPageVisits, title: "留资访问(启用留资表单的活动页访问)",
			def: Definition{
				Source:      "public_view_stats JOIN campaign_links→campaigns→lead_forms(enabled=1)(仅本租户)",
				DedupKey:    "无幂等键:与 touch_triggers 同源(H5 每次页面加载发一次 beacon,≈一次访问),仅限定挂载了启用留资表单的活动;日粒度事实无法做亚日会话切分,按访问计数口径披露,不自称精确去重会话",
				Denominator: "挂载启用留资表单活动的短码上的全部匿名浏览 beacon",
				EventTime:   "public_view_stats.day(beacon 到达日,UTC)",
				WindowNote:  windowNoteDay,
			},
		},
		{
			key: KeyLeadSubmissions, title: "授权留资提交",
			def: Definition{
				Source:      "lead_submissions(授权源域本地表,与 leads_outbox 同事务原子写入)",
				DedupKey:    "lead_submissions.dedup_key(UNIQUE:活动+手机号指纹+UTC 日桶)——同一联系人在同一活动同一 UTC 日内重复提交幂等返回原行,绝不产生第二条事实;正向计数排除 revoked(撤销事实单独按状态披露)",
				Denominator: "unique_contacts = COUNT(DISTINCT phone)(经 D-X1 归一化的手机号,即域内联系人指纹口径;指纹 HMAC 本身绝不出现在看板输出)",
				EventTime:   "lead_submissions.created_at(=consent_at,提交时刻,UTC)",
				WindowNote:  windowNoteSecond,
			},
		},
		{
			key: KeyCRMReceived, title: "CRM 接收",
			def: Definition{
				Source:      "lead_submissions.sync_state 状态机(directed-event/v1:notify 持久接受→pending_sync,投递回执确认→crm_received;compare-and-set 迁移,重复投递不产生新事实)",
				DedupKey:    "状态而非事件日志:一线索一行,重复投递/重放不新增;按当前真实状态计数,可重算",
				Denominator: "窗口内完成 pending_sync→crm_received 迁移的线索数; rejected/dead-letter 不计入",
				EventTime:   "lead_submissions.updated_at(最近一次真实状态迁移时刻,UTC)",
				WindowNote:  windowNoteSecond,
			},
		},
	}
	out := make([]Metric, 0, len(touch)+len(unknownDefs))
	for _, t := range touch {
		out = append(out, Metric{
			Key: t.key, Title: t.title, Available: true, Definition: t.def,
		})
	}
	for _, key := range []string{KeyPublishedVideos, KeyUGCPlays, KeyUGCLikes, KeyPOIExposureDelta, KeyCouponRedemptions} {
		u := unknownDefs[key]
		out = append(out, Metric{
			Key: key, Title: u.title, Available: false, Value: nil,
			Reason: u.reason, Upstream: u.upstream,
			Definition: Definition{
				Source:   "无(touch 域没有该事实的事件源,上游未接入)",
				DedupKey: "-", Denominator: "-",
				EventTime:  "-",
				WindowNote: "不适用:无来源即无窗口口径;接入上游后按其事实时间定义",
			},
		})
	}
	return out
}

func ptr(v int64) *int64 { return &v }

// Build assembles the response: touch-domain metrics take the store-computed
// facts, the external inventory stays UNKNOWN no matter what the facts say.
// Pure function of (window, scope, facts): same inputs → byte-identical output.
func Build(w Window, sc Scope, f store.DashboardFacts, dims store.DashboardDimensions) Response {
	var resp Response
	resp.Window.Start, resp.Window.End = w.Start.Format(time.RFC3339), w.End.Format(time.RFC3339)
	resp.Scope.TenantID, resp.Scope.StoreScope = sc.TenantID, sc.StoreScope
	resp.Dimensions = dims

	for _, base := range Catalog() {
		m := base
		switch m.Key {
		case KeyTouchTriggers:
			m.Value = ptr(f.TriggerTotal)
			m.Breakdowns = map[string][]Bucket{
				"by_channel":  buckets(f.TriggerByChannel),
				"by_campaign": buckets(f.TriggerByCampaign),
				"by_store":    buckets(f.TriggerByStore),
				"by_tag":      buckets(f.TriggerByTag),
			}
		case KeyLeadPageVisits:
			m.Value = ptr(f.LeadVisitTotal)
			m.Breakdowns = map[string][]Bucket{
				"by_campaign": buckets(f.LeadVisitByCampaign),
				"by_store":    buckets(f.LeadVisitByStore),
			}
		case KeyLeadSubmissions:
			m.Value = ptr(f.SubmissionTotal)
			m.UniqueContacts = ptr(f.SubmissionUniqueContacts)
			m.Breakdowns = map[string][]Bucket{
				"by_campaign": buckets(f.SubmissionByCampaign),
				"by_store":    buckets(f.SubmissionByStore),
				"by_state":    buckets(f.SubmissionByState),
			}
		case KeyCRMReceived:
			m.Value = ptr(f.CRMReceivedTotal)
			m.Breakdowns = map[string][]Bucket{
				"by_campaign": buckets(f.CRMReceivedByCampaign),
				"by_store":    buckets(f.CRMReceivedByStore),
			}
		}
		resp.Metrics = append(resp.Metrics, m)
	}
	return resp
}

func buckets(in []store.DimBucket) []Bucket {
	out := make([]Bucket, 0, len(in))
	for _, b := range in {
		out = append(out, Bucket{Key: b.Key, Value: b.Value})
	}
	return out
}
