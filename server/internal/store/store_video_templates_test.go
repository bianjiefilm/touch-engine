package store

// store_video_templates_test.go: HUI-1669 FEAT-0170 视频模板持久层验收。
//
// 覆盖(票面 TDD 清单,持久层部分):
//   - 模板 CRUD:创建即 v1 草稿;名称租户内唯一(ErrDuplicateVideoTemplate);
//     重命名;草稿模板可删,已发布模板拒绝删除(ErrVideoTemplatePublished);
//   - 版本冻结:发布后版本行只读(ErrVideoTemplateVersionReadonly);
//     结构变更 = 新草稿版本(max+1);同一时刻至多一个草稿
//     (ErrVideoTemplateDraftExists);版本行只增不删;
//   - 发布校验:槽位绑定引用不存在(ErrSlotAssetMissing)/媒体类型不符
//     (ErrSlotAssetTypeMismatch)拒绝发布;未绑定槽位合法;
//   - 分配:引用未发布版本拒绝(ErrVideoTemplateVersionNotPublished);
//     version=0 钉最新已发布版;同 (store, template, version) 幂等重放
//     (replayed=true,留痕字段零改动);换版重分配更新钉版并留 updated 痕;
//     解绑幂等;按店/按模板查询(带模板与钉版内容回填);
//   - 版本冻结对分配生效:发布 v2 后既有分配仍钉 v1;
//   - 跨租户一律 ErrNotFound / 0 行;关库重开全部行完整。

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/videotpl"
)

func vtSlots() videotpl.Content {
	return videotpl.Content{Slots: []videotpl.Slot{
		{Name: "opening", MediaType: assetlib.MediaTypeVideo, Role: videotpl.RoleOpening},
		{Name: "hero", MediaType: assetlib.MediaTypeImage, Role: videotpl.RoleProduct},
	}}
}

func vtSlotsAlt() videotpl.Content {
	return videotpl.Content{Slots: []videotpl.Slot{
		{Name: "cover", MediaType: assetlib.MediaTypeImage, Role: videotpl.RoleProduct},
	}}
}

func mustCreateTemplate(t *testing.T, s *Store, tenantID, name string, c videotpl.Content) (VideoTemplate, VideoTemplateVersion) {
	t.Helper()
	tpl, ver, err := s.CreateVideoTemplate(tenantID, name, c, "seed-owner")
	must(t, err)
	return tpl, ver
}

func mustPublish(t *testing.T, s *Store, tenantID, templateID string) VideoTemplateVersion {
	t.Helper()
	v, err := s.PublishVideoTemplate(tenantID, templateID, "seed-owner")
	must(t, err)
	return v
}

func TestVideoTemplateCreateAndNameUnique(t *testing.T) {
	s := openLibStore(t)
	tenA, tenB := libTenants(t, s)

	tpl, ver := mustCreateTemplate(t, s, tenA, "年货节模板", vtSlots())
	if tpl.ID == "" || tpl.Name != "年货节模板" || tpl.TenantID != tenA {
		t.Fatalf("template row mismatch: %+v", tpl)
	}
	if ver.Version != 1 || ver.Status != videotpl.StatusDraft || ver.TemplateID != tpl.ID {
		t.Fatalf("first version mismatch: %+v", ver)
	}
	if len(ver.Slots) != 2 || ver.Slots[0].Name != "opening" || ver.Slots[0].Role != videotpl.RoleOpening {
		t.Fatalf("slots roundtrip mismatch: %+v", ver.Slots)
	}
	if ver.PublishedBy != "" || ver.PublishedAt != "" {
		t.Fatalf("draft must not carry publish trace: %+v", ver)
	}

	// 同租户重名拒绝;跨租户同名允许。
	if _, _, err := s.CreateVideoTemplate(tenA, "年货节模板", vtSlots(), "x"); !errors.Is(err, ErrDuplicateVideoTemplate) {
		t.Fatalf("duplicate name = %v, want ErrDuplicateVideoTemplate", err)
	}
	if _, _, err := s.CreateVideoTemplate(tenB, "年货节模板", vtSlots(), "x"); err != nil {
		t.Fatalf("same name other tenant = %v", err)
	}

	// 域校验拒绝:空槽位列表 / 非法媒体类型。
	if _, _, err := s.CreateVideoTemplate(tenA, "空模板", videotpl.Content{}, "x"); err == nil {
		t.Fatal("empty slots must be refused")
	}
	bad := videotpl.Content{Slots: []videotpl.Slot{{Name: "s", MediaType: "audio", Role: videotpl.RoleOpening}}}
	if _, _, err := s.CreateVideoTemplate(tenA, "坏类型模板", bad, "x"); err == nil {
		t.Fatal("bad media type must be refused")
	}
	if _, _, err := s.CreateVideoTemplate(tenA, " ", vtSlots(), "x"); err == nil {
		t.Fatal("blank name must be refused")
	}

	// 跨租户读取不可见。
	if _, err := s.GetVideoTemplate(tpl.ID, tenB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get = %v, want ErrNotFound", err)
	}

	// 重命名:租户内唯一。
	renamed, err := s.RenameVideoTemplate(tpl.ID, tenA, "新春模板")
	must(t, err)
	if renamed.Name != "新春模板" {
		t.Fatalf("rename = %q", renamed.Name)
	}
	_, _ = mustCreateTemplate(t, s, tenA, "占名模板", vtSlots())
	if _, err := s.RenameVideoTemplate(tpl.ID, tenA, "占名模板"); !errors.Is(err, ErrDuplicateVideoTemplate) {
		t.Fatalf("rename onto taken name = %v, want ErrDuplicateVideoTemplate", err)
	}
	if _, err := s.RenameVideoTemplate(tpl.ID, tenB, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant rename = %v, want ErrNotFound", err)
	}
}

func TestVideoTemplateVersionLifecycleFreeze(t *testing.T) {
	s := openLibStore(t)
	tenA, tenB := libTenants(t, s)
	tpl, _ := mustCreateTemplate(t, s, tenA, "生命周期模板", vtSlots())

	// 草稿可原位改写(版本号不变)。
	v1b, err := s.UpdateVideoTemplateVersionContent(tenA, tpl.ID, 1, vtSlotsAlt(), "owner")
	must(t, err)
	if v1b.Version != 1 || v1b.Status != videotpl.StatusDraft || v1b.Slots[0].Name != "cover" {
		t.Fatalf("draft edit mismatch: %+v", v1b)
	}

	// 发布:状态/留痕落行。
	pub1 := mustPublish(t, s, tenA, tpl.ID)
	if pub1.Version != 1 || pub1.Status != videotpl.StatusPublished || pub1.PublishedBy != "seed-owner" || pub1.PublishedAt == "" {
		t.Fatalf("publish mismatch: %+v", pub1)
	}

	// 发布后版本只读:改 = 拒。
	if _, err := s.UpdateVideoTemplateVersionContent(tenA, tpl.ID, 1, vtSlots(), "owner"); !errors.Is(err, ErrVideoTemplateVersionReadonly) {
		t.Fatalf("edit published = %v, want ErrVideoTemplateVersionReadonly", err)
	}
	// 未知版本改 = ErrNotFound。
	if _, err := s.UpdateVideoTemplateVersionContent(tenA, tpl.ID, 9, vtSlots(), "owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("edit missing version = %v, want ErrNotFound", err)
	}

	// 结构变更 = 新草稿版本。
	v2, err := s.CreateVideoTemplateVersion(tenA, tpl.ID, vtSlotsAlt(), "owner")
	must(t, err)
	if v2.Version != 2 || v2.Status != videotpl.StatusDraft {
		t.Fatalf("new draft mismatch: %+v", v2)
	}
	// 已有草稿时再开新草稿 = 拒。
	if _, err := s.CreateVideoTemplateVersion(tenA, tpl.ID, vtSlots(), "owner"); !errors.Is(err, ErrVideoTemplateDraftExists) {
		t.Fatalf("second draft = %v, want ErrVideoTemplateDraftExists", err)
	}
	// v2 草稿可改;v1 已发布只读(改 v1 再确认一次)。
	if _, err := s.UpdateVideoTemplateVersionContent(tenA, tpl.ID, 2, vtSlots(), "owner"); err != nil {
		t.Fatalf("edit v2 draft: %v", err)
	}

	// 发布 v2;版本行只增不删:两行都在。
	mustPublish(t, s, tenA, tpl.ID)
	vers, err := s.ListVideoTemplateVersions(tenA, tpl.ID)
	must(t, err)
	if len(vers) != 2 || vers[0].Version != 1 || vers[1].Version != 2 ||
		vers[0].Status != videotpl.StatusPublished || vers[1].Status != videotpl.StatusPublished {
		t.Fatalf("versions after two publishes = %+v", vers)
	}
	if _, err := s.PublishVideoTemplate(tenA, tpl.ID, "owner"); !errors.Is(err, ErrVideoTemplateNoDraft) {
		t.Fatalf("publish with no draft = %v, want ErrVideoTemplateNoDraft", err)
	}

	// 汇总:latest=2、全部已发布、published_version=2。
	sum, err := s.GetVideoTemplateSummary(tenA, tpl.ID)
	must(t, err)
	if sum.LatestVersion != 2 || sum.LatestStatus != videotpl.StatusPublished || sum.PublishedVersion != 2 {
		t.Fatalf("summary mismatch: %+v", sum)
	}
	// 跨租户版本读取不可见。
	if _, err := s.GetVideoTemplateVersion(tenB, tpl.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant version = %v, want ErrNotFound", err)
	}
}

func TestVideoTemplatePublishBindingValidation(t *testing.T) {
	s := openLibStore(t)
	tenA, _ := libTenants(t, s)
	img := mustCreateAsset(t, s, tenA, "upl_img", shaHex(11), assetlib.MediaTypeImage)
	vid := mustCreateAsset(t, s, tenA, "upl_vid", shaHex(12), assetlib.MediaTypeVideo)

	bound := videotpl.Content{Slots: []videotpl.Slot{
		{Name: "hero", MediaType: assetlib.MediaTypeVideo, Role: videotpl.RoleProduct, AssetID: vid.ID},
		{Name: "bg", MediaType: assetlib.MediaTypeImage, Role: videotpl.RoleCloseup, AssetID: img.ID},
		{Name: "outro", MediaType: assetlib.MediaTypeVideo, Role: videotpl.RoleEnding}, // 未绑定:合法
	}}

	// 类型不符:video 槽绑了 image 素材 → 拒。
	wrongType := bound
	wrongType.Slots = append([]videotpl.Slot{}, bound.Slots...)
	wrongType.Slots[0] = videotpl.Slot{Name: "hero", MediaType: assetlib.MediaTypeVideo,
		Role: videotpl.RoleProduct, AssetID: img.ID}
	tplBad, _ := mustCreateTemplate(t, s, tenA, "类型不符模板", wrongType)
	if _, err := s.PublishVideoTemplate(tenA, tplBad.ID, "owner"); !errors.Is(err, ErrSlotAssetTypeMismatch) {
		t.Fatalf("type mismatch publish = %v, want ErrSlotAssetTypeMismatch", err)
	}

	// 引用不存在 → 拒。
	missing := bound
	missing.Slots = append([]videotpl.Slot{}, bound.Slots...)
	missing.Slots[0] = videotpl.Slot{Name: "hero", MediaType: assetlib.MediaTypeVideo,
		Role: videotpl.RoleProduct, AssetID: "las_missing"}
	tplMiss, _ := mustCreateTemplate(t, s, tenA, "缺引用模板", missing)
	if _, err := s.PublishVideoTemplate(tenA, tplMiss.ID, "owner"); !errors.Is(err, ErrSlotAssetMissing) {
		t.Fatalf("missing asset publish = %v, want ErrSlotAssetMissing", err)
	}
	// 被拒发布不留半条状态:仍是 draft。
	v, err := s.GetVideoTemplateVersion(tenA, tplMiss.ID, 1)
	must(t, err)
	if v.Status != videotpl.StatusDraft {
		t.Fatalf("rejected publish mutated status: %+v", v)
	}

	// 绑定齐全 → 发布成功。
	tplOK, _ := mustCreateTemplate(t, s, tenA, "绑定齐全模板", bound)
	mustPublish(t, s, tenA, tplOK.ID)
}

func TestVideoTemplateAssignmentFreezeAndTrace(t *testing.T) {
	s := openLibStore(t)
	tenA, _ := libTenants(t, s)
	sto, err := s.CreateStore(tenA, "旗舰店", "地址", "owner")
	must(t, err)

	tpl, _ := mustCreateTemplate(t, s, tenA, "分配模板", vtSlots())
	mustPublish(t, s, tenA, tpl.ID)

	// 引用未发布版本拒绝:先造一个草稿 v2。
	_, err = s.CreateVideoTemplateVersion(tenA, tpl.ID, vtSlotsAlt(), "owner")
	must(t, err)
	if _, _, _, err := s.AssignVideoTemplate(tenA, sto.ID, tpl.ID, 2, "owner"); !errors.Is(err, ErrVideoTemplateVersionNotPublished) {
		t.Fatalf("assign draft version = %v, want ErrVideoTemplateVersionNotPublished", err)
	}
	// version=0(钉最新已发布):草稿 v2 存在也不影响 → 钉 v1。
	a0, replayed, _, err := s.AssignVideoTemplate(tenA, sto.ID, tpl.ID, 0, "boss")
	must(t, err)
	if replayed || a0.Version != 1 || a0.AssignedBy != "boss" || a0.AssignedAt == "" {
		t.Fatalf("latest-published assign mismatch: %+v replayed=%v", a0, replayed)
	}
	if a0.Template.Name != "分配模板" || len(a0.PinnedVersion.Slots) == 0 {
		t.Fatalf("hydration mismatch: %+v", a0)
	}

	// 发布 v2;既有分配必须仍钉 v1(冻结)。
	mustPublish(t, s, tenA, tpl.ID)
	got, err := s.GetVideoTemplateSummary(tenA, tpl.ID)
	must(t, err)
	if got.LatestVersion != 2 {
		t.Fatalf("latest = %d, want 2", got.LatestVersion)
	}
	as, err := s.ListVideoTemplateAssignmentsByStore(tenA, sto.ID)
	must(t, err)
	if len(as) != 1 || as[0].Version != 1 {
		t.Fatalf("existing assignment must stay pinned to v1, got %+v", as)
	}

	// 显式换版重分配:钉版更新,updated 留痕更新,assigned 留痕保持首次。
	a1, replayed, updated, err := s.AssignVideoTemplate(tenA, sto.ID, tpl.ID, 2, "boss2")
	must(t, err)
	if replayed || !updated || a1.Version != 2 || a1.UpdatedBy != "boss2" || a1.AssignedBy != "boss" || a1.AssignedAt != a0.AssignedAt {
		t.Fatalf("re-assign trace mismatch: %+v (was %+v)", a1, a0)
	}

	// 同键同版重分配:幂等重放,零改动。
	a2, replayed, _, err := s.AssignVideoTemplate(tenA, sto.ID, tpl.ID, 2, "boss3")
	must(t, err)
	if !replayed || a2.ID != a1.ID || a2.UpdatedAt != a1.UpdatedAt || a2.UpdatedBy != "boss2" {
		t.Fatalf("idempotent replay mutated the row: %+v (was %+v)", a2, a1)
	}

	// 未知/未发布版本边界。
	if _, _, _, err := s.AssignVideoTemplate(tenA, sto.ID, tpl.ID, 99, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assign missing version = %v, want ErrNotFound", err)
	}
	tplN, _ := mustCreateTemplate(t, s, tenA, "从未发布模板", vtSlots())
	if _, _, _, err := s.AssignVideoTemplate(tenA, sto.ID, tplN.ID, 0, "x"); !errors.Is(err, ErrVideoTemplateVersionNotPublished) {
		t.Fatalf("assign never-published = %v, want ErrVideoTemplateVersionNotPublished", err)
	}
	// 未知门店/模板、跨租户门店:一律 ErrNotFound。
	if _, _, _, err := s.AssignVideoTemplate(tenA, "sto_missing", tpl.ID, 0, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assign unknown store = %v", err)
	}
	if _, _, _, err := s.AssignVideoTemplate(tenA, sto.ID, "vt_missing", 0, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("assign unknown template = %v", err)
	}
	tenB, _ := libTenants(t, s)
	if _, _, _, err := s.AssignVideoTemplate(tenB, sto.ID, tpl.ID, 0, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant assign (B scope on A store) = %v, want ErrNotFound", err)
	}
}

func TestVideoTemplateAssignmentQueriesAndUnbind(t *testing.T) {
	s := openLibStore(t)
	tenA, tenB := libTenants(t, s)
	sto1, err := s.CreateStore(tenA, "一店", "a", "o")
	must(t, err)
	sto2, err := s.CreateStore(tenA, "二店", "b", "o")
	must(t, err)
	t1, _ := mustCreateTemplate(t, s, tenA, "模板一", vtSlots())
	t2, _ := mustCreateTemplate(t, s, tenA, "模板二", vtSlotsAlt())
	mustPublish(t, s, tenA, t1.ID)
	mustPublish(t, s, tenA, t2.ID)

	mustAssign := func(tenantID, storeID, templateID string) {
		t.Helper()
		_, _, _, err := s.AssignVideoTemplate(tenantID, storeID, templateID, 0, "o")
		must(t, err)
	}
	mustAssign(tenA, sto1.ID, t1.ID)
	mustAssign(tenA, sto1.ID, t2.ID)
	mustAssign(tenA, sto2.ID, t1.ID)

	byStore, err := s.ListVideoTemplateAssignmentsByStore(tenA, sto1.ID)
	must(t, err)
	if len(byStore) != 2 {
		t.Fatalf("by store = %d rows, want 2", len(byStore))
	}
	byTpl, err := s.ListVideoTemplateAssignmentsByTemplate(tenA, t1.ID)
	must(t, err)
	if len(byTpl) != 2 || byTpl[0].StoreID == "" {
		t.Fatalf("by template = %+v", byTpl)
	}
	// 回填:模板名与钉版内容都在。
	if byStore[0].Template.Name == "" || byStore[0].PinnedVersion.Version == 0 {
		t.Fatalf("hydration missing: %+v", byStore[0])
	}

	// 解绑幂等:一次移除,二次仍成功(零行也是成功)。
	must(t, s.UnassignVideoTemplate(tenA, sto1.ID, t1.ID))
	must(t, s.UnassignVideoTemplate(tenA, sto1.ID, t1.ID))
	byStore, err = s.ListVideoTemplateAssignmentsByStore(tenA, sto1.ID)
	must(t, err)
	if len(byStore) != 1 || byStore[0].TemplateID != t2.ID {
		t.Fatalf("after unbind = %+v", byStore)
	}
	// 未知门店/模板解绑:不可见 → ErrNotFound。
	if err := s.UnassignVideoTemplate(tenA, "sto_missing", t1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unbind unknown store = %v", err)
	}

	// 跨租户:0 行。
	byStoreB, err := s.ListVideoTemplateAssignmentsByStore(tenB, sto1.ID)
	must(t, err)
	if len(byStoreB) != 0 {
		t.Fatalf("cross-tenant by store = %d rows, want 0", len(byStoreB))
	}
	byTplB, err := s.ListVideoTemplateAssignmentsByTemplate(tenB, t1.ID)
	must(t, err)
	if len(byTplB) != 0 {
		t.Fatalf("cross-tenant by template = %d rows, want 0", len(byTplB))
	}
}

func TestVideoTemplateDeleteGuard(t *testing.T) {
	s := openLibStore(t)
	tenA, _ := libTenants(t, s)

	// 从未发布(纯草稿)可删:模板+版本行一起消失。
	tplDraft, _ := mustCreateTemplate(t, s, tenA, "纯草稿模板", vtSlots())
	must(t, s.DeleteVideoTemplate(tplDraft.ID, tenA))
	if _, err := s.GetVideoTemplate(tplDraft.ID, tenA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft template after delete = %v", err)
	}
	if _, err := s.GetVideoTemplateVersion(tenA, tplDraft.ID, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft version after delete = %v", err)
	}
	if err := s.DeleteVideoTemplate(tplDraft.ID, tenA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete = %v, want ErrNotFound", err)
	}

	// 已发布(含其上再开的草稿)拒绝删除:版本行只读且分配仍冻结。
	tplPub, _ := mustCreateTemplate(t, s, tenA, "已发布模板", vtSlots())
	mustPublish(t, s, tenA, tplPub.ID)
	_, err := s.CreateVideoTemplateVersion(tenA, tplPub.ID, vtSlotsAlt(), "owner")
	must(t, err)
	if err := s.DeleteVideoTemplate(tplPub.ID, tenA); !errors.Is(err, ErrVideoTemplatePublished) {
		t.Fatalf("delete published = %v, want ErrVideoTemplatePublished", err)
	}
	if _, err := s.GetVideoTemplate(tplPub.ID, tenA); err != nil {
		t.Fatalf("published template must survive: %v", err)
	}
	// 跨租户删除不可见(ErrNotFound,绝不误删)。
	tenB, _ := libTenants(t, s)
	if err := s.DeleteVideoTemplate(tplPub.ID, tenB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant delete = %v, want ErrNotFound", err)
	}
}

func TestVideoTemplateListAndReopenPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "touch.db")
	s := openLibStoreAt(t, path)
	tenA, _ := libTenants(t, s)
	t1, _ := mustCreateTemplate(t, s, tenA, "持久一", vtSlots())
	_, _ = mustCreateTemplate(t, s, tenA, "持久二", vtSlotsAlt())
	mustPublish(t, s, tenA, t1.ID)
	sto, err := s.CreateStore(tenA, "店", "a", "o")
	must(t, err)
	_, _, _, err = s.AssignVideoTemplate(tenA, sto.ID, t1.ID, 0, "o")
	must(t, err)

	list, err := s.ListVideoTemplates(tenA)
	must(t, err)
	if len(list) != 2 {
		t.Fatalf("list = %d, want 2", len(list))
	}
	byName := map[string]VideoTemplateSummary{}
	for _, it := range list {
		byName[it.Name] = it
	}
	if byName["持久一"].PublishedVersion != 1 || byName["持久一"].LatestVersion != 1 ||
		byName["持久二"].PublishedVersion != 0 || byName["持久二"].LatestStatus != videotpl.StatusDraft {
		t.Fatalf("summary list mismatch: %+v", byName)
	}
	// 跨租户列表 0 行。
	tenB, _ := libTenants(t, s)
	empty, err := s.ListVideoTemplates(tenB)
	must(t, err)
	if len(empty) != 0 {
		t.Fatalf("cross-tenant list = %d rows", len(empty))
	}

	// 关库重开:模板/版本/分配(含槽位 JSON)完整。
	s2 := openLibStoreAt(t, path)
	got, err := s2.GetVideoTemplateVersion(tenA, t1.ID, 1)
	must(t, err)
	if got.Status != videotpl.StatusPublished || len(got.Slots) != 2 || got.Slots[0].Name != "opening" {
		t.Fatalf("reopened version mismatch: %+v", got)
	}
	as, err := s2.ListVideoTemplateAssignmentsByStore(tenA, sto.ID)
	must(t, err)
	if len(as) != 1 || as[0].Version != 1 || as[0].Template.Name != "持久一" {
		t.Fatalf("reopened assignment mismatch: %+v", as)
	}
}
