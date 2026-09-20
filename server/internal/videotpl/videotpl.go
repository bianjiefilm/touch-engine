// Package videotpl implements HUI-1669 FEAT-0170 视频模板管理: the pure,
// deterministic domain rules for video template structure, lifecycle and
// store-assignment freezing.
//
// 纪律(票面拍板):
//   - 视频模板 = 素材引用集的组织单元,零物理存储:模板是具名结构
//     (名称租户内唯一 + 槽位定义列表);模板内容 = 槽位到 FEAT-0167 素材库
//     素材引用的绑定(asset_id 可空,发布时统一校验存在性与媒体类型);
//   - 视频合成/渲染执行不在本系统(属外部工具/AiCut,deferred):本包与
//     接口均不出现任何生成/渲染触发字段;
//   - 生命周期:draft → published;发布后该版本行只读(结构冻结);
//     结构变更 = 产生新版本;既有分配钉住 (template_id, version),
//     不自动升级(与素材库选择台账同一冻结范式);
//   - 槽位语义标签枚举披露:opening / product / closeup / ending;
//     媒体类型约束复用 assetlib 的 image/video/bgm 枚举。
package videotpl

import (
	"fmt"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
)

// ---- lifecycle statuses -------------------------------------------------------

// Version lifecycle statuses (发布后该版本行只读).
const (
	StatusDraft     = "draft"
	StatusPublished = "published"
)

// ---- slot semantic label enumeration (枚举披露) ---------------------------------

// Role is the disclosed slot semantic-label enumeration.
type Role string

const (
	RoleOpening Role = "opening" // 片头
	RoleProduct Role = "product" // 产品展示
	RoleCloseup Role = "closeup" // 特写
	RoleEnding  Role = "ending"  // 片尾
)

// Valid reports whether r is one of the disclosed roles.
func (r Role) Valid() bool {
	switch r {
	case RoleOpening, RoleProduct, RoleCloseup, RoleEnding:
		return true
	}
	return false
}

// ---- machine reason codes ------------------------------------------------------

// Machine reason codes (written into 422 responses' error fields).
const (
	ReasonMissingName       = "missing_template_name" // 模板名缺失
	ReasonBadName           = "bad_template_name"     // 模板名超长
	ReasonMissingSlots      = "missing_slots"         // 槽位列表为空
	ReasonTooManySlots      = "too_many_slots"        // 槽位数超上限
	ReasonMissingSlotName   = "missing_slot_name"     // 槽位名缺失
	ReasonBadSlotName       = "bad_slot_name"         // 槽位名超长
	ReasonDuplicateSlotName = "duplicate_slot_name"   // 槽位名模板内重复
	ReasonBadMediaType      = "bad_slot_media_type"   // 槽位媒体类型不在枚举内
	ReasonBadRole           = "bad_slot_role"         // 语义标签不在披露枚举内
)

// ValidationError carries a machine-readable reason for the 422 mapping.
type ValidationError struct {
	Reason string
	Field  string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("videotpl: %s (%s)", e.Reason, e.Field)
}

func invalid(reason, field string) error {
	return &ValidationError{Reason: reason, Field: field}
}

// NewValidationError is the exported constructor for the layered callers.
func NewValidationError(reason, field string) error {
	return &ValidationError{Reason: reason, Field: field}
}

// ---- structure -----------------------------------------------------------------

// Field length caps (名称是短文本,不是内容体).
const (
	MaxName     = 64
	MaxSlotName = 64
	MaxSlots    = 32
)

// Slot is one slot definition. AssetID is the OPTIONAL binding to an
// FEAT-0167 lib asset row (lib_assets.id); "" = unbound placeholder. Bindings
// are validated at PUBLISH time (existence + media-type match), not at draft
// write: a draft may reference assets registered later, and publish is the
// single gate (票面: 分配/发布校验).
type Slot struct {
	Name      string             `json:"name"`               // 槽位名(模板内唯一)
	MediaType assetlib.MediaType `json:"media_type"`         // 媒体类型约束 image|video|bgm
	Role      Role               `json:"role"`               // 语义标签(披露枚举)
	AssetID   string             `json:"asset_id,omitempty"` // 绑定的素材引用(可空)
}

// Content is one version's structure content.
type Content struct {
	Slots []Slot `json:"slots"`
}

// NormalizeSlot canonicalizes one slot's text fields (returns a copy).
func NormalizeSlot(s Slot) Slot {
	s.Name = strings.TrimSpace(s.Name)
	s.MediaType = assetlib.MediaType(strings.TrimSpace(string(s.MediaType)))
	s.Role = Role(strings.TrimSpace(string(s.Role)))
	s.AssetID = strings.TrimSpace(s.AssetID)
	return s
}

// NormalizeName canonicalizes the template display name.
func NormalizeName(name string) string { return strings.TrimSpace(name) }

// NormalizeContent canonicalizes every slot of the content (returns a copy;
// the input is never mutated).
func NormalizeContent(c Content) Content {
	out := Content{Slots: make([]Slot, 0, len(c.Slots))}
	for _, s := range c.Slots {
		out.Slots = append(out.Slots, NormalizeSlot(s))
	}
	return out
}

// ValidateContent enforces the structure matrix: the slot list must be
// non-empty and within the cap; every slot name must be present, bounded and
// unique within the template; media type must be in the assetlib enumeration;
// role must be in the disclosed enumeration. Binding existence/type is
// deliberately NOT checked here (publish-time gate).
func ValidateContent(c Content) error {
	if len(c.Slots) == 0 {
		return invalid(ReasonMissingSlots, "slots")
	}
	if len(c.Slots) > MaxSlots {
		return invalid(ReasonTooManySlots, "slots")
	}
	seen := make(map[string]bool, len(c.Slots))
	for i, raw := range c.Slots {
		s := NormalizeSlot(raw)
		field := fmt.Sprintf("slots[%d]", i)
		switch {
		case s.Name == "":
			return invalid(ReasonMissingSlotName, field+".name")
		case len(s.Name) > MaxSlotName:
			return invalid(ReasonBadSlotName, field+".name")
		case seen[s.Name]:
			return invalid(ReasonDuplicateSlotName, field+".name")
		}
		seen[s.Name] = true
		if !s.MediaType.Valid() {
			return invalid(ReasonBadMediaType, field+".media_type")
		}
		if !s.Role.Valid() {
			return invalid(ReasonBadRole, field+".role")
		}
	}
	return nil
}

// ValidateName checks the template display name (租户内唯一的显示名).
func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return invalid(ReasonMissingName, "name")
	case len(name) > MaxName:
		return invalid(ReasonBadName, "name")
	}
	return nil
}
