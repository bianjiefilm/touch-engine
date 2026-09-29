package store

import (
	"database/sql"
	"errors"
	"strings"
)

// CopyJobRow is the persisted copy state machine. Payload is the job JSON.
type CopyJobRow struct {
	ID             string
	TenantID       string
	CampaignID     string
	IdempotencyKey string
	SnapshotHash   string
	State          string
	ChargeCount    int
	Payload        string
	CreatedBy      string
	CreatedAt      string
	UpdatedAt      string
}

// SavedCopy is the explicit campaign save. It does not change campaign status.
type SavedCopy struct {
	CampaignID   string
	TenantID     string
	TitleWritten bool
	IntroWritten bool
	Topics       string
	SourceJobID  string
	ModelID      string
	ModelVersion string
	SourceHash   string
}

func (s *Store) FindCopyJobByKey(tenantID, campaignID, key string) (CopyJobRow, error) {
	row := s.DB.QueryRow(
		`SELECT id,tenant_id,campaign_id,idempotency_key,snapshot_hash,state,charge_count,payload,created_by,created_at,updated_at
		 FROM copy_jobs WHERE tenant_id=? AND campaign_id=? AND idempotency_key=?`,
		tenantID, campaignID, key)
	return scanCopyJob(row)
}

func (s *Store) GetCopyJob(id, tenantID, campaignID string) (CopyJobRow, error) {
	row := s.DB.QueryRow(
		`SELECT id,tenant_id,campaign_id,idempotency_key,snapshot_hash,state,charge_count,payload,created_by,created_at,updated_at
		 FROM copy_jobs WHERE id=? AND tenant_id=? AND campaign_id=?`,
		id, tenantID, campaignID)
	return scanCopyJob(row)
}

func (s *Store) InsertCopyJob(row CopyJobRow) (CopyJobRow, error) {
	if row.ID == "" {
		row.ID = newID("cpj_")
	}
	if row.CreatedAt == "" {
		row.CreatedAt = now()
	}
	if row.UpdatedAt == "" {
		row.UpdatedAt = row.CreatedAt
	}
	_, err := s.DB.Exec(
		`INSERT INTO copy_jobs(id,tenant_id,campaign_id,idempotency_key,snapshot_hash,state,charge_count,payload,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		row.ID, row.TenantID, row.CampaignID, row.IdempotencyKey, row.SnapshotHash, row.State, row.ChargeCount,
		row.Payload, row.CreatedBy, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			existing, findErr := s.FindCopyJobByKey(row.TenantID, row.CampaignID, row.IdempotencyKey)
			if findErr != nil {
				return CopyJobRow{}, err
			}
			if existing.SnapshotHash != row.SnapshotHash {
				return CopyJobRow{}, ErrIdempotencyConflict
			}
			return existing, nil
		}
		return CopyJobRow{}, err
	}
	return row, nil
}

func (s *Store) UpdateCopyJob(row CopyJobRow) error {
	row.UpdatedAt = now()
	res, err := s.DB.Exec(
		`UPDATE copy_jobs SET state=?, charge_count=?, snapshot_hash=?, payload=?, updated_at=? WHERE id=? AND tenant_id=?`,
		row.State, row.ChargeCount, row.SnapshotHash, row.Payload, row.UpdatedAt, row.ID, row.TenantID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SwapCopyJob updates a job only while it is still in fromState.
func (s *Store) SwapCopyJob(id, tenantID, fromState string, row CopyJobRow) (bool, error) {
	row.UpdatedAt = now()
	res, err := s.DB.Exec(
		`UPDATE copy_jobs SET state=?, charge_count=?, snapshot_hash=?, payload=?, updated_at=?
		 WHERE id=? AND tenant_id=? AND state=?`,
		row.State, row.ChargeCount, row.SnapshotHash, row.Payload, row.UpdatedAt, id, tenantID, fromState)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (s *Store) UpsertSavedCopy(c SavedCopy) error {
	title, intro := 0, 0
	if c.TitleWritten {
		title = 1
	}
	if c.IntroWritten {
		intro = 1
	}
	if c.Topics == "" {
		c.Topics = "[]"
	}
	_, err := s.DB.Exec(
		`INSERT INTO campaign_saved_copy(campaign_id,tenant_id,title_written,intro_written,topics,source_job_id,model_id,model_version,source_hash,saved_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)
		 ON CONFLICT(campaign_id) DO UPDATE SET
		   title_written=excluded.title_written,
		   intro_written=excluded.intro_written,
		   topics=excluded.topics,
		   source_job_id=excluded.source_job_id,
		   model_id=excluded.model_id,
		   model_version=excluded.model_version,
		   source_hash=excluded.source_hash,
		   saved_at=excluded.saved_at`,
		c.CampaignID, c.TenantID, title, intro, c.Topics, c.SourceJobID, c.ModelID, c.ModelVersion, c.SourceHash, now())
	return err
}

func scanCopyJob(sc interface{ Scan(...any) error }) (CopyJobRow, error) {
	var row CopyJobRow
	err := sc.Scan(&row.ID, &row.TenantID, &row.CampaignID, &row.IdempotencyKey, &row.SnapshotHash, &row.State, &row.ChargeCount, &row.Payload, &row.CreatedBy, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CopyJobRow{}, ErrNotFound
	}
	return row, err
}
