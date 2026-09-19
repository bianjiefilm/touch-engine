// Package upload is the platform-upload client used to validate material
// references (素材引用). T0 stores only asset_id + version REFERENCES; files
// are never copied into this app.
//
// 契约对齐 public-ai docs/superpowers/specs/2026-09-05-platform-upload-design.md:
// GET /internal/v1/upload/assets/{asset_id} 返回 thin metadata(asset_id、app_id、
// status、sha256 等),头 X-PilotSeaView-Internal-Token + X-App-ID。
// 版本锚点 = 平台返回的 sha256(ADR-0001 D3:经授权的版本快照定位,
// source_revision = 内容 SHA-256);调用方给的 version 不一致即拒绝。
package upload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const internalTokenHeader = "X-PilotSeaView-Internal-Token"
const appIDHeader = "X-App-ID"

// Sentinel errors (fail-closed classification).
var (
	// ErrNotFound means upload answered that the asset does not exist (or was
	// deleted/quarantined beyond read). Callers must surface it, never retry.
	ErrNotFound = errors.New("upload: asset not found")
	// ErrUnauthorized means upload rejected this app's credentials.
	ErrUnauthorized = errors.New("upload: unauthorized")
	// ErrForbidden means the asset exists but this app/principal may not read it.
	ErrForbidden = errors.New("upload: forbidden")
	// ErrNotReady means the asset exists but is not in a distributable state.
	ErrNotReady = errors.New("upload: asset not ready")
	// ErrVersionMismatch means the caller-supplied version is not the asset's
	// anchored sha256.
	ErrVersionMismatch = errors.New("upload: version mismatch")
)

// AssetMeta is the thin metadata platform-upload returns.
type AssetMeta struct {
	AssetID     string `json:"asset_id"`
	AppID       string `json:"app_id"`
	Status      string `json:"status"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
}

// Client calls platform-upload internal endpoints.
type Client struct {
	BaseURL string
	Token   string
	AppID   string
	HTTP    *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// GetAsset fetches thin metadata for assetID.
func (c *Client) GetAsset(ctx context.Context, assetID string) (AssetMeta, error) {
	var out AssetMeta
	url := strings.TrimRight(c.BaseURL, "/") + "/internal/v1/upload/assets/" + assetID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return out, fmt.Errorf("upload asset: %w", err)
	}
	req.Header.Set(internalTokenHeader, c.Token)
	req.Header.Set(appIDHeader, c.AppID)
	res, err := c.httpClient().Do(req)
	if err != nil {
		return out, fmt.Errorf("upload asset %s: %w", assetID, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch res.StatusCode {
	case http.StatusOK:
		if err := json.Unmarshal(b, &out); err != nil {
			return out, fmt.Errorf("upload asset %s: decode: %w", assetID, err)
		}
		return out, nil
	case http.StatusNotFound, http.StatusGone, http.StatusLocked:
		// deleted / expired / quarantined assets are all "cannot reference"
		return out, ErrNotFound
	case http.StatusUnauthorized:
		return out, ErrUnauthorized
	case http.StatusForbidden:
		return out, ErrForbidden
	default:
		return out, fmt.Errorf("upload asset %s: status %d", assetID, res.StatusCode)
	}
}

// ValidateRef resolves assetID and enforces the reference contract:
//   - asset must exist and be `ready`;
//   - if wantVersion is non-empty it must equal the anchored sha256;
//   - the returned version (the reference we store) IS the anchored sha256.
func (c *Client) ValidateRef(ctx context.Context, assetID, wantVersion string) (AssetMeta, error) {
	meta, err := c.GetAsset(ctx, assetID)
	if err != nil {
		return meta, err
	}
	if !strings.EqualFold(strings.TrimSpace(meta.Status), "ready") {
		return meta, ErrNotReady
	}
	if strings.TrimSpace(meta.SHA256) == "" {
		return meta, fmt.Errorf("upload asset %s: metadata without sha256 (cannot anchor a version)", assetID)
	}
	if wantVersion != "" && !strings.EqualFold(wantVersion, meta.SHA256) {
		return meta, ErrVersionMismatch
	}
	return meta, nil
}
