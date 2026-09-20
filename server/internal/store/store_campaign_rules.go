// store_campaign_rules.go: HUI-1676 FEAT-0177 活动规则持久层(additive)。
//
// 纪律:
//   - 规则集挂在既有 campaigns 维度上(UNIQUE campaign_id,行级租户隔离);
//     写入前先在租户内核对活动存在,跨租户一律 ErrNotFound(绝不泄露存在性);
//   - 变更不静默改写:UpsertCampaignRules / DeleteCampaignRules 在同一事务内
//     落规则行与修订快照(campaign_rule_revisions),历史版本可查;
//   - 存储层复用 campaignrules.Validate 做第二道校验(HTTP 层已校验一次);
//   - 非法值被拒时不留任何半条记录(修订与规则行同事务回滚)。
package store

import (
	"database/sql"
	"errors"

	"github.com/bianjiefilm/touch-engine/server/internal/campaignrules"
)

// CampaignRule is the active per-campaign ruleset row plus its version.
type CampaignRule struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	CampaignID string `json:"campaign_id"`
	Ruleset    campaignrules.Ruleset
	Version    int    `json:"version"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// CampaignRuleRevision is one immutable snapshot in the rule change trail.
type CampaignRuleRevision struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenant_id"`
	CampaignID string `json:"campaign_id"`
	Revision   int    `json:"revision"`
	Action     string `json:"action"` // create | update | delete
	Ruleset    campaignrules.Ruleset
	ChangedBy  string `json:"changed_by"`
	CreatedAt  string `json:"created_at"`
}

const ruleCols = `id,tenant_id,campaign_id,reward_threshold,daily_publish_limit,` +
	`duplicate_publish_window_hours,per_contact_daily_submission_cap,version,created_by,created_at,updated_at`

func scanRuleset(sc interface{ Scan(...any) error }) (campaignrules.Ruleset, error) {
	var rs campaignrules.Ruleset
	var reward, daily, window, cap sql.NullInt64
	err := sc.Scan(&reward, &daily, &window, &cap)
	if err != nil {
		return campaignrules.Ruleset{}, err
	}
	nullInt := func(v sql.NullInt64) *int {
		if !v.Valid {
			return nil
		}
		n := int(v.Int64)
		return &n
	}
	rs.RewardThreshold = nullInt(reward)
	rs.DailyPublishLimit = nullInt(daily)
	rs.DuplicatePublishWindowHours = nullInt(window)
	rs.PerContactDailySubmissionCap = nullInt(cap)
	return rs, nil
}

func scanCampaignRule(sc interface{ Scan(...any) error }) (CampaignRule, error) {
	var r CampaignRule
	var rs campaignrules.Ruleset
	var reward, daily, window, cap sql.NullInt64
	err := sc.Scan(&r.ID, &r.TenantID, &r.CampaignID, &reward, &daily, &window, &cap,
		&r.Version, &r.CreatedBy, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return CampaignRule{}, err
	}
	nullInt := func(v sql.NullInt64) *int {
		if !v.Valid {
			return nil
		}
		n := int(v.Int64)
		return &n
	}
	rs.RewardThreshold = nullInt(reward)
	rs.DailyPublishLimit = nullInt(daily)
	rs.DuplicatePublishWindowHours = nullInt(window)
	rs.PerContactDailySubmissionCap = nullInt(cap)
	r.Ruleset = rs
	return r, nil
}

// ruleVals flattens a ruleset into nullable SQL values (insert/update order).
func ruleVals(rs campaignrules.Ruleset) (any, any, any, any) {
	nullInt := func(p *int) any {
		if p == nil {
			return nil
		}
		return *p
	}
	return nullInt(rs.RewardThreshold), nullInt(rs.DailyPublishLimit),
		nullInt(rs.DuplicatePublishWindowHours), nullInt(rs.PerContactDailySubmissionCap)
}

// UpsertCampaignRules creates (version 1) or replaces (version+1) the active
// ruleset of a campaign, recording a full-snapshot revision in the SAME
// transaction. The campaign must exist in the given tenant.
func (s *Store) UpsertCampaignRules(tenantID, campaignID string, rs campaignrules.Ruleset, changedBy string) (CampaignRule, error) {
	if err := campaignrules.Validate(rs); err != nil {
		return CampaignRule{}, err
	}
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return CampaignRule{}, err
	}
	reward, daily, window, cap := ruleVals(rs)

	tx, err := s.DB.Begin()
	if err != nil {
		return CampaignRule{}, err
	}
	defer func() { _ = tx.Rollback() }()

	cur, err := scanCampaignRule(tx.QueryRow(
		`SELECT `+ruleCols+` FROM campaign_rules WHERE campaign_id=? AND tenant_id=?`, campaignID, tenantID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		r := CampaignRule{
			ID: newID("crul_"), TenantID: tenantID, CampaignID: campaignID,
			Ruleset: rs, Version: 1, CreatedBy: changedBy, CreatedAt: now(), UpdatedAt: now(),
		}
		if _, err := tx.Exec(
			`INSERT INTO campaign_rules(`+ruleCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			r.ID, r.TenantID, r.CampaignID, reward, daily, window, cap,
			r.Version, r.CreatedBy, r.CreatedAt, r.UpdatedAt); err != nil {
			return CampaignRule{}, err
		}
		if err := insertRuleRevision(tx, tenantID, campaignID, 1, "create", rs, changedBy); err != nil {
			return CampaignRule{}, err
		}
		if err := tx.Commit(); err != nil {
			return CampaignRule{}, err
		}
		return r, nil
	case err != nil:
		return CampaignRule{}, err
	}
	next := cur.Version + 1
	ts := now()
	if _, err := tx.Exec(
		`UPDATE campaign_rules SET reward_threshold=?,daily_publish_limit=?,`+
			`duplicate_publish_window_hours=?,per_contact_daily_submission_cap=?,`+
			`version=?,updated_at=? WHERE id=? AND tenant_id=?`,
		reward, daily, window, cap, next, ts, cur.ID, tenantID); err != nil {
		return CampaignRule{}, err
	}
	if err := insertRuleRevision(tx, tenantID, campaignID, next, "update", rs, changedBy); err != nil {
		return CampaignRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return CampaignRule{}, err
	}
	return s.GetCampaignRules(tenantID, campaignID)
}

// DeleteCampaignRules removes the active ruleset and records a delete revision
// (snapshot = all-nil). Deleting absent rules is ErrNotFound.
func (s *Store) DeleteCampaignRules(tenantID, campaignID, changedBy string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cur, err := scanCampaignRule(tx.QueryRow(
		`SELECT `+ruleCols+` FROM campaign_rules WHERE campaign_id=? AND tenant_id=?`, campaignID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	next := cur.Version + 1
	if _, err := tx.Exec(`DELETE FROM campaign_rules WHERE id=? AND tenant_id=?`, cur.ID, tenantID); err != nil {
		return err
	}
	if err := insertRuleRevision(tx, tenantID, campaignID, next, "delete", campaignrules.Ruleset{}, changedBy); err != nil {
		return err
	}
	return tx.Commit()
}

func insertRuleRevision(tx *sql.Tx, tenantID, campaignID string, revision int, action string, rs campaignrules.Ruleset, changedBy string) error {
	reward, daily, window, cap := ruleVals(rs)
	_, err := tx.Exec(
		`INSERT INTO campaign_rule_revisions(id,tenant_id,campaign_id,revision,action,`+
			`reward_threshold,daily_publish_limit,duplicate_publish_window_hours,`+
			`per_contact_daily_submission_cap,changed_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		newID("crv_"), tenantID, campaignID, revision, action,
		reward, daily, window, cap, changedBy, now())
	return err
}

// GetCampaignRules is the tenant-scoped admin lookup.
func (s *Store) GetCampaignRules(tenantID, campaignID string) (CampaignRule, error) {
	r, err := scanCampaignRule(s.DB.QueryRow(
		`SELECT `+ruleCols+` FROM campaign_rules WHERE campaign_id=? AND tenant_id=?`, campaignID, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return CampaignRule{}, ErrNotFound
	}
	return r, err
}

// GetCampaignRulesByCampaign is the server-internal evaluation lookup (the
// campaign has already been resolved through a controlled path, so no tenant
// parameter is needed — same discipline as GetLeadFormByCampaign).
func (s *Store) GetCampaignRulesByCampaign(campaignID string) (CampaignRule, error) {
	r, err := scanCampaignRule(s.DB.QueryRow(
		`SELECT `+ruleCols+` FROM campaign_rules WHERE campaign_id=?`, campaignID))
	if errors.Is(err, sql.ErrNoRows) {
		return CampaignRule{}, ErrNotFound
	}
	return r, err
}

// ListCampaignRuleRevisions returns the immutable change trail oldest-first.
func (s *Store) ListCampaignRuleRevisions(tenantID, campaignID string) ([]CampaignRuleRevision, error) {
	rows, err := s.DB.Query(
		`SELECT id,tenant_id,campaign_id,revision,action,reward_threshold,daily_publish_limit,`+
			`duplicate_publish_window_hours,per_contact_daily_submission_cap,changed_by,created_at `+
			`FROM campaign_rule_revisions WHERE tenant_id=? AND campaign_id=? ORDER BY revision`,
		tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]CampaignRuleRevision, 0)
	for rows.Next() {
		var r CampaignRuleRevision
		var reward, daily, window, cap sql.NullInt64
		if err := rows.Scan(&r.ID, &r.TenantID, &r.CampaignID, &r.Revision, &r.Action,
			&reward, &daily, &window, &cap, &r.ChangedBy, &r.CreatedAt); err != nil {
			return nil, err
		}
		nullInt := func(v sql.NullInt64) *int {
			if !v.Valid {
				return nil
			}
			n := int(v.Int64)
			return &n
		}
		r.Ruleset = campaignrules.Ruleset{
			RewardThreshold:              nullInt(reward),
			DailyPublishLimit:            nullInt(daily),
			DuplicatePublishWindowHours:  nullInt(window),
			PerContactDailySubmissionCap: nullInt(cap),
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
