// handlers_campaign_rules.go: HUI-1676 FEAT-0177 活动规则配置的 HTTP 治理面。
//
// 纪律:
//   - 开关:FEATURE_CAMPAIGN_RULES(默认 off)= 路由不注册(见 server.go 条件
//     注册)且本 gate 再答统一 404 —— 与 FEATURE_DASHBOARD 的 off 双保险同一写法;
//   - 授权:CRUD 全部走 authz.ActionManageCampaignRules(仅 org_owner);
//     跨租户活动一律 404 不可见(campaignScoped 掩码);
//   - 服务端单点判定,BFF 零业务判断:校验矩阵复用 campaignrules.Validate,
//     评估复用 campaignrules.Evaluate,本文件只做形状/授权/落库;
//   - 变更走版本化留痕(修订快照),永不静默改写;
//   - PUT 是全量替换语义:体省略/null 的字段 = 收回该规则(nil=不限)。
package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/campaignrules"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// rulesGate answers uniform 404 when FEATURE_CAMPAIGN_RULES is off: an
// invisible surface (dashGate/leadsGate 同一纪律)。
func (s *Server) rulesGate(w http.ResponseWriter) bool {
	if s.Cfg.FeatureCampaignRules {
		return true
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "resource not found"})
	return false
}

// PUT /api/v1/campaigns/{id}/rules — full-replace the campaign's ruleset.
func (s *Server) handleCampaignRulesPut(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionManageCampaignRules, w); !ok {
		return
	}
	var in campaignrules.Ruleset
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "invalid or disallowed field in body")
		return
	}
	if err := campaignrules.Validate(in); err != nil {
		fail(w, http.StatusBadRequest, "invalid_rules", err.Error())
		return
	}
	rec, err := s.St.UpsertCampaignRules(c.Member.TenantID, r.PathValue("id"), in, c.Member.PrincipalRef)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign rules save failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// GET /api/v1/campaigns/{id}/rules — current ruleset (404 when never configured).
func (s *Server) handleCampaignRulesGet(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionManageCampaignRules, w); !ok {
		return
	}
	rec, err := s.St.GetCampaignRules(c.Member.TenantID, r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "campaign rules not configured")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign rules lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// DELETE /api/v1/campaigns/{id}/rules — clear the ruleset (a delete revision is
// recorded; the trail stays auditable).
func (s *Server) handleCampaignRulesDelete(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionManageCampaignRules, w); !ok {
		return
	}
	if err := s.St.DeleteCampaignRules(c.Member.TenantID, r.PathValue("id"), c.Member.PrincipalRef); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			fail(w, http.StatusNotFound, "not_found", "campaign rules not configured")
			return
		}
		fail(w, http.StatusInternalServerError, "internal", "campaign rules delete failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

// GET /api/v1/campaigns/{id}/rules/revisions — the immutable change trail.
func (s *Server) handleCampaignRulesRevisions(w http.ResponseWriter, r *http.Request) {
	if !s.rulesGate(w) {
		return
	}
	c := callerFrom(r)
	if _, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionManageCampaignRules, w); !ok {
		return
	}
	items, err := s.St.ListCampaignRuleRevisions(c.Member.TenantID, r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign revisions lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
