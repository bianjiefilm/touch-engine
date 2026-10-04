// Package storemotion records a store activity's parameter request and the
// unverified Motion reference declaration that request produces (HUI-2748).
//
// The only fields are the store name, activity time, price, address, offer
// copy, and CTA. Price is a string. This package does not settle, render,
// call a model, or copy a Motion editor. HUI-2732 is not done, so a
// declaration never claims that an unchanged store skipped a rerun.
package storemotion

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	OriginApp = "touch"

	// StatusNotRendered is the only status this slice can say.
	StatusNotRendered = "还没生成成片"

	// RerunNotClaimed means this slice does not know whether an unchanged
	// store would be re-run. HUI-2732 is not available, so "skipped" is a lie.
	RerunNotClaimed = "not_claimed"

	// RerunNote is the sentence stored with every declaration.
	RerunNote = "HUI-2732 未完成，不能声称未改门店没有重跑。"

	maxStoreName    = 80
	maxActivityTime = 120
	maxPrice        = 64
	maxAddress      = 200
	maxOfferCopy    = 500
	maxCTA          = 80
	maxID           = 128
	maxContextRef   = 256
)

// ErrInvalid means a required field is missing, too long, or not a plain string.
var ErrInvalid = errors.New("storemotion: invalid params")

// ErrPrice means price was not a string. Numbers are settlement-shaped and refused.
var ErrPrice = errors.New("storemotion: price must be a string")

// ErrPrivacy means the value carries a QR payload or customer private data.
// Those values are not stored and are not placed on a model request.
var ErrPrivacy = errors.New("storemotion: privacy refused")

// Params is the merchant parameter set. Every field is text. There is no
// amount in minor units and no currency code.
type Params struct {
	StoreName    string `json:"store_name"`
	ActivityTime string `json:"activity_time"`
	Price        string `json:"price"`
	Address      string `json:"address"`
	OfferCopy    string `json:"offer_copy"`
	CTA          string `json:"cta"`
}

// Request points at one store activity and the parameters to record for it.
type Request struct {
	TenantID   string
	StoreID    string
	ActivityID string
	Params     Params
}

// Declaration is an unverified Motion reference claim. It is not a sealed
// MotionProjectRef: there is no project id, digest, or permission bit, and
// revision_id is absent because no revision has been issued.
type Declaration struct {
	OriginApp           string  `json:"origin_app"`
	OriginContextRef    string  `json:"origin_context_ref"`
	RevisionID          *string `json:"revision_id"`
	Verified            bool    `json:"verified"`
	Status              string  `json:"status"`
	ModelCalls          int     `json:"model_calls"`
	RenderCalls         int     `json:"render_calls"`
	UnchangedStoreRerun string  `json:"unchanged_store_rerun"`
	UnchangedStoreNote  string  `json:"unchanged_store_note"`
}

// Probe counts model and render calls. Apply does not call it. Tests hold one
// to prove a parameter change leaves both counters at zero. A nil probe is the
// production path: this process has no model client for this slice.
type Probe struct {
	ModelCalls  int
	RenderCalls int
	Payloads    [][]byte
}

// CallModel records one model invocation. Apply does not use this method.
func (p *Probe) CallModel(payload []byte) {
	if p == nil {
		return
	}
	p.ModelCalls++
	p.Payloads = append(p.Payloads, append([]byte(nil), payload...))
}

// CallRender records one render invocation. Apply does not use this method.
func (p *Probe) CallRender(payload []byte) {
	if p == nil {
		return
	}
	p.RenderCalls++
	p.Payloads = append(p.Payloads, append([]byte(nil), payload...))
}

var (
	idRe    = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
	phoneRe = regexp.MustCompile(`(?:^|[^\d])1[3-9]\d{9}(?:[^\d]|$)`)
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	idCard  = regexp.MustCompile(`(?:^|[^\d])\d{17}[\dXx](?:[^\d]|$)`)
)

// Apply checks the request and returns the unverified declaration.
// It does not call probe, even when the check fails.
func Apply(req Request, probe *Probe) (Declaration, error) {
	if err := validate(req); err != nil {
		return Declaration{}, err
	}
	_ = probe // 不调用 CallModel / CallRender。改参数也走这里。
	return declarationFor(req.TenantID, req.StoreID, req.ActivityID), nil
}

func declarationFor(tenantID, storeID, activityID string) Declaration {
	return Declaration{
		OriginApp:           OriginApp,
		OriginContextRef:    contextRef(tenantID, storeID, activityID),
		RevisionID:          nil,
		Verified:            false,
		Status:              StatusNotRendered,
		ModelCalls:          0,
		RenderCalls:         0,
		UnchangedStoreRerun: RerunNotClaimed,
		UnchangedStoreNote:  RerunNote,
	}
}

func contextRef(tenantID, storeID, activityID string) string {
	return "touch:tenant/" + tenantID + "/store/" + storeID + "/activity/" + activityID
}

func validate(req Request) error {
	if !validID(req.TenantID) || !validID(req.StoreID) || !validID(req.ActivityID) {
		return ErrInvalid
	}
	ref := contextRef(req.TenantID, req.StoreID, req.ActivityID)
	if len(ref) > maxContextRef {
		return ErrInvalid
	}
	p := &req.Params
	p.StoreName = strings.TrimSpace(p.StoreName)
	p.ActivityTime = strings.TrimSpace(p.ActivityTime)
	p.Price = strings.TrimSpace(p.Price)
	p.Address = strings.TrimSpace(p.Address)
	p.OfferCopy = strings.TrimSpace(p.OfferCopy)
	p.CTA = strings.TrimSpace(p.CTA)
	if err := plain(p.StoreName, maxStoreName); err != nil {
		return err
	}
	if err := plain(p.ActivityTime, maxActivityTime); err != nil {
		return err
	}
	if err := plain(p.Price, maxPrice); err != nil {
		return err
	}
	if err := plain(p.Address, maxAddress); err != nil {
		return err
	}
	if err := plain(p.OfferCopy, maxOfferCopy); err != nil {
		return err
	}
	if err := plain(p.CTA, maxCTA); err != nil {
		return err
	}
	for _, v := range []string{p.StoreName, p.ActivityTime, p.Price, p.Address, p.OfferCopy, p.CTA} {
		if private(v) {
			return ErrPrivacy
		}
	}
	req.Params = *p
	return nil
}

// Normalize returns the trimmed params or the same error as Apply.
func Normalize(req Request) (Params, error) {
	if err := validate(req); err != nil {
		return Params{}, err
	}
	return Params{
		StoreName:    strings.TrimSpace(req.Params.StoreName),
		ActivityTime: strings.TrimSpace(req.Params.ActivityTime),
		Price:        strings.TrimSpace(req.Params.Price),
		Address:      strings.TrimSpace(req.Params.Address),
		OfferCopy:    strings.TrimSpace(req.Params.OfferCopy),
		CTA:          strings.TrimSpace(req.Params.CTA),
	}, nil
}

func validID(s string) bool {
	return s != "" && len(s) <= maxID && idRe.MatchString(s)
}

func plain(s string, max int) error {
	if s == "" || len(s) > max || !utf8.ValidString(s) {
		return ErrInvalid
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return ErrInvalid
		}
	}
	return nil
}

func private(s string) bool {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "data:image") ||
		strings.Contains(lower, "weixin://") ||
		strings.Contains(lower, "wxp://") ||
		strings.Contains(lower, "begin:vcard") ||
		strings.Contains(lower, "qrcode=") ||
		strings.Contains(lower, "qr_code") {
		return true
	}
	return phoneRe.MatchString(s) || emailRe.MatchString(s) || idCard.MatchString(s)
}
