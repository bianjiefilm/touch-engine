package store

import (
	"database/sql"

	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
)

// ReplaceExtraJumps stores the merchant's jump configuration for one campaign.
// Public display is decided later by extrajump.Present; this does not create
// a lead, a CRM row, a reward, or a publish fact.
func (s *Store) ReplaceExtraJumps(tenantID, campaignID string, items []extrajump.Configured) error {
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM extra_jump_actions WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID); err != nil {
		return err
	}
	for _, item := range items {
		enabled := 0
		if item.Enabled {
			enabled = 1
		}
		if _, err := tx.Exec(`INSERT INTO extra_jump_actions(id,tenant_id,campaign_id,kind,href,enabled,updated_at)
			VALUES(?,?,?,?,?,?,?)`,
			newID("ej_"), tenantID, campaignID, string(item.Kind), item.Href, enabled, now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListExtraJumps returns the stored configuration. Missing rows mean unconfigured.
func (s *Store) ListExtraJumps(tenantID, campaignID string) ([]extrajump.Configured, error) {
	rows, err := s.DB.Query(`SELECT kind,href,enabled FROM extra_jump_actions WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []extrajump.Configured
	for rows.Next() {
		var item extrajump.Configured
		var kind string
		var enabled int
		if err := rows.Scan(&kind, &item.Href, &enabled); err != nil {
			return nil, err
		}
		item.Kind = extrajump.Kind(kind)
		item.Enabled = enabled == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

// RecordExtraJumpClick inserts one unknown click. The columns cannot store success.
func (s *Store) RecordExtraJumpClick(tenantID, campaignID string, click extrajump.Click) error {
	if click.RecordedAs != "click" || click.Success || click.PlatformResult != "unknown" ||
		click.Added || click.Followed || click.LeadCreated || click.CRMImported || click.RewardTriggered || click.PublishSuccess {
		return sql.ErrNoRows
	}
	_, err := s.DB.Exec(`INSERT INTO extra_jump_clicks(
		id,tenant_id,campaign_id,kind,recorded_as,success,platform_result,added,followed,lead_created,crm_imported,reward_triggered,publish_success,created_at
	) VALUES(?,?,?,?,'click',0,'unknown',0,0,0,0,0,0,?)`,
		newID("ejc_"), tenantID, campaignID, string(click.Kind), now())
	return err
}
