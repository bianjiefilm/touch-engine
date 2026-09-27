// Package brandctx is a read-only client of public-ai Brand Registry
// GET /internal/v1/brand/context (HUI-2049).
//
// The response is a display and admit contract: name, logo, theme, support,
// and whether this host may serve login or public pages. It is not a campaign
// directory. This client does not create brands, does not send X-App-ID, and
// does not claim that live touch activities are connected on the far side.
package brandctx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	serviceID       = "brand-reader"
	tokenHeader     = "X-PilotSeaview-Internal-Token"
	serviceHeader   = "X-Service-ID"
	maxStale        = 30 * time.Second
	touchCapability = "touch-engine"
)

// Kind classifies a registry answer. Callers must not collapse these into one
// blank page.
type Kind string

const (
	KindReady          Kind = "ready"
	KindUnknown        Kind = "unknown_brand"
	KindSuspended      Kind = "brand_suspended"
	KindRetired        Kind = "brand_retiring"
	KindDomain         Kind = "domain_error"
	KindUnavailable    Kind = "brand_unavailable"
	KindInvalid        Kind = "invalid_host"
)

// App is one registry app row. Touch only acts on its own app id.
type App struct {
	AppID         string `json:"app_id"`
	DisplayName   string `json:"display_name,omitempty"`
	PublicVisible bool   `json:"public_visible"`
	Availability  string `json:"availability,omitempty"`
}

// Manifest is the display shell. It has no tenant, payer, or ACL.
type Manifest struct {
	BrandID              string            `json:"brand_id"`
	Kind                 string            `json:"kind,omitempty"`
	Status               string            `json:"status"`
	ConfigVersion        int64             `json:"config_version"`
	ETag                 string            `json:"etag,omitempty"`
	DisplayName          string            `json:"display_name,omitempty"`
	LogoRef              string            `json:"logo_ref,omitempty"`
	Theme                map[string]string `json:"theme,omitempty"`
	SupportName          string            `json:"support_name,omitempty"`
	SupportContact       string            `json:"support_contact,omitempty"`
	AuthHeadline         string            `json:"auth_headline,omitempty"`
	NotificationBrandRef string            `json:"notification_brand_ref,omitempty"`
	AdmitLogin           bool              `json:"admit_login"`
	AdmitPublic          bool              `json:"admit_public"`
	SafePage             string            `json:"safe_page,omitempty"`
	Apps                 []App             `json:"apps"`
	LastKnownGood        bool              `json:"last_known_good,omitempty"`
}

// TouchEnabled reports whether this brand currently shows touch-engine.
// Other apps are ignored on purpose: touch must not recommend them.
func (m Manifest) TouchEnabled(appID string) bool {
	if appID == "" {
		appID = touchCapability
	}
	if appID != touchCapability {
		return false
	}
	for _, a := range m.Apps {
		if a.AppID == appID && a.PublicVisible && a.Availability != "hidden" {
			return true
		}
	}
	return false
}

// Result is one read. Manifest may be present on suspended brands so the
// page can name the state; it is never a permission to enter a tenant.
type Result struct {
	Kind     Kind
	Manifest Manifest
	Stale    bool
}

// Client calls the registry. Host is set on the outbound request itself so a
// client X-Forwarded-Host cannot retarget the lookup.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at       time.Time
	manifest Manifest
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 10 * time.Second}
}

// Read loads the brand for host. allowStale returns a cached manifest for at
// most 30s when the registry cannot be reached. Fresh sensitive operations
// must pass allowStale=false.
func (c *Client) Read(ctx context.Context, host string, allowStale bool) (Result, error) {
	if c == nil || strings.TrimSpace(c.BaseURL) == "" || strings.TrimSpace(c.Token) == "" {
		return Result{Kind: KindUnavailable}, errors.New("brandctx: client not configured")
	}
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return Result{Kind: KindInvalid}, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/internal/v1/brand/context", nil)
	if err != nil {
		return Result{}, err
	}
	req.Host = host
	req.Header.Set(serviceHeader, serviceID)
	req.Header.Set(tokenHeader, c.Token)
	// X-App-ID is forbidden by the registry and would also be the wrong
	// authority. Service identity only.
	res, err := c.httpClient().Do(req)
	if err != nil {
		if allowStale {
			if m, ok := c.fresh(host); ok {
				m.LastKnownGood = true
				return Result{Kind: KindReady, Manifest: m, Stale: true}, nil
			}
		}
		return Result{Kind: KindUnavailable}, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	var m Manifest
	_ = json.Unmarshal(body, &m)
	switch res.StatusCode {
	case http.StatusOK:
		if m.Status == "retired" {
			return Result{Kind: KindRetired, Manifest: m}, nil
		}
		c.store(host, m)
		return Result{Kind: KindReady, Manifest: m}, nil
	case http.StatusNotFound:
		return Result{Kind: KindUnknown, Manifest: m}, nil
	case http.StatusForbidden:
		if m.Status == "suspended" || m.SafePage == "brand_unavailable" {
			return Result{Kind: KindSuspended, Manifest: m}, nil
		}
		if m.Status == "retired" {
			return Result{Kind: KindRetired, Manifest: m}, nil
		}
		return Result{Kind: KindDomain, Manifest: m}, nil
	default:
		if allowStale {
			if cachedM, ok := c.fresh(host); ok {
				cachedM.LastKnownGood = true
				return Result{Kind: KindReady, Manifest: cachedM, Stale: true}, nil
			}
		}
		return Result{Kind: KindUnavailable, Manifest: m}, fmt.Errorf("brandctx: status %d", res.StatusCode)
	}
}

func (c *Client) store(host string, m Manifest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		c.cache = map[string]cached{}
	}
	c.cache[host] = cached{at: time.Now(), manifest: m}
}

func (c *Client) fresh(host string) (Manifest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.cache[host]
	if !ok || time.Since(item.at) > maxStale {
		return Manifest{}, false
	}
	return item.manifest, true
}
