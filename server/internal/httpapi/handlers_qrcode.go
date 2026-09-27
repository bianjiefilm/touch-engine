package httpapi

// HUI-1664 FEAT-0165 二维码兜底入口:商家后台按短码生成/下载二维码。
//
// 纪律:
//   - 载荷 = qrentry.PublicEntryURL(PUBLIC_BASE_URL, code):纯短码 canonical
//     URL,不含凭证/租户/客户信息/任意跳转参数;
//   - 复用 T0 活动与短码模型(store.GetLink / 五态解析),零第二套模型;
//   - 导出面 owner-only(authz.ActionExportQR);参数绝不参与鉴权;
//   - PUBLIC_BASE_URL 缺失 → 503 qr_not_configured(fail-closed,不猜测);
//   - size 白名单 128/256/512,越界显式 400。

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/qrentry"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func (s *Server) handleLinkQRCode(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireAction(c, authz.ActionExportQR, authzScope(c), w) {
		return
	}

	// fail-closed:没有对外基地址就无法构造 canonical 载荷,拒绝猜测。
	// 品牌开关打开时,活跃品牌主机可以代替 PUBLIC_BASE_URL;两者都没有才拒绝。
	if !s.Cfg.FeatureBrand && strings.TrimSpace(s.Cfg.PublicBaseURL) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "qr_not_configured",
			"message": "PUBLIC_BASE_URL is not configured; the QR payload cannot be built (fail-closed)",
		})
		return
	}

	size, err := qrentry.ParseSize(r.URL.Query().Get("size"))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_size", "size must be one of 128/256/512")
		return
	}

	// campaign scope first: another tenant's records are invisible here
	if _, err := s.St.GetCampaign(r.PathValue("id"), c.Member.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	link, err := s.St.GetLink(r.PathValue("linkId"), c.Member.TenantID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, "not_found", "link not found")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "link lookup failed")
		return
	}
	if link.CampaignID != r.PathValue("id") {
		fail(w, http.StatusNotFound, "not_found", "link not found")
		return
	}

	base := s.Cfg.PublicBaseURL
	originKind := "configured"
	if s.Cfg.FeatureBrand {
		pub, _ := s.St.LinkPublication(link.Code)
		if pub.Host != "" && pub.BrandID != "" {
			base = "https://" + pub.Host
			originKind = "brand"
		} else if c.Brand != nil && c.Brand.Result.Kind == "ready" && c.Brand.Result.Manifest.AdmitPublic {
			base = "https://" + c.Brand.Host
			originKind = "brand"
		} else if strings.TrimSpace(s.Cfg.PublicBaseURL) != "" {
			base = s.Cfg.PublicBaseURL
			originKind = "fallback"
		}
	}
	if strings.TrimSpace(base) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "qr_not_configured",
			"message": "PUBLIC_BASE_URL is not configured; the QR payload cannot be built (fail-closed)",
		})
		return
	}
	payload, err := qrentry.PublicEntryURL(base, link.Code)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "qr payload build failed")
		return
	}

	// json 载荷:面板展示 + 载荷断言面。品牌开启时多一个 origin_kind,
	// 标明是品牌域名还是受控 fallback。载荷本身仍只有短码 URL。
	if r.URL.Query().Get("format") == "json" {
		body := map[string]any{"url": payload, "code": link.Code, "size": size}
		if s.Cfg.FeatureBrand {
			body["origin_kind"] = originKind
		}
		writeJSON(w, http.StatusOK, body)
		return
	}

	pngBytes, err := qrentry.PNG(payload, size)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "qr render failed")
		return
	}
	w.Header().Set("Content-Type", "image/png")
	// code is Crockford base32: header-safe by construction
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="qr-%s-%d.png"`, link.Code, size))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pngBytes)
}
