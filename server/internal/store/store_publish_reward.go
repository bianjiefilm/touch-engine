package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/publishreward"
)

// PublishRewardRule is a stored reward rule version. Grant and issuance cannot
// be turned on by this store.
type PublishRewardRule struct {
	publishreward.Rule
	TenantID string
}

// RewardProof is a stored manual proof. PlatformConfirmed stays false.
type RewardProof struct {
	ID                string
	TenantID          string
	CampaignID        string
	FactID            string
	Status            string
	PlatformConfirmed bool
	Grant             bool
	PostID            string
	Note              string
	ReviewActor       string
	ReviewBasis       string
	AuditDecision     string
}

const rewardProofCols = `id,tenant_id,campaign_id,fact_id,status,platform_confirmed,granted,post_id,note,review_actor,review_basis,audit_decision`

func scanRewardProof(sc interface{ Scan(...any) error }) (RewardProof, error) {
	var row RewardProof
	var confirmed, granted int
	err := sc.Scan(&row.ID, &row.TenantID, &row.CampaignID, &row.FactID, &row.Status, &confirmed, &granted,
		&row.PostID, &row.Note, &row.ReviewActor, &row.ReviewBasis, &row.AuditDecision)
	row.PlatformConfirmed = confirmed == 1
	row.Grant = granted == 1
	return row, err
}

// SavePublishRewardRule appends one version. It refuses a rule that would grant
// or issue.
func (s *Store) SavePublishRewardRule(tenantID, actor string, rule publishreward.Rule) error {
	if rule.GrantEnabled || rule.IssuanceEnabled {
		return errors.New("store: refusing to persist an enabled publish reward")
	}
	if _, err := s.GetCampaign(rule.CampaignID, tenantID); err != nil {
		return err
	}
	_, err := s.DB.Exec(`INSERT INTO publish_reward_rules(
		id,tenant_id,campaign_id,version,trigger,evidence_level,status,grant_enabled,issuance_enabled,user_notice,reason,created_by,created_at
	) VALUES(?,?,?,?,?,?,?,0,0,?,?,?,?)`,
		newID("pwr_"), tenantID, rule.CampaignID, rule.Version, rule.Trigger, rule.EvidenceLevel, rule.Status,
		rule.UserNotice, rule.Reason, actor, now())
	return err
}

// LatestPublishRewardRule returns the highest version for the campaign.
func (s *Store) LatestPublishRewardRule(tenantID, campaignID string) (PublishRewardRule, error) {
	var row PublishRewardRule
	var grant, issuance int
	err := s.DB.QueryRow(`SELECT tenant_id,campaign_id,version,trigger,evidence_level,status,grant_enabled,issuance_enabled,user_notice,reason
		FROM publish_reward_rules WHERE tenant_id=? AND campaign_id=? ORDER BY version DESC LIMIT 1`, tenantID, campaignID).
		Scan(&row.TenantID, &row.CampaignID, &row.Version, &row.Trigger, &row.EvidenceLevel, &row.Status, &grant, &issuance, &row.UserNotice, &row.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishRewardRule{}, ErrNotFound
	}
	row.GrantEnabled = grant == 1
	row.IssuanceEnabled = issuance == 1
	return row, err
}

// FreezeRewardSubject records the reward subject for a fact once.
func (s *Store) FreezeRewardSubject(tenantID, campaignID, factID, subjectID string) (string, error) {
	subjectID = strings.TrimSpace(subjectID)
	if subjectID == "" || factID == "" {
		return "", errors.New("store: reward subject requires fact and subject")
	}
	_, err := s.DB.Exec(`INSERT INTO publish_reward_subjects(fact_id,tenant_id,campaign_id,subject_id,created_at)
		VALUES(?,?,?,?,?) ON CONFLICT(fact_id) DO NOTHING`, factID, tenantID, campaignID, subjectID, now())
	if err != nil {
		return "", err
	}
	var frozen string
	err = s.DB.QueryRow(`SELECT subject_id FROM publish_reward_subjects WHERE fact_id=? AND tenant_id=?`, factID, tenantID).Scan(&frozen)
	return frozen, err
}

// RecordRewardPosting inserts one ledger row. A duplicate key returns the
// original row with Applied false and does not add another.
func (s *Store) RecordRewardPosting(tenantID string, e publishreward.Entry, d publishreward.Decision) (publishreward.Result, string, error) {
	if err := publishreward.ValidateLedgerAccount(e.AccountClass, e.DeductFrom); err != nil {
		return publishreward.Result{}, "", err
	}
	if d.Grant || d.CouponsIssued != 0 || d.FeesCharged != 0 || d.OutboundCalls != 0 || strings.TrimSpace(d.PostID) != "" {
		return publishreward.Result{}, "", errors.New("store: refusing to persist a reward issuance")
	}
	eligible := 0
	if d.Eligible {
		eligible = 1
	}
	_, err := s.DB.Exec(`INSERT INTO publish_reward_ledger(
		id,tenant_id,campaign_id,subject_id,rule_version,fact_id,op,account_class,deduct_from,outcome,reason,eligible,granted,coupons_issued,fees_charged,outbound_calls,platform_post_id,created_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,0,0,0,0,'',?)`,
		newID("prl_"), tenantID, e.CampaignID, e.SubjectID, e.RuleVersion, e.FactID, e.Op, publishreward.AccountMarketing, "",
		publishreward.OutcomeRecorded, d.Reason, eligible, now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			res, reason, replayErr := s.replayRewardPosting(e)
			return res, reason, replayErr
		}
		return publishreward.Result{}, "", err
	}
	return publishreward.Result{Applied: true, Outcome: publishreward.OutcomeRecorded, AccountClass: publishreward.AccountMarketing}, d.Reason, nil
}

func (s *Store) replayRewardPosting(e publishreward.Entry) (publishreward.Result, string, error) {
	var reason string
	err := s.DB.QueryRow(`SELECT reason FROM publish_reward_ledger
		WHERE campaign_id=? AND subject_id=? AND rule_version=? AND fact_id=? AND op=?`,
		e.CampaignID, e.SubjectID, e.RuleVersion, e.FactID, e.Op).Scan(&reason)
	if err != nil {
		return publishreward.Result{}, "", err
	}
	return publishreward.Result{Applied: false, Outcome: publishreward.OutcomeReplay, AccountClass: publishreward.AccountMarketing}, reason, nil
}

// RewardLedgerReason is the stored reason for one posting.
func (s *Store) RewardLedgerReason(e publishreward.Entry) (string, error) {
	var reason string
	err := s.DB.QueryRow(`SELECT reason FROM publish_reward_ledger
		WHERE campaign_id=? AND subject_id=? AND rule_version=? AND fact_id=? AND op=?`,
		e.CampaignID, e.SubjectID, e.RuleVersion, e.FactID, e.Op).Scan(&reason)
	return reason, err
}

// CountRewardLedger counts rows for one key and operation.
func (s *Store) CountRewardLedger(e publishreward.Entry, op string) (int, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM publish_reward_ledger
		WHERE campaign_id=? AND subject_id=? AND rule_version=? AND fact_id=? AND op=?`,
		e.CampaignID, e.SubjectID, e.RuleVersion, e.FactID, op).Scan(&n)
	return n, err
}

// InsertRewardProof stores a manual proof as pending review.
func (s *Store) InsertRewardProof(tenantID, campaignID, factID, note string) (RewardProof, error) {
	row := RewardProof{
		ID: newID("prp_"), TenantID: tenantID, CampaignID: campaignID, FactID: factID,
		Status: publishreward.StatusPendingReview, Note: strings.TrimSpace(note),
	}
	_, err := s.DB.Exec(`INSERT INTO publish_reward_proofs(
		id,tenant_id,campaign_id,fact_id,status,note,created_at
	) VALUES(?,?,?,?,?,?,?)`,
		row.ID, row.TenantID, row.CampaignID, row.FactID, row.Status, row.Note, now())
	if err != nil {
		return RewardProof{}, err
	}
	return row, nil
}

// GetRewardProof loads one proof inside the tenant.
func (s *Store) GetRewardProof(tenantID, id string) (RewardProof, error) {
	row, err := scanRewardProof(s.DB.QueryRow(`SELECT `+rewardProofCols+` FROM publish_reward_proofs WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return RewardProof{}, ErrNotFound
	}
	return row, err
}

// ReviewRewardProof records an audit and leaves the proof unconfirmed.
func (s *Store) ReviewRewardProof(tenantID, id, actor, basis string) (RewardProof, error) {
	res, err := s.DB.Exec(`UPDATE publish_reward_proofs
		SET status=?, review_actor=?, review_basis=?, audit_decision=?, reviewed_at=?,
		    platform_confirmed=0, granted=0, post_id=''
		WHERE id=? AND tenant_id=? AND status=?`,
		publishreward.StatusReviewedNotConfirmed, actor, basis, publishreward.DecisionNotPlatformConfirmed, now(),
		id, tenantID, publishreward.StatusPendingReview)
	if err != nil {
		return RewardProof{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return RewardProof{}, ErrNotFound
	}
	return s.GetRewardProof(tenantID, id)
}
