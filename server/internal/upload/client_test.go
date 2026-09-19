package upload

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateRefOKAnchorsSha256(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/upload/assets/ast_123" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("X-PilotSeaView-Internal-Token") != "tok" || r.Header.Get("X-App-ID") != "touch-engine" {
			t.Fatalf("headers missing")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"asset_id":"ast_123","app_id":"touch-engine","status":"ready","filename":"a.mp4","content_type":"video/mp4","size_bytes":10,"sha256":"abc123"}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "tok", AppID: "touch-engine", HTTP: srv.Client()}
	meta, err := c.ValidateRef(context.Background(), "ast_123", "")
	if err != nil {
		t.Fatalf("ValidateRef: %v", err)
	}
	if meta.SHA256 != "abc123" || meta.Status != "ready" {
		t.Fatalf("meta = %+v", meta)
	}
	// caller-supplied matching version is accepted
	if _, err := c.ValidateRef(context.Background(), "ast_123", "ABC123"); err != nil {
		t.Fatalf("matching version rejected: %v", err)
	}
	// mismatched version rejected
	if _, err := c.ValidateRef(context.Background(), "ast_123", "deadbeef"); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("want ErrVersionMismatch, got %v", err)
	}
}

func TestValidateRefFailClosed(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"not found", http.StatusNotFound, `{"ok":false}`, ErrNotFound},
		{"gone (expired)", http.StatusGone, `{}`, ErrNotFound},
		{"quarantined (locked)", http.StatusLocked, `{}`, ErrNotFound},
		{"unauthorized", http.StatusUnauthorized, `{}`, ErrUnauthorized},
		{"forbidden (cross-app)", http.StatusForbidden, `{}`, ErrForbidden},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		c := &Client{BaseURL: srv.URL, Token: "tok", AppID: "touch-engine", HTTP: srv.Client()}
		if _, err := c.ValidateRef(context.Background(), "ast_x", ""); !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
		srv.Close()
	}
}

func TestValidateRefNotReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"asset_id":"ast_p","status":"pending","sha256":"abc"}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "tok", AppID: "touch-engine", HTTP: srv.Client()}
	if _, err := c.ValidateRef(context.Background(), "ast_p", ""); !errors.Is(err, ErrNotReady) {
		t.Fatalf("want ErrNotReady, got %v", err)
	}
}

func TestValidateRefTransportFailureIsExplicit(t *testing.T) {
	c := &Client{BaseURL: "http://127.0.0.1:1", Token: "t", AppID: "touch-engine", HTTP: &http.Client{Timeout: 1}}
	_, err := c.ValidateRef(context.Background(), "ast_x", "")
	if err == nil {
		t.Fatal("want explicit transport error, got nil")
	}
	for _, sentinel := range []error{ErrNotFound, ErrUnauthorized, ErrForbidden, ErrNotReady} {
		if errors.Is(err, sentinel) {
			t.Fatalf("transport failure misclassified as %v", sentinel)
		}
	}
}

func TestValidateRefMissingSha256Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"asset_id":"ast_ns","status":"ready","sha256":""}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "tok", AppID: "touch-engine", HTTP: srv.Client()}
	if _, err := c.ValidateRef(context.Background(), "ast_ns", ""); err == nil || errors.Is(err, ErrNotReady) {
		t.Fatalf("asset without sha256 must be rejected, got %v", err)
	}
}
