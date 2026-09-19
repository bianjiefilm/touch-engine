package httpapi

// handlers_admin_leads.go: merchant-side lead visibility (authenticated admin
// surface). The merchant is the authorized source domain, so contact data is
// returned here — but only within the tenant scope, through the standard
// membership middleware, and only for their own campaigns.

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// POST /api/v1/campaigns/{id}/lead-form — enable/configure the fixed light form.
func (s *Server) handleLeadFormUpsert(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authzScope(c), w) {
		return
	}
	// feature off -> the whole lead surface is invisible, admin side included:
	// configuring a form whose collection pipeline (forwarder) is not running
	// would silently dead-end, so the flag gates this surface too.
	if !s.leadsGate(w) {
		return
	}
	var in struct {
		NoticeVersion         string `json:"notice_version"`
		MarketingOptinEnabled *bool  `json:"marketing_optin_enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	// campaign scope first: another tenant's campaign must be invisible here
	if _, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	version := in.NoticeVersion
	if version == "" {
		version = "v1"
	}
	marketing := true
	if in.MarketingOptinEnabled != nil {
		marketing = *in.MarketingOptinEnabled
	}
	rec, err := s.St.UpsertLeadForm(c.Member.TenantID, r.PathValue("id"), version, marketing, c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead form upsert failed")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// GET /api/v1/campaigns/{id}/leads — tenant-scoped submission list.
func (s *Server) handleLeadList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authzScope(c), w) {
		return
	}
	if !s.leadsGate(w) {
		return
	}
	if _, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	items, err := s.St.ListLeadSubmissions(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/campaigns/{id}/lead-stats — submissions vs anonymous views,
// deliberately two separate counters.
func (s *Server) handleLeadStats(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authzScope(c), w) {
		return
	}
	if !s.leadsGate(w) {
		return
	}
	if _, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID); errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	subs, views, err := s.St.LeadStats(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "lead stats failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"submissions": subs, "anonymous_views": views})
}

// GET /api/v1/campaigns/{id}/leads/{ref}/audit — minimal audit trail.
func (s *Server) handleLeadAudit(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadRecord, authzScope(c), w) {
		return
	}
	if !s.leadsGate(w) {
		return
	}
	if _, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	// ref must belong to this tenant+campaign
	lead, err := s.St.GetLeadSubmissionByRef(r.PathValue("ref"))
	if err != nil || lead.TenantID != c.Member.TenantID || lead.CampaignID != r.PathValue("id") {
		fail(w, http.StatusNotFound, "not_found", "lead not found")
		return
	}
	rows, err := s.St.ListLeadAudit(c.Member.TenantID, lead.SubmissionRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "audit list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}
