// Package provision holds the bootstrap/ops subcommands (provision-tenant,
// provision-member). They are operator tools, never reachable over HTTP.
package provision

import (
	"fmt"

	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func open(path string) (*store.Store, func(), error) {
	d, err := db.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return store.New(d), func() { d.Close() }, nil
}

// Tenant creates a tenant and prints its id.
func Tenant(dbPath, name string) (string, error) {
	s, closeFn, err := open(dbPath)
	if err != nil {
		return "", err
	}
	defer closeFn()
	t, err := s.CreateTenant(name)
	if err != nil {
		return "", err
	}
	return t.ID, nil
}

// Member attaches an existing platform principal to a tenant.
// principalRef must be an identity-derived usr_* id.
func Member(dbPath, tenantID, principalRef, role, displayName string, enabled bool) (string, error) {
	if len(principalRef) < 5 || principalRef[:4] != "usr_" {
		return "", fmt.Errorf("principal_ref must be an identity principal id (usr_*), got %q", principalRef)
	}
	if role != "owner" && role != "staff" {
		return "", fmt.Errorf("role must be owner or staff, got %q", role)
	}
	s, closeFn, err := open(dbPath)
	if err != nil {
		return "", err
	}
	defer closeFn()
	if _, err := s.GetTenant(tenantID); err != nil {
		return "", fmt.Errorf("tenant %s: %w", tenantID, err)
	}
	m, err := s.CreateMember(tenantID, principalRef, role, displayName, "provision", enabled)
	if err != nil {
		return "", err
	}
	return m.ID, nil
}
