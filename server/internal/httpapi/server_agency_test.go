package httpapi

// server_agency_test.go: HUI-1675 FEAT-0176 代理与子账号 HTTP 验收矩阵。
//
// 覆盖(票面 TDD 清单):
//   - off 基线:FEATURE_AGENCY 关 = 全部新路由不注册,统一 404(面板不可见);
//   - 建立/解除代管流程与留痕;重复建立幂等(200 replayed,关系行零新增);
//   - 开子账号流程 + 代开留痕可回查(agent 只见自己代开的行;owner 见全量;
//     owner 直开的行不冒充代理身份);
//   - 代理可见性矩阵:名下商家只读可见 / 无代管关系租户零可见(not_member)/
//     跨代理不可见 / 白名单外动作 4xx;
//   - 解除代管后可见性立即收窄(连 agency 面一起 403 member_disabled);
//   - 子账号按既有成员角色能力边界(白名单内可/白名单外 4xx)。
//
// 纪律:夹具手机号/邮箱全合成;token 一律 <token> 占位;PII 不进日志。

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

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type agencyFixture struct {
	s    *Server
	ts   *httptest.Server
	tenA string
	tenB string
	st1  string
	st2  string
}

func newAgencyFixture(t *testing.T, on bool) *agencyFixture {
	t.Helper()
	upSrv, _ := newFakeUpload(t)
	flag := ""
	if on {
		flag = "on"
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_AGENCY":
			return flag
		}
		return ""
	})

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
	s1, err := st.CreateStore(tenA.ID, "一店", "地址1", "test")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := st.CreateStore(tenA.ID, "二店", "地址2", "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenB.ID, "usr_owner_b", "org_owner"},
		// 服务商代理:既有成员体系内的 agent 角色行(挂载点;权威仍来自激活关系)
		{tenA.ID, "usr_ag1", "agent"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed %s: %v", m.principal, err)
		}
	}
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_sm_a", "store_manager", "sm", "test", true, s1.ID); err != nil {
		t.Fatalf("seed sm: %v", err)
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a":   {"usr_owner_a", "owner-a@example.com"},
		"sess-staff-a":   {"usr_staff_a", "staff-a@example.com"},
		"sess-sm-a":      {"usr_sm_a", "sm-a@example.com"},
		"sess-owner-b":   {"usr_owner_b", "owner-b@example.com"},
		"sess-agent1":    {"usr_ag1", "agent1@example.com"},
		"sess-agent2":    {"usr_ag2", "agent2@example.com"},
		"sess-sub-staff": {"usr_sub_staff", "sub-staff@example.com"},
		"sess-sub-mgr":   {"usr_sub_mgr", "sub-mgr@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: upSrv.URL, Token: "upload-token", AppID: "touch-engine", HTTP: upSrv.Client()}

	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &agencyFixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, st1: s1.ID, st2: s2.ID}
}

func (f *agencyFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
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

// mustEstablish runs the owner-side establish and returns the relation id.
func (f *agencyFixture) mustEstablish(t *testing.T, principal, note string) string {
	t.Helper()
	status, out := f.do(t, "POST", "/api/v1/agency/relations", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"agent_principal":%q,"note":%q}`, principal, note))
	if status != 201 {
		t.Fatalf("establish %s: %d %v", principal, status, out)
	}
	id, _ := out["id"].(string)
	if id == "" {
		t.Fatalf("establish returned no id: %v", out)
	}
	return id
}

// ---- 1. off 基线:全部新路由不注册,统一 404 ------------------------------------------

func TestAgencyOffBaseline404(t *testing.T) {
	f := newAgencyFixture(t, false)
	paths := []struct{ method, path string }{
		{"POST", "/api/v1/agency/relations"},
		{"GET", "/api/v1/agency/relations"},
		{"DELETE", "/api/v1/agency/relations/agr_x"},
		{"POST", "/api/v1/agency/sub-accounts"},
		{"GET", "/api/v1/agency/sub-accounts"},
	}
	for _, p := range paths {
		status, _ := f.do(t, p.method, p.path, "sess-owner-a", f.tenA, `{}`)
		if status != http.StatusNotFound {
			t.Fatalf("%s %s off = %d, want 404 (route never registered, invisible surface)", p.method, p.path, status)
		}
	}
}

// 第二道保险:即便路由被误注册,off 下 handler gate 仍答统一 404(双保险写法,
// 与 FEATURE_DASHBOARD/CAMPAIGN_RULES/ASSET_LIB/VIDEO_TEMPLATES 同一纪律)。
func TestAgencyHandlerGateAnswersUniform404WhenOff(t *testing.T) {
	f := newAgencyFixture(t, false)
	handlers := map[string]func(w http.ResponseWriter, r *http.Request){
		"POST /api/v1/agency/relations":    f.s.handleAgencyRelationCreate,
		"GET /api/v1/agency/relations":     f.s.handleAgencyRelationList,
		"DELETE /api/v1/agency/relations":  f.s.handleAgencyRelationRevoke,
		"POST /api/v1/agency/sub-accounts": f.s.handleAgencySubAccountCreate,
		"GET /api/v1/agency/sub-accounts":  f.s.handleAgencySubAccountList,
	}
	for name, h := range handlers {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/agency/x", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("gate %s when off = %d, want uniform 404", name, rec.Code)
		}
	}
}

// ---- 2. 建立代管:流程 + 留痕 + 幂等 --------------------------------------------------

func TestAgencyEstablishFlow(t *testing.T) {
	f := newAgencyFixture(t, true)

	// owner 建立代管;留痕:established_by = owner 成员主体
	status, out := f.do(t, "POST", "/api/v1/agency/relations", "sess-owner-a", f.tenA,
		`{"agent_principal":"usr_ag1","display_name":"服务商A","note":"年度代运营"}`)
	if status != 201 {
		t.Fatalf("establish = %d %v, want 201", status, out)
	}
	relID, _ := out["id"].(string)
	if out["agent_principal"] != "usr_ag1" || relID == "" {
		t.Fatalf("relation payload incomplete: %v", out)
	}
	if out["revoked_at"] != nil && out["revoked_at"] != "" {
		t.Fatalf("new relation must be active, got revoked_at=%v", out["revoked_at"])
	}

	// 重复建立幂等:200 + replayed,同一关系行(不新增)
	status, out = f.do(t, "POST", "/api/v1/agency/relations", "sess-owner-a", f.tenA,
		`{"agent_principal":"usr_ag1"}`)
	if status != 200 || out["replayed"] != true || out["id"] != relID {
		t.Fatalf("duplicate establish = %d %v, want 200 replayed same id", status, out)
	}

	// owner 视角列表:留痕可回查
	status, out = f.do(t, "GET", "/api/v1/agency/relations", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("owner relation list = %d %v", status, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("owner relation list = %d items, want 1", len(items))
	}
	first := items[0].(map[string]any)
	if first["established_by"] != "usr_owner_a" || first["established_at"] == "" {
		t.Fatalf("establish trail missing: %v", first)
	}

	// agent 视角:名下商家组合(按主体过滤,激活行)
	status, out = f.do(t, "GET", "/api/v1/agency/relations", "sess-agent1", f.tenA, "")
	if status != 200 {
		t.Fatalf("agent relation list = %d %v", status, out)
	}
	if items = out["items"].([]any); len(items) != 1 {
		t.Fatalf("agent portfolio = %d items, want 1", len(items))
	}

	// 权限:staff 不是 owner 类,不能建立
	if status, out = f.do(t, "POST", "/api/v1/agency/relations", "sess-staff-a", f.tenA,
		`{"agent_principal":"usr_ag9"}`); status != 403 {
		t.Fatalf("staff establish = %d %v, want 403", status, out)
	}
	// agent 自身也不能建立(建立是 owner 类治理动作)
	if status, out := f.do(t, "POST", "/api/v1/agency/relations", "sess-agent1", f.tenA,
		`{"agent_principal":"usr_ag9"}`); status != 403 {
		t.Fatalf("agent establish = %d %v, want 403", status, out)
	}

	// 目标主体已有非 agent 成员行:拒绝改角色(409),绝不静默复用
	if status, out = f.do(t, "POST", "/api/v1/agency/relations", "sess-owner-a", f.tenA,
		`{"agent_principal":"usr_staff_a"}`); status != 409 {
		t.Fatalf("establish over staff row = %d %v, want 409", status, out)
	}

	// principal_ref 形状纪律:邮箱/手机号永不成为成员
	if status, _ := f.do(t, "POST", "/api/v1/agency/relations", "sess-owner-a", f.tenA,
		`{"agent_principal":"someone@example.com"}`); status != 400 {
		t.Fatalf("email principal = %d, want 400", status)
	}
}

// ---- 3. 代理可见性矩阵 ----------------------------------------------------------------

func TestAgentVisibilityMatrix(t *testing.T) {
	f := newAgencyFixture(t, true)
	relID := f.mustEstablish(t, "usr_ag1", "可见性矩阵")

	// 名下商家:只读可见(含门店级记录,租户级只读)
	status, out := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"title":"店庆","public_content":"礼","store_id":%q}`, f.st1))
	if status != 201 {
		t.Fatalf("seed campaign: %d %v", status, out)
	}
	campID := out["id"].(string)
	if status, _ := f.do(t, "GET", "/api/v1/campaigns", "sess-agent1", f.tenA, ""); status != 200 {
		t.Fatalf("agent list on managed tenant = %d, want 200", status)
	}
	if status, out = f.do(t, "GET", "/api/v1/campaigns/"+campID, "sess-agent1", f.tenA, ""); status != 200 {
		t.Fatalf("agent read record = %d %v, want 200", status, out)
	}

	// 白名单外:写/治理一律 4xx
	if status, out = f.do(t, "POST", "/api/v1/campaigns", "sess-agent1", f.tenA, `{"title":"x"}`); status != 403 {
		t.Fatalf("agent create = %d %v, want 403 forbidden", status, out)
	}
	if status, _ := f.do(t, "PATCH", "/api/v1/campaigns/"+campID, "sess-agent1", f.tenA, `{"title":"y"}`); status != 403 {
		t.Fatalf("agent patch = %d, want 403", status)
	}
	if status, _ := f.do(t, "POST", fmt.Sprintf("/api/v1/campaigns/%s/links", campID), "sess-agent1", f.tenA, ``); status != 403 {
		t.Fatalf("agent create link = %d, want 403", status)
	}
	if status, _ := f.do(t, "GET", "/api/v1/admin/members", "sess-agent1", f.tenA, ""); status != 403 {
		t.Fatalf("agent member list = %d, want 403", status)
	}
	if status, _ := f.do(t, "POST", "/api/v1/stores", "sess-agent1", f.tenA, `{"name":"偷偷建店"}`); status != 403 {
		t.Fatalf("agent store create = %d, want 403", status)
	}

	// 无代管关系的租户对代理零可见:无成员行 -> not_member
	if status, _ := f.do(t, "GET", "/api/v1/campaigns", "sess-agent2", f.tenA, ""); status != 403 {
		t.Fatalf("stray agent list = %d, want 403 not_member", status)
	}
	// 跨代理不可见:agent1 对商家B 零可见
	if status, _ := f.do(t, "GET", "/api/v1/campaigns", "sess-agent1", f.tenB, ""); status != 403 {
		t.Fatalf("agent cross-tenant list = %d, want 403 not_member", status)
	}
	// owner B 的关系列表只含本租户(空)
	if status, out = f.do(t, "GET", "/api/v1/agency/relations", "sess-owner-b", f.tenB, ""); status != 200 {
		t.Fatalf("owner B relations = %d", status)
	}
	if items := out["items"].([]any); len(items) != 0 {
		t.Fatalf("owner B relations = %d items, want 0 (tenant scoping)", len(items))
	}
	_ = relID
}

// ---- 4. 解除代管:留痕 + 可见性立即收窄 ----------------------------------------------

func TestAgencyRevokeNarrowsImmediately(t *testing.T) {
	f := newAgencyFixture(t, true)
	relID := f.mustEstablish(t, "usr_ag1", "待解除")

	// 解除前可见
	if status, _ := f.do(t, "GET", "/api/v1/campaigns", "sess-agent1", f.tenA, ""); status != 200 {
		t.Fatalf("pre-revoke visibility missing: %d", status)
	}

	// owner 解除;墓碑留痕
	status, out := f.do(t, "DELETE", "/api/v1/agency/relations/"+relID, "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("revoke = %d %v, want 200", status, out)
	}
	if out["revoked_at"] == nil || out["revoked_at"] == "" || out["revoked_by"] != "usr_owner_a" {
		t.Fatalf("revoke trail missing: %v", out)
	}

	// 立即收窄:连 agency 面一起 403(成员行已停用,fail-closed)
	if status, _ := f.do(t, "GET", "/api/v1/campaigns", "sess-agent1", f.tenA, ""); status != 403 {
		t.Fatalf("post-revoke data visibility = %d, want 403", status)
	}
	if status, out = f.do(t, "GET", "/api/v1/agency/relations", "sess-agent1", f.tenA, ""); status != 403 || out["error"] != "member_disabled" {
		t.Fatalf("post-revoke agency face = %d %v, want 403 member_disabled", status, out)
	}

	// 重复解除:显式 409(绝不静默二次处理)
	if status, out = f.do(t, "DELETE", "/api/v1/agency/relations/"+relID, "sess-owner-a", f.tenA, ""); status != 409 {
		t.Fatalf("double revoke = %d %v, want 409", status, out)
	}
	// 他租户 owner 解除本租户关系:404 掩码
	if status, _ := f.do(t, "DELETE", "/api/v1/agency/relations/"+relID, "sess-owner-b", f.tenB, ""); status != 404 {
		t.Fatalf("cross-tenant revoke = %d, want 404", status)
	}
	// 解除不是终点:owner 再建立 = 新行,代理可见性恢复
	relID2 := f.mustEstablish(t, "usr_ag1", "复签")
	if relID2 == relID {
		t.Fatal("re-establish must mint a new relation row")
	}
	if status, _ := f.do(t, "GET", "/api/v1/campaigns", "sess-agent1", f.tenA, ""); status != 200 {
		t.Fatalf("re-established visibility = %d, want 200", status)
	}
}

// ---- 5. 开子账号:流程 + 代开留痕可回查 ----------------------------------------------

func TestAgencyIssueSubAccount(t *testing.T) {
	f := newAgencyFixture(t, true)
	f.mustEstablish(t, "usr_ag1", "子账号矩阵")

	issue := func(session, principal, role, scope string) (int, map[string]any) {
		t.Helper()
		body := fmt.Sprintf(`{"principal_ref":%q,"role":%q,"display_name":"子账号-%s"`, principal, role, principal)
		if scope != "" {
			body += fmt.Sprintf(`,"store_scope":%q`, scope)
		}
		body += `}`
		return f.do(t, "POST", "/api/v1/agency/sub-accounts", session, f.tenA, body)
	}

	// 代理开子账号(staff):成员行落位 + 留痕行落位
	status, out := issue("sess-agent1", "usr_sub1", "staff", "")
	if status != 201 {
		t.Fatalf("issue staff = %d %v, want 201", status, out)
	}
	if out["member"] == nil || out["trail"] == nil {
		t.Fatalf("issue response missing member/trail: %v", out)
	}

	// 代开留痕可回查:agent 只见自己代开的行
	status, out = f.do(t, "GET", "/api/v1/agency/sub-accounts", "sess-agent1", f.tenA, "")
	if status != 200 {
		t.Fatalf("agent trail list = %d %v", status, out)
	}
	items := out["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("agent trail = %d items, want 1", len(items))
	}
	row := items[0].(map[string]any)
	if row["issued_by_principal"] != "usr_ag1" || row["member_role"] != "staff" {
		t.Fatalf("trail row incomplete: %v", row)
	}

	// owner 直开:留痕不冒充代理身份(issued_by_principal 为空)
	status, out = issue("sess-owner-a", "usr_sub2", "staff", "")
	if status != 201 {
		t.Fatalf("owner-direct issue = %d %v, want 201", status, out)
	}
	// owner 见全量留痕(2 行);agent 仍只见自己的(1 行)
	if status, out = f.do(t, "GET", "/api/v1/agency/sub-accounts", "sess-owner-a", f.tenA, ""); status != 200 {
		t.Fatalf("owner trail list = %d", status)
	}
	if items = out["items"].([]any); len(items) != 2 {
		t.Fatalf("owner trail = %d items, want 2", len(items))
	}

	// store_manager 子账号:必须带本租户真实门店作用域
	if status, _ = issue("sess-agent1", "usr_sub3", "store_manager", ""); status != 400 {
		t.Fatalf("manager w/o scope = %d, want 400", status)
	}
	if status, _ = issue("sess-agent1", "usr_sub3", "store_manager", f.st2); status != 201 {
		t.Fatalf("manager with scope = %d, want 201", status)
	}

	// 角色白名单:代理永不铸造 owner/agent
	if status, _ = issue("sess-agent1", "usr_sub4", "org_owner", ""); status != 400 {
		t.Fatalf("issue owner = %d, want 400", status)
	}
	if status, _ = issue("sess-agent1", "usr_sub5", "agent", ""); status != 400 {
		t.Fatalf("issue agent = %d, want 400", status)
	}

	// 权限:staff 不是代理/owner 类
	if status, _ = issue("sess-staff-a", "usr_sub6", "staff", ""); status != 403 {
		t.Fatalf("staff issue = %d, want 403", status)
	}
	// 无成员行主体:零可见(not_member)
	if status, _ = issue("sess-agent2", "usr_sub7", "staff", ""); status != 403 {
		t.Fatalf("stray agent issue = %d, want 403", status)
	}
	// 目标主体已在本租户:409,留痕零残留
	if status, _ = issue("sess-agent1", "usr_owner_a", "staff", ""); status != 409 {
		t.Fatalf("duplicate principal issue = %d, want 409", status)
	}
	if status, out = f.do(t, "GET", "/api/v1/agency/sub-accounts", "sess-owner-a", f.tenA, ""); status != 200 {
		t.Fatalf("trail list after failure = %d", status)
	}
	if items = out["items"].([]any); len(items) != 3 {
		t.Fatalf("trail must stay append-only on failure: %d items, want 3", len(items))
	}
}

// ---- 6. 子账号角色能力边界(既有成员角色矩阵,白名单外 4xx)---------------------------

func TestSubAccountCapabilityBoundary(t *testing.T) {
	f := newAgencyFixture(t, true)
	f.mustEstablish(t, "usr_ag1", "能力边界")

	// seed campaigns in st1 / st2 via the owner
	mkCampaign := func(title, storeID string) string {
		t.Helper()
		status, out := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
			fmt.Sprintf(`{"title":%q,"store_id":%q}`, title, storeID))
		if status != 201 {
			t.Fatalf("seed campaign %s: %d %v", title, status, out)
		}
		return out["id"].(string)
	}
	campS1 := mkCampaign("一店活动", f.st1)
	campS2 := mkCampaign("二店活动", f.st2)

	issue := func(principal, role, scope string) map[string]any {
		t.Helper()
		body := fmt.Sprintf(`{"principal_ref":%q,"role":%q`, principal, role)
		if scope != "" {
			body += fmt.Sprintf(`,"store_scope":%q`, scope)
		}
		body += `}`
		status, out := f.do(t, "POST", "/api/v1/agency/sub-accounts", "sess-agent1", f.tenA, body)
		if status != 201 {
			t.Fatalf("issue %s: %d %v", principal, status, out)
		}
		return out
	}
	issue("usr_sub_staff", "staff", "")
	issue("usr_sub_mgr", "store_manager", f.st1)

	// staff 子账号:既有 T0 语义(租户级读写),但 agency 治理面对它关闭
	if status, _ := f.do(t, "GET", "/api/v1/campaigns", "sess-sub-staff", f.tenA, ""); status != 200 {
		t.Fatalf("staff sub-account list = %d, want 200 (T0 semantics)", status)
	}
	if status, _ := f.do(t, "POST", "/api/v1/agency/relations", "sess-sub-staff", f.tenA, `{"agent_principal":"usr_x"}`); status != 403 {
		t.Fatalf("staff sub-account establish = %d, want 403", status)
	}
	if status, _ := f.do(t, "GET", "/api/v1/agency/sub-accounts", "sess-sub-staff", f.tenA, ""); status != 403 {
		t.Fatalf("staff sub-account trail = %d, want 403", status)
	}
	if status, _ := f.do(t, "POST", "/api/v1/agency/sub-accounts", "sess-sub-staff", f.tenA, `{"principal_ref":"usr_y","role":"staff"}`); status != 403 {
		t.Fatalf("staff sub-account issue = %d, want 403", status)
	}
	if status, _ := f.do(t, "DELETE", "/api/v1/agency/relations/agr_x", "sess-sub-staff", f.tenA, ""); status != 403 {
		t.Fatalf("staff sub-account revoke = %d, want 403", status)
	}

	// store_manager 子账号:既有门店作用域(本店可见/他店 404 掩码)
	if status, _ := f.do(t, "GET", "/api/v1/campaigns/"+campS1, "sess-sub-mgr", f.tenA, ""); status != 200 {
		t.Fatalf("mgr own-store record = %d, want 200", status)
	}
	if status, _ := f.do(t, "GET", "/api/v1/campaigns/"+campS2, "sess-sub-mgr", f.tenA, ""); status != 404 {
		t.Fatalf("mgr other-store record = %d, want 404 (mask)", status)
	}
	if status, _ := f.do(t, "POST", "/api/v1/agency/sub-accounts", "sess-sub-mgr", f.tenA, `{"principal_ref":"usr_z","role":"staff"}`); status != 403 {
		t.Fatalf("mgr issue = %d, want 403", status)
	}
}
