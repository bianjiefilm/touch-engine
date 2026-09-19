package notifytask

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDirectedClientPublishesWithContractHeaders(t *testing.T) {
	var gotPath, gotToken, gotApp, gotCT string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotToken = r.Header.Get("X-PilotSeaView-Internal-Token")
		gotApp = r.Header.Get("X-Notify-App-ID")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"event_id":"ev_1","duplicate":false}`))
	}))
	defer srv.Close()

	sc := New(SideNotify, true, srv.URL, "tok", "touch-engine", notifyAppIDHeader)
	c := NewDirectedClient(sc, srv.Client())
	err := c.PostEvent(context.Background(), []byte(`{"event_profile":{}}`))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if gotPath != "/internal/v1/notify/events" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotToken != "tok" || gotApp != "touch-engine" {
		t.Fatalf("headers = %q %q", gotToken, gotApp)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type = %q", gotCT)
	}
}

func TestDirectedClientContractErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
	}{
		{"conflict", http.StatusConflict},
		{"server refusal", http.StatusBadRequest},
		{"auth refusal", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		}))
		sc := New(SideNotify, true, srv.URL, "tok", "touch-engine", notifyAppIDHeader)
		c := NewDirectedClient(sc, srv.Client())
		if err := c.PostEvent(context.Background(), []byte(`{}`)); err == nil {
			t.Fatalf("%s: publish must fail explicitly", tc.name)
		}
		srv.Close()
	}
}

func TestDirectedClientFailClosed(t *testing.T) {
	// feature off -> ErrFeatureDisabled before any network attempt
	off := New(SideNotify, false, "http://127.0.0.1:1", "tok", "app", notifyAppIDHeader)
	c := NewDirectedClient(off, nil)
	if err := c.PostEvent(context.Background(), []byte(`{}`)); err != ErrFeatureDisabled {
		t.Fatalf("off publish = %v, want ErrFeatureDisabled", err)
	}
	if _, err := c.Deliveries(context.Background(), "ev"); err != ErrFeatureDisabled {
		t.Fatalf("off deliveries = %v, want ErrFeatureDisabled", err)
	}
	// on but unconfigured -> ErrNotConfigured
	bad := New(SideNotify, true, "", "tok", "app", notifyAppIDHeader)
	c2 := NewDirectedClient(bad, nil)
	if err := c2.PostEvent(context.Background(), []byte(`{}`)); err != ErrNotConfigured {
		t.Fatalf("unconfigured publish = %v, want ErrNotConfigured", err)
	}
}

func TestDirectedClientDeliveries(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/internal/v1/notify/events/ev_1/deliveries" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"delivery_id":"d1","status":"pending","attempt_count":2,"last_error":"conn refused"}]}`))
	}))
	defer srv.Close()
	sc := New(SideNotify, true, srv.URL, "tok", "touch-engine", notifyAppIDHeader)
	c := NewDirectedClient(sc, srv.Client())
	items, err := c.Deliveries(context.Background(), "ev_1")
	if err != nil {
		t.Fatalf("deliveries: %v", err)
	}
	if len(items) != 1 || items[0].Status != "pending" || items[0].Attempts != 2 {
		t.Fatalf("items = %+v", items)
	}
	if _, err := c.Deliveries(context.Background(), ""); err == nil {
		t.Fatal("empty event id must be refused")
	}
}
