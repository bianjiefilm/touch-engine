package httpapi

// server_asset_lib_test.go: HUI-1666 FEAT-0167 商家素材库 HTTP 验收矩阵。
//
// 覆盖(票面 TDD 清单,HTTP 部分):
//   - off 基线:FEATURE_ASSET_LIB 关 = 路由不注册,全部 404(面板不可见);
//   - 登记校验矩阵:缺来源/用途/授权声明各自 422(独立原因码);sha256 格式
//     坏 422 bad_sha256;sha256 与平台引用不符(坏引用)= 409 version_mismatch
//     且不落行;FEATURE_UPLOAD 关 = 503 显式拒绝(不伪造授权);
//   - 导入路径:仅产品图/AiCut 可导入(merchant_upload 422 source_not_importable);
//   - 池 CRUD 与租户内名称唯一(409 duplicate_pool);池内引用增删/去重;
//   - 确定性随机调取:同种子必同选;同键幂等重放(replayed=true,冻结行);
//     空池 409;调取不改池;跨租户 404;
//   - 选择台账只追加+版本冻结:素材登记新版本后,已选行/重放行 sha 不变;
//   - 候选标记:候选行与正式行并存;置位只改标记;
//   - authz:管理动作仅 org_owner(staff 403 forbidden;store_manager 403
//     out_of_scope);调取 staff 放行。
//
// 纪律:夹具手机号/邮箱全合成;token 一律 <token> 占位;大文件零进测试与日志。

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// ---- fake platform-upload ----------------------------------------------------

// fakeUploadServer mimics platform-upload's internal asset metadata endpoint.
type fakeUploadServer struct {
	mu     sync.Mutex
	assets map[string]upload.AssetMeta
}

func newFakeUpload(t *testing.T) (*httptest.Server, *fakeUploadServer) {
	t.Helper()
	f := &fakeUploadServer{assets: map[string]upload.AssetMeta{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/internal/v1/upload/assets/")
		f.mu.Lock()
		meta, ok := f.assets[id]
		f.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "not_found"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meta)
	}))
	t.Cleanup(srv.Close)
	return srv, f
}

// seedUpload registers a ready asset with the given sha256 in the fake upload.
func (f *fakeUploadServer) seedUpload(id, sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.assets[id] = upload.AssetMeta{AssetID: id, AppID: "touch-engine", Status: "ready",
		ContentType: "video/mp4", SHA256: sha}
}

// ---- fixture -------------------------------------------------------------------

type assetLibFixture struct {
	s    *Server
	ts   *httptest.Server
	up   *httptest.Server
	fup  *fakeUploadServer
	tenA string
	tenB string
	st1  string
}

func newAssetLibFixture(t *testing.T, assetLibOn, uploadOn bool) *assetLibFixture {
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
		case "FEATURE_ASSET_LIB":
			return flag(assetLibOn)
		case "FEATURE_UPLOAD":
			return flag(uploadOn)
		case "PLATFORM_UPLOAD_BASE_URL":
			// FEATURE_UPLOAD=on 进 Gate:必须配齐(fail-closed),指向假上传。
			if uploadOn {
				return upSrv.URL
			}
			return ""
		case "PLATFORM_UPLOAD_TOKEN":
			if uploadOn {
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

	tenA, err := st.CreateTenant("素材商家A")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("素材商家B")
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

	return &assetLibFixture{s: s, ts: ts, up: upSrv, fup: fup, tenA: tenA.ID, tenB: tenB.ID, st1: s1.ID}
}

func (f *assetLibFixture) do(t *testing.T, method, path, session, tenant, body string) (int, map[string]any) {
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

func (f *assetLibFixture) owner(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return f.do(t, method, path, "sess-owner-a", f.tenA, body)
}

// registerAsset posts the canonical registration body.
func (f *assetLibFixture) registerAsset(t *testing.T, session, tenant, ref, sha, mediaType, source string) (int, map[string]any) {
	t.Helper()
	body := fmt.Sprintf(`{"asset_ref":%q,"sha256":%q,"media_type":%q,"source":%q,"purpose":"campaign_publish","grant_ref":"grant-2026-001"}`,
		ref, sha, mediaType, source)
	return f.do(t, "POST", "/api/v1/assets", session, tenant, body)
}

// mustRegister registers a ready upload asset and returns the lib row id.
func (f *assetLibFixture) mustRegister(t *testing.T, ref, sha, mediaType string) string {
	t.Helper()
	f.fup.seedUpload(ref, sha)
	status, out := f.registerAsset(t, "sess-owner-a", f.tenA, ref, sha, mediaType, "aicut")
	if status != 201 {
		t.Fatalf("register %s: %d %v", ref, status, out)
	}
	return out["id"].(string)
}

func (f *assetLibFixture) mustPool(t *testing.T, name string) string {
	t.Helper()
	status, out := f.owner(t, "POST", "/api/v1/asset-pools", fmt.Sprintf(`{"name":%q}`, name))
	if status != 201 {
		t.Fatalf("pool %s: %d %v", name, status, out)
	}
	return out["id"].(string)
}

func (f *assetLibFixture) mustItem(t *testing.T, poolID, assetID string) {
	t.Helper()
	status, out := f.owner(t, "POST", "/api/v1/asset-pools/"+poolID+"/items", fmt.Sprintf(`{"asset_id":%q}`, assetID))
	if status != 201 {
		t.Fatalf("item add %s: %d %v", assetID, status, out)
	}
}

// ---- 1. off 基线:路由不注册,统一 404 ----------------------------------------------

func TestAssetLibOffAtBootRoutesNotRegistered(t *testing.T) {
	f := newAssetLibFixture(t, false, false)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/assets"},
		{"POST", "/api/v1/assets/import"},
		{"GET", "/api/v1/assets"},
		{"GET", "/api/v1/assets/las_x"},
		{"POST", "/api/v1/assets/las_x/candidate"},
		{"POST", "/api/v1/asset-pools"},
		{"GET", "/api/v1/asset-pools"},
		{"GET", "/api/v1/asset-pools/lpool_x"},
		{"PATCH", "/api/v1/asset-pools/lpool_x"},
		{"DELETE", "/api/v1/asset-pools/lpool_x"},
		{"POST", "/api/v1/asset-pools/lpool_x/items"},
		{"GET", "/api/v1/asset-pools/lpool_x/items"},
		{"DELETE", "/api/v1/asset-pools/lpool_x/items/lpi_x"},
		{"POST", "/api/v1/asset-pools/lpool_x/draw"},
		{"GET", "/api/v1/asset-pools/lpool_x/selections"},
	} {
		status, out := f.owner(t, tc.method, tc.path, `{}`)
		if status != 404 {
			t.Fatalf("%s %s off-boot = %d %v, want 404 (route never registered)", tc.method, tc.path, status, out)
		}
	}
}

// ---- 2. 登记校验矩阵 + 坏引用 ---------------------------------------------------

func TestAssetLibRegistrationValidationMatrix(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	f.fup.seedUpload("upl_m", shaFixture)

	base := map[string]string{
		"asset_ref": "upl_m", "sha256": shaFixture, "media_type": "video",
		"source": "aicut", "purpose": "campaign_publish", "grant_ref": "grant-2026-001",
	}
	build := func(mut map[string]string) string {
		merged := map[string]string{}
		for k, v := range base {
			merged[k] = v
		}
		for k, v := range mut {
			merged[k] = v
		}
		parts := make([]string, 0, len(merged))
		for k, v := range merged {
			if v == "\x00DROP" {
				continue
			}
			parts = append(parts, fmt.Sprintf("%q:%q", k, v))
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	for _, tc := range []struct {
		name   string
		mut    map[string]string
		reason string
	}{
		{"missing_source", map[string]string{"source": ""}, "missing_source"},
		{"missing_purpose", map[string]string{"purpose": ""}, "missing_purpose"},
		{"missing_grant_ref", map[string]string{"grant_ref": ""}, "missing_grant_ref"},
		{"missing_asset_ref", map[string]string{"asset_ref": ""}, "missing_asset_ref"},
		{"bad_media_type", map[string]string{"media_type": "audio"}, "bad_media_type"},
		{"bad_source", map[string]string{"source": "scraped"}, "bad_source"},
		{"bad_sha256_format", map[string]string{"sha256": "nothex"}, "bad_sha256"},
	} {
		status, out := f.owner(t, "POST", "/api/v1/assets", build(tc.mut))
		if status != 422 {
			t.Fatalf("%s = %d %v, want 422", tc.name, status, out)
		}
		if out["error"] != tc.reason {
			t.Fatalf("%s error = %v, want %q", tc.name, out["error"], tc.reason)
		}
	}
	// 未知字段:400 bad_request(DisallowUnknownFields)。
	status, out := f.owner(t, "POST", "/api/v1/assets", `{"asset_ref":"x","unknown_field":1}`)
	if status != 400 || out["error"] != "bad_request" {
		t.Fatalf("unknown field = %d %v, want 400 bad_request", status, out)
	}
	// 完整声明 → 201,行内字段逐项回显。
	status, out = f.owner(t, "POST", "/api/v1/assets", build(nil))
	if status != 201 {
		t.Fatalf("full declaration = %d %v, want 201", status, out)
	}
	if out["sha256"] != strings.ToLower(shaFixture) || out["media_type"] != "video" || out["candidate"] != false {
		t.Fatalf("row mismatch: %v", out)
	}
}

func TestAssetLibBadRefVersionMismatchRejected(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	f.fup.seedUpload("upl_v", shaFixture)
	// sha256 与平台引用不符(坏引用):409 version_mismatch,绝不落行。
	body := fmt.Sprintf(`{"asset_ref":"upl_v","sha256":%q,"media_type":"video","source":"aicut","purpose":"p","grant_ref":"g"}`, shaOther)
	status, out := f.owner(t, "POST", "/api/v1/assets", body)
	if status != 409 || out["error"] != "version_mismatch" {
		t.Fatalf("bad ref = %d %v, want 409 version_mismatch", status, out)
	}
	status, out = f.owner(t, "GET", "/api/v1/assets", ``)
	items := out["items"].([]any)
	if status != 200 || len(items) != 0 {
		t.Fatalf("bad ref landed a row: %d %v", status, items)
	}
	// 平台上不存在的引用:404 upload_not_found。
	body = fmt.Sprintf(`{"asset_ref":"upl_missing","sha256":%q,"media_type":"bgm","source":"aicut","purpose":"p","grant_ref":"g"}`, shaFixture)
	if status, out = f.owner(t, "POST", "/api/v1/assets", body); status != 404 || out["error"] != "upload_not_found" {
		t.Fatalf("missing upload asset = %d %v, want 404 upload_not_found", status, out)
	}
	// FEATURE_UPLOAD 关:显式 503,不伪造授权。
	f2 := newAssetLibFixture(t, true, false)
	body = fmt.Sprintf(`{"asset_ref":"upl_x","sha256":%q,"media_type":"bgm","source":"aicut","purpose":"p","grant_ref":"g"}`, shaFixture)
	if status, out = f2.owner(t, "POST", "/api/v1/assets", body); status != 503 || out["error"] != "upload_disabled" {
		t.Fatalf("upload off = %d %v, want 503 upload_disabled", status, out)
	}
	// 平台关但池面可用(池是纯组织面):建池不受影响。
	if st, _ := f2.owner(t, "POST", "/api/v1/asset-pools", `{"name":"池"}`); st != 201 {
		t.Fatalf("pool create with upload off = %d, want 201", st)
	}
}

// ---- 3. 导入路径 ------------------------------------------------------------------

func TestAssetLibImportPathRestriction(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	// merchant_upload 不可经导入端点。
	f.fup.seedUpload("upl_imp", shaFixture)
	body := fmt.Sprintf(`{"asset_ref":"upl_imp","sha256":%q,"media_type":"video","source":"merchant_upload","purpose":"p","grant_ref":"g"}`, shaFixture)
	status, out := f.owner(t, "POST", "/api/v1/assets/import", body)
	if status != 422 || out["error"] != "source_not_importable" {
		t.Fatalf("import merchant_upload = %d %v, want 422 source_not_importable", status, out)
	}
	// 同一素材走通用端点:允许(导入限制只在导入端点)。
	if status, _ = f.owner(t, "POST", "/api/v1/assets", body); status != 201 {
		t.Fatalf("register merchant_upload via generic endpoint = %d, want 201", status)
	}
	// 产品图/AiCut 可导入;授权声明矩阵在导入端点同样生效。
	for _, src := range []string{"product_photo", "aicut"} {
		ref := "upl_imp_" + src
		f.fup.seedUpload(ref, shaOther)
		b := fmt.Sprintf(`{"asset_ref":%q,"sha256":%q,"media_type":"image","source":%q,"purpose":"p","grant_ref":"g"}`, ref, shaOther, src)
		if status, out = f.owner(t, "POST", "/api/v1/assets/import", b); status != 201 {
			t.Fatalf("import %s = %d %v, want 201", src, status, out)
		}
	}
	b := fmt.Sprintf(`{"asset_ref":"upl_imp_aicut","sha256":%q,"media_type":"image","source":"aicut","purpose":"p"}`, shaOther)
	if status, out = f.owner(t, "POST", "/api/v1/assets/import", b); status != 422 || out["error"] != "missing_grant_ref" {
		t.Fatalf("import without grant = %d %v, want 422 missing_grant_ref", status, out)
	}
}

// ---- 4. 池 CRUD 与池内引用 --------------------------------------------------------

func TestAssetLibPoolCRUDAndItemsHTTP(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	p1 := f.mustPool(t, "主视频池")
	// 租户内名称唯一。
	status, out := f.owner(t, "POST", "/api/v1/asset-pools", `{"name":"主视频池"}`)
	if status != 409 || out["error"] != "duplicate_pool" {
		t.Fatalf("dup pool = %d %v, want 409 duplicate_pool", status, out)
	}
	// 空名/超长名:422。
	if status, out = f.owner(t, "POST", "/api/v1/asset-pools", `{"name":" "}`); status != 422 || out["error"] != "missing_pool_name" {
		t.Fatalf("empty name = %d %v, want 422 missing_pool_name", status, out)
	}
	// 列表与读取。
	status, out = f.owner(t, "GET", "/api/v1/asset-pools", ``)
	if status != 200 || len(out["items"].([]any)) != 1 {
		t.Fatalf("pool list = %d %v", status, out)
	}
	if status, _ = f.owner(t, "GET", "/api/v1/asset-pools/"+p1, ``); status != 200 {
		t.Fatalf("pool get = %d", status)
	}
	// 改名 + 撞名冲突。
	if status, _ = f.owner(t, "PATCH", "/api/v1/asset-pools/"+p1, `{"name":"备用池"}`); status != 200 {
		t.Fatalf("rename = %d", status)
	}
	f.mustPool(t, "占名池")
	if status, out = f.owner(t, "PATCH", "/api/v1/asset-pools/"+p1, `{"name":"占名池"}`); status != 409 {
		t.Fatalf("rename dup = %d %v, want 409", status, out)
	}
	// 池内引用:增/重 409/未知素材 404/列表/删。
	a1 := f.mustRegister(t, "upl_p1", shaFixture, "video")
	a2 := f.mustRegister(t, "upl_p2", shaOther, "bgm")
	f.mustItem(t, p1, a1)
	f.mustItem(t, p1, a2)
	status, out = f.owner(t, "POST", "/api/v1/asset-pools/"+p1+"/items", fmt.Sprintf(`{"asset_id":%q}`, a1))
	if status != 409 || out["error"] != "duplicate_pool_item" {
		t.Fatalf("dup item = %d %v, want 409 duplicate_pool_item", status, out)
	}
	status, _ = f.owner(t, "POST", "/api/v1/asset-pools/"+p1+"/items", `{"asset_id":"las_missing"}`)
	if status != 404 {
		t.Fatalf("unknown asset item = %d, want 404", status)
	}
	status, out = f.owner(t, "GET", "/api/v1/asset-pools/"+p1+"/items", ``)
	items := out["items"].([]any)
	if status != 200 || len(items) != 2 {
		t.Fatalf("items = %d %v, want 2", status, out)
	}
	if items[0].(map[string]any)["asset_id"] != a1 {
		t.Fatalf("item order not deterministic: %v", items)
	}
	itemID := items[0].(map[string]any)["id"].(string)
	if status, _ = f.owner(t, "DELETE", "/api/v1/asset-pools/"+p1+"/items/"+itemID, ``); status != 200 {
		t.Fatalf("item remove = %d", status)
	}
	if status, _ = f.owner(t, "DELETE", "/api/v1/asset-pools/"+p1+"/items/"+itemID, ``); status != 404 {
		t.Fatalf("double item remove = %d, want 404", status)
	}
	// 删池:池消失;台账(无)不受影响;再删 404。
	if status, _ = f.owner(t, "DELETE", "/api/v1/asset-pools/"+p1, ``); status != 200 {
		t.Fatalf("pool delete = %d", status)
	}
	if status, _ = f.owner(t, "GET", "/api/v1/asset-pools/"+p1, ``); status != 404 {
		t.Fatalf("pool after delete = %d, want 404", status)
	}
}

// ---- 5. 确定性调取 + 台账冻结 + 幂等 ----------------------------------------------

func TestAssetLibDrawDeterministicIdempotent(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	p := f.mustPool(t, "视频池")
	var ids []string
	for i := 0; i < 4; i++ {
		ids = append(ids, f.mustRegister(t, fmt.Sprintf("upl_d%d", i), fmt.Sprintf("%064x", 100+i), "video"))
	}
	for _, id := range ids {
		f.mustItem(t, p, id)
	}
	day := "2026-09-20"
	drawBody := func(key string) string { return fmt.Sprintf(`{"select_key":%q,"day_utc":%q}`, key, day) }

	// 首次调取:确定性 = 纯函数 PickIndex 对 (pool, day) 的期望值。
	status, out := f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", drawBody("hero"))
	if status != 200 || out["replayed"] != false {
		t.Fatalf("first draw = %d %v", status, out)
	}
	want := ids[assetlib.PickIndex(p, day, len(ids))]
	if out["selection"].(map[string]any)["asset_id"] != want {
		t.Fatalf("draw = %v, want asset %s", out["selection"], want)
	}
	// 同键重复选择:幂等重放,同一冻结行。
	status, out2 := f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", drawBody("hero"))
	if status != 200 || out2["replayed"] != true {
		t.Fatalf("replay = %d %v", status, out2)
	}
	if out2["selection"].(map[string]any)["id"] != out["selection"].(map[string]any)["id"] {
		t.Fatalf("replay row changed: %v vs %v", out, out2)
	}
	// 同日异键:同种子同选,但分键记行。
	status, out3 := f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", drawBody("second"))
	if status != 200 || out3["replayed"] != false {
		t.Fatalf("second key draw = %d %v", status, out3)
	}
	if out3["selection"].(map[string]any)["asset_id"] != want {
		t.Fatalf("same seed different key picked %v, want %s", out3["selection"], want)
	}
	// 调取不改变池。
	status, out = f.owner(t, "GET", "/api/v1/asset-pools/"+p+"/items", ``)
	if status != 200 || len(out["items"].([]any)) != 4 {
		t.Fatalf("pool mutated by draws: %d %v", status, out)
	}
	// 台账:3 行(hero/second + 无候选)。
	status, out = f.owner(t, "GET", "/api/v1/asset-pools/"+p+"/selections", ``)
	if status != 200 || len(out["items"].([]any)) != 2 {
		t.Fatalf("ledger = %d %v, want 2", status, out)
	}
	// 空池 409;缺 select_key 422;坏 day 422。
	pe := f.mustPool(t, "空池")
	if status, out = f.owner(t, "POST", "/api/v1/asset-pools/"+pe+"/draw", drawBody("k")); status != 409 || out["error"] != "empty_pool" {
		t.Fatalf("empty pool = %d %v, want 409 empty_pool", status, out)
	}
	if status, out = f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", `{"day_utc":"`+day+`"}`); status != 422 || out["error"] != "missing_select_key" {
		t.Fatalf("missing key = %d %v, want 422 missing_select_key", status, out)
	}
	if status, out = f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", `{"select_key":"k","day_utc":"2026/09/20"}`); status != 422 || out["error"] != "bad_day_utc" {
		t.Fatalf("bad day = %d %v, want 422 bad_day_utc", status, out)
	}
}

func TestAssetLibSelectionFreezeOnNewVersion(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	p := f.mustPool(t, "冻结池")
	f.fup.seedUpload("upl_freeze", shaFixture)
	a1 := f.mustRegister(t, "upl_freeze", shaFixture, "video")
	f.mustItem(t, p, a1)
	day := "2026-09-20"
	body := fmt.Sprintf(`{"select_key":"k","day_utc":%q}`, day)
	status, out := f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", body)
	if status != 200 {
		t.Fatalf("draw = %d %v", status, out)
	}
	// 源素材更新 = 同 ref 新 sha 新行(显式登记);已选行冻结不变。
	f.fup.seedUpload("upl_freeze_v2", shaOther)
	a2 := f.mustRegister(t, "upl_freeze_v2", shaOther, "video")
	if a2 == a1 {
		t.Fatal("new version must be a new row")
	}
	f.mustItem(t, p, a2)
	// 重放同键:返回冻结行(v1),不是池内新版本。
	status, out = f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", body)
	sel := out["selection"].(map[string]any)
	if status != 200 || out["replayed"] != true || sel["asset_id"] != a1 || sel["sha256"] != strings.ToLower(shaFixture) {
		t.Fatalf("frozen replay broken: %d %v", status, out)
	}
	// 台账行 asset_ref/sha256 快照与 v1 一致,只追加。
	status, out = f.owner(t, "GET", "/api/v1/asset-pools/"+p+"/selections", ``)
	items := out["items"].([]any)
	if status != 200 || len(items) != 1 {
		t.Fatalf("ledger = %d rows, want 1", len(items))
	}
	row := items[0].(map[string]any)
	if row["asset_ref"] != "upl_freeze" || row["sha256"] != strings.ToLower(shaFixture) {
		t.Fatalf("ledger snapshot = %v, want frozen v1", row)
	}
}

func TestAssetLibCandidateDrawSemantics(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	p := f.mustPool(t, "候选池")
	a := f.mustRegister(t, "upl_c", shaFixture, "video")
	f.mustItem(t, p, a)
	day := "2026-09-20"
	official := fmt.Sprintf(`{"select_key":"k","day_utc":%q}`, day)
	if status, _ := f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", official); status != 200 {
		t.Fatal("official draw failed")
	}
	// 候选调取走独立键空间:不替换正式行;同键候选自身幂等。
	cand := fmt.Sprintf(`{"select_key":"k","day_utc":%q,"candidate":true}`, day)
	status, out := f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", cand)
	if status != 200 || out["replayed"] != false {
		t.Fatalf("candidate draw = %d %v", status, out)
	}
	if sel := out["selection"].(map[string]any); sel["candidate"] != true {
		t.Fatalf("candidate flag missing: %v", sel)
	}
	status, out2 := f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", cand)
	if status != 200 || out2["replayed"] != true ||
		out2["selection"].(map[string]any)["id"] != out["selection"].(map[string]any)["id"] {
		t.Fatalf("candidate replay = %d %v", status, out2)
	}
	// 正式行未受候选影响(重放仍 false→true 同一行)。
	status, out3 := f.owner(t, "POST", "/api/v1/asset-pools/"+p+"/draw", official)
	if status != 200 || out3["replayed"] != true || out3["selection"].(map[string]any)["candidate"] != false {
		t.Fatalf("official row disturbed by candidate: %d %v", status, out3)
	}
	// 台账 2 行(official + candidate),只追加。
	status, out = f.owner(t, "GET", "/api/v1/asset-pools/"+p+"/selections", ``)
	if status != 200 || len(out["items"].([]any)) != 2 {
		t.Fatalf("ledger = %d %v, want 2", status, out)
	}
}

// ---- 6. 候选标记 / 跨租户 / authz --------------------------------------------------

func TestAssetLibCandidateFlagEndpoint(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	// candidate=true 与正式行并存。
	f.fup.seedUpload("upl_flag", shaFixture)
	candReg := fmt.Sprintf(`{"asset_ref":"upl_flag","sha256":%q,"media_type":"video","source":"aicut","purpose":"p","grant_ref":"g","candidate":true}`, shaFixture)
	if status, out := f.owner(t, "POST", "/api/v1/assets", candReg); status != 201 || out["candidate"] != true {
		t.Fatalf("candidate register = %d %v", status, out)
	}
	// 同 ref+sha 不带 candidate 字段 = 正式行,与候选行并存。
	reg := fmt.Sprintf(`{"asset_ref":"upl_flag","sha256":%q,"media_type":"video","source":"aicut","purpose":"p","grant_ref":"g"}`, shaFixture)
	if status, out := f.owner(t, "POST", "/api/v1/assets", reg); status != 201 || out["candidate"] != false {
		t.Fatalf("official co-register = %d %v, want 201 (formal+candidate coexist)", status, out)
	}
	// 同键重复登记 = 409(正式行自己也不能重复)。
	if status, _ := f.owner(t, "POST", "/api/v1/assets", reg); status != 409 {
		t.Fatalf("duplicate official = %d, want 409", status)
	}
	// 候选标记端点:只改标记,不动引用/版本(用无同键兄弟行的素材验证翻转;
	// 若同键已有目标标记的行,则 409 拒绝、绝不改写 —— 已由 store 层覆盖)。
	f.fup.seedUpload("upl_flip", shaOther)
	flipID := func() string {
		b := fmt.Sprintf(`{"asset_ref":"upl_flip","sha256":%q,"media_type":"image","source":"product_photo","purpose":"p","grant_ref":"g"}`, shaOther)
		st, out := f.owner(t, "POST", "/api/v1/assets", b)
		if st != 201 {
			t.Fatalf("flip fixture register = %d %v", st, out)
		}
		return out["id"].(string)
	}()
	status, out := f.owner(t, "POST", "/api/v1/assets/"+flipID+"/candidate", `{"candidate":true}`)
	if status != 200 || out["candidate"] != true || out["asset_ref"] != "upl_flip" || out["sha256"] != strings.ToLower(shaOther) {
		t.Fatalf("flag flip = %d %v (must not mutate ref/sha)", status, out)
	}
	// 翻回 false 同样只改标记。
	status, out = f.owner(t, "POST", "/api/v1/assets/"+flipID+"/candidate", `{"candidate":false}`)
	if status != 200 || out["candidate"] != false {
		t.Fatalf("flag flip back = %d %v", status, out)
	}
	// 跨租户与未知 id:404 不可见。
	if status, _ = f.do(t, "POST", "/api/v1/assets/"+flipID+"/candidate", "sess-owner-b", f.tenB, `{"candidate":true}`); status != 404 {
		t.Fatalf("cross-tenant candidate = %d, want 404", status)
	}
	if status, _ = f.owner(t, "POST", "/api/v1/assets/las_missing/candidate", `{"candidate":true}`); status != 404 {
		t.Fatalf("unknown id candidate = %d, want 404", status)
	}
}

func TestAssetLibCrossTenantInvisible(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	aid := f.mustRegister(t, "upl_ct", shaFixture, "video")
	pid := f.mustPool(t, "A池")
	f.mustItem(t, pid, aid)
	// B 的 owner 对 A 的素材/池/台账:一律 404 / 空列表。
	if status, _ := f.do(t, "GET", "/api/v1/assets/"+aid, "sess-owner-b", f.tenB, ``); status != 404 {
		t.Fatalf("cross-tenant asset = %d, want 404", status)
	}
	if status, _ := f.do(t, "GET", "/api/v1/asset-pools/"+pid, "sess-owner-b", f.tenB, ``); status != 404 {
		t.Fatalf("cross-tenant pool = %d, want 404", status)
	}
	if status, out := f.do(t, "GET", "/api/v1/assets", "sess-owner-b", f.tenB, ``); status != 200 || len(out["items"].([]any)) != 0 {
		t.Fatalf("cross-tenant list = %d %v, want 0 rows", status, out)
	}
	if status, _ := f.do(t, "POST", "/api/v1/asset-pools/"+pid+"/draw", "sess-owner-b", f.tenB, `{"select_key":"k","day_utc":"2026-09-20"}`); status != 404 {
		t.Fatalf("cross-tenant draw = %d, want 404", status)
	}
}

func TestAssetLibAuthzRoles(t *testing.T) {
	f := newAssetLibFixture(t, true, true)
	// staff 不能登记/管理素材(403 forbidden)。
	f.fup.seedUpload("upl_auth", shaFixture)
	if status, out := f.registerAsset(t, "sess-staff-a", f.tenA, "upl_auth", shaFixture, "video", "aicut"); status != 403 || out["error"] != "forbidden" {
		t.Fatalf("staff register = %d %v, want 403 forbidden", status, out)
	}
	if status, out := f.owner(t, "POST", "/api/v1/asset-pools", `{"name":"池"}`); status != 201 {
		t.Fatalf("owner pool create = %d %v", status, out)
	}
	pid := func() string {
		_, out := f.owner(t, "GET", "/api/v1/asset-pools", ``)
		return out["items"].([]any)[0].(map[string]any)["id"].(string)
	}()
	if status, out := f.do(t, "PATCH", "/api/v1/asset-pools/"+pid, "sess-staff-a", f.tenA, `{"name":"改名"}`); status != 403 {
		t.Fatalf("staff pool patch = %d %v, want 403", status, out)
	}
	// 池是租户级记录:门店经理按既有纪律 out_of_scope(403),不可调取。
	if status, out := f.do(t, "POST", "/api/v1/asset-pools/"+pid+"/draw", "sess-sm-a", f.tenA, `{"select_key":"k","day_utc":"2026-09-20"}`); status != 403 || out["error"] != "out_of_scope" {
		t.Fatalf("sm draw = %d %v, want 403 out_of_scope", status, out)
	}
	// staff 可调取(既有业务角色)。
	aid := f.mustRegister(t, "upl_draw", shaOther, "video")
	f.mustItem(t, pid, aid)
	if status, out := f.do(t, "POST", "/api/v1/asset-pools/"+pid+"/draw", "sess-staff-a", f.tenA, `{"select_key":"k","day_utc":"2026-09-20"}`); status != 200 {
		t.Fatalf("staff draw = %d %v, want 200", status, out)
	}
}

// shaFixture / shaOther are deterministic 64-hex fixture fingerprints.
var (
	shaFixture = fmt.Sprintf("%064x", 7)
	shaOther   = fmt.Sprintf("%064x", 8)
)
