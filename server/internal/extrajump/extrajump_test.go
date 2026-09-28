package extrajump

import "testing"

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
