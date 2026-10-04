package httpapi

import (
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/storemotion"
)

func TestStoreMotionParamChangeCallsNoModel(t *testing.T) {
	f := newFixture(t, false)
	probe := &storemotion.Probe{}
	f.s.StoreMotionProbe = probe
	sto, err := f.s.St.CreateStore(f.tenA, "南山店", "原地址", f.ownA)
	mustNoErr(t, err)
	stoB, err := f.s.St.CreateStore(f.tenA, "福田店", "福田路2号", f.ownA)
	mustNoErr(t, err)
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "国庆", StoreID: sto.ID, CreatedBy: f.ownA})
	mustNoErr(t, err)
	campB, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "国庆", StoreID: stoB.ID, CreatedBy: f.ownA})
	mustNoErr(t, err)
	path := "/api/v1/campaigns/" + camp.ID + "/store-motion"
	body := `{"store_name":"南山店","activity_time":"10月1日-10月7日","price":"19.9","address":"南山大道1号","offer_copy":"第二杯半价","cta":"进店领取"}`

	status, _, out := f.do(t, "PUT", path, "sess-owner-a", f.tenA, body)
	if status != 201 {
		t.Fatalf("create = %d %v", status, out)
	}
	decl := out["declaration"].(map[string]any)
	if decl["origin_app"] != "touch" || decl["status"] != "还没生成成片" || decl["verified"] != false {
		t.Fatalf("declaration = %v", decl)
	}
	if decl["revision_id"] != nil {
		t.Fatalf("revision = %v", decl["revision_id"])
	}
	ref, _ := decl["origin_context_ref"].(string)
	if !strings.Contains(ref, sto.ID) || !strings.Contains(ref, camp.ID) || !strings.Contains(ref, f.tenA) {
		t.Fatalf("origin_context_ref = %q", ref)
	}
	if decl["model_calls"] != float64(0) || decl["render_calls"] != float64(0) || decl["unchanged_store_rerun"] != "not_claimed" {
		t.Fatalf("declaration calls = %v", decl)
	}
	if _, ok := out["video_url"]; ok {
		t.Fatal("response carried a video")
	}

	changed := strings.Replace(body, `"19.9"`, `"29.9"`, 1)
	status, _, out = f.do(t, "PUT", path, "sess-owner-a", f.tenA, changed)
	if status != 201 || out["version"] != float64(2) {
		t.Fatalf("price change = %d %v", status, out)
	}
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 || len(probe.Payloads) != 0 {
		t.Fatalf("model calls = %d render = %d payloads = %d", probe.ModelCalls, probe.RenderCalls, len(probe.Payloads))
	}

	otherBody := `{"store_name":"福田店","activity_time":"10月1日-10月7日","price":"19.9","address":"福田路2号","offer_copy":"第二杯半价","cta":"进店领取"}`
	status, _, other := f.do(t, "PUT", "/api/v1/campaigns/"+campB.ID+"/store-motion", "sess-owner-a", f.tenA, otherBody)
	if status != 201 {
		t.Fatalf("other store = %d %v", status, other)
	}
	status, _, out = f.do(t, "PUT", path, "sess-owner-a", f.tenA, strings.Replace(changed, `"29.9"`, `"8元"`, 1))
	if status != 201 {
		t.Fatalf("second change = %d %v", status, out)
	}
	status, _, kept := f.do(t, "GET", "/api/v1/campaigns/"+campB.ID+"/store-motion", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("get other = %d %v", status, kept)
	}
	keptDecl := kept["declaration"].(map[string]any)
	keptParams := kept["params"].(map[string]any)
	if kept["version"] != float64(1) || keptParams["price"] != "19.9" || keptDecl["unchanged_store_rerun"] != "not_claimed" {
		t.Fatalf("unchanged store was treated as a skipped rerun: %v", kept)
	}
	note, _ := keptDecl["unchanged_store_note"].(string)
	if !strings.Contains(note, "HUI-2732") || !strings.Contains(note, "不能声称") {
		t.Fatalf("note = %q", note)
	}
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 {
		t.Fatalf("model calls after unchanged store = %d", probe.ModelCalls)
	}

	status, _, same := f.do(t, "PUT", path, "sess-owner-a", f.tenA, strings.Replace(changed, `"29.9"`, `"8元"`, 1))
	if status != 200 || same["version"] != float64(3) {
		t.Fatalf("replay = %d %v", status, same)
	}

	var calls int
	if err := f.s.St.DB.QueryRow(`SELECT COALESCE(SUM(model_calls)+SUM(render_calls),0) FROM store_motion_requests`).Scan(&calls); err != nil || calls != 0 {
		t.Fatalf("stored calls = %d %v", calls, err)
	}
	var storeName string
	if err := f.s.St.DB.QueryRow(`SELECT name FROM stores WHERE id=?`, sto.ID).Scan(&storeName); err != nil || storeName != "南山店" {
		t.Fatalf("store row changed: %q %v", storeName, err)
	}
}

func TestStoreMotionRefusesQRPriceNumberAndPrivacy(t *testing.T) {
	f := newFixture(t, false)
	probe := &storemotion.Probe{}
	f.s.StoreMotionProbe = probe
	sto, err := f.s.St.CreateStore(f.tenA, "南山店", "原地址", f.ownA)
	mustNoErr(t, err)
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "国庆", StoreID: sto.ID, CreatedBy: f.ownA})
	mustNoErr(t, err)
	path := "/api/v1/campaigns/" + camp.ID + "/store-motion"
	base := `"store_name":"南山店","activity_time":"10月1日-10月7日","address":"南山大道1号","offer_copy":"第二杯半价","cta":"进店领取"`

	status, _, out := f.do(t, "PUT", path, "sess-owner-a", f.tenA, `{`+base+`,"price":19.9}`)
	if status != 400 || out["error"] != "price_not_string" {
		t.Fatalf("numeric price = %d %v", status, out)
	}
	status, _, out = f.do(t, "PUT", path, "sess-owner-a", f.tenA, `{`+base+`,"price":"19.9","qr_code":"https://qr.example/a","phone":"13800138000"}`)
	if status != 400 || out["error"] != "forbidden_field" {
		t.Fatalf("qr field = %d %v", status, out)
	}
	status, _, out = f.do(t, "PUT", path, "sess-owner-a", f.tenA, `{`+base+`,"price":"19.9","cta":"联系 13800138000"}`)
	if status != 400 || out["error"] != "privacy_refused" {
		t.Fatalf("phone cta = %d %v", status, out)
	}
	status, _, out = f.do(t, "PUT", path, "", f.tenA, `{`+base+`,"price":"19.9"}`)
	if status != 401 {
		t.Fatalf("guest = %d %v", status, out)
	}
	status, _, out = f.do(t, "PUT", path, "sess-owner-b", f.tenB, `{`+base+`,"price":"19.9"}`)
	if status != 404 || out["error"] != "not_found" {
		t.Fatalf("cross tenant = %d %v", status, out)
	}
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 || len(probe.Payloads) != 0 {
		t.Fatalf("refused body still called the model: %+v", probe)
	}
	var rows int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM store_motion_requests`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("rows = %d %v", rows, err)
	}
	var leaked int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM store_motion_requests WHERE offer_copy LIKE '%qr%' OR cta LIKE '%138%' OR price LIKE '%19.9%'`).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatalf("leaked = %d %v", leaked, err)
	}
}

func TestStoreMotionChannelsRoundTripFormatAndPrivacy(t *testing.T) {
	f := newFixture(t, false)
	sto, err := f.s.St.CreateStore(f.tenA, "南山店", "原地址", f.ownA)
	mustNoErr(t, err)
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "国庆", StoreID: sto.ID, CreatedBy: f.ownA})
	mustNoErr(t, err)
	path := "/api/v1/campaigns/" + camp.ID + "/store-motion"
	base := `"store_name":"南山店","activity_time":"10月1日-10月7日","price":"19.9","address":"南山大道1号","offer_copy":"第二杯半价","cta":"进店领取"`

	// 旧六字段客户端：不带新字段，照常 201，新字段回空串。
	status, _, out := f.do(t, "PUT", path, "sess-owner-a", f.tenA, `{`+base+`}`)
	if status != 201 {
		t.Fatalf("legacy body = %d %v", status, out)
	}
	params := out["params"].(map[string]any)
	if params["channels"] != "" || params["aspect_ratios"] != "" {
		t.Fatalf("legacy params = %v", params)
	}

	status, _, out = f.do(t, "PUT", path, "sess-owner-a", f.tenA, `{`+base+`,"channels":"wechat_grid,table_tent","aspect_ratios":"9:16"}`)
	if status != 201 || out["version"] != float64(2) {
		t.Fatalf("channels body = %d %v", status, out)
	}
	params = out["params"].(map[string]any)
	if params["channels"] != "wechat_grid,table_tent" || params["aspect_ratios"] != "9:16" {
		t.Fatalf("params = %v", params)
	}
	// 八字段全同：幂等 200，不新增版本。
	status, _, same := f.do(t, "PUT", path, "sess-owner-a", f.tenA, `{`+base+`,"channels":"wechat_grid,table_tent","aspect_ratios":"9:16"}`)
	if status != 200 || same["version"] != float64(2) {
		t.Fatalf("replay = %d %v", status, same)
	}
	// 只改 channels：出新版本。
	status, _, next := f.do(t, "PUT", path, "sess-owner-a", f.tenA, `{`+base+`,"channels":"wechat_grid","aspect_ratios":"9:16"}`)
	if status != 201 || next["version"] != float64(3) {
		t.Fatalf("channels change = %d %v", status, next)
	}

	// 非法格式与超长：400 invalid_field。
	for _, bad := range []string{
		`"channels":"WeChat"`,
		`"channels":"wechat grid"`,
		`"channels":"` + strings.Repeat("a", 121) + `"`,
		`"aspect_ratios":"9：16"`,
		`"aspect_ratios":"` + strings.Repeat("a", 41) + `"`,
	} {
		status, _, out := f.do(t, "PUT", path, "sess-owner-a", f.tenA, `{`+base+`,`+bad+`}`)
		if status != 400 || out["error"] != "invalid_field" {
			t.Fatalf("bad %s = %d %v", bad, status, out)
		}
	}
	// 含手机号的渠道：400 privacy_refused。
	status, _, out = f.do(t, "PUT", path, "sess-owner-a", f.tenA, `{`+base+`,"channels":"table_tent,13800138000"}`)
	if status != 400 || out["error"] != "privacy_refused" {
		t.Fatalf("phone channels = %d %v", status, out)
	}
	var rows int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM store_motion_requests`).Scan(&rows); err != nil || rows != 3 {
		t.Fatalf("rows = %d %v", rows, err)
	}
}
