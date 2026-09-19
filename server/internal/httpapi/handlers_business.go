package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

func authzScope(c *caller) authz.RecordScope {
	if c == nil || c.Member == nil {
		return authz.RecordScope{}
	}
	return authz.RecordScope{TenantID: c.Member.TenantID}
}

// ---- stores -----------------------------------------------------------------

func (s *Server) handleStoreCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authzScope(c), w) {
		return
	}
	var in struct {
		Name    string `json:"name"`
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "store name is required")
		return
	}
	rec, err := s.St.CreateStore(c.Member.TenantID, in.Name, in.Address, c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store create failed")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleStoreList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authzScope(c), w) {
		return
	}
	items, err := s.St.ListStores(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleStoreGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadRecord, authzScope(c), w) {
		return
	}
	rec, err := s.St.GetStore(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "store not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// ---- campaigns ---------------------------------------------------------------

type campaignIn struct {
	Title         string `json:"title"`
	PublicContent string `json:"public_content"`
	StartsAt      string `json:"starts_at"`
	EndsAt        string `json:"ends_at"`
	StoreID       string `json:"store_id"`
	OrderRef      string `json:"order_ref"` // optional opaque reference; empty = 无订单活动
}

func (s *Server) handleCampaignCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authzScope(c), w) {
		return
	}
	var in campaignIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Title) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "campaign title is required")
		return
	}
	if in.StartsAt != "" || in.EndsAt != "" {
		if err := validateWindow(in.StartsAt, in.EndsAt); err != "" {
			fail(w, http.StatusBadRequest, "bad_window", err)
			return
		}
	}
	if in.StoreID != "" {
		if _, err := s.St.GetStore(in.StoreID, c.Member.TenantID); err != nil {
			fail(w, http.StatusBadRequest, "bad_store", "store not found in this tenant")
			return
		}
	}
	rec, err := s.St.CreateCampaign(store.NewCampaign{
		TenantID: c.Member.TenantID, Title: in.Title, PublicContent: in.PublicContent,
		StartsAt: in.StartsAt, EndsAt: in.EndsAt, StoreID: in.StoreID,
		OrderRef: in.OrderRef, CreatedBy: c.Member.PrincipalRef,
	})
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign create failed")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func validateWindow(startsAt, endsAt string) string {
	if startsAt != "" {
		if _, err := parseRFC3339(startsAt); err != nil {
			return "starts_at must be RFC3339"
		}
	}
	if endsAt != "" {
		if _, err := parseRFC3339(endsAt); err != nil {
			return "ends_at must be RFC3339"
		}
	}
	if startsAt != "" && endsAt != "" {
		st, _ := parseRFC3339(startsAt)
		en, _ := parseRFC3339(endsAt)
		if en.Before(st) {
			return "ends_at must not be before starts_at"
		}
	}
	return ""
}

func (s *Server) handleCampaignList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authzScope(c), w) {
		return
	}
	items, err := s.St.ListCampaigns(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleCampaignGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadRecord, authzScope(c), w) {
		return
	}
	rec, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleCampaignPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionUpdate, authzScope(c), w) {
		return
	}
	var in struct {
		Title         *string `json:"title"`
		PublicContent *string `json:"public_content"`
		StartsAt      *string `json:"starts_at"`
		EndsAt        *string `json:"ends_at"`
		StoreID       *string `json:"store_id"`
		Status        *string `json:"status"` // accepted only to be refused: use /status
		OrderRef      *string `json:"order_ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if in.Status != nil {
		fail(w, http.StatusBadRequest, "use_status_endpoint", "campaign status changes must use POST /campaigns/{id}/status")
		return
	}
	if in.OrderRef != nil {
		fail(w, http.StatusBadRequest, "order_ref_immutable", "order_ref is fixed at creation")
		return
	}
	if (in.StartsAt != nil && *in.StartsAt != "") || (in.EndsAt != nil && *in.EndsAt != "") {
		cur, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID)
		if err != nil {
			fail(w, http.StatusNotFound, "not_found", "campaign not found")
			return
		}
		st := cur.StartsAt
		en := cur.EndsAt
		if in.StartsAt != nil {
			st = *in.StartsAt
		}
		if in.EndsAt != nil {
			en = *in.EndsAt
		}
		if msg := validateWindow(st, en); msg != "" {
			fail(w, http.StatusBadRequest, "bad_window", msg)
			return
		}
	}
	if in.StoreID != nil && *in.StoreID != "" {
		if _, err := s.St.GetStore(*in.StoreID, c.Member.TenantID); err != nil {
			fail(w, http.StatusBadRequest, "bad_store", "store not found in this tenant")
			return
		}
	}
	rec, err := s.St.UpdateCampaign(r.PathValue("id"), c.Member.TenantID, store.CampaignPatch{
		Title: in.Title, PublicContent: in.PublicContent, StartsAt: in.StartsAt, EndsAt: in.EndsAt, StoreID: in.StoreID,
	})
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign update failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleCampaignStatus(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionUpdate, authzScope(c), w) {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	to := campaign.Status(in.Status)
	if !to.Valid() {
		fail(w, http.StatusBadRequest, "bad_status", "status must be one of draft/active/paused/ended")
		return
	}
	rec, err := s.St.TransitionCampaign(r.PathValue("id"), c.Member.TenantID, to)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if errors.Is(err, campaign.ErrIllegalTransition) {
		fail(w, http.StatusConflict, "illegal_transition", err.Error())
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "status change failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// ---- campaign assets (素材引用,不复制文件) ---------------------------------------

func (s *Server) handleAssetAdd(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authzScope(c), w) {
		return
	}
	var in struct {
		AssetID string `json:"asset_id"`
		Version string `json:"version"` // optional; must equal platform sha256 when given
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.AssetID) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "asset_id is required")
		return
	}

	// campaign scope first: another tenant's campaign must be invisible here
	if _, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}

	// 平台接入纪律:素材引用必须经平台 upload 校验。开关关 = 显式拒绝,不伪造授权。
	if !s.Cfg.FeatureUpload {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "upload_disabled",
			"message": "FEATURE_UPLOAD is off; material references cannot be validated (fail-closed)",
		})
		return
	}

	meta, err := s.Upload.ValidateRef(r.Context(), in.AssetID, in.Version)
	switch {
	case err == nil:
		// continue below
	case errors.Is(err, upload.ErrNotFound):
		fail(w, http.StatusNotFound, "upload_not_found", "asset does not exist (or deleted/quarantined) at platform upload")
		return
	case errors.Is(err, upload.ErrUnauthorized):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "upload_unavailable",
			"message": "platform upload rejected this app's credentials; check PLATFORM_UPLOAD_TOKEN",
		})
		return
	case errors.Is(err, upload.ErrForbidden):
		fail(w, http.StatusForbidden, "upload_forbidden", "asset exists but this app may not reference it")
		return
	case errors.Is(err, upload.ErrNotReady):
		fail(w, http.StatusConflict, "upload_not_ready", "asset is not in ready state")
		return
	case errors.Is(err, upload.ErrVersionMismatch):
		fail(w, http.StatusConflict, "version_mismatch", "supplied version does not match platform sha256")
		return
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "upload_unavailable",
			"message": "platform upload could not be reached; refusing to record a reference (fail-closed)",
		})
		return
	}

	rec, err := s.St.AddCampaignAsset(c.Member.TenantID, r.PathValue("id"), meta.AssetID, meta.SHA256, c.Member.PrincipalRef)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if err != nil {
		fail(w, http.StatusConflict, "asset_ref_conflict", "this asset version is already referenced (or storage error)")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleAssetList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authzScope(c), w) {
		return
	}
	// campaign scope first: another tenant's campaign must be invisible here
	if _, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	items, err := s.St.ListCampaignAssets(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "asset list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAssetRemove(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionUpdate, authzScope(c), w) {
		return
	}
	if err := s.St.RemoveCampaignAsset(r.PathValue("assetId"), c.Member.TenantID, r.PathValue("id")); err != nil {
		fail(w, http.StatusNotFound, "not_found", "asset reference not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// ---- campaign links (受控定位短码) ------------------------------------------------

func (s *Server) handleLinkCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionCreate, authzScope(c), w) {
		return
	}
	rec, err := s.St.CreateLink(c.Member.TenantID, r.PathValue("id"), c.Member.PrincipalRef)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "link create failed")
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (s *Server) handleLinkList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionReadList, authzScope(c), w) {
		return
	}
	// campaign scope first: another tenant's campaign must be invisible here
	if _, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	items, err := s.St.ListLinksByCampaign(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "link list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleLinkEnabled(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionUpdate, authzScope(c), w) {
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Enabled == nil {
		fail(w, http.StatusBadRequest, "bad_request", "enabled (boolean) is required")
		return
	}
	rec, err := s.St.SetLinkEnabled(r.PathValue("linkId"), c.Member.TenantID, *in.Enabled)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "link not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "link update failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
