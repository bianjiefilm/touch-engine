// Package campaign holds the pure domain rules for 碰一碰 campaigns:
// the four-state lifecycle and the controlled locating short code.
//
// 红线(shortcode):码是服务端随机生成的不透明定位符,载荷里没有任何结构——
// 不嵌服务凭证、客户信息、租户/主体标识或任意跳转 URL。链接路由只做
// 短码→campaign 的受控映射。
package campaign

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Status is the campaign lifecycle state.
type Status string

const (
	StatusDraft  Status = "draft"
	StatusActive Status = "active"
	StatusPaused Status = "paused"
	StatusEnded  Status = "ended"
)

// Valid reports whether s is one of the four lifecycle states.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusActive, StatusPaused, StatusEnded:
		return true
	}
	return false
}

// ErrIllegalTransition is returned by Transition for a state change the
// lifecycle does not allow.
var ErrIllegalTransition = errors.New("campaign: illegal status transition")

// allowedTransitions:
//
//	draft  -> active(启用) | ended(废弃)
//	active -> paused(暂停) | ended(结束)
//	paused -> active(恢复) | ended(结束)
var allowedTransitions = map[Status][]Status{
	StatusDraft:  {StatusActive, StatusEnded},
	StatusActive: {StatusPaused, StatusEnded},
	StatusPaused: {StatusActive, StatusEnded},
	StatusEnded:  {},
}

// Transition validates a status change and returns the new status.
func Transition(from, to Status) (Status, error) {
	if !from.Valid() || !to.Valid() {
		return "", fmt.Errorf("%w: unknown status %q -> %q", ErrIllegalTransition, from, to)
	}
	for _, nxt := range allowedTransitions[from] {
		if nxt == to {
			return to, nil
		}
	}
	return "", fmt.Errorf("%w: %s -> %s", ErrIllegalTransition, from, to)
}

// Window is the campaign validity window. Empty bound = unbounded on that side.
type Window struct {
	StartsAt string // RFC3339 or ""
	EndsAt   string // RFC3339 or ""
}

// ResolveTime evaluates the window at now. It returns "" when now is inside
// the window, or a machine-readable reason when outside.
func (w Window) ResolveTime(now time.Time) string {
	if w.StartsAt != "" {
		if start, err := time.Parse(time.RFC3339, w.StartsAt); err == nil && now.Before(start) {
			return "not_started"
		}
	}
	if w.EndsAt != "" {
		if end, err := time.Parse(time.RFC3339, w.EndsAt); err == nil && now.After(end) {
			return "expired"
		}
	}
	return ""
}

// ---- short code -------------------------------------------------------------

// shortcodeAlphabet: Crockford base32 (no I/L/O/U) — URL-safe, unambiguous,
// and cannot carry separators, scheme characters, or query syntax.
const shortcodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// ShortcodeLen is the generated code length: 12 chars over a 32-symbol
// alphabet = 60 bits of entropy.
const ShortcodeLen = 12

// ErrShortcodeFormat marks a code that cannot exist in this system (wrong
// alphabet, wrong length). Lookup must treat it exactly like not-found.
var ErrShortcodeFormat = errors.New("campaign: shortcode format invalid")

// NewShortcode generates a fresh opaque locating code. The payload is random
// bits and nothing else: no tenant, principal, campaign id, URL, or credential
// is ever encoded. Each symbol is uniform over the 32-char alphabet (256%32==0,
// no modulo bias).
func NewShortcode() (string, error) {
	raw := make([]byte, ShortcodeLen)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("campaign: entropy unavailable: %w", err)
	}
	out := make([]byte, ShortcodeLen)
	for i, b := range raw {
		out[i] = shortcodeAlphabet[int(b)%len(shortcodeAlphabet)]
	}
	return string(out), nil
}

// ValidShortcode reports whether code could have been produced by this system.
// It is a pure format gate (alphabet + length): any structured payload
// (separators, scheme characters, '=' padding, base64 '+/', ':', '.') fails it.
func ValidShortcode(code string) bool {
	if len(code) != ShortcodeLen {
		return false
	}
	for _, r := range code {
		if !strings.ContainsRune(shortcodeAlphabet, r) {
			return false
		}
	}
	return true
}
