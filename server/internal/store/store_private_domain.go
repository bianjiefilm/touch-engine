package store

import (
	"database/sql"

	"github.com/bianjiefilm/touch-engine/server/internal/privatedomain"
)

// ReplacePrivateDomain stores the merchant's WeCom and community configuration.
// Public display is decided later by privatedomain.Present. This does not create
// a lead, a CRM contact, a reward, or a redemption.
func (s *Store) ReplacePrivateDomain(tenantID, campaignID string, items []privatedomain.Configured) error {
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM private_domain_entries WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID); err != nil {
		return err
	}
	for _, item := range items {
		enabled := 0
		if item.Enabled {
			enabled = 1
		}
		if _, err := tx.Exec(`INSERT INTO private_domain_entries(id,tenant_id,campaign_id,kind,href,enabled,updated_at)
			VALUES(?,?,?,?,?,?,?)`,
			newID("pd_"), tenantID, campaignID, string(item.Kind), item.Href, enabled, now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListPrivateDomain returns the stored configuration. Missing rows mean unconfigured.
func (s *Store) ListPrivateDomain(tenantID, campaignID string) ([]privatedomain.Configured, error) {
	rows, err := s.DB.Query(`SELECT kind,href,enabled FROM private_domain_entries WHERE tenant_id=? AND campaign_id=?`, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []privatedomain.Configured
	for rows.Next() {
		var item privatedomain.Configured
		var kind string
		var enabled int
		if err := rows.Scan(&kind, &item.Href, &enabled); err != nil {
			return nil, err
		}
		item.Kind = privatedomain.Kind(kind)
		item.Enabled = enabled == 1
		out = append(out, item)
	}
	return out, rows.Err()
}

// RecordPrivateDomainClick inserts one unknown click. The columns cannot store success.
func (s *Store) RecordPrivateDomainClick(tenantID, campaignID string, click privatedomain.Click) error {
	if !honestClick(click) {
		return sql.ErrNoRows
	}
	_, err := s.DB.Exec(`INSERT INTO private_domain_clicks(
		id,tenant_id,campaign_id,kind,event_name,recorded_as,success,platform_result,added,joined,contact_created,lead_created,crm_imported,reward_triggered,connected,redemption,followed,silent_add,forced_join,background_marketing,created_at
	) VALUES(?,?,?,?,?,'click',0,'unknown',0,0,0,0,0,0,0,'unknown',0,0,0,0,?)`,
		newID("pdc_"), tenantID, campaignID, string(click.Kind), click.Event, now())
	return err
}

func honestClick(click privatedomain.Click) bool {
	if click.RecordedAs != "click" || click.Success || click.PlatformResult != "unknown" || click.Redemption != privatedomain.RedemptionUnknown {
		return false
	}
	if click.Added || click.Joined || click.ContactCreated || click.LeadCreated || click.CRMImported || click.RewardTriggered ||
		click.Connected || click.Followed || click.SilentAdd || click.ForcedJoin || click.BackgroundMarketing {
		return false
	}
	switch click.Kind {
	case privatedomain.KindWecom:
		return click.Event == privatedomain.EventClickWecom
	case privatedomain.KindCommunity:
		return click.Event == privatedomain.EventClickCommunity
	default:
		return false
	}
}
