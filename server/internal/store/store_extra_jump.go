package store

import (
	"database/sql"
	"errors"

	"github.com/bianjiefilm/touch-engine/server/internal/extrajump"
)

// SaveJumpMatrix replaces the jump rows when items is non-nil, and upserts the
// authorized return when ret is non-nil. A nil pointer leaves that half as it
// was. This does not create a lead, a CRM row, a reward, or a publish fact.
func (s *Store) SaveJumpMatrix(tenantID, campaignID string, items *[]extrajump.Configured, ret *extrajump.ReturnConfig) error {
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if items != nil {
		if _, err := tx.Exec(`DELETE FROM extra_jump_actions WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID); err != nil {
			return err
		}
		for _, item := range *items {
			enabled, revoked := 0, 0
			if item.Enabled {
				enabled = 1
			}
			if item.Revoked {
				revoked = 1
			}
			if _, err := tx.Exec(`INSERT INTO extra_jump_actions(id,tenant_id,campaign_id,kind,href,enabled,revoked,expires_at,updated_at)
				VALUES(?,?,?,?,?,?,?,?,?)`,
				newID("ej_"), tenantID, campaignID, string(item.Kind), item.Href, enabled, revoked, item.ExpiresAt, now()); err != nil {
				return err
			}
		}
	}
	if ret != nil {
		enabled, revoked := 0, 0
		if ret.Enabled {
			enabled = 1
		}
		if ret.Revoked {
			revoked = 1
		}
		if _, err := tx.Exec(`INSERT INTO authorized_returns(id,tenant_id,campaign_id,href,enabled,revoked,expires_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?)
			ON CONFLICT(campaign_id) DO UPDATE SET
				tenant_id=excluded.tenant_id,
				href=excluded.href,
				enabled=excluded.enabled,
				revoked=excluded.revoked,
				expires_at=excluded.expires_at,
				updated_at=excluded.updated_at`,
			newID("ar_"), tenantID, campaignID, ret.Href, enabled, revoked, ret.ExpiresAt, now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceExtraJumps stores the merchant's jump configuration for one campaign.
// The authorized return is left untouched.
func (s *Store) ReplaceExtraJumps(tenantID, campaignID string, items []extrajump.Configured) error {
	return s.SaveJumpMatrix(tenantID, campaignID, &items, nil)
}

// ListExtraJumps returns the stored configuration. Missing rows mean unconfigured.
func (s *Store) ListExtraJumps(tenantID, campaignID string) ([]extrajump.Configured, error) {
	rows, err := s.DB.Query(`SELECT kind,href,enabled,revoked,expires_at FROM extra_jump_actions WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []extrajump.Configured
	for rows.Next() {
		var item extrajump.Configured
		var kind string
		var enabled, revoked int
		if err := rows.Scan(&kind, &item.Href, &enabled, &revoked, &item.ExpiresAt); err != nil {
			return nil, err
		}
		item.Kind = extrajump.Kind(kind)
		item.Enabled = enabled == 1
		item.Revoked = revoked == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

// GetAuthorizedReturn returns the stored return. No row is an empty config.
func (s *Store) GetAuthorizedReturn(tenantID, campaignID string) (extrajump.ReturnConfig, error) {
	var cfg extrajump.ReturnConfig
	var enabled, revoked int
	err := s.DB.QueryRow(`SELECT href,enabled,revoked,expires_at FROM authorized_returns WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID).
		Scan(&cfg.Href, &enabled, &revoked, &cfg.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return extrajump.ReturnConfig{}, nil
	}
	if err != nil {
		return extrajump.ReturnConfig{}, err
	}
	cfg.Enabled = enabled == 1
	cfg.Revoked = revoked == 1
	return cfg, nil
}

// CampaignPublishedHosts lists brand hosts already stamped on this campaign's links.
func (s *Store) CampaignPublishedHosts(tenantID, campaignID string) ([]string, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT published_host FROM campaign_links WHERE tenant_id=? AND campaign_id=? AND published_host<>''`, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var host string
		if err := rows.Scan(&host); err != nil {
			return nil, err
		}
		out = append(out, host)
	}
	return out, rows.Err()
}

// RecordExtraJumpClick inserts one unknown click. The columns cannot store success.
func (s *Store) RecordExtraJumpClick(tenantID, campaignID string, click extrajump.Click) error {
	if click.RecordedAs != "click" || click.Success || click.PlatformResult != "unknown" ||
		click.Added || click.Followed || click.LeadCreated || click.CRMImported || click.RewardTriggered || click.PublishSuccess ||
		click.AutoFollow || click.AutoJoin || click.AutoPay || click.ClientLaunch != extrajump.EvidenceClientUnverified ||
		click.PlatformAction != extrajump.EvidencePlatformUnknown {
		return sql.ErrNoRows
	}
	_, err := s.DB.Exec(`INSERT INTO extra_jump_clicks(
		id,tenant_id,campaign_id,kind,recorded_as,success,platform_result,added,followed,lead_created,crm_imported,reward_triggered,publish_success,created_at
	) VALUES(?,?,?,?,'click',0,'unknown',0,0,0,0,0,0,?)`,
		newID("ejc_"), tenantID, campaignID, string(click.Kind), now())
	return err
}

// RecordAuthorizedReturnClick inserts one unknown return click.
func (s *Store) RecordAuthorizedReturnClick(tenantID, campaignID string) error {
	_, err := s.DB.Exec(`INSERT INTO authorized_return_clicks(
		id,tenant_id,campaign_id,recorded_as,success,platform_result,created_at
	) VALUES(?,? ,?,'click',0,'unknown',?)`,
		newID("arc_"), tenantID, campaignID, now())
	return err
}
