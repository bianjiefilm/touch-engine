// Package provision holds the bootstrap/ops subcommands (provision-tenant,
// provision-member). They are operator tools, never reachable over HTTP.
package provision

import (
	"fmt"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/authz"
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
//
// HUI-1674 roles: "owner" is accepted as a legacy alias for "org_owner";
// "store_manager" requires storeScope (a store of this tenant); org_owner and
// staff must carry an empty scope.
func Member(dbPath, tenantID, principalRef, role, displayName string, enabled bool, storeScope string) (string, error) {
	if len(principalRef) < 5 || principalRef[:4] != "usr_" {
		return "", fmt.Errorf("principal_ref must be an identity principal id (usr_*), got %q", principalRef)
	}
	canonical, ok := authz.CanonicalRole(authz.Role(role))
	if !ok {
		return "", fmt.Errorf("role must be org_owner (or legacy owner), store_manager or staff, got %q", role)
	}
	role = string(canonical)
	storeScope = strings.TrimSpace(storeScope)
	if role == "store_manager" {
		if storeScope == "" {
			return "", fmt.Errorf("store_manager requires -store <store_id>")
		}
	} else if storeScope != "" {
		return "", fmt.Errorf("-store is only allowed for store_manager")
	}
	s, closeFn, err := open(dbPath)
	if err != nil {
		return "", err
	}
	defer closeFn()
	if _, err := s.GetTenant(tenantID); err != nil {
		return "", fmt.Errorf("tenant %s: %w", tenantID, err)
	}
	if storeScope != "" {
		if _, err := s.GetStore(storeScope, tenantID); err != nil {
			return "", fmt.Errorf("store %s: %w", storeScope, err)
		}
	}
	m, err := s.CreateMemberScoped(tenantID, principalRef, role, displayName, "provision", enabled, storeScope)
	if err != nil {
		return "", err
	}
	return m.ID, nil
}
