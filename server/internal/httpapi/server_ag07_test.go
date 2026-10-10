package httpapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixconsume"
	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func TestSessionMembershipsIgnoreForgedTenant(t *testing.T) {
	f := newFixture(t, false)
	status, _, body := f.do(t, "GET", "/api/v1/session/memberships?tenant_id="+f.tenB, "sess-owner-a", f.tenB, "")
	if status != 200 {
		t.Fatalf("memberships = %d %v", status, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	row, _ := items[0].(map[string]any)
	if row["tenant_id"] != f.tenA || row["display_name"] != "商家A" || row["source"] != "membership" || row["role"] != "org_owner" {
		t.Fatalf("row = %v", row)
	}
	if status, _, who := f.do(t, "GET", "/api/v1/whoami", "sess-owner-a", "", ""); status != 400 {
		t.Fatalf("whoami without tenant = %d %v", status, who)
	}
	if status, _, who := f.do(t, "GET", "/api/v1/whoami", "sess-owner-a", f.tenB, ""); status != 403 {
		t.Fatalf("whoami forged tenant = %d %v", status, who)
	}
	if status, _, _ := f.do(t, "GET", "/api/v1/session/memberships", "", "", ""); status != 401 {
		t.Fatalf("guest memberships = %d", status)
	}

	extra, err := f.s.St.CreateMember(f.tenB, f.ownA, "staff", "跨户", "test", true)
	if err != nil {
		t.Fatal(err)
	}
	status, _, body = f.do(t, "GET", "/api/v1/session/memberships", "sess-owner-a", f.tenB, "")
	items, _ = body["items"].([]any)
	if status != 200 || len(items) != 2 {
		t.Fatalf("two memberships = %d %v", status, items)
	}
	off := false
	if _, err := f.s.St.UpdateMember(extra.ID, nil, &off, nil); err != nil {
		t.Fatal(err)
	}
	status, _, body = f.do(t, "GET", "/api/v1/session/memberships", "sess-owner-a", "", "")
	items, _ = body["items"].([]any)
	if status != 200 || len(items) != 1 {
		t.Fatalf("disabled dropped = %d %v", status, items)
	}
	row, _ = items[0].(map[string]any)
	if row["tenant_id"] != f.tenA {
		t.Fatalf("remaining = %v", row)
	}
}

type countingSink struct {
	puts      int
	available bool
	ack       matrixconsume.DraftAck
}

func (c *countingSink) Available() bool { return c.available }

func (c *countingSink) PutDraft(context.Context, matrixhandoff.Draft) (matrixconsume.DraftAck, error) {
	c.puts++
	return c.ack, nil
}

func readyMatrix(sink *countingSink, effects *matrixhandoff.EffectSink) *matrixconsume.Consumer {
	gate := matrixconsume.PermissionFunc(func(context.Context, string, string, string) (matrixconsume.Permission, error) {
		return matrixconsume.Permission{Allowed: true, Supply: matrixconsume.SupplyReady, Reason: "ok"}, nil
	})
	return matrixconsume.New(gate, sink, effects)
}

func TestMatrixDraftMissingSupplyDisablesOnlyThatButton(t *testing.T) {
	f := newFixture(t, false)
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "无矩阵", CreatedBy: f.ownA})
	if err != nil {
		t.Fatal(err)
	}
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	if status != 200 || body["outbound_complete"] != false {
		t.Fatalf("nil matrix = %d %v", status, body)
	}
	matrix, _ := body["matrix_button"].(map[string]any)
	lead, _ := body["lead_button"].(map[string]any)
	ugc, _ := body["ugc_button"].(map[string]any)
	if matrix["enabled"] != false || matrix["reason"] != matrixconsume.ReasonPermissionSupply || lead["enabled"] != true || ugc["enabled"] != true {
		t.Fatalf("buttons = %v", body)
	}
	if strings.Contains(stringMust(body["copy"]), "发布成功") || strings.Contains(stringMust(body["copy"]), "外发完成") {
		t.Fatalf("copy = %v", body["copy"])
	}
}

func TestMatrixDraftRecordsDraftNotOutbound(t *testing.T) {
	f := newFixture(t, false)
	sink := &countingSink{available: true, ack: matrixconsume.DraftAck{PlanID: "pln_http", Status: matrixhandoff.StatusDraft, Executed: false}}
	effects := &matrixhandoff.EffectSink{}
	f.s.Matrix = readyMatrix(sink, effects)
	camp := seedMatrixCampaign(t, f, time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339), true)

	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-a", f.tenA, `{"brand_id":"forged","sha256":"00"}`)
	if status != 200 || body["outbound_complete"] != false {
		t.Fatalf("draft = %d %v", status, body)
	}
	matrix, _ := body["matrix_button"].(map[string]any)
	lead, _ := body["lead_button"].(map[string]any)
	ugc, _ := body["ugc_button"].(map[string]any)
	copy := stringMust(body["copy"])
	if matrix["enabled"] != true || matrix["reason"] != matrixconsume.ReasonDraftRecorded || lead["enabled"] != true || ugc["enabled"] != true {
		t.Fatalf("buttons = %v", body)
	}
	if body["draft_status"] != matrixhandoff.StatusDraft || body["draft_id"] == "" || sink.puts != 1 {
		t.Fatalf("draft record = %v puts %d", body, sink.puts)
	}
	if strings.Contains(copy, "发布成功") || strings.Contains(copy, "外发完成") || effects.LeadWrites != 0 || effects.UGCWrites != 0 || effects.RewardWrites != 0 {
		t.Fatalf("copy %q effects %+v", copy, effects)
	}

	if status, _, denied := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-owner-b", f.tenB, ""); status != 404 || sink.puts != 1 {
		t.Fatalf("cross tenant = %d %v puts %d", status, denied, sink.puts)
	}
	if status, _, staff := f.do(t, "POST", "/api/v1/campaigns/"+camp.ID+"/matrix-draft", "sess-staff-a", f.tenA, ""); status != 422 || staff["outbound_complete"] != false || sink.puts != 1 {
		t.Fatalf("staff = %d %v puts %d", status, staff, sink.puts)
	}
}

func TestMatrixDraftBlocksExpiredAndMissingVideo(t *testing.T) {
	f := newFixture(t, false)
	sink := &countingSink{available: true, ack: matrixconsume.DraftAck{PlanID: "pln_http", Status: matrixhandoff.StatusDraft}}
	f.s.Matrix = readyMatrix(sink, &matrixhandoff.EffectSink{})

	expired := seedMatrixCampaign(t, f, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), true)
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+expired.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	matrix, _ := body["matrix_button"].(map[string]any)
	if status != 200 || matrix["reason"] != matrixhandoff.ReasonOfferExpired || sink.puts != 0 || body["outbound_complete"] != false {
		t.Fatalf("expired = %d %v", status, body)
	}

	plain := seedMatrixCampaign(t, f, time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339), false)
	status, _, body = f.do(t, "POST", "/api/v1/campaigns/"+plain.ID+"/matrix-draft", "sess-owner-a", f.tenA, "")
	matrix, _ = body["matrix_button"].(map[string]any)
	lead, _ := body["lead_button"].(map[string]any)
	if status != 200 || matrix["reason"] != matrixhandoff.ReasonNeedsVideo || lead["enabled"] != true || sink.puts != 0 {
		t.Fatalf("needs video = %d %v", status, body)
	}
}

func seedMatrixCampaign(t *testing.T, f *fixture, endsAt string, withVideo bool) store.Campaign {
	t.Helper()
	if err := f.s.St.BindTenantBrand(f.tenA, "brd_a"); err != nil {
		t.Fatal(err)
	}
	sto, err := f.s.St.CreateStore(f.tenA, "南山店", "南山大道", f.ownA)
	if err != nil {
		t.Fatal(err)
	}
	camp, err := f.s.St.CreateCampaign(store.NewCampaign{
		TenantID: f.tenA, Title: "国庆档", PublicContent: "到店有礼", StoreID: sto.ID,
		EndsAt: endsAt, OrderRef: "ord_opaque", CreatedBy: f.ownA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !withVideo {
		return camp
	}
	sha := strings.Repeat("ab", 32)
	lib, err := f.s.St.CreateLibAsset(f.tenA, assetlib.Registration{
		AssetRef: "ast_vid", SHA256: sha, MediaType: assetlib.MediaTypeVideo,
		Source: assetlib.SourceMerchantUpload, Purpose: "活动成片", GrantRef: "grant_1",
	}, sto.ID, f.ownA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.AddCampaignAsset(f.tenA, camp.ID, lib.AssetRef, "3", f.ownA); err != nil {
		t.Fatal(err)
	}
	return camp
}

func stringMust(v any) string {
	s, _ := v.(string)
	return s
}
