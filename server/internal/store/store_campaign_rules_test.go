package store

// store_campaign_rules_test.go: HUI-1676 FEAT-0177 活动规则持久层验收。
//
// 覆盖:
//   - CRUD + 版本化留痕:创建=version1/revision1(create);更新不静默改写,
//     落 revision 快照(action=update)且可查历史版本;删除落 delete 修订;
//   - 校验矩阵防线:非法(负数)规则集在存储层同样被拒;
//   - 租户隔离:跨租户读写一律 ErrNotFound;未配置活动 ErrNotFound;
//   - 活动必须真实存在(挂在既有 campaign 维度上)。

import (
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignrules"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func openRulesStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func rulesFixture(t *testing.T, s *Store) (tenantA, tenantB, campA, campB string) {
	t.Helper()
	ta, err := s.CreateTenant("商家A")
	must(t, err)
	tb, err := s.CreateTenant("商家B")
	must(t, err)
	ownA, err := s.CreateMember(ta.ID, "usr_owner_a", "org_owner", "A老板", "seed", true)
	must(t, err)
	ownB, err := s.CreateMember(tb.ID, "usr_owner_b", "org_owner", "B老板", "seed", true)
	must(t, err)
	cA, err := s.CreateCampaign(NewCampaign{TenantID: ta.ID, Title: "A的活动", CreatedBy: ownA.PrincipalRef})
	must(t, err)
	cB, err := s.CreateCampaign(NewCampaign{TenantID: tb.ID, Title: "B的活动", CreatedBy: ownB.PrincipalRef})
	must(t, err)
	return ta.ID, tb.ID, cA.ID, cB.ID
}

func sampleRules() campaignrules.Ruleset {
	return campaignrules.Ruleset{
		RewardThreshold:              intPtr(3),
		DailyPublishLimit:            intPtr(50),
		DuplicatePublishWindowHours:  intPtr(72),
		PerContactDailySubmissionCap: intPtr(1),
	}
}

func intPtr(n int) *int { return &n }

func assertRuleset(t *testing.T, got campaignrules.Ruleset, want campaignrules.Ruleset) {
	t.Helper()
	check := func(name string, g, w *int) {
		switch {
		case g == nil && w == nil:
		case g == nil || w == nil:
			t.Fatalf("%s: nil mismatch got=%v want=%v", name, g, w)
		case *g != *w:
			t.Fatalf("%s: got %d want %d", name, *g, *w)
		}
	}
	check("reward_threshold", got.RewardThreshold, want.RewardThreshold)
	check("daily_publish_limit", got.DailyPublishLimit, want.DailyPublishLimit)
	check("duplicate_publish_window_hours", got.DuplicatePublishWindowHours, want.DuplicatePublishWindowHours)
	check("per_contact_daily_submission_cap", got.PerContactDailySubmissionCap, want.PerContactDailySubmissionCap)
}

func TestCampaignRulesCreateWritesFirstRevision(t *testing.T) {
	s := openRulesStore(t)
	tenA, _, campA, _ := rulesFixture(t, s)

	rec, err := s.UpsertCampaignRules(tenA, campA, sampleRules(), "usr_owner_a")
	must(t, err)
	if rec.Version != 1 {
		t.Fatalf("first upsert version = %d, want 1", rec.Version)
	}
	if rec.TenantID != tenA || rec.CampaignID != campA {
		t.Fatalf("record tenancy mismatch: %+v", rec)
	}
	assertRuleset(t, rec.Ruleset, sampleRules())

	revs, err := s.ListCampaignRuleRevisions(tenA, campA)
	must(t, err)
	if len(revs) != 1 || revs[0].Revision != 1 || revs[0].Action != "create" {
		t.Fatalf("revisions after create = %+v, want [1:create]", revs)
	}
	assertRuleset(t, revs[0].Ruleset, sampleRules())
	if revs[0].ChangedBy != "usr_owner_a" {
		t.Fatalf("revision changed_by = %q", revs[0].ChangedBy)
	}
}

func TestCampaignRulesUpdateKeepsVersionHistory(t *testing.T) {
	s := openRulesStore(t)
	tenA, _, campA, _ := rulesFixture(t, s)

	if _, err := s.UpsertCampaignRules(tenA, campA, sampleRules(), "usr_owner_a"); err != nil {
		t.Fatal(err)
	}
	changed := sampleRules()
	changed.PerContactDailySubmissionCap = intPtr(2)
	changed.DuplicatePublishWindowHours = nil // 也允许收回某条规则(nil=不限)
	rec, err := s.UpsertCampaignRules(tenA, campA, changed, "usr_owner_a")
	must(t, err)
	if rec.Version != 2 {
		t.Fatalf("second upsert version = %d, want 2 (变更必须走版本化)", rec.Version)
	}
	assertRuleset(t, rec.Ruleset, changed)

	// 历史版本可查:两个修订都保留各自快照
	revs, err := s.ListCampaignRuleRevisions(tenA, campA)
	must(t, err)
	if len(revs) != 2 {
		t.Fatalf("revisions = %d, want 2", len(revs))
	}
	if revs[0].Action != "create" || revs[1].Action != "update" || revs[1].Revision != 2 {
		t.Fatalf("revision trail = %+v", revs)
	}
	if revs[0].Ruleset.PerContactDailySubmissionCap == nil || *revs[0].Ruleset.PerContactDailySubmissionCap != 1 {
		t.Fatalf("revision 1 snapshot must keep cap=1, got %+v", revs[0].Ruleset)
	}
	if revs[1].Ruleset.PerContactDailySubmissionCap == nil || *revs[1].Ruleset.PerContactDailySubmissionCap != 2 {
		t.Fatalf("revision 2 snapshot must keep cap=2, got %+v", revs[1].Ruleset)
	}
	if revs[1].Ruleset.DuplicatePublishWindowHours != nil {
		t.Fatalf("revision 2 snapshot must record nil window, got %+v", revs[1].Ruleset)
	}
}

func TestCampaignRulesDeleteRecordsRevisionAndClears(t *testing.T) {
	s := openRulesStore(t)
	tenA, _, campA, _ := rulesFixture(t, s)

	if _, err := s.UpsertCampaignRules(tenA, campA, sampleRules(), "usr_owner_a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCampaignRules(tenA, campA, "usr_owner_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCampaignRules(tenA, campA); err != ErrNotFound {
		t.Fatalf("after delete GetCampaignRules = %v, want ErrNotFound", err)
	}
	revs, err := s.ListCampaignRuleRevisions(tenA, campA)
	must(t, err)
	if len(revs) != 2 || revs[1].Action != "delete" || revs[1].Revision != 2 {
		t.Fatalf("revisions after delete = %+v, want [1:create 2:delete]", revs)
	}
	// delete 修订的快照 = 全 nil(清除后的状态)
	assertRuleset(t, revs[1].Ruleset, campaignrules.Ruleset{})

	// 删除不存在 → ErrNotFound(无规则可删)
	if err := s.DeleteCampaignRules(tenA, campA, "usr_owner_a"); err != ErrNotFound {
		t.Fatalf("delete absent rules = %v, want ErrNotFound", err)
	}
}

func TestCampaignRulesRejectsInvalidRuleset(t *testing.T) {
	s := openRulesStore(t)
	tenA, _, campA, _ := rulesFixture(t, s)

	bad := sampleRules()
	bad.RewardThreshold = intPtr(-1)
	if _, err := s.UpsertCampaignRules(tenA, campA, bad, "usr_owner_a"); err == nil {
		t.Fatalf("negative threshold must be refused at the store layer")
	}
	// 被拒后不得留下半条记录/修订
	if _, err := s.GetCampaignRules(tenA, campA); err != ErrNotFound {
		t.Fatalf("refused upsert must leave nothing behind: %v", err)
	}
}

func TestCampaignRulesUnknownCampaignRefused(t *testing.T) {
	s := openRulesStore(t)
	tenA, _, campA, _ := rulesFixture(t, s)

	if _, err := s.UpsertCampaignRules(tenA, "cmp_missing", sampleRules(), "usr_owner_a"); err != ErrNotFound {
		t.Fatalf("rules on unknown campaign = %v, want ErrNotFound", err)
	}
	_ = campA
}

func TestCampaignRulesCrossTenantInvisible(t *testing.T) {
	s := openRulesStore(t)
	tenA, tenB, campA, _ := rulesFixture(t, s)

	if _, err := s.UpsertCampaignRules(tenA, campA, sampleRules(), "usr_owner_a"); err != nil {
		t.Fatal(err)
	}
	// B 租户读 A 的活动规则:不存在(而不是泄露)
	if _, err := s.GetCampaignRules(tenB, campA); err != ErrNotFound {
		t.Fatalf("cross-tenant read = %v, want ErrNotFound", err)
	}
	// B 租户以自己的身份改 A 的规则:拒绝
	if _, err := s.UpsertCampaignRules(tenB, campA, sampleRules(), "usr_owner_b"); err != ErrNotFound {
		t.Fatalf("cross-tenant upsert = %v, want ErrNotFound", err)
	}
	// B 列修订:同样不可见
	if revs, err := s.ListCampaignRuleRevisions(tenB, campA); err != nil || len(revs) != 0 {
		t.Fatalf("cross-tenant revisions = %v/%d, want empty", err, len(revs))
	}
}

func TestGetCampaignRulesByCampaignIgnoresTenantParam(t *testing.T) {
	s := openRulesStore(t)
	tenA, _, campA, _ := rulesFixture(t, s)

	if _, err := s.UpsertCampaignRules(tenA, campA, sampleRules(), "usr_owner_a"); err != nil {
		t.Fatal(err)
	}
	// 公共路由口径:campaign 已受控解析,无租户参数(与 GetLeadFormByCampaign 同纪律)
	rec, err := s.GetCampaignRulesByCampaign(campA)
	must(t, err)
	assertRuleset(t, rec.Ruleset, sampleRules())
	if _, err := s.GetCampaignRulesByCampaign("cmp_none"); err != ErrNotFound {
		t.Fatalf("absent campaign rules = %v, want ErrNotFound", err)
	}
}
