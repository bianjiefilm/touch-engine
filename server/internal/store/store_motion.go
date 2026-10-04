package store

import (
	"database/sql"
	"errors"

	"github.com/bianjiefilm/touch-engine/server/internal/storemotion"
)

// StoreMotionRow is one recorded parameter request. Revision stays absent.
type StoreMotionRow struct {
	ID          string
	TenantID    string
	StoreID     string
	CampaignID  string
	Version     int
	Params      storemotion.Params
	Declaration storemotion.Declaration
	CreatedBy   string
	CreatedAt   string
}

// SaveStoreMotion records a parameter request. The same parameters return the
// existing row. A changed parameter inserts the next version. Neither path
// calls the probe.
func (s *Store) SaveStoreMotion(req storemotion.Request, createdBy string, probe *storemotion.Probe) (StoreMotionRow, bool, error) {
	params, err := storemotion.Normalize(req)
	if err != nil {
		return StoreMotionRow{}, false, err
	}
	req.Params = params
	decl, err := storemotion.Apply(req, probe)
	if err != nil {
		return StoreMotionRow{}, false, err
	}
	latest, err := s.LatestStoreMotion(req.TenantID, req.ActivityID)
	if err == nil && sameMotionParams(latest.Params, params) && latest.StoreID == req.StoreID {
		return latest, false, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return StoreMotionRow{}, false, err
	}
	version := 1
	if err == nil {
		version = latest.Version + 1
	}
	row := StoreMotionRow{
		ID: newID("smr_"), TenantID: req.TenantID, StoreID: req.StoreID, CampaignID: req.ActivityID,
		Version: version, Params: params, Declaration: decl, CreatedBy: createdBy, CreatedAt: now(),
	}
	_, err = s.DB.Exec(
		`INSERT INTO store_motion_requests(
			id,tenant_id,store_id,campaign_id,version,
			store_name,activity_time,price,address,offer_copy,cta,channels,aspect_ratios,
			origin_app,origin_context_ref,revision_id,verified,status,
			model_calls,render_calls,unchanged_store_rerun,unchanged_store_note,
			created_by,created_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,NULL,0,?,0,0,?,?,?,?)`,
		row.ID, row.TenantID, row.StoreID, row.CampaignID, row.Version,
		row.Params.StoreName, row.Params.ActivityTime, row.Params.Price, row.Params.Address, row.Params.OfferCopy, row.Params.CTA, row.Params.Channels, row.Params.AspectRatios,
		row.Declaration.OriginApp, row.Declaration.OriginContextRef, row.Declaration.Status,
		row.Declaration.UnchangedStoreRerun, row.Declaration.UnchangedStoreNote,
		row.CreatedBy, row.CreatedAt)
	if err != nil {
		return StoreMotionRow{}, false, err
	}
	return row, true, nil
}

// LatestStoreMotion returns the newest recorded request for one activity.
func (s *Store) LatestStoreMotion(tenantID, campaignID string) (StoreMotionRow, error) {
	row := s.DB.QueryRow(
		`SELECT id,tenant_id,store_id,campaign_id,version,
		store_name,activity_time,price,address,offer_copy,cta,channels,aspect_ratios,
		origin_app,origin_context_ref,revision_id,verified,status,
		model_calls,render_calls,unchanged_store_rerun,unchanged_store_note,
		created_by,created_at
		 FROM store_motion_requests
		 WHERE tenant_id=? AND campaign_id=?
		 ORDER BY version DESC LIMIT 1`, tenantID, campaignID)
	return scanStoreMotion(row)
}

func scanStoreMotion(sc interface{ Scan(...any) error }) (StoreMotionRow, error) {
	var row StoreMotionRow
	var revision sql.NullString
	var verified, modelCalls, renderCalls int
	err := sc.Scan(
		&row.ID, &row.TenantID, &row.StoreID, &row.CampaignID, &row.Version,
		&row.Params.StoreName, &row.Params.ActivityTime, &row.Params.Price, &row.Params.Address, &row.Params.OfferCopy, &row.Params.CTA, &row.Params.Channels, &row.Params.AspectRatios,
		&row.Declaration.OriginApp, &row.Declaration.OriginContextRef, &revision, &verified, &row.Declaration.Status,
		&modelCalls, &renderCalls, &row.Declaration.UnchangedStoreRerun, &row.Declaration.UnchangedStoreNote,
		&row.CreatedBy, &row.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return StoreMotionRow{}, ErrNotFound
	}
	if err != nil {
		return StoreMotionRow{}, err
	}
	if revision.Valid || verified != 0 || modelCalls != 0 || renderCalls != 0 {
		return StoreMotionRow{}, errors.New("store: motion row claims a render or a revision")
	}
	if row.Declaration.OriginApp != storemotion.OriginApp ||
		row.Declaration.Status != storemotion.StatusNotRendered ||
		row.Declaration.UnchangedStoreRerun != storemotion.RerunNotClaimed {
		return StoreMotionRow{}, errors.New("store: motion row is not the unverified declaration")
	}
	row.Declaration.RevisionID = nil
	row.Declaration.Verified = false
	row.Declaration.ModelCalls = 0
	row.Declaration.RenderCalls = 0
	return row, nil
}

func sameMotionParams(a, b storemotion.Params) bool {
	return a == b
}
