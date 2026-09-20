package store

// store_asset_lib_test.go: HUI-1666 FEAT-0167 商家素材库持久层验收。
//
// 覆盖(票面 TDD 清单,持久层部分):
//   - 素材登记唯一(同租户 asset_ref+sha256+candidate 唯一);正式行与回流
//     候选行可并存;门店归属可空、绑定门店必须真实存在;
//   - 候选标记语义:置位只改标记,不动引用与版本字段;
//   - 池 CRUD 与租户内名称唯一;池内引用增删与 (pool_id, asset_id) 唯一;
//     删池不删选择台账(台账独立于池生命周期);
//   - 确定性调取:同 (pool, day) 必同选;同键重复选择幂等(先查台账后取池,
//     池变更后重放仍返回冻结行);空池拒绝;调取不改变池;
//   - 选择台账只追加+版本冻结:素材登记新版本(同 ref 新 sha)后,已选行的
//     asset_id/asset_ref/sha256 快照不变;
//   - 重登恢复:关库重开后引用/池/台账完整;
//   - 跨租户一律 ErrNotFound / 0 行(绝不泄露存在性)。

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/assetlib"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func openLibStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

// openLibStoreAt is openLibStore at a caller-chosen path (reopen tests).
func openLibStoreAt(t *testing.T, path string) *Store {
	t.Helper()
	d, err := db.Open(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func libTenants(t *testing.T, s *Store) (tenantA, tenantB string) {
	t.Helper()
	ta, err := s.CreateTenant("素材商家A")
	must(t, err)
	tb, err := s.CreateTenant("素材商家B")
	must(t, err)
	return ta.ID, tb.ID
}

func libReg(ref, sha string, mt assetlib.MediaType) assetlib.Registration {
	return assetlib.Registration{
		AssetRef:  ref,
		SHA256:    sha,
		MediaType: mt,
		Source:    assetlib.SourceAiCut,
		Purpose:   "campaign_publish",
		GrantRef:  "grant-2026-001",
	}
}

func mustCreateAsset(t *testing.T, s *Store, tenantID, ref, sha string, mt assetlib.MediaType) LibAsset {
	t.Helper()
	a, err := s.CreateLibAsset(tenantID, libReg(ref, sha, mt), "", "seed")
	if err != nil {
		t.Fatalf("create asset %s: %v", ref, err)
	}
	return a
}

func TestLibAssetRegistrationAndUniqueness(t *testing.T) {
	s := openLibStore(t)
	tenA, tenB := libTenants(t, s)

	a := mustCreateAsset(t, s, tenA, "upl_a1", shaHex(1), assetlib.MediaTypeVideo)
	if a.ID == "" || a.TenantID != tenA || a.AssetRef != "upl_a1" || a.SHA256 != shaHex(1) {
		t.Fatalf("asset row mismatch: %+v", a)
	}
	if a.MediaType != assetlib.MediaTypeVideo || a.Source != assetlib.SourceAiCut {
		t.Fatalf("enum fields mismatch: %+v", a)
	}
	if a.Candidate || a.StoreID != "" {
		t.Fatalf("defaults mismatch: candidate=%v store=%q", a.Candidate, a.StoreID)
	}
	// 声明字段与标签往返一致。
	withTags := libReg("upl_a2", shaHex(2), assetlib.MediaTypeImage)
	withTags.Tags = []string{"门店海报", "video-cover"}
	withTags.Purpose = "门店陈列"
	img, err := s.CreateLibAsset(tenA, withTags, "", "seed")
	must(t, err)
	if len(img.Tags) != 2 || img.Tags[0] != "门店海报" || img.Tags[1] != "video-cover" {
		t.Fatalf("tags roundtrip = %v", img.Tags)
	}
	if img.Purpose != "门店陈列" {
		t.Fatalf("purpose roundtrip = %q", img.Purpose)
	}

	// 同租户同键重复登记:唯一冲突,绝不静默改写。
	if _, err := s.CreateLibAsset(tenA, libReg("upl_a1", shaHex(1), assetlib.MediaTypeVideo), "", "seed"); !errors.Is(err, ErrDuplicateAsset) {
		t.Fatalf("duplicate registration err = %v, want ErrDuplicateAsset", err)
	}
	// 同 ref 不同 sha = 新版本 = 新行(版本锚点参与唯一键)。
	mustCreateAsset(t, s, tenA, "upl_a1", shaHex(9), assetlib.MediaTypeVideo)
	// 同 ref+sha 换租户:互不可见,均可登记。
	mustCreateAsset(t, s, tenB, "upl_a1", shaHex(1), assetlib.MediaTypeVideo)
	// 正式行与回流候选行可并存(先入候选、复核后另行正式登记)。
	cand := libReg("upl_a1", shaHex(1), assetlib.MediaTypeVideo)
	cand.Candidate = true
	if _, err := s.CreateLibAsset(tenA, cand, "", "seed"); err != nil {
		t.Fatalf("candidate row beside official row: %v", err)
	}
	// 候选行自身同键也唯一。
	if _, err := s.CreateLibAsset(tenA, cand, "", "seed"); !errors.Is(err, ErrDuplicateAsset) {
		t.Fatalf("duplicate candidate err = %v, want ErrDuplicateAsset", err)
	}

	// 门店归属:绑定门店必须真实存在;可空。
	st, err := s.CreateStore(tenA, "旗舰店", "地址", "seed")
	must(t, err)
	bound := libReg("upl_a3", shaHex(3), assetlib.MediaTypeBGM)
	if _, err := s.CreateLibAsset(tenA, bound, st.ID, "seed"); err != nil {
		t.Fatalf("bind existing store: %v", err)
	}
	if _, err := s.CreateLibAsset(tenA, bound, "sto_missing", "seed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bind missing store err = %v, want ErrNotFound", err)
	}
	// 他租户的门店不可绑定(跨租户 404 掩码)。
	stB, err := s.CreateStore(tenB, "B店", "地址B", "seed")
	must(t, err)
	if _, err := s.CreateLibAsset(tenA, libReg("upl_a4", shaHex(4), assetlib.MediaTypeImage), stB.ID, "seed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bind foreign store err = %v, want ErrNotFound", err)
	}

	// 列表租户内完整、跨租户 0 行。
	inA, err := s.ListLibAssets(tenA)
	must(t, err)
	if len(inA) != 5 { // a1, a2, a1-sha9, a1-candidate, a3
		t.Fatalf("tenant A list = %d rows, want 5", len(inA))
	}
	inB, err := s.ListLibAssets(tenB)
	must(t, err)
	if len(inB) != 1 { // B 的自有 upl_a1;他租户门店绑定尝试未落行
		t.Fatalf("tenant B list = %d rows, want 1", len(inB))
	}
}

func TestSetLibAssetCandidateOnlyFlipsFlag(t *testing.T) {
	s := openLibStore(t)
	tenA, _ := libTenants(t, s)
	a := mustCreateAsset(t, s, tenA, "upl_c1", shaHex(11), assetlib.MediaTypeVideo)

	got, err := s.SetLibAssetCandidate(a.ID, tenA, true)
	must(t, err)
	if !got.Candidate {
		t.Fatal("candidate flag not set")
	}
	// 标记语义只改标记:引用/版本/声明字段逐字段不动。
	if got.AssetRef != a.AssetRef || got.SHA256 != a.SHA256 || got.MediaType != a.MediaType ||
		got.Source != a.Source || got.Purpose != a.Purpose || got.GrantRef != a.GrantRef {
		t.Fatalf("candidate flip mutated frozen fields: %+v -> %+v", a, got)
	}
	back, err := s.SetLibAssetCandidate(a.ID, tenA, false)
	must(t, err)
	if back.Candidate {
		t.Fatal("candidate flag not cleared")
	}
	// 跨租户置位:ErrNotFound(不可见)。
	if _, err := s.SetLibAssetCandidate(a.ID, "tnt_other", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant candidate err = %v, want ErrNotFound", err)
	}
	// 未知 id:ErrNotFound。
	if _, err := s.SetLibAssetCandidate("las_missing", tenA, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id err = %v, want ErrNotFound", err)
	}
}

func TestLibPoolCRUDAndUniqueness(t *testing.T) {
	s := openLibStore(t)
	tenA, tenB := libTenants(t, s)

	p, err := s.CreateLibPool(tenA, "门店视频池", "seed")
	must(t, err)
	if p.ID == "" || p.Name != "门店视频池" || p.TenantID != tenA {
		t.Fatalf("pool row mismatch: %+v", p)
	}
	// 租户内名称唯一。
	if _, err := s.CreateLibPool(tenA, " 门店视频池 ", "seed"); !errors.Is(err, ErrDuplicatePool) {
		t.Fatalf("duplicate pool err = %v, want ErrDuplicatePool", err)
	}
	// 同名跨租户:互不可见。
	if _, err := s.CreateLibPool(tenB, "门店视频池", "seed"); err != nil {
		t.Fatalf("same name other tenant: %v", err)
	}
	// 读取与跨租户 404。
	if _, err := s.GetLibPool(p.ID, tenA); err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if _, err := s.GetLibPool(p.ID, tenB); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant pool err = %v, want ErrNotFound", err)
	}
	// 改名;改到撞他池名 = 唯一冲突(改回自己的名字不是冲突)。
	rn, err := s.RenameLibPool(p.ID, tenA, "主视频池")
	must(t, err)
	if rn.Name != "主视频池" {
		t.Fatalf("rename = %q", rn.Name)
	}
	same, err := s.RenameLibPool(p.ID, tenA, "主视频池")
	must(t, err)
	if same.Name != "主视频池" {
		t.Fatalf("rename-to-self = %q, want no-op success", same.Name)
	}
	pDup, err := s.CreateLibPool(tenA, "占名池", "seed")
	must(t, err)
	if _, err := s.RenameLibPool(p.ID, tenA, "占名池"); !errors.Is(err, ErrDuplicatePool) {
		t.Fatalf("rename onto other pool err = %v, want ErrDuplicatePool", err)
	}
	_ = pDup
	if _, err := s.RenameLibPool(p.ID, "tnt_other", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant rename err = %v, want ErrNotFound", err)
	}
	// 列表。
	ps, err := s.ListLibPools(tenA)
	must(t, err)
	if len(ps) != 2 { // 主视频池 + 占名池
		t.Fatalf("pool list = %d, want 2", len(ps))
	}
	// 删池:连带池内引用,不删台账;再删 = ErrNotFound。
	if err := s.DeleteLibPool(p.ID, tenA); err != nil {
		t.Fatalf("delete pool: %v", err)
	}
	if _, err := s.GetLibPool(p.ID, tenA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pool still present after delete")
	}
	if err := s.DeleteLibPool(p.ID, tenA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete err = %v, want ErrNotFound", err)
	}
}

func TestLibPoolItemsAddRemoveOrder(t *testing.T) {
	s := openLibStore(t)
	tenA, _ := libTenants(t, s)
	p, err := s.CreateLibPool(tenA, "素材池", "seed")
	must(t, err)
	a1 := mustCreateAsset(t, s, tenA, "upl_i1", shaHex(21), assetlib.MediaTypeVideo)
	a2 := mustCreateAsset(t, s, tenA, "upl_i2", shaHex(22), assetlib.MediaTypeVideo)
	a3 := mustCreateAsset(t, s, tenA, "upl_i3", shaHex(23), assetlib.MediaTypeBGM)

	// 未知素材不可入池。
	if _, err := s.AddLibPoolItem(tenA, p.ID, "las_missing", "seed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown asset err = %v, want ErrNotFound", err)
	}
	i1, err := s.AddLibPoolItem(tenA, p.ID, a1.ID, "seed")
	must(t, err)
	i2, err := s.AddLibPoolItem(tenA, p.ID, a2.ID, "seed")
	must(t, err)
	if _, err := s.AddLibPoolItem(tenA, p.ID, a3.ID, "seed"); err != nil {
		t.Fatalf("add bgm item: %v", err)
	}
	// (pool, asset) 唯一。
	if _, err := s.AddLibPoolItem(tenA, p.ID, a1.ID, "seed"); !errors.Is(err, ErrDuplicatePoolItem) {
		t.Fatalf("duplicate item err = %v, want ErrDuplicatePoolItem", err)
	}
	// 同一素材可进多个池(池间共享)。
	p2, err := s.CreateLibPool(tenA, "第二池", "seed")
	must(t, err)
	if _, err := s.AddLibPoolItem(tenA, p2.ID, a1.ID, "seed"); err != nil {
		t.Fatalf("share asset into second pool: %v", err)
	}

	// 有序列表:确定性顺序 (created_at, id),带素材快照。
	items, err := s.ListLibPoolItems(tenA, p.ID)
	must(t, err)
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if items[0].AssetID != a1.ID || items[1].AssetID != a2.ID {
		t.Fatalf("item order wrong: %v", items)
	}
	if items[2].Asset.MediaType != assetlib.MediaTypeBGM {
		t.Fatalf("joined asset snapshot missing: %+v", items[2].Asset)
	}
	_ = i1
	_ = i2

	// 移除;再移除 = ErrNotFound。
	if err := s.RemoveLibPoolItem(tenA, p.ID, items[2].ID); err != nil {
		t.Fatalf("remove item: %v", err)
	}
	if err := s.RemoveLibPoolItem(tenA, p.ID, items[2].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double remove err = %v, want ErrNotFound", err)
	}
	items, err = s.ListLibPoolItems(tenA, p.ID)
	must(t, err)
	if len(items) != 2 {
		t.Fatalf("items after remove = %d, want 2", len(items))
	}
	// 跨租户增删一律不可见。
	if _, err := s.AddLibPoolItem("tnt_other", p.ID, a1.ID, "seed"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant add err = %v, want ErrNotFound", err)
	}
	if err := s.RemoveLibPoolItem("tnt_other", p.ID, items[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant remove err = %v, want ErrNotFound", err)
	}
}

func TestDrawLibPoolDeterministicIdempotent(t *testing.T) {
	s := openLibStore(t)
	tenA, _ := libTenants(t, s)
	p, err := s.CreateLibPool(tenA, "视频池", "seed")
	must(t, err)
	var ids []string
	for i := 1; i <= 5; i++ {
		a := mustCreateAsset(t, s, tenA, "upl_d"+string(rune('0'+i)), shaHex(30+i), assetlib.MediaTypeVideo)
		if _, err := s.AddLibPoolItem(tenA, p.ID, a.ID, "seed"); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}

	day := "2026-09-20"
	sel, replayed, err := s.DrawLibPool(tenA, p.ID, "hero-main", day, false, "usr_owner_a")
	must(t, err)
	if replayed {
		t.Fatal("first draw must not be a replay")
	}
	want := ids[assetlib.PickIndex(p.ID, day, len(ids))]
	if sel.AssetID != want || sel.PoolID != p.ID || sel.SelectKey != "hero-main" || sel.DayUTC != day {
		t.Fatalf("selection mismatch: %+v (want asset %s)", sel, want)
	}
	if sel.Candidate {
		t.Fatal("official draw must not be candidate")
	}
	// 冻结快照与素材行一致。
	if sel.AssetRef == "" || len(sel.SHA256) != 64 {
		t.Fatalf("frozen snapshot incomplete: %+v", sel)
	}
	// 同键重复选择:幂等 —— 返回同一冻结行,不新增台账。
	again, replayed, err := s.DrawLibPool(tenA, p.ID, "hero-main", day, false, "usr_staff")
	must(t, err)
	if !replayed || again.ID != sel.ID || again.AssetID != sel.AssetID || again.SHA256 != sel.SHA256 {
		t.Fatalf("replay mismatch: replayed=%v sel=%+v again=%+v", replayed, sel, again)
	}
	// 种子只由 (pool_id, day) 派生:同日异键选出同一素材,但台账分键记行。
	other, replayed, err := s.DrawLibPool(tenA, p.ID, "second-slot", day, false, "usr_owner_a")
	must(t, err)
	if replayed || other.AssetID != want {
		t.Fatalf("same-seed different-key draw = %+v replayed=%v, want asset %s", other, replayed, want)
	}
	// 换日 = 换种子空间;同键跨日是新行(确定性断言:等于新日的确定性结果)。
	tomorrow := "2026-09-21"
	nextDay, replayed, err := s.DrawLibPool(tenA, p.ID, "hero-main", tomorrow, false, "usr_owner_a")
	must(t, err)
	if replayed || nextDay.ID == sel.ID || nextDay.AssetID != ids[assetlib.PickIndex(p.ID, tomorrow, len(ids))] {
		t.Fatalf("next-day draw mismatch: %+v replayed=%v", nextDay, replayed)
	}
	// 回流候选:candidate=true 走独立键空间,不替换正式行。
	cand, replayed, err := s.DrawLibPool(tenA, p.ID, "hero-main", day, true, "usr_owner_a")
	must(t, err)
	if replayed || !cand.Candidate || cand.ID == sel.ID {
		t.Fatalf("candidate draw mismatch: %+v replayed=%v", cand, replayed)
	}
	// 候选行同键也幂等。
	cand2, replayed, err := s.DrawLibPool(tenA, p.ID, "hero-main", day, true, "usr_owner_a")
	must(t, err)
	if !replayed || cand2.ID != cand.ID {
		t.Fatalf("candidate replay mismatch: replayed=%v id=%s", replayed, cand2.ID)
	}
	// 调取不改变池:条目数与顺序原样。
	items, err := s.ListLibPoolItems(tenA, p.ID)
	must(t, err)
	if len(items) != 5 {
		t.Fatalf("pool mutated by draws: %d items", len(items))
	}
	// 台账:同租户可查、按时间有序。
	led, err := s.ListLibSelections(tenA, p.ID)
	must(t, err)
	if len(led) != 4 { // hero-main 官方 + second-slot + 次日 + 候选
		t.Fatalf("ledger = %d rows, want 4", len(led))
	}
	// 空池拒绝。
	pe, err := s.CreateLibPool(tenA, "空池", "seed")
	must(t, err)
	if _, _, err := s.DrawLibPool(tenA, pe.ID, "k", day, false, "u"); !errors.Is(err, assetlib.ErrEmptyPool) {
		t.Fatalf("empty pool err = %v, want assetlib.ErrEmptyPool", err)
	}
	// 跨租户调取:池不可见。
	if _, _, err := s.DrawLibPool("tnt_other", p.ID, "k", day, false, "u"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant draw err = %v, want ErrNotFound", err)
	}
	// 台账跨租户 0 行。
	ledB, err := s.ListLibSelections("tnt_other", p.ID)
	must(t, err)
	if len(ledB) != 0 {
		t.Fatalf("cross-tenant ledger = %d rows, want 0", len(ledB))
	}
}

func TestSelectionFreezeSurvivesNewVersion(t *testing.T) {
	s := openLibStore(t)
	tenA, _ := libTenants(t, s)
	p, err := s.CreateLibPool(tenA, "冻结池", "seed")
	must(t, err)
	v1 := mustCreateAsset(t, s, tenA, "upl_freeze", shaHex(41), assetlib.MediaTypeVideo)
	if _, err := s.AddLibPoolItem(tenA, p.ID, v1.ID, "seed"); err != nil {
		t.Fatal(err)
	}
	day := "2026-09-20"
	sel, _, err := s.DrawLibPool(tenA, p.ID, "k", day, false, "u")
	must(t, err)
	if sel.AssetID != v1.ID || sel.SHA256 != v1.SHA256 {
		t.Fatalf("selection not anchored to v1: %+v", sel)
	}

	// 源素材更新 = 同 ref 新 sha 新行(显式登记,无自动替换)。
	v2 := mustCreateAsset(t, s, tenA, "upl_freeze", shaHex(42), assetlib.MediaTypeVideo)
	if _, err := s.AddLibPoolItem(tenA, p.ID, v2.ID, "seed"); err != nil {
		t.Fatal(err)
	}
	// 已选行原样:asset_id / asset_ref / sha256 快照不变(版本冻结)。
	led, err := s.ListLibSelections(tenA, p.ID)
	must(t, err)
	if len(led) != 1 || led[0].ID != sel.ID || led[0].AssetID != v1.ID || led[0].SHA256 != shaHex(41) {
		t.Fatalf("frozen selection changed after new version: %+v", led)
	}
	// 重放同键仍返回冻结行(即使池内已有新版本)。
	again, replayed, err := s.DrawLibPool(tenA, p.ID, "k", day, false, "u")
	must(t, err)
	if !replayed || again.AssetID != v1.ID || again.SHA256 != shaHex(41) {
		t.Fatalf("replay after update not frozen: replayed=%v %+v", replayed, again)
	}
	// 素材行也未被改写(v1 仍是无候选的原始行)。
	cur, err := s.GetLibAsset(v1.ID, tenA)
	must(t, err)
	if cur.SHA256 != shaHex(41) || cur.Candidate {
		t.Fatalf("v1 row mutated: %+v", cur)
	}
}

func TestDrawReplayAfterPoolChangeIsFrozen(t *testing.T) {
	s := openLibStore(t)
	tenA, _ := libTenants(t, s)
	p, err := s.CreateLibPool(tenA, "易变池", "seed")
	must(t, err)
	a := mustCreateAsset(t, s, tenA, "upl_r1", shaHex(51), assetlib.MediaTypeVideo)
	item, err := s.AddLibPoolItem(tenA, p.ID, a.ID, "seed")
	must(t, err)
	day := "2026-09-20"
	sel, _, err := s.DrawLibPool(tenA, p.ID, "k", day, false, "u")
	must(t, err)
	// 池被清空(条目移除)后,同键重放仍返回冻结行 —— 幂等先于取池。
	if err := s.RemoveLibPoolItem(tenA, p.ID, item.ID); err != nil {
		t.Fatal(err)
	}
	again, replayed, err := s.DrawLibPool(tenA, p.ID, "k", day, false, "u")
	must(t, err)
	if !replayed || again.ID != sel.ID || again.AssetID != a.ID {
		t.Fatalf("replay after pool change: replayed=%v %+v", replayed, again)
	}
	// 新键则走空池拒绝。
	if _, _, err := s.DrawLibPool(tenA, p.ID, "k2", day, false, "u"); !errors.Is(err, assetlib.ErrEmptyPool) {
		t.Fatalf("fresh key on empty pool err = %v, want ErrEmptyPool", err)
	}
}

func TestDeletePoolKeepsSelections(t *testing.T) {
	s := openLibStore(t)
	tenA, _ := libTenants(t, s)
	p, err := s.CreateLibPool(tenA, "将删池", "seed")
	must(t, err)
	a := mustCreateAsset(t, s, tenA, "upl_del", shaHex(61), assetlib.MediaTypeVideo)
	if _, err := s.AddLibPoolItem(tenA, p.ID, a.ID, "seed"); err != nil {
		t.Fatal(err)
	}
	sel, _, err := s.DrawLibPool(tenA, p.ID, "k", "2026-09-20", false, "u")
	must(t, err)
	// 删池后台账行保持完整可审计(pool_id 不设外键,chosen_item_id 不设外键)。
	must(t, s.DeleteLibPool(p.ID, tenA))
	led, err := s.ListLibSelections(tenA, p.ID)
	must(t, err)
	if len(led) != 1 || led[0].ID != sel.ID || led[0].AssetID != a.ID {
		t.Fatalf("ledger not preserved after pool delete: %+v", led)
	}
}

func TestLibReopenRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "touch.db")

	tenA := func() string {
		s := openLibStoreAt(t, path)
		ta, err := s.CreateTenant("持久商家")
		must(t, err)
		a := mustCreateAsset(t, s, ta.ID, "upl_p1", shaHex(71), assetlib.MediaTypeVideo)
		reg := libReg("upl_p2", shaHex(72), assetlib.MediaTypeImage)
		reg.Tags = []string{"门店海报"}
		reg.Candidate = true
		if _, err := s.CreateLibAsset(ta.ID, reg, "", "seed"); err != nil {
			t.Fatal(err)
		}
		p, err := s.CreateLibPool(ta.ID, "持久池", "seed")
		must(t, err)
		if _, err := s.AddLibPoolItem(ta.ID, p.ID, a.ID, "seed"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.DrawLibPool(ta.ID, p.ID, "k", "2026-09-20", false, "u"); err != nil {
			t.Fatal(err)
		}
		return ta.ID
	}()

	// 关库重开:引用/池/台账完整。
	s2 := openLibStoreAt(t, path)
	assets, err := s2.ListLibAssets(tenA)
	must(t, err)
	if len(assets) != 2 {
		t.Fatalf("assets after reopen = %d, want 2", len(assets))
	}
	var cand LibAsset
	for _, a := range assets {
		if a.AssetRef == "upl_p2" {
			cand = a
		}
	}
	if !cand.Candidate || len(cand.Tags) != 1 || cand.Tags[0] != "门店海报" {
		t.Fatalf("candidate row lost fields after reopen: %+v", cand)
	}
	pools, err := s2.ListLibPools(tenA)
	must(t, err)
	if len(pools) != 1 {
		t.Fatalf("pools after reopen = %d, want 1", len(pools))
	}
	items, err := s2.ListLibPoolItems(tenA, pools[0].ID)
	must(t, err)
	if len(items) != 1 {
		t.Fatalf("items after reopen = %d, want 1", len(items))
	}
	led, err := s2.ListLibSelections(tenA, pools[0].ID)
	must(t, err)
	if len(led) != 1 || led[0].DayUTC != "2026-09-20" || len(led[0].SHA256) != 64 {
		t.Fatalf("ledger after reopen = %+v", led)
	}
	// 重开后同键重放仍幂等(冻结行继续有效)。
	again, replayed, err := s2.DrawLibPool(tenA, pools[0].ID, "k", "2026-09-20", false, "u")
	must(t, err)
	if !replayed || again.ID != led[0].ID {
		t.Fatalf("replay after reopen: replayed=%v id=%s", replayed, again.ID)
	}
}

// shaHex is a deterministic stand-in fingerprint for fixtures (64 hex chars).
func shaHex(n int) string {
	return fmt.Sprintf("%064x", n)
}
