package db

import (
	"path/filepath"
	"testing"
)

// HUI-1675 FEAT-0176 代理与子账号:migration 0008 的 additive 语义。
//
// 纪律:
//   - members 重建(0004 同款)只为把 role 白名单扩展 'agent';全部存量行/列
//     原样保留;
//   - 代管关系行(agency_relations)与代开留痕表(agency_subaccount_trail)
//     为全新表,既有表零改动;
//   - 同 (tenant_id, agent_principal) 至多一条激活关系(部分唯一索引)。

func TestOpenCreatesAgencySchema(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	got, err := TableNames(d)
	if err != nil {
		t.Fatalf("table names: %v", err)
	}
	want := map[string]bool{
		"agency_relations":        false,
		"agency_subaccount_trail": false,
	}
	for _, name := range got {
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Fatalf("table %s missing after migrations; got %v", name, got)
		}
	}
}

func TestMembersRoleAcceptsAgentOnly(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_x','X','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
		 VALUES('mem_x1','tnt_x','usr_ag1','agent',NULL,1,'代理','seed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("role=agent must be accepted by members CHECK: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
		 VALUES('mem_x2','tnt_x','usr_bad','superadmin',NULL,1,'x','seed','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err == nil {
		t.Fatal("unknown role must still be refused by members CHECK")
	}
}

func TestAgencyRelationActiveUniqueness(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_x','X','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	rel := func(id string) string {
		return `INSERT INTO agency_relations(id,tenant_id,agent_principal,note,established_by,established_at,revoked_by,revoked_at)
			 VALUES('` + id + `','tnt_x','usr_ag1','','owner','2026-01-01T00:00:00Z','',NULL)`
	}
	if _, err := d.Exec(rel("agr_1")); err != nil {
		t.Fatalf("first active relation: %v", err)
	}
	if _, err := d.Exec(rel("agr_2")); err == nil {
		t.Fatal("second ACTIVE relation for the same (tenant, agent) must be refused by the partial unique index")
	}
	// revoke the first: a new active relation for the same pair becomes legal
	if _, err := d.Exec(`UPDATE agency_relations SET revoked_by='owner', revoked_at='2026-01-02T00:00:00Z' WHERE id='agr_1'`); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := d.Exec(rel("agr_3")); err != nil {
		t.Fatalf("re-establish after revoke must be allowed: %v", err)
	}
}
