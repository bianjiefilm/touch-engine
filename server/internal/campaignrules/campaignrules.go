// Package campaignrules implements HUI-1676 FEAT-0177 活动规则配置: the pure,
// deterministic per-campaign ruleset and its evaluator.
//
// 纪律(票面拍板):
//   - 确定性规则,不是 AI 判定:给定规则集 + 事实集,输出唯一确定的放行/拒绝
//     与机器原因码;只拒绝,不罚款,无任何隐式状态;
//   - 上限字段为空(nil)= 不限;非负整数才是合法值;
//   - 服务端单点判定:评估器是纯函数,HTTP/BFF 层零业务判断;
//   - 独立接线状态:per_contact_daily_submission_cap 已真实接线到公共留资
//     提交入口(HUI-1747 surface);daily_publish_limit / reward_threshold /
//     duplicate_publish_window_hours 的上游发布/奖励链路未建(HUI-1670/1671),
//     评估器先行交付并全测,链路落地即接入(deferred,如实声明)。
//
// 原因码表(机器可读,写入拒绝响应的 error 字段):
//
//	submission_cap_reached     每联系人每日提交频控已满
//	daily_limit_reached        每日发布上限已满
//	duplicate_content          重复内容命中去重窗
//	reward_threshold_not_met   奖励门槛未达成
package campaignrules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Ruleset is the per-campaign deterministic configuration. A nil field means
// "not configured / unlimited" for that rule; a non-nil field must be >= 0.
// JSON 形状即管理面的完整契约(omitempty = 未设,呼应「空 = 不限」)。
type Ruleset struct {
	// RewardThreshold: 奖励门槛(有效完成 N 次后达成)。nil = 未设门槛。
	RewardThreshold *int `json:"reward_threshold,omitempty"`
	// DailyPublishLimit: 每日发布上限(UTC 日)。nil = 不限。
	DailyPublishLimit *int `json:"daily_publish_limit,omitempty"`
	// DuplicatePublishWindowHours: 重复发布过滤窗(小时)。nil = 不过滤。
	DuplicatePublishWindowHours *int `json:"duplicate_publish_window_hours,omitempty"`
	// PerContactDailySubmissionCap: 每联系人每日提交频控(UTC 日窗)。
	// nil = 不限。0 = 关闭该活动的首次提交(极端但合法的配置)。
	PerContactDailySubmissionCap *int `json:"per_contact_daily_submission_cap,omitempty"`
}

// Validate enforces the field matrix: every set field must be a non-negative
// integer. nil stays legal for every field (空 = 不限).
func Validate(rs Ruleset) error {
	for _, tc := range []struct {
		name string
		v    *int
	}{
		{"reward_threshold", rs.RewardThreshold},
		{"daily_publish_limit", rs.DailyPublishLimit},
		{"duplicate_publish_window_hours", rs.DuplicatePublishWindowHours},
		{"per_contact_daily_submission_cap", rs.PerContactDailySubmissionCap},
	} {
		if tc.v != nil && *tc.v < 0 {
			return fmt.Errorf("campaignrules: %s must be a non-negative integer, got %d", tc.name, *tc.v)
		}
	}
	return nil
}

// Machine reason codes returned in Decision.Reason when a rule blocks.
const (
	ReasonSubmissionCapReached  = "submission_cap_reached"
	ReasonDailyLimitReached     = "daily_limit_reached"
	ReasonDuplicateContent      = "duplicate_content"
	ReasonRewardThresholdNotMet = "reward_threshold_not_met"
)

// Decision is the deterministic verdict: allow with an empty reason, or deny
// with exactly one machine reason code (the FIRST blocking rule in the fixed
// evaluation order below). 只拒绝,不罚款。
type Decision struct {
	Allowed bool
	Reason  string
}

// PublishRecord is one prior publish fact: when it happened and the content
// fingerprint it carried.
type PublishRecord struct {
	At                 time.Time
	ContentFingerprint string
}

// Facts is the fact set the evaluator consumes. It carries no identity and no
// state of its own — the caller (store/HTTP layer) assembles it from recorded
// rows. The evaluator itself is stateless and pure.
type Facts struct {
	// Now is the evaluation instant (UTC day window for the daily rules).
	Now time.Time
	// ContactSubmissionsToday: 已计入的首次提交次数(计数口径由接线层决定;
	// 幂等重放不占额度,由接线层先短路)。nil 规则不消费此字段。
	ContactSubmissionsToday int
	// PublishesToday: 当前 UTC 日已发布次数。
	PublishesToday int
	// ContentFingerprint: 候选内容的指纹(FingerprintContent 归一化 sha256)。
	ContentFingerprint string
	// RecentPublishes: 去重窗考察范围内的既往发布事实。
	RecentPublishes []PublishRecord
	// ValidCompletions: 已累计的有效完成次数(奖励门槛考察对象)。
	ValidCompletions int
}

// Evaluate applies the ruleset to the fact set. Fixed deterministic order:
// submission cap -> daily publish limit -> duplicate window -> reward threshold.
// The first blocking rule wins; satisfied rules never appear in the reason.
func Evaluate(rs Ruleset, f Facts) Decision {
	if rs.PerContactDailySubmissionCap != nil && f.ContactSubmissionsToday >= *rs.PerContactDailySubmissionCap {
		return Decision{Allowed: false, Reason: ReasonSubmissionCapReached}
	}
	if rs.DailyPublishLimit != nil && f.PublishesToday >= *rs.DailyPublishLimit {
		return Decision{Allowed: false, Reason: ReasonDailyLimitReached}
	}
	if rs.DuplicatePublishWindowHours != nil && f.ContentFingerprint != "" {
		window := time.Duration(*rs.DuplicatePublishWindowHours) * time.Hour
		cutoff := f.Now.Add(-window)
		for _, p := range f.RecentPublishes {
			// 严格窗:cutoff 时刻恰好命中算窗外(已满 N 小时即过期)。
			if p.ContentFingerprint == f.ContentFingerprint && p.At.After(cutoff) {
				return Decision{Allowed: false, Reason: ReasonDuplicateContent}
			}
		}
	}
	if rs.RewardThreshold != nil && f.ValidCompletions < *rs.RewardThreshold {
		return Decision{Allowed: false, Reason: ReasonRewardThresholdNotMet}
	}
	return Decision{Allowed: true}
}

// ---- content fingerprint -------------------------------------------------------

// NormalizeContent canonicalizes free text before fingerprinting: trim the
// edges and collapse every internal Unicode whitespace run to a single space.
// Deliberately case-preserving and locale-free — the only promise is that
// whitespace-only formatting differences cannot produce two fingerprints.
func NormalizeContent(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// FingerprintContent = lowercase hex sha256 of the normalized content. Callers
// must fingerprint NormalizeContent output (this function does not normalize;
// the split keeps the two steps independently testable).
func FingerprintContent(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}
