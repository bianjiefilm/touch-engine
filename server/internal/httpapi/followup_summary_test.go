package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWorkbenchReadsFollowUpSummary(t *testing.T) {
	f := newLeadsFixture(t)
	var sawTenant string
	summary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Token") != "leads-summary-token" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		sawTenant = r.Header.Get("X-Tenant-ID")
		if r.URL.Query().Get("campaign_ref") != f.campaignID {
			http.Error(w, "campaign", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"known": true,
			"campaigns": []map[string]any{{
				"campaign_ref":      f.campaignID,
				"pending_follow_up": 1,
				"lead_ids":          []string{"lead_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			}},
		})
	}))
	t.Cleanup(summary.Close)
	f.s.Cfg.LeadsFollowUpBaseURL = summary.URL
	f.s.Cfg.LeadsFollowUpToken = "leads-summary-token"

	status, _, body := f.do(t, "GET", "/api/v1/workbench", f.adminSession, f.tenA, "")
	if status != 200 {
		t.Fatalf("status=%d %#v", status, body)
	}
	if sawTenant != f.tenA {
		t.Fatalf("summary tenant=%q", sawTenant)
	}
	raw, _ := json.Marshal(body)
	text := string(raw)
	if strings.Contains(text, "13800138000") || strings.Contains(text, "张三") {
		t.Fatalf("workbench leaked a contact: %s", text)
	}
	customers := body["customers"].(map[string]any)
	cards := customers["cards"].([]any)
	var card map[string]any
	for _, item := range cards {
		row := item.(map[string]any)
		if row["campaign_id"] == f.campaignID {
			card = row
		}
	}
	if card == nil {
		t.Fatalf("cards=%v", cards)
	}
	follow := card["follow_up"].(map[string]any)
	if follow["available"] != true || follow["value"] != float64(1) || follow["reason"] != "leads_follow_up_summary" {
		t.Fatalf("follow_up=%v", follow)
	}
	ids := card["open_lead_ids"].([]any)
	if len(ids) != 1 || ids[0] != "lead_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("open_lead_ids=%v", ids)
	}
}

func TestWorkbenchKeepsFollowUpUnknownWhenSummaryFails(t *testing.T) {
	f := newLeadsFixture(t)
	summary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	t.Cleanup(summary.Close)
	f.s.Cfg.LeadsFollowUpBaseURL = summary.URL
	f.s.Cfg.LeadsFollowUpToken = "leads-summary-token"

	status, _, body := f.do(t, "GET", "/api/v1/workbench", f.adminSession, f.tenA, "")
	if status != 200 {
		t.Fatalf("status=%d", status)
	}
	customers := body["customers"].(map[string]any)
	cards := customers["cards"].([]any)
	follow := cards[0].(map[string]any)["follow_up"].(map[string]any)
	if follow["available"] == true || follow["value"] != nil {
		t.Fatalf("failure written as a count: %v", follow)
	}
}
