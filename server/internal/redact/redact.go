// Package redact masks personal identifiers for logs and API responses.
package redact

import "strings"

// MaskEmail masks the local part of an email: "merchant@example.com" ->
// "m***@example.com". Non-email strings are masked wholesale.
func MaskEmail(v string) string {
	v = strings.TrimSpace(v)
	at := strings.Index(v, "@")
	if at <= 0 || at == len(v)-1 {
		if v == "" {
			return ""
		}
		return "***"
	}
	return v[:1] + "***" + v[at:]
}

// MaskCode keeps only the first 2 and last 2 characters of a secret-ish value.
func MaskCode(v string) string {
	if len(v) <= 4 {
		return "***"
	}
	return v[:2] + "***" + v[len(v)-2:]
}
