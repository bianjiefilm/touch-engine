package httpapi

// server_video_templates_test.go: HUI-1669 FEAT-0170 视频模板管理 HTTP 验收矩阵。
//
// 覆盖(票面 TDD 清单,HTTP 部分):
//   - off 基线:FEATURE_VIDEO_TEMPLATES 关 = 路由不注册,全部 404(面板不可见);
//   - 模板 CRUD + 槽位校验矩阵:空槽位列表/媒体类型不符/语义标签非法/槽位重名
//     各 422(独立原因码);模板重名 409;未知字段 400;
//   - 生命周期:发布后版本只读(PUT 已发布版本 409 version_readonly;带已发布
//     版本的模板 DELETE 409 template_published);结构变更 = 新草稿版本;
//     同一时刻至多一个草稿(409 draft_exists);无草稿可发(409 no_draft);
//   - 发布校验:槽位绑定引用不存在 409 slot_asset_missing / 媒体类型不符
//     409 slot_asset_type_mismatch(复用 FEAT-0167 素材库登记行);
//   - 分配:引用未发布版本 409 version_not_published;幂等重放(replayed=true,
//     留痕零改动);换版重分配留 updated 痕;解绑幂等;按店/按模板查询;
//   - 版本冻结:发布 v2 后既有分配仍钉 v1;
//   - 跨租户 404 / 0 行;authz:管理动作仅 org_owner,查询按既有业务角色
//     (门店经理仅本店分配查询,其余 out_of_scope)。
//
// 纪律:夹具手机号/邮箱全合成;token 一律 <token> 占位;零生成/渲染触发字段。

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

// ---- fixture -------------------------------------------------------------------

type videoTplFixture struct {
	s    *Server
	ts   *httptest.Server
	fup  *fakeUploadServer
	tenA string
	tenB string
	st1  string
	st2  string
}

// newVideoTplFixture boots the API server with FEATURE_VIDEO_TEMPLATES (and
// optionally FEATURE_ASSET_LIB/FEATURE_UPLOAD for binding tests) against a
// fresh sqlite.
func newVideoTplFixture(t *testing.T, vtOn, assetLibOn bool) *videoTplFixture {
	t.Helper()
	upSrv, fup := newFakeUpload(t)
	flag := func(on bool) string {
		if on {
			return "on"
		}
		return ""
	}
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_VIDEO_TEMPLATES":
			return flag(vtOn)
		case "FEATURE_ASSET_LIB":
			return flag(assetLibOn)
		case "FEATURE_UPLOAD":
			return flag(assetLibOn)
		case "PLATFORM_UPLOAD_BASE_URL":
			if assetLibOn {
				return upSrv.URL
			}
			return ""
		case "PLATFORM_UPLOAD_TOKEN":
			if assetLibOn {
				return "upload-token"
			}
			return ""
		}
		return ""
	})

	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	st := store.New(d)

	tenA, err := st.CreateTenant("模板商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("模板商家B")
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
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if _, err := st.CreateMemberScoped(tenA.ID, "usr_sm_a", "store_manager", "sm", "test", true, s1.ID); err != nil {
		t.Fatalf("seed sm: %v", err)
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "owner@example.com"},
		"sess-staff-a": {"usr_staff_a", "staff@example.com"},
		"sess-sm-a":    {"usr_sm_a", "sm@example.com"},
		"sess-owner-b": {"usr_owner_b", "ownerb@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	upc := &upload.Client{BaseURL: upSrv.URL, Token: "upload-token", AppID: "touch-engine", HTTP: upSrv.Client()}

	s := New(cfg, d, idc, upc, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	return &videoTplFixture{s: s, ts: ts, fup: fup, tenA: tenA.ID, tenB: tenB.ID, st1: s1.ID, st2: s2.ID}
}

func (f *videoTplFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
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

func (f *videoTplFixture) owner(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "sess-owner-a", f.tenA, body)
}

// mustTemplate creates a template with the canonical two slots and returns id.
func (f *videoTplFixture) mustTemplate(t *testing.T, name string) string {
	t.Helper()
	status, out := f.owner(t, "POST", "/api/v1/video-templates", vtCreateBodyJSON(name))
	if status != 201 {
		t.Fatalf("template %s: %d %v", name, status, out)
	}
	return out["id"].(string)
}

// mustPublish publishes the current draft of a template via HTTP.
func (f *videoTplFixture) mustPublish(t *testing.T, templateID string) {
	t.Helper()
	status, out := f.owner(t, "POST", "/api/v1/video-templates/"+templateID+"/publish", ``)
	if status != 200 {
		t.Fatalf("publish %s: %d %v", templateID, status, out)
	}
}

// mustRegister seeds a ready upload asset and registers the lib reference row
// (binding tests need FEATURE_ASSET_LIB/FEATURE_UPLOAD on).
func (f *videoTplFixture) mustRegister(t *testing.T, ref, sha, mediaType string) string {
	t.Helper()
	f.fup.seedUpload(ref, sha)
	body := fmt.Sprintf(`{"asset_ref":%q,"sha256":%q,"media_type":%q,"source":"aicut","purpose":"template_binding","grant_ref":"grant-2026-001"}`,
		ref, sha, mediaType)
	status, out := f.owner(t, "POST", "/api/v1/assets", body)
	if status != 201 {
		t.Fatalf("register %s: %d %v", ref, status, out)
	}
	return out["id"].(string)
}

func vtCreateBodyJSON(name string) string {
	return fmt.Sprintf(`{"name":%q,"slots":[`+
		`{"name":"opening","media_type":"video","role":"opening"},`+
		`{"name":"hero","media_type":"image","role":"product"}]}`, name)
}

// ---- 1. off 基线:路由不注册,统一 404 ----------------------------------------------

func TestVideoTemplatesOffAtBootRoutesNotRegistered(t *testing.T) {
	f := newVideoTplFixture(t, false, false)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/video-templates"},
		{"GET", "/api/v1/video-templates"},
		{"GET", "/api/v1/video-templates/vt_x"},
		{"PATCH", "/api/v1/video-templates/vt_x"},
		{"DELETE", "/api/v1/video-templates/vt_x"},
		{"GET", "/api/v1/video-templates/vt_x/versions"},
		{"POST", "/api/v1/video-templates/vt_x/versions"},
		{"GET", "/api/v1/video-templates/vt_x/versions/1"},
		{"PUT", "/api/v1/video-templates/vt_x/versions/1"},
		{"POST", "/api/v1/video-templates/vt_x/publish"},
		{"POST", "/api/v1/video-templates/vt_x/assignments"},
		{"GET", "/api/v1/video-templates/vt_x/assignments"},
		{"DELETE", "/api/v1/video-templates/vt_x/assignments/sto_x"},
		{"GET", "/api/v1/stores/sto_x/video-templates"},
	} {
		status, out := f.owner(t, tc.method, tc.path, `{}`)
		if status != 404 {
			t.Fatalf("%s %s off-boot = %d %v, want 404 (route never registered)", tc.method, tc.path, status, out)
		}
	}
}

// ---- 2. 模板 CRUD + 槽位校验矩阵 ---------------------------------------------------

func TestVideoTemplateCRUDAndValidationMatrix(t *testing.T) {
	f := newVideoTplFixture(t, true, false)
	tpl := f.mustTemplate(t, "年货节模板")

	// 创建响应:草稿 v1 + 槽位回显。
	status, out := f.owner(t, "GET", "/api/v1/video-templates/"+tpl, ``)
	if status != 200 || out["name"] != "年货节模板" {
		t.Fatalf("get = %d %v", status, out)
	}
	vers := out["versions"].([]any)
	if len(vers) != 1 {
		t.Fatalf("versions = %v", vers)
	}
	v1 := vers[0].(map[string]any)
	if v1["version"] != float64(1) || v1["status"] != "draft" {
		t.Fatalf("v1 = %v", v1)
	}
	if slots := v1["slots"].([]any); len(slots) != 2 ||
		slots[0].(map[string]any)["role"] != "opening" {
		t.Fatalf("slots roundtrip = %v", slots)
	}

	// 重名 409;空名/空槽位/坏媒体类型/坏语义标签/槽位重名 422(独立原因码)。
	if status, out = f.owner(t, "POST", "/api/v1/video-templates", vtCreateBodyJSON("年货节模板")); status != 409 || out["error"] != "duplicate_video_template" {
		t.Fatalf("dup name = %d %v", status, out)
	}
	for _, tc := range []struct{ name, body, reason string }{
		{"empty_slots", `{"name":"空模板","slots":[]}`, "missing_slots"},
		{"no_slots_field", `{"name":"无槽模板"}`, "missing_slots"},
		{"bad_media_type", `{"name":"坏类型","slots":[{"name":"s","media_type":"audio","role":"opening"}]}`, "bad_slot_media_type"},
		{"bad_role", `{"name":"坏标签","slots":[{"name":"s","media_type":"video","role":"hero_shot"}]}`, "bad_slot_role"},
		{"dup_slot_name", `{"name":"槽重名","slots":[{"name":"s","media_type":"video","role":"opening"},{"name":"s","media_type":"video","role":"ending"}]}`, "duplicate_slot_name"},
		{"missing_slot_name", `{"name":"无名槽","slots":[{"name":" ","media_type":"video","role":"opening"}]}`, "missing_slot_name"},
		{"missing_name", `{"name":" ","slots":[{"name":"s","media_type":"video","role":"opening"}]}`, "missing_template_name"},
	} {
		if status, out = f.owner(t, "POST", "/api/v1/video-templates", tc.body); status != 422 || out["error"] != tc.reason {
			t.Fatalf("%s = %d %v, want 422 %s", tc.name, status, out, tc.reason)
		}
	}
	// 未知字段:400 bad_request。
	if status, out = f.owner(t, "POST", "/api/v1/video-templates", `{"name":"x","render_now":true}`); status != 400 || out["error"] != "bad_request" {
		t.Fatalf("unknown field = %d %v (接口零生成/渲染触发字段)", status, out)
	}

	// 列表汇总与重命名。
	status, out = f.owner(t, "GET", "/api/v1/video-templates", ``)
	items := out["items"].([]any)
	if status != 200 || len(items) != 1 || items[0].(map[string]any)["latest_version"] != float64(1) {
		t.Fatalf("list = %d %v", status, out)
	}
	if status, out = f.owner(t, "PATCH", "/api/v1/video-templates/"+tpl, `{"name":"新春模板"}`); status != 200 || out["name"] != "新春模板" {
		t.Fatalf("rename = %d %v", status, out)
	}
	f.mustTemplate(t, "占名模板")
	if status, out = f.owner(t, "PATCH", "/api/v1/video-templates/"+tpl, `{"name":"占名模板"}`); status != 409 {
		t.Fatalf("rename dup = %d %v", status, out)
	}
	// 草稿模板(从未发布)可删。
	draft := f.mustTemplate(t, "待删模板")
	if status, _ = f.owner(t, "DELETE", "/api/v1/video-templates/"+draft, ``); status != 200 {
		t.Fatalf("draft delete = %d", status)
	}
	if status, _ = f.owner(t, "GET", "/api/v1/video-templates/"+draft, ``); status != 404 {
		t.Fatalf("draft after delete = %d", status)
	}
}

// ---- 3. 生命周期:发布只读 / 版本只增 / 删除守卫 --------------------------------------

func TestVideoTemplatePublishAndVersionReadonlyHTTP(t *testing.T) {
	f := newVideoTplFixture(t, true, false)
	tpl := f.mustTemplate(t, "生命周期模板")

	// 发布。
	status, out := f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/publish", ``)
	if status != 200 || out["status"] != "published" || out["published_by"] == "" || out["published_at"] == "" {
		t.Fatalf("publish = %d %v", status, out)
	}
	// 无草稿可发(最新版已发布)。
	if status, out = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/publish", ``); status != 409 || out["error"] != "no_draft" {
		t.Fatalf("second publish = %d %v, want 409 no_draft", status, out)
	}
	// 发布后版本只读:改 = 409 version_readonly。
	body := `{"slots":[{"name":"changed","media_type":"video","role":"opening"}]}`
	if status, out = f.owner(t, "PUT", "/api/v1/video-templates/"+tpl+"/versions/1", body); status != 409 || out["error"] != "version_readonly" {
		t.Fatalf("edit published version = %d %v, want 409 version_readonly", status, out)
	}
	// 结构变更 = 新草稿版本。
	if status, out = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/versions", body); status != 201 || out["version"] != float64(2) || out["status"] != "draft" {
		t.Fatalf("new draft = %d %v", status, out)
	}
	// 已有草稿再开 = 409 draft_exists。
	if status, out = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/versions", body); status != 409 || out["error"] != "draft_exists" {
		t.Fatalf("second draft = %d %v, want 409 draft_exists", status, out)
	}
	// 当前草稿可原位改写。
	if status, _ = f.owner(t, "PUT", "/api/v1/video-templates/"+tpl+"/versions/2", body); status != 200 {
		t.Fatalf("edit current draft = %d", status)
	}
	// 带已发布版本的模板:删除 = 409 template_published。
	if status, out = f.owner(t, "DELETE", "/api/v1/video-templates/"+tpl, ``); status != 409 || out["error"] != "template_published" {
		t.Fatalf("delete published = %d %v, want 409 template_published", status, out)
	}
	// 发布 v2;版本行只增不删。
	f.mustPublish(t, tpl)
	status, out = f.owner(t, "GET", "/api/v1/video-templates/"+tpl+"/versions", ``)
	vers := out["items"].([]any)
	if status != 200 || len(vers) != 2 ||
		vers[0].(map[string]any)["status"] != "published" || vers[1].(map[string]any)["version"] != float64(2) {
		t.Fatalf("versions = %d %v", status, out)
	}
	// 未知版本:404。
	if status, _ = f.owner(t, "GET", "/api/v1/video-templates/"+tpl+"/versions/9", ``); status != 404 {
		t.Fatalf("missing version = %d", status)
	}
	if status, _ = f.owner(t, "GET", "/api/v1/video-templates/"+tpl+"/versions/notanumber", ``); status != 404 {
		t.Fatalf("bad version = %d", status)
	}
}

// ---- 4. 发布校验:槽位绑定引用(复用 FEAT-0167 素材行) -------------------------------

func TestVideoTemplatePublishBindingRejections(t *testing.T) {
	f := newVideoTplFixture(t, true, true)
	f.fup.seedUpload("upl_img", shaFixture)
	f.fup.seedUpload("upl_vid", shaOther)
	imgID := f.mustRegister(t, "upl_img", shaFixture, "image")
	vidID := f.mustRegister(t, "upl_vid", shaOther, "video")

	slotWith := func(name, id, media string) string {
		return fmt.Sprintf(`{"name":%q,"slots":[{"name":"hero","media_type":%q,"role":"product","asset_id":%q},`+
			`{"name":"outro","media_type":"video","role":"ending"}]}`, name, media, id)
	}
	// 绑定引用不存在:发布 409 slot_asset_missing。
	status, out := f.owner(t, "POST", "/api/v1/video-templates", slotWith("缺引用模板", "las_missing", "video"))
	if status != 201 {
		t.Fatalf("create with missing binding = %d %v(草稿面允许占位绑定)", status, out)
	}
	missID := out["id"].(string)
	if status, out = f.owner(t, "POST", "/api/v1/video-templates/"+missID+"/publish", ``); status != 409 || out["error"] != "slot_asset_missing" {
		t.Fatalf("publish missing binding = %d %v, want 409 slot_asset_missing", status, out)
	}
	// 被拒发布不留半条状态:版本仍是 draft。
	status, out = f.owner(t, "GET", "/api/v1/video-templates/"+missID+"/versions/1", ``)
	if status != 200 || out["status"] != "draft" {
		t.Fatalf("rejected publish mutated status: %d %v", status, out)
	}
	// 媒体类型不符(video 槽绑 image 素材):发布 409 slot_asset_type_mismatch。
	status, out = f.owner(t, "POST", "/api/v1/video-templates", slotWith("类型不符模板", imgID, "video"))
	if status != 201 {
		t.Fatalf("create wrong type = %d %v", status, out)
	}
	wrongID := out["id"].(string)
	if status, out = f.owner(t, "POST", "/api/v1/video-templates/"+wrongID+"/publish", ``); status != 409 || out["error"] != "slot_asset_type_mismatch" {
		t.Fatalf("publish wrong type = %d %v, want 409 slot_asset_type_mismatch", status, out)
	}
	// 绑定正确 → 发布成功。
	status, out = f.owner(t, "POST", "/api/v1/video-templates", slotWith("绑定齐全模板", vidID, "video"))
	if status != 201 {
		t.Fatalf("create ok binding = %d %v", status, out)
	}
	okID := out["id"].(string)
	f.mustPublish(t, okID)
}

// ---- 5. 分配:幂等 / 留痕 / 钉版冻结 / 双向查询 ---------------------------------------

func TestVideoTemplateAssignmentLifecycleHTTP(t *testing.T) {
	f := newVideoTplFixture(t, true, false)
	tpl := f.mustTemplate(t, "分配模板")

	// 引用未发布版本:无已发布版本(version=0)= 409 version_not_published。
	status, out := f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments",
		fmt.Sprintf(`{"store_id":%q,"version":0}`, f.st1))
	if status != 409 || out["error"] != "version_not_published" {
		t.Fatalf("assign unpublished = %d %v", status, out)
	}
	// 显式引用草稿版本同样 409。
	status, out = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments",
		fmt.Sprintf(`{"store_id":%q,"version":1}`, f.st1))
	if status != 409 || out["error"] != "version_not_published" {
		t.Fatalf("assign draft version = %d %v", status, out)
	}

	f.mustPublish(t, tpl)
	// 首次分配:钉 v1,留痕齐全,201。
	status, out = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments",
		fmt.Sprintf(`{"store_id":%q,"version":0}`, f.st1))
	if status != 201 || out["version"] != float64(1) || out["assigned_by"] == "" || out["assigned_at"] == "" {
		t.Fatalf("first assign = %d %v", status, out)
	}
	assignmentID := out["id"].(string)
	tplName := out["template"].(map[string]any)["name"]
	if tplName != "分配模板" {
		t.Fatalf("hydration = %v", tplName)
	}
	// 同键同版重放:200 replayed=true,行与留痕零改动。
	status, out2 := f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments",
		fmt.Sprintf(`{"store_id":%q,"version":0}`, f.st1))
	if status != 200 || out2["replayed"] != true || out2["id"] != assignmentID || out2["assigned_at"] != out["assigned_at"] {
		t.Fatalf("replay = %d %v (was %v)", status, out2, out)
	}

	// 版本冻结:出 v2 并发布,既有分配仍钉 v1。
	status, out = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/versions",
		`{"slots":[{"name":"v2","media_type":"video","role":"opening"}]}`)
	if status != 201 {
		t.Fatalf("v2 create = %d %v", status, out)
	}
	f.mustPublish(t, tpl)
	status, out = f.owner(t, "GET", "/api/v1/stores/"+f.st1+"/video-templates", ``)
	byStore := out["items"].([]any)
	if status != 200 || len(byStore) != 1 || byStore[0].(map[string]any)["version"] != float64(1) {
		t.Fatalf("pinned assignment after v2 = %d %v, want still v1", status, out)
	}

	// 换版重分配:钉版更新、updated 留痕更新、assigned 留痕保持首次。
	status, out = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments",
		fmt.Sprintf(`{"store_id":%q,"version":2}`, f.st1))
	if status != 200 || out["replayed"] == true || out["version"] != float64(2) ||
		out["assigned_at"] != out2["assigned_at"] || out["updated_at"] == out2["updated_at"] {
		t.Fatalf("re-assign = %d %v (replay was %v)", status, out, out2)
	}
	// 幂等:再解绑两次均成功;按店查询归零。
	if status, _ = f.owner(t, "DELETE", "/api/v1/video-templates/"+tpl+"/assignments/"+f.st1, ``); status != 200 {
		t.Fatalf("unbind = %d", status)
	}
	if status, _ = f.owner(t, "DELETE", "/api/v1/video-templates/"+tpl+"/assignments/"+f.st1, ``); status != 200 {
		t.Fatalf("double unbind must be idempotent = %d", status)
	}
	status, out = f.owner(t, "GET", "/api/v1/stores/"+f.st1+"/video-templates", ``)
	if status != 200 || len(out["items"].([]any)) != 0 {
		t.Fatalf("after unbind = %d %v", status, out)
	}

	// 按模板查询:另一店也分配后返回两店行(解绑后的再分配 = 全新分配,201)。
	status, _ = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments",
		fmt.Sprintf(`{"store_id":%q,"version":0}`, f.st1))
	if status != 201 {
		t.Fatalf("re-bind st1 = %d", status)
	}
	status, _ = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments",
		fmt.Sprintf(`{"store_id":%q,"version":0}`, f.st2))
	if status != 201 {
		t.Fatalf("assign st2 = %d", status)
	}
	status, out = f.owner(t, "GET", "/api/v1/video-templates/"+tpl+"/assignments", ``)
	byTpl := out["items"].([]any)
	if status != 200 || len(byTpl) != 2 {
		t.Fatalf("by template = %d %v", status, out)
	}
	// 未知版本分配:404;未知门店:404。
	if status, _ = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments",
		fmt.Sprintf(`{"store_id":%q,"version":99}`, f.st1)); status != 404 {
		t.Fatalf("assign missing version = %d", status)
	}
	if status, _ = f.owner(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments",
		`{"store_id":"sto_missing","version":0}`); status != 404 {
		t.Fatalf("assign unknown store = %d", status)
	}
}

// ---- 6. 跨租户 / authz --------------------------------------------------------------

func TestVideoTemplateCrossTenantInvisible(t *testing.T) {
	f := newVideoTplFixture(t, true, false)
	tpl := f.mustTemplate(t, "A的模板")
	f.mustPublish(t, tpl)
	if _, _, _, err := f.s.St.AssignVideoTemplate(f.tenA, f.st1, tpl, 0, "test"); err != nil {
		t.Fatalf("seed assign: %v", err)
	}
	// B 的 owner:A 的模板/版本/分配一律 404;列表 0 行。
	if status, _ := f.do(t, "GET", "/api/v1/video-templates/"+tpl, "sess-owner-b", f.tenB, ``); status != 404 {
		t.Fatalf("cross-tenant get = %d", status)
	}
	if status, _ := f.do(t, "GET", "/api/v1/video-templates/"+tpl+"/versions/1", "sess-owner-b", f.tenB, ``); status != 404 {
		t.Fatalf("cross-tenant version = %d", status)
	}
	if status, _ := f.do(t, "POST", "/api/v1/video-templates/"+tpl+"/publish", "sess-owner-b", f.tenB, ``); status != 404 {
		t.Fatalf("cross-tenant publish = %d", status)
	}
	if status, out := f.do(t, "GET", "/api/v1/video-templates", "sess-owner-b", f.tenB, ``); status != 200 || len(out["items"].([]any)) != 0 {
		t.Fatalf("cross-tenant list = %d %v, want 0 rows", status, out)
	}
	if status, _ := f.do(t, "GET", "/api/v1/stores/"+f.st1+"/video-templates", "sess-owner-b", f.tenB, ``); status != 404 {
		t.Fatalf("cross-tenant by-store (foreign store) = %d, want 404", status)
	}
}

func TestVideoTemplateAuthzRoles(t *testing.T) {
	f := newVideoTplFixture(t, true, false)
	tpl := f.mustTemplate(t, "权限模板")
	f.mustPublish(t, tpl)

	// staff:管理动作 403 forbidden;查询按既有业务角色放行。
	if status, out := f.do(t, "POST", "/api/v1/video-templates", "sess-staff-a", f.tenA, vtCreateBodyJSON("职员模板")); status != 403 || out["error"] != "forbidden" {
		t.Fatalf("staff create = %d %v", status, out)
	}
	if status, _ := f.do(t, "POST", "/api/v1/video-templates/"+tpl+"/publish", "sess-staff-a", f.tenA, ``); status != 403 {
		t.Fatalf("staff publish = %d", status)
	}
	if status, _ := f.do(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments", "sess-staff-a", f.tenA, fmt.Sprintf(`{"store_id":%q,"version":0}`, f.st1)); status != 403 {
		t.Fatalf("staff assign = %d", status)
	}
	if status, _ := f.do(t, "GET", "/api/v1/video-templates", "sess-staff-a", f.tenA, ``); status != 200 {
		t.Fatalf("staff list = %d, want 200", status)
	}
	// 门店经理:管理动作 403;本店分配查询 200;他店/租户级 out_of_scope。
	if status, out := f.do(t, "POST", "/api/v1/video-templates", "sess-sm-a", f.tenA, vtCreateBodyJSON("经理模板")); status != 403 || out["error"] != "forbidden" {
		t.Fatalf("sm create = %d %v", status, out)
	}
	if status, _ := f.do(t, "POST", "/api/v1/video-templates/"+tpl+"/assignments", "sess-sm-a", f.tenA, fmt.Sprintf(`{"store_id":%q,"version":0}`, f.st1)); status != 403 {
		t.Fatalf("sm assign = %d", status)
	}
	if _, _, _, err := f.s.St.AssignVideoTemplate(f.tenA, f.st1, tpl, 0, "test"); err != nil {
		t.Fatalf("seed assign: %v", err)
	}
	if status, _ := f.do(t, "GET", "/api/v1/stores/"+f.st1+"/video-templates", "sess-sm-a", f.tenA, ``); status != 200 {
		t.Fatalf("sm own-store assignments = %d, want 200", status)
	}
	if status, out := f.do(t, "GET", "/api/v1/stores/"+f.st2+"/video-templates", "sess-sm-a", f.tenA, ``); status != 403 || out["error"] != "out_of_scope" {
		t.Fatalf("sm other-store assignments = %d %v, want 403 out_of_scope", status, out)
	}
	if status, out := f.do(t, "GET", "/api/v1/video-templates/"+tpl, "sess-sm-a", f.tenA, ``); status != 403 || out["error"] != "out_of_scope" {
		t.Fatalf("sm tenant-level template detail = %d %v, want 403 out_of_scope", status, out)
	}
}
