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
	if !s.requireTagOwner(c, w) {
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
	if !s.requireTagOwner(c, w) {
		return
	}
	f, ok := parseTagFilter(r)
	if !ok {
		fail(w, http.StatusBadRequest, "bad_filter", "status must be active or disabled")
		return
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
	if !s.requireTagOwner(c, w) {
		return
	}
	v, err := s.St.GetTagView(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "tag not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "tag lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleTagPatch(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if !s.requireTagOwner(c, w) {
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
	if !s.requireTagOwner(c, w) {
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
	if !s.requireTagOwner(c, w) {
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
	if !s.requireTagOwner(c, w) {
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
