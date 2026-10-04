package storemotion

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func sample(price string) Request {
	return Request{
		TenantID:   "tnt_a",
		StoreID:    "sto_a",
		ActivityID: "cmp_a",
		Params: Params{
			StoreName:    "南山店",
			ActivityTime: "10月1日-10月7日",
			Price:        price,
			Address:      "南山大道1号",
			OfferCopy:    "第二杯半价",
			CTA:          "进店领取",
		},
	}
}

func TestApplyDeclaresUnverifiedTouchRef(t *testing.T) {
	probe := &Probe{}
	got, err := Apply(sample("19.9"), probe)
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginApp != "touch" {
		t.Fatalf("origin_app = %q", got.OriginApp)
	}
	wantRef := "touch:tenant/tnt_a/store/sto_a/activity/cmp_a"
	if got.OriginContextRef != wantRef {
		t.Fatalf("origin_context_ref = %q", got.OriginContextRef)
	}
	if !strings.Contains(got.OriginContextRef, "sto_a") || !strings.Contains(got.OriginContextRef, "cmp_a") {
		t.Fatalf("context ref does not point at the store activity: %s", got.OriginContextRef)
	}
	if got.RevisionID != nil {
		t.Fatalf("revision = %v, want none", *got.RevisionID)
	}
	if got.Verified {
		t.Fatal("declaration is verified")
	}
	if got.Status != "还没生成成片" {
		t.Fatalf("status = %q", got.Status)
	}
	if got.ModelCalls != 0 || got.RenderCalls != 0 {
		t.Fatalf("calls model=%d render=%d", got.ModelCalls, got.RenderCalls)
	}
	if got.UnchangedStoreRerun != RerunNotClaimed || !strings.Contains(got.UnchangedStoreNote, "HUI-2732") || !strings.Contains(got.UnchangedStoreNote, "不能声称") {
		t.Fatalf("rerun claim = %q %q", got.UnchangedStoreRerun, got.UnchangedStoreNote)
	}
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 || len(probe.Payloads) != 0 {
		t.Fatalf("probe model=%d render=%d payloads=%d", probe.ModelCalls, probe.RenderCalls, len(probe.Payloads))
	}
}

func TestParamChangeDoesNotCallModelOrRender(t *testing.T) {
	probe := &Probe{}
	first, err := Apply(sample("19.9"), probe)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Apply(sample("29.9"), probe)
	if err != nil {
		t.Fatal(err)
	}
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 || len(probe.Payloads) != 0 {
		t.Fatalf("model calls = %d, render calls = %d, payloads = %d", probe.ModelCalls, probe.RenderCalls, len(probe.Payloads))
	}
	if first.ModelCalls != 0 || second.ModelCalls != 0 || first.RenderCalls != 0 || second.RenderCalls != 0 {
		t.Fatal("declaration counted a call")
	}
	if first.Status != StatusNotRendered || second.Status != StatusNotRendered {
		t.Fatal("status changed into a finished render")
	}
	if first.RevisionID != nil || second.RevisionID != nil {
		t.Fatal("a parameter change invented a revision")
	}
}

func TestProbeCountsOnlyWhenCalled(t *testing.T) {
	probe := &Probe{}
	probe.CallModel([]byte("not-sent"))
	probe.CallRender([]byte("not-sent"))
	if probe.ModelCalls != 1 || probe.RenderCalls != 1 || len(probe.Payloads) != 2 {
		t.Fatalf("probe did not count direct calls: %+v", probe)
	}
}

func TestPrivacyNeverBecomesModelPayload(t *testing.T) {
	probe := &Probe{}
	cases := []Params{
		{StoreName: "南山店", ActivityTime: "10月1日", Price: "19.9", Address: "南山大道1号", OfferCopy: "第二杯半价", CTA: "联系 13800138000"},
		{StoreName: "南山店", ActivityTime: "10月1日", Price: "19.9", Address: "南山大道1号", OfferCopy: "扫码 data:image/png;base64,aaaa", CTA: "进店"},
		{StoreName: "南山店", ActivityTime: "10月1日", Price: "19.9", Address: "南山大道1号", OfferCopy: "第二杯半价", CTA: "weixin://wxpay/bizpayurl?pr=abc"},
		{StoreName: "南山店", ActivityTime: "10月1日", Price: "19.9", Address: "南山大道1号", OfferCopy: "写给 a@example.com", CTA: "进店"},
	}
	for _, p := range cases {
		req := sample("19.9")
		req.Params = p
		_, err := Apply(req, probe)
		if !errors.Is(err, ErrPrivacy) {
			t.Fatalf("params %+v err = %v, want privacy", p, err)
		}
	}
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 || len(probe.Payloads) != 0 {
		t.Fatalf("privacy reached the model: calls=%d payloads=%d", probe.ModelCalls, len(probe.Payloads))
	}
}

func TestUnchangedStoreRerunIsNotClaimed(t *testing.T) {
	probe := &Probe{}
	a := sample("19.9")
	b := sample("19.9")
	b.StoreID = "sto_b"
	b.ActivityID = "cmp_b"
	b.Params.StoreName = "福田店"
	declA, err := Apply(a, probe)
	if err != nil {
		t.Fatal(err)
	}
	declB, err := Apply(b, probe)
	if err != nil {
		t.Fatal(err)
	}
	a.Params.Price = "8元"
	again, err := Apply(a, probe)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []Declaration{declA, declB, again} {
		if d.UnchangedStoreRerun != "not_claimed" {
			t.Fatalf("rerun = %q, want not_claimed", d.UnchangedStoreRerun)
		}
		if d.UnchangedStoreRerun == "skipped" || strings.Contains(d.UnchangedStoreRerun, "没有重跑") {
			t.Fatalf("pretended an unchanged store skipped rerun: %q", d.UnchangedStoreRerun)
		}
	}
	if !strings.Contains(declB.OriginContextRef, "sto_b") || strings.Contains(again.OriginContextRef, "sto_b") {
		t.Fatal("context refs crossed stores")
	}
	if probe.ModelCalls != 0 || probe.RenderCalls != 0 {
		t.Fatal("cross-store parameter edit called a model")
	}
}

func TestPriceStaysAString(t *testing.T) {
	typ := reflect.TypeOf(Params{})
	if typ.NumField() != 6 {
		t.Fatalf("fields = %d, want the six merchant parameters", typ.NumField())
	}
	want := []string{"StoreName", "ActivityTime", "Price", "Address", "OfferCopy", "CTA"}
	for i, name := range want {
		f := typ.Field(i)
		if f.Name != name {
			t.Fatalf("field %d = %s, want %s", i, f.Name, name)
		}
		if f.Type.Kind() != reflect.String {
			t.Fatalf("%s kind = %s, want string", f.Name, f.Type.Kind())
		}
		lower := strings.ToLower(f.Name)
		if strings.Contains(lower, "cent") || strings.Contains(lower, "amount") || strings.Contains(lower, "settle") {
			t.Fatalf("settlement field %s", f.Name)
		}
	}
	got, err := Normalize(sample("到店询价"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Price != "到店询价" {
		t.Fatalf("price = %q", got.Price)
	}
}

func TestPackageDoesNotImportAModelOrQR(t *testing.T) {
	src, err := os.ReadFile("storemotion.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"net/http", "go-qrcode", "image/", "openai", "billing"} {
		if strings.Contains(string(src), bad) {
			t.Fatalf("storemotion.go imports %s", bad)
		}
	}
}
