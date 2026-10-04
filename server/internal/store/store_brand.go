package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Lifecycle values for a touch tenant. Brand status is not stored here.
const (
	LifecycleActive         = "active"
	LifecycleSuspended      = "suspended"
	LifecycleSecurityFreeze = "security_freeze"
	LifecycleOffboarding    = "offboarding"
	LifecycleRetired        = "retired"
)

// LinkPublication is the brand/host captured when a short code was minted.
// Empty brand means a pre-WL code: it keeps resolving.
type LinkPublication struct {
	BrandID string
	Host    string
}

// LinkStamp is the mint-time brand/host stamp on one short code, plus the
// code itself. The motion handoff brief reads these; nothing writes here.
type LinkStamp struct {
	LinkID           string
	Code             string
	Enabled          bool
	PublishedBrandID string
	PublishedHost    string
}

// ListLinkStamps returns the campaign's short codes with their mint-time
// brand stamps, in mint order (created_at then rowid, both stable). Missing
// stamps are empty strings.
func (s *Store) ListLinkStamps(tenantID, campaignID string) ([]LinkStamp, error) {
	rows, err := s.DB.Query(
		`SELECT id,code,enabled,published_brand_id,published_host
		 FROM campaign_links
		 WHERE tenant_id=? AND campaign_id=?
		 ORDER BY created_at, rowid`, tenantID, campaignID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LinkStamp{}
	for rows.Next() {
		var st LinkStamp
		var enabled int
		if err := rows.Scan(&st.LinkID, &st.Code, &enabled, &st.PublishedBrandID, &st.PublishedHost); err != nil {
			return nil, err
		}
		st.Enabled = enabled == 1
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) BindTenantBrand(tenantID, brandID string) error {
	res, err := s.DB.Exec(`UPDATE tenants SET brand_id=? WHERE id=?`, brandID, tenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetTenantLifecycle(tenantID, lifecycle string) error {
	switch lifecycle {
	case LifecycleActive, LifecycleSuspended, LifecycleSecurityFreeze, LifecycleOffboarding, LifecycleRetired:
	default:
		return errors.New("store: bad lifecycle")
	}
	res, err := s.DB.Exec(`UPDATE tenants SET lifecycle=? WHERE id=?`, lifecycle, tenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetChargeHold(tenantID string, hold bool) error {
	res, err := s.DB.Exec(`UPDATE tenants SET charge_hold=? WHERE id=?`, boolInt(hold), tenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) StampLinkBrand(linkID, tenantID, brandID, host string) error {
	res, err := s.DB.Exec(
		`UPDATE campaign_links SET published_brand_id=?, published_host=?, updated_at=? WHERE id=? AND tenant_id=?`,
		brandID, host, now(), linkID, tenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) LinkPublication(code string) (LinkPublication, error) {
	var p LinkPublication
	err := s.DB.QueryRow(
		`SELECT published_brand_id, published_host FROM campaign_links WHERE code=?`, code).
		Scan(&p.BrandID, &p.Host)
	if errors.Is(err, sql.ErrNoRows) {
		return LinkPublication{}, ErrNotFound
	}
	return p, err
}

// ExportManifest is the tenant-scoped offboarding bundle. Contact fields and
// other tenants are structurally absent.
type ExportManifest struct {
	BrandID       string           `json:"brand_id"`
	TenantID      string           `json:"tenant_id"`
	TenantName    string           `json:"tenant_name"`
	Requester     string           `json:"requester_principal"`
	Purpose       string           `json:"purpose"`
	ConfigVersion int64            `json:"brand_config_version,omitempty"`
	BrandDisplay  string           `json:"brand_display_name,omitempty"`
	Stores        []map[string]any `json:"stores"`
	Campaigns     []map[string]any `json:"campaigns"`
	TagBindings   []map[string]any `json:"tag_bindings"`
	Publications  []map[string]any `json:"published_content_refs"`
	RewardRules   []map[string]any `json:"reward_rules"`
	Stats         map[string]any   `json:"stats_summary"`
	Submissions   []map[string]any `json:"submission_refs"`
	Assets        []map[string]any `json:"asset_manifest"`
}

type ExportJob struct {
	ID        string
	TenantID  string
	BrandID   string
	Requester string
	Purpose   string
	ExpiresAt string
	Revoked   bool
	Manifest  string
	CreatedAt string
}

func (s *Store) BuildExportManifest(tenantID, brandID, requester, purpose, brandDisplay string, configVersion int64) (ExportManifest, error) {
	t, err := s.GetTenant(tenantID)
	if err != nil {
		return ExportManifest{}, err
	}
	m := ExportManifest{
		BrandID: brandID, TenantID: tenantID, TenantName: t.Name,
		Requester: requester, Purpose: purpose,
		ConfigVersion: configVersion, BrandDisplay: brandDisplay,
		Stores: []map[string]any{}, Campaigns: []map[string]any{},
		TagBindings: []map[string]any{}, Publications: []map[string]any{},
		RewardRules: []map[string]any{}, Submissions: []map[string]any{},
		Assets: []map[string]any{},
		Stats:  map[string]any{},
	}
	if err := s.collectMaps(`SELECT id,name,address FROM stores WHERE tenant_id=? ORDER BY created_at`, tenantID, func(scan func(...any) error) (map[string]any, error) {
		var id, name, address string
		if err := scan(&id, &name, &address); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "name": name, "address": address}, nil
	}, &m.Stores); err != nil {
		return m, err
	}
	if err := s.collectMaps(`SELECT id,title,status,COALESCE(store_id,'') FROM campaigns WHERE tenant_id=? ORDER BY created_at`, tenantID, func(scan func(...any) error) (map[string]any, error) {
		var id, title, status, storeID string
		if err := scan(&id, &title, &status, &storeID); err != nil {
			return nil, err
		}
		return map[string]any{"id": id, "title": title, "status": status, "store_id": storeID}, nil
	}, &m.Campaigns); err != nil {
		return m, err
	}
	if err := s.collectMaps(`SELECT id,link_id,status FROM nfc_tags WHERE tenant_id=?`, tenantID, func(scan func(...any) error) (map[string]any, error) {
		var id, linkID, status string
		if err := scan(&id, &linkID, &status); err != nil {
			return nil, err
		}
		return map[string]any{"tag_id": id, "link_id": linkID, "status": status}, nil
	}, &m.TagBindings); err != nil {
		return m, err
	}
	if err := s.collectMaps(`SELECT code,campaign_id,published_brand_id,published_host,enabled FROM campaign_links WHERE tenant_id=?`, tenantID, func(scan func(...any) error) (map[string]any, error) {
		var code, campaignID, brand, host string
		var enabled int
		if err := scan(&code, &campaignID, &brand, &host, &enabled); err != nil {
			return nil, err
		}
		return map[string]any{"code": code, "campaign_id": campaignID, "published_brand_id": brand, "published_host": host, "enabled": enabled == 1}, nil
	}, &m.Publications); err != nil {
		return m, err
	}
	if err := s.collectMaps(`SELECT campaign_id,reward_threshold,version FROM campaign_rules WHERE tenant_id=?`, tenantID, func(scan func(...any) error) (map[string]any, error) {
		var campaignID string
		var threshold sql.NullInt64
		var version int
		if err := scan(&campaignID, &threshold, &version); err != nil {
			return nil, err
		}
		item := map[string]any{"campaign_id": campaignID, "version": version}
		if threshold.Valid {
			item["reward_threshold"] = threshold.Int64
		}
		return item, nil
	}, &m.RewardRules); err != nil {
		return m, err
	}
	var views int
	if err := s.DB.QueryRow(`SELECT COALESCE(SUM(views),0) FROM public_view_stats WHERE code IN (SELECT code FROM campaign_links WHERE tenant_id=?)`, tenantID).Scan(&views); err != nil {
		return m, err
	}
	m.Stats = map[string]any{"anonymous_views": views, "note": "aggregate only; no visitor identity"}
	if err := s.collectMaps(`SELECT submission_ref,campaign_id,sync_state FROM lead_submissions WHERE tenant_id=?`, tenantID, func(scan func(...any) error) (map[string]any, error) {
		var ref, campaignID, state string
		if err := scan(&ref, &campaignID, &state); err != nil {
			return nil, err
		}
		return map[string]any{"submission_ref": ref, "campaign_id": campaignID, "sync_state": state}, nil
	}, &m.Submissions); err != nil {
		return m, err
	}
	if err := s.collectMaps(`SELECT asset_id,version FROM campaign_assets WHERE tenant_id=?`, tenantID, func(scan func(...any) error) (map[string]any, error) {
		var assetID, version string
		if err := scan(&assetID, &version); err != nil {
			return nil, err
		}
		return map[string]any{
			"asset_id": assetID, "version": version, "hash": "",
			"type": "campaign_asset_ref", "deliverable_ref": assetID, "bytes": "not_packed",
		}, nil
	}, &m.Assets); err != nil {
		return m, err
	}
	if err := s.collectMaps(`SELECT id,sha256,media_type,grant_ref FROM lib_assets WHERE tenant_id=?`, tenantID, func(scan func(...any) error) (map[string]any, error) {
		var id, hash, mediaType, grant string
		if err := scan(&id, &hash, &mediaType, &grant); err != nil {
			return nil, err
		}
		return map[string]any{
			"asset_id": id, "version": hash, "hash": hash,
			"type": mediaType, "deliverable_ref": grant, "bytes": "not_packed",
		}, nil
	}, &m.Assets); err != nil {
		return m, err
	}
	m.Stats["redemption_ledger"] = "not_stored_in_touch"
	return m, nil
}

func (s *Store) collectMaps(query, tenantID string, scan func(func(...any) error) (map[string]any, error), dst *[]map[string]any) error {
	rows, err := s.DB.Query(query, tenantID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		item, err := scan(rows.Scan)
		if err != nil {
			return err
		}
		*dst = append(*dst, item)
	}
	return rows.Err()
}

func (s *Store) CreateExport(tenantID, brandID, requester, purpose, manifest string, expires time.Time) (ExportJob, error) {
	job := ExportJob{
		ID: newID("exp_"), TenantID: tenantID, BrandID: brandID, Requester: requester,
		Purpose: purpose, ExpiresAt: expires.UTC().Format(time.RFC3339), Manifest: manifest,
		CreatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO tenant_exports(id,tenant_id,brand_id,requester_principal,purpose,expires_at,revoked,manifest_json,created_at)
		 VALUES(?,?,?,?,?,?,0,?,?)`,
		job.ID, job.TenantID, job.BrandID, job.Requester, job.Purpose, job.ExpiresAt, job.Manifest, job.CreatedAt)
	return job, err
}

func (s *Store) GetExport(id string) (ExportJob, error) {
	var job ExportJob
	var revoked int
	err := s.DB.QueryRow(
		`SELECT id,tenant_id,brand_id,requester_principal,purpose,expires_at,revoked,manifest_json,created_at FROM tenant_exports WHERE id=?`, id).
		Scan(&job.ID, &job.TenantID, &job.BrandID, &job.Requester, &job.Purpose, &job.ExpiresAt, &revoked, &job.Manifest, &job.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ExportJob{}, ErrNotFound
	}
	job.Revoked = revoked == 1
	return job, err
}

func (s *Store) RevokeExport(id, tenantID string) error {
	res, err := s.DB.Exec(`UPDATE tenant_exports SET revoked=1 WHERE id=? AND tenant_id=?`, id, tenantID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarshalManifest encodes an export. Callers scan the bytes for contact fields.
func MarshalManifest(m ExportManifest) (string, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
