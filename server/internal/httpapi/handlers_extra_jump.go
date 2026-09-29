package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Extra jumps (HUI-1672). The public page lists only configured official
// addresses. A click stays an unknown platform result and does not create a
// lead, import CRM, grant a reward, or count as a publish. Native launch is
// unverified. An authorized return is not an arbitrary external URL.

type jumpActionIn struct {
	Kind      string `json:"kind"`
	Enabled   bool   `json:"enabled"`
	Href      string `json:"href"`
	Revoked   bool   `json:"revoked"`
	ExpiresAt string `json:"expires_at"`
}

type returnIn struct {
	Enabled   bool   `json:"enabled"`
	Href      string `json:"href"`
	Revoked   bool   `json:"revoked"`
	ExpiresAt string `json:"expires_at"`
}

func (s *Server) handlePublicExtraJumps(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	if reason, blocked := s.jumpBlock(r, res); blocked {
		writeJSON(w, http.StatusNotFound, blockedJumpPayload(reason))
		return
	}
	items, ret, hosts, err := s.loadJumpMatrix(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jump lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, jumpDocument(items, ret, hosts, time.Now().UTC(), false))
}

func (s *Server) handleExtraJumpsGet(w http.ResponseWriter, r *http.Request) {
	c, campaignID, ok := s.jumpEditor(w, r)
	if !ok {
		return
	}
	items, ret, hosts, err := s.loadJumpMatrix(c.Member.TenantID, campaignID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jump lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, jumpDocument(items, ret, hosts, time.Now().UTC(), true))
}

func (s *Server) handleExtraJumpsPut(w http.ResponseWriter, r *http.Request) {
	c, campaignID, ok := s.jumpEditor(w, r)
	if !ok {
		return
	}
	var body struct {
		Actions *[]jumpActionIn `json:"actions"`
		Return  *returnIn       `json:"return"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	hosts, err := s.returnHosts(c.Member.TenantID, campaignID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "return hosts could not be read")
		return
	}
	var items *[]extrajump.Configured
	if body.Actions != nil {
		normalized := normalizeExtraJumps(*body.Actions)
		for _, item := range normalized {
			if !validExpiry(item.ExpiresAt) {
				fail(w, http.StatusBadRequest, "bad_expires_at", "expires_at must be RFC3339")
				return
			}
		}
		items = &normalized
	}
	var ret *extrajump.ReturnConfig
	if body.Return != nil {
		cfg := extrajump.ReturnConfig{
			Href: strings.TrimSpace(body.Return.Href), Enabled: body.Return.Enabled,
			Revoked: body.Return.Revoked, ExpiresAt: strings.TrimSpace(body.Return.ExpiresAt),
		}
		if !validExpiry(cfg.ExpiresAt) {
			fail(w, http.StatusBadRequest, "bad_expires_at", "expires_at must be RFC3339")
			return
		}
		if cfg.Href != "" && !extrajump.AllowReturn(cfg.Href, hosts) {
			fail(w, http.StatusBadRequest, "unregistered_url", "that return address is not registered")
			return
		}
		ret = &cfg
	}
	if err := s.St.SaveJumpMatrix(c.Member.TenantID, campaignID, items, ret); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jumps could not be saved")
		return
	}
	savedItems, savedReturn, _, err := s.loadJumpMatrix(c.Member.TenantID, campaignID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jump lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, jumpDocument(savedItems, savedReturn, hosts, time.Now().UTC(), false))
}

func (s *Server) handlePublicExtraJumpClick(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	if reason, blocked := s.jumpBlock(r, res); blocked {
		writeJSON(w, http.StatusNotFound, blockedJumpPayload(reason))
		return
	}
	kind := extrajump.Kind(strings.TrimSpace(r.PathValue("kind")))
	items, err := s.St.ListExtraJumps(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jump lookup failed")
		return
	}
	shown, _ := extrajump.Present(items)
	href, visible := extrajump.CanonicalHref(shown, kind, "")
	if !visible {
		fail(w, http.StatusNotFound, "not_configured", "this jump is not available")
		return
	}
	click := extrajump.RecordClick(kind)
	click.Href = href
	if err := s.St.RecordExtraJumpClick(res.Campaign.TenantID, res.Campaign.ID, click); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "extra jump click could not be recorded")
		return
	}
	writeJSON(w, http.StatusOK, clickPayload(click))
}

func (s *Server) handlePublicReturnClick(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	if reason, blocked := s.jumpBlock(r, res); blocked {
		writeJSON(w, http.StatusNotFound, blockedJumpPayload(reason))
		return
	}
	_, ret, hosts, err := s.loadJumpMatrix(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "return lookup failed")
		return
	}
	view := extrajump.DecideReturn(ret, time.Now().UTC(), hosts)
	if !view.Shown {
		fail(w, http.StatusNotFound, view.Reason, "this return is not available")
		return
	}
	if err := s.St.RecordAuthorizedReturnClick(res.Campaign.TenantID, res.Campaign.ID); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "return click could not be recorded")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": "return", "class": extrajump.ClassReturn, "href": view.Href,
		"recorded_as": "click", "success": false, "platform_result": "unknown",
		"added": false, "followed": false, "lead_created": false, "crm_imported": false,
		"reward_triggered": false, "publish_success": false,
		"auto_follow": false, "auto_join": false, "auto_pay": false,
		"evidence": evidenceFields(view.Web, view.ClientLaunch, view.PlatformAction),
	})
}

func (s *Server) jumpEditor(w http.ResponseWriter, r *http.Request) (*caller, string, bool) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return nil, "", false
	}
	decision := authz.Authorize(s.authzMember(c), authz.ActionManageExtraJumps, authz.RecordScope{TenantID: c.Member.TenantID})
	if !decision.Allowed {
		fail(w, http.StatusForbidden, decision.Reason, "not allowed to change extra jumps")
		return nil, "", false
	}
	campaignID := r.PathValue("id")
	if _, err := s.St.GetCampaign(campaignID, c.Member.TenantID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, "not_found", "campaign not found")
			return nil, "", false
		}
		fail(w, http.StatusInternalServerError, "internal", "campaign lookup failed")
		return nil, "", false
	}
	return c, campaignID, true
}

func (s *Server) jumpBlock(r *http.Request, res store.ResolvedLink) (string, bool) {
	if !s.Cfg.FeatureBrand {
		return "", false
	}
	d := s.decidePublicBrand(r, res)
	return extrajump.BlockReason(d.Apply, d.State)
}

func (s *Server) loadJumpMatrix(tenantID, campaignID string) ([]extrajump.Configured, extrajump.ReturnConfig, []string, error) {
	items, err := s.St.ListExtraJumps(tenantID, campaignID)
	if err != nil {
		return nil, extrajump.ReturnConfig{}, nil, err
	}
	ret, err := s.St.GetAuthorizedReturn(tenantID, campaignID)
	if err != nil {
		return nil, extrajump.ReturnConfig{}, nil, err
	}
	hosts, err := s.returnHosts(tenantID, campaignID)
	if err != nil {
		return nil, extrajump.ReturnConfig{}, nil, err
	}
	return items, ret, hosts, nil
}

func (s *Server) returnHosts(tenantID, campaignID string) ([]string, error) {
	var hosts []string
	if host := hostOfBase(s.Cfg.PublicBaseURL); host != "" {
		hosts = append(hosts, host)
	}
	more, err := s.St.CampaignPublishedHosts(tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	hosts = append(hosts, more...)
	return hosts, nil
}

func hostOfBase(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.ToLower(parsed.Hostname())
}

func normalizeExtraJumps(raw []jumpActionIn) []extrajump.Configured {
	byKind := map[extrajump.Kind]extrajump.Configured{}
	for _, item := range raw {
		kind := extrajump.Kind(strings.TrimSpace(item.Kind))
		switch kind {
		case extrajump.KindWifi, extrajump.KindNavigate, extrajump.KindReview, extrajump.KindWecom, extrajump.KindFollow:
			byKind[kind] = extrajump.Configured{
				Kind: kind, Enabled: item.Enabled, Href: strings.TrimSpace(item.Href),
				Revoked: item.Revoked, ExpiresAt: strings.TrimSpace(item.ExpiresAt),
			}
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

func jumpDocument(items []extrajump.Configured, ret extrajump.ReturnConfig, hosts []string, now time.Time, includeStored bool) map[string]any {
	shown, closed := extrajump.PresentAt(items, now)
	actions := make([]map[string]any, 0, len(shown))
	for _, item := range shown {
		actions = append(actions, map[string]any{
			"kind": string(item.Kind), "class": item.Class, "href": item.Href,
			"available": item.Available, "result": item.Result,
			"evidence": evidenceFields(item.Web, item.ClientLaunch, item.PlatformAction),
		})
	}
	closedRows := make([]map[string]any, 0, len(closed))
	for _, item := range closed {
		closedRows = append(closedRows, map[string]any{
			"kind": string(item.Kind), "shown": false, "reason": item.Reason,
			"evidence": evidenceFields(extrajump.EvidenceWebClosed, extrajump.EvidenceClientUnverified, extrajump.EvidencePlatformUnknown),
		})
	}
	view := extrajump.DecideReturn(ret, now, hosts)
	out := map[string]any{
		"actions": actions, "closed": closedRows, "return": returnPayload(view), "support": supportPayload(),
		"creates_lead": false, "crm_imported": false, "reward_triggered": false, "publish_success": false,
		"auto_follow": false, "auto_join": false, "auto_pay": false,
	}
	if includeStored {
		stored := make([]map[string]any, 0, len(items))
		for _, item := range items {
			stored = append(stored, map[string]any{
				"kind": string(item.Kind), "href": item.Href, "enabled": item.Enabled,
				"revoked": item.Revoked, "expires_at": item.ExpiresAt,
			})
		}
		out["configured"] = stored
		out["return_configured"] = map[string]any{
			"href": ret.Href, "enabled": ret.Enabled, "revoked": ret.Revoked, "expires_at": ret.ExpiresAt,
		}
		out["return_hosts"] = hosts
	}
	return out
}

func blockedJumpPayload(reason string) map[string]any {
	closed := make([]map[string]any, 0, 5)
	for _, kind := range []extrajump.Kind{extrajump.KindWifi, extrajump.KindNavigate, extrajump.KindReview, extrajump.KindWecom, extrajump.KindFollow} {
		closed = append(closed, map[string]any{"kind": string(kind), "shown": false, "reason": reason})
	}
	return map[string]any{
		"state": reason, "actions": []any{}, "closed": closed,
		"return": map[string]any{
			"shown": false, "class": extrajump.ClassReturn, "reason": reason,
			"evidence": evidenceFields(extrajump.EvidenceWebClosed, extrajump.EvidenceClientUnverified, extrajump.EvidencePlatformUnknown),
		},
		"support":      supportPayload(),
		"creates_lead": false, "crm_imported": false, "reward_triggered": false, "publish_success": false,
		"auto_follow": false, "auto_join": false, "auto_pay": false,
	}
}

func returnPayload(view extrajump.ReturnView) map[string]any {
	out := map[string]any{
		"shown": view.Shown, "class": view.Class, "reason": view.Reason,
		"evidence": evidenceFields(view.Web, view.ClientLaunch, view.PlatformAction),
	}
	if view.Shown {
		out["href"] = view.Href
	}
	return out
}

func supportPayload() []map[string]any {
	rows := extrajump.SupportMatrix()
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"kind": row.Kind, "class": row.Class, "label": row.Label, "web": row.Web,
			"client_launch": row.ClientLaunch, "platform_action": row.PlatformAction,
			"native_reason": row.NativeReason,
			"auto_follow":   false, "auto_join": false, "auto_pay": false,
		})
	}
	return out
}

func evidenceFields(web, client, platform string) map[string]any {
	return map[string]any{"web": web, "client_launch": client, "platform_action": platform}
}

func clickPayload(click extrajump.Click) map[string]any {
	return map[string]any{
		"kind": string(click.Kind), "class": click.Class, "href": click.Href,
		"recorded_as": click.RecordedAs, "success": false, "platform_result": "unknown",
		"added": false, "followed": false, "lead_created": false, "crm_imported": false,
		"reward_triggered": false, "publish_success": false,
		"auto_follow": false, "auto_join": false, "auto_pay": false,
		"evidence": evidenceFields(extrajump.EvidenceWebConfigured, extrajump.EvidenceClientUnverified, extrajump.EvidencePlatformUnknown),
	}
}

func validExpiry(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	if _, err := time.Parse(time.RFC3339, raw); err == nil {
		return true
	}
	_, err := time.Parse(time.RFC3339Nano, raw)
	return err == nil
}
