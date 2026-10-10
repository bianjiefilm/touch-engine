package matrixconsume

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

func readyPermission(_ context.Context, _, _, _ string) (Permission, error) {
	return Permission{Allowed: true, Supply: SupplyReady, Reason: "brand_publish"}, nil
}

func missingPermission(_ context.Context, _, _, _ string) (Permission, error) {
	return Permission{Supply: SupplyMissing, Reason: "permission_unconfigured"}, nil
}

type scriptedSink struct {
	available bool
	ack       DraftAck
	err       error
	puts      int
}

func (s *scriptedSink) Available() bool { return s != nil && s.available }

func (s *scriptedSink) PutDraft(_ context.Context, _ matrixhandoff.Draft) (DraftAck, error) {
	s.puts++
	if s.err != nil {
		return DraftAck{}, s.err
	}
	return s.ack, nil
}

func sampleRequest() matrixhandoff.Request {
	return matrixhandoff.Request{
		TenantID:            "tnt_a",
		BrandID:             "br_a",
		StoreID:             "sto_a",
		ActivityID:          "cmp_a",
		ActivityVersion:     3,
		Assets:              []matrixhandoff.Asset{{ID: "asset_1", Hash: strings.Repeat("ab", 32), Kind: "video"}},
		OfferText:           "到店礼",
		OfferExpiry:         time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Disclosure:          "广告",
		ReturnLocationToken: "ret_1",
	}
}

func TestScheduleDraftIsNotOutbound(t *testing.T) {
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_1", Status: "draft", Executed: false}}
	effects := &matrixhandoff.EffectSink{}
	c := New(PermissionFunc(readyPermission), sink, effects)
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.OutboundComplete || out.Draft.Executed || out.Draft.CustomerPublish || sink.puts != 1 {
		t.Fatalf("draft treated as send: %+v puts=%d", out, sink.puts)
	}
	if out.Draft.Status != "draft" || out.Draft.Plan.ID != "pln_1" {
		t.Fatalf("draft = %+v", out.Draft)
	}
	if strings.Contains(out.Copy, "发布成功") || strings.Contains(out.Copy, "外发完成") {
		t.Fatalf("copy claims a real send: %q", out.Copy)
	}
	if !out.MatrixButton.Enabled || out.LeadButton.Enabled != true || out.UGCButton.Enabled != true {
		t.Fatalf("buttons = matrix:%+v lead:%+v ugc:%+v", out.MatrixButton, out.LeadButton, out.UGCButton)
	}
	if effects.UGCWrites != 0 || effects.LeadWrites != 0 || effects.RewardWrites != 0 {
		t.Fatalf("customer effects moved: %+v", effects)
	}
}

func TestMissingSupplyDisablesOnlyMatrixButton(t *testing.T) {
	effects := &matrixhandoff.EffectSink{}
	c := New(PermissionFunc(missingPermission), &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_x", Status: "draft"}}, effects)
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if out.MatrixButton.Enabled || out.MatrixButton.Reason != "permission_supply_missing" {
		t.Fatalf("matrix button = %+v", out.MatrixButton)
	}
	if out.LeadButton.Enabled != true || out.UGCButton.Enabled != false || out.OutboundComplete || out.Draft.ID != "" {
		t.Fatalf("other surfaces changed: %+v", out)
	}
}

func TestMatrixSupplyMissingDoesNotRecordDraft(t *testing.T) {
	c := New(PermissionFunc(readyPermission), &scriptedSink{available: false}, &matrixhandoff.EffectSink{})
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if out.MatrixButton.Enabled || out.MatrixButton.Reason != "matrix_supply_missing" || out.OutboundComplete || out.Draft.ID != "" {
		t.Fatalf("missing matrix = %+v", out)
	}
}

func TestPublishedAckIsNotCompletion(t *testing.T) {
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_live", Status: "published", Executed: true}}
	c := New(PermissionFunc(readyPermission), sink, &matrixhandoff.EffectSink{})
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_a", TenantID: "tnt_a", BrandID: "br_a", Role: "org_owner",
	}, sampleRequest(), Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err == nil || !errors.Is(err, ErrNotDraft) {
		t.Fatalf("err = %v", err)
	}
	if out.OutboundComplete || out.Draft.Executed || out.MatrixButton.Reason != "matrix_ack_not_draft" {
		t.Fatalf("published ack accepted: %+v", out)
	}
}

func TestCrossTenantDoesNotSchedule(t *testing.T) {
	sink := &scriptedSink{available: true, ack: DraftAck{PlanID: "pln_1", Status: "draft"}}
	c := New(PermissionFunc(readyPermission), sink, &matrixhandoff.EffectSink{})
	req := sampleRequest()
	out, err := c.Schedule(context.Background(), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr_b", TenantID: "tnt_b", BrandID: "br_a", Role: "org_owner",
	}, req, Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err == nil || out.OutboundComplete || sink.puts != 0 || out.MatrixButton.Enabled {
		t.Fatalf("cross tenant scheduled: err=%v out=%+v puts=%d", err, out, sink.puts)
	}
}
