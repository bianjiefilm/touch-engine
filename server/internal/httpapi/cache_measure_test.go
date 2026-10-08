package httpapi

// HUI-2981【缓存收益 T1】同口径对照测量夹具(仅测试用,不影响任何生产路径)。
//
// 用途:以同一份种子数据、同一负载形状,对候选方案做对照测量:
//   A 现状基线   —— 本文件直接可跑(HUI2981_MEASURE=on go test -run TestMeasure -v)
//   C 本地缓存   —— 实现 FEATURE_PUBLIC_CACHE / FEATURE_DASHBOARD_CACHE 后同负载重跑
//   D 本地 Redis —— 独立探针(见 _reports;结构性裁决在对照报告)
//
// 负载形状(依据票面「同活动重复访问结构」):热点 10 个短码吃 90% 流量,
// 其余 590 个短码分摊 10%;每流 = 消费者页三连(GET 公共页 → GET 表单 →
// POST 浏览 beacon),另有低频看板读交叠(单写连接下的真实竞争)。
//
// SQL 计数:包一层计数驱动,统计每类请求的 SQL 语句数(含 Query/Exec;
// 显式事务内语句不计,只影响留资提交——它不在测量端点内)。

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/config"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
	"github.com/bianjiefilm/touch-engine/server/internal/identity"
	"github.com/bianjiefilm/touch-engine/server/internal/upload"
	"modernc.org/sqlite"
)

// ---- counting sqlite driver (measurement only) ---------------------------------

type countDriver struct {
	inner *sqlite.Driver
	n     *int64
}

func (d countDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return countConn{Conn: c, n: d.n}, nil
}

type countConn struct {
	driver.Conn
	n *int64
}

func (c countConn) count() { atomic.AddInt64(c.n, 1) }

func (c countConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.count()
	if qc, ok := c.Conn.(driver.QueryerContext); ok {
		return qc.QueryContext(ctx, q, args)
	}
	st, err := c.Conn.Prepare(q)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	rows, err := st.(driver.StmtQueryContext).QueryContext(ctx, toNamed(args))
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func (c countConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.count()
	if ec, ok := c.Conn.(driver.ExecerContext); ok {
		return ec.ExecContext(ctx, q, args)
	}
	st, err := c.Conn.Prepare(q)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return st.(driver.StmtExecContext).ExecContext(ctx, toNamed(args))
}

func (c countConn) Query(q string, args []driver.Value) (driver.Rows, error) {
	c.count()
	if qc, ok := c.Conn.(driver.Queryer); ok {
		return qc.Query(q, args)
	}
	st, err := c.Conn.Prepare(q)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return st.Query(args)
}

func (c countConn) Exec(q string, args []driver.Value) (driver.Result, error) {
	c.count()
	if ec, ok := c.Conn.(driver.Execer); ok {
		return ec.Exec(q, args)
	}
	st, err := c.Conn.Prepare(q)
	if err != nil {
		return nil, err
	}
	defer st.Close()
	return st.Exec(args)
}

func toNamed(args []driver.NamedValue) []driver.NamedValue { return args }

// ---- percentile helper ----------------------------------------------------------

type latencies struct{ xs []float64 }

func (l *latencies) add(d time.Duration) { l.xs = append(l.xs, float64(d.Microseconds())/1000.0) }

func (l *latencies) report() (p50, p95, p99 float64) {
	if len(l.xs) == 0 {
		return 0, 0, 0
	}
	sorted := append([]float64(nil), l.xs...)
	sort.Float64s(sorted)
	pick := func(p float64) float64 {
		i := int(p * float64(len(sorted)-1))
		return sorted[i]
	}
	return pick(0.50), pick(0.95), pick(0.99)
}

// ---- measurement fixture ----------------------------------------------------------

type measureEnv struct {
	s          *Server
	ts         *httptest.Server
	db         *sql.DB
	counter    *int64
	hotCodes   []string
	coldCodes  []string
	tenantID   string
	dashWindow [2]string
}

const (
	measureCampaigns = 30
	measureLinks     = 600 // 20 links per campaign
	measureHot       = 10  // hot codes (90% of traffic)
	measureViewRows  = 200_000
	measureLeadRows  = 20_000
	measureFlows     = 2400
	measureWorkers   = 8
	measureDashEvery = 150 * time.Millisecond
)

func newMeasureEnv(t *testing.T, extra func(*config.Config)) *measureEnv {
	t.Helper()
	path := t.TempDir() + "/measure.db"
	counter := new(int64)

	// migrate through a plain connection first (counting starts after seed)
	plain, err := db.Open(path)
	mustNoErr(t, err)
	seedMeasureData(t, plain)
	mustNoErr(t, plain.Close())

	d := sql.OpenDB(countConnector{dsn: "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", n: counter})
	d.SetMaxOpenConns(1)
	t.Cleanup(func() { d.Close() })
	if err := d.Ping(); err != nil {
		t.Fatalf("ping counting db: %v", err)
	}

	var tenantID string
	if err := d.QueryRow(`SELECT id FROM tenants LIMIT 1`).Scan(&tenantID); err != nil {
		t.Fatalf("tenant: %v", err)
	}

	cfg := config.Load(func(k string) string {
		switch k {
		case "TOUCH_INTERNAL_TOKEN":
			return "test-internal-secret"
		case "PLATFORM_IDENTITY_BASE_URL":
			return "http://identity.test"
		case "PLATFORM_IDENTITY_TOKEN":
			return "identity-token"
		case "PLATFORM_NOTIFY_BASE_URL":
			return "http://notify.test"
		case "PLATFORM_NOTIFY_TOKEN":
			return "notify-token"
		case "FEATURE_LEADS_CAPTURE":
			return "on"
		case "FEATURE_DASHBOARD":
			return "on"
		case "LEADS_TARGET_APP_ID":
			return "crm-test"
		case "LEADS_PHONE_PEPPER":
			return "pepper-test"
		}
		return ""
	})
	if extra != nil {
		extra(&cfg)
	}

	idsrv := fakeIdentity(t, map[string]identitySession{
		"sess-owner-a": {"usr_owner_a", "a@example.com"},
	})
	idc := &identity.Client{BaseURL: idsrv.URL, Token: "identity-token", AppID: "touch-engine", HTTP: idsrv.Client()}
	s := New(cfg, d, idc, &upload.Client{}, log.New(io.Discard, "", 0))
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	hot, cold := loadCodes(t, d, tenantID)
	now := time.Now().UTC()
	return &measureEnv{
		s: s, ts: ts, db: d, counter: counter,
		hotCodes: hot, coldCodes: cold, tenantID: tenantID,
		dashWindow: [2]string{now.Add(-30 * 24 * time.Hour).Format(time.RFC3339), now.Format(time.RFC3339)},
	}
}

type countConnector struct {
	dsn string
	n   *int64
}

func (c countConnector) Connect(context.Context) (driver.Conn, error) {
	d := &sqlite.Driver{}
	conn, err := d.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return countConn{Conn: conn, n: c.n}, nil
}

func (c countConnector) Driver() driver.Driver { return &sqlite.Driver{} }

func loadCodes(t *testing.T, d *sql.DB, tenantID string) (hot, cold []string) {
	t.Helper()
	rows, err := d.Query(`SELECT l.code FROM campaign_links l JOIN campaigns c ON c.id=l.campaign_id WHERE c.tenant_id=? ORDER BY l.rowid`, tenantID)
	mustNoErr(t, err)
	defer rows.Close()
	var all []string
	for rows.Next() {
		var code string
		mustNoErr(t, rows.Scan(&code))
		all = append(all, code)
	}
	mustNoErr(t, rows.Err())
	if len(all) < measureHot+10 {
		t.Fatalf("not enough links seeded: %d", len(all))
	}
	// hot = first 10 (spread over the first 10 campaigns); cold = the rest
	hot = all[:measureHot]
	cold = all[measureHot:]
	return hot, cold
}

// seedMeasureData writes the representative dataset directly (bulk, one tx).
func seedMeasureData(t *testing.T, d *sql.DB) {
	t.Helper()
	now := time.Now().UTC()
	day := func(offset int) string { return now.AddDate(0, 0, -offset).Format("2006-01-02") }
	ts := func(offset int) string { return now.AddDate(0, 0, -offset).Format(time.RFC3339) }

	mustNoErr(t, withTx(d, func(tx *sql.Tx) error {
		step := func(what string, err error) error {
			if err != nil {
				return fmt.Errorf("seed %s: %w", what, err)
			}
			return nil
		}
		if err := step("tenants", func() error {
			_, err := tx.Exec(`INSERT INTO tenants(id,name,created_at) VALUES('tnt_measure','测量商家',?)`, ts(120))
			return err
		}()); err != nil {
			return err
		}
		if err := step("members", func() error {
			_, err := tx.Exec(`INSERT INTO members(id,tenant_id,principal_ref,role,store_scope,enabled,display_name,created_by,created_at,updated_at)
				VALUES('mem_measure','tnt_measure','usr_owner_a','org_owner',NULL,1,'owner','test',?,?)`, ts(120), ts(120))
			return err
		}()); err != nil {
			return err
		}
		if err := step("stores", func() error {
			for i := 0; i < 2; i++ {
				if _, err := tx.Exec(`INSERT INTO stores(id,tenant_id,name,address,status,created_by,created_at,updated_at)
					VALUES(?,?,?,?,'active','test',?,?)`,
					fmt.Sprintf("sto_m%d", i), "tnt_measure", fmt.Sprintf("门店%d", i), "地址", ts(120), ts(120)); err != nil {
					return err
				}
			}
			return nil
		}()); err != nil {
			return err
		}
		if err := step("campaigns/links/forms", func() error {
			for c := 0; c < measureCampaigns; c++ {
				cid := fmt.Sprintf("cmp_m%02d", c)
				storeID := ""
				if c%3 == 0 {
					storeID = fmt.Sprintf("sto_m%d", c%2)
				}
				// hot campaigns (first 10) are active inside the window; the rest mixed
				status := "active"
				starts, ends := ts(2), ts(-90) // ends 90d in the future
				if c >= 20 {
					status, starts, ends = "paused", ts(2), ts(-90)
				}
				var storeArg any // NULL when unbound (FK discipline, mirrors store.nullable)
				if storeID != "" {
					storeArg = storeID
				}
				if _, err := tx.Exec(`INSERT INTO campaigns(id,tenant_id,title,public_content,status,starts_at,ends_at,store_id,created_by,created_at,updated_at)
					VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
					cid, "tnt_measure", fmt.Sprintf("活动%02d", c), "到店有礼,扫码即领", status, starts, ends,
					storeArg, "test", ts(120), ts(120)); err != nil {
					return fmt.Errorf("campaign %d: %w", c, err)
				}
				if c%3 == 0 {
					if _, err := tx.Exec(`INSERT INTO lead_forms(id,tenant_id,campaign_id,enabled,created_by,created_at,updated_at)
						VALUES(?,?,?,1,'test',?,?)`, "lfm_"+cid, "tnt_measure", cid, ts(120), ts(120)); err != nil {
						return fmt.Errorf("form %d: %w", c, err)
					}
				}
				for l := 0; l < measureLinks/measureCampaigns; l++ {
					if _, err := tx.Exec(`INSERT INTO campaign_links(id,tenant_id,campaign_id,code,enabled,created_by,created_at,updated_at)
						VALUES(?,?,?,?,1,'test',?,?)`,
						fmt.Sprintf("lnk_m%02d_%02d", c, l), "tnt_measure", cid, measureCode(c, l), ts(120), ts(120)); err != nil {
						return fmt.Errorf("link %d/%d: %w", c, l, err)
					}
				}
			}
			return nil
		}()); err != nil {
			return err
		}
		// public_view_stats: 90 days × 600 codes, views weighted toward hot codes
		insertStat, err := tx.Prepare(`INSERT INTO public_view_stats(code,day,channel,views) VALUES(?,?,?,?)`)
		if err != nil {
			return err
		}
		defer insertStat.Close()
		chanOf := func(i int) string { return [4]string{"web", "wecom", "qr", "nfc"}[i%4] }
		written := 0
		for dc := 0; dc < 90 && written < measureViewRows; dc++ {
			for c := 0; c < measureCampaigns && written < measureViewRows; c++ {
				weight := 1
				if c < measureHot {
					weight = 20 // hot campaigns get ~20× the code×day cells
				}
				for w := 0; w < weight && written < measureViewRows; w++ {
					code := measureCode(c, w%20)
					if _, err := insertStat.Exec(code, day(dc), chanOf(w), 1+w%9); err != nil {
						return err
					}
					written++
				}
			}
		}
		// lead_submissions: 20k rows spread over 90 days (mostly older)
		insertLead, err := tx.Prepare(`INSERT INTO lead_submissions(id,tenant_id,campaign_id,store_id,link_id,form_id,submission_ref,dedup_key,name,phone,marketing_optin,consent_notice_version,consent_at,consent_ip_fp,sync_state,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
		if err != nil {
			return err
		}
		defer insertLead.Close()
		for i := 0; i < measureLeadRows; i++ {
			c := i % measureCampaigns
			st := "crm_received"
			switch i % 7 {
			case 0:
				st = "accepted"
			case 1:
				st = "pending_sync"
			case 2:
				st = "revoked"
			case 3:
				st = "rejected"
			}
			dayOff := 1 + i%89
			cat := ts(dayOff)
			if _, err := insertLead.Exec(
				fmt.Sprintf("lsub_m%06d", i), "tnt_measure", fmt.Sprintf("cmp_m%02d", c), "", "", "",
				fmt.Sprintf("ref_m%06d", i), fmt.Sprintf("dk_m%06d", i), "测试", "13800000000", 0, "v1",
				cat, "fp", st, cat, cat); err != nil {
				return err
			}
		}
		return nil
	}))
}

// measureCode builds a valid Crockford-base32 12-char code deterministically
// and collision-free over (c,l): the last 3 chars encode n=c*100+l in base 32.
func measureCode(c, l int) string {
	const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	n := c*100 + l
	out := make([]byte, 12)
	for i := 0; i < 9; i++ {
		out[i] = alphabet[(c*13+l*7+i*11)%len(alphabet)]
	}
	out[9] = alphabet[n/1024%32]
	out[10] = alphabet[n/32%32]
	out[11] = alphabet[n%32]
	return string(out)
}

func withTx(d *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ---- the measurement itself -------------------------------------------------------

// guestGet / guestPost are raw fast clients (no JSON decode) for the load loop.
func guestGet(url string) (int, time.Duration) {
	start := time.Now()
	res, err := http.Get(url)
	if err != nil {
		return -1, time.Since(start)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode, time.Since(start)
}

func guestPost(url string) (int, time.Duration) {
	start := time.Now()
	res, err := http.Post(url, "application/json", strings.NewReader(`{"channel":"qr"}`))
	if err != nil {
		return -1, time.Since(start)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode, time.Since(start)
}

func authedDashboard(url, session, tenant, wStart, wEnd string) (int, time.Duration) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-Internal-Token", "test-internal-secret")
	req.Header.Set("X-Session-Token", session)
	req.Header.Set("X-Tenant-ID", tenant)
	start := time.Now()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1, time.Since(start)
	}
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res.StatusCode, time.Since(start)
}

func sqlCount(counter *int64) int64 { return atomic.LoadInt64(counter) }

func TestMeasureBaseline(t *testing.T) {
	if os.Getenv("HUI2981_MEASURE") != "on" {
		t.Skip("measurement harness; run with HUI2981_MEASURE=on")
	}
	env := newMeasureEnv(t, nil)
	runMeasureWorkload(t, env, false)
}

// TestMeasureCacheOn is the C-case arm of the comparison: IDENTICAL seed and
// load with FEATURE_PUBLIC_CACHE / FEATURE_DASHBOARD_CACHE on. Reports the
// same metrics plus cache hit rates and loader (backfill) counts so the
// before/after columns of the comparison report come from one harness.
func TestMeasureCacheOn(t *testing.T) {
	if os.Getenv("HUI2981_MEASURE") != "on" {
		t.Skip("measurement harness; run with HUI2981_MEASURE=on")
	}
	env := newMeasureEnv(t, func(c *config.Config) {
		c.FeaturePublicCache = true
		c.FeatureDashboardCache = true
	})
	runMeasureWorkload(t, env, true)
}

// runMeasureWorkload is the shared A/C harness: warm-up, isolated per-request
// SQL counts, then the hot-spot load (90/10) with a concurrent dashboard
// reader on the same single SQLite connection.
func runMeasureWorkload(t *testing.T, env *measureEnv, cacheOn bool) {
	t.Helper()

	// ---- warm-up: fill any lazily built structures (f.IDENTITY etc.) ----
	for i := 0; i < 30; i++ {
		code := env.hotCodes[i%len(env.hotCodes)]
		guestGet(env.ts.URL + "/api/v1/public/links/" + code)
	}

	// ---- SQL statements per request type (isolated, post-warm) ----
	page := func() int64 {
		before := sqlCount(env.counter)
		if st, _ := guestGet(env.ts.URL + "/api/v1/public/links/" + env.hotCodes[0]); st != 200 {
			t.Fatalf("page get = %d", st)
		}
		return sqlCount(env.counter) - before
	}()
	form := func() int64 {
		before := sqlCount(env.counter)
		if st, _ := guestGet(env.ts.URL + "/api/v1/public/links/" + env.hotCodes[0] + "/lead-form"); st != 200 {
			t.Fatalf("lead form = %d", st)
		}
		return sqlCount(env.counter) - before
	}()
	beacon := func() int64 {
		before := sqlCount(env.counter)
		if st, _ := guestPost(env.ts.URL + "/api/v1/public/links/" + env.hotCodes[0] + "/view-events"); st != 204 {
			t.Fatalf("view beacon = %d", st)
		}
		return sqlCount(env.counter) - before
	}()
	dash := func() int64 {
		if cacheOn { // pre-warm so the isolated measure observes a HIT path
			authedDashboard(env.ts.URL+"/api/v1/dashboard?window_start="+env.dashWindow[0]+"&window_end="+env.dashWindow[1], "sess-owner-a", env.tenantID, env.dashWindow[0], env.dashWindow[1])
		}
		before := sqlCount(env.counter)
		st, _ := authedDashboard(env.ts.URL+"/api/v1/dashboard?window_start="+env.dashWindow[0]+"&window_end="+env.dashWindow[1], "sess-owner-a", env.tenantID, env.dashWindow[0], env.dashWindow[1])
		if st != 200 {
			t.Fatalf("dashboard = %d", st)
		}
		return sqlCount(env.counter) - before
	}()

	// ---- load: page-view flows with a concurrent dashboard reader ----
	pageLat := &latencies{}
	formLat := &latencies{}
	beaconLat := &latencies{}
	dashLat := &latencies{}
	stopDash := make(chan struct{})
	var dashWG sync.WaitGroup
	dashWG.Add(1)
	go func() {
		defer dashWG.Done()
		tick := time.NewTicker(measureDashEvery)
		defer tick.Stop()
		for {
			select {
			case <-stopDash:
				return
			case <-tick.C:
				st, d := authedDashboard(env.ts.URL+"/api/v1/dashboard?window_start="+env.dashWindow[0]+"&window_end="+env.dashWindow[1],
					"sess-owner-a", env.tenantID, env.dashWindow[0], env.dashWindow[1])
				if st == 200 {
					dashLat.add(d)
				}
			}
		}
	}()

	// runLoadPass is one full sweep of the flow load (deterministic per-worker
	// seeds, identical shape every pass).
	runLoadPass := func(pageLat, formLat, beaconLat *latencies) time.Duration {
		perWorker := measureFlows / measureWorkers
		start := time.Now()
		var wg sync.WaitGroup
		for w := 0; w < measureWorkers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				lrng := rand.New(rand.NewSource(int64(w)*7919 + 13))
				for i := 0; i < perWorker; i++ {
					var code string
					if lrng.Intn(10) < 9 {
						code = env.hotCodes[lrng.Intn(len(env.hotCodes))]
					} else {
						code = env.coldCodes[lrng.Intn(len(env.coldCodes))]
					}
					if st, d := guestGet(env.ts.URL + "/api/v1/public/links/" + code); st == 200 {
						pageLat.add(d)
					}
					if st, d := guestGet(env.ts.URL + "/api/v1/public/links/" + code + "/lead-form"); st == 200 {
						formLat.add(d)
					}
					if st, d := guestPost(env.ts.URL + "/api/v1/public/links/" + code + "/view-events"); st == 204 {
						beaconLat.add(d)
					}
				}
			}(w)
		}
		wg.Wait()
		return time.Since(start)
	}

	pass2Page, pass2Form, pass2Beacon := &latencies{}, &latencies{}, &latencies{}
	elapsed := runLoadPass(pageLat, formLat, beaconLat)
	pass2Elapsed := time.Duration(0)
	if cacheOn { // steady-state pass: same shape, warm cache
		pass2Elapsed = runLoadPass(pass2Page, pass2Form, pass2Beacon)
	}
	close(stopDash)
	dashWG.Wait()

	reportArm := func(tag string, pageLat, formLat, beaconLat *latencies, elapsed time.Duration) {
		p50, p95, p99 := pageLat.report()
		f50, f95, f99 := formLat.report()
		b50, b95, b99 := beaconLat.report()
		total := pageLat.len() + formLat.len() + beaconLat.len()
		t.Logf("HUI2981-MEASURE arm=%s workload=%d flows, workers=%d, elapsed=%s, requests=%d (%.0f req/s)",
			tag, measureFlows, measureWorkers, elapsed.Round(time.Millisecond), total, float64(total)/elapsed.Seconds())
		t.Logf("HUI2981-PERC arm=%s page_get p50=%.3f p95=%.3f p99=%.3f ms (n=%d)", tag, p50, p95, p99, pageLat.len())
		t.Logf("HUI2981-PERC arm=%s lead_form p50=%.3f p95=%.3f p99=%.3f ms (n=%d)", tag, f50, f95, f99, formLat.len())
		t.Logf("HUI2981-PERC arm=%s view_beacon p50=%.3f p95=%.3f p99=%.3f ms (n=%d)", tag, b50, b95, b99, beaconLat.len())
	}
	d50, d95, d99 := dashLat.report()
	if cacheOn {
		t.Logf("HUI2981-SQL arm=C page_get=%d stmts(hit), lead_form=%d, view_beacon=%d, dashboard=%d(hit)", page, form, beacon, dash)
		t.Logf("HUI2981-PERC arm=C dashboard p50=%.3f p95=%.3f p99=%.3f ms (n=%d)", d50, d95, d99, dashLat.len())
		reportArm("C-pass1-cold", pageLat, formLat, beaconLat, elapsed)
		reportArm("C-pass2-steady", pass2Page, pass2Form, pass2Beacon, pass2Elapsed)
	} else {
		t.Logf("HUI2981-SQL arm=A page_get=%d stmts, lead_form=%d, view_beacon=%d, dashboard=%d", page, form, beacon, dash)
		t.Logf("HUI2981-PERC arm=A dashboard p50=%.3f p95=%.3f p99=%.3f ms (n=%d)", d50, d95, d99, dashLat.len())
		reportArm("A", pageLat, formLat, beaconLat, elapsed)
	}
	if cacheOn {
		pubHits, pubMisses, pubLoads := env.s.PubCache.Hits(), env.s.PubCache.Misses(), env.s.PubCache.Loads()
		dashHits, dashMisses, dashLoads := env.s.DashCache.Hits(), env.s.DashCache.Misses(), env.s.DashCache.Loads()
		pubRate, dashRate := 0.0, 0.0
		if pubHits+pubMisses > 0 {
			pubRate = 100 * float64(pubHits) / float64(pubHits+pubMisses)
		}
		if dashHits+dashMisses > 0 {
			dashRate = 100 * float64(dashHits) / float64(dashHits+dashMisses)
		}
		t.Logf("HUI2981-CACHE arm=C public hits=%d misses=%d loads=%d hit_rate=%.2f%% entries=%d",
			pubHits, pubMisses, pubLoads, pubRate, env.s.PubCache.Len())
		t.Logf("HUI2981-CACHE arm=C dashboard hits=%d misses=%d loads=%d hit_rate=%.2f%% entries=%d",
			dashHits, dashMisses, dashLoads, dashRate, env.s.DashCache.Len())
	}
}

// len returns the sample count.
func (l *latencies) len() int { return len(l.xs) }
