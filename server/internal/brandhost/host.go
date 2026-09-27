// Package brandhost canonicalizes the public host touch will show to the
// brand registry. Client-supplied X-Forwarded-Host is never a brand signal:
// only the BFF-injected X-Touch-Public-Host (or the request Host when the
// server is called directly) is read.
package brandhost

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"unicode"

	"golang.org/x/net/idna"
)

const publicHostHeader = "X-Touch-Public-Host"

// ErrInvalidHost means the host cannot be a brand identity.
var ErrInvalidHost = errors.New("brandhost: invalid host")

// FromRequest returns the canonical public host.
// X-Forwarded-Host is ignored even if present. Repeated or comma-joined
// X-Touch-Public-Host values are rejected.
func FromRequest(r *http.Request) (string, error) {
	if r == nil {
		return "", ErrInvalidHost
	}
	values := r.Header.Values(publicHostHeader)
	raw := ""
	if len(values) > 0 {
		first := strings.TrimSpace(values[0])
		if first == "" || strings.Contains(first, ",") {
			return "", ErrInvalidHost
		}
		for _, v := range values[1:] {
			if strings.TrimSpace(v) != first {
				return "", ErrInvalidHost
			}
		}
		raw = first
	} else {
		raw = r.Host
	}
	return Canonical(raw)
}

// Canonical lowercases, strips a numeric port and trailing dots, and maps
// IDN to punycode. IP literals are not brand hosts.
func Canonical(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "://") || strings.ContainsAny(raw, "/@?# \t") {
		return "", ErrInvalidHost
	}
	raw = strings.ToLower(raw)
	if i := strings.LastIndex(raw, ":"); i >= 0 {
		port := raw[i+1:]
		if port == "" || strings.IndexByte(raw, ':') != i || !digits(port) {
			return "", ErrInvalidHost
		}
		raw = raw[:i]
	}
	raw = strings.TrimRight(raw, ".")
	if raw == "" || strings.Contains(raw, "..") {
		return "", ErrInvalidHost
	}
	if ip := net.ParseIP(raw); ip != nil {
		return "", ErrInvalidHost
	}
	ascii, err := idna.Lookup.ToASCII(raw)
	if err != nil || ascii == "" {
		return "", ErrInvalidHost
	}
	ascii = strings.TrimRight(ascii, ".")
	for _, label := range strings.Split(ascii, ".") {
		if !validLabel(label) {
			return "", ErrInvalidHost
		}
	}
	return ascii, nil
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validLabel(label string) bool {
	if label == "" || len(label) > 63 {
		return false
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' {
			continue
		}
		return false
	}
	return true
}
