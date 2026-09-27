package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/copydraft"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// copyDraftDailyQuota bounds merchant-initiated drafts per tenant per UTC day.
// Replays of the same idempotency key do not consume another slot and never bill.
const copyDraftDailyQuota = 20

type copyDraftBody struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Price          copydraft.Fact    `json:"price"`
	Address        copydraft.Fact    `json:"address"`
	Hours          copydraft.Fact    `json:"hours"`
	Claims         []copydraft.Claim `json:"claims"`
	POINames       []string          `json:"poi_names"`
	ChannelMount   bool              `json:"channel_mount"`
	AssetIDs       []string          `json:"asset_ids"`
}

type copyDraftView struct {
	ID string `json:"id"`
	copydraft.Draft
}

func (s *Server) handleCopyDraftCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	var body copyDraftBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "copy draft body must be JSON")
		return
	}
	key := strings.TrimSpace(body.IdempotencyKey)
	if !validIdempotencyKey(key) {
		fail(w, http.StatusBadRequest, "bad_request", "idempotency_key is required and must be 1-80 letters, digits, _ or -")
		return
	}
	assetIDs, ok := s.confirmedAssetIDs(c.Member.TenantID, camp.ID, body.AssetIDs, w)
	if !ok {
		return
	}
	storeName, storeAddress := s.campaignStoreFacts(c.Member.TenantID, camp.StoreID)
	in := copydraft.Input{
		StoreName:       storeName,
		CampaignTitle:   camp.Title,
		PublicContent:   camp.PublicContent,
		Price:           body.Price,
		Address:         alignAddress(storeAddress, body.Address),
		Hours:           body.Hours,
		Claims:          body.Claims,
		POINames:        body.POINames,
		AssetIDs:        assetIDs,
		ModelAuthorized: false, // no live model transport is attached; do not label a guess as usable
	}
	// channel_mount from the client is ignored. Place names are suggestions
	// until a real channel action (HUI-2251) is available on the server.
	in.ChannelMountAvailable = false
	raw, err := json.Marshal(in)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy draft encode failed")
		return
	}
	hash := store.CopySnapshotHash(string(raw))
	if existing, err := s.St.FindCopyDraftByKey(c.Member.TenantID, camp.ID, key); err == nil {
		if existing.SnapshotHash != hash {
			fail(w, http.StatusConflict, "idempotency_conflict", "这个请求键已经用于另一份资料。请换一个新的键后重试。本次没有扣费。")
			return
		}
		writeStoredDraft(w, http.StatusOK, existing)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusInternalServerError, "internal", "copy draft lookup failed")
		return
	}
	since := time.Now().UTC().Format("2006-01-02") + "T00:00:00Z"
	n, err := s.St.CountCopyDraftsSince(c.Member.TenantID, since)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy draft quota lookup failed")
		return
	}
	if n >= copyDraftDailyQuota {
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":    "quota_exceeded",
			"message":  "今日文案草稿已达上限。请明天再试。本次没有扣费。",
			"billed":   false,
			"recovery": "retry_tomorrow",
		})
		return
	}
	draft := copydraft.Compose(in)
	view := copyDraftView{Draft: draft}
	payload, err := json.Marshal(view)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy draft encode failed")
		return
	}
	saved, err := s.St.InsertCopyDraft(store.CopyDraft{
		TenantID: c.Member.TenantID, CampaignID: camp.ID, IdempotencyKey: key,
		SnapshotHash: hash, Payload: string(payload), CreatedBy: c.Member.PrincipalRef,
	})
	if errors.Is(err, store.ErrIdempotencyConflict) {
		fail(w, http.StatusConflict, "idempotency_conflict", "这个请求键已经用于另一份资料。请换一个新的键后重试。本次没有扣费。")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy draft save failed")
		return
	}
	if saved.Payload == string(payload) || saved.ID != "" && saved.Payload == "" {
		saved.Payload = string(payload)
	}
	writeStoredDraft(w, http.StatusCreated, saved)
}

func (s *Server) handleCopyDraftGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadRecord, w)
	if !ok {
		return
	}
	row, err := s.St.GetCopyDraft(r.PathValue("draftId"), c.Member.TenantID, camp.ID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "文案草稿不存在。若刚切换了商家，请在当前商家下重新生成。")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy draft lookup failed")
		return
	}
	writeStoredDraft(w, http.StatusOK, row)
}

func (s *Server) handleCopyVersionAccept(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	row, err := s.St.GetCopyDraft(r.PathValue("draftId"), c.Member.TenantID, camp.ID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "文案草稿不存在。若刚切换了商家，请在当前商家下重新生成。")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy draft lookup failed")
		return
	}
	ver, created, err := s.St.AcceptCopyVersion(c.Member.TenantID, camp.ID, row.ID, row.Payload, c.Member.PrincipalRef)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "文案草稿不存在。若刚切换了商家，请在当前商家下重新生成。")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy version save failed")
		return
	}
	fresh, err := s.St.GetCampaign(camp.ID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign lookup failed")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"id":                      ver.ID,
		"draft_id":                ver.DraftID,
		"version":                 ver.Version,
		"rewards_triggered":       false,
		"billed":                  false,
		"campaign_status":         fresh.Status,
		"campaign_title":          fresh.Title,
		"campaign_public_content": fresh.PublicContent,
	})
}

func (s *Server) confirmedAssetIDs(tenantID, campaignID string, ids []string, w http.ResponseWriter) ([]string, bool) {
	if ids == nil {
		ids = []string{}
	}
	listed, err := s.St.ListCampaignAssets(tenantID, campaignID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "asset lookup failed")
		return nil, false
	}
	have := map[string]bool{}
	for _, a := range listed {
		have[a.AssetID] = true
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if !have[id] {
			fail(w, http.StatusBadRequest, "asset_not_on_campaign", "所选素材不属于这个活动。请只选择已引用的素材。")
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

func (s *Server) campaignStoreFacts(tenantID, storeID string) (name, address string) {
	if storeID == "" {
		return "", ""
	}
	rec, err := s.St.GetStore(storeID, tenantID)
	if err != nil {
		return "", ""
	}
	return rec.Name, rec.Address
}

func alignAddress(storeAddress string, fact copydraft.Fact) copydraft.Fact {
	if fact.Status != copydraft.StatusConfirmed {
		return fact
	}
	if strings.TrimSpace(storeAddress) == "" || strings.TrimSpace(fact.Text) != strings.TrimSpace(storeAddress) {
		fact.Status = copydraft.StatusUncertain
	}
	return fact
}

func writeStoredDraft(w http.ResponseWriter, status int, row store.CopyDraft) {
	var view copyDraftView
	if err := json.Unmarshal([]byte(row.Payload), &view); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy draft decode failed")
		return
	}
	view.ID = row.ID
	view.Billed = false
	if view.ModelStatus == "" {
		view.ModelStatus = copydraft.ModelNotAuthorized
	}
	if view.Topics == nil {
		view.Topics = []string{}
	}
	writeJSON(w, status, view)
}

func validIdempotencyKey(key string) bool {
	if key == "" || len(key) > 80 {
		return false
	}
	for _, r := range key {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
