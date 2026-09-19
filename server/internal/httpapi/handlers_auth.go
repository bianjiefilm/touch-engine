package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/redact"
)

// ---- auth handlers (identity is the only path; no stub mode) ----------------

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Email == "" || in.Password == "" {
		fail(w, http.StatusBadRequest, "bad_request", "email and password are required")
		return
	}
	pair, err := s.ID.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		// Never distinguish user-facing reasons beyond rejection; never log tokens.
		fail(w, http.StatusUnauthorized, "login_rejected", "login rejected by platform identity")
		return
	}
	s.setSessionCookies(w, pair)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	refresh := ""
	if ck, err := r.Cookie(s.refreshCookieName()); err == nil {
		refresh = ck.Value
	}
	if refresh == "" {
		fail(w, http.StatusUnauthorized, "unauthenticated", "no refresh token")
		return
	}
	pair, err := s.ID.Refresh(r.Context(), refresh)
	if err != nil {
		fail(w, http.StatusUnauthorized, "refresh_rejected", "refresh rejected by platform identity")
		return
	}
	s.setSessionCookies(w, pair)
	writeJSON(w, http.StatusOK, map[string]any{"refreshed": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	refresh := ""
	if ck, err := r.Cookie(s.refreshCookieName()); err == nil {
		refresh = ck.Value
	}
	// Best-effort server-side revocation; local cookies are always cleared.
	if refresh != "" {
		userID := ""
		if c := callerFrom(r); c != nil {
			userID = c.Principal.ID
		}
		_ = s.ID.Logout(r.Context(), refresh, userID)
	}
	s.clearSessionCookies(w)
	writeJSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusForbidden, "not_member", "principal is not a member of this tenant")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"principal_ref": c.Principal.ID,
		"email":         redact.MaskEmail(c.Principal.Email),
		"tenant_id":     c.Member.TenantID,
		"role":          c.Member.Role,
		"enabled":       c.Member.Enabled,
	})
}

// ---- member admin (owner only) ------------------------------------------------

func (s *Server) handleMemberList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, "manage_members", authzScope(c), w) {
		return
	}
	members, err := s.St.ListMembers(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "list members failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": members})
}

// handleMemberCreate attaches an ALREADY EXISTING platform principal to this
// tenant. 身份纪律:principal_ref must be an identity-derived usr_* id; emails
// and phone numbers are refused here (they never create principals).
func (s *Server) handleMemberCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, "manage_members", authzScope(c), w) {
		return
	}
	var in struct {
		PrincipalRef string `json:"principal_ref"`
		Role         string `json:"role"`
		DisplayName  string `json:"display_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	ref := strings.TrimSpace(in.PrincipalRef)
	if !strings.HasPrefix(ref, "usr_") || strings.ContainsAny(ref, "@ :/+") {
		fail(w, http.StatusBadRequest, "bad_principal_ref", "principal_ref must be an identity principal id (usr_*); emails/phones never create members")
		return
	}
	if in.Role != "owner" && in.Role != "staff" {
		fail(w, http.StatusBadRequest, "bad_role", "role must be owner or staff")
		return
	}
	m, err := s.St.CreateMember(c.Member.TenantID, ref, in.Role, in.DisplayName, c.Member.PrincipalRef, true)
	if err != nil {
		fail(w, http.StatusConflict, "member_exists", "principal already a member of this tenant (or storage error)")
		return
	}
	writeJSON(w, http.StatusCreated, m)
}

func (s *Server) handleMemberPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, "manage_members", authzScope(c), w) {
		return
	}
	var in struct {
		Role        *string `json:"role"`
		Enabled     *bool   `json:"enabled"`
		DisplayName *string `json:"display_name"`
		// PrincipalRef is accepted only to be refused: it is immutable.
		PrincipalRef *string `json:"principal_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if in.PrincipalRef != nil {
		fail(w, http.StatusBadRequest, "principal_immutable", "principal_ref is immutable; it can only come from identity")
		return
	}
	if in.Role != nil && *in.Role != "owner" && *in.Role != "staff" {
		fail(w, http.StatusBadRequest, "bad_role", "role must be owner or staff")
		return
	}
	m, err := s.St.UpdateMember(r.PathValue("id"), in.Role, in.Enabled, in.DisplayName)
	if err != nil {
		fail(w, http.StatusNotFound, "not_found", "member not found in this tenant scope")
		return
	}
	// member must belong to the caller's tenant
	if m.TenantID != c.Member.TenantID {
		fail(w, http.StatusNotFound, "not_found", "member not found in this tenant scope")
		return
	}
	writeJSON(w, http.StatusOK, m)
}
