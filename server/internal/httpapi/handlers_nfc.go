package httpapi

// HUI-1665 FEAT-0166 NFC 标签管理面(全部 owner-only,authz.ActionManageTags)。
//
// 纪律:
//   - 标签是既有 campaign_link(短码)的管理面包装,零第二套链接模型;
//   - 停用/恢复标签 = 同事务改标签状态并复用既有 link 启停路径,
//     公共短码路由走既有五态停用态;
//   - CSV 导出 = 纯文本数据(label,short_code,url,store,group,uid_hint),
//     URL 复用 qrentry.PublicEntryURL(1664 canonical 构造),字段经 tagcsv
//     注入防护;PUBLIC_BASE_URL 缺失 → 503 fail-closed,不猜测;
//   - uid_hint 是管理面回填提示(非凭证):只出现在本面,公共面零泄漏;
//   - 跨租户资源一律 404(不可见掩码)。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/qrentry"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/tagcsv"
)

func (s *Server) requireTagOwner(c *caller, w http.ResponseWriter) bool {
	return s.requireAction(c, authz.ActionManageTags, authzScope(c), w)
}

// requireTagFace authorizes the NFC TAG face (groups stay org_owner-only via
// requireTagOwner). HUI-1674: org_owner reaches every tag; store_manager works
// only on own-store tags (enforced per-record, never by client input). Everyone
// else is refused exactly as before (staff → 403 forbidden).
// Returns the member's store scope ("" = org_owner, no narrowing).
func (s *Server) requireTagFace(c *caller, w http.ResponseWriter) (string, bool) {
	m := authzMember(c)
	if m == nil {
		fail(w, http.StatusForbidden, authz.ReasonNotMember, "no membership resolved")
		return "", false
	}
	if !m.Enabled {
		fail(w, http.StatusForbidden, authz.ReasonDisabled, "member disabled")
		return "", false
	}
	switch m.Role {
	case authz.RoleOrgOwner:
		return "", true
	case authz.RoleStoreManager:
		if m.StoreScope == "" {
			// malformed row: fail closed
			fail(w, http.StatusForbidden, authz.ReasonOutOfScope, "store manager without store scope")
			return "", false
		}
		return m.StoreScope, true
	default:
		fail(w, http.StatusForbidden, authz.ReasonForbidden, "action not allowed for this member")
		return "", false
	}
}

// tagScoped fetches the tag view in the caller's tenant and enforces the
// member's store scope on it. Missing and out-of-scope are both 404 (掩码).
func (s *Server) tagScoped(c *caller, id string, w http.ResponseWriter) (store.NfcTagView, bool) {
	v, err := s.St.GetTagView(id, c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "tag not found")
		return store.NfcTagView{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag lookup failed")
		return store.NfcTagView{}, false
	}
	if !s.requireScopedAction(c, authz.ActionManageTags, authz.RecordScope{TenantID: c.Member.TenantID, StoreID: v.StoreID}, true, w) {
		return store.NfcTagView{}, false
	}
	return v, true
}

// ---- tag groups ---------------------------------------------------------------

func (s *Server) handleTagGroupCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireTagOwner(c, w) {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || strings.TrimSpace(in.Name) == "" {
		fail(w, http.StatusBadRequest, "bad_request", "group name is required")
		return
	}
	g, err := s.St.CreateTagGroup(c.Member.TenantID, in.Name, c.Member.PrincipalRef)
	if errors.Is(err, store.ErrTagGroupNameTaken) {
		fail(w, http.StatusConflict, "group_name_taken", "a group with this name already exists")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "group create failed")
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *Server) handleTagGroupList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireTagOwner(c, w) {
		return
	}
	items, err := s.St.ListTagGroups(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "group list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleTagGroupDelete(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireTagOwner(c, w) {
		return
	}
	// 组内标签 detach(未分组),分组不是记录容器;跨租户/不存在同为 404。
	err := s.St.DeleteTagGroup(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "group not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "group delete failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// ---- tags ---------------------------------------------------------------------

// parseTagFilter reads the shared list/export filters. Unknown values are
// refused (bad_filter) instead of silently ignored.
func parseTagFilter(r *http.Request) (store.TagFilter, bool) {
	f := store.TagFilter{
		CampaignID: strings.TrimSpace(r.URL.Query().Get("campaign_id")),
		GroupID:    strings.TrimSpace(r.URL.Query().Get("group_id")),
		StoreID:    strings.TrimSpace(r.URL.Query().Get("store_id")),
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
	}
	switch f.Status {
	case "", store.TagStatusActive, store.TagStatusDisabled:
	default:
		return f, false
	}
	return f, true
}

func (s *Server) handleTagBatchCreate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	scope, ok := s.requireTagFace(c, w)
	if !ok {
		return
	}
	var in struct {
		CampaignID  string   `json:"campaign_id"`
		LinkIDs     []string `json:"link_ids"`
		BindMode    string   `json:"bind_mode"`
		Count       int      `json:"count"`
		StoreID     string   `json:"store_id"`
		GroupID     string   `json:"group_id"`
		LabelPrefix string   `json:"label_prefix"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if in.Count < 1 || in.Count > store.MaxBatchTags {
		fail(w, http.StatusBadRequest, "bad_count", "count must be between 1 and 500")
		return
	}
	if in.BindMode != "shared" && in.BindMode != "rotate" {
		fail(w, http.StatusBadRequest, "bad_bind_mode", "bind_mode must be shared or rotate")
		return
	}
	if len(in.LinkIDs) == 0 {
		fail(w, http.StatusBadRequest, "bad_links", "at least one link is required")
		return
	}
	if in.BindMode == "shared" && len(in.LinkIDs) != 1 {
		fail(w, http.StatusBadRequest, "bad_links", "shared mode requires exactly one link")
		return
	}
	// HUI-1674: the target campaign must be inside the caller's store scope
	// (跨店活动 404 掩码;org_owner 不受限)。
	if _, ok := s.campaignScoped(c, in.CampaignID, authz.ActionManageTags, w); !ok {
		return
	}
	// HUI-1674: 门店经理的绑定事实由服务端决定——缺省=本店;显式他店=403。
	if scope != "" {
		if in.StoreID == "" {
			in.StoreID = scope
		} else if in.StoreID != scope {
			fail(w, http.StatusForbidden, authz.ReasonOutOfScope, "store manager may only bind tags to its own store")
			return
		}
		// 分组是总部级组织手段(无门店作用域),经理不参与
		if in.GroupID != "" {
			fail(w, http.StatusForbidden, authz.ReasonOutOfScope, "store manager may not assign tag groups")
			return
		}
	}
	if in.StoreID != "" {
		if _, err := s.St.GetStore(in.StoreID, c.Member.TenantID); err != nil {
			fail(w, http.StatusBadRequest, "bad_store", "store not found in this tenant")
			return
		}
	}
	if in.GroupID != "" {
		if _, err := s.St.GetTagGroup(in.GroupID, c.Member.TenantID); err != nil {
			fail(w, http.StatusBadRequest, "bad_group", "group not found in this tenant")
			return
		}
	}
	created, err := s.St.CreateTagsBatch(store.NewTagBatch{
		TenantID: c.Member.TenantID, CampaignID: in.CampaignID, LinkIDs: in.LinkIDs,
		BindMode: in.BindMode, Count: in.Count, GroupID: in.GroupID, StoreID: in.StoreID,
		LabelPrefix: in.LabelPrefix, CreatedBy: c.Member.PrincipalRef,
	})
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "campaign or link not found in this tenant")
		return
	default:
		fail(w, http.StatusBadRequest, "bad_batch", "batch refused: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"items": created, "count": len(created)})
}

func (s *Server) handleTagList(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	scope, ok := s.requireTagFace(c, w)
	if !ok {
		return
	}
	f, ok := parseTagFilter(r)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_filter", "status must be active or disabled")
		return
	}
	// HUI-1674: 服务端强制作用域过滤——客户端传来的 store_id 永不放宽经理视野
	if scope != "" {
		f.StoreID = scope
	}
	items, err := s.St.ListTags(c.Member.TenantID, f)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (s *Server) handleTagGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	v, ok := s.tagScoped(c, r.PathValue("id"), w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleTagPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	scope, ok := s.requireTagFace(c, w)
	if !ok {
		return
	}
	cur, ok := s.tagScoped(c, r.PathValue("id"), w)
	if !ok {
		return
	}
	var in struct {
		Label      *string `json:"label"`
		LinkID     *string `json:"link_id"`
		GroupID    *string `json:"group_id"`
		StoreID    *string `json:"store_id"`
		UIDHint    *string `json:"uid_hint"`
		CampaignID *string `json:"campaign_id"` // accepted only to be refused: binding is by link
		Status     *string `json:"status"`      // accepted only to be refused: use /status
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid json body")
		return
	}
	if in.Status != nil {
		fail(w, http.StatusBadRequest, "use_status_endpoint", "tag status changes must use POST /nfc/tags/{id}/status")
		return
	}
	if in.CampaignID != nil {
		fail(w, http.StatusBadRequest, "rebind_by_link", "tags rebind via link_id; campaign follows the link")
		return
	}
	if in.Label != nil && strings.TrimSpace(*in.Label) == "" {
		fail(w, http.StatusBadRequest, "bad_label", "label must not be empty")
		return
	}
	if in.LinkID != nil && strings.TrimSpace(*in.LinkID) == "" {
		fail(w, http.StatusBadRequest, "bad_link", "link_id must not be empty (omit to keep the binding)")
		return
	}
	// HUI-1674: 门店经理的改动边界(全部服务端强制)
	if scope != "" {
		if in.GroupID != nil {
			fail(w, http.StatusForbidden, authz.ReasonOutOfScope, "store manager may not change tag groups")
			return
		}
		if in.StoreID != nil && *in.StoreID != scope {
			fail(w, http.StatusForbidden, authz.ReasonOutOfScope, "store manager may only keep its own store binding")
			return
		}
		if in.LinkID != nil && *in.LinkID != cur.LinkID {
			// 换绑目标的活动必须仍在本店(跨店 404 掩码)
			target, err := s.St.GetLink(*in.LinkID, c.Member.TenantID)
			if errors.Is(err, store.ErrNotFound) {
				fail(w, http.StatusNotFound, "not_found", "tag or referenced record not found in this tenant")
				return
			}
			if err != nil {
				fail(w, http.StatusInternalServerError, "internal", "link lookup failed")
				return
			}
			if _, ok := s.campaignScoped(c, target.CampaignID, authz.ActionManageTags, w); !ok {
				return
			}
		}
	}
	v, err := s.St.PatchTag(r.PathValue("id"), c.Member.TenantID, store.TagPatch{
		Label: in.Label, LinkID: in.LinkID, GroupID: in.GroupID, StoreID: in.StoreID, UIDHint: in.UIDHint,
	})
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "tag or referenced record not found in this tenant")
		return
	default:
		fail(w, http.StatusBadRequest, "bad_patch", "patch refused: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleTagStatus(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	if _, ok := s.tagScoped(c, r.PathValue("id"), w); !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Status == "" {
		fail(w, http.StatusBadRequest, "bad_request", "status (active|disabled) is required")
		return
	}
	v, err := s.St.SetTagStatus(r.PathValue("id"), c.Member.TenantID, in.Status)
	switch {
	case err == nil:
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "tag not found")
		return
	default:
		fail(w, http.StatusBadRequest, "bad_status", "status must be active or disabled")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleTagDelete(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if _, ok := s.requireTagFace(c, w); !ok {
		return
	}
	if _, ok := s.tagScoped(c, r.PathValue("id"), w); !ok {
		return
	}
	// 只删管理面记录;短码 link 与既有生命周期不受影响。
	err := s.St.DeleteTag(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "tag not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag delete failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// ---- CSV export ----------------------------------------------------------------

func (s *Server) handleTagExportCSV(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	scope, ok := s.requireTagFace(c, w)
	if !ok {
		return
	}
	// fail-closed:没有对外基地址就无法构造 canonical URL 列,拒绝猜测
	if strings.TrimSpace(s.Cfg.PublicBaseURL) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "public_base_url_missing",
			"message": "PUBLIC_BASE_URL is not configured; the url column cannot be built (fail-closed)",
		})
		return
	}
	f, ok := parseTagFilter(r)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_filter", "status must be active or disabled")
		return
	}
	// HUI-1674: 服务端强制作用域过滤
	if scope != "" {
		f.StoreID = scope
	}
	items, err := s.St.ListTags(c.Member.TenantID, f)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag export failed")
		return
	}
	rows := make([]tagcsv.Row, 0, len(items))
	for _, v := range items {
		url, err := qrentry.PublicEntryURL(s.Cfg.PublicBaseURL, v.Code)
		if err != nil {
			// code 是本系统生成的短码,构造失败属内部错误
			fail(w, http.StatusInternalServerError, "internal", "url build failed")
			return
		}
		rows = append(rows, tagcsv.Row{
			Label: v.Label, ShortCode: v.Code, URL: url,
			Store: v.StoreName, Group: v.GroupName, UIDHint: v.UIDHint,
		})
	}
	out, err := tagcsv.Build(rows)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "csv build failed")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="nfc-tags-`+strconv.FormatInt(time.Now().UTC().Unix(), 10)+`.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(out))
}
