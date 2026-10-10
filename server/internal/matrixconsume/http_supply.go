package matrixconsume

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

// httpPermission reads AG01's public permission API.
// An empty base URL is missing supply and does not open a connection.
type httpPermission struct {
	base   string
	client *http.Client
}

// NewHTTPPermission returns a permission gate. An empty base is missing supply.
func NewHTTPPermission(base string, client *http.Client) PermissionGate {
	return &httpPermission{base: strings.TrimRight(strings.TrimSpace(base), "/"), client: client}
}

func (h *httpPermission) Decide(ctx context.Context, principalID, tenantID, brandID string) (Permission, error) {
	if h == nil || h.base == "" {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	u, err := url.Parse(h.base + "/permissions")
	if err != nil {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	q := u.Query()
	q.Set("action", "brand_matrix_draft")
	q.Set("tenant_id", tenantID)
	q.Set("brand_id", brandID)
	q.Set("principal_id", principalID)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	res, err := h.http().Do(req)
	if err != nil {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	var parsed struct {
		Allowed bool   `json:"allowed"`
		Supply  string `json:"supply"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || parsed.Supply == "" {
		return Permission{Supply: SupplyMissing, Reason: ReasonPermissionSupply}, nil
	}
	return Permission{Allowed: parsed.Allowed, Supply: parsed.Supply, Reason: parsed.Reason}, nil
}

func (h *httpPermission) http() *http.Client {
	if h.client != nil {
		return h.client
	}
	return http.DefaultClient
}

// httpDraft posts a matrix draft. An empty base is missing supply.
type httpDraft struct {
	base   string
	client *http.Client
}

// NewHTTPDraftSink returns a draft sink. An empty base does not open a connection.
func NewHTTPDraftSink(base string, client *http.Client) DraftSink {
	return &httpDraft{base: strings.TrimRight(strings.TrimSpace(base), "/"), client: client}
}

func (h *httpDraft) Available() bool {
	return h != nil && h.base != ""
}

func (h *httpDraft) PutDraft(ctx context.Context, doc matrixhandoff.Draft) (DraftAck, error) {
	if !h.Available() {
		return DraftAck{}, errors.New("matrixconsume: matrix supply missing")
	}
	payload, err := json.Marshal(map[string]any{
		"activity_id": doc.Activity.ActivityID,
		"tenant_id":   doc.Activity.TenantID,
		"brand_id":    doc.Activity.BrandID,
		"copy":        doc.Copy,
	})
	if err != nil {
		return DraftAck{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base+"/drafts", bytes.NewReader(payload))
	if err != nil {
		return DraftAck{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := h.client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return DraftAck{}, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusCreated {
		return DraftAck{}, errors.New("matrixconsume: matrix supply missing")
	}
	var ack struct {
		PlanID   string `json:"plan_id"`
		Status   string `json:"status"`
		Executed bool   `json:"executed"`
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		return DraftAck{}, err
	}
	status := strings.ToLower(strings.TrimSpace(ack.Status))
	if status == "published" || status == "executed" {
		ack.Executed = true
	}
	return DraftAck{PlanID: ack.PlanID, Status: ack.Status, Executed: ack.Executed}, nil
}
