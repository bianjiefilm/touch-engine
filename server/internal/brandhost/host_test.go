package brandhost

import (
	"net/http"
	"testing"
)

func TestCanonicalStripsPortAndCase(t *testing.T) {
	got, err := Canonical("Brand-A.Example.:8443")
	if err != nil || got != "brand-a.example" {
		t.Fatalf("got %q err=%v", got, err)
	}
}

func TestForwardedHostDoesNotSelectBrand(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "http://brand-a.example/api", nil)
	r.Host = "brand-a.example"
	r.Header.Set("X-Forwarded-Host", "brand-b.example")
	got, err := FromRequest(r)
	if err != nil || got != "brand-a.example" {
		t.Fatalf("spoof got %q err=%v", got, err)
	}
}

func TestConflictingPublicHostRejected(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "http://brand-a.example/api", nil)
	r.Header.Add("X-Touch-Public-Host", "brand-a.example")
	r.Header.Add("X-Touch-Public-Host", "brand-b.example")
	if _, err := FromRequest(r); err == nil {
		t.Fatal("expected conflict")
	}
}

func TestIPHostRejected(t *testing.T) {
	if _, err := Canonical("127.0.0.1"); err == nil {
		t.Fatal("ip must not be a brand host")
	}
}
