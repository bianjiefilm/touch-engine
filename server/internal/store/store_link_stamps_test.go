package store

import "testing"

func TestListLinkStampsOldestFirstAndEmptyStamps(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	sto, err := s.CreateStore(ctx.tenA.ID, "南山店", "南山大道1号", ctx.ownA.PrincipalRef)
	must(t, err)
	camp, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "国庆", StoreID: sto.ID, CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)

	stamps, err := s.ListLinkStamps(ctx.tenA.ID, camp.ID)
	must(t, err)
	if len(stamps) != 0 {
		t.Fatalf("no links yet, got %d", len(stamps))
	}

	first, err := s.CreateLink(ctx.tenA.ID, camp.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	second, err := s.CreateLink(ctx.tenA.ID, camp.ID, ctx.ownA.PrincipalRef)
	must(t, err)
	must(t, s.StampLinkBrand(first.ID, ctx.tenA.ID, "brd_a", "touch.example.com"))

	stamps, err = s.ListLinkStamps(ctx.tenA.ID, camp.ID)
	must(t, err)
	if len(stamps) != 2 {
		t.Fatalf("stamps = %d", len(stamps))
	}
	if stamps[0].Code != first.Code || stamps[0].LinkID != first.ID || !stamps[0].Enabled {
		t.Fatalf("mint order broken: %+v vs %s", stamps[0], first.ID)
	}
	if stamps[0].PublishedBrandID != "brd_a" || stamps[0].PublishedHost != "touch.example.com" {
		t.Fatalf("stamp lost: %+v", stamps[0])
	}
	if stamps[1].Code != second.Code || stamps[1].PublishedBrandID != "" || stamps[1].PublishedHost != "" {
		t.Fatalf("unstamped link = %+v", stamps[1])
	}
	if stamps, err := s.ListLinkStamps(ctx.tenB.ID, camp.ID); err != nil || len(stamps) != 0 {
		t.Fatalf("cross tenant = %d %v", len(stamps), err)
	}
}
