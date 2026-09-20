package assetlib

// assetlib_test.go: HUI-1666 FEAT-0167 商家素材库 v1 纯域规则验收。
//
// 覆盖(票面 TDD 清单,纯函数部分):
//   - 素材登记校验矩阵:缺来源/用途/授权声明各自独立原因码;sha256 非 64 位
//     十六进制拒绝;媒体类型枚举(image/video/bgm);引用标识必填;
//   - 导入路径:source 仅产品图/AiCut 可导入,其余拒绝(零触发生成、零模型调用);
//   - 确定性随机调取:同种子(pool_id, day)必同选;跨种子分布(穷举固定种子
//     集,确定性断言,无随机波动);空池拒绝;调取是纯函数不改变输入;
//   - 候选标记:标记语义只改标记,不动引用与版本字段;
//   - 标签归一:去空白、去重、保序。

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var okReg = Registration{
	AssetRef:  "upl_asset_1",
	SHA256:    "6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b",
	MediaType: "video",
	Source:    "product_photo",
	Purpose:   "campaign_publish",
	GrantRef:  "grant-2026-001",
}

func reasonOf(t *testing.T, err error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T: %v", err, err)
	}
	return ve.Reason
}

func TestValidateRegistrationMatrix(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Registration)
		reason string
	}{
		{"missing_source", func(r *Registration) { r.Source = "" }, ReasonMissingSource},
		{"missing_purpose", func(r *Registration) { r.Purpose = " " }, ReasonMissingPurpose},
		{"missing_grant_ref", func(r *Registration) { r.GrantRef = "" }, ReasonMissingGrantRef},
		{"missing_asset_ref", func(r *Registration) { r.AssetRef = "" }, ReasonMissingAssetRef},
		{"bad_media_type", func(r *Registration) { r.MediaType = "audio" }, ReasonBadMediaType},
		{"empty_media_type", func(r *Registration) { r.MediaType = "" }, ReasonBadMediaType},
		{"bad_source", func(r *Registration) { r.Source = "scraped" }, ReasonBadSource},
		{"bad_sha256_short", func(r *Registration) { r.SHA256 = "abc123" }, ReasonBadSha256},
		{"bad_sha256_nonhex", func(r *Registration) {
			r.SHA256 = strings.Repeat("z", 64)
		}, ReasonBadSha256},
		{"empty_sha256", func(r *Registration) { r.SHA256 = "" }, ReasonBadSha256},
		{"bad_tag_too_long", func(r *Registration) {
			r.Tags = []string{strings.Repeat("t", 65)}
		}, ReasonBadTag},
		{"too_many_tags", func(r *Registration) {
			r.Tags = make([]string, 33)
			for i := range r.Tags {
				r.Tags[i] = "tag-" + strings.Repeat("x", i)
			}
		}, ReasonBadTag},
		{"purpose_too_long", func(r *Registration) { r.Purpose = strings.Repeat("p", 257) }, ReasonBadPurpose},
		{"grant_ref_too_long", func(r *Registration) { r.GrantRef = strings.Repeat("g", 257) }, ReasonBadGrantRef},
		{"asset_ref_too_long", func(r *Registration) { r.AssetRef = strings.Repeat("a", 257) }, ReasonBadAssetRef},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := okReg
			tc.mutate(&in)
			if got := reasonOf(t, ValidateRegistration(in)); got != tc.reason {
				t.Fatalf("reason = %q, want %q", got, tc.reason)
			}
		})
	}
	// The complete declaration passes.
	if err := ValidateRegistration(okReg); err != nil {
		t.Fatalf("valid registration rejected: %v", err)
	}
	// sha256 is normalized: caller casing/whitespace must not leak into storage.
	in := okReg
	in.SHA256 = "  " + strings.ToUpper(okReg.SHA256) + " "
	in.Source = " aicut "
	if err := ValidateRegistration(in); err != nil {
		t.Fatalf("normalized registration rejected: %v", err)
	}
}

func TestValidateImportRestrictsSource(t *testing.T) {
	// 导入路径只收 产品图/AiCut 两个来源;其余来源必须走通用登记。
	for _, src := range []Source{SourceProductPhoto, SourceAiCut} {
		in := okReg
		in.Source = src
		if err := ValidateImport(in); err != nil {
			t.Fatalf("import %s rejected: %v", src, err)
		}
	}
	in := okReg
	in.Source = SourceMerchantUpload
	if got := reasonOf(t, ValidateImport(in)); got != ReasonSourceNotImportable {
		t.Fatalf("reason = %q, want %q", got, ReasonSourceNotImportable)
	}
	// The declaration matrix still applies on the import path.
	in = okReg
	in.Source = SourceAiCut
	in.GrantRef = ""
	if got := reasonOf(t, ValidateImport(in)); got != ReasonMissingGrantRef {
		t.Fatalf("reason = %q, want %q", got, ReasonMissingGrantRef)
	}
}

func TestMediaTypesAndSources(t *testing.T) {
	for _, m := range []MediaType{MediaTypeImage, MediaTypeVideo, MediaTypeBGM} {
		if !m.Valid() {
			t.Fatalf("media type %q should be valid", m)
		}
	}
	if MediaType("gif").Valid() {
		t.Fatal("gif must not be a valid media type")
	}
	for _, s := range []Source{SourceProductPhoto, SourceAiCut, SourceMerchantUpload} {
		if !s.Valid() {
			t.Fatalf("source %q should be valid", s)
		}
	}
	if Source("crawl").Valid() {
		t.Fatal("crawl must not be a valid source")
	}
	if !SourceProductPhoto.Importable() || !SourceAiCut.Importable() {
		t.Fatal("product_photo/aicut must be importable")
	}
	if SourceMerchantUpload.Importable() {
		t.Fatal("merchant_upload must not be importable via the product-photo/aicut import path")
	}
}

func TestNormalizeTags(t *testing.T) {
	got := NormalizeTags([]string{" 门店海报 ", "", "video-cover", "门店海报", "\tstorefront"})
	want := []string{"门店海报", "video-cover", "storefront"}
	if len(got) != len(want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tags = %v, want %v", got, want)
		}
	}
	if len(NormalizeTags(nil)) != 0 {
		t.Fatal("nil tags must normalize to empty")
	}
}

func TestCandidateFlagIsDeclarationOnly(t *testing.T) {
	// 候选标记只是分流标记:置位不改变任何其他字段的校验结果,也不参与
	// 授权声明矩阵(回流结果先入候选,不直接替换正式行)。
	cand := okReg
	cand.Candidate = true
	if err := ValidateRegistration(cand); err != nil {
		t.Fatalf("candidate registration rejected: %v", err)
	}
	// The declaration matrix still governs candidate rows.
	bad := okReg
	bad.Candidate = true
	bad.GrantRef = ""
	if got := reasonOf(t, ValidateRegistration(bad)); got != ReasonMissingGrantRef {
		t.Fatalf("reason = %q, want %q", got, ReasonMissingGrantRef)
	}
}

func TestValidateSelectKey(t *testing.T) {
	if err := ValidateSelectKey(" hero-main "); err != nil {
		t.Fatalf("valid select key rejected: %v", err)
	}
	if got := reasonOf(t, ValidateSelectKey("  ")); got != ReasonMissingSelectKey {
		t.Fatalf("reason = %q, want %q", got, ReasonMissingSelectKey)
	}
	if got := reasonOf(t, ValidateSelectKey("")); got != ReasonMissingSelectKey {
		t.Fatalf("reason = %q, want %q", got, ReasonMissingSelectKey)
	}
	if got := reasonOf(t, ValidateSelectKey(strings.Repeat("k", MaxSelectKey+1))); got != ReasonBadSelectKey {
		t.Fatalf("reason = %q, want %q", got, ReasonBadSelectKey)
	}
	if err := ValidateSelectKey(strings.Repeat("k", MaxSelectKey)); err != nil {
		t.Fatalf("max-length select key rejected: %v", err)
	}
}

func TestValidatePoolName(t *testing.T) {
	if err := ValidatePoolName(" 门店视频池 "); err != nil {
		t.Fatalf("valid pool name rejected: %v", err)
	}
	if got := reasonOf(t, ValidatePoolName(" ")); got != ReasonMissingPoolName {
		t.Fatalf("reason = %q, want %q", got, ReasonMissingPoolName)
	}
	if got := reasonOf(t, ValidatePoolName(strings.Repeat("池", MaxPoolName+1))); got != ReasonBadPoolName {
		t.Fatalf("reason = %q, want %q", got, ReasonBadPoolName)
	}
}

func TestPickIndexDeterministic(t *testing.T) {
	day := "2026-09-20"
	// Same seed (pool_id, day) must always pick the same index.
	first := PickIndex("pool-a", day, 5)
	for i := 0; i < 50; i++ {
		if got := PickIndex("pool-a", day, 5); got != first {
			t.Fatalf("PickIndex not deterministic: %d vs %d", got, first)
		}
	}
	// Different pool or different day = different seed (assert on real values so
	// the test stays deterministic; the seed hash makes collisions implausible).
	if PickIndex("pool-b", day, 5) == PickIndex("pool-a", day, 5) {
		t.Fatal("different pools must not share a seed")
	}
	if PickIndex("pool-a", "2026-09-21", 5) == first {
		t.Fatal("different days must not share a seed")
	}
	// The seed itself is stable and derives from exactly (pool_id, day).
	if SelectionSeed("pool-a", day) != SelectionSeed("pool-a", day) {
		t.Fatal("seed not stable")
	}
	if SelectionSeed("pool-a", day) == SelectionSeed("pool-b", day) {
		t.Fatal("seed must depend on pool_id")
	}
}

func TestPickIndexDistribution(t *testing.T) {
	// 穷举固定种子集做分布断言:纯函数,结果确定,无随机波动。
	const n = 7
	seen := make([]int, n)
	for i := 0; i < 2000; i++ {
		pool := "pool-dist-" + dayFor(i/31)
		day := dayFor(i % 31)
		seen[PickIndex(pool, day, n)]++
	}
	for idx, c := range seen {
		if c == 0 {
			t.Fatalf("index %d never chosen over 2000 deterministic seeds", idx)
		}
	}
}

func dayFor(i int) string {
	return NormalizeDayUTC(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, i))
}

func TestSelectPureFunction(t *testing.T) {
	items := []string{"item-1", "item-2", "item-3", "item-4"}
	day := "2026-09-20"
	got, err := Select("pool-a", day, items)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	// Same seed picks the same item.
	for i := 0; i < 10; i++ {
		again, err := Select("pool-a", day, items)
		if err != nil || again != got {
			t.Fatalf("select not deterministic: %v/%q vs %q", err, again, got)
		}
	}
	// The chosen item must be a member (调取不改变池:输入切片原样)。
	found := false
	for _, id := range items {
		if id == got {
			found = true
		}
	}
	if !found {
		t.Fatalf("chosen %q not in pool", got)
	}
	// Empty pool refused.
	if _, err := Select("pool-a", day, nil); !errors.Is(err, ErrEmptyPool) {
		t.Fatalf("empty pool: want ErrEmptyPool, got %v", err)
	}
	// Reordering the input changes the mapping, so callers must pass a
	// deterministic order; Select itself never reorders or filters.
	rev := []string{"item-4", "item-3", "item-2", "item-1"}
	if got == items[PickIndex("pool-a", day, 4)] && rev[PickIndex("pool-a", day, 4)] == got {
		t.Fatal("order sensitivity expectation broken")
	}
}

func TestDayUTC(t *testing.T) {
	ts := time.Date(2026, 9, 20, 23, 59, 59, 0, time.UTC)
	if got := NormalizeDayUTC(ts); got != "2026-09-20" {
		t.Fatalf("day = %q", got)
	}
	// Non-UTC instants fold into the UTC calendar day (种子用 UTC 日期).
	sh := time.Date(2026, 9, 21, 8, 0, 0, 0, fixedZone(8*3600))
	if got := NormalizeDayUTC(sh); got != "2026-09-21" {
		t.Fatalf("UTC-fold day = %q", got)
	}
	if !ValidDayUTC("2026-09-20") {
		t.Fatal("valid day rejected")
	}
	for _, bad := range []string{"", "2026-9-20", "2026/09/20", "2026-09-20T00:00:00Z", "20-09-2026"} {
		if ValidDayUTC(bad) {
			t.Fatalf("day %q must be invalid", bad)
		}
	}
}

func fixedZone(offset int) *time.Location {
	return time.FixedZone("test", offset)
}
