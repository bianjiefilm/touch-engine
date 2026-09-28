package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestLeadRecordFetch(t *testing.T) {
	f := newLeadsFixture(t)
	status, out := f.submitLead(t, f.code, validLeadBody)
	if status != 201 {
		t.Fatalf("submit = %d %v", status, out)
	}
	ref := out["submission_ref"].(string)

	t.Run("missing token", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, f.ts.URL+"/internal/v1/lead-records/"+ref, nil)
		req.Header.Set("X-Leads-Tenant", f.tenA)
		res, err := f.ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status=%d", res.StatusCode)
		}
	})

	t.Run("wrong tenant is not found", func(t *testing.T) {
		code, body := f.fetchRecord(t, ref, "test-internal-secret", "tnt_other")
		if code != http.StatusNotFound {
			t.Fatalf("status=%d body=%s", code, body)
		}
		if strings.Contains(body, "13800138000") || strings.Contains(body, "张三") {
			t.Fatalf("mismatch leaked contact: %s", body)
		}
	})

	t.Run("authorized record matches the payload hash", func(t *testing.T) {
		code, body := f.fetchRecord(t, ref, "test-internal-secret", f.tenA)
		if code != http.StatusOK {
			t.Fatalf("status=%d body=%s", code, body)
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(body), &rec); err != nil {
			t.Fatal(err)
		}
		if rec["submission_ref"] != ref || rec["tenant_id"] != f.tenA {
			t.Fatalf("record=%v", rec)
		}
		if rec["name"] != "张三" || rec["phone"] != "13800138000" || rec["authorization"] != "granted" || rec["purpose"] != "lead_submission" {
			t.Fatalf("record=%v", rec)
		}
		consents, _ := rec["consents"].(map[string]any)
		if consents["form_authorized"] != true || consents["marketing_phone"] != true || consents["notice_version"] != "v1" {
			t.Fatalf("consents=%v", consents)
		}
		sum := recordHash("张三", "13800138000", "")
		lead, err := f.s.St.GetLeadSubmissionByRef(ref)
		if err != nil {
			t.Fatal(err)
		}
		if recordHash(lead.Name, lead.Phone, lead.Wechat) != sum {
			t.Fatalf("stored contact hash drifted")
		}
		if strings.Contains(body, "sync_error") || strings.Contains(body, "consent_ip") {
			t.Fatalf("record exposed internal fields: %s", body)
		}
	})
}

func (f *leadsFixture) fetchRecord(t *testing.T, ref, token, tenant string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.ts.URL+"/internal/v1/lead-records/"+ref, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("X-Internal-Token", token)
	}
	if tenant != "" {
		req.Header.Set("X-Leads-Tenant", tenant)
	}
	res, err := f.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(raw)
}

func recordHash(name, phone, wechat string) string {
	b, _ := json.Marshal(map[string]string{"name": name, "phone": phone, "wechat": wechat})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
