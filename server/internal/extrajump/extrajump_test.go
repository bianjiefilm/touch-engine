package extrajump

import (
	"testing"
	"time"
)

// 会让这些测试失败的生产改动：把未配置、.invalid 或本机地址展示成可点动作，
// 或把一次点击记成添加成功、关注成功、留资、进 CRM、发奖或发布成功。

func TestOnlyConfiguredOfficialAddressesAreShown(t *testing.T) {
	shown, closed := Present([]Configured{
		{Kind: KindWecom, Enabled: true, Href: "https://work.weixin.qq.com/ca/demo"},
		{Kind: KindFollow, Enabled: true, Href: "https://example.invalid/follow"},
		{Kind: KindNavigate, Enabled: false, Href: "https://uri.amap.com/marker?position=120,30"},
		{Kind: KindReview, Enabled: true, Href: "https://127.0.0.1/review"},
		{Kind: KindWifi, Enabled: true, Href: "http://shop.example.com/wifi"},
	})
	if len(shown) != 1 || shown[0].Kind != KindWecom || shown[0].Href != "https://work.weixin.qq.com/ca/demo" || !shown[0].Available || shown[0].Result != "ready" {
		t.Fatalf("shown = %+v", shown)
	}
	wantClosed := map[Kind]string{
		KindFollow:   ReasonAddressNotOfficial,
		KindNavigate: ReasonNotConfigured,
		KindReview:   ReasonAddressNotOfficial,
		KindWifi:     ReasonAddressNotOfficial,
	}
	if len(closed) != len(wantClosed) {
		t.Fatalf("closed = %+v", closed)
	}
	for _, item := range closed {
		reason, ok := wantClosed[item.Kind]
		if !ok || item.Reason != reason || item.Shown || item.Href != "" {
			t.Fatalf("closed item = %+v", item)
		}
	}
}

func TestMissingKindsStayClosedWithoutInventingTargets(t *testing.T) {
	shown, closed := Present(nil)
	if len(shown) != 0 {
		t.Fatalf("empty config showed %+v", shown)
	}
	if len(closed) != 5 {
		t.Fatalf("closed = %+v", closed)
	}
	for _, item := range closed {
		if item.Reason != ReasonNotConfigured || item.Shown || item.Href != "" {
			t.Fatalf("missing kind = %+v", item)
		}
	}
}

func TestClickStaysUnknownAndDoesNotSucceed(t *testing.T) {
	for _, kind := range []Kind{KindWifi, KindNavigate, KindReview, KindWecom, KindFollow} {
		got := RecordClick(kind)
		if got.Kind != kind || got.RecordedAs != "click" || got.Success || got.PlatformResult != "unknown" ||
			got.Added || got.Followed || got.LeadCreated || got.CRMImported || got.RewardTriggered || got.PublishSuccess {
			t.Fatalf("%s click = %+v", kind, got)
		}
	}
}

func TestPrivateAndPlaceholderHostsAreNotOfficial(t *testing.T) {
	for _, href := range []string{
		"",
		"https://example.invalid/go",
		"https://localhost/go",
		"https://127.0.0.1/go",
		"https://[::1]/go",
		"https://app.local/go",
		"https://192.168.1.8/go",
		"https://10.1.2.3/go",
		"https://172.16.0.4/go",
		"http://work.weixin.qq.com/ca/demo",
		"javascript:alert(1)",
		"/c/abc",
	} {
		if OfficialAddress(href) {
			t.Fatalf("accepted %q", href)
		}
	}
	if !OfficialAddress("https://work.weixin.qq.com/ca/demo") {
		t.Fatal("rejected a public https address")
	}
}

func TestRevokedAndExpiredJumpsStayClosedWithoutHref(t *testing.T) {
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	shown, closed := PresentAt([]Configured{
		{Kind: KindWifi, Enabled: true, Href: "https://shop.example.com/wifi", ExpiresAt: "2099-01-01T00:00:00Z"},
		{Kind: KindNavigate, Enabled: true, Href: "https://uri.amap.com/marker", ExpiresAt: "2020-01-01T00:00:00Z"},
		{Kind: KindReview, Enabled: true, Href: "https://shop.example.com/review", Revoked: true},
		{Kind: KindFollow, Enabled: true, Href: "not a url", ExpiresAt: "yesterday"},
	}, now)
	if len(shown) != 1 || shown[0].Kind != KindWifi || shown[0].Href != "https://shop.example.com/wifi" || shown[0].Class != ClassStore {
		t.Fatalf("shown = %+v", shown)
	}
	if shown[0].Web != EvidenceWebConfigured || shown[0].ClientLaunch != EvidenceClientUnverified || shown[0].PlatformAction != EvidencePlatformUnknown {
		t.Fatalf("wifi evidence = %+v", shown[0])
	}
	want := map[Kind]string{
		KindNavigate: ReasonExpired,
		KindReview:   ReasonRevoked,
		KindFollow:   ReasonExpired,
		KindWecom:    ReasonNotConfigured,
	}
	if len(closed) != len(want) {
		t.Fatalf("closed = %+v", closed)
	}
	for _, item := range closed {
		if item.Href != "" || item.Shown || item.Reason != want[item.Kind] {
			t.Fatalf("closed item = %+v", item)
		}
	}
}

func TestArbitraryExternalReturnIsRejected(t *testing.T) {
	if AllowReturn("https://evil.example/go", []string{"h5.example.com"}) {
		t.Fatal("accepted an unregistered external return")
	}
	if !AllowReturn("/c/back", nil) {
		t.Fatal("rejected an in-site return")
	}
	if !AllowReturn("/c/5APBG3F8Z9P1", nil) {
		t.Fatal("published short codes are uppercase and must be registrable returns")
	}
	if AllowReturn("/admin", nil) || AllowReturn("/Admin", nil) || AllowReturn("/api/secret", nil) || AllowReturn("/API/secret", nil) || AllowReturn("//evil.example", nil) {
		t.Fatal("accepted a return into the admin or off site")
	}
	if AllowReturn("https://h5.example.com/c/back?next=https://evil.example", []string{"h5.example.com"}) {
		t.Fatal("accepted a return with a query")
	}
	if !AllowReturn("https://h5.example.com/c/back", []string{"h5.example.com"}) {
		t.Fatal("rejected a registered host")
	}
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	rejected := DecideReturn(ReturnConfig{Enabled: true, Href: "https://evil.example/go"}, now, []string{"h5.example.com"})
	if rejected.Shown || rejected.Href != "" || rejected.Reason != ReasonAddressNotOfficial || rejected.ClientLaunch != EvidenceClientUnverified || rejected.PlatformAction != EvidencePlatformUnknown {
		t.Fatalf("rejected return = %+v", rejected)
	}
	ok := DecideReturn(ReturnConfig{Enabled: true, Href: "/c/back"}, now, nil)
	if !ok.Shown || ok.Href != "/c/back" || ok.Class != ClassReturn || ok.Web != EvidenceWebConfigured {
		t.Fatalf("in-site return = %+v", ok)
	}
	revoked := DecideReturn(ReturnConfig{Enabled: true, Href: "/c/back", Revoked: true}, now, nil)
	if revoked.Shown || revoked.Href != "" || revoked.Reason != ReasonRevoked {
		t.Fatalf("revoked return = %+v", revoked)
	}
	expired := DecideReturn(ReturnConfig{Enabled: true, Href: "/c/back", ExpiresAt: "2020-01-01T00:00:00Z"}, now, nil)
	if expired.Shown || expired.Href != "" || expired.Reason != ReasonExpired {
		t.Fatalf("expired return = %+v", expired)
	}
}

func TestCanonicalHrefIgnoresCallerSuppliedURL(t *testing.T) {
	shown := []Shown{{Kind: KindWifi, Href: "https://shop.example.com/wifi", Available: true}}
	got, ok := CanonicalHref(shown, KindWifi, "https://evil.example/phish")
	if !ok || got != "https://shop.example.com/wifi" {
		t.Fatalf("canonical = %q ok=%v", got, ok)
	}
	if _, ok := CanonicalHref(shown, KindNavigate, "https://shop.example.com/wifi"); ok {
		t.Fatal("missing kind became a jump")
	}
}

func TestSupportMatrixKeepsNativeLaunchUnverified(t *testing.T) {
	rows := SupportMatrix()
	if len(rows) != 6 {
		t.Fatalf("rows = %+v", rows)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.Kind] = true
		if row.ClientLaunch != EvidenceClientUnverified || row.PlatformAction != EvidencePlatformUnknown || row.Web != "page_only" {
			t.Fatalf("evidence collapsed: %+v", row)
		}
		if row.AutoFollow || row.AutoJoin || row.AutoPay || !containsText(row.NativeReason, "原生唤起未验证") {
			t.Fatalf("automation or missing native note: %+v", row)
		}
	}
	for _, kind := range []string{"wifi", "navigate", "review", "follow", "wecom", "return"} {
		if !seen[kind] {
			t.Fatalf("missing %s in %v", kind, seen)
		}
	}
}

func TestBlockReasonSeparatesCrossBrandFromUnauthorized(t *testing.T) {
	if reason, blocked := BlockReason(false, "domain_mismatch"); blocked || reason != "" {
		t.Fatalf("brand off blocked: %s %v", reason, blocked)
	}
	if reason, blocked := BlockReason(true, "available"); blocked || reason != "" {
		t.Fatalf("available blocked: %s %v", reason, blocked)
	}
	if reason, blocked := BlockReason(true, "domain_mismatch"); !blocked || reason != ReasonCrossBrand {
		t.Fatalf("mismatch = %s %v", reason, blocked)
	}
	if reason, blocked := BlockReason(true, "unknown_brand"); !blocked || reason != ReasonCrossBrand {
		t.Fatalf("unknown brand = %s %v", reason, blocked)
	}
	if reason, blocked := BlockReason(true, "tenant_suspended"); !blocked || reason != ReasonUnauthorized {
		t.Fatalf("suspended = %s %v", reason, blocked)
	}
}

func TestRecordClickKeepsThreeEvidenceChannelsApart(t *testing.T) {
	got := RecordClick(KindReview)
	if got.Class != ClassStore || got.Success || got.PlatformResult != "unknown" || got.Web != EvidenceWebConfigured ||
		got.ClientLaunch != EvidenceClientUnverified || got.PlatformAction != EvidencePlatformUnknown ||
		got.AutoFollow || got.AutoJoin || got.AutoPay || got.Added || got.Followed {
		t.Fatalf("click = %+v", got)
	}
	if RecordClick(KindFollow).Class != ClassActivity {
		t.Fatal("follow is not an activity target")
	}
}

func containsText(s, part string) bool {
	return len(s) >= len(part) && (s == part || len(part) == 0 || (len(s) > 0 && (func() bool {
		for i := 0; i+len(part) <= len(s); i++ {
			if s[i:i+len(part)] == part {
				return true
			}
		}
		return false
	})()))
}
