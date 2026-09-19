package httpapi

// HUI-1665 FEAT-0166 NFC 标签管理 —— 真实非生产 E2E(真 sqlite + stub identity):
//   - 组 → 批量 50 标签(绑活动短码+门店,rotate 5 link)→ 列表/筛选 →
//     导出 CSV 回读(行数/列/URL/无敏感字段/注入转义/uid_hint 列);
//   - 停用标签 → 公共短码 URL 404 停用态;恢复 → 复用;换绑活动 → URL 随 link 变化;
//   - A/B 隔离、跨租户 404、staff 403、游客 401(伪造 query 不提权);
//   - uid_hint 回填 → 仅管理面可见,公共路由零泄漏;
//   - PUBLIC_BASE_URL 缺失 → 503 fail-closed。

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// seedNFCTenantCampaign:在租户 A 建门店 + active 活动 + nLinks 条短码。
func seedNFCTenantCampaign(t *testing.T, f *fixture, nLinks int) (storeID, campaignID string, linkIDs, codes []string) {
	t.Helper()
	status, _, st := f.do(t, "POST", "/api/v1/stores", "sess-owner-a", f.tenA, `{"name":"旗舰店","address":"北京市"}`)
	mustEqual(t, status, http.StatusCreated)
	storeID = st["id"].(string)

	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"周年庆","public_content":"到店有礼","starts_at":"2026-09-01T00:00:00Z","ends_at":"2026-09-30T00:00:00Z"}`)
	mustEqual(t, status, http.StatusCreated)
	campaignID = cmp["id"].(string)
	for i := 0; i < nLinks; i++ {
		status, _, lnk := f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/links", "sess-owner-a", f.tenA, "")
		mustEqual(t, status, http.StatusCreated)
		linkIDs = append(linkIDs, lnk["id"].(string))
		codes = append(codes, lnk["code"].(string))
	}
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	return storeID, campaignID, linkIDs, codes
}

func batchReq(campaignID string, linkIDs []string, count int, storeID, groupID, prefix, mode string) string {
	links := make([]string, 0, len(linkIDs))
	for _, id := range linkIDs {
		links = append(links, "\""+id+"\"")
	}
	body := fmt.Sprintf(`{"campaign_id":%q,"link_ids":[%s],"bind_mode":%q,"count":%d`, campaignID, strings.Join(links, ","), mode, count)
	if storeID != "" {
		body += `,"store_id":"` + storeID + `"`
	}
	if groupID != "" {
		body += `,"group_id":"` + groupID + `"`
	}
	if prefix != "" {
		body += `,"label_prefix":"` + prefix + `"`
	}
	return body + "}"
}

func parseCSV(t *testing.T, raw []byte) [][]string {
	t.Helper()
	if !bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		t.Fatal("CSV missing UTF-8 BOM")
	}
	rd := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})))
	rd.FieldsPerRecord = -1
	recs, err := rd.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	return recs
}

func TestNFCTagGroupLifecycleAPI(t *testing.T) {
	f := fixtureWithQR(t, nil)
	storeID, campaignID, linkIDs, _ := seedNFCTenantCampaign(t, f, 1)

	status, _, g := f.do(t, "POST", "/api/v1/nfc/tag-groups", "sess-owner-a", f.tenA, `{"name":"门店一组"}`)
	mustEqual(t, status, http.StatusCreated)
	groupID := g["id"].(string)

	// 重名 → 409
	status, _, body := f.do(t, "POST", "/api/v1/nfc/tag-groups", "sess-owner-a", f.tenA, `{"name":"门店一组"}`)
	if status != http.StatusConflict || body["error"] != "group_name_taken" {
		t.Fatalf("duplicate group = %d %v, want 409 group_name_taken", status, body)
	}
	// 空名 → 400
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tag-groups", "sess-owner-a", f.tenA, `{"name":"  "}`)
	mustEqual(t, status, http.StatusBadRequest)

	status, _, lst := f.do(t, "GET", "/api/v1/nfc/tag-groups", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if items := lst["items"].([]any); len(items) != 1 {
		t.Fatalf("groups = %v", lst)
	}

	// 挂组批量 → 删组 → 标签保留且未分组
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs[:1], 2, storeID, groupID, "标签", "shared"))
	mustEqual(t, status, http.StatusCreated)
	status, _, _ = f.do(t, "DELETE", "/api/v1/nfc/tag-groups/"+groupID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	status, _, tags := f.do(t, "GET", "/api/v1/nfc/tags", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(tags["items"].([]any)); got != 2 {
		t.Fatalf("tags after group delete = %d, want 2", got)
	}
	for _, it := range tags["items"].([]any) {
		if m := it.(map[string]any); m["group_id"] != nil && m["group_id"] != "" {
			t.Fatalf("tag still grouped: %v", m)
		}
	}
	// 再删 → 404
	status, _, _ = f.do(t, "DELETE", "/api/v1/nfc/tag-groups/"+groupID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusNotFound)
}

// TestNFCTagBatchAndCSVExportRoundTrip:票面 E2E 主干。
func TestNFCTagBatchAndCSVExportRoundTrip(t *testing.T) {
	f := fixtureWithQR(t, nil)
	storeID, campaignID, linkIDs, codes := seedNFCTenantCampaign(t, f, 5)

	// 组
	status, _, g := f.do(t, "POST", "/api/v1/nfc/tag-groups", "sess-owner-a", f.tenA, `{"name":"收银台"}`)
	mustEqual(t, status, http.StatusCreated)
	groupID := g["id"].(string)

	// 批量 50:rotate 5 link + 门店 + 分组
	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs, 50, storeID, groupID, "周年庆", "rotate"))
	mustEqual(t, status, http.StatusCreated)
	if n := int(batch["count"].(float64)); n != 50 {
		t.Fatalf("batch count = %d, want 50", n)
	}
	items := batch["items"].([]any)
	first := items[0].(map[string]any)
	if first["label"] != "周年庆-001" || first["status"] != "active" {
		t.Fatalf("first tag = %v", first)
	}
	// rotate:第 i 条绑 link[i%5]
	for i, it := range items {
		m := it.(map[string]any)
		if m["code"] != codes[i%5] {
			t.Fatalf("tag %d code = %v, want %s", i, m["code"], codes[i%5])
		}
	}

	// 列表 + 筛选
	status, _, lst := f.do(t, "GET", "/api/v1/nfc/tags?campaign_id="+campaignID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(lst["items"].([]any)); got != 50 {
		t.Fatalf("list = %d, want 50", got)
	}
	status, _, lst = f.do(t, "GET", "/api/v1/nfc/tags?group_id="+groupID+"&store_id="+storeID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(lst["items"].([]any)); got != 50 {
		t.Fatalf("filtered list = %d, want 50", got)
	}
	status, _, lst = f.do(t, "GET", "/api/v1/nfc/tags?status=bogus", "sess-owner-a", f.tenA, "")
	if status != http.StatusBadRequest {
		t.Fatalf("bogus status filter = %d, want 400", status)
	}
	// 视图 JOIN 字段:门店名/组名/活动 id
	status, _, lst = f.do(t, "GET", "/api/v1/nfc/tags", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if m := lst["items"].([]any)[0].(map[string]any); m["store_name"] != "旗舰店" || m["group_name"] != "收银台" || m["campaign_id"] != campaignID {
		t.Fatalf("view join fields = %v", m)
	}

	// 注入标签 + uid_hint 回填(管理面)
	tagID := items[0].(map[string]any)["id"].(string)
	status, _, _ = f.do(t, "PATCH", "/api/v1/nfc/tags/"+tagID, "sess-owner-a", f.tenA,
		`{"label":"=cmd|' /C calc'!A0","uid_hint":"04:A2:2F:9E"}`)
	mustEqual(t, status, http.StatusOK)

	// 导出 CSV 回读
	status, hdr, raw := f.doRaw(t, "GET", "/api/v1/nfc/tags/export.csv?campaign_id="+campaignID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("content-type = %q", ct)
	}
	if cd := hdr.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "nfc-tags-") {
		t.Fatalf("content-disposition = %q", cd)
	}
	recs := parseCSV(t, raw)
	if len(recs) != 51 {
		t.Fatalf("csv rows = %d, want 51 (header+50)", len(recs))
	}
	wantHeader := []string{"label", "short_code", "url", "store", "group", "uid_hint"}
	for i, h := range recs[0] {
		if h != wantHeader[i] {
			t.Fatalf("header = %v", recs[0])
		}
	}
	if len(recs[1]) != 6 {
		t.Fatalf("row width = %d, want 6", len(recs[1]))
	}
	urlSeen := map[string]bool{}
	uidRows := 0
	for _, row := range recs[1:] {
		code := strings.TrimPrefix(row[2], "https://h5.test/c/")
		if row[2] != "https://h5.test/c/"+code || len(code) != 12 {
			t.Fatalf("bad url %q", row[2])
		}
		urlSeen[row[2]] = true
		if row[1] != code {
			t.Fatalf("short_code %q != url code %q", row[1], code)
		}
		if row[3] != "旗舰店" {
			t.Fatalf("store column = %q", row[3])
		}
		if row[5] == "04:A2:2F:9E" {
			uidRows++
		}
	}
	if len(urlSeen) != 5 {
		t.Fatalf("distinct urls = %d, want 5 (rotate)", len(urlSeen))
	}
	if uidRows != 1 {
		t.Fatalf("uid_hint column rows = %d, want 1 (rest empty)", uidRows)
	}
	// 注入字段被 ' 前缀转义(按内容扫描,不依赖行序)
	escaped := 0
	for _, row := range recs[1:] {
		if row[0] == "'=cmd|' /C calc'!A0" {
			escaped++
		}
	}
	if escaped != 1 {
		t.Fatalf("escaped injection labels = %d, want 1", escaped)
	}
	// 无敏感字段:整个文件不含租户/主体/会话/凭证形态
	lower := strings.ToLower(string(raw))
	for _, banned := range []string{"tnt_", "usr_", "sess-", "nfcg_", "sto_", "cmp_", "lnk_", "token", "secret"} {
		if strings.Contains(lower, banned) {
			t.Fatalf("csv leaks %q:\n%s", banned, raw)
		}
	}
}

func TestNFCTagStatusPropagatesToPublicRoute(t *testing.T) {
	f := fixtureWithQR(t, nil)
	_, campaignID, linkIDs, codes := seedNFCTenantCampaign(t, f, 1)

	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs[:1], 1, "", "", "", "shared"))
	mustEqual(t, status, http.StatusCreated)
	tagID := batch["items"].([]any)[0].(map[string]any)["id"].(string)
	code := codes[0]
	pub := "/api/v1/public/links/" + code

	// 初始 available
	status, _, view := f.do(t, "GET", pub, "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["state"] != "available" {
		t.Fatalf("initial public = %v", view)
	}

	// 停用标签 → 公共短码 404 停用态
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/"+tagID+"/status", "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, view = f.do(t, "GET", pub, "", "", "")
	mustEqual(t, status, http.StatusNotFound)
	if view["state"] != "link_disabled" {
		t.Fatalf("disabled public = %v, want link_disabled", view)
	}
	// 停用文案为既有白名单形状(state 字段,无内容泄漏)
	if len(view) != 1 {
		t.Fatalf("disabled view = %v, want state only", view)
	}

	// 恢复 → 复用
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/"+tagID+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)
	status, _, view = f.do(t, "GET", pub, "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["state"] != "available" {
		t.Fatalf("restored public = %v", view)
	}
	// 坏状态 → 400
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/"+tagID+"/status", "sess-owner-a", f.tenA, `{"status":"paused"}`)
	mustEqual(t, status, http.StatusBadRequest)
}

// TestNFCTagRebindChangesURL:换绑活动 → URL 随 link 变化。
func TestNFCTagRebindChangesURL(t *testing.T) {
	f := fixtureWithQR(t, nil)
	_, campaignID, linkIDs, codes := seedNFCTenantCampaign(t, f, 1)

	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs[:1], 1, "", "", "", "shared"))
	mustEqual(t, status, http.StatusCreated)
	tagID := batch["items"].([]any)[0].(map[string]any)["id"].(string)

	// 第二个活动 + link
	status, _, c2 := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA, `{"title":"春季展"}`)
	mustEqual(t, status, http.StatusCreated)
	campaign2 := c2["id"].(string)
	status, _, l2 := f.do(t, "POST", "/api/v1/campaigns/"+campaign2+"/links", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusCreated)
	link2 := l2["id"].(string)
	code2 := l2["code"].(string)
	status, _, _ = f.do(t, "POST", "/api/v1/campaigns/"+campaign2+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	mustEqual(t, status, http.StatusOK)

	// PATCH 换绑:campaign 直改被拒(绑定以 link 为准)
	status, _, body := f.do(t, "PATCH", "/api/v1/nfc/tags/"+tagID, "sess-owner-a", f.tenA, `{"campaign_id":"`+campaign2+`"}`)
	if status != http.StatusBadRequest || body["error"] != "rebind_by_link" {
		t.Fatalf("campaign patch = %d %v", status, body)
	}
	// status 直改被拒
	status, _, body = f.do(t, "PATCH", "/api/v1/nfc/tags/"+tagID, "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	if status != http.StatusBadRequest || body["error"] != "use_status_endpoint" {
		t.Fatalf("status patch = %d %v", status, body)
	}

	// 换绑 link → 管理面视图 code/campaign 变化
	status, _, v := f.do(t, "PATCH", "/api/v1/nfc/tags/"+tagID, "sess-owner-a", f.tenA, `{"link_id":"`+link2+`"}`)
	mustEqual(t, status, http.StatusOK)
	if v["code"] != code2 || v["campaign_id"] != campaign2 {
		t.Fatalf("rebound view = %v", v)
	}
	// 导出 URL 随 link 变化
	status, _, raw := f.doRaw(t, "GET", "/api/v1/nfc/tags/export.csv", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	recs := parseCSV(t, raw)
	if len(recs) != 2 || recs[1][2] != "https://h5.test/c/"+code2 {
		t.Fatalf("export after rebind = %v, want url %s", recs, "https://h5.test/c/"+code2)
	}
	// 新短码公共面 available;旧短码仍归旧活动(管理面记录移走,链接模型不变)
	status, _, view := f.do(t, "GET", "/api/v1/public/links/"+code2, "", "", "")
	mustEqual(t, status, http.StatusOK)
	if view["state"] != "available" {
		t.Fatalf("new code public = %v", view)
	}
	status, _, view = f.do(t, "GET", "/api/v1/public/links/"+codes[0], "", "", "")
	mustEqual(t, status, http.StatusOK) // 旧 link 依旧 active
	if view["state"] != "available" {
		t.Fatalf("old code public = %v", view)
	}
	// 换绑到不存在的 link → 404
	status, _, _ = f.do(t, "PATCH", "/api/v1/nfc/tags/"+tagID, "sess-owner-a", f.tenA, `{"link_id":"lnk_missing"}`)
	mustEqual(t, status, http.StatusNotFound)
}

// TestNFCTagPermissionMatrix:游客 401 / staff 403(伪造 query 不提权)/ 跨租户 404 / 列表隔离。
func TestNFCTagPermissionMatrix(t *testing.T) {
	f := fixtureWithQR(t, nil)
	storeID, campaignID, linkIDs, _ := seedNFCTenantCampaign(t, f, 1)
	status, _, g := f.do(t, "POST", "/api/v1/nfc/tag-groups", "sess-owner-a", f.tenA, `{"name":"g"}`)
	mustEqual(t, status, http.StatusCreated)
	groupID := g["id"].(string)
	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs[:1], 2, storeID, groupID, "T", "shared"))
	mustEqual(t, status, http.StatusCreated)
	tagID := batch["items"].([]any)[0].(map[string]any)["id"].(string)

	paths := []struct{ method, path string }{
		{"GET", "/api/v1/nfc/tag-groups"},
		{"POST", "/api/v1/nfc/tag-groups"},
		{"DELETE", "/api/v1/nfc/tag-groups/" + groupID},
		{"GET", "/api/v1/nfc/tags"},
		{"POST", "/api/v1/nfc/tags/batch"},
		{"GET", "/api/v1/nfc/tags/" + tagID},
		{"PATCH", "/api/v1/nfc/tags/" + tagID},
		{"DELETE", "/api/v1/nfc/tags/" + tagID},
		{"POST", "/api/v1/nfc/tags/" + tagID + "/status"},
		{"GET", "/api/v1/nfc/tags/export.csv"},
	}
	bodyFor := func(method string) string {
		if method == http.MethodGet || method == http.MethodDelete {
			return ""
		}
		return "{}"
	}
	for _, p := range paths {
		// 游客 → 401
		status, _, _ := f.doRaw(t, p.method, p.path, "", f.tenA, bodyFor(p.method))
		if status != http.StatusUnauthorized {
			t.Fatalf("guest %s %s = %d, want 401", p.method, p.path, status)
		}
		// staff → 403(伪造提权 query 也不行)
		status, _, body := f.do(t, p.method, p.path+"?role=owner&admin=1", "sess-staff-a", f.tenA, bodyFor(p.method))
		if status != http.StatusForbidden || body["error"] != "forbidden" {
			t.Fatalf("staff %s %s = %d %v, want 403 forbidden", p.method, p.path, status, body)
		}
	}

	// owner B(另一租户):A 的记录全部不可见 → 404
	status, _, _ = f.doRaw(t, "GET", "/api/v1/nfc/tags/"+tagID, "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusNotFound)
	status, _, _ = f.doRaw(t, "DELETE", "/api/v1/nfc/tags/"+tagID, "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusNotFound)
	status, _, _ = f.doRaw(t, "POST", "/api/v1/nfc/tags/"+tagID+"/status", "sess-owner-b", f.tenB, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusNotFound)
	status, _, _ = f.doRaw(t, "PATCH", "/api/v1/nfc/tags/"+tagID, "sess-owner-b", f.tenB, `{"label":"x"}`)
	mustEqual(t, status, http.StatusNotFound)
	status, _, _ = f.doRaw(t, "DELETE", "/api/v1/nfc/tag-groups/"+groupID, "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusNotFound)
	// B 的批量引用 A 的活动/链接 → 404(租户内不可见)
	status, _, _ = f.doRaw(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-b", f.tenB,
		batchReq(campaignID, linkIDs[:1], 1, "", "", "", "shared"))
	mustEqual(t, status, http.StatusNotFound)

	// 列表互不可见:B 的列表为空
	status, _, lst := f.do(t, "GET", "/api/v1/nfc/tags", "sess-owner-b", f.tenB, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(lst["items"].([]any)); got != 0 {
		t.Fatalf("tenant B sees %d tags, want 0", got)
	}
	// A 停用自己的标签不受 B 的请求影响
	status, _, _ = f.do(t, "POST", "/api/v1/nfc/tags/"+tagID+"/status", "sess-owner-b", f.tenB, `{"status":"disabled"}`)
	mustEqual(t, status, http.StatusNotFound)
	status, _, v := f.do(t, "GET", "/api/v1/nfc/tags/"+tagID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if v["status"] != "active" {
		t.Fatalf("cross-tenant status flip leaked: %v", v)
	}
}

// TestNFCUIDHintNeverLeaksToPublic:uid_hint 只在管理面与导出列,公共路由零泄漏。
func TestNFCUIDHintNeverLeaksToPublic(t *testing.T) {
	f := fixtureWithQR(t, nil)
	_, campaignID, linkIDs, codes := seedNFCTenantCampaign(t, f, 1)
	status, _, batch := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs[:1], 1, "", "", "", "shared"))
	mustEqual(t, status, http.StatusCreated)
	tagID := batch["items"].([]any)[0].(map[string]any)["id"].(string)

	// 批量产物 uid_hint 为空(写入前留空)
	status, _, v := f.do(t, "GET", "/api/v1/nfc/tags/"+tagID, "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if uid, ok := v["uid_hint"]; ok && uid != "" {
		t.Fatalf("fresh tag uid_hint = %v, want empty", uid)
	}

	// 回填
	status, _, v = f.do(t, "PATCH", "/api/v1/nfc/tags/"+tagID, "sess-owner-a", f.tenA, `{"uid_hint":"04:A2:2F:9E:11"}`)
	mustEqual(t, status, http.StatusOK)
	if v["uid_hint"] != "04:A2:2F:9E:11" {
		t.Fatalf("backfilled uid_hint = %v", v["uid_hint"])
	}

	// 公共路由(available 与停用两态)响应零泄漏
	status, _, raw := f.doRaw(t, "GET", "/api/v1/public/links/"+codes[0], "", "", "")
	mustEqual(t, status, http.StatusOK)
	if strings.Contains(strings.ToLower(string(raw)), "uid_hint") || strings.Contains(string(raw), "04:A2:2F") {
		t.Fatalf("public available view leaks uid_hint: %s", raw)
	}
	f.do(t, "POST", "/api/v1/nfc/tags/"+tagID+"/status", "sess-owner-a", f.tenA, `{"status":"disabled"}`)
	status, _, raw = f.doRaw(t, "GET", "/api/v1/public/links/"+codes[0], "", "", "")
	mustEqual(t, status, http.StatusNotFound)
	var view map[string]any
	if err := json.Unmarshal(raw, &view); err != nil || view["state"] != "link_disabled" {
		t.Fatalf("disabled view = %v (%v)", view, err)
	}
	if strings.Contains(strings.ToLower(string(raw)), "uid_hint") || strings.Contains(string(raw), "04:A2:2F") {
		t.Fatalf("public disabled view leaks uid_hint: %s", raw)
	}
	// 公共载荷仍是白名单形状(state 单字段)
	if len(view) != 1 {
		t.Fatalf("disabled view keys = %v, want state only", view)
	}
}

// TestNFCTagBatchValidation:数量/模式/链接引用的服务端校验。
func TestNFCTagBatchValidation(t *testing.T) {
	f := fixtureWithQR(t, nil)
	_, campaignID, linkIDs, _ := seedNFCTenantCampaign(t, f, 2)

	cases := []struct {
		name string
		body string
		code string
	}{
		{"count 0", batchReq(campaignID, linkIDs[:1], 0, "", "", "", "shared"), "bad_count"},
		{"count 501", batchReq(campaignID, linkIDs[:1], 501, "", "", "", "shared"), "bad_count"},
		{"count 500 ok", batchReq(campaignID, linkIDs[:1], 500, "", "", "", "shared"), ""},
		{"bad mode", batchReq(campaignID, linkIDs[:1], 1, "", "", "", "nope"), "bad_bind_mode"},
		{"no links", fmt.Sprintf(`{"campaign_id":%q,"link_ids":[],"bind_mode":"shared","count":1}`, campaignID), "bad_links"},
		{"shared multi links", batchReq(campaignID, linkIDs, 3, "", "", "", "shared"), "bad_links"},
		{"missing campaign", batchReq("cmp_missing", linkIDs[:1], 1, "", "", "", "shared"), "not_found"},
	}
	for _, tc := range cases {
		status, _, body := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA, tc.body)
		if tc.code == "" {
			if status != http.StatusCreated {
				t.Fatalf("%s: %d %v, want 201", tc.name, status, body)
			}
			continue
		}
		if status != http.StatusBadRequest && status != http.StatusNotFound {
			t.Fatalf("%s: %d %v", tc.name, status, body)
		}
		if body["error"] != tc.code {
			t.Fatalf("%s: error = %v, want %s", tc.name, body["error"], tc.code)
		}
	}
	// 500 上限的批量确实产出 500 行
	status, _, lst := f.do(t, "GET", "/api/v1/nfc/tags", "sess-owner-a", f.tenA, "")
	mustEqual(t, status, http.StatusOK)
	if got := len(lst["items"].([]any)); got != 500 {
		t.Fatalf("tags = %d, want 500", got)
	}
}

// TestNFCTagExportRequiresPublicBaseURL:无 PUBLIC_BASE_URL → 503 fail-closed。
func TestNFCTagExportRequiresPublicBaseURL(t *testing.T) {
	f := fixtureWithQR(t, nil)
	_, campaignID, linkIDs, _ := seedNFCTenantCampaign(t, f, 1)
	status, _, _ := f.do(t, "POST", "/api/v1/nfc/tags/batch", "sess-owner-a", f.tenA,
		batchReq(campaignID, linkIDs[:1], 1, "", "", "", "shared"))
	mustEqual(t, status, http.StatusCreated)

	f.s.Cfg.PublicBaseURL = ""
	status, _, body := f.do(t, "GET", "/api/v1/nfc/tags/export.csv", "sess-owner-a", f.tenA, "")
	if status != http.StatusServiceUnavailable || body["error"] != "public_base_url_missing" {
		t.Fatalf("no PUBLIC_BASE_URL = %d %v, want 503 public_base_url_missing", status, body)
	}
}
