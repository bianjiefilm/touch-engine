package campaignrules

// campaignrules_test.go: HUI-1676 FEAT-0177 活动规则确定性评估器验收矩阵。
//
// 覆盖:
//   - Validate 校验矩阵(非负整数;nil = 不限/未设);
//   - Evaluate 矩阵:奖励门槛 / 每日发布上限 / 每联系人每日提交频控 / 重复
//     内容指纹窗,各自独立生效与组合(确定性的拒绝优先级);
//   - 只拒绝不罚款:Decision 只有 Allowed + 机器原因码;
//   - 内容归一化与指纹:空白差异不改变指纹,内容改变必改指纹。

import (
	"strings"
	"testing"
	"time"
)

var baseTime = time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

func ip(n int) *int { return &n }

// ---- Validate ----------------------------------------------------------------

func TestValidateNilRulesetIsUnlimited(t *testing.T) {
	if err := Validate(Ruleset{}); err != nil {
		t.Fatalf("empty ruleset must be valid (all nil = 不限): %v", err)
	}
}

func TestValidateAcceptsZeroAndPositive(t *testing.T) {
	rs := Ruleset{
		RewardThreshold:              ip(0),
		DailyPublishLimit:            ip(0),
		DuplicatePublishWindowHours:  ip(1),
		PerContactDailySubmissionCap: ip(1000000),
	}
	if err := Validate(rs); err != nil {
		t.Fatalf("zero/positive values must be valid: %v", err)
	}
}

func TestValidateRejectsNegative(t *testing.T) {
	for _, tc := range []struct {
		name string
		rs   Ruleset
	}{
		{"reward_threshold", Ruleset{RewardThreshold: ip(-1)}},
		{"daily_publish_limit", Ruleset{DailyPublishLimit: ip(-2)}},
		{"duplicate_publish_window_hours", Ruleset{DuplicatePublishWindowHours: ip(-3)}},
		{"per_contact_daily_submission_cap", Ruleset{PerContactDailySubmissionCap: ip(-4)}},
	} {
		if err := Validate(tc.rs); err == nil {
			t.Fatalf("%s: negative value must be refused", tc.name)
		}
	}
}

// ---- Evaluate: 无规则 = 全放行 --------------------------------------------------

func TestEvaluateNoRulesAllowsEverything(t *testing.T) {
	d := Evaluate(Ruleset{}, Facts{Now: baseTime})
	if !d.Allowed || d.Reason != "" {
		t.Fatalf("no rules must allow: %+v", d)
	}
}

// ---- per_contact_daily_submission_cap -----------------------------------------

func TestEvaluateSubmissionCapIndependent(t *testing.T) {
	rs := Ruleset{PerContactDailySubmissionCap: ip(3)}
	for n := 0; n < 3; n++ {
		d := Evaluate(rs, Facts{Now: baseTime, ContactSubmissionsToday: n})
		if !d.Allowed {
			t.Fatalf("count %d < cap 3 must allow: %+v", n, d)
		}
	}
	d := Evaluate(rs, Facts{Now: baseTime, ContactSubmissionsToday: 3})
	if d.Allowed || d.Reason != "submission_cap_reached" {
		t.Fatalf("count == cap must deny submission_cap_reached: %+v", d)
	}
	d = Evaluate(rs, Facts{Now: baseTime, ContactSubmissionsToday: 99})
	if d.Allowed || d.Reason != "submission_cap_reached" {
		t.Fatalf("count > cap must deny submission_cap_reached: %+v", d)
	}
	// nil = 不限:计数再大也放行
	d = Evaluate(Ruleset{}, Facts{Now: baseTime, ContactSubmissionsToday: 99999})
	if !d.Allowed {
		t.Fatalf("nil cap must not block: %+v", d)
	}
}

// ---- daily_publish_limit --------------------------------------------------------

func TestEvaluateDailyPublishLimitIndependent(t *testing.T) {
	rs := Ruleset{DailyPublishLimit: ip(2)}
	for n := 0; n < 2; n++ {
		d := Evaluate(rs, Facts{Now: baseTime, PublishesToday: n})
		if !d.Allowed {
			t.Fatalf("publish count %d < limit 2 must allow: %+v", n, d)
		}
	}
	d := Evaluate(rs, Facts{Now: baseTime, PublishesToday: 2})
	if d.Allowed || d.Reason != "daily_limit_reached" {
		t.Fatalf("publish count == limit must deny daily_limit_reached: %+v", d)
	}
}

// ---- duplicate_publish_window_hours(内容指纹+窗) ---------------------------------

func TestEvaluateDuplicateWindowIndependent(t *testing.T) {
	rs := Ruleset{DuplicatePublishWindowHours: ip(72)}
	fp := FingerprintContent("到店打卡领好礼")

	// 窗内同指纹 → 拒绝
	prior := []PublishRecord{{At: baseTime.Add(-1 * time.Hour), ContentFingerprint: fp}}
	d := Evaluate(rs, Facts{Now: baseTime, ContentFingerprint: fp, RecentPublishes: prior})
	if d.Allowed || d.Reason != "duplicate_content" {
		t.Fatalf("same fingerprint within window must deny duplicate_content: %+v", d)
	}
	// 窗内不同指纹 → 放行
	priorOther := []PublishRecord{{At: baseTime.Add(-1 * time.Hour), ContentFingerprint: FingerprintContent("别的内容")}}
	d = Evaluate(rs, Facts{Now: baseTime, ContentFingerprint: fp, RecentPublishes: priorOther})
	if !d.Allowed {
		t.Fatalf("different fingerprint within window must allow: %+v", d)
	}
	// 窗外同指纹 → 放行(窗边界:恰好 cutoff 时刻算窗外)
	priorOld := []PublishRecord{{At: baseTime.Add(-72 * time.Hour), ContentFingerprint: fp}}
	d = Evaluate(rs, Facts{Now: baseTime, ContentFingerprint: fp, RecentPublishes: priorOld})
	if !d.Allowed {
		t.Fatalf("same fingerprint exactly at window cutoff must allow (strict window): %+v", d)
	}
	priorJustInside := []PublishRecord{{At: baseTime.Add(-72*time.Hour + time.Second), ContentFingerprint: fp}}
	d = Evaluate(rs, Facts{Now: baseTime, ContentFingerprint: fp, RecentPublishes: priorJustInside})
	if d.Allowed || d.Reason != "duplicate_content" {
		t.Fatalf("same fingerprint 1s inside window must deny: %+v", d)
	}
	// 空指纹(无内容事实)永不构成重复
	d = Evaluate(rs, Facts{Now: baseTime, ContentFingerprint: "", RecentPublishes: prior})
	if !d.Allowed {
		t.Fatalf("empty candidate fingerprint must allow: %+v", d)
	}
}

// ---- reward_threshold ------------------------------------------------------------

func TestEvaluateRewardThresholdIndependent(t *testing.T) {
	rs := Ruleset{RewardThreshold: ip(3)}
	d := Evaluate(rs, Facts{Now: baseTime, ValidCompletions: 2})
	if d.Allowed || d.Reason != "reward_threshold_not_met" {
		t.Fatalf("completions < threshold must deny reward_threshold_not_met: %+v", d)
	}
	d = Evaluate(rs, Facts{Now: baseTime, ValidCompletions: 3})
	if !d.Allowed {
		t.Fatalf("completions == threshold must allow: %+v", d)
	}
	d = Evaluate(Ruleset{}, Facts{Now: baseTime, ValidCompletions: 0})
	if !d.Allowed {
		t.Fatalf("nil threshold must not block: %+v", d)
	}
	// threshold=0 = 无门槛立即达成
	d = Evaluate(Ruleset{RewardThreshold: ip(0)}, Facts{Now: baseTime, ValidCompletions: 0})
	if !d.Allowed {
		t.Fatalf("threshold 0 with 0 completions must allow: %+v", d)
	}
}

// ---- 组合:确定性拒绝优先级 ---------------------------------------------------------

func TestEvaluateCombinationDeterministicPriority(t *testing.T) {
	fp := FingerprintContent("内容X")
	rs := Ruleset{
		PerContactDailySubmissionCap: ip(1),
		DailyPublishLimit:            ip(1),
		DuplicatePublishWindowHours:  ip(24),
		RewardThreshold:              ip(5),
	}
	f := Facts{
		Now:                     baseTime,
		ContactSubmissionsToday: 1, // 频控超限
		PublishesToday:          1, // 每日上限已满
		ContentFingerprint:      fp,
		RecentPublishes:         []PublishRecord{{At: baseTime.Add(-time.Hour), ContentFingerprint: fp}}, // 重复内容
		ValidCompletions:        0,                                                                       // 门槛未达
	}
	// 全部命中时按固定优先级取第一个:submission_cap_reached
	d := Evaluate(rs, f)
	if d.Allowed || d.Reason != "submission_cap_reached" {
		t.Fatalf("combined must deterministically report first blocking rule: %+v", d)
	}
	// 移除频控超限 → daily_limit_reached
	f.ContactSubmissionsToday = 0
	if d = Evaluate(rs, f); d.Allowed || d.Reason != "daily_limit_reached" {
		t.Fatalf("want daily_limit_reached: %+v", d)
	}
	// 再移除发布上限 → duplicate_content
	f.PublishesToday = 0
	if d = Evaluate(rs, f); d.Allowed || d.Reason != "duplicate_content" {
		t.Fatalf("want duplicate_content: %+v", d)
	}
	// 再移除重复 → reward_threshold_not_met
	f.RecentPublishes = nil
	if d = Evaluate(rs, f); d.Allowed || d.Reason != "reward_threshold_not_met" {
		t.Fatalf("want reward_threshold_not_met: %+v", d)
	}
	// 门槛达成 → 全放行
	f.ValidCompletions = 5
	if d = Evaluate(rs, f); !d.Allowed || d.Reason != "" {
		t.Fatalf("all satisfied must allow: %+v", d)
	}
}

// ---- 内容归一化与指纹 ---------------------------------------------------------------

func TestNormalizeContentCollapsesWhitespaceOnly(t *testing.T) {
	a := NormalizeContent("到店打卡 领好礼")
	b := NormalizeContent("  到店打卡\n\t领好礼  ")
	c := NormalizeContent("到店打卡 领好礼 ") // 尾随空格
	if a != b || a != c {
		t.Fatalf("whitespace-only variants must normalize equal: %q %q %q", a, b, c)
	}
	if NormalizeContent("到店打卡领好礼") == a {
		t.Fatalf("dropping a real separator must change normalization")
	}
	if NormalizeContent("") != "" {
		t.Fatalf("empty content normalizes to empty")
	}
}

func TestFingerprintContentDeterministicSHA256(t *testing.T) {
	a := FingerprintContent(NormalizeContent("到店打卡 领好礼"))
	b := FingerprintContent(NormalizeContent("  到店打卡\n\t领好礼 "))
	if a != b {
		t.Fatalf("whitespace variants must fingerprint equal")
	}
	if len(a) != 64 || strings.ToLower(a) != a {
		t.Fatalf("fingerprint must be lowercase hex sha256, got %q", a)
	}
	if a == FingerprintContent(NormalizeContent("到店打卡 领好礼!")) {
		t.Fatalf("content change must change fingerprint")
	}
}
