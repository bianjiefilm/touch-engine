// store_agency.go: HUI-1675 FEAT-0176 代理与子账号的存储层。
//
// 纪律:
//   - 代管关系只追加历史:解除 = 墓碑(revoked_at/revoked_by),绝不物理删除;
//     同 (tenant, agent) 至多一条激活(0008 部分唯一索引兜底);
//   - 代理权威 = 激活关系:HasActiveAgencyRelation 是 authz fail-closed 的
//     数据源(任何错误一律按「无激活关系」处理,绝不放宽);
//   - 解除代管同时停用 agent 成员行 —— 可见性立即收窄的存储半边(另一半在
//     authz 的 AgencyActive 裁决);停用只触 agent 行,别角色零波及;
//   - 代开留痕 append-only:子账号成员行与留痕行同事务落库,失败零残留;
//   - 子账号角色白名单 = staff / store_manager(代理永不铸造 org_owner/agent),
//     store 侧为最后一道闸,处理层先行校验。
package store

import (
	"database/sql"
	"errors"
)

// Sentinel errors (handler 层映射为独立 HTTP 原因码).
var (
	// ErrAgencyAlreadyRevoked: 解除已解除的关系(显式拒绝,绝不静默二次处理)。
	ErrAgencyAlreadyRevoked = errors.New("store: agency relation already revoked")
	// ErrAgentMemberConflict: 目标主体已有非 agent 成员行,拒绝改角色。
	ErrAgentMemberConflict = errors.New("store: principal already a non-agent member of this tenant")
	// ErrMemberExists: 目标主体已在本租户(开子账号唯一性冲突)。
	ErrMemberExists = errors.New("store: member already exists in this tenant")
	// ErrSubAccountRole: 子账号角色不在白名单(staff/store_manager)。
	ErrSubAccountRole = errors.New("store: sub-account role must be staff or store_manager")
	// ErrSubAccountScope: 子账号角色与门店作用域配对非法。
	ErrSubAccountScope = errors.New("store: sub-account store scope does not match its role")
)

// ---- agency relations(代管关系行)---------------------------------------------

type AgencyRelation struct {
	ID             string `json:"id"`
	TenantID       string `json:"tenant_id"`
	AgentPrincipal string `json:"agent_principal"`
	Note           string `json:"note,omitempty"`
	EstablishedBy  string `json:"established_by"`
	EstablishedAt  string `json:"established_at"`
	RevokedBy      string `json:"revoked_by,omitempty"`
	RevokedAt      string `json:"revoked_at,omitempty"`
}

const agencyRelCols = `id,tenant_id,agent_principal,note,established_by,established_at,revoked_by,revoked_at`

func scanAgencyRel(sc interface{ Scan(...any) error }) (AgencyRelation, error) {
	var r AgencyRelation
	var revokedAt sql.NullString
	if err := sc.Scan(&r.ID, &r.TenantID, &r.AgentPrincipal, &r.Note, &r.EstablishedBy, &r.EstablishedAt, &r.RevokedBy, &revokedAt); err != nil {
		return AgencyRelation{}, err
	}
	r.RevokedAt = revokedAt.String
	return r, nil
}

// CreateAgencyRelation mints a NEW relation row. A still-active relation for
// the same (tenant, principal) is refused here (the HTTP layer idempotizes
// before calling); a revoked history never blocks re-establishment.
func (s *Store) CreateAgencyRelation(tenantID, agentPrincipal, establishedBy, note string) (AgencyRelation, error) {
	r := AgencyRelation{
		ID: newID("agr_"), TenantID: tenantID, AgentPrincipal: agentPrincipal,
		Note: note, EstablishedBy: establishedBy, EstablishedAt: now(),
	}
	_, err := s.DB.Exec(
		`INSERT INTO agency_relations(`+agencyRelCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		r.ID, r.TenantID, r.AgentPrincipal, r.Note, r.EstablishedBy, r.EstablishedAt, "", nil)
	if err != nil {
		return AgencyRelation{}, err
	}
	return r, nil
}

func (s *Store) GetAgencyRelation(id string) (AgencyRelation, error) {
	row := s.DB.QueryRow(`SELECT `+agencyRelCols+` FROM agency_relations WHERE id=?`, id)
	r, err := scanAgencyRel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AgencyRelation{}, ErrNotFound
	}
	return r, err
}

// GetActiveAgencyRelation resolves the ONE active relation for the pair.
func (s *Store) GetActiveAgencyRelation(tenantID, agentPrincipal string) (AgencyRelation, error) {
	row := s.DB.QueryRow(
		`SELECT `+agencyRelCols+` FROM agency_relations WHERE tenant_id=? AND agent_principal=? AND revoked_at IS NULL`,
		tenantID, agentPrincipal)
	r, err := scanAgencyRel(row)
	if errors.Is(err, sql.ErrNoRows) {
		return AgencyRelation{}, ErrNotFound
	}
	return r, err
}

// HasActiveAgencyRelation is the fail-closed data source for the authz
// AgencyActive backfill: any error answers false.
func (s *Store) HasActiveAgencyRelation(tenantID, agentPrincipal string) bool {
	var n int
	if err := s.DB.QueryRow(
		`SELECT COUNT(1) FROM agency_relations WHERE tenant_id=? AND agent_principal=? AND revoked_at IS NULL`,
		tenantID, agentPrincipal).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

func (s *Store) ListAgencyRelationsByTenant(tenantID string) ([]AgencyRelation, error) {
	return s.listAgencyRels(`SELECT `+agencyRelCols+` FROM agency_relations WHERE tenant_id=? ORDER BY established_at`, tenantID)
}

// ListActiveAgencyRelationsByAgent is the agent's portfolio (its managed
// merchants; active rows only — revoked relations are invisible to the agent).
func (s *Store) ListActiveAgencyRelationsByAgent(agentPrincipal string) ([]AgencyRelation, error) {
	return s.listAgencyRels(
		`SELECT `+agencyRelCols+` FROM agency_relations WHERE agent_principal=? AND revoked_at IS NULL ORDER BY established_at`,
		agentPrincipal)
}

func (s *Store) listAgencyRels(q string, arg string) ([]AgencyRelation, error) {
	rows, err := s.DB.Query(q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]AgencyRelation, 0)
	for rows.Next() {
		r, err := scanAgencyRel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RevokeAgencyRelation tombstones the relation (append-only history). The
// tenant is mandatory: a foreign relation is indistinguishable from a missing
// one (ErrNotFound, 掩码语义). Double revoke is an explicit conflict.
func (s *Store) RevokeAgencyRelation(id, tenantID, revokedBy string) (AgencyRelation, error) {
	cur, err := s.GetAgencyRelation(id)
	if err != nil {
		return AgencyRelation{}, err
	}
	if cur.TenantID != tenantID {
		return AgencyRelation{}, ErrNotFound
	}
	if cur.RevokedAt != "" {
		return AgencyRelation{}, ErrAgencyAlreadyRevoked
	}
	cur.RevokedBy, cur.RevokedAt = revokedBy, now()
	_, err = s.DB.Exec(`UPDATE agency_relations SET revoked_by=?,revoked_at=? WHERE id=? AND tenant_id=? AND revoked_at IS NULL`,
		cur.RevokedBy, cur.RevokedAt, cur.ID, cur.TenantID)
	if err != nil {
		return AgencyRelation{}, err
	}
	return cur, nil
}

// ---- agent member rows(既有 members 体系内的代理角色行)-----------------------

// EnsureAgentMember creates (or re-enables) the agent member row for the pair.
// It is the ONLY sanctioned creation path for role=agent rows; an existing
// non-agent membership is never re-roled (ErrAgentMemberConflict).
func (s *Store) EnsureAgentMember(tenantID, agentPrincipal, displayName, createdBy string) (Member, error) {
	cur, err := s.GetMemberByPrincipal(tenantID, agentPrincipal)
	if err == nil {
		if cur.Role != "agent" {
			return Member{}, ErrAgentMemberConflict
		}
		if !cur.Enabled {
			return s.UpdateMember(cur.ID, nil, boolPtr(true), nil)
		}
		return cur, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Member{}, err
	}
	return s.CreateMemberScoped(tenantID, agentPrincipal, "agent", displayName, createdBy, true, "")
}

// DisableAgentMember flips enabled=0 on the AGENT row of the pair only
// (non-agent rows are untouched; absent rows are a no-op). Used by revoke so
// visibility narrows immediately at the membership gate too.
func (s *Store) DisableAgentMember(tenantID, agentPrincipal string) error {
	_, err := s.DB.Exec(`UPDATE members SET enabled=0,updated_at=? WHERE tenant_id=? AND principal_ref=? AND role='agent'`,
		now(), tenantID, agentPrincipal)
	return err
}

// ---- sub-accounts(代开的成员)+ trail(代开留痕)--------------------------------

type SubAccountTrail struct {
	ID                string `json:"id"`
	TenantID          string `json:"tenant_id"`
	MemberID          string `json:"member_id"`
	AgencyRelationID  string `json:"agency_relation_id,omitempty"`
	IssuedByPrincipal string `json:"issued_by_principal"`
	IssuedByMember    string `json:"issued_by_member"`
	CreatedAt         string `json:"created_at"`
	// joined member snapshot(留痕回查直接可读,无需二次查询)
	MemberPrincipalRef string `json:"member_principal_ref"`
	MemberRole         string `json:"member_role"`
	MemberDisplayName  string `json:"member_display_name"`
	MemberEnabled      bool   `json:"member_enabled"`
}

type IssueSubAccountInput struct {
	TenantID     string
	PrincipalRef string
	Role         string // 白名单:staff / store_manager
	DisplayName  string
	StoreScope   string // 仅 store_manager 允许非空(存在性由处理层校验)
	CreatedBy    string // 操作者的 members 行主体(留痕用)
	Enabled      bool
	// 代开留痕:AgencyRelationID/IssuedByPrincipal 为空 = owner 直开
	// (留痕行保留,但绝不冒充代理身份)。
	AgencyRelationID  string
	IssuedByPrincipal string
}

// IssueSubAccount creates the sub-account member row and its trail row in ONE
// transaction (失败零残留). The role whitelist and scope pairing are enforced
// here as the last gate; the handler validates them first for better codes.
func (s *Store) IssueSubAccount(in IssueSubAccountInput) (Member, SubAccountTrail, error) {
	// role whitelist: 代理永不铸造 org_owner/agent(legacy "owner" 也不收)
	if in.Role != "staff" && in.Role != "store_manager" {
		return Member{}, SubAccountTrail{}, ErrSubAccountRole
	}
	// scope pairing(与 provision/handleMemberCreate 同一不变量)
	if in.Role == "store_manager" && in.StoreScope == "" {
		return Member{}, SubAccountTrail{}, ErrSubAccountScope
	}
	if in.Role != "store_manager" && in.StoreScope != "" {
		return Member{}, SubAccountTrail{}, ErrSubAccountScope
	}
	// uniqueness pre-check for a clean sentinel (the UNIQUE constraint stays as
	// the concurrency backstop)
	if _, err := s.GetMemberByPrincipal(in.TenantID, in.PrincipalRef); err == nil {
		return Member{}, SubAccountTrail{}, ErrMemberExists
	} else if !errors.Is(err, ErrNotFound) {
		return Member{}, SubAccountTrail{}, err
	}

	tx, err := s.DB.Begin()
	if err != nil {
		return Member{}, SubAccountTrail{}, err
	}
	defer tx.Rollback()

	m := Member{
		ID: newID("mem_"), TenantID: in.TenantID, PrincipalRef: in.PrincipalRef,
		Role: in.Role, Enabled: in.Enabled, DisplayName: in.DisplayName,
		StoreScope: in.StoreScope,
		CreatedBy:  in.CreatedBy, CreatedAt: now(), UpdatedAt: now(),
	}
	if _, err := tx.Exec(
		`INSERT INTO members(id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
		 VALUES(?,?,?,?,?,?,?,?,?,?)`,
		m.ID, m.TenantID, m.PrincipalRef, m.Role, nullable(m.StoreScope), boolInt(m.Enabled), m.DisplayName, m.CreatedBy, m.CreatedAt, m.UpdatedAt); err != nil {
		return Member{}, SubAccountTrail{}, err
	}

	tr := SubAccountTrail{
		ID: newID("sat_"), TenantID: in.TenantID, MemberID: m.ID,
		AgencyRelationID: in.AgencyRelationID, IssuedByPrincipal: in.IssuedByPrincipal,
		IssuedByMember: in.CreatedBy, CreatedAt: now(),
	}
	if _, err := tx.Exec(
		`INSERT INTO agency_subaccount_trail(id,tenant_id,member_id,agency_rel_id,issued_by_principal,issued_by_member,created_at)
		 VALUES(?,?,?,?,?,?,?)`,
		tr.ID, tr.TenantID, tr.MemberID, nullable(tr.AgencyRelationID), tr.IssuedByPrincipal, tr.IssuedByMember, tr.CreatedAt); err != nil {
		return Member{}, SubAccountTrail{}, err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, SubAccountTrail{}, err
	}
	return m, tr, nil
}

// ListSubAccountTrail lists the issuance trail of a tenant, newest first.
// issuedByPrincipal "" = owner view (all rows); non-empty = agent view
// (strictly its own issues — the filter is a bound parameter, never client
// free-text beyond the resolved principal).
func (s *Store) ListSubAccountTrail(tenantID, issuedByPrincipal string) ([]SubAccountTrail, error) {
	q := `SELECT t.id,t.tenant_id,t.member_id,t.agency_rel_id,t.issued_by_principal,t.issued_by_member,t.created_at,
	             m.principal_ref,m.role,m.display_name,m.enabled
	      FROM agency_subaccount_trail t JOIN members m ON m.id = t.member_id
	      WHERE t.tenant_id=?`
	args := []any{tenantID}
	if issuedByPrincipal != "" {
		q += ` AND t.issued_by_principal=?`
		args = append(args, issuedByPrincipal)
	}
	q += ` ORDER BY t.created_at DESC, t.id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SubAccountTrail, 0)
	for rows.Next() {
		var t SubAccountTrail
		var relID sql.NullString
		var enabled int
		if err := rows.Scan(&t.ID, &t.TenantID, &t.MemberID, &relID, &t.IssuedByPrincipal, &t.IssuedByMember, &t.CreatedAt,
			&t.MemberPrincipalRef, &t.MemberRole, &t.MemberDisplayName, &enabled); err != nil {
			return nil, err
		}
		t.AgencyRelationID = relID.String
		t.MemberEnabled = enabled == 1
		out = append(out, t)
	}
	return out, rows.Err()
}

func boolPtr(b bool) *bool { return &b }
