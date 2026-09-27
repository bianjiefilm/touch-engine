package httpapi

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/brandctx"
	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

type brandScript struct {
	status int
	body   string
}

func TestBrandPilotKeepsTenantsApartAndDoesNotInventALiveBridge(t *testing.T) {
	script := map[string]brandScript{
		"brand-a.example": {status: 200, body: `{
			"brand_id":"brand-a","kind":"white_label","status":"active","display_name":"甲牌增长",
			"support_name":"甲牌客服","support_contact":"support@brand-a.example",
			"config_version":4,"admit_login":true,"admit_public":true,
			"notification_brand_ref":"notif-a",
			"apps":[
				{"app_id":"goboost","public_visible":true,"availability":"ready"},
				{"app_id":"touch-engine","public_visible":true,"availability":"ready"}
			]
		}`},
		"brand-b.example": {status: 200, body: `{
			"brand_id":"brand-b","kind":"first_party","status":"active","display_name":"自营壳",
			"config_version":1,"admit_login":true,"admit_public":true,
			"apps":[{"app_id":"touch-engine","public_visible":true,"availability":"ready"}]
		}`},
		"paused.example": {status: 403, body: `{
			"brand_id":"brand-a","status":"suspended","display_name":"甲牌增长",
			"safe_page":"brand_unavailable","admit_login":false,"admit_public":false
		}`},
	}
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/internal/v1/brand/context" {
			t.Errorf("unexpected registry path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Header.Get("X-App-ID") != "" {
			t.Errorf("registry must not see X-App-ID")
		}
		host := r.Host
		if i := strings.LastIndex(host, ":"); i >= 0 && strings.IndexByte(host, ']') < 0 {
			// httptest may append a port; brand identity ignores it
			if _, err := http.NewRequest(http.MethodGet, "http://"+host, nil); err == nil {
				host = host[:i]
			}
		}
		reply, ok := script[host]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"unknown_brand","admit_login":false,"admit_public":false}`))
			return
		}
		w.WriteHeader(reply.status)
		_, _ = w.Write([]byte(reply.body))
	}))
	defer reg.Close()

	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	st := store.New(d)
	tenA, err := st.CreateTenant("A餐饮")
	if err != nil {
		t.Fatal(err)
	}
	tenB, err := st.CreateTenant("B餐饮")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindTenantBrand(tenA.ID, "brand-a"); err != nil {
		t.Fatal(err)
	}
	if err := st.BindTenantBrand(tenB.ID, "brand-a"); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct{ tenant, principal, role string }{
		{tenA.ID, "usr_owner_a", "org_owner"},
		{tenB.ID, "usr_owner_b", "org_owner"},
		{tenA.ID, "usr_staff_off", "staff"},
	} {
		if _, err := st.CreateMember(m.tenant, m.principal, m.role, m.principal, "test", true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Exec(`UPDATE members SET enabled=0 WHERE principal_ref=?`, "usr_staff_off"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO agency_relations(id,tenant_id,agent_principal,established_at,revoked_at,revoked_by) VALUES('rel_old',?,'usr_agent','2026-09-01T00:00:00Z','2026-09-02T00:00:00Z','usr_owner_a')`, tenA.ID); err != nil {
		t.Fatal(err)
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
	})
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return idsrv.URL
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_BRAND":
			return "on"
		case "PLATFORM_BRAND_BASE_URL":
			return reg.URL
		case "PLATFORM_BRAND_TOKEN":
			return "reader-secret"
		case "PUBLIC_BASE_URL":
			return "https://fallback.example"
		default:
			return ""
		}
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cfg, d, idc, nil, log.New(io.Discard, "", 0))
	s.Brand = &brandctx.Client{BaseURL: reg.URL, Token: "reader-secret", HTTP: reg.Client()}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	do := func(method, path, session, tenant, host, body string) (int, map[string]any, string) {
		t.Helper()
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, ts.URL+path, rd)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Internal-Token", "test-internal-secret")
		if session != "" {
			req.Header.Set("X-Session-Token", session)
		}
		if tenant != "" {
			req.Header.Set("X-Tenant-ID", tenant)
		}
		if host != "" {
			req.Header.Set("X-Touch-Public-Host", host)
		}
		req.Header.Set("X-Forwarded-Host", "brand-b.example")
		req.Header.Set("X-Channel-Admin", "1")
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return res.StatusCode, out, string(raw)
	}

	status, who, raw := do("GET", "/api/v1/whoami", "sess-owner-a", tenA.ID, "brand-a.example", "")
	if status != 200 || who["workbar"] != "甲牌增长 / A餐饮" {
		t.Fatalf("whoami %d %s", status, raw)
	}
	if strings.Contains(raw, "goboost") || strings.Contains(raw, "payer") {
		t.Fatalf("whoami leaked foreign app or payer: %s", raw)
	}
	caps, _ := who["capabilities"].([]any)
	if len(caps) != 1 || caps[0] != "touch-engine" {
		t.Fatalf("capabilities %#v", who["capabilities"])
	}

	// Host of another brand cannot retarget tenant A, even with a forged forwarded host.
	status, mismatch, _ := do("GET", "/api/v1/whoami", "sess-owner-a", tenA.ID, "brand-b.example", "")
	if status != http.StatusForbidden || mismatch["error"] != "brand_mismatch" {
		t.Fatalf("mismatch %d %#v", status, mismatch)
	}
	again, err := st.GetTenant(tenA.ID)
	if err != nil || again.BrandID != "brand-a" {
		t.Fatalf("tenant brand changed: %+v %v", again, err)
	}

	// Channel-admin header is not a membership.
	status, nobody, _ := do("GET", "/api/v1/whoami", "sess-owner-a", "tnt_missing", "brand-a.example", "")
	if status != http.StatusForbidden || nobody["error"] != "not_member" {
		t.Fatalf("channel admin %d %#v", status, nobody)
	}

	// Two tenants on one brand do not share campaigns.
	status, created, _ := do("POST", "/api/v1/campaigns", "sess-owner-a", tenA.ID, "brand-a.example", `{"title":"A店午市"}`)
	if status != http.StatusCreated {
		t.Fatalf("create %d %#v", status, created)
	}
	campID := created["id"].(string)
	status, _, _ = do("POST", "/api/v1/campaigns/"+campID+"/status", "sess-owner-a", tenA.ID, "brand-a.example", `{"status":"active"}`)
	if status != 200 {
		t.Fatalf("activate %d", status)
	}
	status, listed, raw := do("GET", "/api/v1/campaigns", "sess-owner-b", tenB.ID, "brand-a.example", "")
	if status != 200 {
		t.Fatalf("list B %d %s", status, raw)
	}
	if items, _ := listed["items"].([]any); len(items) != 0 {
		t.Fatalf("tenant B saw A's campaigns: %s", raw)
	}
	status, _, _ = do("GET", "/api/v1/campaigns/"+campID, "sess-owner-b", tenB.ID, "brand-a.example", "")
	if status != http.StatusNotFound {
		t.Fatalf("cross read %d", status)
	}

	status, link, raw := do("POST", "/api/v1/campaigns/"+campID+"/links", "sess-owner-a", tenA.ID, "brand-a.example", "")
	if status != http.StatusCreated {
		t.Fatalf("link %d %s", status, raw)
	}
	code := link["code"].(string)
	pub, err := st.LinkPublication(code)
	if err != nil || pub.BrandID != "brand-a" || pub.Host != "brand-a.example" {
		t.Fatalf("publication %+v %v", pub, err)
	}

	// Public page leads with the merchant, brand is only the shell.
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/public/links/"+code, nil)
	req.Header.Set("X-Touch-Public-Host", "brand-a.example")
	req.Header.Set("X-Forwarded-Host", "brand-b.example")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), `"title":"A店午市"`) || !strings.Contains(string(body), `"merchant_name":"A餐饮"`) || !strings.Contains(string(body), `"display_name":"甲牌增长"`) {
		t.Fatalf("public %d %s", res.StatusCode, body)
	}

	// Other brand host does not reveal the campaign.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/public/links/"+code, nil)
	req.Header.Set("X-Touch-Public-Host", "brand-b.example")
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound || strings.Contains(string(body), "A店午市") || !strings.Contains(string(body), "domain_mismatch") {
		t.Fatalf("mismatch public %d %s", res.StatusCode, body)
	}

	// Brand pause is not campaign pause, and does not look like not_found.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/public/links/"+code, nil)
	req.Header.Set("X-Touch-Public-Host", "paused.example")
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `"state":"brand_suspended"`) || strings.Contains(string(body), `"state":"paused"`) || strings.Contains(string(body), "A店午市") {
		t.Fatalf("suspended page %d %s", res.StatusCode, body)
	}

	// Fallback host still opens an already published code.
	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/public/links/"+code, nil)
	req.Header.Set("X-Touch-Public-Host", "fallback.example")
	res, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(string(body), "A店午市") {
		t.Fatalf("fallback %d %s", res.StatusCode, body)
	}

	// New QR uses the published brand host and carries no credential.
	status, qr, raw := do("GET", "/api/v1/campaigns/"+campID+"/links/"+link["id"].(string)+"/qrcode?format=json", "sess-owner-a", tenA.ID, "brand-a.example", "")
	if status != 200 || qr["origin_kind"] != "brand" {
		t.Fatalf("qr %d %s", status, raw)
	}
	qurl := qr["url"].(string)
	if !strings.HasPrefix(qurl, "https://brand-a.example/c/") || strings.Contains(qurl, "token") || strings.Contains(qurl, tenA.ID) {
		t.Fatalf("qr url %s", qurl)
	}

	// Charge hold blocks only billable creates.
	status, _, _ = do("POST", "/api/v1/tenant-charge-hold", "sess-owner-a", tenA.ID, "brand-a.example", `{"held":true}`)
	if status != 200 {
		t.Fatalf("hold %d", status)
	}
	status, held, _ := do("POST", "/api/v1/campaigns", "sess-owner-a", tenA.ID, "brand-a.example", `{"title":"收费任务","billable":true}`)
	if status != http.StatusConflict || held["error"] != "charge_hold" {
		t.Fatalf("billable %d %#v", status, held)
	}
	status, plain, _ := do("POST", "/api/v1/campaigns", "sess-owner-a", tenA.ID, "brand-a.example", `{"title":"普通活动"}`)
	if status != http.StatusCreated {
		t.Fatalf("non-billable %d %#v", status, plain)
	}

	// Suspend then restore does not re-enable the disabled staff or the revoked delegation.
	status, life, raw := do("POST", "/api/v1/tenant-lifecycle", "sess-owner-a", tenA.ID, "brand-a.example", `{"lifecycle":"suspended"}`)
	if status != 200 || life["members_unchanged"] != true {
		t.Fatalf("suspend %d %s", status, raw)
	}
	status, _, _ = do("POST", "/api/v1/tenant-lifecycle", "sess-owner-a", tenA.ID, "brand-a.example", `{"lifecycle":"active"}`)
	if status != 200 {
		t.Fatalf("restore %d", status)
	}
	var enabled int
	if err := d.QueryRow(`SELECT enabled FROM members WHERE principal_ref=?`, "usr_staff_off").Scan(&enabled); err != nil || enabled != 0 {
		t.Fatalf("staff re-enabled %d %v", enabled, err)
	}
	var revoked string
	if err := d.QueryRow(`SELECT revoked_at FROM agency_relations WHERE id='rel_old'`).Scan(&revoked); err != nil || revoked == "" {
		t.Fatalf("delegation revived %q %v", revoked, err)
	}

	// Export is tenant-scoped and stays bound when the other tenant asks.
	if _, err := st.CreateStore(tenB.ID, "B店", "秘密地址", "usr_owner_b"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO lead_submissions(id,tenant_id,campaign_id,submission_ref,dedup_key,name,phone,consent_at,created_at,updated_at) VALUES('ld1',?,?,'sub_a','dk_a','张三','13800138000','2026-09-27T00:00:00Z','2026-09-27T00:00:00Z','2026-09-27T00:00:00Z')`, tenA.ID, campID); err != nil {
		t.Fatal(err)
	}
	status, exp, raw := do("POST", "/api/v1/tenant-exports", "sess-owner-a", tenA.ID, "brand-a.example", `{"purpose":"offboarding"}`)
	if status != http.StatusCreated {
		t.Fatalf("export %d %s", status, raw)
	}
	status, _, raw = do("GET", "/api/v1/tenant-exports/"+exp["id"].(string), "sess-owner-b", tenB.ID, "brand-a.example", "")
	if status != http.StatusForbidden || strings.Contains(raw, "13800138000") || strings.Contains(raw, "秘密地址") {
		t.Fatalf("cross export %d %s", status, raw)
	}
	status, _, raw = do("GET", "/api/v1/tenant-exports/"+exp["id"].(string), "sess-owner-a", tenA.ID, "brand-a.example", "")
	if status != 200 || strings.Contains(raw, "13800138000") || strings.Contains(raw, "张三") || strings.Contains(raw, "秘密地址") || !strings.Contains(raw, "sub_a") || !strings.Contains(raw, `"brand_id":"brand-a"`) {
		t.Fatalf("own export %d %s", status, raw)
	}

	// Login on a suspended host does not set a session.
	loginReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/login", strings.NewReader(`{"email":"a@example.com","password":"x"}`))
	loginReq.Header.Set("X-Internal-Token", "test-internal-secret")
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-Touch-Public-Host", "paused.example")
	loginRes, err := ts.Client().Do(loginReq)
	if err != nil {
		t.Fatal(err)
	}
	defer loginRes.Body.Close()
	if loginRes.StatusCode != http.StatusForbidden || loginRes.Header.Get("Set-Cookie") != "" {
		t.Fatalf("suspended login %d cookies %q", loginRes.StatusCode, loginRes.Header.Get("Set-Cookie"))
	}
}

func TestLegacyShortCodeSurvivesBrandFeature(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	st := store.New(d)
	ten, err := st.CreateTenant("老商家")
	if err != nil {
		t.Fatal(err)
	}
	camp, err := st.CreateCampaign(store.NewCampaign{TenantID: ten.ID, Title: "老活动", CreatedBy: "ops"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.TransitionCampaign(camp.ID, ten.ID, "active"); err != nil {
		t.Fatal(err)
	}
	link, err := st.CreateLink(ten.ID, camp.ID, "ops")
	if err != nil {
		t.Fatal(err)
	}
	reg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":"unknown_brand"}`))
	}))
	defer reg.Close()
	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "tok"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.invalid"
		case "PLATFORM_IDENTITY_TOKEN":
			return "t"
		case "FEATURE_BRAND":
			return "on"
		case "PLATFORM_BRAND_BASE_URL":
			return reg.URL
		case "PLATFORM_BRAND_TOKEN":
			return "reader"
		default:
			return ""
		}
	})
	s := New(cfg, d, &identity.Client{BaseURL: "http://identity.invalid", Token: "t", AppID: "touch-engine"}, nil, log.New(io.Discard, "", 0))
	s.Brand = &brandctx.Client{BaseURL: reg.URL, Token: "reader", HTTP: reg.Client()}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/public/links/"+link.Code, nil)
	req.Header.Set("X-Touch-Public-Host", "anywhere.example")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(raw), "老活动") {
		t.Fatalf("legacy code died: %d %s", res.StatusCode, raw)
	}
}
