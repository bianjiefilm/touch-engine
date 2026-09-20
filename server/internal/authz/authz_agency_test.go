package authz

import "testing"

// HUI-1675 FEAT-0176 代理与子账号:agent 角色的纯授权矩阵测试。
//
// 纪律(拍板 2/3):
//   - agent 是既有成员/角色体系内的代理角色;权威不在角色本身,而在激活的
//     代管关系行(Member.AgencyActive,由 HTTP 层按 agency_relations 单点回填)。
//   - 白名单 = 名下商家数据只读(read_list/read_record)+ 开子账号
//     (manage_agency_subaccounts);其余动作一律拒绝(fail-closed)。
//   - 无激活关系(AgencyActive=false)的 agent 行:一切拒绝(agency_inactive)。
//   - 跨租户先于角色裁决:agent 对无成员行租户 = cross_tenant(既有语义不变)。

var (
	// agent of tenant A with an ACTIVE agency relation
	agentA_active = &Member{ID: "mem_a7", TenantID: "tnt_A", PrincipalRef: "usr_ag1", Role: RoleAgent, Enabled: true, AgencyActive: true}
	// agent of tenant A whose relation is NOT active (revoked / not yet established)
	agentA_inactive = &Member{ID: "mem_a8", TenantID: "tnt_A", PrincipalRef: "usr_ag2", Role: RoleAgent, Enabled: true, AgencyActive: false}
	// agent row left disabled after revoke
	agentA_disabled = &Member{ID: "mem_a9", TenantID: "tnt_A", PrincipalRef: "usr_ag3", Role: RoleAgent, Enabled: false, AgencyActive: false}
)

func TestAgentRoleIsValidStoredRole(t *testing.T) {
	if !RoleAgent.Valid() {
		t.Fatal("agent must be a canonical stored role (FEAT-0176)")
	}
	if r, ok := CanonicalRole(Role("agent")); !ok || r != RoleAgent {
		t.Fatalf("CanonicalRole(agent) = %q,%v want agent,true", r, ok)
	}
}

func TestAgencyMatrix(t *testing.T) {
	tenA := RecordScope{TenantID: "tnt_A"}
	tenB := RecordScope{TenantID: "tnt_B"}
	storeRecA := RecordScope{TenantID: "tnt_A", StoreID: "sto_S1"}

	type tc struct {
		name    string
		member  *Member
		action  Action
		rec     RecordScope
		allowed bool
		reason  string
	}
	cases := []tc{
		// ---- 白名单内:名下商家数据只读 ----
		{"active agent reads list in managed tenant", agentA_active, ActionReadList, tenA, true, ""},
		{"active agent reads record in managed tenant", agentA_active, ActionReadRecord, tenA, true, ""},
		{"active agent reads store-bound record (tenant-wide read)", agentA_active, ActionReadRecord, storeRecA, true, ""},

		// ---- 白名单内:开子账号(代管理动作)----
		{"active agent issues sub-account", agentA_active, ActionManageAgencySubaccounts, tenA, true, ""},

		// ---- 白名单外:写/治理一律拒绝 ----
		{"active agent cannot create", agentA_active, ActionCreate, tenA, false, ReasonForbidden},
		{"active agent cannot update", agentA_active, ActionUpdate, tenA, false, ReasonForbidden},
		{"active agent cannot manage members", agentA_active, ActionManageMembers, tenA, false, ReasonForbidden},
		{"active agent cannot manage stores", agentA_active, ActionManageStores, tenA, false, ReasonForbidden},
		{"active agent cannot manage tags", agentA_active, ActionManageTags, tenA, false, ReasonForbidden},
		{"active agent cannot export QR", agentA_active, ActionExportQR, tenA, false, ReasonForbidden},
		{"active agent cannot manage campaign rules", agentA_active, ActionManageCampaignRules, tenA, false, ReasonForbidden},
		{"active agent cannot manage asset lib", agentA_active, ActionManageAssetLib, tenA, false, ReasonForbidden},
		{"active agent cannot manage video templates", agentA_active, ActionManageVideoTemplates, tenA, false, ReasonForbidden},

		// ---- 建立代管/解除代管:平台/owner 类(org_owner)专属 ----
		{"owner A establishes agency", ownerA, ActionManageAgency, tenA, true, ""},
		{"staff A cannot establish agency", staffA, ActionManageAgency, tenA, false, ReasonForbidden},
		{"active agent cannot establish agency", agentA_active, ActionManageAgency, tenA, false, ReasonForbidden},
		{"owner B establish A agency refused (cross tenant)", ownerB, ActionManageAgency, tenA, false, ReasonCrossTenant},

		// ---- 无激活关系:fail-closed,连只读都没有 ----
		{"inactive agent read list refused", agentA_inactive, ActionReadList, tenA, false, ReasonAgencyInactive},
		{"inactive agent read record refused", agentA_inactive, ActionReadRecord, tenA, false, ReasonAgencyInactive},
		{"inactive agent issue sub-account refused", agentA_inactive, ActionManageAgencySubaccounts, tenA, false, ReasonAgencyInactive},
		{"disabled agent member refused", agentA_disabled, ActionReadList, tenA, false, ReasonDisabled},

		// ---- 子账号(owner 直开面,org_owner 恒可)----
		{"owner A issues sub-account directly", ownerA, ActionManageAgencySubaccounts, tenA, true, ""},
		{"staff A cannot issue sub-account", staffA, ActionManageAgencySubaccounts, tenA, false, ReasonForbidden},
		{"store manager A cannot issue sub-account", mgrA_S1, ActionManageAgencySubaccounts, tenA, false, ReasonForbidden},
		{"owner B issue sub-account in A refused (cross tenant)", ownerB, ActionManageAgencySubaccounts, tenA, false, ReasonCrossTenant},

		// ---- agent 跨租户 fail-closed(既有成员行门槛先行)----
		{"active agent read foreign tenant refused", agentA_active, ActionReadList, tenB, false, ReasonCrossTenant},
		{"active agent read record foreign tenant refused", agentA_active, ActionReadRecord, tenB, false, ReasonCrossTenant},
		{"active agent issue sub-account foreign tenant refused", agentA_active, ActionManageAgencySubaccounts, tenB, false, ReasonCrossTenant},

		// ---- 既有语义回归:owner/staff/manager 行为逐字节不变 ----
		{"owner A still creates in A", ownerA, ActionCreate, tenA, true, ""},
		{"staff A still reads list A", staffA, ActionReadList, tenA, true, ""},
		{"manager S1 still reads own store", mgrA_S1, ActionReadRecord, storeRecA, true, ""},
		{"manager S2 out of store scope", mgrA_S2, ActionReadRecord, storeRecA, false, ReasonOutOfScope},
	}
	for _, c := range cases {
		d := Authorize(c.member, c.action, c.rec)
		if d.Allowed != c.allowed || d.Reason != c.reason {
			t.Errorf("%s: got allowed=%v reason=%q, want allowed=%v reason=%q",
				c.name, d.Allowed, d.Reason, c.allowed, c.reason)
		}
	}
}

func TestNilMemberStillDenied(t *testing.T) {
	if d := Authorize(nil, ActionManageAgency, RecordScope{TenantID: "tnt_A"}); d.Allowed {
		t.Fatal("nil member must be denied")
	}
}
