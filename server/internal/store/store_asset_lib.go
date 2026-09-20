// store_asset_lib.go: HUI-1666 FEAT-0167 商家素材库持久层(additive)。
//
// 纪律:
//   - v1 = 素材登记与组织面,不建物理文件仓库:lib_assets 是对 platform-upload
//     产物(asset_ref)的引用记录 + sha256 版本锚点;大文件零进本库;
//   - 每条写路径先在租户内核对目标存在(跨租户一律 ErrNotFound,绝不泄露
//     存在性);唯一冲突返回独立哨兵错误,绝不静默改写既有行;
//   - 素材行只增不改(除候选标记):登记新版本 = 同 ref 新 sha 新行,已选
//     引用因此天然冻结;选择台账自己落 asset_ref/sha256 快照,重放先于取池;
//   - 域规则复用 assetlib(存储层第二道校验,HTTP 层已校验一次 —— 与
//     UpsertCampaignRules 复用 campaignrules.Validate 同一纪律);
//   - 单写连接(db.SetMaxOpenConns(1))下「先查后插」即无竞态,不用驱动
//     错误串解析判唯一冲突。

package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
)

// Sentinel errors for the asset-lib uniqueness contracts.
var (
	ErrDuplicateAsset    = errors.New("store: duplicate lib asset")
	ErrDuplicatePool     = errors.New("store: duplicate lib pool")
	ErrDuplicatePoolItem = errors.New("store: duplicate lib pool item")
)

// ---- lib assets (素材引用行) ---------------------------------------------------

// LibAsset is one registered reference into the merchant asset library.
type LibAsset struct {
	ID        string             `json:"id"`
	TenantID  string             `json:"tenant_id"`
	AssetRef  string             `json:"asset_ref"`
	SHA256    string             `json:"sha256"`
	MediaType assetlib.MediaType `json:"media_type"`
	Source    assetlib.Source    `json:"source"`
	Purpose   string             `json:"purpose"`
	GrantRef  string             `json:"grant_ref"`
	StoreID   string             `json:"store_id,omitempty"` // 门店归属(可空)
	Tags      []string           `json:"tags"`
	Candidate bool               `json:"candidate"` // 回流候选标记(先入候选,不直接替换)
	CreatedBy string             `json:"created_by"`
	CreatedAt string             `json:"created_at"`
	UpdatedAt string             `json:"updated_at"`
}

const libAssetCols = `id,tenant_id,asset_ref,sha256,media_type,source,purpose,grant_ref,store_id,tags,candidate,created_by,created_at,updated_at`

func scanLibAsset(sc interface{ Scan(...any) error }) (LibAsset, error) {
	var a LibAsset
	var storeID sql.NullString
	var rawTags string
	var cand int
	err := sc.Scan(&a.ID, &a.TenantID, &a.AssetRef, &a.SHA256, &a.MediaType, &a.Source,
		&a.Purpose, &a.GrantRef, &storeID, &rawTags, &cand, &a.CreatedBy, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		return LibAsset{}, err
	}
	a.StoreID = storeID.String
	a.Tags = unmarshalTags(rawTags)
	a.Candidate = cand == 1
	return a, nil
}

// marshalTags stores tags as JSON (comma-safe); empty slice = ”.
func marshalTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	b, err := json.Marshal(tags)
	if err != nil {
		return ""
	}
	return string(b)
}

func unmarshalTags(raw string) []string {
	if raw == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []string{}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// CreateLibAsset registers a reference row. The declaration is re-validated
// here (second gate); the caller's fingerprint/ref were already checked
// against platform upload by the HTTP layer. Duplicate (tenant, asset_ref,
// sha256, candidate) -> ErrDuplicateAsset; a non-empty storeID must exist in
// the same tenant.
func (s *Store) CreateLibAsset(tenantID string, reg assetlib.Registration, storeID, createdBy string) (LibAsset, error) {
	if err := assetlib.ValidateRegistration(reg); err != nil {
		return LibAsset{}, err
	}
	if storeID != "" {
		if _, err := s.GetStore(storeID, tenantID); err != nil {
			return LibAsset{}, err
		}
	}
	ref := assetlib.NormalizeText(reg.AssetRef)
	sha := assetlib.NormalizeSHA256(reg.SHA256)
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM lib_assets WHERE tenant_id=? AND asset_ref=? AND sha256=? AND candidate=?`,
		tenantID, ref, sha, boolInt(reg.Candidate)).Scan(&existing); err != nil {
		return LibAsset{}, err
	}
	if existing > 0 {
		return LibAsset{}, ErrDuplicateAsset
	}
	a := LibAsset{
		ID: newID("las_"), TenantID: tenantID, AssetRef: ref, SHA256: sha,
		MediaType: assetlib.MediaType(assetlib.NormalizeText(string(reg.MediaType))),
		Source:    assetlib.Source(assetlib.NormalizeText(string(reg.Source))),
		Purpose:   assetlib.NormalizeText(reg.Purpose), GrantRef: assetlib.NormalizeText(reg.GrantRef),
		StoreID: storeID, Tags: assetlib.NormalizeTags(reg.Tags), Candidate: reg.Candidate,
		CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO lib_assets(`+libAssetCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.TenantID, a.AssetRef, a.SHA256, a.MediaType, a.Source, a.Purpose, a.GrantRef,
		nullable(a.StoreID), marshalTags(a.Tags), boolInt(a.Candidate), a.CreatedBy, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return LibAsset{}, err
	}
	return a, nil
}

// GetLibAsset is the tenant-scoped lookup; foreign ids are ErrNotFound.
func (s *Store) GetLibAsset(id, tenantID string) (LibAsset, error) {
	a, err := scanLibAsset(s.DB.QueryRow(
		`SELECT `+libAssetCols+` FROM lib_assets WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return LibAsset{}, ErrNotFound
	}
	return a, err
}

// ListLibAssets returns the tenant's rows oldest-first.
func (s *Store) ListLibAssets(tenantID string) ([]LibAsset, error) {
	rows, err := s.DB.Query(
		`SELECT `+libAssetCols+` FROM lib_assets WHERE tenant_id=? ORDER BY created_at, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LibAsset, 0)
	for rows.Next() {
		a, err := scanLibAsset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetLibAssetCandidate flips ONLY the candidate flag (标记语义只改标记:引用/
// 版本/声明字段零改动). Promoting onto an existing same-key row is
// ErrDuplicateAsset, never an overwrite.
func (s *Store) SetLibAssetCandidate(id, tenantID string, candidate bool) (LibAsset, error) {
	cur, err := s.GetLibAsset(id, tenantID)
	if err != nil {
		return LibAsset{}, err
	}
	if cur.Candidate == candidate {
		return cur, nil
	}
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM lib_assets WHERE tenant_id=? AND asset_ref=? AND sha256=? AND candidate=? AND id<>?`,
		tenantID, cur.AssetRef, cur.SHA256, boolInt(candidate), cur.ID).Scan(&existing); err != nil {
		return LibAsset{}, err
	}
	if existing > 0 {
		return LibAsset{}, ErrDuplicateAsset
	}
	cur.Candidate = candidate
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE lib_assets SET candidate=?,updated_at=? WHERE id=? AND tenant_id=?`,
		boolInt(candidate), cur.UpdatedAt, cur.ID, tenantID)
	if err != nil {
		return LibAsset{}, err
	}
	return cur, nil
}

// ---- pools (视频池/素材池) ------------------------------------------------------

type LibPool struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Name      string `json:"name"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const libPoolCols = `id,tenant_id,name,created_by,created_at,updated_at`

func scanLibPool(sc interface{ Scan(...any) error }) (LibPool, error) {
	var p LibPool
	err := sc.Scan(&p.ID, &p.TenantID, &p.Name, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}

// CreateLibPool mints a pool; names are unique per tenant.
func (s *Store) CreateLibPool(tenantID, name, createdBy string) (LibPool, error) {
	if err := assetlib.ValidatePoolName(name); err != nil {
		return LibPool{}, err
	}
	if err := s.libPoolNameTaken(tenantID, assetlib.NormalizeText(name), ""); err != nil {
		return LibPool{}, err
	}
	p := LibPool{ID: newID("lpool_"), TenantID: tenantID, Name: assetlib.NormalizeText(name),
		CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now()}
	_, err := s.DB.Exec(
		`INSERT INTO lib_pools(`+libPoolCols+`) VALUES(?,?,?,?,?,?)`,
		p.ID, p.TenantID, p.Name, p.CreatedBy, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return LibPool{}, err
	}
	return p, nil
}

func (s *Store) libPoolNameTaken(tenantID, name, excludeID string) error {
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM lib_pools WHERE tenant_id=? AND name=? AND id<>?`,
		tenantID, name, excludeID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return ErrDuplicatePool
	}
	return nil
}

// GetLibPool is tenant-scoped; foreign ids are ErrNotFound.
func (s *Store) GetLibPool(id, tenantID string) (LibPool, error) {
	p, err := scanLibPool(s.DB.QueryRow(
		`SELECT `+libPoolCols+` FROM lib_pools WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return LibPool{}, ErrNotFound
	}
	return p, err
}

func (s *Store) ListLibPools(tenantID string) ([]LibPool, error) {
	rows, err := s.DB.Query(
		`SELECT `+libPoolCols+` FROM lib_pools WHERE tenant_id=? ORDER BY created_at, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LibPool, 0)
	for rows.Next() {
		p, err := scanLibPool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RenameLibPool changes only the name (still tenant-unique).
func (s *Store) RenameLibPool(id, tenantID, name string) (LibPool, error) {
	cur, err := s.GetLibPool(id, tenantID)
	if err != nil {
		return LibPool{}, err
	}
	if err := assetlib.ValidatePoolName(name); err != nil {
		return LibPool{}, err
	}
	if err := s.libPoolNameTaken(tenantID, assetlib.NormalizeText(name), cur.ID); err != nil {
		return LibPool{}, err
	}
	cur.Name = assetlib.NormalizeText(name)
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE lib_pools SET name=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Name, cur.UpdatedAt, cur.ID, tenantID)
	if err != nil {
		return LibPool{}, err
	}
	return cur, nil
}

// DeleteLibPool removes the pool AND its membership rows in one transaction.
// The selection ledger is deliberately untouched (台账独立于池的生命周期:
// pool_id / chosen_item_id 不设外键,删池后台账行保持完整可审计).
func (s *Store) DeleteLibPool(id, tenantID string) error {
	cur, err := s.GetLibPool(id, tenantID)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM lib_pool_items WHERE pool_id=? AND tenant_id=?`, cur.ID, tenantID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM lib_pools WHERE id=? AND tenant_id=?`, cur.ID, tenantID); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- pool items (池内素材引用) --------------------------------------------------

// LibPoolItem is one membership row with the referenced asset snapshot joined
// in (display convenience; the authoritative asset row is lib_assets).
type LibPoolItem struct {
	ID        string   `json:"id"`
	TenantID  string   `json:"tenant_id"`
	PoolID    string   `json:"pool_id"`
	AssetID   string   `json:"asset_id"`
	CreatedAt string   `json:"created_at"`
	Asset     LibAsset `json:"asset"`
}

const libPoolItemCols = `i.id,i.tenant_id,i.pool_id,i.asset_id,i.created_at`

// AddLibPoolItem references an existing lib asset into a pool. The same asset
// may enter many pools (池间共享); within one pool (pool, asset) is unique.
func (s *Store) AddLibPoolItem(tenantID, poolID, assetID, createdBy string) (LibPoolItem, error) {
	if _, err := s.GetLibPool(poolID, tenantID); err != nil {
		return LibPoolItem{}, err
	}
	if _, err := s.GetLibAsset(assetID, tenantID); err != nil {
		return LibPoolItem{}, err
	}
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM lib_pool_items WHERE pool_id=? AND asset_id=?`, poolID, assetID).Scan(&existing); err != nil {
		return LibPoolItem{}, err
	}
	if existing > 0 {
		return LibPoolItem{}, ErrDuplicatePoolItem
	}
	it := LibPoolItem{ID: newID("lpi_"), TenantID: tenantID, PoolID: poolID,
		AssetID: assetID, CreatedAt: now()}
	_ = createdBy
	_, err := s.DB.Exec(
		`INSERT INTO lib_pool_items(id,tenant_id,pool_id,asset_id,created_at) VALUES(?,?,?,?,?)`,
		it.ID, it.TenantID, it.PoolID, it.AssetID, it.CreatedAt)
	if err != nil {
		return LibPoolItem{}, err
	}
	return it, nil
}

// ListLibPoolItems returns the pool membership in the deterministic draw order
// (created_at, id) — the order Select depends on.
func (s *Store) ListLibPoolItems(tenantID, poolID string) ([]LibPoolItem, error) {
	if _, err := s.GetLibPool(poolID, tenantID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(
		`SELECT `+libPoolItemCols+`,a.id,a.tenant_id,a.asset_ref,a.sha256,a.media_type,a.source,`+
			`a.purpose,a.grant_ref,a.store_id,a.tags,a.candidate,a.created_by,a.created_at,a.updated_at `+
			`FROM lib_pool_items i JOIN lib_assets a ON a.id=i.asset_id `+
			`WHERE i.pool_id=? AND i.tenant_id=? ORDER BY i.created_at, i.id`, poolID, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LibPoolItem, 0)
	for rows.Next() {
		var it LibPoolItem
		var storeID sql.NullString
		var rawTags string
		var cand int
		if err := rows.Scan(&it.ID, &it.TenantID, &it.PoolID, &it.AssetID, &it.CreatedAt,
			&it.Asset.ID, &it.Asset.TenantID, &it.Asset.AssetRef, &it.Asset.SHA256, &it.Asset.MediaType,
			&it.Asset.Source, &it.Asset.Purpose, &it.Asset.GrantRef, &storeID, &rawTags, &cand,
			&it.Asset.CreatedBy, &it.Asset.CreatedAt, &it.Asset.UpdatedAt); err != nil {
			return nil, err
		}
		it.Asset.StoreID = storeID.String
		it.Asset.Tags = unmarshalTags(rawTags)
		it.Asset.Candidate = cand == 1
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) RemoveLibPoolItem(tenantID, poolID, itemID string) error {
	res, err := s.DB.Exec(
		`DELETE FROM lib_pool_items WHERE id=? AND pool_id=? AND tenant_id=?`, itemID, poolID, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- selections (选择台账:只追加,版本冻结) ----------------------------------------

// LibSelection is one appended ledger row. It freezes the chosen asset's
// identity and version (asset_id, asset_ref, sha256) at draw time: later
// registrations never rewrite it. pool_id / chosen_item_id carry no foreign
// keys so the ledger outlives pool deletion.
type LibSelection struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	PoolID       string `json:"pool_id"`
	SelectKey    string `json:"select_key"`
	DayUTC       string `json:"day_utc"`
	ChosenItemID string `json:"chosen_item_id"`
	AssetID      string `json:"asset_id"`
	AssetRef     string `json:"asset_ref"`
	SHA256       string `json:"sha256"`
	Candidate    bool   `json:"candidate"`
	CreatedBy    string `json:"created_by"`
	CreatedAt    string `json:"created_at"`
}

const libSelectionCols = `id,tenant_id,pool_id,select_key,day_utc,chosen_item_id,asset_id,asset_ref,sha256,candidate,created_by,created_at`

func scanLibSelection(sc interface{ Scan(...any) error }) (LibSelection, error) {
	var l LibSelection
	var cand int
	err := sc.Scan(&l.ID, &l.TenantID, &l.PoolID, &l.SelectKey, &l.DayUTC, &l.ChosenItemID,
		&l.AssetID, &l.AssetRef, &l.SHA256, &cand, &l.CreatedBy, &l.CreatedAt)
	if err != nil {
		return LibSelection{}, err
	}
	l.Candidate = cand == 1
	return l, nil
}

// DrawLibPool performs the deterministic draw: seed = (pool_id, dayUTC), the
// pool order is (created_at, id), and the whole thing is the pure
// assetlib.Select function. Same-key draws are IDEMPOTENT and the replay path
// precedes pool inspection: an already-frozen selection is returned even if
// the pool changed since. Empty fresh draws are assetlib.ErrEmptyPool.
func (s *Store) DrawLibPool(tenantID, poolID, selectKey, dayUTC string, candidate bool, createdBy string) (LibSelection, bool, error) {
	if _, err := s.GetLibPool(poolID, tenantID); err != nil {
		return LibSelection{}, false, err
	}
	if err := assetlib.ValidateSelectKey(selectKey); err != nil {
		return LibSelection{}, false, err
	}
	if !assetlib.ValidDayUTC(dayUTC) {
		return LibSelection{}, false, assetlib.NewValidationError("bad_day_utc", "day_utc")
	}
	key := assetlib.NormalizeText(selectKey)
	// 1) replay: same (pool, key, day, candidate) returns the frozen row.
	cur, err := scanLibSelection(s.DB.QueryRow(
		`SELECT `+libSelectionCols+` FROM lib_selections WHERE pool_id=? AND select_key=? AND day_utc=? AND candidate=?`,
		poolID, key, dayUTC, boolInt(candidate)))
	switch {
	case err == nil:
		return cur, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return LibSelection{}, false, err
	}
	// 2) fresh draw: pick deterministically from the ordered membership.
	items, err := s.ListLibPoolItems(tenantID, poolID)
	if err != nil {
		return LibSelection{}, false, err
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.AssetID)
	}
	chosenAssetID, err := assetlib.Select(poolID, dayUTC, ids)
	if err != nil {
		return LibSelection{}, false, err
	}
	asset, err := s.GetLibAsset(chosenAssetID, tenantID)
	if err != nil {
		return LibSelection{}, false, err
	}
	sel := LibSelection{
		ID: newID("lsel_"), TenantID: tenantID, PoolID: poolID, SelectKey: key, DayUTC: dayUTC,
		ChosenItemID: items[assetlib.PickIndex(poolID, dayUTC, len(items))].ID,
		AssetID:      asset.ID, AssetRef: asset.AssetRef, SHA256: asset.SHA256,
		Candidate: candidate, CreatedBy: createdBy, CreatedAt: now(),
	}
	_, err = s.DB.Exec(
		`INSERT INTO lib_selections(`+libSelectionCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		sel.ID, sel.TenantID, sel.PoolID, sel.SelectKey, sel.DayUTC, sel.ChosenItemID,
		sel.AssetID, sel.AssetRef, sel.SHA256, boolInt(sel.Candidate), sel.CreatedBy, sel.CreatedAt)
	if err != nil {
		return LibSelection{}, false, err
	}
	return sel, false, nil
}

// ListLibSelections returns the tenant's (optionally per-pool) ledger rows,
// oldest-first. Cross-tenant callers get zero rows.
func (s *Store) ListLibSelections(tenantID, poolID string) ([]LibSelection, error) {
	q := `SELECT ` + libSelectionCols + ` FROM lib_selections WHERE tenant_id=?`
	args := []any{tenantID}
	if poolID != "" {
		q += ` AND pool_id=?`
		args = append(args, poolID)
	}
	q += ` ORDER BY created_at, id`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]LibSelection, 0)
	for rows.Next() {
		l, err := scanLibSelection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
