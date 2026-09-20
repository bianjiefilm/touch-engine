// store_video_templates.go: HUI-1669 FEAT-0170 视频模板持久层(additive)。
//
// 纪律:
//   - 视频模板 = 素材引用集的组织单元,零物理存储:模板是具名结构
//     (名称租户内唯一),内容 = 槽位定义 + 到 lib_assets(FEAT-0167)的绑定
//     (JSON 快照存版本行);绑定校验只读 assetlib 既有行,零写放大;
//   - 生命周期与版本冻结:draft → published;发布后该版本行只读(零
//     UPDATE 内容列);结构变更 = 新草稿版本(version 只增,行永不删除);
//     同一时刻至多一个草稿版本;既有分配钉住 (template_id, version),
//     发布新版本绝不自动升级存量分配(与素材库选择台账同一冻结范式);
//   - 发布校验(服务端单点):槽位绑定引用不存在 / 媒体类型不符即拒发布,
//     拒绝不落任何半条状态;分配引用未发布版本即拒;
//   - 每条写路径先在租户内核对目标存在(跨租户一律 ErrNotFound,绝不泄露
//     存在性);唯一冲突返回独立哨兵错误,绝不静默改写既有行;
//   - 分配 = 模板⇄门店关系(一店多模板、一模板多店),UNIQUE(store_id,
//     template_id);同键同版幂等重放(零改动),换版重分配更新钉版并留
//     updated 痕,assigned 留痕(谁/何时)永保持首次;解绑幂等;
//   - 域规则复用 videotpl(存储层第二道校验,HTTP 层已校验一次 —— 与
//     UpsertCampaignRules 复用 campaignrules.Validate 同一纪律);
//   - 单写连接(db.SetMaxOpenConns(1))下「先查后插」即无竞态。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/bianjiefilm/touch-engine/server/internal/videotpl"
)

// Sentinel errors for the video-template contracts.
var (
	// ErrDuplicateVideoTemplate: 租户内模板重名。
	ErrDuplicateVideoTemplate = errors.New("store: duplicate video template")
	// ErrVideoTemplateVersionReadonly: 发布后版本行只读(仅当前草稿可改)。
	ErrVideoTemplateVersionReadonly = errors.New("store: video template version is readonly")
	// ErrVideoTemplateDraftExists: 已有草稿版本时拒绝再开新草稿。
	ErrVideoTemplateDraftExists = errors.New("store: video template draft exists")
	// ErrVideoTemplatePublished: 已发布版本存在,模板不可删除。
	ErrVideoTemplatePublished = errors.New("store: video template has published versions")
	// ErrVideoTemplateNoDraft: 无草稿版本可发布(最新版已发布)。
	ErrVideoTemplateNoDraft = errors.New("store: video template has no draft version")
	// ErrSlotAssetMissing: 槽位绑定的素材引用不存在。
	ErrSlotAssetMissing = errors.New("store: slot bound asset missing")
	// ErrSlotAssetTypeMismatch: 槽位绑定素材的媒体类型与槽位约束不符。
	ErrSlotAssetTypeMismatch = errors.New("store: slot bound asset media type mismatch")
	// ErrVideoTemplateVersionNotPublished: 分配引用了未发布版本(或模板无已发布版本)。
	ErrVideoTemplateVersionNotPublished = errors.New("store: video template version not published")
)

// ---- templates(模板身份行) ---------------------------------------------------

// VideoTemplate is the named organizational unit (名称租户内唯一).
type VideoTemplate struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Name      string `json:"name"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const videoTemplateCols = `id,tenant_id,name,created_by,created_at,updated_at`

func scanVideoTemplate(sc interface{ Scan(...any) error }) (VideoTemplate, error) {
	var t VideoTemplate
	err := sc.Scan(&t.ID, &t.TenantID, &t.Name, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

// VideoTemplateSummary is a template with its version-state aggregates.
type VideoTemplateSummary struct {
	VideoTemplate
	// LatestVersion: 最大版本号(创建即有 v1,故 >= 1)。
	LatestVersion int `json:"latest_version"`
	// LatestStatus: 最新版本的 StatusDraft/StatusPublished。
	LatestStatus string `json:"latest_status"`
	// PublishedVersion: 最新已发布版本号;0 = 从未发布。
	PublishedVersion int `json:"published_version"`
}

// ---- versions(版本行:draft → published,发布后只读) ---------------------------

// VideoTemplateVersion is one frozen-or-draft version row. Slots is the
// structure content snapshot (含槽位绑定)。
type VideoTemplateVersion struct {
	ID          string          `json:"id"`
	TenantID    string          `json:"tenant_id"`
	TemplateID  string          `json:"template_id"`
	Version     int             `json:"version"`
	Status      string          `json:"status"`
	Slots       []videotpl.Slot `json:"slots"`
	CreatedBy   string          `json:"created_by"`
	PublishedBy string          `json:"published_by,omitempty"`
	PublishedAt string          `json:"published_at,omitempty"` // "" = draft
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

const videoTemplateVersionCols = `id,tenant_id,template_id,version,status,slots,created_by,published_by,published_at,created_at,updated_at`

func scanVideoTemplateVersion(sc interface{ Scan(...any) error }) (VideoTemplateVersion, error) {
	var v VideoTemplateVersion
	var publishedBy sql.NullString
	var publishedAt sql.NullString
	var rawSlots string
	err := sc.Scan(&v.ID, &v.TenantID, &v.TemplateID, &v.Version, &v.Status, &rawSlots,
		&v.CreatedBy, &publishedBy, &publishedAt, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return VideoTemplateVersion{}, err
	}
	v.PublishedBy = publishedBy.String
	v.PublishedAt = publishedAt.String
	v.Slots = unmarshalSlots(rawSlots)
	return v, nil
}

// marshalSlots stores the structure content as JSON; empty = "".
func marshalSlots(slots []videotpl.Slot) string {
	if len(slots) == 0 {
		return ""
	}
	b, err := json.Marshal(slots)
	if err != nil {
		return ""
	}
	return string(b)
}

func unmarshalSlots(raw string) []videotpl.Slot {
	if raw == "" {
		return []videotpl.Slot{}
	}
	var out []videotpl.Slot
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []videotpl.Slot{}
	}
	if out == nil {
		out = []videotpl.Slot{}
	}
	return out
}

// CreateVideoTemplate mints the template AND its draft version 1 in one
// transaction. The name/content are re-validated here (second gate).
func (s *Store) CreateVideoTemplate(tenantID, name string, content videotpl.Content, createdBy string) (VideoTemplate, VideoTemplateVersion, error) {
	if err := videotpl.ValidateName(name); err != nil {
		return VideoTemplate{}, VideoTemplateVersion{}, err
	}
	if err := videotpl.ValidateContent(content); err != nil {
		return VideoTemplate{}, VideoTemplateVersion{}, err
	}
	if err := s.videoTemplateNameTaken(tenantID, videotpl.NormalizeName(name), ""); err != nil {
		return VideoTemplate{}, VideoTemplateVersion{}, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return VideoTemplate{}, VideoTemplateVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	tpl := VideoTemplate{
		ID: newID("vt_"), TenantID: tenantID, Name: videotpl.NormalizeName(name),
		CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now(),
	}
	if _, err := tx.Exec(
		`INSERT INTO video_templates(`+videoTemplateCols+`) VALUES(?,?,?,?,?,?)`,
		tpl.ID, tpl.TenantID, tpl.Name, tpl.CreatedBy, tpl.CreatedAt, tpl.UpdatedAt); err != nil {
		return VideoTemplate{}, VideoTemplateVersion{}, err
	}
	ver, err := insertVideoTemplateVersion(tx, tenantID, tpl.ID, 1, content, createdBy)
	if err != nil {
		return VideoTemplate{}, VideoTemplateVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return VideoTemplate{}, VideoTemplateVersion{}, err
	}
	return tpl, ver, nil
}

// insertVideoTemplateVersion inserts one draft version row inside tx.
func insertVideoTemplateVersion(tx *sql.Tx, tenantID, templateID string, version int, content videotpl.Content, createdBy string) (VideoTemplateVersion, error) {
	normalized := videotpl.NormalizeContent(content)
	ver := VideoTemplateVersion{
		ID: newID("vtv_"), TenantID: tenantID, TemplateID: templateID, Version: version,
		Status: videotpl.StatusDraft, Slots: normalized.Slots, CreatedBy: createdBy,
		CreatedAt: now(), UpdatedAt: now(),
	}
	if _, err := tx.Exec(
		`INSERT INTO video_template_versions(`+videoTemplateVersionCols+`) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		ver.ID, ver.TenantID, ver.TemplateID, ver.Version, ver.Status, marshalSlots(ver.Slots),
		ver.CreatedBy, "", nil, ver.CreatedAt, ver.UpdatedAt); err != nil {
		return VideoTemplateVersion{}, err
	}
	return ver, nil
}

func (s *Store) videoTemplateNameTaken(tenantID, name, excludeID string) error {
	var existing int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM video_templates WHERE tenant_id=? AND name=? AND id<>?`,
		tenantID, name, excludeID).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return ErrDuplicateVideoTemplate
	}
	return nil
}

// GetVideoTemplate is tenant-scoped; foreign ids are ErrNotFound.
func (s *Store) GetVideoTemplate(id, tenantID string) (VideoTemplate, error) {
	t, err := scanVideoTemplate(s.DB.QueryRow(
		`SELECT `+videoTemplateCols+` FROM video_templates WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return VideoTemplate{}, ErrNotFound
	}
	return t, err
}

// RenameVideoTemplate changes only the name (still tenant-unique). The name is
// the organizational identity, NOT versioned content: renaming never mints a
// version and never disturbs pins.
func (s *Store) RenameVideoTemplate(id, tenantID, name string) (VideoTemplate, error) {
	cur, err := s.GetVideoTemplate(id, tenantID)
	if err != nil {
		return VideoTemplate{}, err
	}
	if err := videotpl.ValidateName(name); err != nil {
		return VideoTemplate{}, err
	}
	if err := s.videoTemplateNameTaken(tenantID, videotpl.NormalizeName(name), cur.ID); err != nil {
		return VideoTemplate{}, err
	}
	cur.Name = videotpl.NormalizeName(name)
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(`UPDATE video_templates SET name=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Name, cur.UpdatedAt, cur.ID, tenantID)
	if err != nil {
		return VideoTemplate{}, err
	}
	return cur, nil
}

// DeleteVideoTemplate removes a NEVER-published (pure draft) template with its
// version rows. Any published version freezes the whole template
// (ErrVideoTemplatePublished): assignments pin (template_id, version) and must
// stay resolvable (版本行永不删除).
func (s *Store) DeleteVideoTemplate(id, tenantID string) error {
	if _, err := s.GetVideoTemplate(id, tenantID); err != nil {
		return err
	}
	var published int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM video_template_versions WHERE template_id=? AND tenant_id=? AND status=?`,
		id, tenantID, videotpl.StatusPublished).Scan(&published); err != nil {
		return err
	}
	if published > 0 {
		return ErrVideoTemplatePublished
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM video_template_versions WHERE template_id=? AND tenant_id=?`, id, tenantID); err != nil {
		return err
	}
	// 纯草稿不可能有分配(分配要求已发布版本),防御性清理同事务执行。
	if _, err := tx.Exec(`DELETE FROM video_template_assignments WHERE template_id=? AND tenant_id=?`, id, tenantID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM video_templates WHERE id=? AND tenant_id=?`, id, tenantID); err != nil {
		return err
	}
	return tx.Commit()
}

// GetVideoTemplateSummary computes the version-state aggregates of a template.
func (s *Store) GetVideoTemplateSummary(tenantID, id string) (VideoTemplateSummary, error) {
	tpl, err := s.GetVideoTemplate(id, tenantID)
	if err != nil {
		return VideoTemplateSummary{}, err
	}
	vers, err := s.ListVideoTemplateVersions(tenantID, id)
	if err != nil {
		return VideoTemplateSummary{}, err
	}
	return summarizeVideoTemplate(tpl, vers), nil
}

func summarizeVideoTemplate(tpl VideoTemplate, vers []VideoTemplateVersion) VideoTemplateSummary {
	sum := VideoTemplateSummary{VideoTemplate: tpl, PublishedVersion: 0}
	for _, v := range vers {
		if v.Version > sum.LatestVersion {
			sum.LatestVersion = v.Version
			sum.LatestStatus = v.Status
		}
		if v.Status == videotpl.StatusPublished && v.Version > sum.PublishedVersion {
			sum.PublishedVersion = v.Version
		}
	}
	return sum
}

// ListVideoTemplates returns the tenant's templates oldest-first, with
// version-state aggregates.
func (s *Store) ListVideoTemplates(tenantID string) ([]VideoTemplateSummary, error) {
	tpls, err := s.listVideoTemplateRows(tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]VideoTemplateSummary, 0, len(tpls))
	for _, tpl := range tpls {
		vers, err := s.ListVideoTemplateVersions(tenantID, tpl.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, summarizeVideoTemplate(tpl, vers))
	}
	return out, nil
}

func (s *Store) listVideoTemplateRows(tenantID string) ([]VideoTemplate, error) {
	rows, err := s.DB.Query(
		`SELECT `+videoTemplateCols+` FROM video_templates WHERE tenant_id=? ORDER BY created_at, id`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]VideoTemplate, 0)
	for rows.Next() {
		t, err := scanVideoTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ---- version access / mutation ------------------------------------------------

// GetVideoTemplateVersion is tenant-scoped; foreign ids are ErrNotFound.
func (s *Store) GetVideoTemplateVersion(tenantID, templateID string, version int) (VideoTemplateVersion, error) {
	v, err := scanVideoTemplateVersion(s.DB.QueryRow(
		`SELECT `+videoTemplateVersionCols+` FROM video_template_versions WHERE tenant_id=? AND template_id=? AND version=?`,
		tenantID, templateID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return VideoTemplateVersion{}, ErrNotFound
	}
	return v, err
}

// ListVideoTemplateVersions returns the version rows oldest-first (只增不删).
func (s *Store) ListVideoTemplateVersions(tenantID, templateID string) ([]VideoTemplateVersion, error) {
	rows, err := s.DB.Query(
		`SELECT `+videoTemplateVersionCols+` FROM video_template_versions WHERE tenant_id=? AND template_id=? ORDER BY version`,
		tenantID, templateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]VideoTemplateVersion, 0)
	for rows.Next() {
		v, err := scanVideoTemplateVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// UpdateVideoTemplateVersionContent replaces a DRAFT version's structure
// content in place. Published versions (and any non-latest row) are readonly:
// ErrVideoTemplateVersionReadonly. 结构变更出已发布版本 = CreateVideoTemplateVersion.
func (s *Store) UpdateVideoTemplateVersionContent(tenantID, templateID string, version int, content videotpl.Content, updatedBy string) (VideoTemplateVersion, error) {
	if err := videotpl.ValidateContent(content); err != nil {
		return VideoTemplateVersion{}, err
	}
	cur, err := s.GetVideoTemplateVersion(tenantID, templateID, version)
	if err != nil {
		return VideoTemplateVersion{}, err
	}
	latest, err := s.latestVideoTemplateVersion(tenantID, templateID)
	if err != nil {
		return VideoTemplateVersion{}, err
	}
	if cur.Status != videotpl.StatusDraft || cur.Version != latest.Version {
		return VideoTemplateVersion{}, ErrVideoTemplateVersionReadonly
	}
	cur.Slots = videotpl.NormalizeContent(content).Slots
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(
		`UPDATE video_template_versions SET slots=?,updated_at=? WHERE id=? AND tenant_id=? AND status=?`,
		marshalSlots(cur.Slots), cur.UpdatedAt, cur.ID, tenantID, videotpl.StatusDraft)
	if err != nil {
		return VideoTemplateVersion{}, err
	}
	return cur, nil
}

// CreateVideoTemplateVersion mints draft version max+1 (结构变更 = 新版本).
// Refused while a draft already exists (ErrVideoTemplateDraftExists): at most
// one editable draft at a time.
func (s *Store) CreateVideoTemplateVersion(tenantID, templateID string, content videotpl.Content, createdBy string) (VideoTemplateVersion, error) {
	if err := videotpl.ValidateContent(content); err != nil {
		return VideoTemplateVersion{}, err
	}
	if _, err := s.GetVideoTemplate(templateID, tenantID); err != nil {
		return VideoTemplateVersion{}, err
	}
	latest, err := s.latestVideoTemplateVersion(tenantID, templateID)
	if err != nil {
		return VideoTemplateVersion{}, err
	}
	if latest.Status == videotpl.StatusDraft {
		return VideoTemplateVersion{}, ErrVideoTemplateDraftExists
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return VideoTemplateVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	ver, err := insertVideoTemplateVersion(tx, tenantID, templateID, latest.Version+1, content, createdBy)
	if err != nil {
		return VideoTemplateVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return VideoTemplateVersion{}, err
	}
	return ver, nil
}

func (s *Store) latestVideoTemplateVersion(tenantID, templateID string) (VideoTemplateVersion, error) {
	v, err := scanVideoTemplateVersion(s.DB.QueryRow(
		`SELECT `+videoTemplateVersionCols+` FROM video_template_versions WHERE tenant_id=? AND template_id=? ORDER BY version DESC LIMIT 1`,
		tenantID, templateID))
	if errors.Is(err, sql.ErrNoRows) {
		return VideoTemplateVersion{}, ErrNotFound
	}
	return v, err
}

// PublishVideoTemplate publishes the CURRENT DRAFT (latest version) after the
// binding gate: every bound slot's asset must exist in the tenant
// (ErrSlotAssetMissing) and match the slot media type (ErrSlotAssetTypeMismatch).
// Rejected publishes leave zero state change. Afterwards the version row is
// readonly forever.
func (s *Store) PublishVideoTemplate(tenantID, templateID, publishedBy string) (VideoTemplateVersion, error) {
	if _, err := s.GetVideoTemplate(templateID, tenantID); err != nil {
		return VideoTemplateVersion{}, err
	}
	cur, err := s.latestVideoTemplateVersion(tenantID, templateID)
	if err != nil {
		return VideoTemplateVersion{}, err
	}
	if cur.Status != videotpl.StatusDraft {
		return VideoTemplateVersion{}, ErrVideoTemplateNoDraft
	}
	if err := s.validateSlotBindings(tenantID, cur.Slots); err != nil {
		return VideoTemplateVersion{}, err
	}
	ts := now()
	res, err := s.DB.Exec(
		`UPDATE video_template_versions SET status=?,published_by=?,published_at=?,updated_at=? `+
			`WHERE id=? AND tenant_id=? AND status=?`,
		videotpl.StatusPublished, publishedBy, ts, ts, cur.ID, tenantID, videotpl.StatusDraft)
	if err != nil {
		return VideoTemplateVersion{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return VideoTemplateVersion{}, ErrVideoTemplateNoDraft
	}
	return s.GetVideoTemplateVersion(tenantID, templateID, cur.Version)
}

// validateSlotBindings is the single binding gate (发布时统一校验;草稿面允许
// 占位槽位/先绑后登)。绑定只读 assetlib 既有行。
func (s *Store) validateSlotBindings(tenantID string, slots []videotpl.Slot) error {
	for _, raw := range slots {
		slot := videotpl.NormalizeSlot(raw)
		if slot.AssetID == "" {
			continue
		}
		a, err := s.GetLibAsset(slot.AssetID, tenantID)
		if errors.Is(err, ErrNotFound) {
			return ErrSlotAssetMissing
		}
		if err != nil {
			return err
		}
		if a.MediaType != slot.MediaType {
			return ErrSlotAssetTypeMismatch
		}
	}
	return nil
}

// ---- assignments(模板⇄门店分配:钉版 + 留痕) -------------------------------------

// VideoTemplateAssignment is one store⇄template relation pinned to a version.
// assigned_by/assigned_at are the FIRST-assignment trace (永不改写);
// updated_by/updated_at carry the latest re-assignment. Template and
// PinnedVersion are hydrated for display convenience.
type VideoTemplateAssignment struct {
	ID            string               `json:"id"`
	TenantID      string               `json:"tenant_id"`
	TemplateID    string               `json:"template_id"`
	Version       int                  `json:"version"` // 钉住的版本号(不自动升级)
	StoreID       string               `json:"store_id"`
	AssignedBy    string               `json:"assigned_by"`
	AssignedAt    string               `json:"assigned_at"`
	UpdatedBy     string               `json:"updated_by"`
	UpdatedAt     string               `json:"updated_at"`
	Template      VideoTemplate        `json:"template"`
	PinnedVersion VideoTemplateVersion `json:"pinned_version"`
}

const videoTemplateAssignmentCols = `id,tenant_id,template_id,version,store_id,assigned_by,assigned_at,updated_by,updated_at`

func scanVideoTemplateAssignment(sc interface{ Scan(...any) error }) (VideoTemplateAssignment, error) {
	var a VideoTemplateAssignment
	err := sc.Scan(&a.ID, &a.TenantID, &a.TemplateID, &a.Version, &a.StoreID,
		&a.AssignedBy, &a.AssignedAt, &a.UpdatedBy, &a.UpdatedAt)
	return a, err
}

// AssignVideoTemplate creates or re-points the (store, template) assignment.
// version == 0 pins the LATEST PUBLISHED version at assign time; an explicit
// version must be published (未发布版本即拒). Same-key same-version is an
// idempotent replay (replayed=true, zero mutation); a different version
// re-points the pin (updated=true) and leaves an updated_* trace while the
// assigned_* trace keeps the first assignment; a fresh relation reports
// created (both false). The store and template must both exist in the
// tenant (cross-tenant = ErrNotFound).
func (s *Store) AssignVideoTemplate(tenantID, storeID, templateID string, version int, assignedBy string) (a VideoTemplateAssignment, replayed bool, updated bool, err error) {
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return VideoTemplateAssignment{}, false, false, err
	}
	if _, err := s.GetVideoTemplate(templateID, tenantID); err != nil {
		return VideoTemplateAssignment{}, false, false, err
	}
	if version == 0 {
		pub, err := s.latestPublishedVideoTemplateVersion(tenantID, templateID)
		if err != nil {
			return VideoTemplateAssignment{}, false, false, err
		}
		version = pub.Version
	} else {
		v, err := s.GetVideoTemplateVersion(tenantID, templateID, version)
		if err != nil {
			return VideoTemplateAssignment{}, false, false, err
		}
		if v.Status != videotpl.StatusPublished {
			return VideoTemplateAssignment{}, false, false, ErrVideoTemplateVersionNotPublished
		}
	}

	cur, err := scanVideoTemplateAssignment(s.DB.QueryRow(
		`SELECT `+videoTemplateAssignmentCols+` FROM video_template_assignments WHERE store_id=? AND template_id=?`,
		storeID, templateID))
	switch {
	case err == nil:
		if cur.Version == version {
			if err := s.hydrateVideoTemplateAssignment(tenantID, &cur); err != nil {
				return VideoTemplateAssignment{}, false, false, err
			}
			return cur, true, false, nil // 幂等重放:同键同版,零改动
		}
		cur.Version = version
		cur.UpdatedBy = assignedBy
		cur.UpdatedAt = now()
		_, err = s.DB.Exec(
			`UPDATE video_template_assignments SET version=?,updated_by=?,updated_at=? WHERE id=? AND tenant_id=?`,
			cur.Version, cur.UpdatedBy, cur.UpdatedAt, cur.ID, tenantID)
		if err != nil {
			return VideoTemplateAssignment{}, false, false, err
		}
		if err := s.hydrateVideoTemplateAssignment(tenantID, &cur); err != nil {
			return VideoTemplateAssignment{}, false, false, err
		}
		return cur, false, true, nil // 换版重分配:钉版更新 + updated 留痕
	case !errors.Is(err, sql.ErrNoRows):
		return VideoTemplateAssignment{}, false, false, err
	}
	a = VideoTemplateAssignment{
		ID: newID("vta_"), TenantID: tenantID, TemplateID: templateID, Version: version,
		StoreID: storeID, AssignedBy: assignedBy, AssignedAt: now(),
		UpdatedBy: assignedBy, UpdatedAt: now(),
	}
	_, err = s.DB.Exec(
		`INSERT INTO video_template_assignments(`+videoTemplateAssignmentCols+`) VALUES(?,?,?,?,?,?,?,?,?)`,
		a.ID, a.TenantID, a.TemplateID, a.Version, a.StoreID,
		a.AssignedBy, a.AssignedAt, a.UpdatedBy, a.UpdatedAt)
	if err != nil {
		return VideoTemplateAssignment{}, false, false, err
	}
	if err := s.hydrateVideoTemplateAssignment(tenantID, &a); err != nil {
		return VideoTemplateAssignment{}, false, false, err
	}
	return a, false, false, nil
}

func (s *Store) latestPublishedVideoTemplateVersion(tenantID, templateID string) (VideoTemplateVersion, error) {
	v, err := scanVideoTemplateVersion(s.DB.QueryRow(
		`SELECT `+videoTemplateVersionCols+` FROM video_template_versions WHERE tenant_id=? AND template_id=? AND status=? ORDER BY version DESC LIMIT 1`,
		tenantID, templateID, videotpl.StatusPublished))
	if errors.Is(err, sql.ErrNoRows) {
		return VideoTemplateVersion{}, ErrVideoTemplateVersionNotPublished
	}
	return v, err
}

// UnassignVideoTemplate removes the (store, template) assignment. Idempotent:
// unbinding an already-unbound pair succeeds (解绑幂等); unknown store/template
// in the tenant stays ErrNotFound (fail on typos, never leak existence).
func (s *Store) UnassignVideoTemplate(tenantID, storeID, templateID string) error {
	if _, err := s.GetStore(storeID, tenantID); err != nil {
		return err
	}
	if _, err := s.GetVideoTemplate(templateID, tenantID); err != nil {
		return err
	}
	_, err := s.DB.Exec(
		`DELETE FROM video_template_assignments WHERE store_id=? AND template_id=? AND tenant_id=?`,
		storeID, templateID, tenantID)
	return err
}

// ListVideoTemplateAssignmentsByStore returns one store's assignments
// (template + pinned version hydrated), oldest-first.
func (s *Store) ListVideoTemplateAssignmentsByStore(tenantID, storeID string) ([]VideoTemplateAssignment, error) {
	return s.listVideoTemplateAssignments(tenantID, `store_id=?`, storeID)
}

// ListVideoTemplateAssignmentsByTemplate returns one template's assignments
// across stores, oldest-first.
func (s *Store) ListVideoTemplateAssignmentsByTemplate(tenantID, templateID string) ([]VideoTemplateAssignment, error) {
	return s.listVideoTemplateAssignments(tenantID, `template_id=?`, templateID)
}

func (s *Store) listVideoTemplateAssignments(tenantID, filter, val string) ([]VideoTemplateAssignment, error) {
	rows, err := s.DB.Query(
		`SELECT `+videoTemplateAssignmentCols+` FROM video_template_assignments WHERE tenant_id=? AND `+filter+` ORDER BY assigned_at, id`,
		tenantID, val)
	if err != nil {
		return nil, err
	}
	// 先物化全部行再回填(单写连接下,打开的游标占用唯一连接;回填查询
	// 必须等游标关闭后执行,否则自锁)。
	pending := make([]VideoTemplateAssignment, 0)
	for rows.Next() {
		a, err := scanVideoTemplateAssignment(rows)
		if err != nil {
			return nil, err
		}
		pending = append(pending, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	out := make([]VideoTemplateAssignment, 0, len(pending))
	for i := range pending {
		a := pending[i]
		if err := s.hydrateVideoTemplateAssignment(tenantID, &a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) hydrateVideoTemplateAssignment(tenantID string, a *VideoTemplateAssignment) error {
	tpl, err := s.GetVideoTemplate(a.TemplateID, tenantID)
	if err != nil {
		return err
	}
	ver, err := s.GetVideoTemplateVersion(tenantID, a.TemplateID, a.Version)
	if err != nil {
		return err
	}
	a.Template, a.PinnedVersion = tpl, ver
	return nil
}
