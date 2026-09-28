package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/publishreward"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// Publish reward surface (HUI-1671). Guests can see that a publish reward is
// pending verification, and can submit a proof for review. Nothing here issues
// a coupon, charges a fee, or treats preview, export, or a typed post id as
// publish success.

const rewardPendingMessage = "该渠道无法证明发布成功，发布奖励已停用，待核实。"

func (s *Server) handlePublicPublishReward(w http.ResponseWriter, r *http.Request) {
	res, ok := s.availablePublishLink(w, r)
	if !ok {
		return
	}
	rule, err := s.currentRewardRule(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish reward lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, rewardRulePayload(rule))
}

func (s *Server) handlePublishRewardPut(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	decision := authz.Authorize(s.authzMember(c), authz.ActionManagePublishReward, authz.RecordScope{TenantID: c.Member.TenantID})
	if !decision.Allowed {
		fail(w, http.StatusForbidden, decision.Reason, "not allowed to change publish reward rules")
		return
	}
	var body struct {
		Trigger         string `json:"trigger"`
		UserNotice      string `json:"user_notice"`
		RequestIssuance bool   `json:"request_issuance"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	_ = body.RequestIssuance
	campaignID := r.PathValue("id")
	if _, err := s.St.GetCampaign(campaignID, c.Member.TenantID); err != nil {
		fail(w, http.StatusNotFound, "not_found", "campaign not found")
		return
	}
	notes, err := s.St.ListPublishAdapterNotes(c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish reward lookup failed")
		return
	}
	channels := publishreward.MatrixChannels(notes)
	trigger := strings.TrimSpace(body.Trigger)
	if trigger == "" {
		trigger = publishreward.TriggerOfficialPublish
	}
	stored, err := s.St.LatestPublishRewardRule(c.Member.TenantID, campaignID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusInternalServerError, "internal", "publish reward lookup failed")
		return
	}
	var next publishreward.Rule
	if errors.Is(err, store.ErrNotFound) {
		base, cfgErr := publishreward.Configure(publishreward.ConfigureInput{
			CampaignID: campaignID, Trigger: publishreward.TriggerOfficialPublish, Channels: channels,
		})
		if cfgErr != nil {
			fail(w, http.StatusUnprocessableEntity, publishreward.ReasonOf(cfgErr), "publish reward rule was refused")
			return
		}
		if trigger == base.Trigger {
			next = base
		} else {
			revised, revErr := publishreward.ReviseTrigger(base, trigger, body.UserNotice)
			if revErr != nil {
				fail(w, http.StatusUnprocessableEntity, publishreward.ReasonOf(revErr), "changing the reward needs a new version and a notice")
				return
			}
			next = revised
		}
	} else {
		current := stored.Rule
		current.Channels = channels
		current.GrantEnabled = false
		current.IssuanceEnabled = false
		if trigger == current.Trigger {
			writeJSON(w, http.StatusOK, rewardRulePayload(current))
			return
		}
		revised, revErr := publishreward.ReviseTrigger(current, trigger, body.UserNotice)
		if revErr != nil {
			fail(w, http.StatusUnprocessableEntity, publishreward.ReasonOf(revErr), "changing the reward needs a new version and a notice")
			return
		}
		next = revised
	}
	next.GrantEnabled = false
	next.IssuanceEnabled = false
	if err := s.St.SavePublishRewardRule(c.Member.TenantID, c.Member.PrincipalRef, next); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish reward rule could not be saved")
		return
	}
	writeJSON(w, http.StatusOK, rewardRulePayload(next))
}

func (s *Server) handlePublicRewardClaim(w http.ResponseWriter, r *http.Request) {
	res, row, ok := s.publishAttempt(w, r)
	if !ok {
		return
	}
	var body struct {
		AccountClass string `json:"account_class"`
		DeductFrom   string `json:"deduct_from"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	class := strings.TrimSpace(body.AccountClass)
	if class == "" {
		class = publishreward.AccountMarketing
	}
	if err := publishreward.ValidateLedgerAccount(class, strings.TrimSpace(body.DeductFrom)); err != nil {
		fail(w, http.StatusUnprocessableEntity, publishreward.ReasonOf(err), "营销奖励不能记入订单服务款、工具余额，也不能从未结算款扣除")
		return
	}
	rule, err := s.currentRewardRule(res.Campaign.TenantID, res.Campaign.ID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "publish reward lookup failed")
		return
	}
	subject, err := s.St.FreezeRewardSubject(res.Campaign.TenantID, res.Campaign.ID, row.ID, "activity_customer:"+strings.TrimSpace(row.AccountLabel))
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "reward subject could not be saved")
		return
	}
	fact := publishreward.FactFromPreparation(row.ID, row.CampaignID, subject, row.Platform, row.Status, row.SelfReported)
	decision := publishreward.Consume(rule, fact)
	issuance := (&publishreward.Gateway{}).Issue(decision)
	if issuance.CouponsIssued != 0 || issuance.FeesCharged != 0 || issuance.OutboundCalls != 0 {
		fail(w, http.StatusConflict, publishreward.ReasonIssuanceClosed, "real coupon issuance is closed")
		return
	}
	entry := publishreward.Entry{
		CampaignID: res.Campaign.ID, SubjectID: subject, RuleVersion: rule.Version, FactID: row.ID,
		Op: publishreward.OpClaim, AccountClass: publishreward.AccountMarketing,
	}
	posted, reason, err := s.St.RecordRewardPosting(res.Campaign.TenantID, entry, decision)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "reward ledger could not be written")
		return
	}
	if reason == "" {
		reason = decision.Reason
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"grant": false, "eligible": decision.Eligible && posted.Applied, "publish_success": false,
		"platform_post_id": "", "applied": posted.Applied, "reason": reason,
		"coupons_issued": 0, "fees_charged": 0, "outbound_calls": 0,
		"account_class": publishreward.AccountMarketing,
	})
}

func (s *Server) handlePublicRewardProof(w http.ResponseWriter, r *http.Request) {
	res, row, ok := s.publishAttempt(w, r)
	if !ok {
		return
	}
	var body struct {
		Note string `json:"note"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	proof := publishreward.SubmitManualProof(publishreward.Fact{ID: row.ID, PlatformPostID: "ignored"}, body.Note)
	saved, err := s.St.InsertRewardProof(res.Campaign.TenantID, res.Campaign.ID, row.ID, proof.Note)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "proof could not be saved")
		return
	}
	writeJSON(w, http.StatusCreated, proofPayload(saved))
}

func (s *Server) handlePublishRewardReview(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	if c == nil || c.Member == nil {
		fail(w, http.StatusUnauthorized, "unauthenticated", "sign in required")
		return
	}
	decision := authz.Authorize(s.authzMember(c), authz.ActionReviewPublishReward, authz.RecordScope{TenantID: c.Member.TenantID})
	if !decision.Allowed {
		fail(w, http.StatusForbidden, decision.Reason, "not allowed to review a publish proof")
		return
	}
	var body struct {
		Basis   string `json:"basis"`
		Approve bool   `json:"approve"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", "request body must be JSON")
		return
	}
	row, err := s.St.GetRewardProof(c.Member.TenantID, r.PathValue("proofId"))
	if err != nil || row.CampaignID != r.PathValue("id") {
		fail(w, http.StatusNotFound, "not_found", "proof not found")
		return
	}
	reviewed, audit, err := publishreward.ReviewProof(publishreward.Proof{
		FactID: row.FactID, Status: row.Status, Note: row.Note,
	}, publishreward.ReviewInput{
		Actor: c.Member.PrincipalRef, Allowed: true, Basis: body.Basis, Approve: body.Approve,
	})
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, publishreward.ReasonOf(err), "review needs a written basis and does not confirm the platform post")
		return
	}
	_ = reviewed
	saved, err := s.St.ReviewRewardProof(c.Member.TenantID, row.ID, audit.Actor, audit.Basis)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "review could not be saved")
		return
	}
	out := proofPayload(saved)
	out["audit_decision"] = saved.AuditDecision
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) currentRewardRule(tenantID, campaignID string) (publishreward.Rule, error) {
	notes, err := s.St.ListPublishAdapterNotes(tenantID)
	if err != nil {
		return publishreward.Rule{}, err
	}
	channels := publishreward.MatrixChannels(notes)
	stored, err := s.St.LatestPublishRewardRule(tenantID, campaignID)
	if errors.Is(err, store.ErrNotFound) {
		return publishreward.Configure(publishreward.ConfigureInput{
			CampaignID: campaignID, Trigger: publishreward.TriggerOfficialPublish, Channels: channels,
		})
	}
	if err != nil {
		return publishreward.Rule{}, err
	}
	rule := stored.Rule
	rule.Channels = channels
	rule.GrantEnabled = false
	rule.IssuanceEnabled = false
	return rule, nil
}

func rewardRulePayload(rule publishreward.Rule) map[string]any {
	message := rewardPendingMessage
	if rule.Trigger == publishreward.TriggerParticipation && strings.TrimSpace(rule.UserNotice) != "" {
		message = rule.UserNotice
	}
	platforms := make([]any, 0, len(rule.Channels))
	for _, ch := range rule.Channels {
		platforms = append(platforms, map[string]any{
			"platform": ch.Platform, "reward_status": publishreward.StatusPendingVerification,
			"grant_enabled": false, "evidence_url": ch.EvidenceURL,
		})
	}
	return map[string]any{
		"campaign_id": rule.CampaignID, "version": rule.Version, "trigger": rule.Trigger,
		"evidence_level": rule.EvidenceLevel, "status": rule.Status,
		"grant_enabled": false, "issuance_enabled": false,
		"reason": rule.Reason, "user_notice": rule.UserNotice, "message": message,
		"coupons_issued": 0, "fees_charged": 0, "platforms": platforms,
	}
}

func proofPayload(row store.RewardProof) map[string]any {
	return map[string]any{
		"proof_id": row.ID, "status": row.Status, "platform_confirmed": false,
		"grant": false, "post_id": "", "note": row.Note,
	}
}
