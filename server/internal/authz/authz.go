// Package authz is the pure server-side authorization core for touch-engine.
//
// 分层纪律(ADR-0001 D5):登录身份(identity principal)≠ 租户成员资格
// (members 行)≠ 资源授权。本包只裁决后两层;principal 的真伪由 identity
// 解析保证。公共活动页(游客)不属于本包:它是无会话只读白名单面,写操作
// 在 HTTP 层直接 401。
//
// HUI-1674 FEAT-0175 连锁多门店:租户内新增组织角色 org_owner(总部,全门店)
// 与门店作用域成员 store_manager(仅本门店)。作用域是服务端裁决事实
// (members.store_scope),客户端声明(头/参数/体)永不参与授权。
package authz

// Role of a tenant member.
type Role string

const (
	RoleOrgOwner     Role = "org_owner"     // 连锁总部:全门店管控
	RoleStoreManager Role = "store_manager" // 门店经理:仅本门店(store_scope)
	RoleStaff        Role = "staff"         // 租户级职员(T0 语义保留)
	// LegacyOwner is the T0 role name. After migration 0004 no row stores it;
	// it survives only as an INPUT alias for RoleOrgOwner (provision/API),
	// resolved by CanonicalRole. It is NOT a valid stored role (fails closed).
	LegacyOwner Role = "owner"
)

// Valid reports whether the role is one of the canonical stored values.
func (r Role) Valid() bool {
	return r == RoleOrgOwner || r == RoleStoreManager || r == RoleStaff
}

// CanonicalRole maps an input role to the canonical stored role. The T0 alias
// "owner" (full tenant control) maps to org_owner; unknown roles are refused.
func CanonicalRole(r Role) (Role, bool) {
	switch r {
	case LegacyOwner:
		return RoleOrgOwner, true
	default:
		if r.Valid() {
			return r, true
		}
		return "", false
	}
}

// Member is a tenant membership resolved from the members table.
type Member struct {
	ID           string // members.id
	TenantID     string
	PrincipalRef string
	Role         Role
	// StoreScope: "" = 总部/全门店;非空 = 仅该门店(store id)。只有
	// store_manager 行允许非空(写路径强制);缺 scope 的 manager 行全拒(fail-closed)。
	StoreScope string
	Enabled    bool
}

// inOwnStore reports whether the member's scope covers the record's store.
func (m Member) inOwnStore(storeID string) bool {
	return m.StoreScope != "" && storeID == m.StoreScope
}

// Action is an operation the API can authorize.
type Action string

const (
	ActionCreate              Action = "create"                // stores/campaigns/assets/links
	ActionReadList            Action = "read_list"             // own-tenant lists
	ActionReadRecord          Action = "read_record"           // own-tenant record
	ActionUpdate              Action = "update"                // campaigns incl. pause/resume/end
	ActionManageMembers       Action = "manage_members"        // org_owner only
	ActionManageStores        Action = "manage_stores"         // HUI-1674 store create/edit/enable/disable: org_owner only
	ActionExportQR            Action = "export_qr"             // HUI-1664 QR download: org_owner only
	ActionManageTags          Action = "manage_tags"           // HUI-1665 NFC tags CRUD/batch/export; HUI-1674 store_manager on own-store tags
	ActionManageCampaignRules Action = "manage_campaign_rules" // HUI-1676 FEAT-0177 活动规则 CRUD: org_owner only
)

// Deny reason codes.
const (
	ReasonNotMember   = "not_member"
	ReasonDisabled    = "member_disabled"
	ReasonCrossTenant = "cross_tenant"
	ReasonForbidden   = "forbidden"
	// ReasonOutOfScope: the record lives in the caller's tenant but outside the
	// member's store scope (another store, or tenant-level/unassigned). The
	// HTTP layer masks reads as 404 so foreign records stay invisible.
	ReasonOutOfScope = "out_of_scope"
)

// Decision is the outcome of an authorization check.
type Decision struct {
	Allowed bool
	// Reason is a stable machine-readable code for tests/logs.
	Reason string
}

func allow() Decision { return Decision{Allowed: true} }

func deny(reason string) Decision { return Decision{Allowed: false, Reason: reason} }

// RecordScope describes the target record's tenancy AND its owning store.
type RecordScope struct {
	TenantID string
	// StoreID is the store the record belongs to; "" = 租户级/未绑定
	// (总部可见,门店经理不可见)。
	StoreID string
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
	// membership row in THAT tenant. Without it: cross-tenant refusal. This
	// check precedes store scope (a foreign tenant's row is not even scoped).
	if rec.TenantID != "" && rec.TenantID != member.TenantID {
		return deny(ReasonCrossTenant)
	}

	switch action {
	case ActionManageMembers, ActionManageStores, ActionExportQR, ActionManageCampaignRules:
		// 总部专属治理面。store_manager 对「本店」也无此权(不可建删/改门店)。
		if member.Role != RoleOrgOwner {
			return deny(ReasonForbidden)
		}
		return allow()
	case ActionManageTags:
		switch member.Role {
		case RoleOrgOwner:
			return allow()
		case RoleStoreManager:
			if member.inOwnStore(rec.StoreID) {
				return allow()
			}
			return deny(ReasonOutOfScope)
		default:
			return deny(ReasonForbidden)
		}
	case ActionCreate, ActionReadList, ActionReadRecord, ActionUpdate:
		switch member.Role {
		case RoleOrgOwner, RoleStaff:
			// org_owner 看全门店;staff 保留 T0 租户级语义(存量兼容)。
			return allow()
		case RoleStoreManager:
			if member.inOwnStore(rec.StoreID) {
				return allow()
			}
			return deny(ReasonOutOfScope)
		default:
			return deny(ReasonForbidden)
		}
	default:
		return deny(ReasonForbidden)
	}
}
