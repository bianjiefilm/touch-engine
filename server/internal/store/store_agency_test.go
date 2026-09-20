package store

import (
	"errors"
	"testing"
)

// HUI-1675 FEAT-0176 代理与子账号:存储层(代管关系行 / 代理成员行 / 代开留痕)。
// 全部走真实 sqlite(embedded migrations,含 0008)。
//
// 纪律:
//   - 代管关系只追加:解除 = 墓碑(revoked_at/revoked_by),不物理删除;
//     同 (tenant, agent) 至多一条激活;再建立 = 新行(历史可回查);
//   - 代理权威 = 激活关系;解除即禁用 agent 成员行(可见性立即收窄的存储半边,
//     另一半在 authz 的 AgencyActive 裁决);
//   - 代开留痕 append-only:子账号成员行与留痕行同事务落库(失败零残留);
//   - 子账号角色白名单 = staff / store_manager(代理永不铸造 owner/agent)。

func TestAgencyRelationLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	rel, err := s.CreateAgencyRelation(ctx.tenA.ID, "usr_ag1", ctx.ownA.ID, "年度代运营")
	must(t, err)
	if rel.ID == "" || rel.TenantID != ctx.tenA.ID || rel.AgentPrincipal != "usr_ag1" ||
		rel.EstablishedBy != ctx.ownA.ID || rel.EstablishedAt == "" || rel.RevokedAt != "" {
		t.Fatalf("relation row incomplete: %+v", rel)
	}

	// active lookup (both faces) + has-active
	got, err := s.GetActiveAgencyRelation(ctx.tenA.ID, "usr_ag1")
	must(t, err)
	if got.ID != rel.ID {
		t.Fatalf("active lookup = %+v want %s", got, rel.ID)
	}
	if !s.HasActiveAgencyRelation(ctx.tenA.ID, "usr_ag1") {
		t.Fatal("HasActiveAgencyRelation = false, want true")
	}
	byAgent, err := s.ListActiveAgencyRelationsByAgent("usr_ag1")
	must(t, err)
	if len(byAgent) != 1 || byAgent[0].ID != rel.ID {
		t.Fatalf("agent portfolio = %+v, want exactly the relation", byAgent)
	}
	// other tenant/agent pairs see nothing
	if s.HasActiveAgencyRelation(ctx.tenA.ID, "usr_nobody") {
		t.Fatal("unknown agent must not be active")
	}
	if s.HasActiveAgencyRelation(ctx.tenB.ID, "usr_ag1") {
		t.Fatal("relation must be tenant-bound")
	}

	// duplicate active create is refused at store level (handler idempotizes first)
	if _, err := s.CreateAgencyRelation(ctx.tenA.ID, "usr_ag1", ctx.ownA.ID, ""); err == nil {
		t.Fatal("duplicate active relation must be refused (partial unique index)")
	}

	// revoke = tombstone, immediate narrowing, strict re-revoke
	rev, err := s.RevokeAgencyRelation(rel.ID, ctx.tenA.ID, ctx.ownA.ID)
	must(t, err)
	if rev.RevokedAt == "" || rev.RevokedBy != ctx.ownA.ID {
		t.Fatalf("revoke trail missing: %+v", rev)
	}
	if s.HasActiveAgencyRelation(ctx.tenA.ID, "usr_ag1") {
		t.Fatal("relation must be inactive right after revoke")
	}
	if _, err := s.GetActiveAgencyRelation(ctx.tenA.ID, "usr_ag1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("active lookup after revoke = %v, want ErrNotFound", err)
	}
	if _, err := s.RevokeAgencyRelation(rel.ID, ctx.tenA.ID, ctx.ownA.ID); !errors.Is(err, ErrAgencyAlreadyRevoked) {
		t.Fatalf("double revoke = %v, want ErrAgencyAlreadyRevoked", err)
	}

	// cross-tenant revoke is invisible (mask at store level)
	if _, err := s.RevokeAgencyRelation(rel.ID, ctx.tenB.ID, ctx.ownB.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant revoke = %v, want ErrNotFound", err)
	}

	// re-establish = NEW row; full history queryable for the tenant
	rel2, err := s.CreateAgencyRelation(ctx.tenA.ID, "usr_ag1", ctx.ownA.ID, "复签")
	must(t, err)
	if rel2.ID == rel.ID {
		t.Fatal("re-establish must create a new relation row (append-only history)")
	}
	all, err := s.ListAgencyRelationsByTenant(ctx.tenA.ID)
	must(t, err)
	if len(all) != 2 {
		t.Fatalf("tenant history = %d rows, want 2", len(all))
	}
}

func TestEnsureAgentMember(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	// create path: role=agent, enabled, no store scope
	m1, err := s.EnsureAgentMember(ctx.tenA.ID, "usr_ag1", "服务商A", ctx.ownA.ID)
	must(t, err)
	if m1.Role != "agent" || !m1.Enabled || m1.StoreScope != "" || m1.CreatedBy != ctx.ownA.ID {
		t.Fatalf("agent member row wrong: %+v", m1)
	}

	// idempotent re-ensure on an enabled agent row returns the same row
	m2, err := s.EnsureAgentMember(ctx.tenA.ID, "usr_ag1", "服务商A", ctx.ownA.ID)
	must(t, err)
	if m2.ID != m1.ID {
		t.Fatalf("re-ensure must not duplicate: %s vs %s", m2.ID, m1.ID)
	}

	// a non-agent membership is never silently re-roled
	if _, err := s.EnsureAgentMember(ctx.tenA.ID, ctx.ownA.PrincipalRef, "x", ctx.ownA.ID); !errors.Is(err, ErrAgentMemberConflict) {
		t.Fatalf("ensure over org_owner row = %v, want ErrAgentMemberConflict", err)
	}

	// disabled agent row (post-revoke) is re-enabled by a new establish
	if err := s.DisableAgentMember(ctx.tenA.ID, "usr_ag1"); err != nil {
		t.Fatalf("disable: %v", err)
	}
	m3, err := s.EnsureAgentMember(ctx.tenA.ID, "usr_ag1", "服务商A", ctx.ownA.ID)
	must(t, err)
	if !m3.Enabled {
		t.Fatal("re-establish must re-enable the agent member row")
	}

	// disable is scoped to (tenant, principal)
	if _, err := s.CreateMember(ctx.tenB.ID, "usr_ag1", "staff", "别处职员", "seed", true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.DisableAgentMember(ctx.tenB.ID, "usr_ag1"); err != nil {
		t.Fatalf("disable must be a no-op for non-agent rows in scope: %v", err)
	}
	kept, err := s.GetMemberByPrincipal(ctx.tenB.ID, "usr_ag1")
	must(t, err)
	if !kept.Enabled {
		t.Fatal("disable must only touch agent rows")
	}
}

func TestIssueSubAccountTrail(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	rel, err := s.CreateAgencyRelation(ctx.tenA.ID, "usr_ag1", ctx.ownA.ID, "")
	must(t, err)

	mem, trail, err := s.IssueSubAccount(IssueSubAccountInput{
		TenantID: ctx.tenA.ID, PrincipalRef: "usr_sub1", Role: "staff",
		DisplayName: "商家子账号1", CreatedBy: "mem_agent", Enabled: true,
		AgencyRelationID: rel.ID, IssuedByPrincipal: "usr_ag1",
	})
	must(t, err)
	if mem.Role != "staff" || !mem.Enabled || mem.TenantID != ctx.tenA.ID {
		t.Fatalf("sub-account member wrong: %+v", mem)
	}
	if trail.MemberID != mem.ID || trail.IssuedByPrincipal != "usr_ag1" ||
		trail.AgencyRelationID != rel.ID || trail.TenantID != ctx.tenA.ID || trail.CreatedAt == "" {
		t.Fatalf("trail row incomplete: %+v", trail)
	}

	// store_manager sub-account requires a scope of the tenant
	if _, _, err := s.IssueSubAccount(IssueSubAccountInput{
		TenantID: ctx.tenA.ID, PrincipalRef: "usr_sub2", Role: "store_manager",
		CreatedBy: "mem_agent", Enabled: true,
		AgencyRelationID: rel.ID, IssuedByPrincipal: "usr_ag1",
	}); !errors.Is(err, ErrSubAccountScope) {
		t.Fatalf("manager without scope = %v, want ErrSubAccountScope", err)
	}

	// role whitelist: agent can never mint owner/agent sub-accounts
	for _, bad := range []string{"org_owner", "agent", "owner", "intern"} {
		if _, _, err := s.IssueSubAccount(IssueSubAccountInput{
			TenantID: ctx.tenA.ID, PrincipalRef: "usr_sub3", Role: bad,
			CreatedBy: "mem_agent", Enabled: true,
			AgencyRelationID: rel.ID, IssuedByPrincipal: "usr_ag1",
		}); !errors.Is(err, ErrSubAccountRole) {
			t.Fatalf("role %q = %v, want ErrSubAccountRole", bad, err)
		}
	}

	// duplicate principal in tenant: atomic refusal, zero trail residue
	if _, _, err := s.IssueSubAccount(IssueSubAccountInput{
		TenantID: ctx.tenA.ID, PrincipalRef: ctx.ownA.PrincipalRef, Role: "staff",
		CreatedBy: "mem_agent", Enabled: true,
		AgencyRelationID: rel.ID, IssuedByPrincipal: "usr_ag1",
	}); !errors.Is(err, ErrMemberExists) {
		t.Fatalf("duplicate principal = %v, want ErrMemberExists", err)
	}
	rows, err := s.ListSubAccountTrail(ctx.tenA.ID, "")
	must(t, err)
	if len(rows) != 1 {
		t.Fatalf("trail must stay append-only on failure: %d rows, want 1", len(rows))
	}

	// scope pairing the other way: staff must not carry a scope
	if _, _, err := s.IssueSubAccount(IssueSubAccountInput{
		TenantID: ctx.tenA.ID, PrincipalRef: "usr_sub4", Role: "staff", StoreScope: "sto_x",
		CreatedBy: "mem_agent", Enabled: true,
		AgencyRelationID: rel.ID, IssuedByPrincipal: "usr_ag1",
	}); !errors.Is(err, ErrSubAccountScope) {
		t.Fatalf("staff with scope = %v, want ErrSubAccountScope", err)
	}

	// owner-direct issue: trail keeps the record with empty agent identity
	_, trail2, err := s.IssueSubAccount(IssueSubAccountInput{
		TenantID: ctx.tenA.ID, PrincipalRef: "usr_sub5", Role: "staff",
		CreatedBy: ctx.ownA.ID, Enabled: true,
	})
	must(t, err)
	if trail2.IssuedByPrincipal != "" || trail2.AgencyRelationID != "" {
		t.Fatalf("owner-direct trail must not borrow an agent identity: %+v", trail2)
	}

	// trail listing: owner sees all; agent sees only its own issues
	if _, _, err := s.IssueSubAccount(IssueSubAccountInput{
		TenantID: ctx.tenA.ID, PrincipalRef: "usr_sub6", Role: "staff",
		CreatedBy: "mem_other_agent", Enabled: true,
		AgencyRelationID: rel.ID, IssuedByPrincipal: "usr_ag9",
	}); err != nil {
		t.Fatalf("issue usr_sub6: %v", err)
	}
	all, err := s.ListSubAccountTrail(ctx.tenA.ID, "")
	must(t, err)
	if len(all) != 3 {
		t.Fatalf("owner trail view = %d rows, want 3", len(all))
	}
	own, err := s.ListSubAccountTrail(ctx.tenA.ID, "usr_ag1")
	must(t, err)
	if len(own) != 1 || own[0].IssuedByPrincipal != "usr_ag1" {
		t.Fatalf("agent trail view = %+v, want exactly its own issue", own)
	}
	// tenant scoping of the trail
	otherTenant, err := s.ListSubAccountTrail(ctx.tenB.ID, "")
	must(t, err)
	if len(otherTenant) != 0 {
		t.Fatalf("trail must be tenant-scoped, got %d rows in tenant B", len(otherTenant))
	}
}
