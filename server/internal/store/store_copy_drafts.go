package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrIdempotencyConflict means the same key was reused for a different snapshot.
var ErrIdempotencyConflict = errors.New("store: copy draft idempotency conflict")

// CopyDraft is one generated candidate. Payload is the JSON draft view.
type CopyDraft struct {
	ID             string
	TenantID       string
	CampaignID     string
	IdempotencyKey string
	SnapshotHash   string
	Payload        string
	CreatedBy      string
	CreatedAt      string
}

// CopyVersion is the merchant's selection of a draft. It does not publish.
type CopyVersion struct {
	ID         string
	TenantID   string
	CampaignID string
	DraftID    string
	Version    int
	Payload    string
	CreatedBy  string
	CreatedAt  string
}

func (s *Store) FindCopyDraftByKey(tenantID, campaignID, key string) (CopyDraft, error) {
	row := s.DB.QueryRow(
		`SELECT id,tenant_id,campaign_id,idempotency_key,snapshot_hash,payload,created_by,created_at
		 FROM copy_drafts WHERE tenant_id=? AND campaign_id=? AND idempotency_key=?`,
		tenantID, campaignID, key)
	return scanCopyDraft(row)
}

func (s *Store) GetCopyDraft(id, tenantID, campaignID string) (CopyDraft, error) {
	row := s.DB.QueryRow(
		`SELECT id,tenant_id,campaign_id,idempotency_key,snapshot_hash,payload,created_by,created_at
		 FROM copy_drafts WHERE id=? AND tenant_id=? AND campaign_id=?`,
		id, tenantID, campaignID)
	return scanCopyDraft(row)
}

func (s *Store) CountCopyDraftsSince(tenantID, since string) (int, error) {
	var n int
	err := s.DB.QueryRow(
		`SELECT COUNT(*) FROM copy_drafts WHERE tenant_id=? AND created_at>=?`,
		tenantID, since).Scan(&n)
	return n, err
}

func (s *Store) InsertCopyDraft(d CopyDraft) (CopyDraft, error) {
	if d.ID == "" {
		d.ID = newID("cdf_")
	}
	if d.CreatedAt == "" {
		d.CreatedAt = now()
	}
	_, err := s.DB.Exec(
		`INSERT INTO copy_drafts(id,tenant_id,campaign_id,idempotency_key,snapshot_hash,payload,created_by,created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		d.ID, d.TenantID, d.CampaignID, d.IdempotencyKey, d.SnapshotHash, d.Payload, d.CreatedBy, d.CreatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			existing, findErr := s.FindCopyDraftByKey(d.TenantID, d.CampaignID, d.IdempotencyKey)
			if findErr != nil {
				return CopyDraft{}, err
			}
			if existing.SnapshotHash != d.SnapshotHash {
				return CopyDraft{}, ErrIdempotencyConflict
			}
			return existing, nil
		}
		return CopyDraft{}, err
	}
	return d, nil
}

// AcceptCopyVersion inserts the next version for a draft. Selecting the same
// draft again returns the existing version. Campaign rows are not updated.
func (s *Store) AcceptCopyVersion(tenantID, campaignID, draftID, payload, createdBy string) (CopyVersion, bool, error) {
	draft, err := s.GetCopyDraft(draftID, tenantID, campaignID)
	if err != nil {
		return CopyVersion{}, false, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return CopyVersion{}, false, err
	}
	defer tx.Rollback()
	var existing CopyVersion
	err = tx.QueryRow(
		`SELECT id,tenant_id,campaign_id,draft_id,version,payload,created_by,created_at
		 FROM copy_versions WHERE campaign_id=? AND draft_id=?`, campaignID, draft.ID).Scan(
		&existing.ID, &existing.TenantID, &existing.CampaignID, &existing.DraftID,
		&existing.Version, &existing.Payload, &existing.CreatedBy, &existing.CreatedAt)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return CopyVersion{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CopyVersion{}, false, err
	}
	var maxVersion sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(version) FROM copy_versions WHERE campaign_id=?`, campaignID).Scan(&maxVersion); err != nil {
		return CopyVersion{}, false, err
	}
	ver := CopyVersion{
		ID: newID("cpv_"), TenantID: tenantID, CampaignID: campaignID, DraftID: draft.ID,
		Version: int(maxVersion.Int64) + 1, Payload: payload, CreatedBy: createdBy, CreatedAt: now(),
	}
	if _, err := tx.Exec(
		`INSERT INTO copy_versions(id,tenant_id,campaign_id,draft_id,version,payload,created_by,created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		ver.ID, ver.TenantID, ver.CampaignID, ver.DraftID, ver.Version, ver.Payload, ver.CreatedBy, ver.CreatedAt); err != nil {
		return CopyVersion{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CopyVersion{}, false, err
	}
	return ver, true, nil
}

func scanCopyDraft(sc interface{ Scan(...any) error }) (CopyDraft, error) {
	var d CopyDraft
	err := sc.Scan(&d.ID, &d.TenantID, &d.CampaignID, &d.IdempotencyKey, &d.SnapshotHash, &d.Payload, &d.CreatedBy, &d.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CopyDraft{}, ErrNotFound
	}
	return d, err
}

// CopySnapshotHash is the identity of one aligned snapshot.
func CopySnapshotHash(canonical string) string {
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
