// Package authz is the pure server-side authorization core for touch-engine.
//
// 分层纪律(ADR-0001 D5):登录身份(identity principal)≠ 租户成员资格
// (members 行)≠ 资源授权。本包只裁决后两层;principal 的真伪由 identity
// 解析保证。公共活动页(游客)不属于本包:它是无会话只读白名单面,写操作
// 在 HTTP 层直接 401。
package authz

// Role of a tenant member.
type Role string

const (
	RoleOwner Role = "owner"
	RoleStaff Role = "staff"
)

// Valid reports whether the role is one of the known values.
func (r Role) Valid() bool { return r == RoleOwner || r == RoleStaff }

// Member is a tenant membership resolved from the members table.
type Member struct {
	ID           string // members.id
	TenantID     string
	PrincipalRef string
	Role         Role
	Enabled      bool
}

// Action is an operation the API can authorize.
type Action string

const (
	ActionCreate        Action = "create"         // stores/campaigns/assets/links
	ActionReadList      Action = "read_list"      // own-tenant lists
	ActionReadRecord    Action = "read_record"    // own-tenant record
	ActionUpdate        Action = "update"         // campaigns incl. pause/resume/end
	ActionManageMembers Action = "manage_members" // owner only
	ActionExportQR      Action = "export_qr"      // HUI-1664 QR download: owner only
	ActionManageTags    Action = "manage_tags"    // HUI-1665 NFC tags CRUD/batch/export: owner only
)

// Deny reason codes.
const (
	ReasonNotMember   = "not_member"
	ReasonDisabled    = "member_disabled"
	ReasonCrossTenant = "cross_tenant"
	ReasonForbidden   = "forbidden"
)

// Decision is the outcome of an authorization check.
type Decision struct {
	Allowed bool
	// Reason is a stable machine-readable code for tests/logs.
	Reason string
}

func allow() Decision { return Decision{Allowed: true} }

func deny(reason string) Decision { return Decision{Allowed: false, Reason: reason} }

// RecordScope describes the target record's tenancy.
type RecordScope struct {
	TenantID string
}

// Authorize decides whether member may perform action on the tenant/record.
// member must be non-nil (a resolved membership for the caller's principal).
// Guests never reach this function: the HTTP layer answers 401 before it.
func Authorize(member *Member, action Action, rec RecordScope) Decision {
	if member == nil {
		return deny(ReasonNotMember)
	}
	if !member.Enabled {
		return deny(ReasonDisabled)
	}
	if !member.Role.Valid() {
		return deny(ReasonForbidden)
	}

	// Membership is per-tenant: acting on another tenant's anything requires a
	// membership row in THAT tenant. Without it: cross-tenant refusal.
	if rec.TenantID != "" && rec.TenantID != member.TenantID {
		return deny(ReasonCrossTenant)
	}

	switch action {
	case ActionManageMembers, ActionExportQR, ActionManageTags:
		if member.Role != RoleOwner {
			return deny(ReasonForbidden)
		}
		return allow()
	case ActionCreate, ActionReadList, ActionReadRecord, ActionUpdate:
		return allow()
	default:
		return deny(ReasonForbidden)
	}
}
