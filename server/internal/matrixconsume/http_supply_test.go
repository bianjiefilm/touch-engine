package matrixconsume

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/matrixhandoff"
)

func TestEmptySupplyURLDoesNotDial(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("empty supply dialed")
		return nil, nil
	})}
	perm, err := NewHTTPPermission("", client).Decide(context.Background(), "usr", "ten", "brd")
	if err != nil || perm.Supply != SupplyMissing || perm.Allowed {
		t.Fatalf("permission = %+v %v", perm, err)
	}
	sink := NewHTTPDraftSink("  ", client)
	if sink.Available() {
		t.Fatal("blank url is available")
	}
}

func TestHTTPSupplyAcceptsDraftAndRejectsPublished(t *testing.T) {
	var sawPerm, sawDraft bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/permissions":
			sawPerm = true
			if r.URL.Query().Get("action") != "brand_matrix_draft" || r.URL.Query().Get("tenant_id") != "ten" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"allowed":true,"supply":"ready","reason":"ok"}`))
		case "/drafts":
			sawDraft = true
			raw, _ := io.ReadAll(r.Body)
			var posted map[string]any
			if err := json.Unmarshal(raw, &posted); err != nil {
				t.Errorf("draft json: %v", err)
			}
			if posted["activity_version"] != float64(3) || posted["sha256"] != strings.Repeat("ab", 32) || posted["disclosure"] == "" || posted["return_location_token"] != "return:cmp" || posted["offer_expiry"] == nil || posted["offer_expiry"] == "" {
				t.Errorf("draft body = %s", raw)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"plan_id":"pln","status":"draft","executed":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	gate := NewHTTPPermission(srv.URL, srv.Client())
	sink := NewHTTPDraftSink(srv.URL, srv.Client())
	out, err := New(gate, sink, nil).Schedule(context.Background(), time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC), Actor{
		PrincipalID: "usr", TenantID: "ten", BrandID: "brd", Role: "org_owner",
	}, matrixhandoff.Request{
		TenantID: "ten", BrandID: "brd", StoreID: "sto", ActivityID: "cmp", ActivityVersion: 3,
		Assets:              []matrixhandoff.Asset{{ID: "ast", Hash: strings.Repeat("ab", 32), Kind: "video"}},
		OfferText:           "到店有礼",
		OfferExpiry:         time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		Disclosure:          "这是商家活动草稿，不是已经发出的内容。",
		ReturnLocationToken: "return:cmp",
	}, Surfaces{LeadEnabled: true, UGCEnabled: true})
	if err != nil || !sawPerm || !sawDraft || out.OutboundComplete || out.Draft.Status != matrixhandoff.StatusDraft {
		t.Fatalf("schedule err=%v perm=%v draft=%v out=%+v", err, sawPerm, sawDraft, out)
	}
	if strings.Contains(out.Copy, "发布成功") || strings.Contains(out.Copy, "外发完成") {
		t.Fatalf("copy %q", out.Copy)
	}

	published := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/permissions" {
			_, _ = w.Write([]byte(`{"allowed":true,"supply":"ready","reason":"ok"}`))
			return
		}
		_, _ = w.Write([]byte(`{"plan_id":"pln","status":"published","executed":false}`))
	}))
	defer published.Close()
	_, err = New(NewHTTPPermission(published.URL, published.Client()), NewHTTPDraftSink(published.URL, published.Client()), nil).Schedule(
		context.Background(), time.Now().UTC(), Actor{PrincipalID: "usr", TenantID: "ten", BrandID: "brd", Role: "org_owner"},
		matrixhandoff.Request{
			TenantID: "ten", BrandID: "brd", StoreID: "sto", ActivityID: "cmp", ActivityVersion: 1,
			Assets:              []matrixhandoff.Asset{{ID: "ast", Hash: strings.Repeat("cd", 32), Kind: "video"}},
			OfferText:           "到店有礼",
			OfferExpiry:         time.Now().Add(24 * time.Hour),
			Disclosure:          "这是商家活动草稿，不是已经发出的内容。",
			ReturnLocationToken: "return:cmp",
		}, Surfaces{LeadEnabled: true, UGCEnabled: false})
	if err != ErrNotDraft {
		t.Fatalf("published ack err = %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
