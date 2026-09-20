package store

// store_dashboard_test.go: HUI-1677 FEAT-0178 数据统计看板存储层验收。
//
// 方法:已知事件集播种 → 手工推导期望值(独立复算)+ 独立 SQL 交叉核对 →
// 与 DashboardFacts 输出逐项一致。覆盖:
//   - 窗口语义(秒粒度/日粒度,边界含,纳秒精度行,窗外语义确定);
//   - 去重分母(唯一联系人指纹口径)、撤销不计正向、全状态披露;
//   - CRM 接收按真实状态机状态计(重复投递不新增是状态机的结构性质);
//   - 租户隔离(跨租户行绝不进聚合)与门店作用域(store_manager);
//   - 维度下钻 channel/campaign/store/tag 与来源引用。

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

// ---- 已知事件集播种 ---------------------------------------------------------------

type dashSeed struct {
	s      *Store
	tenA   string
	tenB   string
	s1ID   string
	s2ID   string
	c1     string // store s1, form enabled
	c2     string // unbound, form enabled
	c3     string // store s2, NO form
	code1  string
	code2  string
	code3  string
	tag1ID string
	tag2ID string
}

// Fixed window: [2026-09-15T00:00:00Z, 2026-09-16T12:00:00Z].
var (
	dashStart = time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	dashEnd   = time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
)

func seedDashboardFacts(t *testing.T) *dashSeed {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := New(d)

	tenA, err := st.CreateTenant("商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("商家B")
	if err != nil {
		t.Fatal(err)
	}
	s1, err := st.CreateStore(tenA.ID, "旗舰店", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := st.CreateStore(tenA.ID, "分店", "", "test")
	if err != nil {
		t.Fatal(err)
	}
	mkCampaign := func(title, storeID string) string {
		c, err := st.CreateCampaign(NewCampaign{TenantID: tenA.ID, Title: title, StoreID: storeID, CreatedBy: "test"})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	c1 := mkCampaign("活动一", s1.ID)
	c2 := mkCampaign("活动二", "")
	c3 := mkCampaign("活动三", s2.ID)
	mkLink := func(tenantID, campaignID string) (string, string) {
		l, err := st.CreateLink(tenantID, campaignID, "test")
		if err != nil {
			t.Fatal(err)
		}
		return l.ID, l.Code
	}
	l1ID, code1 := mkLink(tenA.ID, c1)
	l2ID, code2 := mkLink(tenA.ID, c2)
	_, code3 := mkLink(tenA.ID, c3)

	g, err := st.CreateTagGroup(tenA.ID, "门店物料", "test")
	if err != nil {
		t.Fatal(err)
	}
	tags, err := st.CreateTagsBatch(NewTagBatch{TenantID: tenA.ID, CampaignID: c1, LinkIDs: []string{l1ID},
		BindMode: "shared", Count: 1, GroupID: g.ID, StoreID: s1.ID, LabelPrefix: "入口A", CreatedBy: "test"})
	if err != nil || len(tags) != 1 {
		t.Fatalf("tag1: %v", err)
	}
	tag1ID := tags[0].ID
	// tag2 绑定 c2 的既有短码(该 code 上有窗内触发事实)。
	tags, err = st.CreateTagsBatch(NewTagBatch{TenantID: tenA.ID, CampaignID: c2, LinkIDs: []string{l2ID},
		BindMode: "shared", Count: 1, StoreID: s2.ID, LabelPrefix: "入口B", CreatedBy: "test"})
	if err != nil || len(tags) != 1 {
		t.Fatalf("tag2: %v", err)
	}
	tag2ID := tags[0].ID

	// lead forms: c1/c2 enabled, c3 none.
	if _, err := st.UpsertLeadForm(tenA.ID, c1, "v1", true, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertLeadForm(tenA.ID, c2, "v1", true, "test"); err != nil {
		t.Fatal(err)
	}

	// 触发事实(纯聚合计数;手工求和为期望值):
	//   code1: 09-14 web 3(窗外) | 09-15 web 2 | 09-15 wecom 1 | 09-16 wecom 4 | 09-17 web 9(窗外)
	//   code2: 09-15 web 5
	//   code3: 09-15 web 7
	view := func(code, day, channel string, n int) {
		for i := 0; i < n; i++ {
			if err := st.IncrementViewStat(code, day, channel); err != nil {
				t.Fatal(err)
			}
		}
	}
	view(code1, "2026-09-14", "web", 3)
	view(code1, "2026-09-15", "web", 2)
	view(code1, "2026-09-15", "wecom", 1)
	view(code1, "2026-09-16", "wecom", 4)
	view(code1, "2026-09-17", "web", 9)
	view(code2, "2026-09-15", "web", 5)
	view(code3, "2026-09-15", "web", 7)

	// 留资事实(直接播种以控制事件时间;已知事件集):
	//   窗内正向: sub2(边界起点) sub3(纳秒) sub4(边界终点) sub5(终点秒内) sub8(rejected 计正向)
	//   窗内但撤销: sub7(revoked 不计正向,按状态披露)
	//   窗外: sub1(起点前 1s) sub6(终点后 1s)
	//   CRM 接收: sub3(窗内迁移) sub9(窗外迁移) sub10(c2,窗内)
	seed := func(ref, campaign, storeID, phone, state, createdAt, updatedAt string) {
		if err := st.submitLeadRaw(tenA.ID, campaign, storeID, ref, phone, state, createdAt, updatedAt); err != nil {
			t.Fatalf("seed %s: %v", ref, err)
		}
	}
	seed("sub1", c1, s1.ID, "13800000001", "accepted", "2026-09-14T23:59:59Z", "2026-09-14T23:59:59Z")
	seed("sub2", c1, s1.ID, "13811112222", "accepted", "2026-09-15T00:00:00Z", "2026-09-15T00:00:00Z")
	seed("sub3", c1, s1.ID, "13833334444", "crm_received", "2026-09-15T10:00:00.123456789Z", "2026-09-15T11:00:00Z")
	seed("sub4", c1, s1.ID, "13811112222", "pending_sync", "2026-09-16T12:00:00Z", "2026-09-16T12:00:00Z")
	seed("sub5", c1, s1.ID, "13855556666", "accepted", "2026-09-16T12:00:00.000000001Z", "2026-09-16T12:00:00.000000001Z")
	seed("sub6", c1, s1.ID, "13899990000", "accepted", "2026-09-16T12:00:01Z", "2026-09-16T12:00:01Z")
	seed("sub7", c1, s1.ID, "13877770000", "revoked", "2026-09-15T08:00:00Z", "2026-09-15T08:30:00Z")
	// sub8:store_id 列故意填陈旧值,证明聚合按 campaigns.store_id 当前绑定(与 authz 一致)
	seed("sub8", c1, "sto_stale_value", "13877778888", "rejected", "2026-09-15T09:00:00Z", "2026-09-15T09:00:00Z")
	seed("sub9", c1, s1.ID, "13822223333", "crm_received", "2026-09-13T10:00:00Z", "2026-09-14T10:00:00Z")
	seed("sub10", c2, "", "13844445555", "crm_received", "2026-09-15T05:00:00Z", "2026-09-15T06:00:00Z")

	// 租户 B(隔离对照):自己的活动/短码/事实。
	cBCamp, err := st.CreateCampaign(NewCampaign{TenantID: tenB.ID, Title: "B活动", CreatedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	cB := cBCamp.ID
	_, codeB := mkLink(tenB.ID, cB)
	view(codeB, "2026-09-15", "web", 99)
	if err := st.submitLeadRaw(tenB.ID, cB, "", "subB", "13900000000", "accepted", "2026-09-15T10:00:00Z", "2026-09-15T10:00:00Z"); err != nil {
		t.Fatal(err)
	}

	return &dashSeed{s: st, tenA: tenA.ID, tenB: tenB.ID, s1ID: s1.ID, s2ID: s2.ID,
		c1: c1, c2: c2, c3: c3, code1: code1, code2: code2, code3: code3,
		tag1ID: tag1ID, tag2ID: tag2ID}
}

// submitLeadRaw inserts one lead_submissions row with explicit event times
// (test-only seeding of a known event set).
func (s *Store) submitLeadRaw(tenantID, campaignID, storeID, ref, phone, state, createdAt, updatedAt string) error {
	_, err := s.DB.Exec(
		`INSERT INTO lead_submissions(id,tenant_id,campaign_id,store_id,link_id,form_id,submission_ref,dedup_key,
		   name,phone,wechat,marketing_optin,consent_notice_version,consent_at,consent_ip_fp,sync_state,sync_error,
		   attempts,source_version,revoked_at,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,0,1,NULL,?,?)`,
		"lead_"+ref, tenantID, campaignID, storeID, "", "", ref, "dk_"+ref,
		"测试", phone, "", 0, "v1", createdAt, "", state, "",
		createdAt, updatedAt)
	return err
}

// ---- assertion helpers ---------------------------------------------------------

func bucketMap(bs []DimBucket) map[string]int64 {
	m := map[string]int64{}
	for _, b := range bs {
		m[b.Key] = b.Value
	}
	return m
}

func wantBuckets(t *testing.T, what string, got []DimBucket, want map[string]int64) {
	t.Helper()
	gotM := bucketMap(got)
	if len(gotM) != len(want) {
		t.Fatalf("%s: got %v (%d buckets), want %v (%d)", what, got, len(gotM), want, len(want))
	}
	for k, v := range want {
		if gotM[k] != v {
			t.Fatalf("%s[%q] = %d, want %d (all: %v)", what, k, gotM[k], v, got)
		}
	}
	// deterministic ordering: keys sorted ascending ("" first)
	for i := 1; i < len(got); i++ {
		if got[i-1].Key > got[i].Key {
			t.Fatalf("%s: buckets not key-sorted: %v", what, got)
		}
	}
}

// ---- 1. 已知事件集逐项复算(租户全域) ---------------------------------------------

func TestDashboardFactsKnownEventSet(t *testing.T) {
	f := seedDashboardFacts(t)

	// 独立复算:测试用独立 SQL 从已知事件集重推触发总数(与实现的 join 路径无关)。
	var indepTriggers int64
	if err := f.s.DB.QueryRow(
		`SELECT COALESCE(SUM(views),0) FROM public_view_stats
		 WHERE code IN (?,?,?) AND day >= '2026-09-15' AND day <= '2026-09-16'`,
		f.code1, f.code2, f.code3).Scan(&indepTriggers); err != nil {
		t.Fatal(err)
	}
	if indepTriggers != 19 {
		t.Fatalf("independent SQL recompute = %d, want 19 (event set mismatch)", indepTriggers)
	}

	facts, dims, err := f.s.DashboardFacts(f.tenA, "", dashStart, dashEnd)
	if err != nil {
		t.Fatal(err)
	}

	// 手工期望(已知事件集):
	//   触发: code1 窗内 2+1+4=7, code2 5, code3 7 → 19
	//   留资访问(c1/c2 启用表单): 7+5=12(c3 无表单排除)
	//   提交正向(排除 revoked;created_at 落窗): sub2/3/4/5/8/sub10 = 6
	//     唯一联系人: ...2222,...3333(实为 ...4444 编号见表),...6666,...8888,...5555 = 5
	//   CRM 接收(updated_at 窗内且状态真实): sub3, sub10 = 2
	if facts.TriggerTotal != 19 {
		t.Fatalf("TriggerTotal = %d, want 19", facts.TriggerTotal)
	}
	wantBuckets(t, "TriggerByChannel", facts.TriggerByChannel, map[string]int64{"web": 14, "wecom": 5})
	wantBuckets(t, "TriggerByCampaign", facts.TriggerByCampaign, map[string]int64{f.c1: 7, f.c2: 5, f.c3: 7})
	wantBuckets(t, "TriggerByStore", facts.TriggerByStore, map[string]int64{"": 5, f.s1ID: 7, f.s2ID: 7})
	wantBuckets(t, "TriggerByTag", facts.TriggerByTag, map[string]int64{f.tag1ID: 7, f.tag2ID: 5})

	if facts.LeadVisitTotal != 12 {
		t.Fatalf("LeadVisitTotal = %d, want 12", facts.LeadVisitTotal)
	}
	wantBuckets(t, "LeadVisitByCampaign", facts.LeadVisitByCampaign, map[string]int64{f.c1: 7, f.c2: 5})
	wantBuckets(t, "LeadVisitByStore", facts.LeadVisitByStore, map[string]int64{"": 5, f.s1ID: 7})

	if facts.SubmissionTotal != 6 {
		t.Fatalf("SubmissionTotal = %d, want 6 (revoked excluded, rejected included)", facts.SubmissionTotal)
	}
	if facts.SubmissionUniqueContacts != 5 {
		t.Fatalf("SubmissionUniqueContacts = %d, want 5", facts.SubmissionUniqueContacts)
	}
	wantBuckets(t, "SubmissionByCampaign", facts.SubmissionByCampaign, map[string]int64{f.c1: 5, f.c2: 1})
	wantBuckets(t, "SubmissionByStore", facts.SubmissionByStore, map[string]int64{"": 1, f.s1ID: 5})
	wantBuckets(t, "SubmissionByState", facts.SubmissionByState, map[string]int64{
		"accepted": 2, "pending_sync": 1, "crm_received": 2, "rejected": 1, "revoked": 1})

	if facts.CRMReceivedTotal != 2 {
		t.Fatalf("CRMReceivedTotal = %d, want 2 (sub3 in-window transition, sub9 out-of-window)", facts.CRMReceivedTotal)
	}
	wantBuckets(t, "CRMReceivedByCampaign", facts.CRMReceivedByCampaign, map[string]int64{f.c1: 1, f.c2: 1})
	wantBuckets(t, "CRMReceivedByStore", facts.CRMReceivedByStore, map[string]int64{"": 1, f.s1ID: 1})

	// 维度来源引用。
	if len(dims.Campaigns) != 3 || len(dims.Stores) != 2 || len(dims.Tags) != 2 {
		t.Fatalf("dimensions = %+v", dims)
	}
	campByID := map[string]DashboardCampaignRef{}
	for _, c := range dims.Campaigns {
		campByID[c.ID] = c
	}
	if campByID[f.c1].StoreID != f.s1ID || campByID[f.c2].StoreID != "" {
		t.Fatalf("campaign refs store binding wrong: %+v", dims.Campaigns)
	}
}

// ---- 2. 门店作用域(store_manager):只见本店,租户级/未绑定不可见 -------------------

func TestDashboardFactsStoreScope(t *testing.T) {
	f := seedDashboardFacts(t)
	facts, dims, err := f.s.DashboardFacts(f.tenA, f.s1ID, dashStart, dashEnd)
	if err != nil {
		t.Fatal(err)
	}
	// 只有 c1(绑定 s1)的事实进入聚合;c2(未绑定,租户级)与 c3(s2)不可见。
	if facts.TriggerTotal != 7 {
		t.Fatalf("scoped TriggerTotal = %d, want 7", facts.TriggerTotal)
	}
	wantBuckets(t, "scoped TriggerByChannel", facts.TriggerByChannel, map[string]int64{"web": 2, "wecom": 5})
	wantBuckets(t, "scoped TriggerByCampaign", facts.TriggerByCampaign, map[string]int64{f.c1: 7})
	wantBuckets(t, "scoped TriggerByTag", facts.TriggerByTag, map[string]int64{f.tag1ID: 7})

	if facts.LeadVisitTotal != 7 {
		t.Fatalf("scoped LeadVisitTotal = %d, want 7", facts.LeadVisitTotal)
	}
	// 提交:c1 行=sub2/3/4/5(4)+sub8 rejected(1),sub7 revoked 排除 → 5;CRM 接收仅 sub3。
	if facts.SubmissionTotal != 5 || facts.SubmissionUniqueContacts != 4 {
		t.Fatalf("scoped submissions = %d/%d, want 5/4", facts.SubmissionTotal, facts.SubmissionUniqueContacts)
	}
	if facts.CRMReceivedTotal != 1 {
		t.Fatalf("scoped CRMReceivedTotal = %d, want 1", facts.CRMReceivedTotal)
	}
	// 维度收窄到本店。
	if len(dims.Campaigns) != 1 || dims.Campaigns[0].ID != f.c1 {
		t.Fatalf("scoped campaign dims = %+v", dims.Campaigns)
	}
	if len(dims.Stores) != 1 || dims.Stores[0].ID != f.s1ID {
		t.Fatalf("scoped store dims = %+v", dims.Stores)
	}
	if len(dims.Tags) != 1 || dims.Tags[0].ID != f.tag1ID {
		t.Fatalf("scoped tag dims = %+v", dims.Tags)
	}
}

// ---- 3. 跨租户隔离:对方租户的事实绝不进聚合 ---------------------------------------

func TestDashboardFactsCrossTenant(t *testing.T) {
	f := seedDashboardFacts(t)
	// B 只见 B 自己的 99 次触发与 1 条提交,A 的事实不出现。
	bf, bdim, err := f.s.DashboardFacts(f.tenB, "", dashStart, dashEnd)
	if err != nil {
		t.Fatal(err)
	}
	if bf.TriggerTotal != 99 || bf.SubmissionTotal != 1 {
		t.Fatalf("tenant B facts = triggers %d submissions %d, want 99/1", bf.TriggerTotal, bf.SubmissionTotal)
	}
	if len(bf.TriggerByCampaign) != 1 || bf.TriggerByCampaign[0].Key == f.c1 {
		t.Fatalf("tenant B campaign breakdown leaked tenant A: %v", bf.TriggerByCampaign)
	}
	if len(bdim.Campaigns) != 1 {
		t.Fatalf("tenant B dimensions leaked: %+v", bdim)
	}
	// A 的期望值不因 B 的 99 次而膨胀(已知事件集钉死 19/5)。
	af, _, err := f.s.DashboardFacts(f.tenA, "", dashStart, dashEnd)
	if err != nil {
		t.Fatal(err)
	}
	if af.TriggerTotal != 19 || af.SubmissionTotal != 6 {
		t.Fatalf("tenant A facts polluted: %d/%d", af.TriggerTotal, af.SubmissionTotal)
	}
}

// ---- 4. 无事实作用域:全零(真 0,不是 UNKNOWN) ------------------------------------

func TestDashboardFactsEmptyScope(t *testing.T) {
	f := seedDashboardFacts(t)
	facts, dims, err := f.s.DashboardFacts("tnt_missing", "", dashStart, dashEnd)
	if err != nil {
		t.Fatal(err)
	}
	if facts.TriggerTotal != 0 || facts.SubmissionTotal != 0 || facts.CRMReceivedTotal != 0 || facts.LeadVisitTotal != 0 {
		t.Fatalf("unknown tenant must aggregate to true zeros: %+v", facts)
	}
	if len(dims.Campaigns) != 0 || len(dims.Tags) != 0 {
		t.Fatalf("dimensions must be empty: %+v", dims)
	}
}
