// handlers_dashboard.go: HUI-1677 FEAT-0178 数据统计看板的 HTTP 读面。
//
// 纪律:
//   - 只读:唯一入口 GET /api/v1/dashboard,无任何写路径;
//   - 开关:FEATURE_DASHBOARD(默认 off)= 路由不注册,404 不可见
//     (见 server.go Handler 的条件注册);
//   - 口径:窗口参数必填且 RFC3339,非法一律 400 invalid_window
//     (绝不静默落到"默认窗口"这种隐式口径);
//   - 作用域:服务端单点裁决——org_owner/staff 租户级;store_manager 一律
//     收窄到 members.store_scope,空作用域的畸形经理 fail-closed 403。
//     客户端输入(查询参数/请求体)永不参与"能看哪些数据"的裁决。
package httpapi

import (
	"net/http"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/dashboard"
)

// dashGate answers uniform 404 when FEATURE_DASHBOARD is off: an invisible
// surface (与 FEATURE_LEADS_CAPTURE 的 leadsGate 同一纪律;叠加 server.go 的
// 条件注册,off = 路由根本不注册)。
func (s *Server) dashGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureDashboard {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// GET /api/v1/dashboard?window_start=...&window_end=...
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if !s.dashGate(w) {
		return
	}
	c := callerFrom(r)
	if !allowScopedList(c, w) {
		return
	}
	// 作用域:门店经理收窄到其 store_scope;空作用域畸形经理 fail-closed。
	scope := ""
	if callerIsStoreManager(c) {
		scope = callerStoreScope(c)
		if scope == "" {
			fail(w, http.StatusForbidden, authz.ReasonOutOfScope, "store manager has no store scope")
			return
		}
	}
	win, err := dashboard.ParseWindow(r.URL.Query().Get("window_start"), r.URL.Query().Get("window_end"))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid_window", err.Error())
		return
	}
	facts, dims, err := s.St.DashboardFacts(c.Member.TenantID, scope, win.Start, win.End)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "dashboard aggregation failed")
		return
	}
	resp := dashboard.Build(win, dashboard.Scope{TenantID: c.Member.TenantID, StoreScope: scope}, facts, dims)
	writeJSON(w, http.StatusOK, resp)
}
