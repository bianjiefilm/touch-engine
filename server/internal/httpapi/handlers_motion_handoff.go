package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/brandctx"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/storemotion"
)

// motionHandoffVersion is the only version this projection speaks.
const motionHandoffVersion = "touch-motion-handoff/v1alpha1"

type motionHandoffCampaign struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	PublicContent string `json:"public_content"`
	Status        string `json:"status"`
	StartsAt      string `json:"starts_at"`
	EndsAt        string `json:"ends_at"`
}

type motionHandoffStore struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	Status  string `json:"status"`
}

type motionHandoffBrand struct {
	BrandID                  string `json:"brand_id"`
	ContextAvailable         bool   `json:"context_available"`
	ContextUnavailableReason string `json:"context_unavailable_reason,omitempty"`
	BrandConfigVersion       int64  `json:"brand_config_version,omitempty"`
	PublishedBrandID         string `json:"published_brand_id,omitempty"`
	PublishedHost            string `json:"published_host,omitempty"`
}

type motionHandoffOffer struct {
	OfferCopy string `json:"offer_copy"`
	Price     string `json:"price"`
}

type motionHandoffReturn struct {
	Href string `json:"href"`
}

type motionHandoffLanding struct {
	ShortCode        string               `json:"short_code,omitempty"`
	ExtraJumpKinds   []string             `json:"extra_jump_kinds"`
	AuthorizedReturn *motionHandoffReturn `json:"authorized_return,omitempty"`
}

type motionHandoffAsset struct {
	AssetID string `json:"asset_id"`
	Version string `json:"version"`
}

type motionHandoffDisposition struct {
	MotionConsumable bool     `json:"motion_consumable"`
	ReasonCode       string   `json:"reason_code"`
	Required         []string `json:"required"`
}

type motionHandoffBrief struct {
	Version          string                   `json:"version"`
	OriginContextRef string                   `json:"origin_context_ref"`
	GeneratedAt      string                   `json:"generated_at"`
	Campaign         motionHandoffCampaign    `json:"campaign"`
	Store            motionHandoffStore       `json:"store"`
	Brand            motionHandoffBrand       `json:"brand"`
	Offer            motionHandoffOffer       `json:"offer"`
	CTA              string                   `json:"cta"`
	Channels         []string                 `json:"channels"`
	AspectRatios     []string                 `json:"aspect_ratios"`
	Landing          motionHandoffLanding     `json:"landing"`
	Assets           []motionHandoffAsset     `json:"assets"`
	ParamsVersion    int                      `json:"params_version"`
	Digest           string                   `json:"digest"`
	Disposition      motionHandoffDisposition `json:"disposition"`
}

// handleMotionHandoffGet projects the campaign's motion handoff brief.
// Read-only: no insert, no table, no probe call, no feature gate beyond the
// logged-in campaign record read. The brief is input for a future Motion
// consumer; it never claims a render or a successful handoff.
func (s *Server) handleMotionHandoffGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadRecord, w)
	if !ok {
		return
	}
	if camp.StoreID == "" {
		fail(w, http.StatusBadRequest, "store_required", "活动还没有门店，Motion Handoff Brief 需要一个门店。")
		return
	}
	brief, err := s.motionHandoffBrief(r.Context(), c.Member.TenantID, camp)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "motion handoff projection failed")
		return
	}
	writeJSON(w, http.StatusOK, brief)
}

func (s *Server) motionHandoffBrief(ctx context.Context, tenantID string, camp store.Campaign) (motionHandoffBrief, error) {
	brief := motionHandoffBrief{
		Version:          motionHandoffVersion,
		OriginContextRef: storemotion.ContextRef(tenantID, camp.StoreID, camp.ID),
		GeneratedAt:      time.Now().UTC().Format(time.RFC3339),
		Campaign: motionHandoffCampaign{
			ID: camp.ID, Title: camp.Title, PublicContent: camp.PublicContent,
			Status: camp.Status, StartsAt: camp.StartsAt, EndsAt: camp.EndsAt,
		},
		Channels:     []string{},
		AspectRatios: []string{},
		Assets:       []motionHandoffAsset{},
		Landing:      motionHandoffLanding{ExtraJumpKinds: []string{}},
		Disposition: motionHandoffDisposition{
			MotionConsumable: false,
			ReasonCode:       "upstream_unavailable",
			Required:         []string{"public-ai sdk/go motion export", "HUI-2732", "HUI-2733"},
		},
	}

	sto, err := s.St.GetStore(camp.StoreID, tenantID)
	if err != nil {
		return motionHandoffBrief{}, err
	}
	brief.Store = motionHandoffStore{ID: sto.ID, Name: sto.Name, Address: sto.Address, Status: sto.Status}

	// 最新一次商家参数声明。没有就保持空串与 params_version=0，不伪造。
	row, err := s.St.LatestStoreMotion(tenantID, camp.ID)
	if err == nil {
		brief.Offer = motionHandoffOffer{OfferCopy: row.Params.OfferCopy, Price: row.Params.Price}
		brief.CTA = row.Params.CTA
		brief.Channels = splitTokenList(row.Params.Channels)
		brief.AspectRatios = splitTokenList(row.Params.AspectRatios)
		brief.ParamsVersion = row.Version
	} else if !errors.Is(err, store.ErrNotFound) {
		return motionHandoffBrief{}, err
	}

	tenant, err := s.St.GetTenant(tenantID)
	if err != nil {
		return motionHandoffBrief{}, err
	}

	stamps, err := s.St.ListLinkStamps(tenantID, camp.ID)
	if err != nil {
		return motionHandoffBrief{}, err
	}
	var pubBrandID, pubHost string
	for _, st := range stamps {
		if brief.Landing.ShortCode == "" && st.Enabled && st.Code != "" {
			brief.Landing.ShortCode = st.Code
		}
		if pubBrandID == "" && st.PublishedBrandID != "" {
			pubBrandID = st.PublishedBrandID
		}
		if pubHost == "" && st.PublishedHost != "" {
			pubHost = st.PublishedHost
		}
	}

	jumps, err := s.St.ListExtraJumps(tenantID, camp.ID)
	if err != nil {
		return motionHandoffBrief{}, err
	}
	for _, j := range jumps {
		if !j.Revoked {
			brief.Landing.ExtraJumpKinds = append(brief.Landing.ExtraJumpKinds, string(j.Kind))
		}
	}
	sort.Strings(brief.Landing.ExtraJumpKinds)

	ret, err := s.St.GetAuthorizedReturn(tenantID, camp.ID)
	if err != nil {
		return motionHandoffBrief{}, err
	}
	if !ret.Revoked && strings.TrimSpace(ret.Href) != "" {
		brief.Landing.AuthorizedReturn = &motionHandoffReturn{Href: ret.Href}
	}

	assets, err := s.St.ListCampaignAssets(tenantID, camp.ID)
	if err != nil {
		return motionHandoffBrief{}, err
	}
	for _, a := range assets {
		brief.Assets = append(brief.Assets, motionHandoffAsset{AssetID: a.AssetID, Version: a.Version})
	}

	brief.Brand = s.motionBrandBlock(ctx, tenant, pubBrandID, pubHost)

	digest, err := motionHandoffDigest(brief)
	if err != nil {
		return motionHandoffBrief{}, err
	}
	brief.Digest = digest
	return brief, nil
}

// motionBrandBlock reads the brand registry through the existing brandctx
// path. The brief is an input projection, not a gate: when the upstream
// cannot be read it says so and stays false. It never fakes readiness.
func (s *Server) motionBrandBlock(ctx context.Context, tenant store.Tenant, pubBrandID, pubHost string) motionHandoffBrand {
	brand := motionHandoffBrand{
		BrandID:          tenant.BrandID,
		PublishedBrandID: pubBrandID,
		PublishedHost:    pubHost,
	}
	switch {
	case s.Brand == nil || !s.Cfg.FeatureBrand:
		brand.ContextUnavailableReason = "brand_client_not_configured"
	case strings.TrimSpace(tenant.BrandID) == "":
		brand.ContextUnavailableReason = "brand_not_bound"
	case pubHost == "":
		brand.ContextUnavailableReason = "no_published_host"
	default:
		res, err := s.Brand.Read(ctx, pubHost, true)
		if err != nil || res.Kind != brandctx.KindReady {
			brand.ContextUnavailableReason = brandUnavailableReason(res.Kind)
		} else {
			brand.ContextAvailable = true
			brand.BrandConfigVersion = res.Manifest.ConfigVersion
		}
	}
	return brand
}

func brandUnavailableReason(k brandctx.Kind) string {
	switch k {
	case brandctx.KindUnknown:
		return "unknown_brand"
	case brandctx.KindSuspended:
		return "brand_suspended"
	case brandctx.KindRetired:
		return "brand_retiring"
	case brandctx.KindDomain:
		return "domain_error"
	default:
		return "brand_unavailable"
	}
}

func splitTokenList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// motionHandoffDigest hashes the canonical JSON of the brief with the two
// serving-time fields (digest, generated_at) excluded. generated_at must be
// excluded: it is when the brief was served, not content, and the digest has
// to stay stable for identical data.
func motionHandoffDigest(b motionHandoffBrief) (string, error) {
	b.Digest = ""
	b.GeneratedAt = ""
	raw, err := json.Marshal(b)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
