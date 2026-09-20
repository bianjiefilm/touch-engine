package httpapi

// server_campaign_rules_test.go: HUI-1676 FEAT-0177 活动规则 HTTP 验收矩阵。
//
// 覆盖(票面 TDD 清单):
//   - CRUD 校验矩阵(非法值 400 invalid_rules;未知字段 400 bad_request);
//   - 版本化变更留痕(两次 PUT → 两条修订,历史快照可查);
//   - 频控真实接线(超限 429 带原因码;幂等重放不占额度;跨 UTC 日重置);
//   - off 基线(路由 404 + 直插规则行频控也不生效 = 现行为);
//   - 跨租户 404;authz 仅 org_owner。
//
// 纪律:夹具手机号仅用合成号 139000000xx 序列;响应断言不含任何联系方式。

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignrules"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// ---- fixture -------------------------------------------------------------------

type rulesFixture struct {
	s         *Server
	ts        *httptest.Server
	tenA      string
	tenB      string
	store1ID  string
	camp1ID   string
	camp2ID   string
	camp3ID   string
	code1     string
	code2     string
	code3     string
	forwarder *leads.Forwarder
}

func rulesTestConfig(campaignRulesOn bool) config.Config {
	flag := "off"
	if campaignRulesOn {
		flag = "on"
	}
	return config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_NOTIFY_BASE_URL":
			return "http://notify.test"
		case "PLATFORM_NOTIFY_TOKEN":
			return "notify-token"
		case "LEADS_TARGET_APP_ID":
			return "crm-app"
		case "LEADS_PHONE_PEPPER":
			return "test-pepper"
		case "FEATURE_LEADS_CAPTURE":
			return "on"
		case "FEATURE_CAMPAIGN_RULES":
			return flag
		}
		return ""
	})
}

func newRulesFixture(t *testing.T, campaignRulesOn bool) *rulesFixture {
	t.Helper()
	cfg := rulesTestConfig(campaignRulesOn)

	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("商家B")
	if err != nil {
		t.Fatal(err)
	}
	s1, err := st.CreateStore(tenA.ID, "旗舰店", "地址1", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_sm_a", "store_manager", "sm", "test", true, s1.ID); err != nil {
		t.Fatalf("seed sm: %v", err)
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-sm-a":    {"usr_sm_a", "sm@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: "http://upload.test", Token: "upload-token", AppID: "touch-engine"}

	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	f := &rulesFixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, store1ID: s1.ID,
		forwarder: leads.NewForwarder(s.St, newTestPoster(newNotifyStub(t)), log.New(io.Discard, "", 0))}

	mkCamp := func(title, storeID string) (string, string) {
		body := fmt.Sprintf(`{"title":%q,"public_content":"到店礼","starts_at":"2026-01-01T00:00:00Z","ends_at":"2030-01-01T00:00:00Z"}`, title)
		if storeID != "" {
			body = fmt.Sprintf(`{"title":%q,"public_content":"到店礼","store_id":%q,"starts_at":"2026-01-01T00:00:00Z","ends_at":"2030-01-01T00:00:00Z"}`, title, storeID)
		}
		status, out := f.admin(t, "POST", "/api/v1/campaigns", body)
		if status != 201 {
			t.Fatalf("campaign create %s: %d %v", title, status, out)
		}
		id := out["id"].(string)
		if status, _ := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/status", `{"status":"active"}`); status != 200 {
			t.Fatalf("activate %s failed", title)
		}
		status, link := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/links", ``)
		if status != 201 {
			t.Fatalf("link create %s failed", title)
		}
		if status, _ := f.admin(t, "POST", "/api/v1/campaigns/"+id+"/lead-form", `{"marketing_optin_enabled":false}`); status != 201 {
			t.Fatalf("lead form %s failed", title)
		}
		return id, link["code"].(string)
	}
	f.camp1ID, f.code1 = mkCamp("活动一", s1.ID)
	f.camp2ID, f.code2 = mkCamp("活动二", "")
	f.camp3ID, f.code3 = mkCamp("活动三", "")
	return f
}

func (f *rulesFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("do %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func (f *rulesFixture) admin(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "sess-owner-a", f.tenA, body)
}

func (f *rulesFixture) guest(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "", "", body)
}

func (f *rulesFixture) submit(t *testing.T, code, phone string) (int, map[string]any) {
	t.Helper()
	body := fmt.Sprintf(`{"name":"测试用户","phone":%q,"consent_version":"v1","consent":true}`, phone)
	return f.guest(t, "POST", "/api/v1/public/links/"+code+"/lead-submissions", body)
}

// putRules is the canonical full-replace PUT body builder.
func putRules(reward, daily, window, cap any) string {
	return fmt.Sprintf(`{"reward_threshold":%v,"daily_publish_limit":%v,"duplicate_publish_window_hours":%v,"per_contact_daily_submission_cap":%v}`,
		jsonOrNull(reward), jsonOrNull(daily), jsonOrNull(window), jsonOrNull(cap))
}

func jsonOrNull(v any) string {
	if v == nil {
		return "null"
	}
	return fmt.Sprintf("%v", v)
}

// ---- 1. off 基线:路由不注册 404,频控不生效(现行为锁定) ----------------------------

func TestCampaignRulesOffAtBootRoutesNotRegistered(t *testing.T) {
	f := newRulesFixture(t, false)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/campaigns/" + f.camp1ID + "/rules"},
		{"PUT", "/api/v1/campaigns/" + f.camp1ID + "/rules"},
		{"DELETE", "/api/v1/campaigns/" + f.camp1ID + "/rules"},
		{"GET", "/api/v1/campaigns/" + f.camp1ID + "/rules/revisions"},
	} {
		status, out := f.admin(t, tc.method, tc.path, ``)
		if status != 404 {
			t.Fatalf("%s %s off-boot = %d %v, want 404 (route never registered)", tc.method, tc.path, status, out)
		}
	}
}

func TestCampaignRulesOffFrequencyControlInert(t *testing.T) {
	f := newRulesFixture(t, false)
	// 直接落库一条 cap=1 的规则行:off 时频控必须不生效(现行为逐字节不变)
	capRules := campaignrules.Ruleset{PerContactDailySubmissionCap: rulesIntPtr(1)}
	if _, err := f.s.St.UpsertCampaignRules(f.tenA, f.camp1ID, capRules, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.UpsertCampaignRules(f.tenA, f.camp2ID, capRules, "test"); err != nil {
		t.Fatal(err)
	}
	if st, out := f.submit(t, f.code1, "13900000003"); st != 201 {
		t.Fatalf("off: first submit = %d %v, want 201 (no cap enforced)", st, out)
	}
	if st, out := f.submit(t, f.code2, "13900000003"); st != 201 {
		t.Fatalf("off: second submit = %d %v, want 201 (频控不生效=现行为)", st, out)
	}
}

func rulesIntPtr(n int) *int { return &n }

// ---- 2. CRUD + 校验矩阵 + 版本化 -------------------------------------------------

func TestCampaignRulesPutGetDeleteFlow(t *testing.T) {
	f := newRulesFixture(t, true)

	status, out := f.admin(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/rules", putRules(3, 50, 72, 2))
	if status != 200 || out["version"].(float64) != 1 {
		t.Fatalf("PUT rules = %d %v, want 200 version 1", status, out)
	}
	rs := out["ruleset"].(map[string]any)
	if rs["reward_threshold"].(float64) != 3 || rs["daily_publish_limit"].(float64) != 50 ||
		rs["duplicate_publish_window_hours"].(float64) != 72 || rs["per_contact_daily_submission_cap"].(float64) != 2 {
		t.Fatalf("ruleset round-trip mismatch: %v", rs)
	}

	status, out = f.admin(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/rules", ``)
	if status != 200 || out["version"].(float64) != 1 {
		t.Fatalf("GET rules = %d %v, want 200 version 1", status, out)
	}

	// 变更走版本化:第二次 PUT → version 2,修订表两条快照
	status, out = f.admin(t, "PUT", "/api/v1/campaigns/"+f.camp1ID+"/rules", putRules(nil, 50, 72, 1))
	if status != 200 || out["version"].(float64) != 2 {
		t.Fatalf("second PUT = %d %v, want 200 version 2", status, out)
	}
	status, revs := f.admin(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/rules/revisions", ``)
	if status != 200 || len(revs["items"].([]any)) != 2 {
		t.Fatalf("revisions = %d %v, want 2 items", status, revs)
	}
	r1 := revs["items"].([]any)[0].(map[string]any)
	r2 := revs["items"].([]any)[1].(map[string]any)
	if r1["revision"].(float64) != 1 || r1["action"] != "create" {
		t.Fatalf("revision 1 = %v, want 1:create", r1)
	}
	if r2["revision"].(float64) != 2 || r2["action"] != "update" {
		t.Fatalf("revision 2 = %v, want 2:update", r2)
	}
	// 快照独立:修订1仍记 reward_threshold=3,修订2 已是 null(收回)
	if r1["ruleset"].(map[string]any)["reward_threshold"].(float64) != 3 {
		t.Fatalf("revision 1 snapshot lost reward_threshold: %v", r1)
	}
	if _, present := r2["ruleset"].(map[string]any)["reward_threshold"]; present {
		t.Fatalf("revision 2 snapshot must record cleared reward_threshold as absent: %v", r2)
	}

	// DELETE:清除规则并落 delete 修订
	status, out = f.admin(t, "DELETE", "/api/v1/campaigns/"+f.camp1ID+"/rules", ``)
	if status != 200 {
		t.Fatalf("DELETE = %d %v, want 200", status, out)
	}
	if status, _ := f.admin(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/rules", ``); status != 404 {
		t.Fatalf("GET after DELETE = %d, want 404", status)
	}
	status, revs = f.admin(t, "GET", "/api/v1/campaigns/"+f.camp1ID+"/rules/revisions", ``)
	items := revs["items"].([]any)
	last := items[len(items)-1].(map[string]any)
	if status != 200 || len(items) != 3 || last["action"] != "delete" {
		t.Fatalf("revisions after delete = %d %v, want 3 items ending delete", status, revs)
	}
}

func TestCampaignRulesValidationMatrix(t *testing.T) {
	f := newRulesFixture(t, true)
	url := "/api/v1/campaigns/" + f.camp1ID + "/rules"

	// 负数一律 400 invalid_rules(四个字段逐一)
	for _, body := range []string{
		`{"reward_threshold":-1}`,
		`{"daily_publish_limit":-2}`,
		`{"duplicate_publish_window_hours":-3}`,
		`{"per_contact_daily_submission_cap":-4}`,
	} {
		status, out := f.admin(t, "PUT", url, body)
		if status != 400 || out["error"] != "invalid_rules" {
			t.Fatalf("%s: %d %v, want 400 invalid_rules", body, status, out)
		}
	}
	// 被拒后无残留
	if status, _ := f.admin(t, "GET", url, ``); status != 404 {
		t.Fatalf("refused PUT left residue: GET = %d, want 404", status)
	}
	// 未知字段拒绝(strict schema)
	status, out := f.admin(t, "PUT", url, `{"reward_threshold":1,"target_tenant":"tnt_x"}`)
	if status != 400 || out["error"] != "bad_request" {
		t.Fatalf("unknown field: %d %v, want 400 bad_request", status, out)
	}
	// 全 null 合法(空 = 全部不限)
	status, _ = f.admin(t, "PUT", url, putRules(nil, nil, nil, nil))
	if status != 200 {
		t.Fatalf("all-null ruleset = %d, want 200", status)
	}
	// 0 合法(非负)
	status, _ = f.admin(t, "PUT", url, putRules(0, 0, 0, 0))
	if status != 200 {
		t.Fatalf("zero ruleset = %d, want 200", status)
	}
	// 未配置规则的 GET = 404(活动存在但从未配置)
	if status, _ := f.admin(t, "GET", "/api/v1/campaigns/"+f.camp2ID+"/rules", ``); status != 404 {
		t.Fatalf("unconfigured GET = %d, want 404", status)
	}
}

// ---- 3. authz + 跨租户 -----------------------------------------------------------

func TestCampaignRulesAuthzOwnerOnly(t *testing.T) {
	f := newRulesFixture(t, true)
	url := "/api/v1/campaigns/" + f.camp1ID + "/rules"

	// staff:403(治理面仅 org_owner)
	status, out := f.do(t, "PUT", url, "sess-staff-a", f.tenA, putRules(1, nil, nil, nil))
	if status != 403 || out["error"] != "forbidden" {
		t.Fatalf("staff PUT rules = %d %v, want 403 forbidden", status, out)
	}
	// store_manager:对本店活动也无规则权
	status, out = f.do(t, "PUT", url, "sess-sm-a", f.tenA, putRules(1, nil, nil, nil))
	if status != 403 || out["error"] != "forbidden" {
		t.Fatalf("store_manager PUT rules = %d %v, want 403 forbidden", status, out)
	}
	status, _ = f.do(t, "GET", url, "sess-staff-a", f.tenA, ``)
	if status != 403 {
		t.Fatalf("staff GET rules = %d, want 403", status)
	}
}

func TestCampaignRulesCrossTenant404(t *testing.T) {
	f := newRulesFixture(t, true)
	url := "/api/v1/campaigns/" + f.camp1ID + "/rules"

	// A 配好规则
	if status, out := f.admin(t, "PUT", url, putRules(3, nil, nil, 1)); status != 200 {
		t.Fatalf("seed PUT = %d %v", status, out)
	}
	// B 租户对 A 的活动:GET/PUT/DELETE/修订 全部 404(不可见,不泄露存在性)
	status, out := f.do(t, "GET", url, "sess-owner-b", f.tenB, ``)
	if status != 404 || out["error"] != "not_found" {
		t.Fatalf("cross-tenant GET = %d %v, want 404", status, out)
	}
	status, _ = f.do(t, "PUT", url, "sess-owner-b", f.tenB, putRules(9, nil, nil, nil))
	if status != 404 {
		t.Fatalf("cross-tenant PUT = %d, want 404", status)
	}
	status, _ = f.do(t, "DELETE", url, "sess-owner-b", f.tenB, ``)
	if status != 404 {
		t.Fatalf("cross-tenant DELETE = %d, want 404", status)
	}
	status, _ = f.do(t, "GET", url+"/revisions", "sess-owner-b", f.tenB, ``)
	if status != 404 {
		t.Fatalf("cross-tenant revisions = %d, want 404", status)
	}
	// A 的规则未被 B 的 PUT 玷污
	status, out = f.admin(t, "GET", url, ``)
	if status != 200 || out["ruleset"].(map[string]any)["reward_threshold"].(float64) != 3 {
		t.Fatalf("tenant A rules polluted: %d %v", status, out)
	}
}

// ---- 4. 频控真实接线 ---------------------------------------------------------------

// seedCapOnAllCampaigns gives all three campaigns a tenant-wide per-contact cap.
func (f *rulesFixture) seedCapOnAllCampaigns(t *testing.T, cap int) {
	t.Helper()
	rs := campaignrules.Ruleset{
		RewardThreshold:              rulesIntPtr(3),
		DailyPublishLimit:            rulesIntPtr(100),
		DuplicatePublishWindowHours:  rulesIntPtr(72),
		PerContactDailySubmissionCap: rulesIntPtr(cap),
	}
	for _, id := range []string{f.camp1ID, f.camp2ID, f.camp3ID} {
		if _, err := f.s.St.UpsertCampaignRules(f.tenA, id, rs, "usr_owner_a"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSubmissionCapWiringRejectsWithReasonCode(t *testing.T) {
	f := newRulesFixture(t, true)
	f.seedCapOnAllCampaigns(t, 2)

	// 第 1、2 次首次提交(跨活动,同联系人,租户内计数)放行
	if st, out := f.submit(t, f.code1, "13900000001"); st != 201 {
		t.Fatalf("submit 1 = %d %v, want 201", st, out)
	}
	if st, out := f.submit(t, f.code2, "13900000001"); st != 201 {
		t.Fatalf("submit 2 = %d %v, want 201", st, out)
	}
	// 第 3 次:超限 429 + 机器原因码
	st, out := f.submit(t, f.code3, "13900000001")
	if st != http.StatusTooManyRequests || out["error"] != "submission_cap_reached" {
		t.Fatalf("submit 3 = %d %v, want 429 submission_cap_reached", st, out)
	}
	// 被拒的提交不得落库
	if st, _ := f.submit(t, f.code3, "13900000001"); st != http.StatusTooManyRequests {
		t.Fatalf("repeat over-cap submit = %d, want 429 again", st)
	}
	// 其他联系人不受影响(cap 按联系人计)
	if st, _ := f.submit(t, f.code3, "13900000002"); st != 201 {
		t.Fatalf("other contact = %d, want 201", st)
	}
}

func TestSubmissionCapReplayDoesNotConsumeQuota(t *testing.T) {
	f := newRulesFixture(t, true)
	f.seedCapOnAllCampaigns(t, 1)

	// 首次提交占用唯一额度
	if st, out := f.submit(t, f.code1, "13900000004"); st != 201 {
		t.Fatalf("first submit = %d %v", st, out)
	}
	// 其他活动已超限
	if st, out := f.submit(t, f.code2, "13900000004"); st != http.StatusTooManyRequests || out["error"] != "submission_cap_reached" {
		t.Fatalf("over-cap = %d %v, want 429 submission_cap_reached", st, out)
	}
	// 幂等重放先短路:同 (活动,手机号,UTC日) 重放返回原 ref,不评估频控
	st, out := f.submit(t, f.code1, "13900000004")
	if st != 200 || out["duplicate"] != true {
		t.Fatalf("replay = %d %v, want 200 duplicate (幂等重放不计频控)", st, out)
	}
	// 重放后的库内行数不变(仍只有 1 行)
	var n int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(1) FROM lead_submissions WHERE phone=?`, "13900000004").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rows = %d, want 1 (重放绝不新增)", n)
	}
}

func TestSubmissionCapResetsAcrossUTCDay(t *testing.T) {
	f := newRulesFixture(t, true)
	f.seedCapOnAllCampaigns(t, 1)

	if st, _ := f.submit(t, f.code1, "13900000005"); st != 201 {
		t.Fatalf("day1 submit 1 failed")
	}
	if st, out := f.submit(t, f.code2, "13900000005"); st != http.StatusTooManyRequests {
		t.Fatalf("day1 over-cap = %d %v, want 429", st, out)
	}
	// 把第一次提交搬到昨天(时间机器,验证 UTC 日窗重置)
	if _, err := f.s.St.DB.Exec(`UPDATE lead_submissions SET created_at='2026-01-19T23:59:59Z', updated_at='2026-01-19T23:59:59Z' WHERE phone=?`, "13900000005"); err != nil {
		t.Fatal(err)
	}
	st, out := f.submit(t, f.code2, "13900000005")
	if st != 201 {
		t.Fatalf("day2 submit = %d %v, want 201 (跨 UTC 日重置)", st, out)
	}
}

func TestSubmissionCapNoRulesMeansNoCap(t *testing.T) {
	f := newRulesFixture(t, true)
	// 开着 FEATURE_CAMPAIGN_RULES 但活动未配置规则:与现行为一致,不设限
	for i := 0; i < 3; i++ {
		if st, _ := f.submit(t, f.code1, "13900000006"); st != 201 && st != 200 {
			t.Fatalf("submit %d = %d, want 201/200 (no rules = no cap)", i, st)
		}
	}
}
