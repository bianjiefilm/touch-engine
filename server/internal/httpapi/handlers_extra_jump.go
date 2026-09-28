package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Extra jumps (HUI-1672). The public page lists only configured official
// addresses. A click stays an unknown platform result and does not create a
// lead, import CRM, grant a reward, or count as a publish.

func (s *Server) handlePublicExtraJumps(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	items, err := s.St.ListExtraJumps(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jump lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, extraJumpPayload(items))
}

func (s *Server) handleExtraJumpsPut(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	decision := authz.Authorize(s.authzMember(c), authz.ActionManageExtraJumps, authz.RecordScope{TenantID: c.Member.TenantID})
	if !decision.Allowed {
		fail(w, http.StatusForbidden, decision.Reason, "not allowed to change extra jumps")
		return
	}
	var body struct {
		Actions []struct {
			Kind    string `json:"kind"`
			Enabled bool   `json:"enabled"`
			Href    string `json:"href"`
		} `json:"actions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	campaignID := r.PathValue("id")
	if _, err := s.St.GetCampaign(campaignID, c.Member.TenantID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, "not_found", "campaign not found")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "campaign lookup failed")
		return
	}
	items := normalizeExtraJumps(body.Actions)
	if err := s.St.ReplaceExtraJumps(c.Member.TenantID, campaignID, items); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jumps could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, extraJumpPayload(items))
}

func (s *Server) handlePublicExtraJumpClick(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	kind := extrajump.Kind(strings.TrimSpace(r.PathValue("kind")))
	items, err := s.St.ListExtraJumps(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jump lookup failed")
		return
	}
	shown, _ := extrajump.Present(items)
	visible := false
	for _, item := range shown {
		if item.Kind == kind {
			visible = true
			break
		}
	}
	if !visible {
		fail(w, http.StatusNotFound, "not_configured", "this jump is not available")
		return
	}
	click := extrajump.RecordClick(kind)
	if err := s.St.RecordExtraJumpClick(res.Campaign.TenantID, res.Campaign.ID, click); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jump click could not be recorded")
		return
	}
	writeJSON(w, http.StatusOK, clickPayload(click))
}

func normalizeExtraJumps(raw []struct {
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
	Href    string `json:"href"`
}) []extrajump.Configured {
	byKind := map[extrajump.Kind]extrajump.Configured{}
	for _, item := range raw {
		kind := extrajump.Kind(strings.TrimSpace(item.Kind))
		switch kind {
		case extrajump.KindWifi, extrajump.KindNavigate, extrajump.KindReview, extrajump.KindWecom, extrajump.KindFollow:
			byKind[kind] = extrajump.Configured{Kind: kind, Enabled: item.Enabled, Href: strings.TrimSpace(item.Href)}
		}
	}
	var out []extrajump.Configured
	for _, kind := range []extrajump.Kind{extrajump.KindWifi, extrajump.KindNavigate, extrajump.KindReview, extrajump.KindWecom, extrajump.KindFollow} {
		if item, ok := byKind[kind]; ok {
			out = append(out, item)
		}
	}
	return out
}

func extraJumpPayload(items []extrajump.Configured) map[string]any {
	shown, closed := extrajump.Present(items)
	actions := make([]map[string]any, 0, len(shown))
	for _, item := range shown {
		actions = append(actions, map[string]any{
			"kind": string(item.Kind), "href": item.Href, "available": item.Available, "result": item.Result,
		})
	}
	closedRows := make([]map[string]any, 0, len(closed))
	for _, item := range closed {
		closedRows = append(closedRows, map[string]any{
			"kind": string(item.Kind), "shown": false, "reason": item.Reason,
		})
	}
	return map[string]any{
		"actions": actions, "closed": closedRows,
		"creates_lead": false, "crm_imported": false, "reward_triggered": false, "publish_success": false,
	}
}

func clickPayload(click extrajump.Click) map[string]any {
	return map[string]any{
		"kind": string(click.Kind), "recorded_as": click.RecordedAs, "success": false,
		"platform_result": "unknown", "added": false, "followed": false,
		"lead_created": false, "crm_imported": false, "reward_triggered": false, "publish_success": false,
	}
}
