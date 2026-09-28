// Package privatedomain decides which WeCom or community entries a guest may see.
//
// An entry appears only when the merchant configured it, the address is an
// official WeCom contact or group link, and the current channel can open that
// link. A click is click_wecom or click_community. It is not an add, a join,
// a new contact, a CRM import, a reward, or a coupon redemption. This
// deployment has no WeCom callback, so the guide stays disconnected.
package privatedomain

import (
	"net/url"
	"strings"
)

type Kind string

const (
	KindWecom     Kind = "wecom"
	KindCommunity Kind = "community"

	EventClickWecom     = "click_wecom"
	EventClickCommunity = "click_community"

	ReasonNotConfigured     = "not_configured"
	ReasonAddressNotWecom   = "address_not_wecom"
	ReasonChannelCannotOpen = "channel_cannot_open"
	RedemptionUnknown       = "unknown"
)

var knownKinds = []Kind{KindWecom, KindCommunity}

type Configured struct {
	Kind    Kind
	Enabled bool
	Href    string
}

type Shown struct {
	Kind  Kind
	Href  string
	Event string
}

type Closed struct {
	Kind      Kind
	Shown     bool
	Href      string
	Reason    string
	Connected bool
}

type Guide struct {
	Connected  bool
	Redemption string
}

type Click struct {
	Event               string
	Kind                Kind
	RecordedAs          string
	Success             bool
	PlatformResult      string
	Added               bool
	Joined              bool
	ContactCreated      bool
	LeadCreated         bool
	CRMImported         bool
	RewardTriggered     bool
	Connected           bool
	Redemption          string
	Followed            bool
	SilentAdd           bool
	ForcedJoin          bool
	BackgroundMarketing bool
}

// Present keeps merchant entries the current channel can open.
// Closed rows never carry the rejected address and never claim a connection.
func Present(channel string, items []Configured) ([]Shown, []Closed) {
	byKind := map[Kind][]Configured{}
	for _, item := range items {
		if known(item.Kind) {
			byKind[item.Kind] = append(byKind[item.Kind], item)
		}
	}
	var shown []Shown
	var closed []Closed
	for _, kind := range knownKinds {
		candidates := enabled(byKind[kind])
		if len(candidates) == 0 {
			closed = append(closed, Closed{Kind: kind, Reason: ReasonNotConfigured})
			continue
		}
		if !channelCanOpen(channel) {
			closed = append(closed, Closed{Kind: kind, Reason: ReasonChannelCannotOpen})
			continue
		}
		href, ok := firstOfficial(kind, candidates)
		if !ok {
			closed = append(closed, Closed{Kind: kind, Reason: ReasonAddressNotWecom})
			continue
		}
		shown = append(shown, Shown{Kind: kind, Href: href, Event: eventName(kind)})
	}
	return shown, closed
}

// Summarize never marks private domain connected and never invents a redemption.
func Summarize([]Shown) Guide {
	return Guide{Connected: false, Redemption: RedemptionUnknown}
}

// RecordClick records the explicit click as an unknown platform result.
func RecordClick(kind Kind) Click {
	return Click{
		Event:          eventName(kind),
		Kind:           kind,
		RecordedAs:     "click",
		PlatformResult: "unknown",
		Redemption:     RedemptionUnknown,
	}
}

func known(kind Kind) bool {
	for _, item := range knownKinds {
		if item == kind {
			return true
		}
	}
	return false
}

func enabled(items []Configured) []Configured {
	var out []Configured
	for _, item := range items {
		if item.Enabled {
			out = append(out, item)
		}
	}
	return out
}

func channelCanOpen(channel string) bool {
	switch strings.TrimSpace(channel) {
	case "web", "qr", "nfc":
		return true
	default:
		return false
	}
}

func firstOfficial(kind Kind, items []Configured) (string, bool) {
	for _, item := range items {
		href := strings.TrimSpace(item.Href)
		if official(kind, href) {
			return href, true
		}
	}
	return "", false
}

func official(kind Kind, href string) bool {
	parsed, err := url.Parse(href)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	if !strings.EqualFold(parsed.Hostname(), "work.weixin.qq.com") {
		return false
	}
	path := parsed.EscapedPath()
	switch kind {
	case KindWecom:
		return hasID(path, "/ca/")
	case KindCommunity:
		return hasID(path, "/gm/")
	default:
		return false
	}
}

func hasID(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	rest := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if rest == "" || strings.Contains(rest, "..") {
		return false
	}
	segment := strings.Split(rest, "/")[0]
	return segment != "" && segment != "."
}

func eventName(kind Kind) string {
	if kind == KindCommunity {
		return EventClickCommunity
	}
	if kind == KindWecom {
		return EventClickWecom
	}
	return ""
}
