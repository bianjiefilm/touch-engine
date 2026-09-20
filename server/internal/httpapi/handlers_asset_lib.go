// handlers_asset_lib.go: HUI-1666 FEAT-0167 商家素材库 v1 的 HTTP 面。
//
// 纪律:
//   - 开关:FEATURE_ASSET_LIB(默认 off)= 路由不注册(见 server.go 条件注册)
//     且本 gate 再答统一 404 —— 与 FEATURE_DASHBOARD / FEATURE_CAMPAIGN_RULES
//     的 off 双保险同一写法;
//   - 授权:管理动作(登记/导入素材、池增删改、池内引用增删、候选标记)走
//     authz.ActionManageAssetLib(仅 org_owner);选择/调取按既有业务角色
//     (ActionCreate / ActionReadList;池与库均为租户级记录,门店经理按既有
//     纪律 out_of_scope);
//   - 授权语义 v1 = 引用级声明:source/purpose/grant_ref 缺任一 422(独立
//     原因码);sha256 与平台引用不符(坏引用)= 409 version_mismatch,与
//     既有 campaign assets 纪律一致;引用必须经平台 upload 校验
//     (FEATURE_UPLOAD off = 显式 503,不伪造授权);
//   - v1 不建物理文件仓库:接口只收元数据+指纹,零文件体;导入路径零触发
//     生成、零模型调用;
//   - 大文件零进 Context/Notify:本文件没有任何素材内容通道。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

// assetLibGate answers uniform 404 when FEATURE_ASSET_LIB is off: an
// invisible surface (rulesGate/dashGate 同一纪律).
func (s *Server) assetLibGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureAssetLib {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// libTenantAction authorizes an action on the tenant-level library surface
// (assets/pools/selections carry no store binding of their own).
func (s *Server) libTenantAction(c *caller, action authz.Action, w http.ResponseWriter) bool {
	return s.requireAction(c, action, authz.RecordScope{TenantID: c.Member.TenantID}, w)
}

// invalid422 maps a domain ValidationError to 422 with the machine reason as
// the error code (缺任一声明的独立原因码直通响应).
func invalid422(w http.ResponseWriter, err error) bool {
	var ve *assetlib.ValidationError
	if !errors.As(err, &ve) {
		return false
	}
	fail(w, http.StatusUnprocessableEntity, ve.Reason, "declaration rejected: "+ve.Reason+" ("+ve.Field+")")
	return true
}

// decodeLibBody decodes a JSON body with unknown fields refused; an empty
// body decodes to the zero value (draw with all defaults).
func decodeLibBody(r *http.Request, in any) (bool, error) {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(in)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return err == nil, err
}

// validateLibRefAgainstUpload is the single reference-validation path: the
// declaration matrix ran first (422), the fingerprint is checked against the
// platform upload anchor here (409 version_mismatch = 坏引用拒绝). FEATURE_UPLOAD
// off = explicit refusal (fail-closed, never fake an authorization).
func (s *Server) validateLibRefAgainstUpload(w http.ResponseWriter, r *http.Request, assetRef, sha256 string) bool {
	if !s.Cfg.FeatureUpload {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "upload_disabled",
			"message": "FEATURE_UPLOAD is off; asset references cannot be validated (fail-closed)",
		})
		return false
	}
	meta, err := s.Upload.ValidateRef(r.Context(), assetRef, sha256)
	switch {
	case err == nil:
		_ = meta
	case errors.Is(err, upload.ErrNotFound):
		fail(w, http.StatusNotFound, "upload_not_found", "asset does not exist (or deleted/quarantined) at platform upload")
	case errors.Is(err, upload.ErrUnauthorized):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "upload_unavailable",
			"message": "platform upload rejected this app's credentials; check PLATFORM_UPLOAD_TOKEN",
		})
	case errors.Is(err, upload.ErrForbidden):
		fail(w, http.StatusForbidden, "upload_forbidden", "asset exists but this app may not reference it")
	case errors.Is(err, upload.ErrNotReady):
		fail(w, http.StatusConflict, "upload_not_ready", "asset is not in ready state")
	case errors.Is(err, upload.ErrVersionMismatch):
		// sha256 与引用不符(坏引用):拒绝登记,绝不落引用行。
		fail(w, http.StatusConflict, "version_mismatch", "supplied sha256 does not match the platform upload anchor for this asset_ref")
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "upload_unavailable",
			"message": "platform upload could not be reached; refusing to record a reference (fail-closed)",
		})
	}
	return err == nil
}

// ---- assets (素材登记/导入/查询) -------------------------------------------------

type libAssetBody struct {
	AssetRef  string   `json:"asset_ref"`
	SHA256    string   `json:"sha256"`
	MediaType string   `json:"media_type"`
	Source    string   `json:"source"`
	Purpose   string   `json:"purpose"`
	GrantRef  string   `json:"grant_ref"`
	StoreID   string   `json:"store_id"`
	Tags      []string `json:"tags"`
	Candidate bool     `json:"candidate"`
}

func (b libAssetBody) registration() assetlib.Registration {
	return assetlib.Registration{
		AssetRef:  b.AssetRef,
		SHA256:    b.SHA256,
		MediaType: assetlib.MediaType(b.MediaType),
		Source:    assetlib.Source(b.Source),
		Purpose:   b.Purpose,
		GrantRef:  b.GrantRef,
		Tags:      b.Tags,
		Candidate: b.Candidate,
	}
}

// POST /api/v1/assets — 通用登记端点(全部 v1 来源)。
func (s *Server) handleLibAssetRegister(w http.ResponseWriter, r *http.Request) {
	s.handleLibAssetUpsert(w, r, false)
}

// POST /api/v1/assets/import — 产品图/AiCut 导入端点(仅这两个来源;引用级
// 登记:零触发生成、零模型调用,本路径没有任何生成通道)。
func (s *Server) handleLibAssetImport(w http.ResponseWriter, r *http.Request) {
	s.handleLibAssetUpsert(w, r, true)
}

func (s *Server) handleLibAssetUpsert(w http.ResponseWriter, r *http.Request, importPath bool) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.libTenantAction(c, authz.ActionManageAssetLib, w) {
		return
	}
	var in libAssetBody
	ok, err := decodeLibBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	reg := in.registration()
	verr := assetlib.ValidateRegistration(reg)
	if importPath {
		verr = assetlib.ValidateImport(reg)
	}
	if verr != nil {
		if !invalid422(w, verr) {
			fail(w, http.StatusInternalServerError, "internal", "validation failed")
		}
		return
	}
	if !s.validateLibRefAgainstUpload(w, r, reg.AssetRef, reg.SHA256) {
		return
	}
	a, err := s.St.CreateLibAsset(c.Member.TenantID, reg, in.StoreID, c.Member.PrincipalRef)
	if errors.Is(err, store.ErrDuplicateAsset) {
		fail(w, http.StatusConflict, "duplicate_asset", "this (asset_ref, sha256, candidate) row is already registered")
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "store_not_found", "store_id not found in this tenant")
		return
	}
	if err != nil {
		if invalid422(w, err) {
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "asset registration failed")
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

// GET /api/v1/assets — 租户内素材列表( oldest-first)。
func (s *Server) handleLibAssetList(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.libTenantAction(c, authz.ActionReadList, w) {
		return
	}
	items, err := s.St.ListLibAssets(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "asset list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// GET /api/v1/assets/{id} — 单条素材(跨租户 404 不可见)。
func (s *Server) handleLibAssetGet(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	a, err := s.St.GetLibAsset(r.PathValue("id"), c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "asset lookup failed")
		return
	}
	if !s.libTenantAction(c, authz.ActionReadRecord, w) {
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// POST /api/v1/assets/{id}/candidate — 回流候选标记:只改标记,不动引用与版本。
func (s *Server) handleLibAssetCandidate(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.libTenantAction(c, authz.ActionManageAssetLib, w) {
		return
	}
	var in struct {
		Candidate *bool `json:"candidate"`
	}
	ok, err := decodeLibBody(r, &in)
	if err != nil || !ok || in.Candidate == nil {
		fail(w, http.StatusBadRequest, "bad_request", "candidate (boolean) is required")
		return
	}
	a, err := s.St.SetLibAssetCandidate(r.PathValue("id"), c.Member.TenantID, *in.Candidate)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return
	}
	if errors.Is(err, store.ErrDuplicateAsset) {
		fail(w, http.StatusConflict, "duplicate_asset", "a same-key row already exists with the target candidate flag")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "candidate update failed")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// ---- pools (池 CRUD 与池内引用) --------------------------------------------------

// POST /api/v1/asset-pools
func (s *Server) handleLibPoolCreate(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.libTenantAction(c, authz.ActionManageAssetLib, w) {
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
	if err := assetlib.ValidatePoolName(in.Name); err != nil {
		if !invalid422(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "validation failed")
		}
		return
	}
	p, err := s.St.CreateLibPool(c.Member.TenantID, in.Name, c.Member.PrincipalRef)
	if errors.Is(err, store.ErrDuplicatePool) {
		fail(w, http.StatusConflict, "duplicate_pool", "a pool with this name already exists in this tenant")
		return
	}
	if err != nil {
		if invalid422(w, err) {
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "pool create failed")
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// GET /api/v1/asset-pools
func (s *Server) handleLibPoolList(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	if !s.libTenantAction(c, authz.ActionReadList, w) {
		return
	}
	items, err := s.St.ListLibPools(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "pool list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// libPoolScoped fetches the pool within the caller's tenant (cross-tenant =
// 404 invisible) and then authorizes the action on the tenant-level record.
func (s *Server) libPoolScoped(c *caller, id string, action authz.Action, w http.ResponseWriter) (store.LibPool, bool) {
	p, err := s.St.GetLibPool(id, c.Member.TenantID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "resource not found")
		return store.LibPool{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "pool lookup failed")
		return store.LibPool{}, false
	}
	if !s.libTenantAction(c, action, w) {
		return store.LibPool{}, false
	}
	return p, true
}

// GET /api/v1/asset-pools/{id}
func (s *Server) handleLibPoolGet(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	if p, ok := s.libPoolScoped(c, r.PathValue("id"), authz.ActionReadRecord, w); ok {
		writeJSON(w, http.StatusOK, p)
	}
}

// PATCH /api/v1/asset-pools/{id} — rename only (增删改池的「改」).
func (s *Server) handleLibPoolPatch(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	id := r.PathValue("id")
	if _, ok := s.libPoolScoped(c, id, authz.ActionManageAssetLib, w); !ok {
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
	p, err := s.St.RenameLibPool(id, c.Member.TenantID, in.Name)
	if errors.Is(err, store.ErrDuplicatePool) {
		fail(w, http.StatusConflict, "duplicate_pool", "a pool with this name already exists in this tenant")
		return
	}
	if err != nil {
		if invalid422(w, err) {
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "pool rename failed")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// DELETE /api/v1/asset-pools/{id} — 删池连带池内引用;选择台账保持完整
// (台账独立于池生命周期)。
func (s *Server) handleLibPoolDelete(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	if _, ok := s.libPoolScoped(c, r.PathValue("id"), authz.ActionManageAssetLib, w); !ok {
		return
	}
	if err := s.St.DeleteLibPool(r.PathValue("id"), c.Member.TenantID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, "not_found", "resource not found")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "pool delete failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// POST /api/v1/asset-pools/{id}/items — 池内素材引用(多套池,池间可共享)。
func (s *Server) handleLibPoolItemAdd(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	poolID := r.PathValue("id")
	if _, ok := s.libPoolScoped(c, poolID, authz.ActionManageAssetLib, w); !ok {
		return
	}
	var in struct {
		AssetID string `json:"asset_id"`
	}
	ok, err := decodeLibBody(r, &in)
	if err != nil || !ok {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	item, err := s.St.AddLibPoolItem(c.Member.TenantID, poolID, in.AssetID, c.Member.PrincipalRef)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "asset (or pool) not found in this tenant")
	case errors.Is(err, store.ErrDuplicatePoolItem):
		fail(w, http.StatusConflict, "duplicate_pool_item", "this asset is already in the pool")
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "pool item add failed")
	default:
		writeJSON(w, http.StatusCreated, item)
	}
}

// GET /api/v1/asset-pools/{id}/items — 确定性顺序 (created_at, id)。
func (s *Server) handleLibPoolItemList(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	poolID := r.PathValue("id")
	if _, ok := s.libPoolScoped(c, poolID, authz.ActionReadList, w); !ok {
		return
	}
	items, err := s.St.ListLibPoolItems(c.Member.TenantID, poolID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "pool items lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// DELETE /api/v1/asset-pools/{id}/items/{itemId}
func (s *Server) handleLibPoolItemRemove(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	poolID := r.PathValue("id")
	if _, ok := s.libPoolScoped(c, poolID, authz.ActionManageAssetLib, w); !ok {
		return
	}
	err := s.St.RemoveLibPoolItem(c.Member.TenantID, poolID, r.PathValue("itemId"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "pool item not found")
	case err != nil:
		fail(w, http.StatusInternalServerError, "internal", "pool item remove failed")
	default:
		writeJSON(w, http.StatusOK, map[string]any{"removed": true})
	}
}

// ---- draw + ledger (调取与选择台账) ----------------------------------------------

// POST /api/v1/asset-pools/{id}/draw — 确定性随机调取:种子=(pool_id, 当日 UTC
// 日期),同种子必同选;同键 (select_key, day, candidate) 幂等重放冻结行;
// 空池 409;调取不改池。请求体只含键与标记,零内容体。
func (s *Server) handleLibPoolDraw(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	poolID := r.PathValue("id")
	if _, ok := s.libPoolScoped(c, poolID, authz.ActionCreate, w); !ok {
		return
	}
	var in struct {
		SelectKey string `json:"select_key"`
		DayUTC    string `json:"day_utc"`
		Candidate bool   `json:"candidate"`
	}
	if _, err := decodeLibBody(r, &in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	day := in.DayUTC
	if day == "" {
		day = time.Now().UTC().Format("2006-01-02")
	}
	if err := assetlib.ValidateSelectKey(in.SelectKey); err != nil {
		if !invalid422(w, err) {
			fail(w, http.StatusInternalServerError, "internal", "validation failed")
		}
		return
	}
	if !assetlib.ValidDayUTC(day) {
		if !invalid422(w, assetlib.NewValidationError("bad_day_utc", "day_utc")) {
			fail(w, http.StatusInternalServerError, "internal", "validation failed")
		}
		return
	}
	sel, replayed, err := s.St.DrawLibPool(c.Member.TenantID, poolID, in.SelectKey, day, in.Candidate, c.Member.PrincipalRef)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "not_found", "resource not found")
	case errors.Is(err, assetlib.ErrEmptyPool):
		fail(w, http.StatusConflict, "empty_pool", "cannot draw from an empty pool")
	case err != nil:
		if invalid422(w, err) {
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "draw failed")
	default:
		asset, aerr := s.St.GetLibAsset(sel.AssetID, c.Member.TenantID)
		if aerr != nil {
			fail(w, http.StatusInternalServerError, "internal", "drawn asset lookup failed")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"selection": sel,
			"asset":     asset,
			"replayed":  replayed,
		})
	}
}

// GET /api/v1/asset-pools/{id}/selections — 该池的选择台账(只追加,可审计)。
func (s *Server) handleLibPoolSelections(w http.ResponseWriter, r *http.Request) {
	if !s.assetLibGate(w) {
		return
	}
	c := callerFrom(r)
	poolID := r.PathValue("id")
	if _, ok := s.libPoolScoped(c, poolID, authz.ActionReadList, w); !ok {
		return
	}
	items, err := s.St.ListLibSelections(c.Member.TenantID, poolID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "selections lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
