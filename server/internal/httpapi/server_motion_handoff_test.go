package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/brandctx"
	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/storemotion"
)

func handoffSeed(t *testing.T, f *fixture) (string, string) {
	t.Helper()
	sto, err := f.s.St.CreateStore(f.tenA, "南山店", "南山大道1号", f.ownA)
	mustNoErr(t, err)
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "国庆档", PublicContent: "第二杯半价", StoreID: sto.ID, CreatedBy: f.ownA})
	mustNoErr(t, err)
	return "/api/v1/campaigns/" + camp.ID + "/motion-handoff", camp.ID
}

func TestMotionHandoffAccessControl(t *testing.T) {
	f := newFixture(t, false)
	path, _ := handoffSeed(t, f)

	status, _, out := f.do(t, "GET", path, "", f.tenA, "")
	if status != 401 {
		t.Fatalf("guest = %d %v", status, out)
	}
	status, _, out = f.do(t, "GET", path, "sess-owner-b", f.tenB, "")
	if status != 404 || out["error"] != "not_found" {
		t.Fatalf("cross tenant = %d %v", status, out)
	}
}

func TestMotionHandoffStoreRequired(t *testing.T) {
	f := newFixture(t, false)
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "没有门店", CreatedBy: f.ownA})
	mustNoErr(t, err)
	path := "/api/v1/campaigns/" + camp.ID + "/motion-handoff"
	status, _, out := f.do(t, "GET", path, "sess-owner-a", f.tenA, "")
	if status != 400 || out["error"] != "store_required" {
		t.Fatalf("no store = %d %v", status, out)
	}
}

func TestMotionHandoffProjectsAndDigestStable(t *testing.T) {
	f := newFixture(t, false)
	probe := &storemotion.Probe{}
	f.s.StoreMotionProbe = probe
	path, campID := handoffSeed(t, f)

	status, _, out := f.do(t, "PUT", "/api/v1/campaigns/"+campID+"/store-motion", "sess-owner-a", f.tenA,
		`{"store_name":"南山店","activity_time":"10月1日-10月7日","price":"19.9","address":"南山大道1号","offer_copy":"第二杯半价","cta":"进店领取","channels":"wechat_grid,table_tent","aspect_ratios":"9:16"}`)
	if status != 201 {
		t.Fatalf("seed motion = %d %v", status, out)
	}
	if _, err := f.s.St.AddCampaignAsset(f.tenA, campID, "ast_good", "v3", f.ownA); err != nil {
		t.Fatal(err)
	}
	if err := f.s.St.ReplaceExtraJumps(f.tenA, campID, []extrajump.Configured{
		{Kind: extrajump.KindWifi, Enabled: true},
		{Kind: extrajump.KindNavigate, Enabled: true, Revoked: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.s.St.SaveJumpMatrix(f.tenA, campID, nil, &extrajump.ReturnConfig{Href: "https://shop.example.com/back", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.CreateLink(f.tenA, campID, f.ownA); err != nil {
		t.Fatal(err)
	}

	status, _, brief := f.do(t, "GET", path, "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("brief = %d %v", status, brief)
	}
	if brief["version"] != "touch-motion-handoff/v1alpha1" {
		t.Fatalf("version = %v", brief["version"])
	}
	ref, _ := brief["origin_context_ref"].(string)
	if !strings.HasPrefix(ref, "touch:tenant/"+f.tenA+"/store/") || !strings.Contains(ref, campID) {
		t.Fatalf("origin_context_ref = %q", ref)
	}
	camp := brief["campaign"].(map[string]any)
	if camp["title"] != "国庆档" || camp["public_content"] != "第二杯半价" || camp["status"] != "draft" {
		t.Fatalf("campaign = %v", camp)
	}
	sto := brief["store"].(map[string]any)
	if sto["name"] != "南山店" || sto["address"] != "南山大道1号" || sto["status"] != "active" {
		t.Fatalf("store = %v", sto)
	}
	offer := brief["offer"].(map[string]any)
	if offer["offer_copy"] != "第二杯半价" || offer["price"] != "19.9" {
		t.Fatalf("offer = %v", offer)
	}
	if brief["cta"] != "进店领取" || brief["params_version"] != float64(1) {
		t.Fatalf("cta/params_version = %v %v", brief["cta"], brief["params_version"])
	}
	channels, ok := brief["channels"].([]any)
	if !ok || len(channels) != 2 || channels[0] != "wechat_grid" {
		t.Fatalf("channels = %v", brief["channels"])
	}
	ratios, ok := brief["aspect_ratios"].([]any)
	if !ok || len(ratios) != 1 || ratios[0] != "9:16" {
		t.Fatalf("aspect_ratios = %v", brief["aspect_ratios"])
	}
	landing := brief["landing"].(map[string]any)
	if sc, _ := landing["short_code"].(string); sc == "" {
		t.Fatalf("short_code missing: %v", landing)
	}
	kinds, ok := landing["extra_jump_kinds"].([]any)
	if !ok || len(kinds) != 1 || kinds[0] != "wifi" {
		t.Fatalf("revoked jump leaked: %v", landing["extra_jump_kinds"])
	}
	ret, ok := landing["authorized_return"].(map[string]any)
	if !ok || ret["href"] != "https://shop.example.com/back" {
		t.Fatalf("authorized_return = %v", landing["authorized_return"])
	}
	assets, ok := brief["assets"].([]any)
	if !ok || len(assets) != 1 {
		t.Fatalf("assets = %v", brief["assets"])
	}
	asset := assets[0].(map[string]any)
	if asset["asset_id"] != "ast_good" || asset["version"] != "v3" {
		t.Fatalf("asset = %v", asset)
	}
	digest, _ := brief["digest"].(string)
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("digest = %q", digest)
	}
	disp := brief["disposition"].(map[string]any)
	if disp["motion_consumable"] != false || disp["reason_code"] != "upstream_unavailable" {
		t.Fatalf("disposition = %v", disp)
	}
	wantRequired := []any{"public-ai sdk/go motion export", "HUI-2732", "HUI-2733"}
	gotRequired, _ := disp["required"].([]any)
	if len(gotRequired) != 3 {
		t.Fatalf("required = %v", disp["required"])
	}
	for i, want := range wantRequired {
		if gotRequired[i] != want {
			t.Fatalf("required[%d] = %v, want %v", i, gotRequired[i], want)
		}
	}
	// fixture 默认品牌关闭：诚实显示不可用，不是成功。
	brand := brief["brand"].(map[string]any)
	if brand["context_available"] != false || brand["context_unavailable_reason"] != "brand_client_not_configured" {
		t.Fatalf("brand = %v", brand)
	}
	// never 调 probe。
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 || len(probe.Payloads) != 0 {
		t.Fatalf("handoff called the probe: %+v", probe)
	}
	// 同数据两次：digest 稳定（generated_at 是服务时刻，不进 digest）。
	status, _, again := f.do(t, "GET", path, "sess-owner-a", f.tenA, "")
	if status != 200 || again["digest"] != digest {
		t.Fatalf("digest not stable: %v vs %v", again["digest"], digest)
	}
}

func TestMotionHandoffDigestFollowsParamsVersion(t *testing.T) {
	f := newFixture(t, false)
	path, campID := handoffSeed(t, f)
	motionPath := "/api/v1/campaigns/" + campID + "/store-motion"
	body := `{"store_name":"南山店","activity_time":"10月1日","price":"19.9","address":"南山大道1号","offer_copy":"第二杯半价","cta":"进店领取"}`

	_, _, empty := f.do(t, "GET", path, "sess-owner-a", f.tenA, "")
	if empty["params_version"] != float64(0) {
		t.Fatalf("params_version = %v", empty["params_version"])
	}
	// 空串投影为空数组，不是 null。
	if _, ok := empty["channels"].([]any); !ok {
		t.Fatalf("channels not an empty array: %v", empty["channels"])
	}
	if _, ok := empty["aspect_ratios"].([]any); !ok {
		t.Fatalf("aspect_ratios not an empty array: %v", empty["aspect_ratios"])
	}
	digest0, _ := empty["digest"].(string)

	status, _, _ := f.do(t, "PUT", motionPath, "sess-owner-a", f.tenA, body)
	if status != 201 {
		t.Fatalf("seed = %d", status)
	}
	_, _, v1 := f.do(t, "GET", path, "sess-owner-a", f.tenA, "")
	digest1, _ := v1["digest"].(string)
	if v1["params_version"] != float64(1) || digest1 == digest0 {
		t.Fatalf("v1 = %v digest0=%s digest1=%s", v1["params_version"], digest0, digest1)
	}

	status, _, _ = f.do(t, "PUT", motionPath, "sess-owner-a", f.tenA, strings.Replace(body, `"19.9"`, `"29.9"`, 1))
	if status != 201 {
		t.Fatalf("change = %d", status)
	}
	_, _, v2 := f.do(t, "GET", path, "sess-owner-a", f.tenA, "")
	digest2, _ := v2["digest"].(string)
	if v2["params_version"] != float64(2) || digest2 == digest1 {
		t.Fatalf("v2 = %v digest1=%s digest2=%s", v2["params_version"], digest1, digest2)
	}
}

// doHost is f.do plus the BFF-injected public host header, which attachBrand
// requires whenever FEATURE_BRAND is on (an IP literal is not a brand host).
func (f *fixture) doHost(t *testing.T, method, path, session, tenant, host, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	mustNoErr(t, err)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	if session != "" {
		req.Header.Set("X-Session-Token", session)
	}
	if tenant != "" {
		req.Header.Set("X-Tenant-ID", tenant)
	}
	req.Header.Set("X-Touch-Public-Host", host)
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

func TestMotionHandoffBrandContextHonest(t *testing.T) {
	f := newFixture(t, false)
	path, campID := handoffSeed(t, f)
	mustNoErr(t, f.s.St.BindTenantBrand(f.tenA, "brd_a"))
	link, err := f.s.St.CreateLink(f.tenA, campID, f.ownA)
	mustNoErr(t, err)
	mustNoErr(t, f.s.St.StampLinkBrand(link.ID, f.tenA, "brd_a", "touch.example.com"))

	// attachBrand 在 FEATURE_BRAND=on 时对所有会话请求先行 fail-closed：
	// gate 要求 PLATFORM_BRAND_BASE_URL/TOKEN 非空，请求必须带品牌 host，
	// 注册表不可达时整个请求 503。这里补齐 gate 占位值，实际读取走 s.Brand。
	f.s.Cfg.FeatureBrand = true
	f.s.Cfg.BrandBaseURL = "http://brand-registry.invalid"
	f.s.Cfg.BrandToken = "t"

	// 注册表连不上：平台 gate 先行 503，绝不返回一份伪造可用的 brief。
	f.s.Brand = &brandctx.Client{BaseURL: "http://127.0.0.1:1", Token: "t"}
	status, out := f.doHost(t, "GET", path, "sess-owner-a", f.tenA, "touch.example.com", "")
	if status != 503 || out["error"] != "brand_unavailable" {
		t.Fatalf("unreachable registry = %d %v", status, out)
	}

	// 注册表可达但品牌 suspended：GET 放行，brief 诚实降级 brand_suspended，
	// 铸码戳照给，disposition 不变。
	suspended := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"brand_id": "brd_a", "status": "suspended", "config_version": 3, "apps": []any{},
		})
	}))
	defer suspended.Close()
	f.s.Brand = &brandctx.Client{BaseURL: suspended.URL, Token: "t"}
	status, brief := f.doHost(t, "GET", path, "sess-owner-a", f.tenA, "touch.example.com", "")
	if status != 200 {
		t.Fatalf("suspended brief = %d %v", status, brief)
	}
	brand := brief["brand"].(map[string]any)
	if brand["context_available"] != false || brand["context_unavailable_reason"] != "brand_suspended" {
		t.Fatalf("suspended brand = %v", brand)
	}
	if brand["brand_id"] != "brd_a" || brand["published_brand_id"] != "brd_a" || brand["published_host"] != "touch.example.com" {
		t.Fatalf("brand = %v", brand)
	}
	disp := brief["disposition"].(map[string]any)
	if disp["reason_code"] != "upstream_unavailable" {
		t.Fatalf("disposition changed with brand = %v", disp)
	}

	// 本地桩注册表就绪：context_available=true + 配置版本，disposition 不变。
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"brand_id": "brd_a", "status": "active", "config_version": 7, "apps": []any{},
		})
	}))
	defer registry.Close()
	f.s.Brand = &brandctx.Client{BaseURL: registry.URL, Token: "t"}
	status, brief = f.doHost(t, "GET", path, "sess-owner-a", f.tenA, "touch.example.com", "")
	if status != 200 {
		t.Fatalf("ready brand = %d", status)
	}
	brand = brief["brand"].(map[string]any)
	if brand["context_available"] != true || brand["brand_config_version"] != float64(7) {
		t.Fatalf("ready brand = %v", brand)
	}
	if _, present := brand["context_unavailable_reason"]; present {
		t.Fatalf("reason leaked = %v", brand["context_unavailable_reason"])
	}
	disp = brief["disposition"].(map[string]any)
	if disp["reason_code"] != "upstream_unavailable" {
		t.Fatalf("disposition changed with brand = %v", disp)
	}
}
