// Package extrajump decides which activity-page jumps a guest may see.
//
// A jump appears only when the merchant configured it and the address is a
// public https URL. A click is a click: it is not an add, a follow, a lead,
// a CRM import, a reward, or a confirmed publish.
package extrajump

import (
	"net"
	"net/url"
	"strings"
)

type Kind string

const (
	KindWifi     Kind = "wifi"
	KindNavigate Kind = "navigate"
	KindReview   Kind = "review"
	KindWecom    Kind = "wecom"
	KindFollow   Kind = "follow"

	ReasonNotConfigured      = "not_configured"
	ReasonAddressNotOfficial = "address_not_official"
)

var knownKinds = []Kind{KindWifi, KindNavigate, KindReview, KindWecom, KindFollow}

type Configured struct {
	Kind    Kind
	Enabled bool
	Href    string
}

type Shown struct {
	Kind      Kind
	Href      string
	Available bool
	Result    string
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
}

func known(kind Kind) bool {
	for _, item := range knownKinds {
		if item == kind {
			return true
		}
	}
	return false
}

// Present keeps configured public https targets and closes everything else.
// Closed rows never carry the rejected address.
func Present(items []Configured) ([]Shown, []Closed) {
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
		if !OfficialAddress(item.Href) {
			closed = append(closed, Closed{Kind: kind, Reason: ReasonAddressNotOfficial})
			continue
		}
		shown = append(shown, Shown{
			Kind: kind, Href: strings.TrimSpace(item.Href), Available: true, Result: "ready",
		})
	}
	return shown, closed
}

// RecordClick records the jump as an unknown platform result.
func RecordClick(kind Kind) Click {
	return Click{Kind: kind, RecordedAs: "click", PlatformResult: "unknown"}
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
