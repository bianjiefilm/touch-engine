package authz

import "testing"

var (
	ownerA    = &Member{ID: "mem_a1", TenantID: "tnt_A", PrincipalRef: "usr_a1", Role: RoleOrgOwner, Enabled: true}
	staffA    = &Member{ID: "mem_a2", TenantID: "tnt_A", PrincipalRef: "usr_a2", Role: RoleStaff, Enabled: true}
	ownerB    = &Member{ID: "mem_b1", TenantID: "tnt_B", PrincipalRef: "usr_b1", Role: RoleOrgOwner, Enabled: true}
	disabledA = &Member{ID: "mem_a3", TenantID: "tnt_A", PrincipalRef: "usr_a3", Role: RoleStaff, Enabled: false}
	// HUI-1674: store managers of store S1 / S2 in tenant A.
	mgrA_S1 = &Member{ID: "mem_a4", TenantID: "tnt_A", PrincipalRef: "usr_a4", Role: RoleStoreManager, StoreScope: "sto_S1", Enabled: true}
	mgrA_S2 = &Member{ID: "mem_a5", TenantID: "tnt_A", PrincipalRef: "usr_a5", Role: RoleStoreManager, StoreScope: "sto_S2", Enabled: true}
	// malformed: manager row without a scope must fail closed everywhere
	mgrA_NoScope = &Member{ID: "mem_a6", TenantID: "tnt_A", PrincipalRef: "usr_a6", Role: RoleStoreManager, Enabled: true}
)

func TestMatrix(t *testing.T) {
	tenantA := RecordScope{TenantID: "tnt_A"}
	tenantB := RecordScope{TenantID: "tnt_B"}

	type tc struct {
		name    string
		member  *Member
		action  Action
		rec     RecordScope
		allowed bool
		reason  string
	}
	cases := []tc{
		{"owner A creates in A", ownerA, ActionCreate, tenantA, true, ""},
		{"staff A creates in A", staffA, ActionCreate, tenantA, true, ""},
		{"owner A reads list A", ownerA, ActionReadList, tenantA, true, ""},
		{"staff A reads list A", staffA, ActionReadList, tenantA, true, ""},
		{"owner A updates A", ownerA, ActionUpdate, tenantA, true, ""},
		{"staff A updates A (pause/resume)", staffA, ActionUpdate, tenantA, true, ""},
		{"owner A manages members", ownerA, ActionManageMembers, tenantA, true, ""},
		{"staff A cannot manage members", staffA, ActionManageMembers, tenantA, false, ReasonForbidden},

		// HUI-1664: QR download is an owner-only export surface
		{"owner A exports QR", ownerA, ActionExportQR, tenantA, true, ""},
		{"staff A cannot export QR", staffA, ActionExportQR, tenantA, false, ReasonForbidden},

		// HUI-1665: NFC tag management (CRUD/batch/export) is owner-only
		{"owner A manages tags", ownerA, ActionManageTags, tenantA, true, ""},
		{"staff A cannot manage tags", staffA, ActionManageTags, tenantA, false, ReasonForbidden},

		// HUI-1674: store governance is org_owner-only; store-scoped records in
		// the org_owner path ignore StoreID (HQ sees every store).
		{"owner A manages stores", ownerA, ActionManageStores, tenantA, true, ""},
		{"staff A cannot manage stores", staffA, ActionManageStores, tenantA, false, ReasonForbidden},
		{"owner A reads store record", ownerA, ActionReadRecord, RecordScope{TenantID: "tnt_A", StoreID: "sto_S9"}, true, ""},
		{"owner A updates store-bound record", ownerA, ActionUpdate, RecordScope{TenantID: "tnt_A", StoreID: "sto_S1"}, true, ""},

		// cross-tenant: B has no membership in A, and vice versa
		{"owner B create in A refused", ownerB, ActionCreate, tenantA, false, ReasonCrossTenant},
		{"owner B read list A refused", ownerB, ActionReadList, tenantA, false, ReasonCrossTenant},
		{"owner B read record A refused", ownerB, ActionReadRecord, tenantA, false, ReasonCrossTenant},
		{"owner B update A refused", ownerB, ActionUpdate, tenantA, false, ReasonCrossTenant},
		{"owner B manage members A refused", ownerB, ActionManageMembers, tenantA, false, ReasonCrossTenant},
		{"owner B export A QR refused", ownerB, ActionExportQR, tenantA, false, ReasonCrossTenant},
		{"owner B manage A tags refused", ownerB, ActionManageTags, tenantA, false, ReasonCrossTenant},
		{"owner A create in B refused", ownerA, ActionCreate, tenantB, false, ReasonCrossTenant},

		// disabled member: denied for everything, own tenant included
		{"disabled create refused", disabledA, ActionCreate, tenantA, false, ReasonDisabled},
		{"disabled read refused", disabledA, ActionReadList, tenantA, false, ReasonDisabled},
		{"disabled manage refused", disabledA, ActionManageMembers, tenantA, false, ReasonDisabled},

		// nil member (should never happen; guests are stopped at 401 earlier)
		{"nil member refused", nil, ActionReadList, tenantA, false, ReasonNotMember},
	}
	for _, c := range cases {
		d := Authorize(c.member, c.action, c.rec)
		if d.Allowed != c.allowed || d.Reason != c.reason {
			t.Fatalf("%s: got %+v want allowed=%v reason=%q", c.name, d, c.allowed, c.reason)
		}
	}
}

// TestStoreScopeMatrix pins the HUI-1674 作用域矩阵: store_manager acts only on
// records of its own store; everything else is out_of_scope (HTTP masks reads
// as 404). Malformed manager rows (no scope) fail closed.
func TestStoreScopeMatrix(t *testing.T) {
	ownS1 := RecordScope{TenantID: "tnt_A", StoreID: "sto_S1"}
	ownS2 := RecordScope{TenantID: "tnt_A", StoreID: "sto_S2"}
	hqLevel := RecordScope{TenantID: "tnt_A"} // tenant-level / unassigned record

	type tc struct {
		name    string
		member  *Member
		action  Action
		rec     RecordScope
		allowed bool
		reason  string
	}
	cases := []tc{
		// ---- store_manager on OWN store ----
		{"manager S1 creates in S1", mgrA_S1, ActionCreate, ownS1, true, ""},
		{"manager S1 reads list of S1", mgrA_S1, ActionReadList, ownS1, true, ""},
		{"manager S1 reads record of S1", mgrA_S1, ActionReadRecord, ownS1, true, ""},
		{"manager S1 updates S1", mgrA_S1, ActionUpdate, ownS1, true, ""},
		{"manager S1 manages tags of S1", mgrA_S1, ActionManageTags, ownS1, true, ""},

		// ---- store_manager on ANOTHER store: out_of_scope (maskable) ----
		{"manager S1 creates in S2 refused", mgrA_S1, ActionCreate, ownS2, false, ReasonOutOfScope},
		{"manager S1 reads list of S2 refused", mgrA_S1, ActionReadList, ownS2, false, ReasonOutOfScope},
		{"manager S1 reads record of S2 refused", mgrA_S1, ActionReadRecord, ownS2, false, ReasonOutOfScope},
		{"manager S1 updates S2 refused", mgrA_S1, ActionUpdate, ownS2, false, ReasonOutOfScope},
		{"manager S1 manages tags of S2 refused", mgrA_S1, ActionManageTags, ownS2, false, ReasonOutOfScope},

		// ---- store_manager on tenant-level / unassigned records: out_of_scope ----
		{"manager S1 creates unassigned campaign refused", mgrA_S1, ActionCreate, hqLevel, false, ReasonOutOfScope},
		{"manager S1 reads unassigned record refused", mgrA_S1, ActionReadRecord, hqLevel, false, ReasonOutOfScope},
		{"manager S1 updates unassigned refused", mgrA_S1, ActionUpdate, hqLevel, false, ReasonOutOfScope},
		{"manager S1 manages HQ tags refused", mgrA_S1, ActionManageTags, hqLevel, false, ReasonOutOfScope},

		// ---- store_manager never governs stores/members/QR ----
		{"manager cannot create store", mgrA_S1, ActionManageStores, ownS1, false, ReasonForbidden},
		{"manager cannot manage members", mgrA_S1, ActionManageMembers, ownS1, false, ReasonForbidden},
		{"manager cannot export QR", mgrA_S1, ActionExportQR, ownS1, false, ReasonForbidden},

		// ---- malformed manager rows fail closed ----
		{"scopeless manager record refused", mgrA_NoScope, ActionReadRecord, ownS1, false, ReasonOutOfScope},
		{"scopeless manager tags refused", mgrA_NoScope, ActionManageTags, ownS1, false, ReasonOutOfScope},

		// ---- cross-tenant still wins over scope ----
		{"manager S1 of B tenant record refused", mgrA_S1, ActionReadRecord, RecordScope{TenantID: "tnt_B", StoreID: "sto_S1"}, false, ReasonCrossTenant},

		// ---- disabled manager ----
		{"disabled manager refused", &Member{ID: "m", TenantID: "tnt_A", Role: RoleStoreManager, StoreScope: "sto_S1", Enabled: false}, ActionReadRecord, ownS1, false, ReasonDisabled},
	}
	for _, c := range cases {
		d := Authorize(c.member, c.action, c.rec)
		if d.Allowed != c.allowed || d.Reason != c.reason {
			t.Fatalf("%s: got %+v want allowed=%v reason=%q", c.name, d, c.allowed, c.reason)
		}
	}
}

func TestRoleValidity(t *testing.T) {
	if RoleOrgOwner.Valid() != true || RoleStaff.Valid() != true || RoleStoreManager.Valid() != true {
		t.Fatal("org_owner/store_manager/staff must be valid roles")
	}
	if Role("admin").Valid() || Role("").Valid() {
		t.Fatal("unknown roles must be invalid")
	}
	// Legacy "owner" is an input alias only: never valid as a stored role.
	if Role("owner").Valid() {
		t.Fatal("legacy owner must not be a valid stored role")
	}
}

func TestCanonicalRole(t *testing.T) {
	if r, ok := CanonicalRole(LegacyOwner); !ok || r != RoleOrgOwner {
		t.Fatalf("owner alias must map to org_owner, got %q ok=%v", r, ok)
	}
	for _, r := range []Role{RoleOrgOwner, RoleStoreManager, RoleStaff} {
		if got, ok := CanonicalRole(r); !ok || got != r {
			t.Fatalf("canonical role %q must pass through, got %q ok=%v", r, got, ok)
		}
	}
	if _, ok := CanonicalRole(Role("admin")); ok {
		t.Fatal("unknown role must be refused")
	}
}
