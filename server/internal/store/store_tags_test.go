package store

// HUI-1665 FEAT-0166 NFC 标签存储层测试:
//   - 分组:创建/重名拒绝/删除时组内标签 detach(不删标签);
//   - 批量:1..500 边界、shared/rotate 绑定、跨活动 link 拒绝、label 序号;
//   - 状态:标签停用→绑定 link 一并停用(ResolveLink 五态 link_disabled),恢复→复用;
//   - 换绑:link 换到另一活动,campaign 视图随之变化;分组/门店/uid_hint patch。

import (
	"errors"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
)

func tagTestStore(t *testing.T) *Store {
	t.Helper()
	return openStore(t)
}

// seedCampaignWithLinks: tenant -> campaign(+status) -> n links.
func seedCampaignWithLinks(t *testing.T, s *Store, tenantID, status string, nLinks int) (campaignID string, links []CampaignLink) {
	t.Helper()
	c, err := s.CreateCampaign(NewCampaign{TenantID: tenantID, Title: "活动", PublicContent: "内容", CreatedBy: "u"})
	if err != nil {
		t.Fatalf("campaign: %v", err)
	}
	if status != string(campaign.StatusDraft) {
		if _, err := s.TransitionCampaign(c.ID, tenantID, campaign.Status(status)); err != nil {
			t.Fatalf("transition: %v", err)
		}
	}
	for i := 0; i < nLinks; i++ {
		l, err := s.CreateLink(tenantID, c.ID, "u")
		if err != nil {
			t.Fatalf("link: %v", err)
		}
		links = append(links, l)
	}
	return c.ID, links
}

func TestTagGroupLifecycle(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)

	g, err := s.CreateTagGroup(ten.ID, "门店一组", "u1")
	mustNoErr(t, err)
	if g.ID == "" || g.Name != "门店一组" {
		t.Fatalf("group = %+v", g)
	}

	// 重名拒绝
	if _, err := s.CreateTagGroup(ten.ID, "门店一组", "u1"); !errors.Is(err, ErrTagGroupNameTaken) {
		t.Fatalf("duplicate group name = %v, want ErrTagGroupNameTaken", err)
	}
	// 同名跨租户允许
	ten2, err := s.CreateTenant("B")
	mustNoErr(t, err)
	if _, err := s.CreateTagGroup(ten2.ID, "门店一组", "u2"); err != nil {
		t.Fatalf("same name other tenant: %v", err)
	}

	cID, links := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	_, err = s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID, LinkIDs: []string{links[0].ID},
		BindMode: "shared", Count: 3, GroupID: g.ID, CreatedBy: "u1"})
	mustNoErr(t, err)

	// 删除分组:组内标签 detach 保留
	if err := s.DeleteTagGroup(g.ID, ten.ID); err != nil {
		t.Fatalf("delete group: %v", err)
	}
	tags, err := s.ListTags(ten.ID, TagFilter{})
	mustNoErr(t, err)
	if len(tags) != 3 {
		t.Fatalf("tags after group delete = %d, want 3", len(tags))
	}
	for _, v := range tags {
		if v.GroupID != "" || v.GroupName != "" {
			t.Fatalf("tag %s still grouped: %+v", v.ID, v)
		}
	}
	if err := s.DeleteTagGroup(g.ID, ten.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete = %v, want ErrNotFound", err)
	}
	// 跨租户删除不可见
	if err := s.DeleteTagGroup(g.ID, ten2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant group delete = %v, want ErrNotFound", err)
	}
}

func TestTagBatchValidationAndBinding(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	cID, links := seedCampaignWithLinks(t, s, ten.ID, "active", 3)

	// rotate:第 i 条绑 links[i%3]
	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID, links[1].ID, links[2].ID}, BindMode: "rotate", Count: 7,
		StoreID: "", GroupID: "", LabelPrefix: "周年庆", CreatedBy: "u1"})
	mustNoErr(t, err)
	if len(tags) != 7 {
		t.Fatalf("count = %d, want 7", len(tags))
	}
	for i, v := range tags {
		wantLink := links[i%3].ID
		if v.LinkID != wantLink {
			t.Fatalf("tag %d bound to %s, want %s", i, v.LinkID, wantLink)
		}
		wantLabel := "周年庆-" + []string{"001", "002", "003", "004", "005", "006", "007"}[i]
		if v.Label != wantLabel {
			t.Fatalf("label = %q, want %q", v.Label, wantLabel)
		}
		if v.Status != TagStatusActive || v.Code != links[i%3].Code {
			t.Fatalf("tag %d = %+v", i, v)
		}
	}

	// shared:全部绑第一条
	tags2, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[1].ID}, BindMode: "shared", Count: 2, CreatedBy: "u1"})
	mustNoErr(t, err)
	for _, v := range tags2 {
		if v.LinkID != links[1].ID {
			t.Fatalf("shared tag bound to %s, want %s", v.LinkID, links[1].ID)
		}
	}

	// 边界:0 / 501 拒绝
	for _, bad := range []int{0, -1, 501} {
		if _, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
			LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: bad, CreatedBy: "u"}); err == nil {
			t.Fatalf("count %d accepted", bad)
		}
	}
	// 500 恰好允许
	if _, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: MaxBatchTags, CreatedBy: "u"}); err != nil {
		t.Fatalf("count 500 refused: %v", err)
	}
	// 空链接列表 / 坏 bind_mode / 跨活动 link
	if _, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID, BindMode: "shared", Count: 1, CreatedBy: "u"}); err == nil {
		t.Fatal("empty link list accepted")
	}
	if _, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID, LinkIDs: []string{links[0].ID}, BindMode: "nope", Count: 1, CreatedBy: "u"}); err == nil {
		t.Fatal("bad bind_mode accepted")
	}
	otherCID, otherLinks := seedCampaignWithLinks(t, s, ten.ID, "draft", 1)
	if _, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID, LinkIDs: []string{otherLinks[0].ID}, BindMode: "shared", Count: 1, CreatedBy: "u"}); err == nil {
		t.Fatal("cross-campaign link accepted")
	}
	_ = otherCID
	// 跨租户 campaign 不可见
	ten2, err := s.CreateTenant("B")
	mustNoErr(t, err)
	if _, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten2.ID, CampaignID: cID, LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 1, CreatedBy: "u"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant batch = %v, want ErrNotFound", err)
	}
}

func TestTagStatusPropagatesToLinkResolution(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	cID, links := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	code := links[0].Code

	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 1, CreatedBy: "u"})
	mustNoErr(t, err)
	tag := tags[0]

	// 初始:available
	if got := s.ResolveLink(code, time.Now()); got.Outcome != OutcomeAvailable {
		t.Fatalf("initial resolve = %s, want available", got.Outcome)
	}
	// 停用标签:公共短码立刻进入停用态
	if _, err := s.SetTagStatus(tag.ID, ten.ID, TagStatusDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if got := s.ResolveLink(code, time.Now()); got.Outcome != OutcomeLinkDisabled {
		t.Fatalf("disabled resolve = %s, want link_disabled", got.Outcome)
	}
	// 恢复:复用
	if _, err := s.SetTagStatus(tag.ID, ten.ID, TagStatusActive); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := s.ResolveLink(code, time.Now()); got.Outcome != OutcomeAvailable {
		t.Fatalf("restored resolve = %s, want available", got.Outcome)
	}
	// 坏状态值拒绝
	if _, err := s.SetTagStatus(tag.ID, ten.ID, "paused"); err == nil {
		t.Fatal("bad status accepted")
	}
}

func TestTagPatchRebindAndClears(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	cID, links := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	tags, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 1, CreatedBy: "u"})
	mustNoErr(t, err)
	tagID := tags[0].ID

	// 换绑到另一活动的 link:campaign 视图随之变化
	cID2, links2 := seedCampaignWithLinks(t, s, ten.ID, "active", 1)
	sto, err := s.CreateStore(ten.ID, "旗舰店", "地址", "u")
	mustNoErr(t, err)
	v, err := s.PatchTag(tagID, ten.ID, TagPatch{
		LinkID: &links2[0].ID, StoreID: &sto.ID,
		UIDHint: strPtr("04:a2:2f:xx"), Label: strPtr("前台立牌"),
	})
	mustNoErr(t, err)
	if v.LinkID != links2[0].ID || v.CampaignID != cID2 || v.Code != links2[0].Code {
		t.Fatalf("rebind view = %+v", v)
	}
	if v.StoreName != "旗舰店" || v.UIDHint != "04:a2:2f:xx" || v.Label != "前台立牌" {
		t.Fatalf("patch view = %+v", v)
	}

	// 分组:挂上再清空
	g, err := s.CreateTagGroup(ten.ID, "g1", "u")
	mustNoErr(t, err)
	v, err = s.PatchTag(tagID, ten.ID, TagPatch{GroupID: &g.ID})
	mustNoErr(t, err)
	if v.GroupName != "g1" {
		t.Fatalf("group name = %q", v.GroupName)
	}
	empty := ""
	v, err = s.PatchTag(tagID, ten.ID, TagPatch{GroupID: &empty, UIDHint: &empty})
	mustNoErr(t, err)
	if v.GroupID != "" || v.UIDHint != "" {
		t.Fatalf("clear view = %+v", v)
	}

	// 跨租户 link / group / store 拒绝
	ten2, err := s.CreateTenant("B")
	mustNoErr(t, err)
	_, l2 := seedCampaignWithLinks(t, s, ten2.ID, "active", 1)
	if _, err := s.PatchTag(tagID, ten.ID, TagPatch{LinkID: &l2[0].ID}); err == nil {
		t.Fatal("cross-tenant link accepted")
	}

	// 删除标签:只删管理面记录,link 原样
	mustNoErr(t, s.DeleteTag(tagID, ten.ID))
	if _, err := s.GetTagView(tagID, ten.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted = %v, want ErrNotFound", err)
	}
	if _, err := s.GetLink(links2[0].ID, ten.ID); err != nil {
		t.Fatalf("link must survive tag delete: %v", err)
	}
	// 跨租户删除不可见
	tags2, err := s.CreateTagsBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID,
		LinkIDs: []string{links[0].ID}, BindMode: "shared", Count: 1, CreatedBy: "u"})
	mustNoErr(t, err)
	if err := s.DeleteTag(tags2[0].ID, ten2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant delete = %v, want ErrNotFound", err)
	}
}

func TestTagListFilters(t *testing.T) {
	s := tagTestStore(t)
	ten, err := s.CreateTenant("A")
	mustNoErr(t, err)
	ten2, err := s.CreateTenant("B")
	mustNoErr(t, err)
	cID, links := seedCampaignWithLinks(t, s, ten.ID, "active", 2)
	cID2, links2 := seedCampaignWithLinks(t, s, ten.ID, "active", 1)

	g, err := s.CreateTagGroup(ten.ID, "g", "u")
	mustNoErr(t, err)
	sto, err := s.CreateStore(ten.ID, "店", "", "u")
	mustNoErr(t, err)

	mustNoErrBatch := func(n NewTagBatch) {
		t.Helper()
		if _, err := s.CreateTagsBatch(n); err != nil {
			t.Fatalf("batch: %v", err)
		}
	}
	mustNoErrBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID, LinkIDs: []string{links[0].ID, links[1].ID},
		BindMode: "rotate", Count: 4, GroupID: g.ID, StoreID: sto.ID, CreatedBy: "u"})
	mustNoErrBatch(NewTagBatch{TenantID: ten.ID, CampaignID: cID2, LinkIDs: []string{links2[0].ID},
		BindMode: "shared", Count: 2, CreatedBy: "u"})
	// 另一租户的数据
	ten3CID, ten3Links := seedCampaignWithLinks(t, s, ten2.ID, "active", 1)
	mustNoErrBatch(NewTagBatch{TenantID: ten2.ID, CampaignID: ten3CID, LinkIDs: []string{ten3Links[0].ID},
		BindMode: "shared", Count: 1, CreatedBy: "u"})

	all, err := s.ListTags(ten.ID, TagFilter{})
	mustNoErr(t, err)
	if len(all) != 6 {
		t.Fatalf("all = %d, want 6", len(all))
	}
	byCampaign, err := s.ListTags(ten.ID, TagFilter{CampaignID: cID})
	mustNoErr(t, err)
	if len(byCampaign) != 4 {
		t.Fatalf("byCampaign = %d, want 4", len(byCampaign))
	}
	byGroup, err := s.ListTags(ten.ID, TagFilter{GroupID: g.ID})
	mustNoErr(t, err)
	if len(byGroup) != 4 {
		t.Fatalf("byGroup = %d, want 4", len(byGroup))
	}
	byStore, err := s.ListTags(ten.ID, TagFilter{StoreID: sto.ID})
	mustNoErr(t, err)
	if len(byStore) != 4 {
		t.Fatalf("byStore = %d, want 4", len(byStore))
	}
	// 停用一个 → status 过滤
	if _, err := s.SetTagStatus(byCampaign[0].ID, ten.ID, TagStatusDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	disabled, err := s.ListTags(ten.ID, TagFilter{Status: TagStatusDisabled})
	mustNoErr(t, err)
	if len(disabled) != 1 {
		t.Fatalf("disabled = %d, want 1", len(disabled))
	}
	active, err := s.ListTags(ten.ID, TagFilter{Status: TagStatusActive})
	mustNoErr(t, err)
	if len(active) != 5 {
		t.Fatalf("active = %d, want 5", len(active))
	}
	// 租户隔离
	other, err := s.ListTags(ten2.ID, TagFilter{})
	mustNoErr(t, err)
	if len(other) != 1 {
		t.Fatalf("tenant B sees %d tags, want 1", len(other))
	}
	// 非法 status 过滤
	if _, err := s.ListTags(ten.ID, TagFilter{Status: "bogus"}); err == nil {
		t.Fatal("bogus status filter accepted")
	}
}

func strPtr(s string) *string { return &s }
