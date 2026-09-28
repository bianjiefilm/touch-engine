package privatedomain

import "testing"

// 会让这些测试失败的生产改动：把非企微地址、未配置社群或当前渠道打不开的入口展示出来，
// 或把 click_wecom 记成添加成功、进群成功、新联系人、私域已接通或优惠券核销。

func TestOnlyConfiguredChannelCapableEntriesAreShown(t *testing.T) {
	shown, closed := Present("web", []Configured{
		{Kind: KindWecom, Enabled: true, Href: "https://work.weixin.qq.com/ca/demo"},
		{Kind: KindCommunity, Enabled: true, Href: "https://shop.example.com/group"},
		{Kind: KindWecom, Enabled: true, Href: "https://example.invalid/wecom"},
	})
	if len(shown) != 1 || shown[0].Kind != KindWecom || shown[0].Href != "https://work.weixin.qq.com/ca/demo" || shown[0].Event != EventClickWecom {
		t.Fatalf("shown = %+v", shown)
	}
	reasons := map[Kind]string{}
	for _, item := range closed {
		reasons[item.Kind] = item.Reason
		if item.Shown || item.Href != "" || item.Connected {
			t.Fatalf("closed leaked a target: %+v", item)
		}
	}
	if reasons[KindCommunity] != ReasonAddressNotWecom {
		t.Fatalf("community reason = %v", reasons)
	}
}

func TestCommunityUsesGroupLinkAndWecomUsesContactLink(t *testing.T) {
	shown, _ := Present("qr", []Configured{
		{Kind: KindWecom, Enabled: true, Href: "https://work.weixin.qq.com/gm/group-demo"},
		{Kind: KindCommunity, Enabled: true, Href: "https://work.weixin.qq.com/ca/contact-demo"},
	})
	if len(shown) != 0 {
		t.Fatalf("swapped links shown = %+v", shown)
	}
	shown, _ = Present("nfc", []Configured{
		{Kind: KindWecom, Enabled: true, Href: "https://work.weixin.qq.com/ca/contact-demo"},
		{Kind: KindCommunity, Enabled: true, Href: "https://work.weixin.qq.com/gm/group-demo"},
	})
	if len(shown) != 2 || shown[0].Kind != KindWecom || shown[1].Kind != KindCommunity || shown[1].Event != EventClickCommunity {
		t.Fatalf("matched links = %+v", shown)
	}
}

func TestUnknownChannelHidesEntries(t *testing.T) {
	shown, closed := Present("miniprogram", []Configured{
		{Kind: KindWecom, Enabled: true, Href: "https://work.weixin.qq.com/ca/demo"},
		{Kind: KindCommunity, Enabled: true, Href: "https://work.weixin.qq.com/gm/demo"},
	})
	if len(shown) != 0 || len(closed) != 2 {
		t.Fatalf("unknown channel shown=%+v closed=%+v", shown, closed)
	}
	for _, item := range closed {
		if item.Reason != ReasonChannelCannotOpen || item.Shown || item.Connected {
			t.Fatalf("closed = %+v", item)
		}
	}
}

func TestMissingConfigIsNotConnected(t *testing.T) {
	shown, closed := Present("web", nil)
	if len(shown) != 0 {
		t.Fatalf("empty config showed %+v", shown)
	}
	if len(closed) != 2 {
		t.Fatalf("closed = %+v", closed)
	}
	for _, item := range closed {
		if item.Reason != ReasonNotConfigured || item.Connected || item.Href != "" {
			t.Fatalf("missing = %+v", item)
		}
	}
	guide := Guide{}
	if guide.Connected || (guide.Redemption != "" && guide.Redemption != RedemptionUnknown) {
		t.Fatal("zero guide must not look connected")
	}
}

func TestClickWecomStaysUnknown(t *testing.T) {
	got := RecordClick(KindWecom)
	if got.Event != EventClickWecom || got.RecordedAs != "click" || got.Success || got.PlatformResult != "unknown" ||
		got.Added || got.Joined || got.ContactCreated || got.LeadCreated || got.CRMImported || got.RewardTriggered ||
		got.Connected || got.Redemption != RedemptionUnknown || got.Followed || got.SilentAdd || got.ForcedJoin || got.BackgroundMarketing {
		t.Fatalf("wecom click = %+v", got)
	}
	community := RecordClick(KindCommunity)
	if community.Event != EventClickCommunity || community.Joined || community.Success || community.Redemption != RedemptionUnknown {
		t.Fatalf("community click = %+v", community)
	}
}
