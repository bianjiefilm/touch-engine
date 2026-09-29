package copyjob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/copydraft"
)

const (
	taskTokenHeader = "X-PilotSeaview-Internal-Token"
	appIDHeader     = "X-App-ID"
)

// ErrNotConfigured means a real model/task/billing call cannot be made.
var ErrNotConfigured = errors.New("copyjob: model transport is not configured")

// ErrUnpriced means billing did not return a positive quote. Nothing was submitted.
var ErrUnpriced = errors.New("copyjob: billing quote is not priced")

// ErrTimeout means the provider did not finish before the deadline.
var ErrTimeout = errors.New("copyjob: model timed out")

// ErrQuota means the provider or billing refused for quota or balance.
var ErrQuota = errors.New("copyjob: provider quota or balance refused")

// ErrTransport means the provider answered with an error and no copy.
var ErrTransport = errors.New("copyjob: model transport failed")

// TextModel is the only path that can turn a quote into model text.
type TextModel interface {
	Ready() bool
	Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error)
}

// GenerateRequest is one idempotent model attempt. It carries no project id.
type GenerateRequest struct {
	IdempotencyKey string
	PayerAccountID string
	SnapshotHash   string
	Prompt         string
}

// GenerateResult is a provider result. Submitted is true only after a task
// create was accepted. Output may still be empty.
type GenerateResult struct {
	Output       copydraft.ModelOutput
	ModelID      string
	ModelVersion string
	TaskID       string
	HoldID       string
	AmountMinor  int64
	Priced       bool
	Submitted    bool
}

// PlatformClient calls public task and billing. Missing credentials fail closed.
type PlatformClient struct {
	Enabled        bool
	TaskBaseURL    string
	TaskToken      string
	BillingBaseURL string
	BillingToken   string
	AppID          string
	Provider       string
	ModelID        string
	ModelVersion   string
	PricingVersion string
	HTTP           *http.Client
}

// NewPlatformClient uses process config. Ready is false unless task, billing,
// and the model id are all set. This does not invent a price.
func NewPlatformClient(cfg config.Config) *PlatformClient {
	return &PlatformClient{
		Enabled:        cfg.FeatureTask,
		TaskBaseURL:    strings.TrimRight(strings.TrimSpace(cfg.TaskBaseURL), "/"),
		TaskToken:      cfg.TaskToken,
		BillingBaseURL: strings.TrimRight(strings.TrimSpace(cfg.BillingBaseURL), "/"),
		BillingToken:   cfg.BillingToken,
		AppID:          strings.TrimSpace(cfg.AppID),
		Provider:       strings.TrimSpace(cfg.CopyModelProvider),
		ModelID:        strings.TrimSpace(cfg.CopyModelID),
		ModelVersion:   strings.TrimSpace(cfg.CopyModelVersion),
		PricingVersion: strings.TrimSpace(cfg.CopyPricingVersion),
	}
}

// Ready reports whether a real call is possible.
func (c *PlatformClient) Ready() bool {
	if c == nil {
		return false
	}
	return c.Enabled && c.TaskBaseURL != "" && c.TaskToken != "" && c.BillingBaseURL != "" && c.BillingToken != "" &&
		c.AppID != "" && c.Provider != "" && c.ModelID != "" && c.ModelVersion != "" && c.PricingVersion != ""
}

type taskBody struct {
	AppID          string          `json:"app_id"`
	AccountID      string          `json:"account_id"`
	IdempotencyKey string          `json:"idempotency_key"`
	Capability     string          `json:"capability"`
	Provider       string          `json:"provider"`
	Params         json.RawMessage `json:"params"`
	Billing        taskBilling     `json:"billing"`
}

type taskBilling struct {
	Mode      string  `json:"mode"`
	AmountCNY float64 `json:"amount_cny"`
	Reason    string  `json:"reason"`
}

// Generate quotes billing, then submits one task without a project id.
// A quote failure returns before the task post.
func (c *PlatformClient) Generate(ctx context.Context, req GenerateRequest) (GenerateResult, error) {
	if !c.Ready() {
		return GenerateResult{}, ErrNotConfigured
	}
	usageID, err := c.ingest(ctx, req)
	if err != nil {
		return GenerateResult{}, err
	}
	amount, err := c.quote(ctx, usageID)
	if err != nil {
		return GenerateResult{}, err
	}
	if amount <= 0 {
		return GenerateResult{}, ErrUnpriced
	}
	return c.submit(ctx, req, amount)
}

func (c *PlatformClient) ingest(ctx context.Context, req GenerateRequest) (string, error) {
	var out struct {
		Fact struct {
			UsageID string `json:"usage_id"`
		} `json:"fact"`
	}
	err := c.post(ctx, c.BillingBaseURL+"/internal/v1/billing/unified/usage", c.BillingToken, map[string]any{
		"app_id": c.AppID, "capability": Capability, "quantity": 1,
		"pricing_version": c.PricingVersion, "business_ref": req.SnapshotHash,
		"payer_account_id": req.PayerAccountID, "idempotency_key": req.IdempotencyKey,
	}, &out)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Fact.UsageID) == "" {
		return "", ErrTransport
	}
	return out.Fact.UsageID, nil
}

func (c *PlatformClient) quote(ctx context.Context, usageID string) (int64, error) {
	var out struct {
		Quote struct {
			AmountMinor int64 `json:"amount_minor"`
		} `json:"quote"`
	}
	if err := c.post(ctx, c.BillingBaseURL+"/internal/v1/billing/unified/quote", c.BillingToken, map[string]any{"usage_id": usageID}, &out); err != nil {
		return 0, err
	}
	return out.Quote.AmountMinor, nil
}

func (c *PlatformClient) submit(ctx context.Context, req GenerateRequest, amountMinor int64) (GenerateResult, error) {
	params, err := json.Marshal(map[string]string{
		"kind": "touch_ai_copy", "snapshot_hash": req.SnapshotHash, "prompt": req.Prompt,
		"model_id": c.ModelID, "model_version": c.ModelVersion,
	})
	if err != nil {
		return GenerateResult{}, err
	}
	body := taskBody{
		AppID: c.AppID, AccountID: req.PayerAccountID, IdempotencyKey: req.IdempotencyKey,
		Capability: Capability, Provider: c.Provider, Params: params,
		Billing: taskBilling{Mode: "hold", AmountCNY: float64(amountMinor) / 100, Reason: "fee_kind=platform_ai_fee; " + Capability},
	}
	var summary struct {
		TaskID     string `json:"task_id"`
		Status     string `json:"status"`
		HoldID     string `json:"hold_id"`
		ResultJSON string `json:"result_json"`
		ErrorCode  string `json:"error_code"`
	}
	if err := c.post(ctx, c.TaskBaseURL+"/internal/v1/tasks", c.TaskToken, body, &summary); err != nil {
		return GenerateResult{AmountMinor: amountMinor, Priced: true}, err
	}
	result := GenerateResult{
		ModelID: c.ModelID, ModelVersion: c.ModelVersion, TaskID: summary.TaskID,
		HoldID: summary.HoldID, AmountMinor: amountMinor, Priced: true, Submitted: true,
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for {
		if summary.Status == "SUCCEEDED" || summary.Status == "FAILED" || summary.Status == "CANCELED" {
			break
		}
		select {
		case <-ctx.Done():
			return result, ErrTimeout
		case <-deadline.C:
			return result, ErrTimeout
		case <-time.After(50 * time.Millisecond):
		}
		if err := c.get(ctx, summary.TaskID, &summary); err != nil {
			return result, err
		}
		result.HoldID = summary.HoldID
		result.TaskID = summary.TaskID
	}
	if summary.Status != "SUCCEEDED" {
		return result, ErrTransport
	}
	result.Output = parseModelOutput(summary.ResultJSON)
	return result, nil
}

func parseModelOutput(raw string) copydraft.ModelOutput {
	var out struct {
		Title  string   `json:"title"`
		Intro  string   `json:"intro"`
		Topics []string `json:"topics"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return copydraft.ModelOutput{}
	}
	return copydraft.ModelOutput{Title: out.Title, Intro: out.Intro, Topics: out.Topics}
}

func (c *PlatformClient) post(ctx context.Context, url, token string, payload any, dest any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(taskTokenHeader, token)
	req.Header.Set(appIDHeader, c.AppID)
	return c.do(req, dest)
}

func (c *PlatformClient) get(ctx context.Context, taskID string, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.TaskBaseURL+"/internal/v1/tasks/"+taskID+"?app_id="+c.AppID, nil)
	if err != nil {
		return err
	}
	req.Header.Set(taskTokenHeader, c.TaskToken)
	req.Header.Set(appIDHeader, c.AppID)
	return c.do(req, dest)
}

func (c *PlatformClient) do(req *http.Request, dest any) error {
	res, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrTransport, err.Error())
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusPaymentRequired {
		return ErrQuota
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("%w: http %d", ErrTransport, res.StatusCode)
	}
	if dest == nil {
		return nil
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return ErrTransport
	}
	return nil
}

func (c *PlatformClient) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}
