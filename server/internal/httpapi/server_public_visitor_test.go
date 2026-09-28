package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// HUI-1896: the public page must name the store before asking for contact
// details, and a visit must not become a lead or a CRM row.

func TestPublicPageNamesMerchantWithoutInternalFields(t *testing.T) {
	f := newFixture(t, true)
	status, _, sto := f.do(t, "POST", "/api/v1/stores", "sess-owner-a", f.tenA, `{"name":"湖滨店","address":"内部地址不公开"}`)
	mustEqual(t, status, http.StatusCreated)
	storeID := sto["id"].(string)

	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"到店咖啡","public_content":"到店送一杯","store_id":"`+storeID+`","starts_at":"2026-09-01T00:00:00Z","ends_at":"2026-10-31T00:00:00Z"}`)
	mustEqual(t, status, http.StatusCreated)
	id := cmp["id"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	code := link["code"].(string)

	status, _, view := f.do(t, "GET", "/api/v1/public/links/"+code+"?entry=nfc&tenant_id="+f.tenB, "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["merchant_name"] != "商家A" || view["store_name"] != "湖滨店" || view["title"] != "到店咖啡" || view["public_content"] != "到店送一杯" {
		t.Fatalf("public identity = %v", view)
	}
	for _, forbidden := range []string{
		"tenant_id", "store_id", "id", "order_ref", "created_by", "principal", "agency",
		"asset_id", "crm", "address", "code", "status",
	} {
		if _, ok := view[forbidden]; ok {
			t.Fatalf("public payload leaks %s: %v", forbidden, view)
		}
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "内部地址") || strings.Contains(string(raw), f.tenB) {
		t.Fatalf("query or address leaked: %v", view)
	}
}

func TestLeadFormStatesPurposeSeparatelyFromMarketing(t *testing.T) {
	f := newLeadsFixture(t)
	status, _, form := f.guest(t, "GET", "/api/v1/public/links/"+f.code+"/lead-form", "")
	if status != 200 {
		t.Fatalf("lead form = %d %v", status, form)
	}
	recipient, _ := form["recipient"].(map[string]any)
	if recipient["name"] != "商家A" {
		t.Fatalf("recipient = %v", form["recipient"])
	}
	if form["purpose"] == "" || form["marketing_blocks_browse"] != false {
		t.Fatalf("purpose/browse = %v", form)
	}
	notice, _ := form["notice"].(map[string]any)
	if notice["version"] != "v1" {
		t.Fatalf("notice = %v", form["notice"])
	}
	required, _ := form["required_fields"].([]any)
	if len(required) != 2 || required[0] != "name" || required[1] != "phone" {
		t.Fatalf("required = %v", form["required_fields"])
	}
}

func TestSubmitSaysAcceptedNotSalesReceived(t *testing.T) {
	f := newLeadsFixture(t)
	beforeMembers := countRows(t, f, "members")
	body := `{"name":"李四","phone":"13800138000","consent_version":"v1","consent":true,"marketing_optin":false,"channel":"nfc"}`
	status, out := f.submitLead(t, f.code, body)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	if out["state"] != "accepted" || out["duplicate"] != false || out["sales_received"] != false || out["crm_received"] != false || out["owner_followed_up"] != false || out["creates_platform_account"] != false {
		t.Fatalf("submit honesty = %v", out)
	}
	submitted, _ := out["submitted_to"].(map[string]any)
	if submitted["name"] != "商家A" {
		t.Fatalf("submitted_to = %v", out["submitted_to"])
	}
	if countRows(t, f, "members") != beforeMembers {
		t.Fatal("visitor submit created a platform member")
	}
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 || rows[0].MarketingOptin {
		t.Fatalf("stored lead = %+v", rows)
	}

	againStatus, again := f.submitLead(t, f.code, body)
	if againStatus != 200 || again["duplicate"] != true || again["submission_ref"] != out["submission_ref"] || again["sales_received"] != false {
		t.Fatalf("duplicate = %d %v", againStatus, again)
	}
	rows, _ = f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 1 {
		t.Fatalf("duplicate created another row: %d", len(rows))
	}
}

func TestViewAndScanDoNotCreateLeads(t *testing.T) {
	f := newLeadsFixture(t)
	for _, channel := range []string{"nfc", "qr", "web"} {
		status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/view-events", `{"channel":"`+channel+`"}`)
		if status != 204 {
			t.Fatalf("view %s = %d", channel, status)
		}
	}
	if status, _, _ := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/view-events", `{"channel":"nfc","tenant_id":"tnt_evil"}`); status != 400 {
		t.Fatalf("forged tenant on beacon = %d, want 400", status)
	}
	rows, _ := f.s.St.ListLeadSubmissions(f.tenA, f.campaignID)
	if len(rows) != 0 {
		t.Fatalf("views created leads: %d", len(rows))
	}
	for _, channel := range []string{"nfc", "qr", "web"} {
		n, err := f.s.St.GetViewStat(f.code, todayUTC(), channel)
		if err != nil || n != 1 {
			t.Fatalf("stat %s = %d %v", channel, n, err)
		}
	}
}

func TestLeadStatusProjectsCRMPausedWithoutClaimingSales(t *testing.T) {
	f := newLeadsFixture(t)
	status, out := f.submitLead(t, f.code, validLeadBody)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)
	if err := f.s.St.MarkLeadRejected(ref, "subscription_disabled: crm paused"); err != nil {
		t.Fatal(err)
	}
	status, _, body := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-status",
		`{"submission_ref":"`+ref+`","phone":"13800138000"}`)
	if status != 200 || body["state"] != "crm_paused" || body["sales_received"] != false || body["crm_received"] != false || body["owner_followed_up"] != false {
		t.Fatalf("crm paused status = %d %v", status, body)
	}
	status, _, hidden := f.guest(t, "POST", "/api/v1/public/links/"+f.code+"/lead-status",
		`{"submission_ref":"`+ref+`","phone":"13900139000"}`)
	if status != 404 {
		t.Fatalf("wrong phone status = %d %v", status, hidden)
	}
}

func countRows(t *testing.T, f *leadsFixture, table string) int {
	t.Helper()
	var n int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func todayUTC() string {
	return time.Now().UTC().Format("2006-01-02")
}
