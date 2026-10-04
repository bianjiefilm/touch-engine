package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/storemotion"
)

// storeMotionBody is the only JSON object this slice accepts. Unknown keys,
// including QR and customer fields, are rejected before anything is stored.
type storeMotionBody struct {
	StoreName    string          `json:"store_name"`
	ActivityTime string          `json:"activity_time"`
	Price        json.RawMessage `json:"price"`
	Address      string          `json:"address"`
	OfferCopy    string          `json:"offer_copy"`
	CTA          string          `json:"cta"`
	Channels     string          `json:"channels"`
	AspectRatios string          `json:"aspect_ratios"`
}

type storeMotionView struct {
	ID          string                  `json:"id"`
	Version     int                     `json:"version"`
	StoreID     string                  `json:"store_id"`
	ActivityID  string                  `json:"activity_id"`
	Params      storemotion.Params      `json:"params"`
	Declaration storemotion.Declaration `json:"declaration"`
}

func (s *Server) handleStoreMotionPut(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	if camp.StoreID == "" {
		fail(w, http.StatusBadRequest, "store_required", "活动还没有门店，不能记下门店参数。")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "store motion body must be JSON")
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var body storeMotionBody
	if err := dec.Decode(&body); err != nil {
		if strings.Contains(err.Error(), "unknown field") {
			fail(w, http.StatusBadRequest, "forbidden_field", "只接受门店名、活动时间、价格、地址、优惠文案和 CTA。二维码和客户资料不能写入。")
			return
		}
		fail(w, http.StatusBadRequest, "bad_request", "store motion body must be JSON")
		return
	}
	if dec.More() {
		fail(w, http.StatusBadRequest, "bad_request", "store motion body must be one JSON object")
		return
	}
	price, err := motionPriceString(body.Price)
	if err != nil {
		fail(w, http.StatusBadRequest, "price_not_string", "价格必须是字符串，这里不做结算。")
		return
	}
	req := storemotion.Request{
		TenantID: c.Member.TenantID, StoreID: camp.StoreID, ActivityID: camp.ID,
		Params: storemotion.Params{
			StoreName: body.StoreName, ActivityTime: body.ActivityTime, Price: price,
			Address: body.Address, OfferCopy: body.OfferCopy, CTA: body.CTA,
			Channels: body.Channels, AspectRatios: body.AspectRatios,
		},
	}
	row, created, err := s.St.SaveStoreMotion(req, c.Member.PrincipalRef, s.StoreMotionProbe)
	if err != nil {
		writeStoreMotionErr(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, storeMotionViewFrom(row))
}

func (s *Server) handleStoreMotionGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadRecord, w)
	if !ok {
		return
	}
	row, err := s.St.LatestStoreMotion(c.Member.TenantID, camp.ID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "还没有记下门店参数。")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store motion lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, storeMotionViewFrom(row))
}

func storeMotionViewFrom(row store.StoreMotionRow) storeMotionView {
	return storeMotionView{
		ID: row.ID, Version: row.Version, StoreID: row.StoreID, ActivityID: row.CampaignID,
		Params: row.Params, Declaration: row.Declaration,
	}
}

func writeStoreMotionErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, storemotion.ErrPrice):
		fail(w, http.StatusBadRequest, "price_not_string", "价格必须是字符串，这里不做结算。")
	case errors.Is(err, storemotion.ErrPrivacy):
		fail(w, http.StatusBadRequest, "privacy_refused", "二维码和客户资料不能写入，也没有发给模型。")
	case errors.Is(err, storemotion.ErrFormat):
		fail(w, http.StatusBadRequest, "invalid_field", "投放渠道和画幅比例只能是逗号分隔的小写字母、数字、冒号、斜杠、横线和下划线。")
	case errors.Is(err, storemotion.ErrInvalid):
		fail(w, http.StatusBadRequest, "invalid_params", "门店名、活动时间、价格、地址、优惠文案和 CTA 都要是一段文字。")
	default:
		fail(w, http.StatusInternalServerError, "internal", "store motion save failed")
	}
}

func motionPriceString(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", storemotion.ErrPrice
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", storemotion.ErrPrice
	}
	return s, nil
}
