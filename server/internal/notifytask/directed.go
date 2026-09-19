// directed.go upgrades the T0 notify scaffold into the real directed-event
// delivery client (HUI-1747 / contract HUI-1734).
//
// 契约对齐 public-ai docs/integration-guide.md §5 与 HUI-1734 spec:
//   - 发布:POST {base}/internal/v1/notify/events,头 X-PilotSeaView-Internal-Token
//     + X-Notify-App-ID;notify 持久接受(2xx)即确认源(此后投递状态查 deliveries)。
//   - 查询:GET {base}/internal/v1/notify/events/{id}/deliveries,零副作用。
//   - 同键重复发布幂等(duplicate 也算接受);同键异内容由 notify 以 409 拒绝。
//
// Fail-closed 沿用 T0:Scaffold.Check() 为开关与配置的唯一闸门;HTTP 层错误
// 一律显式返回,不伪造接受。
package notifytask

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
)

const (
	internalTokenHeader = "X-PilotSeaView-Internal-Token"
	notifyAppIDHeader   = "X-Notify-App-ID"
)

// DirectedPoster is the transport surface the forwarder depends on (interface
// keeps the leads package testable without network).
type DirectedPoster interface {
	PostEvent(ctx context.Context, payload []byte) error
	// Deliveries returns the per-route delivery statuses notify currently
	// reports for one event (empty = not yet attempted).
	Deliveries(ctx context.Context, eventID string) ([]DeliveryStatus, error)
}

// DeliveryStatus mirrors GET /internal/v1/notify/events/{id}/deliveries rows
// (delivery_id/status/attempt_count/next_attempt_at/last_error — no business
// result; business result lives on the target side per the contract).
type DeliveryStatus struct {
	DeliveryID string `json:"delivery_id"`
	Status     string `json:"status"` // pending | in_flight | delivered | dead | subscription_disabled
	Attempts   int    `json:"attempt_count"`
	LastError  string `json:"last_error"`
}

// DirectedClient posts directed facts to platform-notify over real HTTP.
type DirectedClient struct {
	Scaffold *Scaffold
	HTTP     *http.Client
}

// NewDirectedClient wires the client onto the notify scaffold.
func NewDirectedClient(sc *Scaffold, httpClient *http.Client) *DirectedClient {
	return &DirectedClient{Scaffold: sc, HTTP: httpClient}
}

func (c *DirectedClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// PostEvent publishes one directed fact. Any 2xx is acceptance (including the
// duplicate branch); 409 is a same-key-different-content conflict; everything
// else is an explicit error.
func (c *DirectedClient) PostEvent(ctx context.Context, payload []byte) error {
	if err := c.Scaffold.Check(); err != nil {
		return err
	}
	url := strings.TrimRight(c.Scaffold.BaseURL, "/") + "/internal/v1/notify/events"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("notifytask: build publish request: %w", err)
	}
	req.Header.Set(internalTokenHeader, c.Scaffold.Token)
	req.Header.Set(notifyAppIDHeader, c.Scaffold.AppID)
	req.Header.Set("Content-Type", "application/json")

	res, err := c.httpClient().Do(req)
	if err != nil {
		return fmt.Errorf("notifytask: publish transport: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return nil
	case res.StatusCode == http.StatusConflict:
		return fmt.Errorf("notifytask: publish conflict (same key, different content): %s", truncate(body))
	default:
		return fmt.Errorf("notifytask: publish refused with status %d: %s", res.StatusCode, truncate(body))
	}
}

// Deliveries queries the delivery rows of one event. Zero side effects per
// contract; transport errors are explicit.
func (c *DirectedClient) Deliveries(ctx context.Context, eventID string) ([]DeliveryStatus, error) {
	if err := c.Scaffold.Check(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(eventID) == "" {
		return nil, errors.New("notifytask: deliveries query requires an event id")
	}
	url := strings.TrimRight(c.Scaffold.BaseURL, "/") + "/internal/v1/notify/events/" +
		strings.TrimPrefix(eventID, "/") + "/deliveries"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("notifytask: build deliveries request: %w", err)
	}
	req.Header.Set(internalTokenHeader, c.Scaffold.Token)
	req.Header.Set(notifyAppIDHeader, c.Scaffold.AppID)

	res, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("notifytask: deliveries transport: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
		return nil, fmt.Errorf("notifytask: deliveries refused with status %d: %s", res.StatusCode, truncate(body))
	}
	var out struct {
		Items []DeliveryStatus `json:"items"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("notifytask: deliveries decode: %w", err)
	}
	return out.Items, nil
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
