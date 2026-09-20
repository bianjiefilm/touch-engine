package videotpl

// videotpl_test.go: HUI-1669 FEAT-0170 视频模板域规则验收(纯函数部分)。
//
// 覆盖(票面 TDD 清单,域规则部分):
//   - 模板名校验:缺失/超长拒绝,独立原因码;
//   - 槽位校验矩阵:空槽位列表拒绝;媒体类型不在 image/video/bgm 拒绝;
//     语义标签不在披露枚举(opening/product/closeup/ending)拒绝;
//     槽位名缺失/超长/模板内重复拒绝;槽位数超上限拒绝;
//   - 绑定字段:asset_id 可空(草稿允许未绑定槽位,发布时统一校验);
//   - 生命周期状态枚举披露:draft / published。

import (
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
)

func validContent() Content {
	return Content{Slots: []Slot{
		{Name: "opening", MediaType: assetlib.MediaTypeVideo, Role: RoleOpening},
		{Name: "hero", MediaType: assetlib.MediaTypeImage, Role: RoleProduct, AssetID: "las_x"},
		{Name: "detail", MediaType: assetlib.MediaTypeVideo, Role: RoleCloseup},
		{Name: "outro", MediaType: assetlib.MediaTypeVideo, Role: RoleEnding},
	}}
}

func TestValidateContentAcceptsWellFormed(t *testing.T) {
	if err := ValidateContent(validContent()); err != nil {
		t.Fatalf("valid content rejected: %v", err)
	}
	// 全部未绑定也合法(草稿面允许占位槽位)。
	c := Content{Slots: []Slot{{Name: "s1", MediaType: assetlib.MediaTypeBGM, Role: RoleEnding}}}
	if err := ValidateContent(c); err != nil {
		t.Fatalf("unbound slot rejected: %v", err)
	}
}

func TestValidateContentMatrix(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mut    func(*Content)
		reason string
		field  string
	}{
		{"empty_slots", func(c *Content) { c.Slots = nil }, ReasonMissingSlots, "slots"},
		{"empty_slots_slice", func(c *Content) { c.Slots = []Slot{} }, ReasonMissingSlots, "slots"},
		{"bad_media_type", func(c *Content) { c.Slots[0].MediaType = "audio" }, ReasonBadMediaType, "slots[0].media_type"},
		{"missing_media_type", func(c *Content) { c.Slots[0].MediaType = " " }, ReasonBadMediaType, "slots[0].media_type"},
		{"bad_role", func(c *Content) { c.Slots[1].Role = "hero_shot" }, ReasonBadRole, "slots[1].role"},
		{"missing_role", func(c *Content) { c.Slots[1].Role = "" }, ReasonBadRole, "slots[1].role"},
		{"missing_slot_name", func(c *Content) { c.Slots[2].Name = "  " }, ReasonMissingSlotName, "slots[2].name"},
		{"long_slot_name", func(c *Content) { c.Slots[2].Name = string(make([]byte, MaxSlotName+1)) }, ReasonBadSlotName, "slots[2].name"},
		{"duplicate_slot_name", func(c *Content) { c.Slots[3].Name = "opening" }, ReasonDuplicateSlotName, "slots[3].name"},
		{"too_many_slots", func(c *Content) {
			c.Slots = nil
			for i := 0; i <= MaxSlots; i++ {
				c.Slots = append(c.Slots, Slot{Name: fmtSlot(i), MediaType: assetlib.MediaTypeVideo, Role: RoleProduct})
			}
		}, ReasonTooManySlots, "slots"},
	} {
		c := validContent()
		tc.mut(&c)
		err := ValidateContent(c)
		if err == nil {
			t.Fatalf("%s: expected rejection, got nil", tc.name)
		}
		ve, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("%s: error type = %T, want *ValidationError", tc.name, err)
		}
		if ve.Reason != tc.reason || ve.Field != tc.field {
			t.Fatalf("%s: reason/field = %s/%s, want %s/%s", tc.name, ve.Reason, ve.Field, tc.reason, tc.field)
		}
	}
}

func fmtSlot(i int) string { return "slot" + string(rune('a'+i%26)) + timeLabel(i) }

func timeLabel(i int) string {
	out := ""
	for i > 0 {
		out += string(rune('0' + i%10))
		i /= 10
	}
	return out
}

func TestValidateName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     string
		reason string
	}{
		{"ok", "年货节模板", ""},
		{"trim_ok", "  年货节模板  ", ""},
		{"missing", "   ", ReasonMissingName},
		{"too_long", string(make([]byte, MaxName+1)), ReasonBadName},
	} {
		err := ValidateName(tc.in)
		if tc.reason == "" {
			if err != nil {
				t.Fatalf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		ve, ok := err.(*ValidationError)
		if !ok || ve.Reason != tc.reason {
			t.Fatalf("%s: err = %v, want reason %s", tc.name, err, tc.reason)
		}
	}
}

func TestRoleEnumDisclosed(t *testing.T) {
	// 语义标签枚举披露:仅这四个合法。
	for _, r := range []Role{RoleOpening, RoleProduct, RoleCloseup, RoleEnding} {
		if !r.Valid() {
			t.Fatalf("role %q must be valid", r)
		}
	}
	for _, r := range []Role{"", "hero", "bgm", "OPENING"} {
		if r.Valid() {
			t.Fatalf("role %q must be invalid", r)
		}
	}
}

func TestStatusValues(t *testing.T) {
	if StatusDraft != "draft" || StatusPublished != "published" {
		t.Fatalf("status values = %q/%q", StatusDraft, StatusPublished)
	}
}
