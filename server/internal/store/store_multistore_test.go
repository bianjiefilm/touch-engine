package store

import (
	"errors"
	"testing"
	"time"
)

// HUI-1674 FEAT-0175 存储层:门店启停、成员作用域、按店活动列表、公共解析的
// 停店标注。全部走真实 sqlite(embedded migrations)。



func TestStoreStatusLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)

	sto, err := s.CreateStore(ctx.tenA.ID, "旗舰店", "A 市 B 街 1 号", ctx.ownA.PrincipalRef)
	must(t, err)
	if sto.Status != StoreStatusActive {
		t.Fatalf("new store status = %q, want active", sto.Status)
	}
	got, err := s.GetStore(sto.ID, ctx.tenA.ID)
	if err != nil || got.Status != StoreStatusActive {
		t.Fatalf("get store: %v status=%q", err, got.Status)
	}

	// edit name/address
	if _, err := s.UpdateStore(sto.ID, ctx.tenA.ID, strPtr("旗舰店(新装修)"), strPtr("A 市 B 街 2 号")); err != nil {
		t.Fatalf("update store: %v", err)
	}
	got, err = s.GetStore(sto.ID, ctx.tenA.ID)
	if err != nil || got.Name != "旗舰店(新装修)" || got.Address != "A 市 B 街 2 号" {
		t.Fatalf("update store not applied: %+v %v", got, err)
	}

	// disable / re-enable; no cascade touches campaigns
	camp, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "店庆", StoreID: sto.ID, CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	dis, err := s.SetStoreStatus(sto.ID, ctx.tenA.ID, StoreStatusDisabled)
	if err != nil || dis.Status != StoreStatusDisabled {
		t.Fatalf("disable store: %v %+v", err, dis)
	}
	after, err := s.GetCampaign(camp.ID, ctx.tenA.ID)
	if err != nil || after.Status != camp.Status {
		t.Fatalf("campaign must not cascade on store disable: %v %q", err, after.Status)
	}
	back, err := s.SetStoreStatus(sto.ID, ctx.tenA.ID, StoreStatusActive)
	if err != nil || back.Status != StoreStatusActive {
		t.Fatalf("re-enable store: %v %+v", err, back)
	}
	// 非法状态值拒绝
	if _, err := s.SetStoreStatus(sto.ID, ctx.tenA.ID, "closed"); err == nil {
		t.Fatal("bad store status must be refused")
	}
	// 跨租户不可见
	if _, err := s.SetStoreStatus(sto.ID, ctx.tenB.ID, StoreStatusDisabled); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant store status = %v, want ErrNotFound", err)
	}
	// 跨租户改店不可见
	if _, err := s.UpdateStore(sto.ID, ctx.tenB.ID, strPtr("x"), nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant store update = %v, want ErrNotFound", err)
	}
}

func TestMemberStoreScopeRoundtrip(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	sto, err := s.CreateStore(ctx.tenA.ID, "分店", "", ctx.ownA.PrincipalRef)
	must(t, err)

	// 总部成员:无作用域
	m1, err := s.CreateMember(ctx.tenA.ID, "usr_hq", "org_owner", "总部", "boot", true)
	must(t, err)
	if m1.StoreScope != "" {
		t.Fatalf("org_owner scope = %q, want empty", m1.StoreScope)
	}
	// 门店经理:作用域=本店
	m2, err := s.CreateMemberScoped(ctx.tenA.ID, "usr_mgr", "store_manager", "店长", "boot", true, sto.ID)
	must(t, err)
	if m2.StoreScope != sto.ID {
		t.Fatalf("manager scope = %q, want %q", m2.StoreScope, sto.ID)
	}
	got, err := s.GetMemberByPrincipal(ctx.tenA.ID, "usr_mgr")
	if err != nil || got.StoreScope != sto.ID {
		t.Fatalf("get by principal: %v scope=%q", err, got.StoreScope)
	}
	// 作用域改绑到同租户另一家店
	sto2, err := s.CreateStore(ctx.tenA.ID, "二店", "", ctx.ownA.PrincipalRef)
	must(t, err)
	newRole := "store_manager"
	patched, err := s.UpdateMemberScoped(m2.ID, &newRole, nil, nil, &sto2.ID)
	if err != nil || patched.StoreScope != sto2.ID {
		t.Fatalf("patch scope: %v %+v", err, patched)
	}
	// 显式清空作用域(存储层按调用者指示执行)
	cleared := ""
	patched, err = s.UpdateMemberScoped(m2.ID, nil, nil, nil, &cleared)
	if err != nil || patched.StoreScope != "" {
		t.Fatalf("clear scope: %v %+v", err, patched)
	}
	// 列表带出作用域
	mems, err := s.ListMembers(ctx.tenA.ID)
	must(t, err)
	if len(mems) != 3 {
		t.Fatalf("list members n=%d, want 3", len(mems))
	}
}

func TestListCampaignsByStore(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	stoA, err := s.CreateStore(ctx.tenA.ID, "A 店", "", ctx.ownA.PrincipalRef)
	must(t, err)
	stoB, err := s.CreateStore(ctx.tenA.ID, "B 店", "", ctx.ownA.PrincipalRef)
	must(t, err)

	for _, sid := range []string{stoA.ID, stoB.ID, ""} {
		_, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "活动", StoreID: sid, CreatedBy: ctx.ownA.PrincipalRef})
		must(t, err)
	}
	inA, err := s.ListCampaignsByStore(ctx.tenA.ID, stoA.ID)
	must(t, err)
	if len(inA) != 1 || inA[0].StoreID != stoA.ID {
		t.Fatalf("list by store A = %+v, want exactly A's campaign", inA)
	}
	inB, err := s.ListCampaignsByStore(ctx.tenA.ID, stoB.ID)
	must(t, err)
	if len(inB) != 1 || inB[0].StoreID != stoB.ID {
		t.Fatalf("list by store B = %+v, want exactly B's campaign", inB)
	}
	all, err := s.ListCampaigns(ctx.tenA.ID)
	must(t, err)
	if len(all) != 3 {
		t.Fatalf("tenant list = %d, want 3 (HQ sees all incl. unassigned)", len(all))
	}
	// 另一租户的按店列表恒空
	other, err := s.ListCampaignsByStore(ctx.tenB.ID, stoA.ID)
	must(t, err)
	if len(other) != 0 {
		t.Fatalf("cross-tenant by-store list = %d, want 0", len(other))
	}
}

func TestResolveLinkStoreUnavailableNotice(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	sto, err := s.CreateStore(ctx.tenA.ID, "C 店", "", ctx.ownA.PrincipalRef)
	must(t, err)
	camp, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "在营活动", StoreID: sto.ID, CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	if _, err := s.TransitionCampaign(camp.ID, ctx.tenA.ID, "active"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	link, err := s.CreateLink(ctx.tenA.ID, camp.ID, ctx.ownA.PrincipalRef)
	must(t, err)

	// healthy store: no notice
	res := s.ResolveLink(link.Code, time.Now())
	if res.Outcome != OutcomeAvailable || res.StoreUnavailable {
		t.Fatalf("healthy resolve = %v notice=%v", res.Outcome, res.StoreUnavailable)
	}
	// disabled store: campaign NOT silenced, notice set
	if _, err := s.SetStoreStatus(sto.ID, ctx.tenA.ID, StoreStatusDisabled); err != nil {
		t.Fatalf("disable: %v", err)
	}
	res = s.ResolveLink(link.Code, time.Now())
	if res.Outcome != OutcomeAvailable {
		t.Fatalf("disabled store must NOT silently offlink: outcome=%v", res.Outcome)
	}
	if !res.StoreUnavailable {
		t.Fatal("store unavailable notice must be set for available campaign in disabled store")
	}
	// unbound campaign: never a notice
	camp2, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "无店活动", CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	if _, err := s.TransitionCampaign(camp2.ID, ctx.tenA.ID, "active"); err != nil {
		t.Fatalf("activate2: %v", err)
	}
	link2, err := s.CreateLink(ctx.tenA.ID, camp2.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	res = s.ResolveLink(link2.Code, time.Now())
	if res.Outcome != OutcomeAvailable || res.StoreUnavailable {
		t.Fatalf("unbound resolve = %v notice=%v, want available/no notice", res.Outcome, res.StoreUnavailable)
	}
}
