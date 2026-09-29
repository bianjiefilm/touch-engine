package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/brandctx"
)

// 会让这个测试失败的生产改动：公共页把未配置或非正式地址展示出来，
// 或把跳转点击写成添加成功、关注成功、留资、CRM、发奖或发布成功。

func TestExtraJumpsShowOnlyOfficialTargetsAndClicksStayUnknown(t *testing.T) {
	f := newFixture(t, false)
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"到店","public_content":"欢迎"}`)
	mustEqual(t, status, http.StatusCreated)
	id := cmp["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	code := link["code"].(string)

	status, _, empty := f.do(t, "GET", "/api/v1/public/links/"+code+"/extra-jumps", "", "", "")
	mustEqual(t, status, http.StatusOK)
	if actions, _ := empty["actions"].([]any); len(actions) != 0 {
		t.Fatalf("unconfigured actions = %v", empty["actions"])
	}
	if empty["creates_lead"] != false || empty["crm_imported"] != false || empty["reward_triggered"] != false || empty["publish_success"] != false {
		t.Fatalf("empty effects = %v", empty)
	}
	closed := closedReasons(t, empty["closed"])
	for _, kind := range []string{"wifi", "navigate", "review", "wecom", "follow"} {
		if closed[kind] != "not_configured" {
			t.Fatalf("closed %s = %v", kind, empty["closed"])
		}
	}

	body := `{"actions":[
		{"kind":"wecom","enabled":true,"href":"https://work.weixin.qq.com/ca/demo"},
		{"kind":"follow","enabled":true,"href":"https://example.invalid/follow"},
		{"kind":"navigate","enabled":false,"href":"https://uri.amap.com/marker?position=120,30"},
		{"kind":"review","enabled":true,"href":"https://127.0.0.1/review"},
		{"kind":"wifi","enabled":true,"href":"https://shop.example.com/wifi"}
	]}`
	status, _, saved := f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-staff-a", f.tenA, body)
	if status != http.StatusForbidden {
		t.Fatalf("staff put = %d %v", status, saved)
	}
	status, _, saved = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-b", f.tenA, body)
	if status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatalf("cross-tenant put = %d %v", status, saved)
	}
	status, _, saved = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, body)
	mustEqual(t, status, http.StatusOK)
	shown := shownHrefs(t, saved["actions"])
	if len(shown) != 2 || shown["wecom"] != "https://work.weixin.qq.com/ca/demo" || shown["wifi"] != "https://shop.example.com/wifi" {
		t.Fatalf("saved shown = %v", saved["actions"])
	}
	closed = closedReasons(t, saved["closed"])
	if closed["follow"] != "address_not_official" || closed["review"] != "address_not_official" || closed["navigate"] != "not_configured" {
		t.Fatalf("saved closed = %v", saved["closed"])
	}
	if raw := jsonString(saved); containsAny(raw, "example.invalid", "127.0.0.1") {
		t.Fatalf("rejected address leaked: %s", raw)
	}

	status, _, view := f.do(t, "GET", "/api/v1/public/links/"+code+"/extra-jumps", "", "", "")
	mustEqual(t, status, http.StatusOK)
	shown = shownHrefs(t, view["actions"])
	if len(shown) != 2 || shown["wecom"] != "https://work.weixin.qq.com/ca/demo" || shown["wifi"] != "https://shop.example.com/wifi" {
		t.Fatalf("public shown = %v", view["actions"])
	}

	beforeLeads := tableCount(t, f, "lead_submissions")
	beforeOutbox := tableCount(t, f, "leads_outbox")
	beforeReward := tableCount(t, f, "publish_reward_ledger")
	beforePublish := tableCount(t, f, "customer_publish_attempts")

	status, _, click := f.do(t, "POST", "/api/v1/public/links/"+code+"/extra-jumps/wecom/clicks", "", "", `{}`)
	mustEqual(t, status, http.StatusOK)
	if click["kind"] != "wecom" || click["recorded_as"] != "click" || click["success"] != false || click["platform_result"] != "unknown" ||
		click["added"] != false || click["followed"] != false || click["lead_created"] != false || click["crm_imported"] != false ||
		click["reward_triggered"] != false || click["publish_success"] != false {
		t.Fatalf("click = %v", click)
	}
	status, _, missed := f.do(t, "POST", "/api/v1/public/links/"+code+"/extra-jumps/navigate/clicks", "", "", `{}`)
	if status != http.StatusNotFound {
		t.Fatalf("closed click = %d %v", status, missed)
	}
	if tableCount(t, f, "lead_submissions") != beforeLeads || tableCount(t, f, "leads_outbox") != beforeOutbox ||
		tableCount(t, f, "publish_reward_ledger") != beforeReward || tableCount(t, f, "customer_publish_attempts") != beforePublish {
		t.Fatal("a jump click created a lead, CRM row, reward, or publish attempt")
	}
}

func TestNavigateFollowAndWecomClicksStayUnknown(t *testing.T) {
	f := newFixture(t, false)
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"到店","public_content":"欢迎"}`)
	mustEqual(t, status, http.StatusCreated)
	id := cmp["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	code := link["code"].(string)
	body := `{"actions":[
		{"kind":"wecom","enabled":true,"href":"https://work.weixin.qq.com/ca/demo"},
		{"kind":"follow","enabled":true,"href":"https://shop.example.com/follow"},
		{"kind":"navigate","enabled":true,"href":"https://uri.amap.com/marker?position=120,30"}
	]}`
	status, _, saved := f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, body)
	mustEqual(t, status, http.StatusOK)
	shown := shownHrefs(t, saved["actions"])
	if len(shown) != 3 {
		t.Fatalf("shown = %v", saved["actions"])
	}
	beforeLeads := tableCount(t, f, "lead_submissions")
	for _, kind := range []string{"wecom", "follow", "navigate"} {
		status, _, click := f.do(t, "POST", "/api/v1/public/links/"+code+"/extra-jumps/"+kind+"/clicks", "", "", `{}`)
		mustEqual(t, status, http.StatusOK)
		if click["recorded_as"] != "click" || click["success"] != false || click["platform_result"] != "unknown" || click["lead_created"] != false {
			t.Fatalf("%s click = %v", kind, click)
		}
	}
	if tableCount(t, f, "lead_submissions") != beforeLeads {
		t.Fatal("jump clicks created a lead")
	}
	rows, err := f.s.St.DB.Query(`SELECT kind, success, platform_result FROM extra_jump_clicks ORDER BY kind`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var kind, result string
		var success int
		if err := rows.Scan(&kind, &success, &result); err != nil {
			t.Fatal(err)
		}
		seen[kind] = true
		if success != 0 || result != "unknown" {
			t.Fatalf("stored %s success=%d result=%s", kind, success, result)
		}
	}
	if !seen["wecom"] || !seen["follow"] || !seen["navigate"] {
		t.Fatalf("stored clicks = %v", seen)
	}
}

func closedReasons(t *testing.T, raw any) map[string]string {
	t.Helper()
	items, _ := raw.([]any)
	out := map[string]string{}
	for _, item := range items {
		row, _ := item.(map[string]any)
		if row["shown"] != false || row["href"] != nil && row["href"] != "" {
			t.Fatalf("closed row leaked a target: %v", row)
		}
		out[row["kind"].(string)] = row["reason"].(string)
	}
	return out
}

func shownHrefs(t *testing.T, raw any) map[string]string {
	t.Helper()
	items, _ := raw.([]any)
	out := map[string]string{}
	for _, item := range items {
		row, _ := item.(map[string]any)
		if row["available"] != true || row["result"] != "ready" {
			t.Fatalf("shown row = %v", row)
		}
		out[row["kind"].(string)] = row["href"].(string)
	}
	return out
}

func tableCount(t *testing.T, f *fixture, table string) int {
	t.Helper()
	var n int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func jsonString(v any) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func containsAny(s string, parts ...string) bool {
	for _, part := range parts {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func TestJumpMatrixRegisteredTargetsAndClicks(t *testing.T) {
	f := newFixture(t, false)
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"到店","public_content":"欢迎"}`)
	mustEqual(t, status, http.StatusCreated)
	id := cmp["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	code := link["code"].(string)

	body := `{"actions":[
		{"kind":"wifi","enabled":true,"href":"https://shop.example.com/wifi","expires_at":"2099-01-01T00:00:00Z"},
		{"kind":"navigate","enabled":true,"href":"https://uri.amap.com/marker","expires_at":"2020-01-01T00:00:00Z"},
		{"kind":"review","enabled":true,"href":"https://shop.example.com/review","revoked":true},
		{"kind":"follow","enabled":true,"href":"https://shop.example.com/follow"}
	],"return":{"enabled":true,"href":"/c/welcome"}}`
	status, _, saved := f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, body)
	mustEqual(t, status, http.StatusOK)
	shown := shownHrefs(t, saved["actions"])
	if len(shown) != 2 || shown["wifi"] != "https://shop.example.com/wifi" || shown["follow"] != "https://shop.example.com/follow" {
		t.Fatalf("saved shown = %v", saved["actions"])
	}
	closed := closedReasons(t, saved["closed"])
	if closed["navigate"] != "expired" || closed["review"] != "revoked" {
		t.Fatalf("saved closed = %v", saved["closed"])
	}
	ret := returnView(t, saved["return"])
	if ret["shown"] != true || ret["href"] != "/c/welcome" || ret["class"] != "return" {
		t.Fatalf("saved return = %v", saved["return"])
	}
	assertEvidenceSplit(t, saved["support"])

	status, _, view := f.do(t, "GET", "/api/v1/public/links/"+code+"/extra-jumps", "", "", "")
	mustEqual(t, status, http.StatusOK)
	if _, ok := view["configured"]; ok {
		t.Fatal("public payload echoed stored configuration")
	}
	shown = shownHrefs(t, view["actions"])
	if shown["wifi"] != "https://shop.example.com/wifi" || shown["follow"] != "https://shop.example.com/follow" || len(shown) != 2 {
		t.Fatalf("public shown = %v", view["actions"])
	}
	if strings.Contains(jsonString(view), "uri.amap.com") || strings.Contains(jsonString(view), "shop.example.com/review") {
		t.Fatalf("closed address leaked: %s", jsonString(view))
	}
	ret = returnView(t, view["return"])
	if ret["href"] != "/c/welcome" || ret["shown"] != true {
		t.Fatalf("public return = %v", view["return"])
	}
	wifiEvidence := actionEvidence(t, view["actions"], "wifi")
	if wifiEvidence["web"] != "configured" || wifiEvidence["client_launch"] != "unverified" || wifiEvidence["platform_action"] != "unknown" {
		t.Fatalf("wifi evidence = %v", wifiEvidence)
	}

	status, _, merchant := f.do(t, "GET", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if !strings.Contains(jsonString(merchant["configured"]), "https://shop.example.com/wifi") {
		t.Fatalf("merchant configured = %v", merchant["configured"])
	}
	status, _, denied := f.do(t, "GET", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-staff-a", f.tenA, "")
	if status != http.StatusForbidden {
		t.Fatalf("staff get = %d %v", status, denied)
	}

	beforeLeads := tableCount(t, f, "lead_submissions")
	beforeClicks := tableCount(t, f, "extra_jump_clicks")
	status, _, click := f.do(t, "POST", "/api/v1/public/links/"+code+"/extra-jumps/wifi/clicks", "", "", `{"href":"https://evil.example/phish"}`)
	mustEqual(t, status, http.StatusOK)
	if click["href"] != "https://shop.example.com/wifi" || click["class"] != "store" || click["success"] != false || click["platform_result"] != "unknown" ||
		click["auto_follow"] != false || click["auto_join"] != false || click["auto_pay"] != false || click["lead_created"] != false {
		t.Fatalf("click = %v", click)
	}
	ev, _ := click["evidence"].(map[string]any)
	if ev["web"] != "configured" || ev["client_launch"] != "unverified" || ev["platform_action"] != "unknown" {
		t.Fatalf("click evidence = %v", click["evidence"])
	}
	if strings.Contains(jsonString(click), "evil.example") {
		t.Fatalf("caller href was accepted: %s", jsonString(click))
	}
	status, _, missed := f.do(t, "POST", "/api/v1/public/links/"+code+"/extra-jumps/navigate/clicks", "", "", `{}`)
	if status != http.StatusNotFound {
		t.Fatalf("expired click = %d %v", status, missed)
	}
	status, _, back := f.do(t, "POST", "/api/v1/public/links/"+code+"/returns/clicks", "", "", `{"href":"https://evil.example/back"}`)
	mustEqual(t, status, http.StatusOK)
	if back["href"] != "/c/welcome" || back["success"] != false || back["platform_result"] != "unknown" || back["auto_pay"] != false {
		t.Fatalf("return click = %v", back)
	}
	if tableCount(t, f, "lead_submissions") != beforeLeads || tableCount(t, f, "extra_jump_clicks") != beforeClicks+1 {
		t.Fatal("jump click created a lead or stored the expired click")
	}

	evil := `{"actions":[{"kind":"follow","enabled":true,"href":"https://shop.example.com/other"}],"return":{"enabled":true,"href":"https://evil.example/back"}}`
	status, _, rejected := f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, evil)
	if status != http.StatusBadRequest || rejected["error"] != "unregistered_url" || strings.Contains(jsonString(rejected), "evil.example") {
		t.Fatalf("unregistered return = %d %v", status, rejected)
	}
	status, _, view = f.do(t, "GET", "/api/v1/public/links/"+code+"/extra-jumps", "", "", "")
	mustEqual(t, status, http.StatusOK)
	shown = shownHrefs(t, view["actions"])
	if shown["follow"] != "https://shop.example.com/follow" || shown["wifi"] != "https://shop.example.com/wifi" {
		t.Fatal("rejected return rewrote the saved jumps")
	}

	f.s.Cfg.PublicBaseURL = "https://h5.example.com"
	registered := `{"actions":[{"kind":"wifi","enabled":true,"href":"https://shop.example.com/wifi"}],"return":{"enabled":true,"href":"https://h5.example.com/c/back"}}`
	status, _, saved = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, registered)
	mustEqual(t, status, http.StatusOK)
	ret = returnView(t, saved["return"])
	if ret["href"] != "https://h5.example.com/c/back" || ret["shown"] != true {
		t.Fatalf("registered host return = %v", saved["return"])
	}

	adminReturn := `{"return":{"enabled":true,"href":"/admin"}}`
	status, _, rejected = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, adminReturn)
	if status != http.StatusBadRequest || rejected["error"] != "unregistered_url" {
		t.Fatalf("admin return = %d %v", status, rejected)
	}

	revokedWecom := `{"actions":[{"kind":"wecom","enabled":true,"href":"https://work.weixin.qq.com/ca/from-jump","revoked":true}]}`
	status, _, _ = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, revokedWecom)
	mustEqual(t, status, http.StatusOK)
	status, _, guide := f.do(t, "GET", "/api/v1/public/links/"+code+"/private-domain?channel=web", "", "", "")
	mustEqual(t, status, http.StatusOK)
	if entries, _ := guide["entries"].([]any); len(entries) != 0 {
		t.Fatalf("revoked wecom was offered: %v", guide["entries"])
	}

	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"ended"}`)
	mustEqual(t, status, http.StatusOK)
	clicksBeforeEnded := tableCount(t, f, "extra_jump_clicks")
	status, _, ended := f.do(t, "GET", "/api/v1/public/links/"+code+"/extra-jumps", "", "", "")
	if status != http.StatusNotFound || ended["state"] != "ended" {
		t.Fatalf("ended jumps = %d %v", status, ended)
	}
	status, _, endedClick := f.do(t, "POST", "/api/v1/public/links/"+code+"/extra-jumps/wifi/clicks", "", "", `{}`)
	if status != http.StatusNotFound || tableCount(t, f, "extra_jump_clicks") != clicksBeforeEnded {
		t.Fatalf("ended click = %d %v", status, endedClick)
	}
}

func TestJumpMatrixCrossBrandAndUnauthorized(t *testing.T) {
	f := newFixture(t, false)
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, `{"title":"到店","public_content":"欢迎"}`)
	mustEqual(t, status, http.StatusCreated)
	id := cmp["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	code := link["code"].(string)
	body := `{"actions":[{"kind":"wifi","enabled":true,"href":"https://shop.example.com/wifi"}],"return":{"enabled":true,"href":"/c/welcome"}}`
	status, _, _ = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, body)
	mustEqual(t, status, http.StatusOK)

	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host, "]") {
			host = host[:i]
		}
		switch host {
		case "brand-a.example":
			_, _ = w.Write([]byte(`{"brand_id":"brand-a","status":"active","display_name":"甲牌","admit_login":true,"admit_public":true,"apps":[{"app_id":"touch-engine","public_visible":true,"availability":"ready"}]}`))
		case "brand-b.example":
			_, _ = w.Write([]byte(`{"brand_id":"brand-b","status":"active","display_name":"乙牌","admit_login":true,"admit_public":true,"apps":[{"app_id":"touch-engine","public_visible":true,"availability":"ready"}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"unknown_brand"}`))
		}
	}))
	defer reg.Close()
	f.s.Cfg.FeatureBrand = true
	f.s.Cfg.BrandBaseURL = reg.URL
	f.s.Cfg.BrandToken = "reader-secret"
	f.s.Brand = &brandctx.Client{BaseURL: reg.URL, Token: "reader-secret", HTTP: reg.Client()}
	if err := f.s.St.BindTenantBrand(f.tenA, "brand-a"); err != nil {
		t.Fatal(err)
	}
	if err := f.s.St.StampLinkBrand(link["id"].(string), f.tenA, "brand-a", "brand-a.example"); err != nil {
		t.Fatal(err)
	}

	status, raw, other := brandedDo(t, f, "GET", "/api/v1/public/links/"+code+"/extra-jumps", "brand-b.example", "")
	if status != http.StatusNotFound || other["state"] != "cross_brand" || strings.Contains(raw, "shop.example.com") || strings.Contains(raw, "/c/welcome") {
		t.Fatalf("cross brand = %d %s", status, raw)
	}
	clicks := tableCount(t, f, "extra_jump_clicks")
	status, raw, _ = brandedDo(t, f, "POST", "/api/v1/public/links/"+code+"/extra-jumps/wifi/clicks", "brand-b.example", `{}`)
	if status != http.StatusNotFound || tableCount(t, f, "extra_jump_clicks") != clicks || strings.Contains(raw, "shop.example.com") {
		t.Fatalf("cross brand click = %d %s", status, raw)
	}

	status, raw, same := brandedDo(t, f, "GET", "/api/v1/public/links/"+code+"/extra-jumps", "brand-a.example", "")
	if status != http.StatusOK || returnView(t, same["return"])["href"] != "/c/welcome" {
		t.Fatalf("same brand = %d %s", status, raw)
	}

	if err := f.s.St.SetTenantLifecycle(f.tenA, "suspended"); err != nil {
		t.Fatal(err)
	}
	status, raw, blocked := brandedDo(t, f, "GET", "/api/v1/public/links/"+code+"/extra-jumps", "brand-a.example", "")
	if status != http.StatusNotFound || blocked["state"] != "unauthorized" || strings.Contains(raw, "shop.example.com") {
		t.Fatalf("suspended = %d %s", status, raw)
	}
}

func brandedDo(t *testing.T, f *fixture, method, path, host, body string) (int, string, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Touch-Public-Host", host)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, string(raw), out
}

func returnView(t *testing.T, raw any) map[string]any {
	t.Helper()
	row, _ := raw.(map[string]any)
	if row == nil {
		t.Fatalf("return = %v", raw)
	}
	return row
}

func actionEvidence(t *testing.T, raw any, kind string) map[string]any {
	t.Helper()
	items, _ := raw.([]any)
	for _, item := range items {
		row, _ := item.(map[string]any)
		if row["kind"] == kind {
			ev, _ := row["evidence"].(map[string]any)
			return ev
		}
	}
	t.Fatalf("missing %s in %v", kind, raw)
	return nil
}

func assertEvidenceSplit(t *testing.T, raw any) {
	t.Helper()
	items, _ := raw.([]any)
	if len(items) != 6 {
		t.Fatalf("support = %v", raw)
	}
	for _, item := range items {
		row, _ := item.(map[string]any)
		if row["client_launch"] != "unverified" || row["platform_action"] != "unknown" || row["web"] != "page_only" ||
			row["auto_follow"] != false || row["auto_join"] != false || row["auto_pay"] != false ||
			!strings.Contains(fmt.Sprint(row["native_reason"]), "原生唤起未验证") {
			t.Fatalf("support row = %v", row)
		}
	}
}
