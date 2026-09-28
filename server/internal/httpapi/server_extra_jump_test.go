package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
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
