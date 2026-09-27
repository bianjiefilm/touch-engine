package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/brandctx"
	"github.com/bianjiefilm/touch-engine/server/internal/brandhost"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// brandGate is the brand shell attached to an authenticated request.
// It never grants membership and never selects a payer.
type brandGate struct {
	Host     string
	Result   brandctx.Result
	Tenant   store.Tenant
	TouchOn  bool
}

func (s *Server) attachBrand(w http.ResponseWriter, r *http.Request, tenantID string, mutating bool) (*brandGate, bool) {
	if !s.Cfg.FeatureBrand {
		return nil, true
	}
	host, err := brandhost.FromRequest(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_host", "public host is not a brand identity")
		return nil, false
	}
	res, err := s.Brand.Read(r.Context(), host, !mutating)
	if err != nil && res.Kind == brandctx.KindUnavailable {
		fail(w, http.StatusServiceUnavailable, "brand_unavailable", "brand registry could not be read; refusing to act (fail-closed)")
		return nil, false
	}
	tenant, err := s.St.GetTenant(tenantID)
	if err != nil {
		fail(w, http.StatusForbidden, authz.ReasonNotMember, "tenant is not available")
		return nil, false
	}
	if strings.TrimSpace(tenant.BrandID) == "" {
		fail(w, http.StatusConflict, "brand_unbound", "tenant has no brand binding; host cannot assign one")
		return nil, false
	}
	switch res.Kind {
	case brandctx.KindUnknown:
		fail(w, http.StatusNotFound, "unknown_brand", "host is not a registered brand")
		return nil, false
	case brandctx.KindDomain:
		fail(w, http.StatusForbidden, "domain_error", "brand domain is not admitted")
		return nil, false
	case brandctx.KindSuspended:
		fail(w, http.StatusForbidden, "brand_suspended", "brand is suspended; historical records stay, new entry is refused")
		return nil, false
	case brandctx.KindRetired:
		fail(w, http.StatusForbidden, "brand_retiring", "brand is retiring")
		return nil, false
	case brandctx.KindUnavailable, brandctx.KindInvalid:
		fail(w, http.StatusServiceUnavailable, "brand_unavailable", "brand registry could not be read; refusing to act (fail-closed)")
		return nil, false
	}
	if res.Manifest.BrandID != tenant.BrandID {
		fail(w, http.StatusForbidden, "brand_mismatch", "host brand does not match this tenant; tenant and payer are unchanged")
		return nil, false
	}
	if mutating && !res.Manifest.AdmitLogin {
		fail(w, http.StatusForbidden, "brand_not_admitted", "brand is not admitting authenticated actions")
		return nil, false
	}
	return &brandGate{
		Host: host, Result: res, Tenant: tenant,
		TouchOn: res.Manifest.TouchEnabled(s.Cfg.AppID),
	}, true
}

// denyFrozenWrite blocks mutations that the tenant lifecycle does not allow.
// charge_hold blocks only billable creates. History reads never call this.
func (s *Server) denyFrozenWrite(c *caller, billable bool, w http.ResponseWriter) bool {
	if !s.Cfg.FeatureBrand || c == nil || c.Member == nil {
		return false
	}
	t, err := s.St.GetTenant(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant lookup failed")
		return true
	}
	switch t.Lifecycle {
	case "", store.LifecycleActive:
		if billable && t.ChargeHold {
			fail(w, http.StatusConflict, "charge_hold", "paid actions are held; existing campaigns stay visible")
			return true
		}
		return false
	case store.LifecycleSuspended:
		fail(w, http.StatusConflict, "lifecycle_suspended", "tenant is suspended; history stays readable, new changes are refused")
		return true
	case store.LifecycleSecurityFreeze:
		fail(w, http.StatusConflict, "lifecycle_security_freeze", "tenant is frozen; read-only, export refused")
		return true
	case store.LifecycleOffboarding:
		fail(w, http.StatusConflict, "lifecycle_offboarding", "tenant is offboarding; export and history only, no new paid work")
		return true
	case store.LifecycleRetired:
		fail(w, http.StatusConflict, "lifecycle_retired", "tenant is retired")
		return true
	default:
		fail(w, http.StatusConflict, "lifecycle_unknown", "tenant lifecycle is not active")
		return true
	}
}

func (s *Server) handlePublicBrandShell(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "brand shell is read-only")
		return
	}
	host, err := brandhost.FromRequest(r)
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_host", "public host is not a brand identity")
		return
	}
	res, err := s.Brand.Read(r.Context(), host, true)
	if err != nil && res.Kind == brandctx.KindUnavailable {
		fail(w, http.StatusServiceUnavailable, "brand_unavailable", "brand registry could not be read")
		return
	}
	switch res.Kind {
	case brandctx.KindUnknown:
		writeJSON(w, http.StatusNotFound, map[string]any{"state": "unknown_brand", "safe_page": "unknown_brand"})
		return
	case brandctx.KindSuspended:
		writeJSON(w, http.StatusForbidden, map[string]any{
			"state": "brand_suspended", "display_name": res.Manifest.DisplayName, "safe_page": "brand_unavailable",
		})
		return
	case brandctx.KindRetired:
		writeJSON(w, http.StatusForbidden, map[string]any{"state": "brand_retiring", "display_name": res.Manifest.DisplayName})
		return
	case brandctx.KindDomain:
		writeJSON(w, http.StatusForbidden, map[string]any{"state": "domain_error"})
		return
	case brandctx.KindReady:
		// Display shell only. No tenant, no other apps, no payer.
		writeJSON(w, http.StatusOK, map[string]any{
			"state":          "ready",
			"display_name":   res.Manifest.DisplayName,
			"logo_ref":       res.Manifest.LogoRef,
			"theme":          res.Manifest.Theme,
			"support_name":   res.Manifest.SupportName,
			"support_contact": res.Manifest.SupportContact,
			"auth_headline":  res.Manifest.AuthHeadline,
			"touch_enabled":  res.Manifest.TouchEnabled(s.Cfg.AppID),
		})
	default:
		fail(w, http.StatusServiceUnavailable, "brand_unavailable", "brand registry could not be read")
	}
}

func (s *Server) brandShellJSON(g *brandGate) map[string]any {
	if g == nil {
		return nil
	}
	m := g.Result.Manifest
	return map[string]any{
		"brand_id":        m.BrandID,
		"display_name":    m.DisplayName,
		"logo_ref":        m.LogoRef,
		"theme":           m.Theme,
		"support_name":    m.SupportName,
		"support_contact": m.SupportContact,
		"config_version":  m.ConfigVersion,
		"touch_enabled":   g.TouchOn,
	}
}

func (s *Server) handleTenantLifecycle(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authzScope(c), w) {
		return
	}
	var in struct {
		Lifecycle string `json:"lifecycle"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "lifecycle is required")
		return
	}
	before, err := s.St.ListMembers(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "member list failed")
		return
	}
	if err := s.St.SetTenantLifecycle(c.Member.TenantID, strings.TrimSpace(in.Lifecycle)); err != nil {
		fail(w, http.StatusBadRequest, "bad_lifecycle", "lifecycle must be active, suspended, security_freeze, offboarding, or retired")
		return
	}
	after, err := s.St.ListMembers(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "member list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lifecycle": in.Lifecycle,
		"members_unchanged": membersSame(before, after),
		"note": "restoring a tenant does not re-enable revoked staff, agents, or delegations",
	})
}

func membersSame(a, b []store.Member) bool {
	if len(a) != len(b) {
		return false
	}
	byID := map[string]store.Member{}
	for _, m := range a {
		byID[m.ID] = m
	}
	for _, m := range b {
		prev, ok := byID[m.ID]
		if !ok || prev.Enabled != m.Enabled || prev.Role != m.Role {
			return false
		}
	}
	return true
}

func (s *Server) handleTenantChargeHold(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authzScope(c), w) {
		return
	}
	var in struct {
		Held bool `json:"held"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "held is required")
		return
	}
	if err := s.St.SetChargeHold(c.Member.TenantID, in.Held); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "charge hold update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"charge_hold": in.Held})
}

func (s *Server) handleTenantExportCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authzScope(c), w) {
		return
	}
	t, err := s.St.GetTenant(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tenant lookup failed")
		return
	}
	switch t.Lifecycle {
	case "", store.LifecycleActive, store.LifecycleSuspended, store.LifecycleOffboarding:
	default:
		fail(w, http.StatusConflict, "export_refused", "export is refused in this lifecycle")
		return
	}
	var in struct {
		Purpose string `json:"purpose"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "purpose is required")
		return
	}
	purpose := strings.TrimSpace(in.Purpose)
	if purpose == "" || len(purpose) > 80 || strings.ContainsAny(purpose, "\r\n") {
		fail(w, http.StatusBadRequest, "bad_purpose", "purpose must be a short single-line reason")
		return
	}
	g := brandFrom(r)
	display, version := "", int64(0)
	if g != nil {
		display = g.Result.Manifest.DisplayName
		version = g.Result.Manifest.ConfigVersion
	}
	manifest, err := s.St.BuildExportManifest(t.ID, t.BrandID, c.Principal.ID, purpose, display, version)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export build failed")
		return
	}
	raw, err := store.MarshalManifest(manifest)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export encode failed")
		return
	}
	job, err := s.St.CreateExport(t.ID, t.BrandID, c.Principal.ID, purpose, raw, time.Now().Add(15*time.Minute))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export store failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": job.ID, "brand_id": job.BrandID, "tenant_id": job.TenantID,
		"requester_principal": job.Requester, "purpose": job.Purpose, "expires_at": job.ExpiresAt,
	})
}

func (s *Server) handleTenantExportGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authzScope(c), w) {
		return
	}
	job, err := s.St.GetExport(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || job.TenantID != c.Member.TenantID || job.Requester != c.Principal.ID {
		fail(w, http.StatusForbidden, "export_denied", "export is bound to its original tenant and requester")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export lookup failed")
		return
	}
	if job.Revoked {
		fail(w, http.StatusGone, "export_revoked", "export was revoked")
		return
	}
	exp, err := time.Parse(time.RFC3339, job.ExpiresAt)
	if err != nil || time.Now().After(exp) {
		fail(w, http.StatusGone, "export_expired", "export has expired")
		return
	}
	t, err := s.St.GetTenant(c.Member.TenantID)
	if err != nil || t.BrandID != job.BrandID {
		fail(w, http.StatusForbidden, "export_denied", "export stayed bound to its original brand")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(job.Manifest))
}

func (s *Server) handleTenantExportRevoke(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionManageMembers, authzScope(c), w) {
		return
	}
	job, err := s.St.GetExport(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) || job.TenantID != c.Member.TenantID {
		fail(w, http.StatusNotFound, "not_found", "export not found")
		return
	}
	if err := s.St.RevokeExport(job.ID, c.Member.TenantID); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "export revoke failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true, "id": job.ID})
}
