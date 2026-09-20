// Package httpapi wires the HTTP surface: middleware (internal token ->
// identity session resolve -> tenant member resolution) and handlers.
//
// Fail-closed rules:
//   - missing internal token -> 401
//   - config gate problems   -> 503 config_gate (explicit, lists keys)
//   - identity unreachable   -> 503 identity_unavailable (no fake data)
//   - unauthenticated        -> 401
//   - no membership row      -> 403 not_member (never auto-provision)
//   - cross-tenant           -> 403 cross_tenant
//
// Two disjoint surfaces:
//   - /api/v1/... admin surface: platform session + membership required;
//   - /api/v1/public/... guest surface: NO session concept, GET-only, whitelist
//     fields only. A guest can never mutate anything; there is no code path
//     from the public surface to generation, billing, or any paid call.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/redact"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

const internalTokenHeader = "X-Internal-Token"
const tenantHeader = "X-Tenant-ID"

// Server is the API server.
type Server struct {
	Cfg     config.Config
	St      *store.Store
	ID      *identity.Client
	Upload  *upload.Client
	Log     *log.Logger
	closeDB func()

	// LeadsLimiter gates the public lead-write surface per client IP
	// (single-process sliding window; not distributed by design for T1).
	LeadsLimiter *leads.RateLimiter
}

// leadsRatePerMinute bounds the public lead-write surface per client IP.
// Generous enough that legitimate retries/duplicates pass (they are idempotent
// anyway); strict enough to stop bulk form spam. Single-process scope by design.
const leadsRatePerMinute = 30

// New builds a Server over an opened database.
func New(cfg config.Config, database *sql.DB, idc *identity.Client, upc *upload.Client, logger *log.Logger) *Server {
	if logger == nil {
		logger = log.Default()
	}
	return &Server{
		Cfg: cfg, St: store.New(database), ID: idc, Upload: upc, Log: logger,
		LeadsLimiter: leads.NewRateLimiter(leadsRatePerMinute, time.Minute),
	}
}

// Open opens the database and returns a ready Server.
func Open(cfg config.Config, logger *log.Logger) (*Server, error) {
	d, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	idc := &identity.Client{BaseURL: cfg.IdentityBaseURL, Token: cfg.IdentityToken, AppID: cfg.AppID}
	upc := &upload.Client{BaseURL: cfg.UploadBaseURL, Token: cfg.UploadToken, AppID: cfg.AppID}
	s := New(cfg, d, idc, upc, logger)
	s.closeDB = func() { d.Close() }
	return s, nil
}

// Close releases the database handle.
func (s *Server) Close() {
	if s.closeDB != nil {
		s.closeDB()
	}
}

// ---- caller context ---------------------------------------------------------

type caller struct {
	Principal identity.Principal
	Member    *store.Member
}

type ctxKey int

const callerKey ctxKey = 1

func callerFrom(r *http.Request) *caller {
	if v, ok := r.Context().Value(callerKey).(*caller); ok {
		return v
	}
	return nil
}

func authzMember(c *caller) *authz.Member {
	if c == nil || c.Member == nil {
		return nil
	}
	return &authz.Member{ID: c.Member.ID, TenantID: c.Member.TenantID, PrincipalRef: c.Member.PrincipalRef,
		Role: authz.Role(c.Member.Role), StoreScope: c.Member.StoreScope, Enabled: c.Member.Enabled}
}

// ---- responses --------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": code, "message": message})
}

// ---- routing ----------------------------------------------------------------

// Handler returns the root handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	// auth (internal token required; no session yet for login/refresh)
	mux.Handle("POST /api/v1/auth/login", s.requireInternal(s.handleLogin))
	mux.Handle("POST /api/v1/auth/refresh", s.requireInternal(s.handleRefresh))
	mux.Handle("POST /api/v1/auth/logout", s.requireInternal(s.handleLogout))

	// authenticated admin surface (商家后台)
	mux.Handle("GET /api/v1/whoami", s.requireSession(s.handleWhoami))

	mux.Handle("GET /api/v1/stores", s.requireSession(s.handleStoreList))
	mux.Handle("POST /api/v1/stores", s.requireSession(s.handleStoreCreate))
	mux.Handle("GET /api/v1/stores/{id}", s.requireSession(s.handleStoreGet))
	// HUI-1674 连锁多门店:org_owner 治理面(改店/停用店)。停用不级联:
	// 存量活动逐个显式处理,公共页附「门店暂不可用」标注。
	mux.Handle("PATCH /api/v1/stores/{id}", s.requireSession(s.handleStorePatch))
	mux.Handle("POST /api/v1/stores/{id}/status", s.requireSession(s.handleStoreStatus))

	mux.Handle("GET /api/v1/campaigns", s.requireSession(s.handleCampaignList))
	mux.Handle("POST /api/v1/campaigns", s.requireSession(s.handleCampaignCreate))
	mux.Handle("GET /api/v1/campaigns/{id}", s.requireSession(s.handleCampaignGet))
	mux.Handle("PATCH /api/v1/campaigns/{id}", s.requireSession(s.handleCampaignPatch))
	mux.Handle("POST /api/v1/campaigns/{id}/status", s.requireSession(s.handleCampaignStatus))

	mux.Handle("GET /api/v1/campaigns/{id}/assets", s.requireSession(s.handleAssetList))
	mux.Handle("POST /api/v1/campaigns/{id}/assets", s.requireSession(s.handleAssetAdd))
	mux.Handle("DELETE /api/v1/campaigns/{id}/assets/{assetId}", s.requireSession(s.handleAssetRemove))

	mux.Handle("GET /api/v1/campaigns/{id}/links", s.requireSession(s.handleLinkList))
	mux.Handle("POST /api/v1/campaigns/{id}/links", s.requireSession(s.handleLinkCreate))
	mux.Handle("POST /api/v1/campaigns/{id}/links/{linkId}/enabled", s.requireSession(s.handleLinkEnabled))

	// QR fallback entry (HUI-1664 FEAT-0165): owner-only export of the
	// canonical short-code URL as a server-rendered PNG (or json payload).
	mux.Handle("GET /api/v1/campaigns/{id}/links/{linkId}/qrcode", s.requireSession(s.handleLinkQRCode))

	// NFC tag management (HUI-1665 FEAT-0166): owner-only CRUD/batch/export.
	// Tags wrap EXISTING campaign_links (no second link model); disable/enable
	// propagates to the bound link so the public route follows the existing
	// five-state logic. No FEATURE gate: this is an admin surface.
	mux.Handle("GET /api/v1/nfc/tag-groups", s.requireSession(s.handleTagGroupList))
	mux.Handle("POST /api/v1/nfc/tag-groups", s.requireSession(s.handleTagGroupCreate))
	mux.Handle("DELETE /api/v1/nfc/tag-groups/{id}", s.requireSession(s.handleTagGroupDelete))
	mux.Handle("GET /api/v1/nfc/tags/export.csv", s.requireSession(s.handleTagExportCSV))
	mux.Handle("GET /api/v1/nfc/tags", s.requireSession(s.handleTagList))
	mux.Handle("POST /api/v1/nfc/tags/batch", s.requireSession(s.handleTagBatchCreate))
	mux.Handle("GET /api/v1/nfc/tags/{id}", s.requireSession(s.handleTagGet))
	mux.Handle("PATCH /api/v1/nfc/tags/{id}", s.requireSession(s.handleTagPatch))
	mux.Handle("DELETE /api/v1/nfc/tags/{id}", s.requireSession(s.handleTagDelete))
	mux.Handle("POST /api/v1/nfc/tags/{id}/status", s.requireSession(s.handleTagStatus))

	mux.Handle("GET /api/v1/admin/members", s.requireSession(s.handleMemberList))
	mux.Handle("POST /api/v1/admin/members", s.requireSession(s.handleMemberCreate))
	mux.Handle("PATCH /api/v1/admin/members/{id}", s.requireSession(s.handleMemberPatch))

	// public guest surface (公共活动页): GET-only, no session, whitelist fields.
	// Any other method on the public path is answered 405 before anything else.
	mux.Handle("GET /api/v1/public/links/{code}", http.HandlerFunc(s.handlePublicLink))

	// public lead-capture surface (HUI-1747): the ONLY guest-write surface.
	// Gated by FEATURE_LEADS_CAPTURE (off -> uniform 404, surface invisible).
	mux.Handle("GET /api/v1/public/links/{code}/lead-form", http.HandlerFunc(s.handlePublicLeadForm))
	mux.Handle("POST /api/v1/public/links/{code}/lead-submissions", http.HandlerFunc(s.handlePublicLeadSubmit))
	mux.Handle("POST /api/v1/public/links/{code}/lead-revocations", http.HandlerFunc(s.handlePublicLeadRevoke))
	mux.Handle("POST /api/v1/public/links/{code}/view-events", http.HandlerFunc(s.handlePublicViewEvent))

	// admin lead surface (HUI-1747): merchant visibility of their own leads.
	mux.Handle("POST /api/v1/campaigns/{id}/lead-form", s.requireSession(s.handleLeadFormUpsert))
	mux.Handle("GET /api/v1/campaigns/{id}/leads", s.requireSession(s.handleLeadList))
	mux.Handle("GET /api/v1/campaigns/{id}/lead-stats", s.requireSession(s.handleLeadStats))
	mux.Handle("GET /api/v1/campaigns/{id}/leads/{ref}/audit", s.requireSession(s.handleLeadAudit))

	// dashboard read surface (HUI-1677 FEAT-0178): gated by FEATURE_DASHBOARD
	// (default off; off = route not registered AND handler gate answers uniform
	// 404 — an invisible surface). Read-only aggregation, no write paths.
	if s.Cfg.FeatureDashboard {
		mux.Handle("GET /api/v1/dashboard", s.requireSession(s.handleDashboard))
	}

	// campaign rules (HUI-1676 FEAT-0177): 登记制开关 FEATURE_CAMPAIGN_RULES
	// (默认 off;off = 路由不注册且 handler gate 再答统一 404 —— 与
	// FEATURE_DASHBOARD 的 off 双保险同一写法)。规则读写仅 org_owner
	// (authz.manage_campaign_rules);确定性评估在服务端单点完成,BFF 零业务判断。
	if s.Cfg.FeatureCampaignRules {
		mux.Handle("GET /api/v1/campaigns/{id}/rules", s.requireSession(s.handleCampaignRulesGet))
		mux.Handle("PUT /api/v1/campaigns/{id}/rules", s.requireSession(s.handleCampaignRulesPut))
		mux.Handle("DELETE /api/v1/campaigns/{id}/rules", s.requireSession(s.handleCampaignRulesDelete))
		mux.Handle("GET /api/v1/campaigns/{id}/rules/revisions", s.requireSession(s.handleCampaignRulesRevisions))
	}

	// asset library (HUI-1666 FEAT-0167 商家素材库 v1): 登记制开关
	// FEATURE_ASSET_LIB(默认 off;off = 路由不注册且 handler gate 再答统一
	// 404 —— 同一双保险写法,面板不可见)。管理动作(登记/导入素材、池增删改、
	// 池内引用增删、候选标记)仅 org_owner(authz.manage_asset_lib);选择/调取
	// 按既有业务角色(ActionCreate/ActionReadList)。v1 不建物理文件仓库:
	// 接口只收元数据+指纹;物理授权资产版本面(HUI-1732)未建,grant_ref 为
	// 不透明声明引用。
	if s.Cfg.FeatureAssetLib {
		mux.Handle("POST /api/v1/assets", s.requireSession(s.handleLibAssetRegister))
		mux.Handle("POST /api/v1/assets/import", s.requireSession(s.handleLibAssetImport))
		mux.Handle("GET /api/v1/assets", s.requireSession(s.handleLibAssetList))
		mux.Handle("GET /api/v1/assets/{id}", s.requireSession(s.handleLibAssetGet))
		mux.Handle("POST /api/v1/assets/{id}/candidate", s.requireSession(s.handleLibAssetCandidate))

		mux.Handle("POST /api/v1/asset-pools", s.requireSession(s.handleLibPoolCreate))
		mux.Handle("GET /api/v1/asset-pools", s.requireSession(s.handleLibPoolList))
		mux.Handle("GET /api/v1/asset-pools/{id}", s.requireSession(s.handleLibPoolGet))
		mux.Handle("PATCH /api/v1/asset-pools/{id}", s.requireSession(s.handleLibPoolPatch))
		mux.Handle("DELETE /api/v1/asset-pools/{id}", s.requireSession(s.handleLibPoolDelete))
		mux.Handle("POST /api/v1/asset-pools/{id}/items", s.requireSession(s.handleLibPoolItemAdd))
		mux.Handle("GET /api/v1/asset-pools/{id}/items", s.requireSession(s.handleLibPoolItemList))
		mux.Handle("DELETE /api/v1/asset-pools/{id}/items/{itemId}", s.requireSession(s.handleLibPoolItemRemove))
		mux.Handle("POST /api/v1/asset-pools/{id}/draw", s.requireSession(s.handleLibPoolDraw))
		mux.Handle("GET /api/v1/asset-pools/{id}/selections", s.requireSession(s.handleLibPoolSelections))
	}

	// video templates (HUI-1669 FEAT-0170 视频模板管理): 登记制开关
	// FEATURE_VIDEO_TEMPLATES(默认 off;off = 全部路由不注册且 handler gate
	// 再答统一 404 —— 同一双保险写法,面板不可见)。管理动作(模板 CRUD/
	// 发布/版本/分配)仅 org_owner(authz.manage_video_templates);查询按
	// 既有业务角色(本店分配查询按 store 作用域)。模板=素材引用集的组织
	// 单元,零物理存储;视频合成/渲染执行不做(接口零生成/渲染触发字段)。
	if s.Cfg.FeatureVideoTemplates {
		mux.Handle("POST /api/v1/video-templates", s.requireSession(s.handleVideoTemplateCreate))
		mux.Handle("GET /api/v1/video-templates", s.requireSession(s.handleVideoTemplateList))
		mux.Handle("GET /api/v1/video-templates/{id}", s.requireSession(s.handleVideoTemplateGet))
		mux.Handle("PATCH /api/v1/video-templates/{id}", s.requireSession(s.handleVideoTemplatePatch))
		mux.Handle("DELETE /api/v1/video-templates/{id}", s.requireSession(s.handleVideoTemplateDelete))
		mux.Handle("GET /api/v1/video-templates/{id}/versions", s.requireSession(s.handleVideoTemplateVersionList))
		mux.Handle("POST /api/v1/video-templates/{id}/versions", s.requireSession(s.handleVideoTemplateVersionCreate))
		mux.Handle("GET /api/v1/video-templates/{id}/versions/{version}", s.requireSession(s.handleVideoTemplateVersionGet))
		mux.Handle("PUT /api/v1/video-templates/{id}/versions/{version}", s.requireSession(s.handleVideoTemplateVersionPut))
		mux.Handle("POST /api/v1/video-templates/{id}/publish", s.requireSession(s.handleVideoTemplatePublish))
		mux.Handle("POST /api/v1/video-templates/{id}/assignments", s.requireSession(s.handleVideoTemplateAssign))
		mux.Handle("GET /api/v1/video-templates/{id}/assignments", s.requireSession(s.handleVideoTemplateAssignmentsByTemplate))
		mux.Handle("DELETE /api/v1/video-templates/{id}/assignments/{storeId}", s.requireSession(s.handleVideoTemplateUnassign))
		mux.Handle("GET /api/v1/stores/{id}/video-templates", s.requireSession(s.handleVideoTemplatesByStore))
	}

	return s.withRequestLog(mux)
}

// ---- middleware -------------------------------------------------------------

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		c := callerFrom(r)
		who := "guest"
		if c != nil {
			who = redact.MaskEmail(c.Principal.Email) + " principal=" + c.Principal.ID
		}
		s.Log.Printf("%s %s -> %d (%s) %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond), who)
	})
}

// requireInternal enforces the BFF->server shared secret.
func (s *Server) requireInternal(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Cfg.InternalToken == "" || r.Header.Get(internalTokenHeader) != s.Cfg.InternalToken {
			fail(w, http.StatusUnauthorized, "unauthorized", "missing or wrong internal token")
			return
		}
		next(w, r)
	})
}

// requireSession chains: internal token -> config gate -> identity resolve ->
// tenant membership resolve. It is the single identity path; there is no
// alternate or stub mode.
func (s *Server) requireSession(next http.HandlerFunc) http.Handler {
	return s.requireInternal(func(w http.ResponseWriter, r *http.Request) {
		if problems := s.Cfg.Gate(); len(problems) > 0 {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":   "config_gate",
				"message": "server platform integration is not configured; refusing to act (fail-closed)",
				"detail":  problems,
			})
			return
		}

		sessionToken := sessionTokenFromRequest(r, s.Cfg.SessionCookie)
		if sessionToken == "" {
			fail(w, http.StatusUnauthorized, "unauthenticated", "no session")
			return
		}
		principal, err := s.ID.ResolveSession(r.Context(), sessionToken)
		if err != nil {
			switch {
			case errors.Is(err, identity.ErrUnauthenticated):
				fail(w, http.StatusUnauthorized, "unauthenticated", "session rejected by identity")
			default:
				// transport/config failure: explicit 503, never fake a session
				writeJSON(w, http.StatusServiceUnavailable, map[string]any{
					"error":   "identity_unavailable",
					"message": "platform identity could not be reached; refusing to act (fail-closed)",
				})
			}
			return
		}

		tenantID := strings.TrimSpace(r.Header.Get(tenantHeader))
		if tenantID == "" {
			fail(w, http.StatusBadRequest, "tenant_required", "header "+tenantHeader+" selects the workspace tenant")
			return
		}

		member, err := s.St.GetMemberByPrincipal(tenantID, principal.ID)
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
			// 身份纪律:principal 无成员行 -> 拒绝,绝不自动入租户/开户
			fail(w, http.StatusForbidden, authz.ReasonNotMember, "principal is not a member of this tenant")
			return
		}
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "member lookup failed")
			return
		}

		c := &caller{Principal: principal, Member: &member}
		next(w, r.WithContext(context.WithValue(r.Context(), callerKey, c)))
	})
}

func sessionTokenFromRequest(r *http.Request, cookieName string) string {
	if h := r.Header.Get("X-Session-Token"); h != "" {
		return h
	}
	if ck, err := r.Cookie(cookieName); err == nil {
		return ck.Value
	}
	return ""
}

// requireAction wraps a handler with an authz decision for a specific action.
func (s *Server) requireAction(c *caller, action authz.Action, rec authz.RecordScope, w http.ResponseWriter) bool {
	d := authz.Authorize(authzMember(c), action, rec)
	if d.Allowed {
		return true
	}
	fail(w, http.StatusForbidden, d.Reason, "action not allowed for this member")
	return false
}
