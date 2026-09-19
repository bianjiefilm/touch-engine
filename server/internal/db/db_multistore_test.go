package db

import (
	"path/filepath"
	"testing"
)

// TestMigration0004LegacyOwnerRowMigrates replays the pre-0004 schema with a
// legacy owner row and a pre-status stores table, then re-applies migration
// 0004 through the real Migrate path. It pins the 存量兼容 contract:
// owner → org_owner (语义等价), staff unchanged, store rows default active,
// and nothing is lost.
func TestMigration0004LegacyOwnerRowMigrates(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	// Rebuild the pre-0004 state: legacy members table (owner/staff CHECK, no
	// store_scope) with one owner + one staff row, and stores without status.
	steps := []string{
		`DROP TABLE members`,
		`CREATE TABLE members (
			id            TEXT PRIMARY KEY,
			tenant_id     TEXT NOT NULL REFERENCES tenants(id),
			principal_ref TEXT NOT NULL,
			role          TEXT NOT NULL CHECK (role IN ('owner','staff')),
			enabled       INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
			display_name  TEXT NOT NULL DEFAULT '',
			created_by    TEXT NOT NULL DEFAULT '',
			created_at    TEXT NOT NULL,
			updated_at    TEXT NOT NULL,
			UNIQUE (tenant_id, principal_ref)
		)`,
		`ALTER TABLE stores DROP COLUMN status`,
		`INSERT INTO tenants(id,name,created_at) VALUES('tnt_legacy','老商家','2026-01-01T00:00:00Z')`,
		`INSERT INTO stores(id,tenant_id,name,address,created_by,created_at,updated_at)
		 VALUES('sto_legacy','tnt_legacy','唯一门店','老地址','usr_x','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
		 VALUES('mem_o','tnt_legacy','usr_boss','owner',1,'老板','boot','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
		 VALUES('mem_s','tnt_legacy','usr_clerk','staff',1,'店员','boot','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`,
		// schema_migrations rows store the glob path including the directory
		`DELETE FROM schema_migrations WHERE name LIKE '%0004_multi_store.sql'`,
	}
	for _, q := range steps {
		if _, err := d.Exec(q); err != nil {
			t.Fatalf("legacy setup %q: %v", q, err)
		}
	}

	if err := Migrate(d); err != nil {
		t.Fatalf("re-migrate 0004: %v", err)
	}

	var role, scope string
	if err := d.QueryRow(`SELECT role,COALESCE(store_scope,'') FROM members WHERE id='mem_o'`).Scan(&role, &scope); err != nil {
		t.Fatalf("legacy owner row missing: %v", err)
	}
	if role != "org_owner" || scope != "" {
		t.Fatalf("legacy owner = role=%q scope=%q, want org_owner + empty scope", role, scope)
	}
	if err := d.QueryRow(`SELECT role,COALESCE(store_scope,'') FROM members WHERE id='mem_s'`).Scan(&role, &scope); err != nil {
		t.Fatalf("legacy staff row missing: %v", err)
	}
	if role != "staff" || scope != "" {
		t.Fatalf("legacy staff = role=%q scope=%q, want staff + empty scope", role, scope)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM stores WHERE id='sto_legacy'`).Scan(&status); err != nil {
		t.Fatalf("legacy store missing: %v", err)
	}
	if status != "active" {
		t.Fatalf("legacy store status = %q, want active (default)", status)
	}

	// The new CHECK refuses legacy/dropped role names: fail closed.
	if _, err := d.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
		VALUES('mem_bad','tnt_legacy','usr_bad','owner',1,'x','t','now','now')`); err == nil {
		t.Fatal("legacy role 'owner' must be refused by the new CHECK")
	}
	// store_scope FK is enforced.
	if _, err := d.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
		VALUES('mem_bad2','tnt_legacy','usr_bad2','store_manager','sto_missing',1,'x','t','now','now')`); err == nil {
		t.Fatal("store_scope FK must be enforced")
	}
}

// TestMembersStoreScopeNotNullInvariant: a store_manager row without scope is
// representable at the SQL layer (defensive) but the authz layer fails closed;
// the write paths refuse it. Here we only assert the column accepts NULL for
// org_owner/staff (总部/租户级成员无作用域).
func TestMembersStoreScopeNullForHqRoles(t *testing.T) {
	d, err := Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if _, err := d.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_1','t','now')`); err != nil {
		t.Fatalf("tenant: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,enabled,display_name,created_by,created_at,updated_at)
		VALUES('mem_1','tnt_1','usr_1','org_owner',1,'hq','t','now','now')`); err != nil {
		t.Fatalf("org_owner without scope must be insertable: %v", err)
	}
	var n int
	if err := d.QueryRow(`SELECT COUNT(1) FROM members WHERE id='mem_1' AND store_scope IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("store_scope should be NULL, n=%d err=%v", n, err)
	}
}
