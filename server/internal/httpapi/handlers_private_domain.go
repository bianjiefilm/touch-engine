package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
	"github.com/bianjiefilm/touch-engine/server/internal/privatedomain"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Private domain (HUI-1673). Guests only see a merchant-configured WeCom
// contact link or community link that the current channel can open. A click
// stays click_wecom / click_community with an unknown result. It does not add
// a friend, join a group, create a contact, import CRM, grant a reward, or
// redeem a coupon. There is no WeCom callback, so the guide is not connected.

func (s *Server) handlePublicPrivateDomain(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	items, err := s.privateDomainConfig(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "private domain lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, privateDomainPayload(r.URL.Query().Get("channel"), items))
}

func (s *Server) handlePrivateDomainPut(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	decision := authz.Authorize(s.authzMember(c), authz.ActionManageExtraJumps, authz.RecordScope{TenantID: c.Member.TenantID})
	if !decision.Allowed {
		fail(w, http.StatusForbidden, decision.Reason, "not allowed to change private domain entries")
		return
	}
	var body struct {
		Entries []struct {
			Kind    string `json:"kind"`
			Enabled bool   `json:"enabled"`
			Href    string `json:"href"`
		} `json:"entries"`
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
	items := normalizePrivateDomain(body.Entries)
	if err := s.St.ReplacePrivateDomain(c.Member.TenantID, campaignID, items); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "private domain entries could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, privateDomainPayload("web", items))
}

func (s *Server) handlePublicPrivateDomainClick(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	kind := privatedomain.Kind(strings.TrimSpace(r.PathValue("kind")))
	items, err := s.privateDomainConfig(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "private domain lookup failed")
		return
	}
	shown, _ := privatedomain.Present(r.URL.Query().Get("channel"), items)
	visible := false
	for _, item := range shown {
		if item.Kind == kind {
			visible = true
			break
		}
	}
	if !visible {
		fail(w, http.StatusNotFound, "not_configured", "this private domain entry is not available")
		return
	}
	click := privatedomain.RecordClick(kind)
	if err := s.St.RecordPrivateDomainClick(res.Campaign.TenantID, res.Campaign.ID, click); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "private domain click could not be recorded")
		return
	}
	writeJSON(w, http.StatusOK, privateDomainClickPayload(click))
}

func (s *Server) privateDomainConfig(tenantID, campaignID string) ([]privatedomain.Configured, error) {
	items, err := s.St.ListPrivateDomain(tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	hasWecom := false
	for _, item := range items {
		if item.Kind == privatedomain.KindWecom {
			hasWecom = true
			break
		}
	}
	if hasWecom {
		return items, nil
	}
	jumps, err := s.St.ListExtraJumps(tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	for _, jump := range jumps {
		if jump.Kind == extrajump.KindWecom {
			items = append(items, privatedomain.Configured{
				Kind: privatedomain.KindWecom, Enabled: jump.Enabled, Href: jump.Href,
			})
		}
	}
	return items, nil
}

func normalizePrivateDomain(raw []struct {
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
	Href    string `json:"href"`
}) []privatedomain.Configured {
	byKind := map[privatedomain.Kind]privatedomain.Configured{}
	officialKept := map[privatedomain.Kind]bool{}
	for _, item := range raw {
		kind := privatedomain.Kind(strings.TrimSpace(item.Kind))
		if kind != privatedomain.KindWecom && kind != privatedomain.KindCommunity {
			continue
		}
		next := privatedomain.Configured{Kind: kind, Enabled: item.Enabled, Href: strings.TrimSpace(item.Href)}
		if officialKept[kind] {
			continue
		}
		byKind[kind] = next
		if next.Enabled {
			shown, _ := privatedomain.Present("web", []privatedomain.Configured{next})
			if len(shown) == 1 {
				officialKept[kind] = true
			}
		}
	}
	var out []privatedomain.Configured
	for _, kind := range []privatedomain.Kind{privatedomain.KindWecom, privatedomain.KindCommunity} {
		if item, ok := byKind[kind]; ok {
			out = append(out, item)
		}
	}
	return out
}

func privateDomainPayload(channel string, items []privatedomain.Configured) map[string]any {
	shown, closed := privatedomain.Present(channel, items)
	guide := privatedomain.Summarize(shown)
	entries := make([]map[string]any, 0, len(shown))
	for _, item := range shown {
		entries = append(entries, map[string]any{
			"kind": string(item.Kind), "href": item.Href, "event": item.Event,
			"available": true, "result": "ready",
		})
	}
	closedRows := make([]map[string]any, 0, len(closed))
	for _, item := range closed {
		closedRows = append(closedRows, map[string]any{
			"kind": string(item.Kind), "shown": false, "reason": item.Reason, "connected": false,
		})
	}
	return map[string]any{
		"entries": entries, "closed": closedRows,
		"connected": false, "redemption": guide.Redemption,
		"creates_lead": false, "crm_imported": false, "reward_triggered": false,
		"silent_add": false, "forced_join": false, "background_marketing": false,
	}
}

func privateDomainClickPayload(click privatedomain.Click) map[string]any {
	return map[string]any{
		"event": click.Event, "kind": string(click.Kind), "recorded_as": "click", "success": false,
		"platform_result": "unknown", "added": false, "joined": false, "contact_created": false,
		"lead_created": false, "crm_imported": false, "reward_triggered": false,
		"connected": false, "redemption": "unknown", "followed": false,
		"silent_add": false, "forced_join": false, "background_marketing": false,
	}
}
