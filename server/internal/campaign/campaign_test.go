package campaign

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTransitionTable(t *testing.T) {
	ok := map[Status][]Status{
		StatusDraft:  {StatusActive, StatusEnded},
		StatusActive: {StatusPaused, StatusEnded},
		StatusPaused: {StatusActive, StatusEnded},
	}
	for from, tos := range ok {
		for _, to := range tos {
			if _, err := Transition(from, to); err != nil {
				t.Fatalf("%s -> %s: unexpected error %v", from, to, err)
			}
		}
	}
	bad := [][2]Status{
		{StatusDraft, StatusPaused},  // 未启用不能直接暂停
		{StatusPaused, StatusDraft},  // 不可回草稿
		{StatusActive, StatusDraft},  // 不可回草稿
		{StatusEnded, StatusActive},  // 终态不可复活
		{StatusEnded, StatusPaused},  // 终态不可暂停
		{StatusEnded, StatusDraft},   // 终态不可回草稿
		{StatusActive, StatusActive}, // 自反非法(幂等走 store 层显式路径)
		{Status("junk"), StatusActive},
	}
	for _, tc := range bad {
		if _, err := Transition(tc[0], tc[1]); !errors.Is(err, ErrIllegalTransition) {
			t.Fatalf("%s -> %s: want ErrIllegalTransition, got %v", tc[0], tc[1], err)
		}
	}
}

func TestWindowResolve(t *testing.T) {
	now := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name       string
		start, end string
		want       string
	}{
		{"inside window", "2026-09-19T00:00:00Z", "2026-09-20T00:00:00Z", ""},
		{"no bounds", "", "", ""},
		{"expired", "2026-09-01T00:00:00Z", "2026-09-10T00:00:00Z", "expired"},
		{"not started", "2026-09-30T00:00:00Z", "", "not_started"},
		{"only end in past", "", "2026-09-18T00:00:00Z", "expired"},
		{"bad timestamps ignored", "not-a-time", "also-bad", ""},
	}
	for _, tc := range cases {
		w := Window{StartsAt: tc.start, EndsAt: tc.end}
		if got := w.ResolveTime(now); got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

// TestShortcodePayloadHasNoStructure pins the red line: the code is opaque
// random bits over a fixed alphabet. It can never carry separators, scheme or
// query syntax, padding, or id-shaped prefixes with delimiters, and it is not
// derived from any system identifier (never equal to one).
func TestShortcodePayloadHasNoStructure(t *testing.T) {
	ids := []string{"tnt_abc123", "usr_demo01", "cmp_demo01", "https://example.com/c/x?tenant=tnt_abc123"}
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		code, err := NewShortcode()
		if err != nil {
			t.Fatalf("NewShortcode: %v", err)
		}
		if len(code) != ShortcodeLen {
			t.Fatalf("len = %d, want %d", len(code), ShortcodeLen)
		}
		if !ValidShortcode(code) {
			t.Fatalf("generated code %q fails its own format gate", code)
		}
		// alphabet-only already excludes ? & = / . : space + % padding etc.;
		// assert it explicitly so the red line does not depend on ValidShortcode.
		for _, r := range code {
			if !strings.ContainsRune(shortcodeAlphabet, r) {
				t.Fatalf("code %q contains non-alphabet rune %q", code, r)
			}
		}
		// the code is never equal to (or a prefix/suffix of) any real identifier
		for _, id := range ids {
			if code == id || strings.HasPrefix(id, code) || strings.HasSuffix(id, code) {
				t.Fatalf("code %q collides with identifier %q", code, id)
			}
		}
		if seen[code] {
			t.Fatalf("duplicate code %q over %d draws", code, i+1)
		}
		seen[code] = true
	}
}

func TestValidShortcodeRejectsStructuredPayloads(t *testing.T) {
	bad := []string{
		"",                 // empty
		"SHORT",            // too short
		"ABCDEFGHIJKL",     // I and L are excluded from the alphabet
		"ABC.DEFGHIJKL",    // dot separator
		"ABC/DEFGHIJKL",    // slash (path-like)
		"ABC:DEFGHIJKL",    // colon (scheme-like)
		"ABC=DEFGHIJKL",    // '=' (query-like)
		"ABC&DEFGHIJKL",    // query separator
		"abcd1234efgh",     // lowercase is not in the alphabet
		"aGVsbG8gd29ybGQ=", // base64 with padding
		"http://x/y000000", // URL-ish
		"../../etc/passwd", // traversal
	}
	for _, code := range bad {
		if ValidShortcode(code) {
			t.Fatalf("ValidShortcode(%q) = true, want false", code)
		}
	}
	// a canonical generated code must pass
	good, err := NewShortcode()
	if err != nil {
		t.Fatal(err)
	}
	if !ValidShortcode(good) {
		t.Fatalf("generated %q must pass", good)
	}
}
