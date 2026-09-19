package httpapi

// HUI-1674 FEAT-0175 连锁总部管控多门店:HTTP 层作用域矩阵 E2E。
//
// 真实非生产:httptest + 真 sqlite(embedded migrations)+ stub identity/upload。
// 覆盖验收:
//   - org_owner 建 3 店,各建活动/短码/标签,全量可见;
//   - A 店 manager 仅见/操作 A 店;跨店读取 404 掩码;伪造参数不放宽作用域;
//   - store_manager 不能建/改/停门店、不能管成员/导 QR/管分组(403);
//   - 停用门店:新建绑定被拒(400 store_disabled);存量活动不静默消失,
//     公共页标注 store_unavailable;启用恢复;
//   - 存量兼容:owner 别名→org_owner;staff 保留 T0 租户级语义;
//     未绑定门店的活动对门店经理不可见(总部级)。

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type multiFixture struct {
	f                *fixture
	stoA, stoB, stoC store.StoreRecord
	mgrA             string // principal ref of A-store manager
	sessMgrA         string
	campA, campB     store.Campaign
	linkA, linkB     store.CampaignLink
	tagA, tagB       string
	hqCampaign       store.Campaign
}

func newMultiFixture(t *testing.T) *multiFixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	mustNoErr(t, err)
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("连锁A")
	mustNoErr(t, err)
	tenB, err := st.CreateTenant("商家B")
	mustNoErr(t, err)
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_org_a", "org_owner"},
		{tenA.ID, "usr_staff_a", "staff"},
		{tenB.ID, "usr_owner_b", "org_owner"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}

	// 3 家门店 + 2 位门店经理(A 店/B 店)
	stoA, err := st.CreateStore(tenA.ID, "A 店", "A 址", "usr_org_a")
	mustNoErr(t, err)
	stoB, err := st.CreateStore(tenA.ID, "B 店", "B 址", "usr_org_a")
	mustNoErr(t, err)
	stoC, err := st.CreateStore(tenA.ID, "C 店", "C 址", "usr_org_a")
	mustNoErr(t, err)
	for _, m := range []struct{ principal, scope string }{
		{"usr_mgr_a", stoA.ID},
		{"usr_mgr_b", stoB.ID},
	} {
		if _, err := st.CreateMemberScoped(tenA.ID, m.principal, "store_manager", m.principal, "test", true, m.scope); err != nil {
			t.Fatalf("seed manager: %v", err)
		}
	}

	// A/B 店各一个活动 + 短码 + 标签;外加一个总部级(未绑定门店)活动
	newCamp := func(title, storeID string) (store.Campaign, store.CampaignLink) {
		c, err := st.CreateCampaign(store.NewCampaign{TenantID: tenA.ID, Title: title, StoreID: storeID, CreatedBy: "usr_org_a"})
		mustNoErr(t, err)
		_, err = st.TransitionCampaign(c.ID, tenA.ID, "active")
		mustNoErr(t, err)
		l, err := st.CreateLink(tenA.ID, c.ID, "usr_org_a")
		mustNoErr(t, err)
		return c, l
	}
	campA, linkA := newCamp("A 店活动", stoA.ID)
	campB, linkB := newCamp("B 店活动", stoB.ID)
	hq, err := st.CreateCampaign(store.NewCampaign{TenantID: tenA.ID, Title: "总部活动", CreatedBy: "usr_org_a"})
	mustNoErr(t, err)
	tagA, err := st.CreateTagsBatch(store.NewTagBatch{TenantID: tenA.ID, CampaignID: campA.ID, LinkIDs: []string{linkA.ID}, BindMode: "shared", Count: 1, StoreID: stoA.ID, CreatedBy: "usr_org_a"})
	mustNoErr(t, err)
	tagB, err := st.CreateTagsBatch(store.NewTagBatch{TenantID: tenA.ID, CampaignID: campB.ID, LinkIDs: []string{linkB.ID}, BindMode: "shared", Count: 1, StoreID: stoB.ID, CreatedBy: "usr_org_a"})
	mustNoErr(t, err)

	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_UPLOAD_BASE_URL":
			return "http://upload.test"
		case "PLATFORM_UPLOAD_TOKEN":
			return "upload-token"
		case "PUBLIC_BASE_URL":
			return "https://h5.test"
		}
		return ""
	})
	sessions := map[string]identitySession{
		"sess-org-a":   {"usr_org_a", "org@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-mgr-a":   {"usr_mgr_a", "mgr.a@example.com"},
		"sess-mgr-b":   {"usr_mgr_b", "mgr.b@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	}
	idsrv := fakeIdentity(t, sessions)
	upsrv := fakeUpload(t, map[string]string{})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: upsrv.URL, Token: "upload-token", AppID: "touch-engine", HTTP: upsrv.Client()}
	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &multiFixture{
		f:    &fixture{s: s, ts: ts, tenA: tenA.ID, tenB: tenB.ID, ownA: "usr_org_a", ownB: "usr_owner_b", staffA: "usr_staff_a"},
		stoA: stoA, stoB: stoB, stoC: stoC,
		mgrA: "usr_mgr_a", sessMgrA: "sess-mgr-a",
		campA: campA, campB: campB, linkA: linkA, linkB: linkB,
		tagA: tagA[0].ID, tagB: tagB[0].ID, hqCampaign: hq,
	}
}

// TestMultiStoreScopeMatrix: 3 店矩阵 + 伪造参数不放宽作用域。
func TestMultiStoreScopeMatrix(t *testing.T) {
	mf := newMultiFixture(t)
	f := mf.f
	org, tenA := "sess-org-a", f.tenA

	// ---- org_owner: 全量可见 ----
	status, _, body := f.do(t, "GET", "/api/v1/stores", org, tenA, "")
	if status != http.StatusOK || len(body["items"].([]any)) != 3 {
		t.Fatalf("org_owner stores = %d %v, want 3", len(body["items"].([]any)), status)
	}
	status, _, body = f.do(t, "GET", "/api/v1/campaigns", org, tenA, "")
	if n := len(body["items"].([]any)); status != http.StatusOK || n != 3 {
		t.Fatalf("org_owner campaigns = %d %v, want 3 (A/B/HQ)", n, status)
	}
	status, _, body = f.do(t, "GET", "/api/v1/nfc/tags", org, tenA, "")
	if n := int(body["count"].(float64)); status != http.StatusOK || n != 2 {
		t.Fatalf("org_owner tags = %d %v, want 2", n, status)
	}

	// ---- A 店经理:仅本店 ----
	mgr := "sess-mgr-a"
	status, _, body = f.do(t, "GET", "/api/v1/stores", mgr, tenA, "")
	items := body["items"].([]any)
	if status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["id"] != mf.stoA.ID {
		t.Fatalf("manager stores = %v (%d), want exactly A store", items, len(items))
	}
	status, _, body = f.do(t, "GET", "/api/v1/campaigns", mgr, tenA, "")
	items = body["items"].([]any)
	if status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["id"] != mf.campA.ID {
		t.Fatalf("manager campaigns = %v, want exactly A campaign", items)
	}
	// 跨店活动:读取/更新/状态/链接/二维码 全部 404 掩码
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/campaigns/" + mf.campB.ID},
		{"PATCH", "/api/v1/campaigns/" + mf.campB.ID},
		{"POST", "/api/v1/campaigns/" + mf.campB.ID + "/status"},
		{"GET", "/api/v1/campaigns/" + mf.campB.ID + "/links"},
		{"POST", "/api/v1/campaigns/" + mf.campB.ID + "/links"},
		{"GET", "/api/v1/campaigns/" + mf.campB.ID + "/assets"},
		{"GET", "/api/v1/campaigns/" + mf.campB.ID + "/leads"},
	} {
		var b string
		if tc.method != "GET" {
			b = `{"status":"paused","enabled":true}`
		}
		status, _, body := f.do(t, tc.method, tc.path, mgr, tenA, b)
		if status != http.StatusNotFound || body["error"] != "not_found" {
			t.Fatalf("manager %s %s = %d %v, want 404 not_found (mask)", tc.method, tc.path, status, body)
		}
	}
	// 总部级(未绑定门店)活动对经理不可见
	status, _, body = f.do(t, "GET", "/api/v1/campaigns/"+mf.hqCampaign.ID, mgr, tenA, "")
	if status != http.StatusNotFound {
		t.Fatalf("manager HQ campaign get = %d, want 404", status)
	}
	// 本店活动完整可操作
	status, _, _ = f.do(t, "GET", "/api/v1/campaigns/"+mf.campA.ID, mgr, tenA, "")
	if status != http.StatusOK {
		t.Fatalf("manager own campaign get = %d, want 200", status)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+mf.campA.ID+"/status", mgr, tenA, `{"status":"paused"}`)
	if status != http.StatusOK {
		t.Fatalf("manager own campaign pause = %d, want 200", status)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+mf.campA.ID+"/links", mgr, tenA, "{}")
	if status != http.StatusCreated {
		t.Fatalf("manager own link create = %d, want 201", status)
	}

	// ---- 经理建活动的门店约束 ----
	status, _, body = f.do(t, "POST", "/api/v1/campaigns", mgr, tenA, `{"title":"本店新活动","store_id":"`+mf.stoA.ID+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("manager create own-store campaign = %d %v, want 201", status, body)
	}
	status, _, body = f.do(t, "POST", "/api/v1/campaigns", mgr, tenA, `{"title":"无店活动"}`)
	if status != http.StatusForbidden || body["error"] != "out_of_scope" {
		t.Fatalf("manager create unassigned = %d %v, want 403 out_of_scope", status, body)
	}
	status, _, body = f.do(t, "POST", "/api/v1/campaigns", mgr, tenA, `{"title":"他店活动","store_id":"`+mf.stoB.ID+`"}`)
	if status != http.StatusForbidden || body["error"] != "out_of_scope" {
		t.Fatalf("manager create other-store = %d %v, want 403 out_of_scope", status, body)
	}
	// 经理不能把本店活动解绑/改绑他店
	status, _, body = f.do(t, "PATCH", "/api/v1/campaigns/"+mf.campA.ID, mgr, tenA, `{"store_id":"`+mf.stoB.ID+`"}`)
	if status != http.StatusForbidden || body["error"] != "out_of_scope" {
		t.Fatalf("manager rebind to B = %d %v, want 403 out_of_scope", status, body)
	}
	status, _, _ = f.do(t, "PATCH", "/api/v1/campaigns/"+mf.campA.ID, mgr, tenA, `{"store_id":""}`)
	if status != http.StatusForbidden {
		t.Fatalf("manager unbind = %d, want 403", status)
	}
	// 本店内改标题正常
	status, _, _ = f.do(t, "PATCH", "/api/v1/campaigns/"+mf.campA.ID, mgr, tenA, `{"title":"A 店活动(改)"}`)
	if status != http.StatusOK {
		t.Fatalf("manager own patch = %d, want 200", status)
	}

	// ---- 经理不能治理门店/成员/QR/分组 ----
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/stores", `{"name":"私建店"}`},
		{"PATCH", "/api/v1/stores/" + mf.stoA.ID, `{"name":"改店名"}`},
		{"POST", "/api/v1/stores/" + mf.stoA.ID + "/status", `{"status":"disabled"}`},
		{"POST", "/api/v1/admin/members", `{"principal_ref":"usr_x","role":"staff"}`},
		{"POST", "/api/v1/nfc/tag-groups", `{"name":"私建组"}`},
	} {
		status, _, body := f.do(t, tc.method, tc.path, mgr, tenA, tc.body)
		if status != http.StatusForbidden {
			t.Fatalf("manager %s %s = %d %v, want 403", tc.method, tc.path, status, body)
		}
	}
	// QR 导出也是 org_owner 专属(经理 403 forbidden)
	status, _, body = f.do(t, "GET", fmt.Sprintf("/api/v1/campaigns/%s/links/%s/qrcode?format=json", mf.campA.ID, mf.linkA.ID), mgr, tenA, "")
	if status != http.StatusForbidden || body["error"] != "forbidden" {
		t.Fatalf("manager QR = %d %v, want 403 forbidden", status, body)
	}
	// 但 org_owner QR 正常(配置门放行)
	status, _, body = f.do(t, "GET", fmt.Sprintf("/api/v1/campaigns/%s/links/%s/qrcode?format=json", mf.campA.ID, mf.linkA.ID), org, tenA, "")
	if status != http.StatusOK || body["code"] == nil {
		t.Fatalf("org_owner QR = %d %v, want 200 with payload", status, body)
	}

	// ---- 标签作用域:伪造参数不放宽 ----
	status, _, body = f.do(t, "GET", "/api/v1/nfc/tags?store_id="+mf.stoB.ID, mgr, tenA, "")
	if n := int(body["count"].(float64)); status != http.StatusOK || n != 1 {
		t.Fatalf("manager tags with forged store_id = %d %v, want forced to 1 (own store)", n, status)
	}
	if got := body["items"].([]any)[0].(map[string]any)["id"]; got != mf.tagA {
		t.Fatalf("manager forced-scope tag = %v, want own tag %s", got, mf.tagA)
	}
	// 跨店标签:读取/停用/删除 404 掩码
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/v1/nfc/tags/" + mf.tagB, ""},
		{"PATCH", "/api/v1/nfc/tags/" + mf.tagB, `{"label":"x"}`},
		{"POST", "/api/v1/nfc/tags/" + mf.tagB + "/status", `{"status":"disabled"}`},
		{"DELETE", "/api/v1/nfc/tags/" + mf.tagB, ""},
	} {
		status, _, body := f.do(t, tc.method, tc.path, mgr, tenA, tc.body)
		if status != http.StatusNotFound || body["error"] != "not_found" {
			t.Fatalf("manager %s %s other tag = %d %v, want 404 mask", tc.method, tc.path, status, body)
		}
	}
	// 本店标签可改 uid_hint
	status, _, _ = f.do(t, "PATCH", "/api/v1/nfc/tags/"+mf.tagA, mgr, tenA, `{"uid_hint":"04:11"}`)
	if status != http.StatusOK {
		t.Fatalf("manager own tag patch = %d, want 200", status)
	}
	// batch:显式他店 403;缺省默认本店;分组 403
	status, _, body = f.do(t, "POST", "/api/v1/nfc/tags/batch", mgr, tenA,
		`{"campaign_id":"`+mf.campA.ID+`","link_ids":["`+mf.linkA.ID+`"],"bind_mode":"shared","count":1,"store_id":"`+mf.stoB.ID+`"}`)
	if status != http.StatusForbidden || body["error"] != "out_of_scope" {
		t.Fatalf("manager batch other store = %d %v, want 403 out_of_scope", status, body)
	}
	status, _, body = f.do(t, "POST", "/api/v1/nfc/tags/batch", mgr, tenA,
		`{"campaign_id":"`+mf.campA.ID+`","link_ids":["`+mf.linkA.ID+`"],"bind_mode":"shared","count":1}`)
	if status != http.StatusCreated {
		t.Fatalf("manager batch default own store = %d %v, want 201", status, body)
	}
	if got := body["items"].([]any)[0].(map[string]any)["store_id"]; got != mf.stoA.ID {
		t.Fatalf("manager batch default store = %v, want own store", got)
	}
	// 换绑到他店活动的短码 → 404 掩码
	status, _, _ = f.do(t, "PATCH", "/api/v1/nfc/tags/"+mf.tagA, mgr, tenA, `{"link_id":"`+mf.linkB.ID+`"}`)
	if status != http.StatusNotFound {
		t.Fatalf("manager rebind to B link = %d, want 404", status)
	}

	// ---- staff 保留 T0 租户级语义 ----
	status, _, _ = f.do(t, "GET", "/api/v1/campaigns/"+mf.campB.ID, "sess-staff-a", tenA, "")
	if status != http.StatusOK {
		t.Fatalf("staff cross-store campaign get = %d, want 200 (T0 tenant semantics)", status)
	}
	status, _, body = f.do(t, "GET", "/api/v1/nfc/tags", "sess-staff-a", tenA, "")
	if status != http.StatusForbidden || body["error"] != "forbidden" {
		t.Fatalf("staff tags = %d %v, want 403 forbidden", status, body)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/stores", "sess-staff-a", tenA, `{"name":"x"}`)
	if status != http.StatusForbidden {
		t.Fatalf("staff create store = %d, want 403", status)
	}

	// ---- 成员管理:角色/作用域不变量 ----
	status, _, body = f.do(t, "POST", "/api/v1/admin/members", org, tenA, `{"principal_ref":"usr_new1","role":"owner"}`)
	if status != http.StatusCreated || body["role"] != "org_owner" {
		t.Fatalf("legacy owner alias = %d %v, want 201 org_owner", status, body)
	}
	status, _, body = f.do(t, "POST", "/api/v1/admin/members", org, tenA, `{"principal_ref":"usr_new2","role":"store_manager"}`)
	if status != http.StatusBadRequest || body["error"] != "bad_scope" {
		t.Fatalf("manager without scope = %d %v, want 400 bad_scope", status, body)
	}
	status, _, body = f.do(t, "POST", "/api/v1/admin/members", org, tenA, `{"principal_ref":"usr_new3","role":"org_owner","store_scope":"`+mf.stoA.ID+`"}`)
	if status != http.StatusBadRequest || body["error"] != "bad_scope" {
		t.Fatalf("org_owner with scope = %d %v, want 400 bad_scope", status, body)
	}
	status, _, body = f.do(t, "POST", "/api/v1/admin/members", org, tenA, `{"principal_ref":"usr_new4","role":"store_manager","store_scope":"sto_missing"}`)
	if status != http.StatusBadRequest || body["error"] != "bad_scope" {
		t.Fatalf("manager with missing store = %d %v, want 400 bad_scope", status, body)
	}
	status, _, body = f.do(t, "POST", "/api/v1/admin/members", org, tenA, `{"principal_ref":"usr_new5","role":"store_manager","store_scope":"`+mf.stoC.ID+`"}`)
	if status != http.StatusCreated || body["store_scope"] != mf.stoC.ID {
		t.Fatalf("manager create = %d %v, want 201 with scope", status, body)
	}
	newMgrID := body["id"].(string)
	// whoami 暴露作用域
	status, _, body = f.do(t, "GET", "/api/v1/whoami", mgr, tenA, "")
	if status != http.StatusOK || body["role"] != "store_manager" || body["store_scope"] != mf.stoA.ID {
		t.Fatalf("manager whoami = %d %v, want role+scope", status, body)
	}
	// 经理降级为 staff:作用域自动清空(空作用域在 JSON 中省略)
	status, _, body = f.do(t, "PATCH", "/api/v1/admin/members/"+newMgrID, org, tenA, `{"role":"staff"}`)
	scopeCleared := body["store_scope"] == nil || body["store_scope"] == ""
	if status != http.StatusOK || body["role"] != "staff" || !scopeCleared {
		t.Fatalf("demote manager = %d %v, want staff + cleared scope", status, body)
	}
}

// TestStoreDisableLifecycle: 停用门店 = 禁新建 + 公共页标注;存量不静默消失。
func TestStoreDisableLifecycle(t *testing.T) {
	mf := newMultiFixture(t)
	f := mf.f
	org, mgrB, tenA := "sess-org-a", "sess-mgr-b", f.tenA

	// 停用 B 店(org_owner)
	status, _, body := f.do(t, "POST", "/api/v1/stores/"+mf.stoB.ID+"/status", org, tenA, `{"status":"disabled"}`)
	if status != http.StatusOK || body["status"] != "disabled" {
		t.Fatalf("disable store = %d %v, want 200 disabled", status, body)
	}
	// 经理仍能看到自己(已停用)的店
	status, _, body = f.do(t, "GET", "/api/v1/stores", mgrB, tenA, "")
	items := body["items"].([]any)
	if status != http.StatusOK || len(items) != 1 || items[0].(map[string]any)["status"] != "disabled" {
		t.Fatalf("disabled store still visible to manager = %v", items)
	}

	// 新建绑定 B 店 → 400 store_disabled(org_owner 同样被拒)
	status, _, body = f.do(t, "POST", "/api/v1/campaigns", org, tenA, `{"title":"新活动","store_id":"`+mf.stoB.ID+`"}`)
	if status != http.StatusBadRequest || body["error"] != "store_disabled" {
		t.Fatalf("create in disabled store = %d %v, want 400 store_disabled", status, body)
	}
	// 改绑入 B 店 → 400;改绑出 B 店允许
	status, _, _ = f.do(t, "PATCH", "/api/v1/campaigns/"+mf.campA.ID, org, tenA, `{"store_id":"`+mf.stoB.ID+`"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("rebind into disabled store = %d, want 400", status)
	}

	// 存量活动不静默消失:仍列表可见、可读取、可状态流转
	status, _, body = f.do(t, "GET", "/api/v1/campaigns", org, tenA, "")
	found := false
	for _, it := range body["items"].([]any) {
		if it.(map[string]any)["id"] == mf.campB.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("disabled store campaign must remain listed (逐个显式处理)")
	}
	status, _, _ = f.do(t, "GET", "/api/v1/campaigns/"+mf.campB.ID, org, tenA, "")
	if status != http.StatusOK {
		t.Fatalf("disabled store campaign get = %d, want 200", status)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+mf.campB.ID+"/status", org, tenA, `{"status":"paused"}`)
	if status != http.StatusOK {
		t.Fatalf("disabled store campaign pause = %d, want 200", status)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+mf.campB.ID+"/status", org, tenA, `{"status":"active"}`)
	if status != http.StatusOK {
		t.Fatalf("disabled store campaign resume = %d, want 200", status)
	}
	// B 店经理在停用店里仍可正常作业(存量活动)
	status, _, _ = f.do(t, "GET", "/api/v1/campaigns/"+mf.campB.ID, mgrB, tenA, "")
	if status != http.StatusOK {
		t.Fatalf("manager of disabled store reads own campaign = %d, want 200", status)
	}

	// 公共页:available + store_unavailable 标注
	status, _, body = f.do(t, "GET", "/api/v1/public/links/"+mf.linkB.Code, "", "", "")
	if status != http.StatusOK || body["state"] != "available" || body["store_notice"] != "store_unavailable" {
		t.Fatalf("public disabled-store page = %d %v, want available + notice", status, body)
	}
	// 非可用态保持单字段白名单(标注不出现)
	f.do(t, "POST", "/api/v1/campaigns/"+mf.campB.ID+"/status", org, tenA, `{"status":"paused"}`)
	status, _, body = f.do(t, "GET", "/api/v1/public/links/"+mf.linkB.Code, "", "", "")
	if status != http.StatusNotFound || len(body) != 1 || body["state"] != "paused" {
		t.Fatalf("public paused page = %d %v, want 404 single-field", status, body)
	}
	f.do(t, "POST", "/api/v1/campaigns/"+mf.campB.ID+"/status", org, tenA, `{"status":"active"}`)

	// 未停用店的公共页无标注
	status, _, body = f.do(t, "GET", "/api/v1/public/links/"+mf.linkA.Code, "", "", "")
	if status != http.StatusOK || body["store_notice"] != nil {
		t.Fatalf("public healthy page = %d %v, want no notice", status, body)
	}

	// 启用恢复:新建绑定放行,标注消失
	status, _, _ = f.do(t, "POST", "/api/v1/stores/"+mf.stoB.ID+"/status", org, tenA, `{"status":"active"}`)
	if status != http.StatusOK {
		t.Fatalf("re-enable = %d", status)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns", org, tenA, `{"title":"恢复后","store_id":"`+mf.stoB.ID+`"}`)
	if status != http.StatusCreated {
		t.Fatalf("create after re-enable = %d, want 201", status)
	}
	status, _, body = f.do(t, "GET", "/api/v1/public/links/"+mf.linkB.Code, "", "", "")
	if status != http.StatusOK || body["store_notice"] != nil {
		t.Fatalf("public page after re-enable = %d %v, want no notice", status, body)
	}

	// 停用不级联:标签/短码保持原状(B 店标签仍启用、可导出给 org_owner)
	status, _, body = f.do(t, "GET", "/api/v1/nfc/tags?store_id="+mf.stoB.ID, org, tenA, "")
	if n := int(body["count"].(float64)); status != http.StatusOK || n != 1 {
		t.Fatalf("tags of disabled store must not cascade = %d %v", n, status)
	}
	if got := body["items"].([]any)[0].(map[string]any)["status"]; got != "active" {
		t.Fatalf("tag status after store disable = %v, want active (no cascade)", got)
	}
}
