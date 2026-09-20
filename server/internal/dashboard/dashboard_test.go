package dashboard

// dashboard_test.go: HUI-1677 FEAT-0178 数据统计看板(纯汇总层)。
//
// 纪律(票面 2026-09-19「统计真实性与生态来源链」):
//   - v1 只汇总 touch 域已有真实事实;拿不到的外部平台指标 = UNKNOWN
//     (available=false + reason),绝不伪造、不推算、无演示数据;
//   - 每项指标必须带定义(source/dedup_key/denominator/window);
//   - 汇总按需计算,同窗口重算幂等一致。

import (
	"strings"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// ---- 1. window parsing ---------------------------------------------------------

func TestParseWindowValid(t *testing.T) {
	w, err := ParseWindow("2026-09-15T00:00:00+08:00", "2026-09-16T12:00:00Z")
	if err != nil {
		t.Fatalf("ParseWindow: %v", err)
	}
	// bounds are normalized to UTC whole seconds, inclusive
	if !w.Start.Equal(time.Date(2026, 9, 14, 16, 0, 0, 0, time.UTC)) {
		t.Fatalf("start = %v", w.Start)
	}
	if !w.End.Equal(time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("end = %v", w.End)
	}
}

func TestParseWindowRejects(t *testing.T) {
	cases := []struct{ name, start, end string }{
		{"missing start", "", "2026-09-16T00:00:00Z"},
		{"missing end", "2026-09-15T00:00:00Z", ""},
		{"not rfc3339", "2026-09-15", "2026-09-16T00:00:00Z"},
		{"garbage", "yesterday", "tomorrow"},
		{"end before start", "2026-09-16T00:00:00Z", "2026-09-15T00:00:00Z"},
	}
	for _, tc := range cases {
		if _, err := ParseWindow(tc.start, tc.end); err == nil {
			t.Fatalf("%s: want error, got nil", tc.name)
		}
	}
}

// ---- 2. catalog: every ticket metric present, unknowns honestly unavailable ----

func TestCatalogCoversTicketMetrics(t *testing.T) {
	wantOrder := []string{
		KeyTouchTriggers, KeyLeadPageVisits, KeyLeadSubmissions, KeyCRMReceived,
		KeyPublishedVideos, KeyUGCPlays, KeyUGCLikes, KeyPOIExposureDelta, KeyCouponRedemptions,
	}
	got := Catalog()
	if len(got) != len(wantOrder) {
		t.Fatalf("catalog size = %d, want %d", len(got), len(wantOrder))
	}
	for i, m := range got {
		if m.Key != wantOrder[i] {
			t.Fatalf("catalog[%d].key = %q, want %q", i, m.Key, wantOrder[i])
		}
	}
}

// UNKNOWN 指标:available=false 且值绝不出现(不是 0),必须带 reason 与上游引用。
func TestUnknownMetricsNeverPretend(t *testing.T) {
	unknown := map[string]bool{
		KeyPublishedVideos: true, KeyUGCPlays: true, KeyUGCLikes: true,
		KeyPOIExposureDelta: true, KeyCouponRedemptions: true,
	}
	for _, m := range Catalog() {
		if !unknown[m.Key] {
			if !m.Available {
				t.Fatalf("%s: touch-domain metric must be available in the catalog", m.Key)
			}
			continue
		}
		if m.Available {
			t.Fatalf("%s: external metric marked available without a source", m.Key)
		}
		if m.Value != nil {
			t.Fatalf("%s: unavailable metric carries a value %v (must be null, never 0)", m.Key, *m.Value)
		}
		if strings.TrimSpace(m.Reason) == "" {
			t.Fatalf("%s: unavailable metric must carry a reason", m.Key)
		}
		if len(m.Upstream) == 0 {
			t.Fatalf("%s: unavailable metric must name its upstream tickets", m.Key)
		}
	}
}

// 每一项(含 UNKNOWN)都必须带完整定义四要素。
func TestEveryMetricCarriesDefinition(t *testing.T) {
	for _, m := range Catalog() {
		d := m.Definition
		if strings.TrimSpace(d.Source) == "" || strings.TrimSpace(d.DedupKey) == "" ||
			strings.TrimSpace(d.Denominator) == "" || strings.TrimSpace(d.WindowNote) == "" {
			t.Fatalf("%s: definition incomplete: %+v", m.Key, d)
		}
	}
}

// ---- 3. Build: facts -> metrics (available metrics take real values) -----------

func testWindow() Window {
	start, _ := ParseWindow("2026-09-15T00:00:00Z", "2026-09-16T00:00:00Z")
	return start
}

func TestBuildMapsAvailableMetrics(t *testing.T) {
	facts := store.DashboardFacts{
		TriggerTotal:      14,
		TriggerByChannel:  []store.DimBucket{{Key: "web", Value: 9}, {Key: "wecom", Value: 5}},
		TriggerByCampaign: []store.DimBucket{{Key: "cmp_1", Value: 14}},
		TriggerByStore:    []store.DimBucket{{Key: "sto_1", Value: 6}, {Key: "", Value: 8}},
		TriggerByTag:      []store.DimBucket{{Key: "nfc_1", Value: 14}},

		LeadVisitTotal:      9,
		LeadVisitByCampaign: []store.DimBucket{{Key: "cmp_1", Value: 9}},
		LeadVisitByStore:    []store.DimBucket{{Key: "sto_1", Value: 6}},

		SubmissionTotal:          3,
		SubmissionUniqueContacts: 2,
		SubmissionByCampaign:     []store.DimBucket{{Key: "cmp_1", Value: 2}, {Key: "cmp_2", Value: 1}},
		SubmissionByStore:        []store.DimBucket{{Key: "sto_1", Value: 2}, {Key: "", Value: 1}},
		SubmissionByState:        []store.DimBucket{{Key: "crm_received", Value: 1}, {Key: "revoked", Value: 1}, {Key: "pending_sync", Value: 1}},

		CRMReceivedTotal:      1,
		CRMReceivedByCampaign: []store.DimBucket{{Key: "cmp_1", Value: 1}},
		CRMReceivedByStore:    []store.DimBucket{{Key: "sto_1", Value: 1}},
	}
	out := Build(testWindow(), Scope{TenantID: "tnt_a"}, facts, store.DashboardDimensions{})
	byKey := map[string]Metric{}
	for _, m := range out.Metrics {
		byKey[m.Key] = m
	}

	tt := byKey[KeyTouchTriggers]
	if !tt.Available || tt.Value == nil || *tt.Value != 14 {
		t.Fatalf("touch_triggers = %+v", tt)
	}
	if got := tt.Breakdowns["by_channel"]; len(got) != 2 || got[0].Key != "web" || got[0].Value != 9 {
		t.Fatalf("by_channel = %+v", got)
	}
	if got := tt.Breakdowns["by_store"]; len(got) != 2 || got[1].Key != "" || got[1].Value != 8 {
		t.Fatalf("by_store must keep the unbound bucket: %+v", got)
	}

	lp := byKey[KeyLeadPageVisits]
	if !lp.Available || lp.Value == nil || *lp.Value != 9 {
		t.Fatalf("lead_page_visits = %+v", lp)
	}

	ls := byKey[KeyLeadSubmissions]
	if !ls.Available || ls.Value == nil || *ls.Value != 3 {
		t.Fatalf("lead_submissions = %+v", ls)
	}
	// 分母:唯一联系人指纹去重
	if ls.UniqueContacts == nil || *ls.UniqueContacts != 2 {
		t.Fatalf("unique_contacts denominator = %+v", ls.UniqueContacts)
	}

	cr := byKey[KeyCRMReceived]
	if !cr.Available || cr.Value == nil || *cr.Value != 1 {
		t.Fatalf("crm_received = %+v", cr)
	}

	// facts 变化不得让 UNKNOWN 变成 available/0:发布/UGC/POI/核销恒为 UNKNOWN。
	for _, key := range []string{KeyPublishedVideos, KeyUGCPlays, KeyUGCLikes, KeyPOIExposureDelta, KeyCouponRedemptions} {
		m := byKey[key]
		if m.Available || m.Value != nil {
			t.Fatalf("%s must stay UNKNOWN regardless of facts: %+v", key, m)
		}
	}
}

// 空事实集:可用指标是真 0(来源表存在、聚合为空),不是 UNKNOWN。
func TestBuildZeroIsTrueZeroForTouchMetrics(t *testing.T) {
	out := Build(testWindow(), Scope{TenantID: "tnt_a"}, store.DashboardFacts{}, store.DashboardDimensions{})
	for _, m := range out.Metrics {
		switch m.Key {
		case KeyTouchTriggers, KeyLeadPageVisits, KeyLeadSubmissions, KeyCRMReceived:
			if !m.Available || m.Value == nil || *m.Value != 0 {
				t.Fatalf("%s with empty facts = %+v, want available true 0 (a true zero)", m.Key, m)
			}
		}
	}
}

// 同一事实集两次 Build:逐字段一致(同窗口重算幂等)。
func TestBuildIdempotent(t *testing.T) {
	facts := store.DashboardFacts{TriggerTotal: 7, SubmissionTotal: 2, SubmissionUniqueContacts: 1}
	a := Build(testWindow(), Scope{TenantID: "tnt_a"}, facts, store.DashboardDimensions{})
	b := Build(testWindow(), Scope{TenantID: "tnt_a"}, facts, store.DashboardDimensions{})
	if len(a.Metrics) != len(b.Metrics) {
		t.Fatal("metric count differs between recomputes")
	}
	for i := range a.Metrics {
		if a.Metrics[i].Key != b.Metrics[i].Key ||
			a.Metrics[i].Available != b.Metrics[i].Available ||
			!samePtr(a.Metrics[i].Value, b.Metrics[i].Value) ||
			!samePtr(a.Metrics[i].UniqueContacts, b.Metrics[i].UniqueContacts) {
			t.Fatalf("recompute mismatch at %s", a.Metrics[i].Key)
		}
	}
}

func samePtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
