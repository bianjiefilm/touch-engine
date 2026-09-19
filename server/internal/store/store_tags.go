// store_tags.go: HUI-1665 FEAT-0166 NFC tag management persistence (additive
// to T0/T1).
//
// 纪律:
//   - 标签是既有 campaign_link(短码)的管理面包装:路由唯一事实来源仍是
//     campaign_links,本文件绝不复制/改写短码,也绝不新建第二套链接模型。
//   - SetTagStatus 在同一事务内改标签 status 并把其绑定 link 置为同态:
//     标签停用 → 短码走既有五态停用态(link_disabled);恢复 → 复用。
//     多标签共享一条 link 时它们是同一物理入口的别名,停用其一即停用该入口。
//   - 批量 CreateTagsBatch 单事务写入;数量上限 MaxBatchTags(500)。
//   - 全部查询租户作用域、全参数化;uid_hint 只在管理面视图返回,
//     公共面不经过本文件。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// MaxBatchTags bounds one batch-generation request.
const MaxBatchTags = 500

// Tag status values.
const (
	TagStatusActive   = "active"
	TagStatusDisabled = "disabled"
)

// ---- tag groups ---------------------------------------------------------------

type NfcTagGroup struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Name      string `json:"name"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

const tagGroupCols = `id,tenant_id,name,created_by,created_at,updated_at`

func scanTagGroup(sc interface{ Scan(...any) error }) (NfcTagGroup, error) {
	var g NfcTagGroup
	err := sc.Scan(&g.ID, &g.TenantID, &g.Name, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

// ErrTagGroupNameTaken is returned when the tenant already has a group with
// the same name.
var ErrTagGroupNameTaken = errors.New("store: tag group name already taken")

func (s *Store) CreateTagGroup(tenantID, name, createdBy string) (NfcTagGroup, error) {
	g := NfcTagGroup{ID: newID("nfcg_"), TenantID: tenantID, Name: strings.TrimSpace(name),
		CreatedBy: createdBy, CreatedAt: now(), UpdatedAt: now()}
	_, err := s.DB.Exec(
		`INSERT INTO nfc_tag_groups(`+tagGroupCols+`) VALUES(?,?,?,?,?,?)`,
		g.ID, g.TenantID, g.Name, g.CreatedBy, g.CreatedAt, g.UpdatedAt)
	if err != nil && isUniqueViolation(err, "nfc_tag_groups.name") {
		return NfcTagGroup{}, ErrTagGroupNameTaken
	}
	return g, err
}

func (s *Store) GetTagGroup(id, tenantID string) (NfcTagGroup, error) {
	g, err := scanTagGroup(s.DB.QueryRow(`SELECT `+tagGroupCols+` FROM nfc_tag_groups WHERE id=? AND tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return NfcTagGroup{}, ErrNotFound
	}
	return g, err
}

func (s *Store) ListTagGroups(tenantID string) ([]NfcTagGroup, error) {
	rows, err := s.DB.Query(`SELECT `+tagGroupCols+` FROM nfc_tag_groups WHERE tenant_id=? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NfcTagGroup, 0)
	for rows.Next() {
		g, err := scanTagGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// DeleteTagGroup removes an (empty after detach) group. Tags keep existing,
// ungrouped: the group is a label, not a container of record.
func (s *Store) DeleteTagGroup(id, tenantID string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE nfc_tags SET group_id=NULL,updated_at=? WHERE group_id=? AND tenant_id=?`, now(), id, tenantID); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM nfc_tag_groups WHERE id=? AND tenant_id=?`, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// ---- tags ---------------------------------------------------------------------

type NfcTag struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	GroupID   string `json:"group_id,omitempty"`
	StoreID   string `json:"store_id,omitempty"`
	LinkID    string `json:"link_id"`
	UIDHint   string `json:"uid_hint,omitempty"`
	Label     string `json:"label"`
	Status    string `json:"status"`
	CreatedBy string `json:"created_by"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// NfcTagView is the admin-surface projection: the tag plus the joined display
// fields needed by the panel and the CSV export.
type NfcTagView struct {
	NfcTag
	Code       string `json:"code"`
	CampaignID string `json:"campaign_id"`
	StoreName  string `json:"store_name,omitempty"`
	GroupName  string `json:"group_name,omitempty"`
}

const tagCols = `t.id,t.tenant_id,t.group_id,t.store_id,t.link_id,t.uid_hint,t.label,t.status,t.created_by,t.created_at,t.updated_at`

const tagViewSelect = `SELECT ` + tagCols + `,l.code,l.campaign_id,COALESCE(st.name,''),COALESCE(g.name,'')
FROM nfc_tags t
JOIN campaign_links l ON l.id=t.link_id
LEFT JOIN stores st ON st.id=t.store_id
LEFT JOIN nfc_tag_groups g ON g.id=t.group_id`

func scanTagView(sc interface{ Scan(...any) error }) (NfcTagView, error) {
	var v NfcTagView
	var groupID, storeID, uidHint sql.NullString
	err := sc.Scan(&v.ID, &v.TenantID, &groupID, &storeID, &v.LinkID, &uidHint, &v.Label, &v.Status,
		&v.CreatedBy, &v.CreatedAt, &v.UpdatedAt, &v.Code, &v.CampaignID, &v.StoreName, &v.GroupName)
	if err != nil {
		return NfcTagView{}, err
	}
	v.GroupID, v.StoreID, v.UIDHint = groupID.String, storeID.String, uidHint.String
	return v, nil
}

func (s *Store) GetTagView(id, tenantID string) (NfcTagView, error) {
	v, err := scanTagView(s.DB.QueryRow(tagViewSelect+` WHERE t.id=? AND t.tenant_id=?`, id, tenantID))
	if errors.Is(err, sql.ErrNoRows) {
		return NfcTagView{}, ErrNotFound
	}
	return v, err
}

// TagFilter narrows ListTags; empty fields mean "no filter". Every value is a
// bound parameter; the caller's tenant is mandatory.
type TagFilter struct {
	CampaignID string
	GroupID    string
	StoreID    string
	Status     string
}

func (f TagFilter) valid() bool {
	switch f.Status {
	case "", TagStatusActive, TagStatusDisabled:
	default:
		return false
	}
	return true
}

func (s *Store) ListTags(tenantID string, f TagFilter) ([]NfcTagView, error) {
	if !f.valid() {
		return nil, fmt.Errorf("store: bad tag status filter %q", f.Status)
	}
	conds := []string{"t.tenant_id=?"}
	args := []any{tenantID}
	if f.CampaignID != "" {
		conds = append(conds, "l.campaign_id=?")
		args = append(args, f.CampaignID)
	}
	if f.GroupID != "" {
		conds = append(conds, "t.group_id=?")
		args = append(args, f.GroupID)
	}
	if f.StoreID != "" {
		conds = append(conds, "t.store_id=?")
		args = append(args, f.StoreID)
	}
	if f.Status != "" {
		conds = append(conds, "t.status=?")
		args = append(args, f.Status)
	}
	rows, err := s.DB.Query(tagViewSelect+` WHERE `+strings.Join(conds, " AND ")+` ORDER BY t.created_at,t.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]NfcTagView, 0)
	for rows.Next() {
		v, err := scanTagView(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// NewTagBatch carries one batch-generation request (validated by the caller).
type NewTagBatch struct {
	TenantID    string
	CampaignID  string
	LinkIDs     []string // existing campaign_links of CampaignID
	BindMode    string   // "shared" (all -> LinkIDs[0]) or "rotate" (i -> LinkIDs[i%len])
	Count       int
	GroupID     string // optional
	StoreID     string // optional
	LabelPrefix string // optional; default "NFC"
	CreatedBy   string
}

// CreateTagsBatch inserts Count tags in ONE transaction. Validation of the
// campaign/links tenancy happens here again (defense in depth): every link
// must exist in this tenant AND belong to CampaignID.
func (s *Store) CreateTagsBatch(n NewTagBatch) ([]NfcTagView, error) {
	if n.Count < 1 || n.Count > MaxBatchTags {
		return nil, fmt.Errorf("store: batch count %d out of range 1..%d", n.Count, MaxBatchTags)
	}
	if len(n.LinkIDs) == 0 {
		return nil, errors.New("store: batch requires at least one link")
	}
	if n.BindMode != "shared" && n.BindMode != "rotate" {
		return nil, fmt.Errorf("store: bad bind_mode %q", n.BindMode)
	}
	if _, err := s.GetCampaign(n.CampaignID, n.TenantID); err != nil {
		return nil, err
	}
	links := make([]CampaignLink, 0, len(n.LinkIDs))
	for _, id := range n.LinkIDs {
		l, err := s.GetLink(id, n.TenantID)
		if err != nil {
			return nil, fmt.Errorf("store: link %s: %w", id, err)
		}
		if l.CampaignID != n.CampaignID {
			return nil, fmt.Errorf("store: link %s does not belong to campaign %s", id, n.CampaignID)
		}
		links = append(links, l)
	}
	prefix := strings.TrimSpace(n.LabelPrefix)
	if prefix == "" {
		prefix = "NFC"
	}
	groupID := nullable(n.GroupID)
	storeID := nullable(n.StoreID)

	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	created := make([]NfcTagView, 0, n.Count)
	for i := 0; i < n.Count; i++ {
		var link CampaignLink
		if n.BindMode == "shared" {
			link = links[0]
		} else {
			link = links[i%len(links)]
		}
		t := NfcTag{
			ID: newID("nfc_"), TenantID: n.TenantID, GroupID: n.GroupID, StoreID: n.StoreID,
			LinkID: link.ID, Label: fmt.Sprintf("%s-%03d", prefix, i+1),
			Status: TagStatusActive, CreatedBy: n.CreatedBy, CreatedAt: now(), UpdatedAt: now(),
		}
		if _, err := tx.Exec(
			`INSERT INTO nfc_tags(id,tenant_id,group_id,store_id,link_id,uid_hint,label,status,created_by,created_at,updated_at)
			 VALUES(?,?,?,?,?,NULL,?,?,?,?,?)`,
			t.ID, t.TenantID, groupID, storeID, t.LinkID, t.Label, t.Status, t.CreatedBy, t.CreatedAt, t.UpdatedAt); err != nil {
			return nil, err
		}
		created = append(created, NfcTagView{
			NfcTag: t, Code: link.Code, CampaignID: n.CampaignID,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

// SetTagStatus flips the tag's own status AND its bound link's enabled flag in
// ONE transaction, so the public short-code route follows the existing
// five-state logic immediately (停用态 / 复用). Stopping never deletes anything.
func (s *Store) SetTagStatus(id, tenantID string, status string) (NfcTagView, error) {
	if status != TagStatusActive && status != TagStatusDisabled {
		return NfcTagView{}, fmt.Errorf("store: bad tag status %q", status)
	}
	cur, err := s.GetTagView(id, tenantID)
	if err != nil {
		return NfcTagView{}, err
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return NfcTagView{}, err
	}
	defer func() { _ = tx.Rollback() }()
	ts := now()
	if _, err := tx.Exec(`UPDATE nfc_tags SET status=?,updated_at=? WHERE id=? AND tenant_id=?`,
		status, ts, id, tenantID); err != nil {
		return NfcTagView{}, err
	}
	if _, err := tx.Exec(`UPDATE campaign_links SET enabled=?,updated_at=? WHERE id=? AND tenant_id=?`,
		boolInt(status == TagStatusActive), ts, cur.LinkID, tenantID); err != nil {
		return NfcTagView{}, err
	}
	if err := tx.Commit(); err != nil {
		return NfcTagView{}, err
	}
	return s.GetTagView(id, tenantID)
}

// TagPatch carries optional tag updates; nil leaves the field unchanged.
// Empty-string GroupID/StoreID/UIDHint clears them; empty-string Label/LinkID
// are refused by the handler (label and binding are load-bearing).
type TagPatch struct {
	Label   *string
	LinkID  *string
	GroupID *string
	StoreID *string
	UIDHint *string
}

// PatchTag validates every provided reference inside the tenant, then applies
// the change in one UPDATE. Rebinding the link is how a tag changes campaign
// (换绑活动); the URL follows the new link's code everywhere it is derived.
func (s *Store) PatchTag(id, tenantID string, p TagPatch) (NfcTagView, error) {
	cur, err := s.GetTagView(id, tenantID)
	if err != nil {
		return NfcTagView{}, err
	}
	if p.LinkID != nil && *p.LinkID != cur.LinkID {
		l, err := s.GetLink(*p.LinkID, tenantID)
		if err != nil {
			return NfcTagView{}, fmt.Errorf("store: link %s: %w", *p.LinkID, err)
		}
		cur.LinkID = l.ID
		cur.Code = l.Code
		cur.CampaignID = l.CampaignID
	}
	if p.GroupID != nil && *p.GroupID != cur.GroupID {
		if *p.GroupID != "" {
			if _, err := s.GetTagGroup(*p.GroupID, tenantID); err != nil {
				return NfcTagView{}, fmt.Errorf("store: group %s: %w", *p.GroupID, err)
			}
		}
		cur.GroupID = *p.GroupID
	}
	if p.StoreID != nil && *p.StoreID != cur.StoreID {
		if *p.StoreID != "" {
			if _, err := s.GetStore(*p.StoreID, tenantID); err != nil {
				return NfcTagView{}, fmt.Errorf("store: store %s: %w", *p.StoreID, err)
			}
		}
		cur.StoreID = *p.StoreID
	}
	if p.Label != nil {
		cur.Label = strings.TrimSpace(*p.Label)
	}
	if p.UIDHint != nil {
		cur.UIDHint = strings.TrimSpace(*p.UIDHint)
	}
	cur.UpdatedAt = now()
	_, err = s.DB.Exec(
		`UPDATE nfc_tags SET label=?,link_id=?,group_id=?,store_id=?,uid_hint=?,updated_at=? WHERE id=? AND tenant_id=?`,
		cur.Label, cur.LinkID, nullable(cur.GroupID), nullable(cur.StoreID), nullable(cur.UIDHint),
		cur.UpdatedAt, id, tenantID)
	if err != nil {
		return NfcTagView{}, err
	}
	return s.GetTagView(id, tenantID)
}

// DeleteTag removes the management record only: the campaign link (short code)
// is untouched and keeps its existing lifecycle.
func (s *Store) DeleteTag(id, tenantID string) error {
	res, err := s.DB.Exec(`DELETE FROM nfc_tags WHERE id=? AND tenant_id=?`, id, tenantID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
