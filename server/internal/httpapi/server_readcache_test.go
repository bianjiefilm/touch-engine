package httpapi

// HUI-2981 缓存收益 T1 —— 缓存开启下的不变量 E2E(隔离环境,零生产接触):
//
//   管理员修改 → 消费者页面 → 实际操作 的确定性失效(0 请求陈旧);
//   available 按当前时间现判;无串租户/品牌/作用域;写路径回源+权限;
//   已知事件集重算 == 缓存结果(带 as_of);Disable 一键关断;
//   高并发 miss 不击穿(singleflight 合并,loader 恰一次);命中 0 SQL。
//
// 既有全部测试在 flag off 下运行 = 现状逐字节不变(回归由原套件保证)。

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/readcache"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
)

type cacheFixture struct {
	s    *Server
	ts   *httptest.Server
	st   *store.Store
	tenA string
}

func newCacheFixtureOn(t *testing.T, d *sql.DB) *cacheFixture {
	t.Helper()
	st := store.New(d)
	tenA, err := st.CreateTenant("缓存商家")
	mustNoErr(t, err)
	for _, m := range []string{"usr_owner_a", "usr_owner_b"} {
		if _, err := st.CreateMember(tenA.ID, m, "org_owner", m, "test", true); err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}
	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
		"sess-owner-b": {"usr_owner_b", "b@example.com"},
		// 门店经理会话:成员行(含 store_scope)由具体测试自行创建。
		"sess-mgr-a": {"usr_mgr_a", "mgr@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cacheCfg(), d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return &cacheFixture{s: s, ts: ts, st: st, tenA: tenA.ID}
}

func newCacheFixture(t *testing.T) *cacheFixture {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return newCacheFixtureOn(t, d)
}

func cacheCfg() config.Config {
	return config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "FEATURE_DASHBOARD":
			return "on"
		case "FEATURE_PUBLIC_CACHE":
			return "on"
		case "FEATURE_DASHBOARD_CACHE":
			return "on"
		case "FEATURE_LEADS_CAPTURE":
			return "on"
		// notify 不可达:E4 outbox 档——提交本地接受,投递留待后台,不阻塞 E2E。
		case "PLATFORM_NOTIFY_BASE_URL":
			return "http://notify.test"
		case "PLATFORM_NOTIFY_TOKEN":
			return "notify-token"
		case "LEADS_TARGET_APP_ID":
			return "crm-test"
		case "LEADS_PHONE_PEPPER":
			return "pepper-test"
		}
		return ""
	})
}

// admin runs an authenticated owner-A request against the admin surface.
func (f *cacheFixture) admin(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	return f.authed(t, method, path, body, "sess-owner-a", f.tenA)
}

func (f *cacheFixture) authed(t *testing.T, method, path, body, session, tenant string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	mustNoErr(t, err)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Session-Token", session)
	req.Header.Set("X-Tenant-ID", tenant)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := f.ts.Client().Do(req)
	mustNoErr(t, err)
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// page is the guest public-page GET decoded into the whitelist view.
func (f *cacheFixture) page(t *testing.T, code string) (int, publicLinkView) {
	t.Helper()
	res, err := http.Get(f.ts.URL + "/api/v1/public/links/" + code)
	mustNoErr(t, err)
	defer res.Body.Close()
	var v publicLinkView
	raw, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(raw, &v)
	return res.StatusCode, v
}

func (f *cacheFixture) dashWindow() string {
	return fmt.Sprintf("?window_start=%s&window_end=%s",
		time.Now().UTC().Add(-24*time.Hour).Format(time.RFC3339),
		time.Now().UTC().Add(time.Hour).Format(time.RFC3339))
}

func (f *cacheFixture) dash(t *testing.T, session, tenant string) (int, map[string]any) {
	t.Helper()
	return f.authed(t, "GET", "/api/v1/dashboard"+f.dashWindow(), "", session, tenant)
}

func metricValue(t *testing.T, body map[string]any, key string) int64 {
	t.Helper()
	metrics, _ := body["metrics"].([]any)
	for _, m := range metrics {
		mm, _ := m.(map[string]any)
		if mm["key"] == key {
			v, _ := mm["value"].(float64)
			return int64(v)
		}
	}
	t.Fatalf("metric %s not found in %v", key, body)
	return 0
}

// seedLiveCampaign runs the full admin flow: store (optional) → campaign →
// activate → link. Returns (campaignID, linkID, code).
func (f *cacheFixture) seedLiveCampaign(t *testing.T, storeID string) (string, string, string) {
	t.Helper()
	cBody := fmt.Sprintf(`{"title":"T1","public_content":"内容","starts_at":%q,"ends_at":%q,"store_id":%q}`,
		time.Now().UTC().Add(-time.Hour).Format(time.RFC3339),
		time.Now().UTC().Add(time.Hour).Format(time.RFC3339), storeID)
	st, body := f.admin(t, "POST", "/api/v1/campaigns", cBody)
	if st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("campaign create = %d %v", st, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("campaign id missing: %v", body)
	}
	if st, body = f.admin(t, "POST", "/api/v1/campaigns/"+id+"/status", `{"status":"active"}`); st != http.StatusOK {
		t.Fatalf("activate = %d %v", st, body)
	}
	st, body = f.admin(t, "POST", "/api/v1/campaigns/"+id+"/links", `{}`)
	if st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("link create = %d %v", st, body)
	}
	linkID, _ := body["id"].(string)
	code, _ := body["code"].(string)
	if linkID == "" || code == "" {
		t.Fatalf("link fields missing: %v", body)
	}
	return id, linkID, code
}

// ---- 1. 管理员修改 → 消费者页面:0 请求陈旧 -------------------------------------

func TestCacheAdminEditsVisibleNextRequest(t *testing.T) {
	f := newCacheFixture(t)
	cid, linkID, code := f.seedLiveCampaign(t, "")

	if st, v := f.page(t, code); st != 200 || v.State != "available" || v.Title != "T1" {
		t.Fatalf("first view = %d %+v", st, v)
	}
	if st, v := f.page(t, code); st != 200 || v.Title != "T1" {
		t.Fatalf("warm view = %d %+v", st, v)
	}

	// 改标题 → 下一请求立即新值(epoch 失效,0 陈旧)
	if st, body := f.admin(t, "PATCH", "/api/v1/campaigns/"+cid, `{"title":"T2"}`); st != http.StatusOK {
		t.Fatalf("patch = %d %v", st, body)
	}
	if st, v := f.page(t, code); st != 200 || v.Title != "T2" {
		t.Fatalf("after title edit = %d %+v (stale value served)", st, v)
	}

	// 暂停 → 404/paused;恢复 → available
	if st, body := f.admin(t, "POST", "/api/v1/campaigns/"+cid+"/status", `{"status":"paused"}`); st != http.StatusOK {
		t.Fatalf("pause = %d %v", st, body)
	}
	if st, v := f.page(t, code); st != 404 || v.State != "paused" {
		t.Fatalf("after pause = %d %+v", st, v)
	}
	if st, body := f.admin(t, "POST", "/api/v1/campaigns/"+cid+"/status", `{"status":"active"}`); st != http.StatusOK {
		t.Fatalf("resume = %d %v", st, body)
	}
	if st, v := f.page(t, code); st != 200 || v.State != "available" {
		t.Fatalf("after resume = %d %+v", st, v)
	}

	// 窗口改到过去 → expired(available 按当前时间现判,绝不缓存"永久有效")
	past := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)
	if st, body := f.admin(t, "PATCH", "/api/v1/campaigns/"+cid, fmt.Sprintf(`{"ends_at":%q}`, past)); st != http.StatusOK {
		t.Fatalf("patch ends_at = %d %v", st, body)
	}
	if st, v := f.page(t, code); st != 404 || v.State != "expired" {
		t.Fatalf("after expiring = %d %+v", st, v)
	}
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	if st, body := f.admin(t, "PATCH", "/api/v1/campaigns/"+cid, fmt.Sprintf(`{"ends_at":%q}`, future)); st != http.StatusOK {
		t.Fatalf("restore ends_at = %d %v", st, body)
	}
	if st, v := f.page(t, code); st != 200 || v.State != "available" {
		t.Fatalf("after restore = %d %+v", st, v)
	}

	// 短码停用 → link_disabled;重启 → available
	if st, body := f.admin(t, "POST", "/api/v1/campaigns/"+cid+"/links/"+linkID+"/enabled", `{"enabled":false}`); st != http.StatusOK {
		t.Fatalf("link disable = %d %v", st, body)
	}
	if st, v := f.page(t, code); st != 404 || v.State != "link_disabled" {
		t.Fatalf("after link disable = %d %+v", st, v)
	}
	if st, body := f.admin(t, "POST", "/api/v1/campaigns/"+cid+"/links/"+linkID+"/enabled", `{"enabled":true}`); st != http.StatusOK {
		t.Fatalf("link enable = %d %v", st, body)
	}
	if st, v := f.page(t, code); st != 200 || v.State != "available" {
		t.Fatalf("after link enable = %d %+v", st, v)
	}

	// 结束态
	if st, body := f.admin(t, "POST", "/api/v1/campaigns/"+cid+"/status", `{"status":"ended"}`); st != http.StatusOK {
		t.Fatalf("end = %d %v", st, body)
	}
	if st, v := f.page(t, code); st != 404 || v.State != "ended" {
		t.Fatalf("after end = %d %+v", st, v)
	}
}

// ---- 2. 门店停用/恢复 → 标注立即变化 -------------------------------------------

func TestCacheStoreDisableNoticeImmediate(t *testing.T) {
	f := newCacheFixture(t)
	if st, body := f.admin(t, "POST", "/api/v1/stores", `{"name":"门店一","address":"地址"}`); st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("store create = %d %v", st, body)
	}
	{
		_, list := f.admin(t, "GET", "/api/v1/stores", "")
		stores, _ := list["items"].([]any)
		if len(stores) == 0 {
			t.Fatalf("no stores in %v", list)
		}
		s0, _ := stores[0].(map[string]any)
		storeID, _ := s0["id"].(string)
		cid, _, code := f.seedLiveCampaign(t, storeID)
		_ = cid

		if st, v := f.page(t, code); st != 200 || v.State != "available" || v.StoreName != "门店一" || v.StoreNotice != "" {
			t.Fatalf("bound view = %d %+v", st, v)
		}
		// 停用门店 → available + store_unavailable 标注(下一请求立即)
		if st, body := f.admin(t, "POST", "/api/v1/stores/"+storeID+"/status", `{"status":"disabled"}`); st != http.StatusOK {
			t.Fatalf("store disable = %d %v", st, body)
		}
		if st, v := f.page(t, code); st != 200 || v.StoreNotice != storeNoticeUnavailable {
			t.Fatalf("after store disable = %d %+v", st, v)
		}
		// 恢复 → 标注消失
		if st, body := f.admin(t, "POST", "/api/v1/stores/"+storeID+"/status", `{"status":"active"}`); st != http.StatusOK {
			t.Fatalf("store enable = %d %v", st, body)
		}
		if st, v := f.page(t, code); st != 200 || v.StoreNotice != "" {
			t.Fatalf("after store enable = %d %+v", st, v)
		}
	}
}

// ---- 3. 命中 0 SQL(核心收益主张的直接验证)--------------------------------------

// openCounting reopens a migrated database through the SQL-counting driver
// (same counting connector as the measure fixture).
func openCounting(t *testing.T, path string, counter *int64) *sql.DB {
	t.Helper()
	d := sql.OpenDB(countConnector{dsn: "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", n: counter})
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	if err := d.Ping(); err != nil {
		t.Fatalf("ping counting db: %v", err)
	}
	return d
}

func TestCacheHitCostsZeroSQL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "count.db")
	plain, err := db.Open(path) // migrations only
	mustNoErr(t, err)
	mustNoErr(t, plain.Close())

	counter := new(int64)
	d := openCounting(t, path, counter)
	f := newCacheFixtureOn(t, d)

	cid, _, code := f.seedLiveCampaign(t, "")
	_ = cid
	if st, v := f.page(t, code); st != 200 || v.State != "available" {
		t.Fatalf("warm = %d %+v", st, v)
	}

	before := sqlCount(counter)
	if st, v := f.page(t, code); st != 200 || v.Title != "T1" {
		t.Fatalf("hit = %d %+v", st, v)
	}
	if got := sqlCount(counter) - before; got != 0 {
		t.Fatalf("public page hit cost %d SQL statements, want exactly 0", got)
	}

	before = sqlCount(counter)
	st, body := f.dash(t, "sess-owner-a", f.tenA)
	loadCost := sqlCount(counter) - before
	if st != 200 {
		t.Fatalf("dash load = %d %v", st, body)
	}
	before = sqlCount(counter)
	st, body = f.dash(t, "sess-owner-a", f.tenA)
	hitCost := sqlCount(counter) - before
	if st != 200 {
		t.Fatalf("dash hit = %d", st)
	}
	if body["as_of"] == nil || body["as_of"].(string) == "" {
		t.Fatalf("cached dashboard must carry as_of, got %v", body)
	}
	// the hit keeps only the fixed per-request auth lookup (member row);
	// all ~18 aggregation statements must be gone from the hit path.
	if hitCost > 2 {
		t.Fatalf("dashboard hit cost %d SQL (auth-only expected <=2), load cost %d", hitCost, loadCost)
	}
	if loadCost < hitCost+10 {
		t.Fatalf("load cost %d must carry the aggregation absent from the hit (%d)", loadCost, hitCost)
	}
	if f.s.PubCache.Hits() < 1 || f.s.DashCache.Hits() < 1 {
		t.Fatalf("hits must be recorded: pub=%d dash=%d", f.s.PubCache.Hits(), f.s.DashCache.Hits())
	}
}

// ---- 4. 无串租户/无串作用域 ------------------------------------------------------

func TestCacheNoCrossTenantOrScope(t *testing.T) {
	f := newCacheFixture(t)
	// tenant B exists in fixture (usr_owner_b); A gets two campaigns
	st, _ := f.admin(t, "POST", "/api/v1/stores", `{"name":"S1","address":"a"}`)
	if st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("store create = %d", st)
	}
	_, list := f.admin(t, "GET", "/api/v1/stores", "")
	items4, _ := list["items"].([]any)
	if len(items4) == 0 {
		t.Fatalf("no stores in %v", list)
	}
	s0, _ := items4[0].(map[string]any)
	storeID, _ := s0["id"].(string)

	_, _, codeA1 := f.seedLiveCampaign(t, storeID) // bound to S1
	_, _, codeA2 := f.seedLiveCampaign(t, "")      // unbound

	// known events: 7 views on A1 (S1), 5 on A2 (unbound), today, tenant A only
	today := time.Now().UTC().Format("2006-01-02")
	for i := 0; i < 7; i++ {
		mustNoErr(t, f.st.IncrementViewStat(codeA1, today, "qr"))
	}
	for i := 0; i < 5; i++ {
		mustNoErr(t, f.st.IncrementViewStat(codeA2, today, "web"))
	}

	// owner A sees 12, twice (second from cache, identical)
	st1, body1 := f.dash(t, "sess-owner-a", f.tenA)
	if st1 != 200 || metricValue(t, body1, "touch_triggers") != 12 {
		t.Fatalf("owner A dash = %d %v", st1, body1)
	}
	st2, body2 := f.dash(t, "sess-owner-a", f.tenA)
	if st2 != 200 || metricValue(t, body2, "touch_triggers") != 12 {
		t.Fatalf("owner A dash#2 = %d %v", st2, body2)
	}
	if body1["as_of"] != body2["as_of"] || body1["as_of"] == nil {
		t.Fatalf("same-window hits must reuse one entry: %v vs %v", body1["as_of"], body2["as_of"])
	}

	// store manager scoped to S1 sees ONLY S1's 7 — never the owner's cached 12
	if _, err := f.st.CreateMemberScoped(f.tenA, "usr_mgr_a", "store_manager", "经理", "test", true, storeID); err != nil {
		t.Fatalf("mgr member: %v", err)
	}
	// 作用域 key 隔离:经理(S1)与总部(全门店)绝不共享一个缓存条目
	stM, bodyM := f.dash(t, "sess-mgr-a", f.tenA)
	if stM != 200 || metricValue(t, bodyM, "touch_triggers") != 7 {
		t.Fatalf("manager dash must be S1-scoped 7, got %d %v", stM, bodyM)
	}
	if scope, _ := bodyM["scope"].(map[string]any); scope["store_scope"] != storeID {
		t.Fatalf("manager scope not pinned: %v", bodyM["scope"])
	}
	if got := f.s.DashCache.Len(); got != 2 {
		t.Fatalf("owner(scope='') and manager(scope=S1) must occupy separate entries, len=%d", got)
	}

	// tenant B (true second tenant) sees ZERO of A's facts
	dB, err := db.Open(filepath.Join(t.TempDir(), "b.db"))
	mustNoErr(t, err)
	t.Cleanup(func() { dB.Close() })
	fb := newCacheFixtureOn(t, dB)
	stB2, bodyB2 := fb.dash(t, "sess-owner-b", fb.tenA)
	if stB2 != 200 || metricValue(t, bodyB2, "touch_triggers") != 0 {
		t.Fatalf("tenant B dash = %d %v (cross-tenant leak)", stB2, bodyB2)
	}
	if bodyB2["as_of"] == nil {
		t.Fatalf("B cached response missing as_of: %v", bodyB2)
	}
}

// ---- 5. 已知事件集重算一致性 + 有界陈旧(as_of) ----------------------------------

func TestCacheRecomputeConsistencyAndBoundedStaleness(t *testing.T) {
	f := newCacheFixture(t)
	_, _, code := f.seedLiveCampaign(t, "")
	today := time.Now().UTC().Format("2006-01-02")
	for i := 0; i < 7; i++ {
		mustNoErr(t, f.st.IncrementViewStat(code, today, "qr"))
	}

	// 换手工时钟缓存(TTL 80ms,零抖动)观察有界陈旧:时钟推进完全受控,
	// 不依赖 sleep,可并行负载下确定性复现
	now := time.Now()
	clock := func() time.Time { return now }
	f.s.DashCache = readcache.New[dashboardEntry](readcache.Options{
		Capacity: 16, TTL: 80 * time.Millisecond, Epoch: f.st.Epoch, Now: clock,
	})
	st, cached := f.dash(t, "sess-owner-a", f.tenA)
	if st != 200 || metricValue(t, cached, "touch_triggers") != 7 {
		t.Fatalf("cached dash = %d %v", st, cached)
	}
	asOf, _ := cached["as_of"].(string)
	if asOf == "" {
		t.Fatal("cached dash must carry as_of")
	}

	// 晚到事实在 TTL 内不可见(有界陈旧),TTL 后必须如实出现
	mustNoErr(t, f.st.IncrementViewStat(code, today, "qr")) // +1 晚到
	now = now.Add(40 * time.Millisecond)                    // 仍在 TTL 内
	st, within := f.dash(t, "sess-owner-a", f.tenA)
	if st != 200 || metricValue(t, within, "touch_triggers") != 7 {
		t.Fatalf("within-TTL dash must stay 7, got %d %v", st, within)
	}
	now = now.Add(120 * time.Millisecond) // 越过 TTL 上界
	st, after := f.dash(t, "sess-owner-a", f.tenA)
	if st != 200 || metricValue(t, after, "touch_triggers") != 8 {
		t.Fatalf("post-TTL dash must show 8, got %d %v", st, after)
	}
	// 重载证据:as_of 为整秒粒度,80ms 内重载可能同秒;用装载计数证明条目确实重建
	if loads := f.s.DashCache.Loads(); loads != 2 {
		t.Fatalf("post-TTL reload must have run the loader once more, loads=%d", loads)
	}
	if a2, _ := after["as_of"].(string); a2 == "" {
		t.Fatal("post-TTL as_of must stay present")
	}
	_ = asOf

	// 重算一致性:直算(Disable)与缓存同窗口逐项相等
	st, cached2 := f.dash(t, "sess-owner-a", f.tenA)
	if st != 200 {
		t.Fatalf("dash = %d", st)
	}
	f.s.DashCache.Disable()
	st, direct := f.dash(t, "sess-owner-a", f.tenA)
	if st != 200 {
		t.Fatalf("direct dash = %d", st)
	}
	if direct["as_of"] != nil {
		t.Fatalf("direct dash must not carry as_of, got %v", direct["as_of"])
	}
	cm, _ := cached2["metrics"].([]any)
	dm, _ := direct["metrics"].([]any)
	if len(cm) != len(dm) {
		t.Fatalf("metric count mismatch")
	}
	for i := range cm {
		a, _ := cm[i].(map[string]any)
		b, _ := dm[i].(map[string]any)
		if a["key"] != b["key"] || a["value"] != b["value"] {
			t.Fatalf("recompute mismatch at %v: cached=%v direct=%v", a["key"], a["value"], b["value"])
		}
	}
}

// ---- 6. 写路径不经过缓存、数据保持新鲜 -------------------------------------------

func TestCacheWritePathsStayFresh(t *testing.T) {
	f := newCacheFixture(t)
	cid, _, code := f.seedLiveCampaign(t, "")

	// 公共页先热身进缓存
	if st, v := f.page(t, code); st != 200 || v.State != "available" {
		t.Fatalf("warm = %d %+v", st, v)
	}

	// 表单挂载(写)→ 立即可见(写路径回源,绝不读缓存)
	mkt := true
	if st, body := f.admin(t, "POST", "/api/v1/campaigns/"+cid+"/lead-form",
		fmt.Sprintf(`{"notice_version":%q,"marketing_optin_enabled":%v}`, leadsCurrentVersion(t), mkt)); st != http.StatusOK && st != http.StatusCreated {
		t.Fatalf("form upsert = %d %v", st, body)
	}
	res, err := http.Get(f.ts.URL + "/api/v1/public/links/" + code + "/lead-form")
	mustNoErr(t, err)
	raw, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var form map[string]any
	_ = json.Unmarshal(raw, &form)
	if res.StatusCode != 200 || form["enabled"] != true {
		t.Fatalf("lead form = %d %v", res.StatusCode, form)
	}
	notice, _ := form["notice"].(map[string]any)
	version, _ := notice["version"].(string)

	// 浏览 beacon(写)成功,且不影响已缓存的页面载荷
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/v1/public/links/"+code+"/view-events", strings.NewReader(`{"channel":"qr"}`))
	req.Header.Set("Content-Type", "application/json")
	res2, err := http.DefaultClient.Do(req)
	mustNoErr(t, err)
	io.Copy(io.Discard, res2.Body)
	res2.Body.Close()
	if res2.StatusCode != 204 && res2.StatusCode != 200 {
		t.Fatalf("beacon = %d", res2.StatusCode)
	}

	// 看板换手工时钟缓存(TTL 60ms):先装载(此时 0 条提交),再提交——
	// TTL 界内不泄露半真值,界后如实计入(时钟推进受控,无 sleep)
	dashNow := time.Now()
	dashClock := func() time.Time { return dashNow }
	f.s.DashCache = readcache.New[dashboardEntry](readcache.Options{
		Capacity: 16, TTL: 60 * time.Millisecond, Epoch: f.st.Epoch, Now: dashClock,
	})
	if stA, bodyA := f.dash(t, "sess-owner-a", f.tenA); stA != 200 || metricValue(t, bodyA, "lead_submissions") != 0 {
		t.Fatalf("pre-submit dash must show 0, got %d %v", stA, bodyA)
	}

	// 授权留资提交(写)→ 本地接受;状态查询(核验读)一致
	subBody := fmt.Sprintf(`{"name":"访客","phone":"13800001234","consent":true,"consent_version":%q,"channel":"qr"}`, version)
	st, sub := f.authed(t, "POST", "/api/v1/public/links/"+code+"/lead-submissions", subBody, "", "")
	if st != http.StatusCreated && st != http.StatusOK {
		t.Fatalf("submit = %d %v", st, sub)
	}
	ref, _ := sub["submission_ref"].(string)
	if ref == "" {
		t.Fatalf("no submission_ref: %v", sub)
	}
	st, stat := f.authed(t, "POST", "/api/v1/public/links/"+code+"/lead-status",
		fmt.Sprintf(`{"submission_ref":%q,"phone":"13800001234"}`, ref), "", "")
	if st != 200 || stat["state"] == nil {
		t.Fatalf("lead status = %d %v", st, stat)
	}

	// TTL 界内:提交事实已入库,缓存仍如实报 0(有界陈旧,as_of 标明口径时刻)
	dashNow = dashNow.Add(30 * time.Millisecond)
	if stW, bodyW := f.dash(t, "sess-owner-a", f.tenA); stW != 200 || metricValue(t, bodyW, "lead_submissions") != 0 {
		t.Fatalf("within-TTL dash must stay 0, got %d %v", stW, bodyW)
	}
	dashNow = dashNow.Add(120 * time.Millisecond)
	stB, afterBody := f.dash(t, "sess-owner-a", f.tenA)
	if stB != 200 || metricValue(t, afterBody, "lead_submissions") != 1 {
		t.Fatalf("post-TTL dash = %d %v", stB, afterBody)
	}
}

func leadsCurrentVersion(t *testing.T) string {
	t.Helper()
	return leads.CurrentNotice().Version
}

// ---- 7. 一键禁用:关断后一切照常(等效直查) ---------------------------------------

func TestCacheDisableSwitchKeepsServing(t *testing.T) {
	f := newCacheFixture(t)
	cid, _, code := f.seedLiveCampaign(t, "")
	if st, v := f.page(t, code); st != 200 || v.Title != "T1" {
		t.Fatalf("warm = %d %+v", st, v)
	}
	if st, _ := f.dash(t, "sess-owner-a", f.tenA); st != 200 {
		t.Fatal("dash warm failed")
	}

	f.s.PubCache.Disable()
	f.s.DashCache.Disable()

	// 公共页照常,管理修改照常立即生效
	if st, v := f.page(t, code); st != 200 || v.Title != "T1" {
		t.Fatalf("after disable = %d %+v", st, v)
	}
	if st, _ := f.admin(t, "PATCH", "/api/v1/campaigns/"+cid, `{"title":"T9"}`); st != http.StatusOK {
		t.Fatal("patch failed")
	}
	if st, v := f.page(t, code); st != 200 || v.Title != "T9" {
		t.Fatalf("after disable+edit = %d %+v", st, v)
	}
	st, body := f.dash(t, "sess-owner-a", f.tenA)
	if st != 200 || body["as_of"] != nil {
		t.Fatalf("disabled dash = %d %v (as_of must be absent on direct path)", st, body["as_of"])
	}
}

// ---- 8. 高并发 miss:不击穿、不全站回源风暴 --------------------------------------

func TestCacheConcurrentMissMerged(t *testing.T) {
	f := newCacheFixture(t)
	_, _, code := f.seedLiveCampaign(t, "")

	const n = 40
	var wg sync.WaitGroup
	statuses := make([]int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st, _ := http.Get(f.ts.URL + "/api/v1/public/links/" + code)
			io.Copy(io.Discard, st.Body)
			st.Body.Close()
			statuses[i] = st.StatusCode
		}(i)
	}
	close(start)
	wg.Wait()
	for i, st := range statuses {
		if st != 200 {
			t.Fatalf("concurrent miss #%d = %d (breakdown)", i, st)
		}
	}
	if loads := f.s.PubCache.Loads(); loads != 1 {
		t.Fatalf("public loader ran %d times for one cold key, want exactly 1", loads)
	}

	// 看板并发 miss 同口径
	var dwg sync.WaitGroup
	dst := make([]int, n)
	dstart := make(chan struct{})
	for i := 0; i < n; i++ {
		dwg.Add(1)
		go func(i int) {
			defer dwg.Done()
			<-dstart
			st, body := f.dash(t, "sess-owner-a", f.tenA)
			_ = body
			dst[i] = st
		}(i)
	}
	close(dstart)
	dwg.Wait()
	for i, st := range dst {
		if st != 200 {
			t.Fatalf("dashboard concurrent miss #%d = %d", i, st)
		}
	}
	if loads := f.s.DashCache.Loads(); loads != 1 {
		t.Fatalf("dashboard loader ran %d times, want exactly 1", loads)
	}
}

// ---- 9. 负缓存一致性:未知短码稳定 404,绝不变成 5xx/200 --------------------------

func TestCacheUnknownCodeStable(t *testing.T) {
	f := newCacheFixture(t)
	for i := 0; i < 3; i++ {
		st, v := f.page(t, "0123456789AB")
		if st != 404 || v.State != "not_found" {
			t.Fatalf("unknown code #%d = %d %+v", i, st, v)
		}
	}
	if loads := f.s.PubCache.Loads(); loads != 1 {
		t.Fatalf("negative entry must be cached too, loads=%d", loads)
	}
}
