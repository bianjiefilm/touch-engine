package httpapi

import (
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// ---- public guest surface -----------------------------------------------------
//
// 游客(无商家账户)只读明确公开的活动字段。纪律:
//   - 无会话概念:本面永不读 cookie/session,永不写库;
//   - 白名单字段:标题/有效期/公开内容;租户 id、principal、内部 asset id、
//     订单引用、门店内部字段一律不出现;
//   - 非有效态统一 HTTP 404(状态码不区分,防止探测短码存在性),仅文案区分;
//   - 其他方法 405;本面到生成/付费调用之间不存在任何代码路径。

// publicLinkView is the whitelist payload for the public activity page.
type publicLinkView struct {
	State         string `json:"state"` // machine-readable: available|link_disabled|not_started|expired|paused|draft|ended|not_found
	Title         string `json:"title,omitempty"`
	PublicContent string `json:"public_content,omitempty"`
	StartsAt      string `json:"starts_at,omitempty"`
	EndsAt        string `json:"ends_at,omitempty"`
	// StoreNotice (HUI-1674): 白名单常量 "store_unavailable" — 活动绑定的门店
	// 已停用(公共页标注「门店暂不可用」)。不含任何门店内部字段;仅 available
	// 态出现,非可用态保持单字段白名单形状。
	StoreNotice string `json:"store_notice,omitempty"`
	// MerchantName is the tenant display name (the store the guest is visiting).
	// Brand shell is separate and must not replace it.
	MerchantName string            `json:"merchant_name,omitempty"`
	StoreName    string            `json:"store_name,omitempty"`
	BrandShell   *publicBrandShell `json:"brand_shell,omitempty"`
}

type publicBrandShell struct {
	DisplayName    string `json:"display_name,omitempty"`
	SupportName    string `json:"support_name,omitempty"`
	SupportContact string `json:"support_contact,omitempty"`
}

// storeNoticeUnavailable is the only value store_notice may carry.
const storeNoticeUnavailable = "store_unavailable"

// attachPublicNames adds the store the guest is visiting. Names only:
// tenant id, store id, address, and agency fields stay off this payload.
func (s *Server) attachPublicNames(view *publicLinkView, res store.ResolvedLink) {
	tenantID := res.Link.TenantID
	if tenantID == "" {
		tenantID = res.Campaign.TenantID
	}
	if tenantID != "" {
		if t, err := s.St.GetTenant(tenantID); err == nil {
			view.MerchantName = t.Name
		}
	}
	if res.Campaign.StoreID != "" && tenantID != "" {
		if st, err := s.St.GetStore(res.Campaign.StoreID, tenantID); err == nil {
			view.StoreName = st.Name
		}
	}
}

func (s *Server) handlePublicLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, http.StatusMethodNotAllowed, "method_not_allowed", "public activity pages are read-only")
		return
	}
	code := strings.TrimSpace(r.PathValue("code"))
	res := s.St.ResolveLink(code, time.Now())

	if s.Cfg.FeatureBrand {
		if done := s.writeBrandedPublic(w, r, code, res); done {
			return
		}
	}
	view := publicLinkView{State: string(res.Outcome)}
	if res.Outcome == store.OutcomeAvailable {
		view.Title = res.Campaign.Title
		view.PublicContent = res.Campaign.PublicContent
		view.StartsAt = res.Campaign.StartsAt
		view.EndsAt = res.Campaign.EndsAt
		s.attachPublicNames(&view, res)
		if res.StoreUnavailable {
			view.StoreNotice = storeNoticeUnavailable
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	// every non-available state: uniform 404 + state-specific text
	writeJSON(w, http.StatusNotFound, view)
}
