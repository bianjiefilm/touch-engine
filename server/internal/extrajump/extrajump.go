// Package extrajump decides which activity-page jumps a guest may see.
//
// A jump appears only when the merchant configured it and the address is a
// public https URL. A click is a click: it is not an add, a follow, a lead,
// a CRM import, a reward, or a confirmed publish. Opening that page is not
// the same fact as launching a native client or completing a platform action.
package extrajump

import (
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Kind string

const (
	KindWifi     Kind = "wifi"
	KindNavigate Kind = "navigate"
	KindReview   Kind = "review"
	KindWecom    Kind = "wecom"
	KindFollow   Kind = "follow"

	ClassStore    = "store"
	ClassActivity = "activity"
	ClassReturn   = "return"

	ReasonNotConfigured      = "not_configured"
	ReasonAddressNotOfficial = "address_not_official"
	ReasonExpired            = "expired"
	ReasonRevoked            = "revoked"
	ReasonUnauthorized       = "unauthorized"
	ReasonCrossBrand         = "cross_brand"

	EvidenceWebConfigured    = "configured"
	EvidenceWebClosed        = "closed"
	EvidenceClientUnverified = "unverified"
	EvidencePlatformUnknown  = "unknown"
)

var knownKinds = []Kind{KindWifi, KindNavigate, KindReview, KindWecom, KindFollow}

var inSiteReturnPattern = regexp.MustCompile(`^/[A-Za-z0-9/_-]*$`)

type Configured struct {
	Kind      Kind
	Enabled   bool
	Href      string
	Revoked   bool
	ExpiresAt string
}

type Shown struct {
	Kind           Kind
	Href           string
	Available      bool
	Result         string
	Class          string
	Web            string
	ClientLaunch   string
	PlatformAction string
}

type Closed struct {
	Kind   Kind
	Shown  bool
	Href   string
	Reason string
}

type Click struct {
	Kind            Kind
	RecordedAs      string
	Success         bool
	PlatformResult  string
	Added           bool
	Followed        bool
	LeadCreated     bool
	CRMImported     bool
	RewardTriggered bool
	PublishSuccess  bool
	Class           string
	Href            string
	Web             string
	ClientLaunch    string
	PlatformAction  string
	AutoFollow      bool
	AutoJoin        bool
	AutoPay         bool
}

type ReturnConfig struct {
	Href      string
	Enabled   bool
	Revoked   bool
	ExpiresAt string
}

type ReturnView struct {
	Shown          bool
	Href           string
	Class          string
	Reason         string
	Web            string
	ClientLaunch   string
	PlatformAction string
}

type SupportRow struct {
	Kind           string
	Class          string
	Label          string
	Web            string
	ClientLaunch   string
	PlatformAction string
	NativeReason   string
	AutoFollow     bool
	AutoJoin       bool
	AutoPay        bool
}

func known(kind Kind) bool {
	for _, item := range knownKinds {
		if item == kind {
			return true
		}
	}
	return false
}

func ClassOf(kind Kind) string {
	switch kind {
	case KindWifi, KindNavigate, KindReview:
		return ClassStore
	case KindFollow, KindWecom:
		return ClassActivity
	default:
		return ""
	}
}

// Present keeps configured public https targets and closes everything else.
// Closed rows never carry the rejected address.
func Present(items []Configured) ([]Shown, []Closed) {
	return PresentAt(items, time.Now().UTC())
}

// PresentAt is Present at a fixed instant. Revoked and expired rows close
// before the address check, and still do not echo the address.
func PresentAt(items []Configured, now time.Time) ([]Shown, []Closed) {
	byKind := map[Kind]Configured{}
	for _, item := range items {
		if known(item.Kind) {
			byKind[item.Kind] = item
		}
	}
	var shown []Shown
	var closed []Closed
	for _, kind := range knownKinds {
		item, ok := byKind[kind]
		if !ok || !item.Enabled {
			closed = append(closed, Closed{Kind: kind, Reason: ReasonNotConfigured})
			continue
		}
		if item.Revoked {
			closed = append(closed, Closed{Kind: kind, Reason: ReasonRevoked})
			continue
		}
		if expired(item.ExpiresAt, now) {
			closed = append(closed, Closed{Kind: kind, Reason: ReasonExpired})
			continue
		}
		if !OfficialAddress(item.Href) {
			closed = append(closed, Closed{Kind: kind, Reason: ReasonAddressNotOfficial})
			continue
		}
		shown = append(shown, Shown{
			Kind: kind, Href: strings.TrimSpace(item.Href), Available: true, Result: "ready",
			Class: ClassOf(kind), Web: EvidenceWebConfigured,
			ClientLaunch: EvidenceClientUnverified, PlatformAction: EvidencePlatformUnknown,
		})
	}
	return shown, closed
}

// RecordClick records the jump as an unknown platform result. The client
// launch stays unverified because this process cannot see a phone.
func RecordClick(kind Kind) Click {
	return Click{
		Kind: kind, RecordedAs: "click", PlatformResult: "unknown",
		Class: ClassOf(kind), Web: EvidenceWebConfigured,
		ClientLaunch: EvidenceClientUnverified, PlatformAction: EvidencePlatformUnknown,
	}
}

// CanonicalHref returns the stored shown address. The supplied value is the
// caller's suggestion and is never used.
func CanonicalHref(shown []Shown, kind Kind, supplied string) (string, bool) {
	_ = supplied
	for _, item := range shown {
		if item.Kind == kind && item.Available && item.Href != "" {
			return item.Href, true
		}
	}
	return "", false
}

// AllowReturn accepts an in-site path or an https URL whose host was
// registered for this campaign. Any other external URL is rejected.
func AllowReturn(href string, hosts []string) bool {
	raw := strings.TrimSpace(href)
	if inSiteReturn(raw) {
		return true
	}
	if !OfficialAddress(raw) {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, allowed := range hosts {
		if host == strings.ToLower(strings.TrimSpace(allowed)) && host != "" {
			return true
		}
	}
	return false
}

func inSiteReturn(raw string) bool {
	if raw == "" || strings.Contains(raw, `\`) || strings.Contains(raw, "//") || strings.Contains(raw, "?") || strings.Contains(raw, "#") || strings.Contains(raw, "%") {
		return false
	}
	if !inSiteReturnPattern.MatchString(raw) {
		return false
	}
	lower := strings.ToLower(raw)
	if lower == "/admin" || strings.HasPrefix(lower, "/admin/") || lower == "/api" || strings.HasPrefix(lower, "/api/") {
		return false
	}
	return true
}

// DecideReturn shows only a still-authorized registered return. Closed
// decisions do not carry the address.
func DecideReturn(cfg ReturnConfig, now time.Time, hosts []string) ReturnView {
	view := ReturnView{
		Class: ClassReturn, Web: EvidenceWebClosed,
		ClientLaunch: EvidenceClientUnverified, PlatformAction: EvidencePlatformUnknown,
	}
	if !cfg.Enabled || strings.TrimSpace(cfg.Href) == "" {
		view.Reason = ReasonNotConfigured
		return view
	}
	if cfg.Revoked {
		view.Reason = ReasonRevoked
		return view
	}
	if expired(cfg.ExpiresAt, now) {
		view.Reason = ReasonExpired
		return view
	}
	if !AllowReturn(cfg.Href, hosts) {
		view.Reason = ReasonAddressNotOfficial
		return view
	}
	view.Shown = true
	view.Href = strings.TrimSpace(cfg.Href)
	view.Web = EvidenceWebConfigured
	return view
}

// SupportMatrix is the static capability table. Native launch is unverified
// on this machine. None of these rows follow, join, or charge by themselves.
func SupportMatrix() []SupportRow {
	return []SupportRow{
		{Kind: "wifi", Class: ClassStore, Label: "门店 WiFi", Web: "page_only", ClientLaunch: EvidenceClientUnverified, PlatformAction: EvidencePlatformUnknown, NativeReason: "不能代连 WiFi，原生唤起未验证"},
		{Kind: "navigate", Class: ClassStore, Label: "导航", Web: "page_only", ClientLaunch: EvidenceClientUnverified, PlatformAction: EvidencePlatformUnknown, NativeReason: "不能代开地图客户端，原生唤起未验证"},
		{Kind: "review", Class: ClassStore, Label: "写点评", Web: "page_only", ClientLaunch: EvidenceClientUnverified, PlatformAction: EvidencePlatformUnknown, NativeReason: "不能代开点评客户端，原生唤起未验证"},
		{Kind: "follow", Class: ClassActivity, Label: "关注账号", Web: "page_only", ClientLaunch: EvidenceClientUnverified, PlatformAction: EvidencePlatformUnknown, NativeReason: "不能代开关注客户端，原生唤起未验证"},
		{Kind: "wecom", Class: ClassActivity, Label: "加企微", Web: "page_only", ClientLaunch: EvidenceClientUnverified, PlatformAction: EvidencePlatformUnknown, NativeReason: "不能代开企微，原生唤起未验证"},
		{Kind: "return", Class: ClassReturn, Label: "返回", Web: "page_only", ClientLaunch: EvidenceClientUnverified, PlatformAction: EvidencePlatformUnknown, NativeReason: "返回只走已登记地址，原生唤起未验证"},
	}
}

// BlockReason closes public jumps when the brand decision applies and the
// activity is not available on this host. Brand off does not invent a block.
func BlockReason(applied bool, state string) (string, bool) {
	if !applied {
		return "", false
	}
	switch state {
	case "domain_mismatch", "unknown_brand":
		return ReasonCrossBrand, true
	case "available":
		return "", false
	default:
		return ReasonUnauthorized, true
	}
}

// OfficialAddress accepts only public https URLs. Placeholder, loopback,
// link-local names, and private networks are not official targets.
func OfficialAddress(href string) bool {
	parsed, err := url.Parse(strings.TrimSpace(href))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return false
	}
	if strings.HasSuffix(host, ".invalid") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return false
		}
	}
	return true
}

func expired(raw string, now time.Time) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			return true
		}
	}
	return !t.After(now)
}

// Usable reports whether a stored row may still be offered. Revoked and
// expired rows are not.
func Usable(item Configured, now time.Time) bool {
	return item.Enabled && !item.Revoked && !expired(item.ExpiresAt, now) && OfficialAddress(item.Href)
}
