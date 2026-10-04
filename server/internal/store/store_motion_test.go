package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/storemotion"
)

func TestStoreMotionChannelsRoundTripAndIdempotent(t *testing.T) {
	s := openStore(t)
	ctx := seedTwoTenants(t, s)
	sto, err := s.CreateStore(ctx.tenA.ID, "南山店", "南山大道1号", ctx.ownA.PrincipalRef)
	must(t, err)
	camp, err := s.CreateCampaign(NewCampaign{TenantID: ctx.tenA.ID, Title: "国庆", StoreID: sto.ID, CreatedBy: ctx.ownA.PrincipalRef})
	must(t, err)

	var cols int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('store_motion_requests') WHERE name IN ('channels','aspect_ratios')`).Scan(&cols); err != nil || cols != 2 {
		t.Fatalf("channels columns = %d %v", cols, err)
	}

	probe := &storemotion.Probe{}
	req := storemotion.Request{
		TenantID: ctx.tenA.ID, StoreID: sto.ID, ActivityID: camp.ID,
		Params: storemotion.Params{StoreName: "南山店", ActivityTime: "10月1日-10月7日", Price: "19.9", Address: "南山大道1号", OfferCopy: "第二杯半价", CTA: "进店领取", Channels: "wechat_grid,table_tent", AspectRatios: "9:16"},
	}
	first, created, err := s.SaveStoreMotion(req, ctx.ownA.PrincipalRef, probe)
	must(t, err)
	if !created || first.Params.Channels != "wechat_grid,table_tent" || first.Params.AspectRatios != "9:16" {
		t.Fatalf("first = %+v", first)
	}
	again, created, err := s.SaveStoreMotion(req, ctx.ownA.PrincipalRef, probe)
	must(t, err)
	if created || again.Version != 1 {
		t.Fatal("same eight params inserted another version")
	}
	req.Params.Channels = "wechat_grid"
	changed, created, err := s.SaveStoreMotion(req, ctx.ownA.PrincipalRef, probe)
	must(t, err)
	if !created || changed.Version != 2 || changed.Params.Channels != "wechat_grid" {
		t.Fatalf("channels change did not version: %+v created=%v", changed, created)
	}
	got, err := s.LatestStoreMotion(ctx.tenA.ID, camp.ID)
	must(t, err)
	if got.Params.Channels != "wechat_grid" || got.Params.AspectRatios != "9:16" {
		t.Fatalf("round trip lost channels: %+v", got.Params)
	}
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 {
		t.Fatal("channels edit called a model")
	}

	// 0018 时代旧形态行（新列靠 DEFAULT ''）：读回空串，不报错。
	if _, err := s.DB.Exec(`INSERT INTO store_motion_requests
		(id,tenant_id,store_id,campaign_id,version,store_name,activity_time,price,address,offer_copy,cta,origin_app,origin_context_ref,status,unchanged_store_rerun,unchanged_store_note,created_by,created_at)
		SELECT id||'_old',tenant_id,store_id,campaign_id,version+10,store_name,activity_time,price,address,offer_copy,cta,origin_app,origin_context_ref,status,unchanged_store_rerun,unchanged_store_note,created_by,created_at
		FROM store_motion_requests WHERE id=?`, changed.ID); err != nil {
		t.Fatal(err)
	}
	oldRow, err := s.LatestStoreMotion(ctx.tenA.ID, camp.ID)
	must(t, err)
	if oldRow.Params.Channels != "" || oldRow.Params.AspectRatios != "" {
		t.Fatalf("old-shape row = %+v", oldRow.Params)
	}
}

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
