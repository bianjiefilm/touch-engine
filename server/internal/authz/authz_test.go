package authz

import "testing"

var (
	ownerA    = &Member{ID: "mem_a1", TenantID: "tnt_A", PrincipalRef: "usr_a1", Role: RoleOwner, Enabled: true}
	staffA    = &Member{ID: "mem_a2", TenantID: "tnt_A", PrincipalRef: "usr_a2", Role: RoleStaff, Enabled: true}
	ownerB    = &Member{ID: "mem_b1", TenantID: "tnt_B", PrincipalRef: "usr_b1", Role: RoleOwner, Enabled: true}
	disabledA = &Member{ID: "mem_a3", TenantID: "tnt_A", PrincipalRef: "usr_a3", Role: RoleStaff, Enabled: false}
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

func TestRoleValidity(t *testing.T) {
	if RoleOwner.Valid() != true || RoleStaff.Valid() != true {
		t.Fatal("owner/staff must be valid roles")
	}
	if Role("admin").Valid() || Role("").Valid() {
		t.Fatal("unknown roles must be invalid")
	}
}
