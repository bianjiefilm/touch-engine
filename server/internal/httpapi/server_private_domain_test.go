package httpapi

import (
	"net/http"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/privatedomain"
)

// 会让这个测试失败的生产改动：公共页展示未配置、非企微或当前渠道打不开的入口，
// 或把 click_wecom 写成添加成功、进群成功、新联系人、留资、CRM、发奖或核销。

func TestPrivateDomainShowsOnlyCapableEntriesAndClicksStayUnknown(t *testing.T) {
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

	status, _, empty := f.do(t, "GET", "/api/v1/public/links/"+code+"/private-domain?channel=web", "", "", "")
	mustEqual(t, status, http.StatusOK)
	if entries, _ := empty["entries"].([]any); len(entries) != 0 {
		t.Fatalf("unconfigured entries = %v", empty["entries"])
	}
	if empty["connected"] != false || empty["redemption"] != "unknown" || empty["creates_lead"] != false ||
		empty["crm_imported"] != false || empty["reward_triggered"] != false || empty["silent_add"] != false ||
		empty["forced_join"] != false || empty["background_marketing"] != false {
		t.Fatalf("empty guide = %v", empty)
	}
	closed := closedReasons(t, empty["closed"])
	if closed["wecom"] != "not_configured" || closed["community"] != "not_configured" {
		t.Fatalf("closed = %v", empty["closed"])
	}

	body := `{"entries":[
		{"kind":"wecom","enabled":true,"href":"https://work.weixin.qq.com/ca/demo"},
		{"kind":"community","enabled":true,"href":"https://shop.example.com/group"},
		{"kind":"wecom","enabled":true,"href":"https://example.invalid/wecom"}
	]}`
	status, _, saved := f.do(t, "PUT", "/api/v1/campaigns/"+id+"/private-domain", "sess-staff-a", f.tenA, body)
	if status != http.StatusForbidden {
		t.Fatalf("staff put = %d %v", status, saved)
	}
	status, _, saved = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/private-domain", "sess-owner-b", f.tenA, body)
	if status != http.StatusForbidden && status != http.StatusNotFound {
		t.Fatalf("cross-tenant put = %d %v", status, saved)
	}
	status, _, saved = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/private-domain", "sess-owner-a", f.tenA, body)
	mustEqual(t, status, http.StatusOK)
	shown := shownHrefs(t, saved["entries"])
	if len(shown) != 1 || shown["wecom"] != "https://work.weixin.qq.com/ca/demo" {
		t.Fatalf("saved entries = %v", saved["entries"])
	}
	if saved["connected"] != false || saved["redemption"] != "unknown" {
		t.Fatalf("saved guide claimed a result: %v", saved)
	}
	if raw := jsonString(saved); containsAny(raw, "example.invalid", "shop.example.com") {
		t.Fatalf("rejected address leaked: %s", raw)
	}

	status, _, hidden := f.do(t, "GET", "/api/v1/public/links/"+code+"/private-domain?channel=miniprogram", "", "", "")
	mustEqual(t, status, http.StatusOK)
	if entries, _ := hidden["entries"].([]any); len(entries) != 0 || hidden["connected"] != false {
		t.Fatalf("unknown channel = %v", hidden)
	}
	if reasons := closedReasons(t, hidden["closed"]); reasons["wecom"] != "channel_cannot_open" {
		t.Fatalf("unknown channel closed = %v", hidden["closed"])
	}

	beforeLeads := tableCount(t, f, "lead_submissions")
	beforeOutbox := tableCount(t, f, "leads_outbox")
	beforeReward := tableCount(t, f, "publish_reward_ledger")
	beforePublish := tableCount(t, f, "customer_publish_attempts")

	status, _, view := f.do(t, "GET", "/api/v1/public/links/"+code+"/private-domain?channel=nfc", "", "", "")
	mustEqual(t, status, http.StatusOK)
	shown = shownHrefs(t, view["entries"])
	if len(shown) != 1 || shown["wecom"] != "https://work.weixin.qq.com/ca/demo" || view["connected"] != false || view["redemption"] != "unknown" {
		t.Fatalf("public guide = %v", view)
	}

	status, _, click := f.do(t, "POST", "/api/v1/public/links/"+code+"/private-domain/wecom/clicks?channel=web", "", "", `{}`)
	mustEqual(t, status, http.StatusOK)
	if click["event"] != "click_wecom" || click["recorded_as"] != "click" || click["success"] != false ||
		click["platform_result"] != "unknown" || click["added"] != false || click["joined"] != false ||
		click["contact_created"] != false || click["lead_created"] != false || click["crm_imported"] != false ||
		click["reward_triggered"] != false || click["connected"] != false || click["redemption"] != "unknown" ||
		click["followed"] != false || click["silent_add"] != false || click["forced_join"] != false ||
		click["background_marketing"] != false {
		t.Fatalf("click = %v", click)
	}
	status, _, missed := f.do(t, "POST", "/api/v1/public/links/"+code+"/private-domain/community/clicks?channel=web", "", "", `{}`)
	if status != http.StatusNotFound {
		t.Fatalf("closed click = %d %v", status, missed)
	}
	if tableCount(t, f, "lead_submissions") != beforeLeads || tableCount(t, f, "leads_outbox") != beforeOutbox ||
		tableCount(t, f, "publish_reward_ledger") != beforeReward || tableCount(t, f, "customer_publish_attempts") != beforePublish {
		t.Fatal("a wecom click created a lead, CRM row, reward, or publish attempt")
	}
	assertClickRow(t, f, "click_wecom")

	jump := `{"actions":[{"kind":"wecom","enabled":true,"href":"https://shop.example.com/add"}]}`
	status, _, _ = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, jump)
	mustEqual(t, status, http.StatusOK)
	status, _, still := f.do(t, "GET", "/api/v1/public/links/"+code+"/private-domain?channel=web", "", "", "")
	mustEqual(t, status, http.StatusOK)
	shown = shownHrefs(t, still["entries"])
	if shown["wecom"] != "https://work.weixin.qq.com/ca/demo" || len(shown) != 1 || still["connected"] != false {
		t.Fatalf("generic https wecom replaced the real entry: %v", still["entries"])
	}

	if err := f.s.St.RecordPrivateDomainClick(f.tenA, id, privatedomain.Click{
		Event: privatedomain.EventClickWecom, Kind: privatedomain.KindWecom, RecordedAs: "click", Success: true, PlatformResult: "added",
	}); err == nil {
		t.Fatal("stored a successful add")
	}

	status, _, cleared := f.do(t, "PUT", "/api/v1/campaigns/"+id+"/private-domain", "sess-owner-a", f.tenA, `{"entries":[]}`)
	mustEqual(t, status, http.StatusOK)
	if entries, _ := cleared["entries"].([]any); len(entries) != 0 || cleared["connected"] != false {
		t.Fatalf("cleared guide = %v", cleared)
	}
	realJump := `{"actions":[{"kind":"wecom","enabled":true,"href":"https://work.weixin.qq.com/ca/from-jump"}]}`
	status, _, _ = f.do(t, "PUT", "/api/v1/campaigns/"+id+"/extra-jumps", "sess-owner-a", f.tenA, realJump)
	mustEqual(t, status, http.StatusOK)
	status, _, fromJump := f.do(t, "GET", "/api/v1/public/links/"+code+"/private-domain?channel=qr", "", "", "")
	mustEqual(t, status, http.StatusOK)
	shown = shownHrefs(t, fromJump["entries"])
	if len(shown) != 1 || shown["wecom"] != "https://work.weixin.qq.com/ca/from-jump" || fromJump["connected"] != false {
		t.Fatalf("real extra-jump wecom was hidden: %v", fromJump["entries"])
	}
}

func assertClickRow(t *testing.T, f *fixture, event string) {
	t.Helper()
	var recorded, result, redemption string
	var success, added, joined, contact, lead, crm, reward, connected int
	err := f.s.St.DB.QueryRow(`SELECT recorded_as, success, platform_result, added, joined, contact_created, lead_created, crm_imported, reward_triggered, connected, redemption
		FROM private_domain_clicks WHERE event_name=?`, event).Scan(
		&recorded, &success, &result, &added, &joined, &contact, &lead, &crm, &reward, &connected, &redemption)
	if err != nil {
		t.Fatal(err)
	}
	if recorded != "click" || success != 0 || result != "unknown" || added != 0 || joined != 0 || contact != 0 ||
		lead != 0 || crm != 0 || reward != 0 || connected != 0 || redemption != "unknown" {
		t.Fatalf("stored click %s success=%d result=%s redemption=%s", recorded, success, result, redemption)
	}
}
