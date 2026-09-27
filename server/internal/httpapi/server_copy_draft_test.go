package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestCopyDraftDoesNotInventOrPublishOrBill(t *testing.T) {
	f := newFixture(t, false)
	status, _, storeRec := f.do(t, "POST", "/api/v1/stores", "sess-owner-a", f.tenA,
		`{"name":"江边小馆","address":"杭州市湖滨路8号"}`)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create store = %d %+v", status, storeRec)
	}
	storeID, _ := storeRec["id"].(string)
	status, _, camp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"title":"周末到店","public_content":"到店有礼","store_id":%q}`, storeID))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create campaign = %d %+v", status, camp)
	}
	campID, _ := camp["id"].(string)
	beforeTitle, _ := camp["title"].(string)
	beforeStatus, _ := camp["status"].(string)
	beforeUpdated, _ := camp["updated_at"].(string)

	body := `{
		"idempotency_key":"draft-1",
		"price":{"text":"19.9元","status":"expired"},
		"address":{"text":"上海市南京西路1号","status":"confirmed"},
		"hours":{"text":"10:00-22:00","status":"uncertain"},
		"claims":[{"text":"全市最低","evidence":""}],
		"poi_names":["江边小馆(南京西路店)","江边小馆(人民广场店)"],
		"channel_mount":true,
		"asset_ids":[]
	}`
	status, _, draft := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-drafts", "sess-owner-a", f.tenA, body)
	if status != http.StatusCreated {
		t.Fatalf("draft = %d %+v", status, draft)
	}
	if draft["usable"] != false || draft["billed"] != false || draft["model_status"] != "not_authorized" {
		t.Fatalf("draft labeled usable or billed: %+v", draft)
	}
	if draft["title"] != "" || draft["intro"] != "" {
		t.Fatalf("unauthorized copy was filled: %+v", draft)
	}
	assertNoWithheld(t, draft)
	poi, _ := draft["poi"].(map[string]any)
	if poi["mounted"] != false {
		t.Fatalf("poi mounted from names: %+v", poi)
	}
	handoff, _ := draft["professional_handoff"].(map[string]any)
	if handoff["store_name"] != "江边小馆" {
		t.Fatalf("handoff = %+v", handoff)
	}
	if addr, _ := handoff["address"].(string); addr != "" {
		t.Fatalf("handoff address leaked: %+v", handoff)
	}
	if price, _ := handoff["price"].(string); price != "" {
		t.Fatalf("handoff price leaked: %+v", handoff)
	}
	for _, key := range []string{"launch_url", "url", "redirect"} {
		if _, ok := handoff[key]; ok {
			t.Fatalf("handoff has jump field %s", key)
		}
	}

	status, _, again := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-drafts", "sess-owner-a", f.tenA, body)
	if status != http.StatusOK || again["id"] != draft["id"] {
		t.Fatalf("replay = %d id %v want %v", status, again["id"], draft["id"])
	}

	conflict := strings.Replace(body, "19.9元", "29元", 1)
	status, _, conflictBody := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-drafts", "sess-owner-a", f.tenA, conflict)
	if status != http.StatusConflict || conflictBody["error"] != "idempotency_conflict" {
		t.Fatalf("conflict = %d %+v", status, conflictBody)
	}

	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campID+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	if status != http.StatusOK {
		t.Fatalf("activate = %d", status)
	}
	draftID, _ := draft["id"].(string)
	status, _, ver := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-drafts/"+draftID+"/versions", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusCreated {
		t.Fatalf("version = %d %+v", status, ver)
	}
	if ver["rewards_triggered"] != false || ver["campaign_status"] != "active" || ver["campaign_title"] != beforeTitle {
		t.Fatalf("accept changed publication: %+v", ver)
	}
	status, _, got := f.do(t, "GET", "/api/v1/campaigns/"+campID, "sess-owner-a", f.tenA, "")
	if status != http.StatusOK || got["title"] != beforeTitle || got["status"] != "active" || got["public_content"] != "到店有礼" {
		t.Fatalf("campaign overwritten: %d %+v", status, got)
	}
	if got["updated_at"] == beforeUpdated && beforeStatus == "draft" {
		// activation updates the row; accepting a version must not be required to touch it again.
	}
	var rules int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM campaign_rules`).Scan(&rules); err != nil {
		t.Fatal(err)
	}
	if rules != 0 {
		t.Fatalf("reward rules created: %d", rules)
	}

	status, _, hidden := f.do(t, "GET", "/api/v1/campaigns/"+campID+"/copy-drafts/"+draftID, "sess-owner-b", f.tenB, "")
	if status != http.StatusNotFound {
		t.Fatalf("cross tenant = %d %+v", status, hidden)
	}
	status, _, missing := f.do(t, "GET", "/api/v1/campaigns/"+campID+"/copy-drafts/cdf_missing", "sess-owner-a", f.tenA, "")
	if status != http.StatusNotFound || missing["error"] != "not_found" {
		t.Fatalf("unknown = %d %+v", status, missing)
	}
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-drafts", "", f.tenA, body)
	if status != http.StatusUnauthorized {
		t.Fatalf("guest draft = %d", status)
	}
	status, _, pub := f.do(t, "POST", "/api/v1/public/links/nope/copy-drafts", "", "", body)
	if status != http.StatusNotFound {
		t.Fatalf("public generate = %d %+v", status, pub)
	}
}

func TestCopyDraftQuotaHasRecoveryAndDoesNotBill(t *testing.T) {
	f := newFixture(t, false)
	_, _, camp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, `{"title":"配额活动","public_content":"到店有礼"}`)
	campID, _ := camp["id"].(string)
	for i := 0; i < copyDraftDailyQuota; i++ {
		body := fmt.Sprintf(`{"idempotency_key":"q%d","price":{"text":"","status":"absent"},"address":{"text":"","status":"absent"},"hours":{"text":"","status":"absent"},"claims":[],"poi_names":[],"asset_ids":[]}`, i)
		status, _, draft := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-drafts", "sess-owner-a", f.tenA, body)
		if status != http.StatusCreated || draft["billed"] != false {
			t.Fatalf("draft %d = %d %+v", i, status, draft)
		}
	}
	status, _, over := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-drafts", "sess-owner-a", f.tenA,
		`{"idempotency_key":"over","price":{"text":"","status":"absent"},"address":{"text":"","status":"absent"},"hours":{"text":"","status":"absent"},"claims":[],"poi_names":[],"asset_ids":[]}`)
	if status != http.StatusTooManyRequests || over["error"] != "quota_exceeded" || over["billed"] != false {
		t.Fatalf("quota = %d %+v", status, over)
	}
	if !strings.Contains(fmt.Sprint(over["message"]), "没有扣费") {
		t.Fatalf("quota message = %+v", over)
	}
}

func assertNoWithheld(t *testing.T, draft map[string]any) {
	t.Helper()
	raw := fmt.Sprintf("%v %v %v %v %v", draft["title"], draft["intro"], draft["topics"], draft["confirmed_facts"], draft["professional_handoff"])
	for _, forbidden := range []string{"19.9", "南京西路", "10:00", "全市最低"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("response contains %q: %s", forbidden, raw)
		}
	}
}
