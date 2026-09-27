package brandctx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadUsesHostNotForwardedAndOmitsAppID(t *testing.T) {
	var gotHost, gotApp, gotSvc string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotApp = r.Header.Get("X-App-ID")
		gotSvc = r.Header.Get("X-Service-ID")
		if r.Header.Get("X-Forwarded-Host") != "" {
			t.Errorf("client forwarded X-Forwarded-Host")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"brand_id":"brand-a","status":"active","display_name":"甲牌",
			"admit_login":true,"admit_public":true,"config_version":3,
			"apps":[
				{"app_id":"goboost","public_visible":true,"availability":"ready"},
				{"app_id":"touch-engine","public_visible":true,"availability":"ready"}
			]
		}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "reader", HTTP: srv.Client()}
	res, err := c.Read(context.Background(), "Brand-A.Example", false)
	if err != nil || res.Kind != KindReady || res.Manifest.BrandID != "brand-a" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if gotHost != "Brand-A.Example" && !strings.EqualFold(gotHost, "brand-a.example") {
		t.Fatalf("host sent %q", gotHost)
	}
	if gotApp != "" {
		t.Fatalf("X-App-ID must not be sent, got %q", gotApp)
	}
	if gotSvc != "brand-reader" {
		t.Fatalf("service %q", gotSvc)
	}
	if !res.Manifest.TouchEnabled("touch-engine") {
		t.Fatal("touch should be enabled")
	}
	// Foreign apps stay in the raw manifest for the client to ignore.
	// TouchEnabled must not treat goboost as touch.
	if res.Manifest.TouchEnabled("goboost") {
		t.Fatal("goboost is not the touch app id")
	}
}

func TestSuspendedIsNotUnknown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"brand_id":"brand-a","status":"suspended","safe_page":"brand_unavailable","admit_login":false,"admit_public":false,"display_name":"甲牌"}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "reader", HTTP: srv.Client()}
	res, err := c.Read(context.Background(), "a.example", false)
	if err != nil || res.Kind != KindSuspended || res.Manifest.DisplayName != "甲牌" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestStaleCacheOnlyWhenAllowed(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits == 1 {
			_, _ = w.Write([]byte(`{"brand_id":"brand-a","status":"active","display_name":"甲牌","admit_login":true,"admit_public":true,"apps":[{"app_id":"touch-engine","public_visible":true}]}`))
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"code":"brand_unavailable"}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "reader", HTTP: srv.Client()}
	if _, err := c.Read(context.Background(), "a.example", false); err != nil {
		t.Fatal(err)
	}
	stale, err := c.Read(context.Background(), "a.example", true)
	if err != nil || !stale.Stale || stale.Manifest.DisplayName != "甲牌" {
		t.Fatalf("stale=%+v err=%v", stale, err)
	}
	fresh, err := c.Read(context.Background(), "a.example", false)
	if err == nil || fresh.Kind != KindUnavailable {
		t.Fatalf("sensitive read must fail closed, got %+v err=%v", fresh, err)
	}
}
