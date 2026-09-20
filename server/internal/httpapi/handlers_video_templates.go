// handlers_video_templates.go: HUI-1669 FEAT-0170 视频模板管理的 HTTP 面。
//
// 纪律:
//   - 开关:FEATURE_VIDEO_TEMPLATES(默认 off)= 路由不注册(见 server.go
//     条件注册)且本 gate 再答统一 404 —— 与 FEATURE_DASHBOARD /
//     FEATURE_CAMPAIGN_RULES / FEATURE_ASSET_LIB 的 off 双保险同一写法;
//   - 授权:管理动作(模板 CRUD/重命名/删除、版本增改、发布、分配/解绑)走
//     authz.ActionManageVideoTemplates(仅 org_owner);查询按既有业务角色
//     (ActionReadList/ActionReadRecord;模板与版本是租户级记录,门店经理
//     out_of_scope;本店分配查询按 store 作用域放行);
//   - 版本冻结:发布后版本行只读(PUT 已发布版本 409 version_readonly);
//     带已发布版本的模板不可删(409 template_published);结构变更 =
//     新草稿版本,既有分配钉 (template_id, version) 不自动升级;
//   - 发布校验(服务端单点):槽位绑定引用不存在/媒体类型不符即拒(409,
//     复用 FEAT-0167 素材库登记行);分配引用未发布版本即拒;
//   - 零生成/渲染:本面没有任何视频合成/渲染触发字段(合成执行属外部
//     工具/AiCut,deferred 如实声明);
//   - 零物理存储:模板只持有素材引用(lib_assets.id),无内容通道。
package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/videotpl"
)

// videoTemplatesGate answers uniform 404 when FEATURE_VIDEO_TEMPLATES is off:
// an invisible surface (assetLibGate/rulesGate 同一纪律).
func (s *Server) videoTemplatesGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureVideoTemplates {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// vtManage authorizes an org_owner-only management action on the tenant-level
// template surface.
func (s *Server) vtManage(c *caller, w http.ResponseWriter) bool {
	return s.requireAction(c, authz.ActionManageVideoTemplates,
		authz.RecordScope{TenantID: c.Member.TenantID}, w)
}

// vtRead authorizes a query on the tenant-level template surface (templates and
// versions carry no store binding of their own; 门店经理按既有纪律 out_of_scope).
func (s *Server) vtRead(c *caller, action authz.Action, w http.ResponseWriter) bool {
	return s.requireAction(c, action, authz.RecordScope{TenantID: c.Member.TenantID}, w)
}

// vtInvalid422 maps a videotpl domain ValidationError to 422 with the machine
// reason as the error code (invalid422 的 videotpl 同款).
func vtInvalid422(w http.ResponseWriter, err error) bool {
	var ve *videotpl.ValidationError
	if !errors.As(err, &ve) {
		return false
	}
	fail(w, http.StatusUnprocessableEntity, ve.Reason, "declaration rejected: "+ve.Reason+" ("+ve.Field+")")
	return true
}

// vtFail maps the video-template sentinel errors onto their HTTP responses
// (独立机器原因码); returns true when err was mapped.
func vtFail(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, store.ErrDuplicateVideoTemplate):
		fail(w, http.StatusConflict, "duplicate_video_template", "a template with this name already exists in this tenant")
	case errors.Is(err, store.ErrVideoTemplateVersionReadonly):
		fail(w, http.StatusConflict, "version_readonly", "this version is readonly; a structural change creates a new draft version")
	case errors.Is(err, store.ErrVideoTemplateDraftExists):
		fail(w, http.StatusConflict, "draft_exists", "a draft version already exists; edit it instead")
	case errors.Is(err, store.ErrVideoTemplatePublished):
		fail(w, http.StatusConflict, "template_published", "templates with published versions cannot be deleted")
	case errors.Is(err, store.ErrVideoTemplateNoDraft):
		fail(w, http.StatusConflict, "no_draft", "there is no draft version to publish")
	case errors.Is(err, store.ErrSlotAssetMissing):
		fail(w, http.StatusConflict, "slot_asset_missing", "a slot binding references an asset that does not exist in this tenant")
	case errors.Is(err, store.ErrSlotAssetTypeMismatch):
		fail(w, http.StatusConflict, "slot_asset_type_mismatch", "a slot binding media type does not match the slot constraint")
	case errors.Is(err, store.ErrVideoTemplateVersionNotPublished):
		fail(w, http.StatusConflict, "version_not_published", "assignments must reference a published version")
	default:
		return false
	}
	return true
}

// ---- bodies -----------------------------------------------------------------

type vtSlotBody struct {
	Name      string `json:"name"`
	MediaType string `json:"media_type"`
	Role      string `json:"role"`
	AssetID   string `json:"asset_id"`
}

type vtContentBody struct {
	Slots []vtSlotBody `json:"slots"`
}

func (b vtContentBody) content() videotpl.Content {
	c := videotpl.Content{Slots: make([]videotpl.Slot, 0, len(b.Slots))}
	for _, s := range b.Slots {
		c.Slots = append(c.Slots, videotpl.Slot{
			Name:      s.Name,
			MediaType: assetlib.MediaType(s.MediaType),
			Role:      videotpl.Role(s.Role),
			AssetID:   s.AssetID,
		})
	}
	return c
}

type vtCreateBody struct {
	Name  string       `json:"name"`
	Slots []vtSlotBody `json:"slots"`
}

type vtAssignBody struct {
	StoreID string `json:"store_id"`
	Version int    `json:"version"` // 0 = 钉最新已发布版
}

// vtAssignmentResponse flattens the hydrated assignment plus the replay flag.
type vtAssignmentResponse struct {
	store.VideoTemplateAssignment
	Replayed bool `json:"replayed"`
}

// vtVersionPath parses the {version} path segment; a malformed number is just
// another missing resource (404).
func vtVersionPath(w http.ResponseWriter, raw string) (int, bool) {
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return 0, false
	}
	return v, true
}

// ---- templates(CRUD) -----------------------------------------------------------

// POST /api/v1/video-templates — 创建模板(即带草稿 v1)。
func (s *Server) handleVideoTemplateCreate(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.vtManage(c, w) {
		return
	}
	var in vtCreateBody
	ok, err := decodeLibBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	tpl, ver, err := s.St.CreateVideoTemplate(c.Member.TenantID, in.Name,
		vtContentBody{Slots: in.Slots}.content(), c.Member.PrincipalRef)
	switch {
	case vtInvalid422(w, err):
		return
	case errors.Is(err, store.ErrDuplicateVideoTemplate):
		fail(w, http.StatusConflict, "duplicate_video_template", "a template with this name already exists in this tenant")
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "template create failed")
	default:
		writeJSON(w, http.StatusCreated, vtDetail{VideoTemplateSummary: vtSummaryFrom(tpl, ver), Versions: []store.VideoTemplateVersion{ver}})
	}
}

// vtDetail flattens the summary (which itself flattens the template row) plus
// the version rows.
type vtDetail struct {
	store.VideoTemplateSummary
	Versions []store.VideoTemplateVersion `json:"versions"`
}

// vtSummaryFrom builds the create-time summary (fresh template: v1 draft,
// never published).
func vtSummaryFrom(tpl store.VideoTemplate, ver store.VideoTemplateVersion) store.VideoTemplateSummary {
	return store.VideoTemplateSummary{
		VideoTemplate:    tpl,
		LatestVersion:    ver.Version,
		LatestStatus:     ver.Status,
		PublishedVersion: 0,
	}
}

// GET /api/v1/video-templates — 租户内模板列表(含版本状态汇总)。
func (s *Server) handleVideoTemplateList(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.vtRead(c, authz.ActionReadList, w) {
		return
	}
	items, err := s.St.ListVideoTemplates(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "template list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// vtTemplateScoped fetches the template summary within the caller's tenant
// (cross-tenant = 404 invisible) and authorizes the action.
func (s *Server) vtTemplateScoped(c *caller, id string, action authz.Action, w http.ResponseWriter) (store.VideoTemplateSummary, bool) {
	sum, err := s.St.GetVideoTemplateSummary(c.Member.TenantID, id)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return store.VideoTemplateSummary{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "template lookup failed")
		return store.VideoTemplateSummary{}, false
	}
	if !s.vtRead(c, action, w) {
		return store.VideoTemplateSummary{}, false
	}
	return sum, true
}

// GET /api/v1/video-templates/{id} — 模板详情(含全部版本行)。
func (s *Server) handleVideoTemplateGet(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	sum, ok := s.vtTemplateScoped(c, id, authz.ActionReadRecord, w)
	if !ok {
		return
	}
	vers, err := s.St.ListVideoTemplateVersions(c.Member.TenantID, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "versions lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, vtDetail{VideoTemplateSummary: sum, Versions: vers})
}

// PATCH /api/v1/video-templates/{id} — 重命名(组织身份,不入版本)。
func (s *Server) handleVideoTemplatePatch(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionManageVideoTemplates, w); !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	ok, err := decodeLibBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	tpl, err := s.St.RenameVideoTemplate(id, c.Member.TenantID, in.Name)
	switch {
	case vtInvalid422(w, err):
	case errors.Is(err, store.ErrDuplicateVideoTemplate):
		fail(w, http.StatusConflict, "duplicate_video_template", "a template with this name already exists in this tenant")
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "resource not found")
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "template rename failed")
	default:
		writeJSON(w, http.StatusOK, tpl)
	}
}

// DELETE /api/v1/video-templates/{id} — 仅纯草稿(从未发布)可删;带已发布
// 版本的模板 409 template_published(分配钉住的版本必须保持可回查)。
func (s *Server) handleVideoTemplateDelete(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionManageVideoTemplates, w); !ok {
		return
	}
	err := s.St.DeleteVideoTemplate(id, c.Member.TenantID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, store.ErrVideoTemplatePublished):
		fail(w, http.StatusConflict, "template_published", "templates with published versions cannot be deleted")
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "template delete failed")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"removed": true})
	}
}

// ---- versions(版本行:draft → published,发布后只读) -----------------------------

// POST /api/v1/video-templates/{id}/versions — 结构变更 = 新草稿版本。
func (s *Server) handleVideoTemplateVersionCreate(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionManageVideoTemplates, w); !ok {
		return
	}
	var in vtContentBody
	ok, err := decodeLibBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	ver, err := s.St.CreateVideoTemplateVersion(c.Member.TenantID, id, in.content(), c.Member.PrincipalRef)
	switch {
	case vtInvalid422(w, err):
	case vtFail(w, err):
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "version create failed")
	default:
		writeJSON(w, http.StatusCreated, ver)
	}
}

// GET /api/v1/video-templates/{id}/versions — 版本行只增不删(oldest-first)。
func (s *Server) handleVideoTemplateVersionList(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionReadList, w); !ok {
		return
	}
	items, err := s.St.ListVideoTemplateVersions(c.Member.TenantID, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "versions lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/video-templates/{id}/versions/{version}
func (s *Server) handleVideoTemplateVersionGet(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionReadRecord, w); !ok {
		return
	}
	version, ok := vtVersionPath(w, r.PathValue("version"))
	if !ok {
		return
	}
	ver, err := s.St.GetVideoTemplateVersion(c.Member.TenantID, id, version)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "version lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, ver)
}

// PUT /api/v1/video-templates/{id}/versions/{version} — 只允许改当前草稿;
// 已发布版本 409 version_readonly(发布后该版本行只读)。
func (s *Server) handleVideoTemplateVersionPut(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionManageVideoTemplates, w); !ok {
		return
	}
	version, ok := vtVersionPath(w, r.PathValue("version"))
	if !ok {
		return
	}
	var in vtContentBody
	ok, err := decodeLibBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	ver, err := s.St.UpdateVideoTemplateVersionContent(c.Member.TenantID, id, version, in.content(), c.Member.PrincipalRef)
	switch {
	case vtInvalid422(w, err):
	case vtFail(w, err):
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "version update failed")
	default:
		writeJSON(w, http.StatusOK, ver)
	}
}

// POST /api/v1/video-templates/{id}/publish — 发布当前草稿(绑定校验通过后
// 该版本行冻结为只读)。
func (s *Server) handleVideoTemplatePublish(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionManageVideoTemplates, w); !ok {
		return
	}
	ver, err := s.St.PublishVideoTemplate(c.Member.TenantID, id, c.Member.PrincipalRef)
	switch {
	case vtFail(w, err):
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "publish failed")
	default:
		writeJSON(w, http.StatusOK, ver)
	}
}

// ---- assignments(模板⇄门店分配) -------------------------------------------------

// POST /api/v1/video-templates/{id}/assignments — 分配(幂等;version=0 =
// 钉最新已发布版;换版重分配留 updated 痕)。
func (s *Server) handleVideoTemplateAssign(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionManageVideoTemplates, w); !ok {
		return
	}
	var in vtAssignBody
	ok, err := decodeLibBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	if in.Version < 0 {
		fail(w, http.StatusBadRequest, "bad_request", "version must be a positive number (0 = latest published)")
		return
	}
	a, replayed, updated, err := s.St.AssignVideoTemplate(c.Member.TenantID, in.StoreID, id, in.Version, c.Member.PrincipalRef)
	switch {
	case vtFail(w, err):
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "assignment failed")
	case replayed:
		// 同键同版:幂等重放,零改动(200)。
		writeJSON(w, http.StatusOK, vtAssignmentResponse{VideoTemplateAssignment: a, Replayed: true})
	case updated:
		// 换版重分配:钉版更新 + updated 留痕(200)。
		writeJSON(w, http.StatusOK, vtAssignmentResponse{VideoTemplateAssignment: a, Replayed: false})
	default:
		// 新建分配关系(201)。
		writeJSON(w, http.StatusCreated, vtAssignmentResponse{VideoTemplateAssignment: a, Replayed: false})
	}
}

// GET /api/v1/video-templates/{id}/assignments — 按模板查询(租户级记录)。
func (s *Server) handleVideoTemplateAssignmentsByTemplate(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionReadList, w); !ok {
		return
	}
	items, err := s.St.ListVideoTemplateAssignmentsByTemplate(c.Member.TenantID, id)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "assignments lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// DELETE /api/v1/video-templates/{id}/assignments/{storeId} — 解绑(幂等)。
func (s *Server) handleVideoTemplateUnassign(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.vtTemplateScoped(c, id, authz.ActionManageVideoTemplates, w); !ok {
		return
	}
	err := s.St.UnassignVideoTemplate(c.Member.TenantID, r.PathValue("storeId"), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "resource not found")
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "unassignment failed")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"removed": true})
	}
}

// GET /api/v1/stores/{id}/video-templates — 按店查询(本店作用域:门店经理
// 可查本店;他店 out_of_scope;未知门店 404)。
func (s *Server) handleVideoTemplatesByStore(w http.ResponseWriter, r *http.Request) {
	if !s.videoTemplatesGate(w) {
		return
	}
	c := callerFrom(r)
	storeID := r.PathValue("id")
	if _, err := s.St.GetStore(storeID, c.Member.TenantID); errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "store lookup failed")
		return
	}
	if !s.requireAction(c, authz.ActionReadList,
		authz.RecordScope{TenantID: c.Member.TenantID, StoreID: storeID}, w) {
		return
	}
	items, err := s.St.ListVideoTemplateAssignmentsByStore(c.Member.TenantID, storeID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "assignments lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
