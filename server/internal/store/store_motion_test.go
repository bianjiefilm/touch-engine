package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/storemotion"
)

func TestStoreMotionRecordsParamChangeWithoutRerunClaim(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	stoA, err := s.CreateStore(ctx.tenA.ID, "南山店", "南山大道1号", ctx.ownA.PrincipalRef)
	must(t, err)
	stoB, err := s.CreateStore(ctx.tenA.ID, "福田店", "福田路2号", ctx.ownA.PrincipalRef)
	must(t, err)
	campA, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "国庆", StoreID: stoA.ID, CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)
	campB, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "国庆", StoreID: stoB.ID, CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)

	probe := &storemotion.Probe{}
	reqA := storemotion.Request{
		TenantID: ctx.tenA.ID, StoreID: stoA.ID, ActivityID: campA.ID,
		Params: storemotion.Params{StoreName: "南山店", ActivityTime: "10月1日-10月7日", Price: "19.9", Address: "南山大道1号", OfferCopy: "第二杯半价", CTA: "进店领取"},
	}
	reqB := reqA
	reqB.StoreID = stoB.ID
	reqB.ActivityID = campB.ID
	reqB.Params.StoreName = "福田店"
	reqB.Params.Address = "福田路2号"

	first, created, err := s.SaveStoreMotion(reqA, ctx.ownA.PrincipalRef, probe)
	must(t, err)
	if !created || first.Version != 1 || first.Declaration.RevisionID != nil || first.Declaration.Status != "还没生成成片" {
		t.Fatalf("first = %+v created=%v", first, created)
	}
	if !strings.Contains(first.Declaration.OriginContextRef, stoA.ID) || !strings.Contains(first.Declaration.OriginContextRef, campA.ID) {
		t.Fatalf("context = %s", first.Declaration.OriginContextRef)
	}
	other, created, err := s.SaveStoreMotion(reqB, ctx.ownA.PrincipalRef, probe)
	must(t, err)
	if !created || other.Version != 1 {
		t.Fatalf("other = %+v created=%v", other, created)
	}

	reqA.Params.Price = "到店询价"
	changed, created, err := s.SaveStoreMotion(reqA, ctx.ownA.PrincipalRef, probe)
	must(t, err)
	if !created || changed.Version != 2 || changed.Params.Price != "到店询价" {
		t.Fatalf("changed = %+v created=%v", changed, created)
	}
	again, created, err := s.SaveStoreMotion(reqA, ctx.ownA.PrincipalRef, probe)
	must(t, err)
	if created || again.ID != changed.ID {
		t.Fatal("identical params inserted another row")
	}
	kept, err := s.LatestStoreMotion(ctx.tenA.ID, campB.ID)
	must(t, err)
	if kept.Params != other.Params || kept.Version != 1 {
		t.Fatalf("unchanged store was rewritten: %+v", kept)
	}
	if kept.Declaration.UnchangedStoreRerun != "not_claimed" || changed.Declaration.UnchangedStoreRerun != "not_claimed" {
		t.Fatal("claimed an unchanged store skipped rerun")
	}
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 || len(probe.Payloads) != 0 {
		t.Fatalf("model calls = %d render = %d", probe.ModelCalls, probe.RenderCalls)
	}

	var priceType string
	if err := s.DB.QueryRow(`SELECT type FROM pragma_table_info('store_motion_requests') WHERE name='price'`).Scan(&priceType); err != nil || !strings.EqualFold(priceType, "TEXT") {
		t.Fatalf("price column = %q %v", priceType, err)
	}
	var cents int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('store_motion_requests') WHERE name IN ('amount_cents','price_cents','settlement')`).Scan(&cents); err != nil || cents != 0 {
		t.Fatalf("settlement columns = %d %v", cents, err)
	}
	if _, err := s.LatestStoreMotion(ctx.tenB.ID, campA.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant read = %v", err)
	}
	var calls int
	if err := s.DB.QueryRow(`SELECT COALESCE(SUM(model_calls)+SUM(render_calls),0) FROM store_motion_requests`).Scan(&calls); err != nil || calls != 0 {
		t.Fatalf("stored calls = %d %v", calls, err)
	}
}
