// Package matrixconsume is the Touch-side consumer of a matrix draft and the
// public permission read. A recorded draft is not an outbound send. Customer
// leads and UGC are different buttons and are not written here.
package matrixconsume

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

const (
	SupplyReady   = "ready"
	SupplyMissing = "missing"

	ReasonPermissionSupply = "permission_supply_missing"
	ReasonMatrixSupply     = "matrix_supply_missing"
	ReasonAckNotDraft      = "matrix_ack_not_draft"
	ReasonCrossTenant      = "cross_tenant"
	ReasonForbidden        = "forbidden"
	ReasonDraftRecorded    = "draft_recorded"
)

var (
	ErrNotDraft    = errors.New("matrixconsume: ack is not a draft")
	ErrCrossTenant = errors.New("matrixconsume: actor tenant does not own the activity")
)

// Permission is one public-permission read. Supply "missing" means the
// permission API is not configured or did not answer; it is not a grant.
type Permission struct {
	Allowed bool
	Supply  string
	Reason  string
}

// PermissionGate is the AG01 public permission port. A nil gate is missing supply.
type PermissionGate interface {
	Decide(ctx context.Context, principalID, tenantID, brandID string) (Permission, error)
}

// PermissionFunc adapts a function to PermissionGate.
type PermissionFunc func(ctx context.Context, principalID, tenantID, brandID string) (Permission, error)

// Decide calls the function.
func (f PermissionFunc) Decide(ctx context.Context, principalID, tenantID, brandID string) (Permission, error) {
	if f == nil {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	return f(ctx, principalID, tenantID, brandID)
}

// DraftAck is the matrix draft receipt. Anything other than status draft with
// executed false is not a completed send and is not stored as one.
type DraftAck struct {
	PlanID   string
	Status   string
	Executed bool
}

// DraftSink is the real matrix draft API. Available false means that supply
// is missing; only the matrix button is disabled.
type DraftSink interface {
	Available() bool
	PutDraft(ctx context.Context, doc matrixhandoff.Draft) (DraftAck, error)
}

// Actor is the signed-in member. The tenant comes from membership resolution,
// not from a hand-filled request field.
type Actor struct {
	PrincipalID string
	TenantID    string
	BrandID     string
	Role        string
}

// Button is one surface control. Lead and UGC are echoed unchanged.
type Button struct {
	Enabled bool
	Reason  string
}

// Surfaces is the state of the buttons this call must not rewrite.
type Surfaces struct {
	LeadEnabled bool
	UGCEnabled  bool
}

// Outcome is the consumer result. OutboundComplete is never set for a draft.
type Outcome struct {
	MatrixButton     Button
	LeadButton       Button
	UGCButton        Button
	Draft            matrixhandoff.Draft
	Copy             string
	OutboundComplete bool
}

// Consumer schedules a merchant matrix draft.
type Consumer struct {
	gate    PermissionGate
	sink    DraftSink
	effects *matrixhandoff.EffectSink
}

// New returns a consumer. Nil dependencies are missing supply, not success.
func New(gate PermissionGate, sink DraftSink, effects *matrixhandoff.EffectSink) *Consumer {
	if effects == nil {
		effects = &matrixhandoff.EffectSink{}
	}
	return &Consumer{gate: gate, sink: sink, effects: effects}
}

// Schedule records a matrix draft or disables only the matrix button.
// Customer lead and UGC buttons are returned as given. Customer effect
// counters are not incremented.
func (c *Consumer) Schedule(ctx context.Context, now time.Time, actor Actor, req matrixhandoff.Request, surfaces Surfaces) (Outcome, error) {
	out := Outcome{
		LeadButton: Button{Enabled: surfaces.LeadEnabled},
		UGCButton:  Button{Enabled: surfaces.UGCEnabled},
	}
	if strings.TrimSpace(actor.TenantID) == "" || actor.TenantID != strings.TrimSpace(req.TenantID) || actor.BrandID != strings.TrimSpace(req.BrandID) {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonCrossTenant}
		return out, ErrCrossTenant
	}
	if actor.Role != "org_owner" && actor.Role != matrixhandoff.RoleBrandPublisher {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonForbidden}
		return out, errors.New("matrixconsume: role cannot schedule a brand draft")
	}
	perm, err := c.decide(ctx, actor)
	if err != nil || perm.Supply != SupplyReady {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonPermissionSupply}
		return out, nil
	}
	if !perm.Allowed {
		reason := perm.Reason
		if reason == "" {
			reason = ReasonForbidden
		}
		out.MatrixButton = Button{Enabled: false, Reason: reason}
		return out, nil
	}
	if c.sink == nil || !c.sink.Available() {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonMatrixSupply}
		return out, nil
	}
	preview := matrixhandoff.Draft{
		Activity: matrixhandoff.TouchActivity{
			TenantID: req.TenantID, BrandID: req.BrandID, StoreID: req.StoreID,
			ActivityID: req.ActivityID, Version: req.ActivityVersion,
		},
		ActivityStatus: matrixhandoff.ActivityOpen,
		Status:         matrixhandoff.StatusDraft,
		Copy:           matrixhandoff.CopyDraft,
	}
	ack, err := c.sink.PutDraft(ctx, preview)
	if err != nil {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonMatrixSupply}
		return out, nil
	}
	if ack.Executed || ack.Status != matrixhandoff.StatusDraft || strings.TrimSpace(ack.PlanID) == "" {
		out.MatrixButton = Button{Enabled: false, Reason: ReasonAckNotDraft}
		return out, ErrNotDraft
	}
	svc := matrixhandoff.New(ackClient{id: ack.PlanID}, c.effects)
	recorded, err := svc.CreateDraft(now, matrixhandoff.Actor{
		TenantID: actor.TenantID,
		BrandID:  actor.BrandID,
		Role:     matrixhandoff.RoleBrandPublisher,
	}, req)
	if err != nil {
		out.MatrixButton = Button{Enabled: false, Reason: matrixhandoff.ReasonOf(err)}
		if out.MatrixButton.Reason == "" {
			out.MatrixButton.Reason = ReasonMatrixSupply
		}
		return out, err
	}
	recorded.Draft.Executed = false
	recorded.Draft.CustomerPublish = false
	out.Draft = recorded.Draft
	out.Copy = recorded.Draft.Copy
	out.OutboundComplete = false
	out.MatrixButton = Button{Enabled: true, Reason: ReasonDraftRecorded}
	return out, nil
}

func (c *Consumer) decide(ctx context.Context, actor Actor) (Permission, error) {
	if c == nil || c.gate == nil {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	return c.gate.Decide(ctx, actor.PrincipalID, actor.TenantID, actor.BrandID)
}

type ackClient struct{ id string }

func (a ackClient) RequiresVideo() bool { return true }
func (a ackClient) LiveMatrix() bool    { return false }
func (a ackClient) PutDraft(matrixhandoff.Draft) (matrixhandoff.MatrixPlanRef, error) {
	return matrixhandoff.MatrixPlanRef{ID: a.id}, nil
}
func (a ackClient) RecordMerchant(matrixhandoff.MatrixPlanRef) error { return nil }
