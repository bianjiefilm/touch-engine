package store

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
)

// CustomerPublishAttempt is a stored customer preparation. The schema refuses
// platform receipts, outbound calls, and reward issuance.
type CustomerPublishAttempt struct {
	ID                      string
	TenantID                string
	CampaignID              string
	LinkCode                string
	Platform                string
	Publisher               string
	Copy                    string
	AccountLabel            string
	ContentVersion          string
	Status                  string
	AssetUseAccepted        bool
	ConfirmedContentVersion string
	ConfirmedAccountLabel   string
	SelfReported            bool
	PlatformPostID          string
	ReceiptSource           string
	OutboundCalls           int
	QueryCount              int
	RewardTriggered         bool
	CreatedAt               string
	UpdatedAt               string
}

// DomainAttempt is the pure gate value for this row.
func (row CustomerPublishAttempt) DomainAttempt() custpublish.Attempt {
	return custpublish.Attempt{
		Platform: custpublish.Platform(row.Platform), Publisher: row.Publisher,
		Copy: row.Copy, AccountLabel: row.AccountLabel, ContentVersion: row.ContentVersion,
		Status: row.Status, AssetUseAccepted: row.AssetUseAccepted,
		ConfirmedContentVersion: row.ConfirmedContentVersion, ConfirmedAccountLabel: row.ConfirmedAccountLabel,
		SelfReported: row.SelfReported, PlatformPostID: row.PlatformPostID, ReceiptSource: row.ReceiptSource,
		OutboundCalls: row.OutboundCalls, QueryCount: row.QueryCount, RewardTriggered: row.RewardTriggered,
	}
}

const customerPublishCols = `id,tenant_id,campaign_id,link_code,platform,publisher_subject,copy_text,content_version,account_label,status,asset_use_accepted,confirmed_content_version,confirmed_account_label,self_reported,platform_post_id,receipt_source,outbound_calls,query_count,reward_triggered,created_at,updated_at`

func scanCustomerPublish(sc interface{ Scan(...any) error }) (CustomerPublishAttempt, error) {
	var row CustomerPublishAttempt
	var asset, self, reward int
	err := sc.Scan(&row.ID, &row.TenantID, &row.CampaignID, &row.LinkCode, &row.Platform, &row.Publisher,
		&row.Copy, &row.ContentVersion, &row.AccountLabel, &row.Status, &asset, &row.ConfirmedContentVersion,
		&row.ConfirmedAccountLabel, &self, &row.PlatformPostID, &row.ReceiptSource, &row.OutboundCalls,
		&row.QueryCount, &reward, &row.CreatedAt, &row.UpdatedAt)
	row.AssetUseAccepted = asset == 1
	row.SelfReported = self == 1
	row.RewardTriggered = reward == 1
	return row, err
}

// CreateCustomerPublishPreview inserts a local preview. Receipts and outbound
// counters are not accepted from the caller.
func (s *Store) CreateCustomerPublishPreview(tenantID, campaignID, linkCode string, a custpublish.Attempt) (CustomerPublishAttempt, error) {
	if a.Publisher != custpublish.PublisherActivityCustomer || a.Status != custpublish.StatusPreviewed || a.PlatformPostID != "" {
		return CustomerPublishAttempt{}, errors.New("store: refusing to persist a publish receipt")
	}
	if _, err := s.GetCampaign(campaignID, tenantID); err != nil {
		return CustomerPublishAttempt{}, err
	}
	row := CustomerPublishAttempt{
		ID: newID("cpa_"), TenantID: tenantID, CampaignID: campaignID, LinkCode: linkCode,
		Platform: string(a.Platform), Publisher: a.Publisher, Copy: a.Copy, AccountLabel: a.AccountLabel,
		ContentVersion: a.ContentVersion, Status: custpublish.StatusPreviewed,
		CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.DB.Exec(`INSERT INTO customer_publish_attempts(
		id,tenant_id,campaign_id,link_code,platform,publisher_subject,copy_text,content_version,account_label,status,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.ID, row.TenantID, row.CampaignID, row.LinkCode, row.Platform, row.Publisher,
		row.Copy, row.ContentVersion, row.AccountLabel, row.Status, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return CustomerPublishAttempt{}, err
	}
	return row, nil
}

// GetCustomerPublish loads one attempt inside the tenant. Other tenants look
// like ErrNotFound.
func (s *Store) GetCustomerPublish(id, tenantID string) (CustomerPublishAttempt, error) {
	row, err := scanCustomerPublish(s.DB.QueryRow(`SELECT `+customerPublishCols+` FROM customer_publish_attempts WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return CustomerPublishAttempt{}, ErrNotFound
	}
	return row, err
}

// MarkCustomerPublishExported moves a preview to exported and clears any post id.
func (s *Store) MarkCustomerPublishExported(id, tenantID string) (CustomerPublishAttempt, error) {
	res, err := s.DB.Exec(`UPDATE customer_publish_attempts
		SET status=?, platform_post_id='', receipt_source='', updated_at=?
		WHERE id=? AND tenant_id=? AND status IN (?, ?)`,
		custpublish.StatusExported, now(), id, tenantID, custpublish.StatusPreviewed, custpublish.StatusExported)
	if err != nil {
		return CustomerPublishAttempt{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return CustomerPublishAttempt{}, ErrNotFound
	}
	return s.GetCustomerPublish(id, tenantID)
}

// MarkCustomerPublishSelfReport records the customer's own claim and drops
// whatever post id they typed.
func (s *Store) MarkCustomerPublishSelfReport(id, tenantID, _ string) (CustomerPublishAttempt, error) {
	res, err := s.DB.Exec(`UPDATE customer_publish_attempts
		SET self_reported=1, platform_post_id='', receipt_source='', reward_triggered=0, updated_at=?
		WHERE id=? AND tenant_id=?`, now(), id, tenantID)
	if err != nil {
		return CustomerPublishAttempt{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return CustomerPublishAttempt{}, ErrNotFound
	}
	return s.GetCustomerPublish(id, tenantID)
}

// CountOfficialCustomerPublishes counts rows that would be verifiable platform
// publishes. The table constraints make that count stay at zero in this ticket.
func (s *Store) CountOfficialCustomerPublishes(tenantID, storeScope string) (int64, error) {
	q := `SELECT COUNT(*) FROM customer_publish_attempts a
		JOIN campaigns c ON c.id=a.campaign_id
		WHERE a.tenant_id=? AND c.tenant_id=?
		  AND a.status=? AND a.receipt_source=? AND a.platform_post_id<>''
		  AND a.publisher_subject=?`
	args := []any{tenantID, tenantID, custpublish.StatusPublishConfirmed, custpublish.ReceiptOfficialQuery, custpublish.PublisherActivityCustomer}
	if storeScope != "" {
		q += ` AND c.store_id=?`
		args = append(args, storeScope)
	}
	var n int64
	err := s.DB.QueryRow(q, args...).Scan(&n)
	return n, err
}

// SaveCustomerPublishConfirmation stores explicit consent without a platform receipt.
func (s *Store) SaveCustomerPublishConfirmation(id, tenantID, contentVersion, accountLabel string) (CustomerPublishAttempt, error) {
	res, err := s.DB.Exec(`UPDATE customer_publish_attempts
		SET asset_use_accepted=1, confirmed_content_version=?, confirmed_account_label=?,
		    platform_post_id='', receipt_source='', reward_triggered=0, updated_at=?
		WHERE id=? AND tenant_id=? AND content_version=? AND account_label=?`,
		contentVersion, accountLabel, now(), id, tenantID, contentVersion, accountLabel)
	if err != nil {
		return CustomerPublishAttempt{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return CustomerPublishAttempt{}, ErrNotFound
	}
	return s.GetCustomerPublish(id, tenantID)
}

// ReviseCustomerPublish stores a new copy or account and drops stale consent.
func (s *Store) ReviseCustomerPublish(id, tenantID string, a custpublish.Attempt) (CustomerPublishAttempt, error) {
	res, err := s.DB.Exec(`UPDATE customer_publish_attempts
		SET copy_text=?, content_version=?, account_label=?, status=?,
		    asset_use_accepted=0, confirmed_content_version='', confirmed_account_label='',
		    platform_post_id='', receipt_source='', reward_triggered=0, updated_at=?
		WHERE id=? AND tenant_id=?`,
		a.Copy, a.ContentVersion, a.AccountLabel, a.Status, now(), id, tenantID)
	if err != nil {
		return CustomerPublishAttempt{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return CustomerPublishAttempt{}, ErrNotFound
	}
	return s.GetCustomerPublish(id, tenantID)
}

// NotePublishAdapter records an adapter name. It has no column that enables a platform.
func (s *Store) NotePublishAdapter(tenantID, platform, name string) error {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(platform) == "" {
		return errors.New("store: adapter note requires platform and name")
	}
	_, err := s.DB.Exec(`INSERT INTO publish_adapter_notes(id,tenant_id,platform,adapter_name,created_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(tenant_id, platform, adapter_name) DO NOTHING`,
		newID("pan_"), tenantID, platform, name, now())
	return err
}

// ListPublishAdapterNotes returns registration notes for the tenant.
func (s *Store) ListPublishAdapterNotes(tenantID string) ([]custpublish.AdapterNote, error) {
	rows, err := s.DB.Query(`SELECT platform, adapter_name FROM publish_adapter_notes WHERE tenant_id=? ORDER BY created_at, adapter_name`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]custpublish.AdapterNote, 0)
	for rows.Next() {
		var n custpublish.AdapterNote
		if err := rows.Scan(&n.Platform, &n.Name); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
