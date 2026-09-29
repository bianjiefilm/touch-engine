package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/activityspend"
	"github.com/bianjiefilm/touch-engine/server/internal/copydraft"
	"github.com/bianjiefilm/touch-engine/server/internal/copyjob"
)

func TestCopyJobFailClosedWithoutCredentials(t *testing.T) {
	f := newFixture(t, false)
	campID, title := seedCopyCampaign(t, f)
	body := copyJobSample("job-close-1")

	status, _, quoted := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-owner-a", f.tenA, body)
	if status != http.StatusCreated {
		t.Fatalf("quote = %d %+v", status, quoted)
	}
	assertFailClosed(t, quoted, title, "draft")
	if quoted["state"] != "quoted" || quoted["charge_count"] != float64(0) {
		t.Fatalf("quote state %+v", quoted)
	}
	if !strings.Contains(fmt.Sprint(quoted["notice"]), "没有可用的模型凭证") {
		t.Fatalf("notice = %v", quoted["notice"])
	}
	codes := gapCodes(quoted)
	if !codes["product_unconfirmed"] || !codes["price_expired"] || !codes["address_uncertain"] || !codes["hours_uncertain"] || !codes["claim_without_evidence"] {
		t.Fatalf("gaps = %+v", quoted["gaps"])
	}
	var drafts int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM copy_drafts`).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if drafts != 0 {
		t.Fatalf("quote consumed a draft slot: %d", drafts)
	}

	status, _, again := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-owner-a", f.tenA, body)
	if status != http.StatusOK || again["id"] != quoted["id"] {
		t.Fatalf("replay = %d id %v want %v", status, again["id"], quoted["id"])
	}
	status, _, conflict := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-owner-a", f.tenA, strings.Replace(body, "19.9元", "29元", 1))
	if status != http.StatusConflict || conflict["error"] != "idempotency_conflict" || conflict["billed"] != false {
		t.Fatalf("conflict = %d %+v", status, conflict)
	}

	jobID, _ := quoted["id"].(string)
	base := "/api/v1/campaigns/" + campID + "/copy-jobs/" + jobID
	status, _, early := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusConflict || early["error"] != "not_confirmed" || !strings.Contains(fmt.Sprint(early["message"]), "请先确认报价") {
		t.Fatalf("early generate = %d %+v", status, early)
	}
	if early["campaign_title"] != title {
		t.Fatalf("early generate wrote the campaign: %+v", early)
	}

	status, _, confirmed := f.do(t, "POST", base+"/confirm", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || confirmed["state"] != "confirmed" || confirmed["charge_count"] != float64(0) {
		t.Fatalf("confirm = %d %+v", status, confirmed)
	}
	status, _, generated := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || generated["state"] != "generation_incomplete" {
		t.Fatalf("generate = %d %+v", status, generated)
	}
	assertFailClosed(t, generated, title, "draft")
	if !strings.Contains(fmt.Sprint(generated["notice"]), "真实生成未完成") || generated["draft_id"] == "" {
		t.Fatalf("incomplete notice %+v", generated)
	}
	status, _, retry := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || retry["id"] != jobID || retry["charge_count"] != float64(0) {
		t.Fatalf("retry = %d %+v", status, retry)
	}
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM copy_drafts`).Scan(&drafts); err != nil {
		t.Fatal(err)
	}
	if drafts != 1 {
		t.Fatalf("retry inserted another draft: %d", drafts)
	}

	status, _, selected := f.do(t, "POST", base+"/select", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || selected["state"] != "selected" || selected["campaign_status"] != "draft" || selected["rewards_triggered"] != false {
		t.Fatalf("select = %d %+v", status, selected)
	}
	status, _, saved := f.do(t, "POST", base+"/save", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusConflict || saved["error"] != "generation_incomplete" || !strings.Contains(fmt.Sprint(saved["message"]), "模型未完成") {
		t.Fatalf("save = %d %+v", status, saved)
	}
	if saved["campaign_mutated"] != false || saved["campaign_title"] != title || saved["campaign_public_content"] != "到店有礼" {
		t.Fatalf("save wrote copy: %+v", saved)
	}
	status, _, got := f.do(t, "GET", "/api/v1/campaigns/"+campID, "sess-owner-a", f.tenA, "")
	if status != http.StatusOK || got["title"] != title || got["status"] != "draft" || got["public_content"] != "到店有礼" {
		t.Fatalf("campaign changed: %+v", got)
	}
	for _, table := range []string{"campaign_rules", "publish_reward_ledger", "activity_benefit_facts"} {
		var n int
		if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("%s rows = %d", table, n)
		}
	}

	status, _, hidden := f.do(t, "GET", base, "sess-owner-b", f.tenB, "")
	if status != http.StatusNotFound {
		t.Fatalf("cross tenant = %d %+v", status, hidden)
	}
	status, _, guest := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "", f.tenA, body)
	if status != http.StatusUnauthorized {
		t.Fatalf("guest = %d %+v", status, guest)
	}
	status, _, pub := f.do(t, "POST", "/api/v1/public/links/nope/copy-jobs", "", "", body)
	if status != http.StatusNotFound {
		t.Fatalf("public generate = %d %+v", status, pub)
	}
	var jobs int
	if err := f.s.St.DB.QueryRow(`SELECT COUNT(*) FROM copy_jobs`).Scan(&jobs); err != nil {
		t.Fatal(err)
	}
	if jobs != 1 {
		t.Fatalf("public or guest created a job: %d", jobs)
	}
}

func TestCopyJobInputChangeQuotaAndRevokeDoNotCharge(t *testing.T) {
	f := newFixture(t, false)
	campID, title := seedCopyCampaign(t, f)
	if _, err := f.s.St.AddCampaignAsset(f.tenA, campID, "ast_1", "", "test"); err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(copyJobSample("job-asset"), `"asset_ids":[]`, `"asset_ids":["ast_1"]`, 1)
	status, _, quoted := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-owner-a", f.tenA, body)
	if status != http.StatusCreated {
		t.Fatalf("quote = %d %+v", status, quoted)
	}
	jobID, _ := quoted["id"].(string)
	base := "/api/v1/campaigns/" + campID + "/copy-jobs/" + jobID
	assets, err := f.s.St.ListCampaignAssets(f.tenA, campID)
	if err != nil || len(assets) != 1 {
		t.Fatalf("assets %v %v", assets, err)
	}
	if err := f.s.St.RemoveCampaignAsset(assets[0].ID, f.tenA, campID); err != nil {
		t.Fatal(err)
	}
	status, _, changed := f.do(t, "POST", base+"/confirm", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusConflict || changed["state"] != "input_changed" || changed["charge_count"] != float64(0) || changed["billed"] != false {
		t.Fatalf("input change = %d %+v", status, changed)
	}
	if !strings.Contains(fmt.Sprint(changed["message"]), "资料已变更") || changed["campaign_title"] != title {
		t.Fatalf("input change overwrote copy: %+v", changed)
	}

	status, _, fresh := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-owner-a", f.tenA, copyJobSample("job-quota"))
	if status != http.StatusCreated {
		t.Fatalf("second quote = %d %+v", status, fresh)
	}
	jobID, _ = fresh["id"].(string)
	base = "/api/v1/campaigns/" + campID + "/copy-jobs/" + jobID
	status, _, _ = f.do(t, "POST", base+"/confirm", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK {
		t.Fatalf("confirm = %d", status)
	}
	for i := 0; i < copyDraftDailyQuota; i++ {
		draftBody := fmt.Sprintf(`{"idempotency_key":"slot-%d","price":{"text":"","status":"absent"},"address":{"text":"","status":"absent"},"hours":{"text":"","status":"absent"},"claims":[],"poi_names":[],"asset_ids":[]}`, i)
		status, _, draft := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-drafts", "sess-owner-a", f.tenA, draftBody)
		if status != http.StatusCreated || draft["billed"] != false {
			t.Fatalf("fill quota %d = %d %+v", i, status, draft)
		}
	}
	status, _, over := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusTooManyRequests || over["error"] != "quota_exceeded" || over["billed"] != false || over["charge_count"] != float64(0) {
		t.Fatalf("quota = %d %+v", status, over)
	}
	if !strings.Contains(fmt.Sprint(over["message"]), "没有扣费") || over["campaign_title"] != title {
		t.Fatalf("quota wrote copy: %+v", over)
	}

	status, _, revJob := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-owner-a", f.tenA, copyJobSample("job-revoke"))
	if status != http.StatusCreated {
		t.Fatalf("revoke quote = %d %+v", status, revJob)
	}
	revID, _ := revJob["id"].(string)
	status, _, revoked := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs/"+revID+"/revoke", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || revoked["state"] != "revoked" || revoked["charge_count"] != float64(0) || !strings.Contains(fmt.Sprint(revoked["notice"]), "已撤销") {
		t.Fatalf("revoke = %d %+v", status, revoked)
	}
	status, _, late := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs/"+revID+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || late["state"] != "revoked" || late["charge_count"] != float64(0) || late["success"] != false {
		t.Fatalf("late generate = %d %+v", status, late)
	}
}

func TestCopyJobInjectedModelDoesNotPublishOrDoubleCharge(t *testing.T) {
	f := newFixture(t, false)
	f.s.Cfg.SubscriptionStatus = activityspend.StatusActive
	model := &scriptedModel{ready: true, out: copyjob.GenerateResult{
		Output:       copydraft.ModelOutput{Title: "周末到店", Intro: "到店有礼", Topics: []string{"到店"}},
		ModelID:      "copy-small",
		ModelVersion: "2026-09-29",
		TaskID:       "tsk_test",
		HoldID:       "hold_test",
		AmountMinor:  12,
		Priced:       true,
		Submitted:    true,
	}}
	f.s.CopyModel = model
	campID, title := seedCopyCampaign(t, f)
	status, _, quoted := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-owner-a", f.tenA, copyJobSample("job-model"))
	if status != http.StatusCreated || quoted["priced"] != false || quoted["amount_minor"] != nil || quoted["success"] != false {
		t.Fatalf("quote before model = %d %+v", status, quoted)
	}
	jobID, _ := quoted["id"].(string)
	base := "/api/v1/campaigns/" + campID + "/copy-jobs/" + jobID
	status, _, _ = f.do(t, "POST", base+"/confirm", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK {
		t.Fatalf("confirm = %d", status)
	}
	status, _, generated := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || generated["state"] != "generated" || generated["success"] != true || generated["real_generation"] != "completed" {
		t.Fatalf("generate = %d %+v", status, generated)
	}
	if generated["billed"] != false || generated["charge_count"] != float64(1) || generated["balance_mutated"] != false || generated["goboost_project_id"] != "" {
		t.Fatalf("billing trace = %+v", generated)
	}
	if generated["amount_minor"] != float64(12) || generated["model_id"] != "copy-small" || generated["model_version"] != "2026-09-29" || generated["poi_mounted"] != false {
		t.Fatalf("trace fields = %+v", generated)
	}
	if generated["campaign_status"] != "draft" || generated["campaign_title"] != title {
		t.Fatalf("generate published: %+v", generated)
	}
	if model.calls != 1 || len(model.prompts) != 1 {
		t.Fatalf("calls = %d", model.calls)
	}
	for _, forbidden := range []string{"19.9", "南京西路", "10:00", "全市最低", "招牌鱼头"} {
		if strings.Contains(model.prompts[0], forbidden) {
			t.Fatalf("prompt leaked %s: %s", forbidden, model.prompts[0])
		}
	}
	if !strings.Contains(model.prompts[0], "江边小馆") || !strings.Contains(model.prompts[0], "周末到店") {
		t.Fatalf("prompt dropped confirmed facts: %s", model.prompts[0])
	}
	status, _, retry := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || retry["charge_count"] != float64(1) || model.calls != 1 {
		t.Fatalf("retry calls %d body %+v", model.calls, retry)
	}

	status, _, patched := f.do(t, "PATCH", "/api/v1/campaigns/"+campID, "sess-owner-a", f.tenA, `{"title":"手写标题"}`)
	if status != http.StatusOK || patched["title"] != "手写标题" {
		t.Fatalf("patch = %d %+v", status, patched)
	}
	status, _, selected := f.do(t, "POST", base+"/select", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || selected["state"] != "selected" || selected["campaign_status"] != "draft" {
		t.Fatalf("select = %d %+v", status, selected)
	}
	status, _, saved := f.do(t, "POST", base+"/save", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || saved["state"] != "saved" || saved["campaign_title"] != "手写标题" || saved["campaign_status"] != "draft" {
		t.Fatalf("save = %d %+v", status, saved)
	}
	if saved["rewards_triggered"] != false || saved["billed"] != false || !strings.Contains(fmt.Sprint(saved["notice"]), "没有发奖励") {
		t.Fatalf("save side effects %+v", saved)
	}
	preserved, _ := saved["preserved"].([]any)
	if len(preserved) != 1 || preserved[0] != "title" {
		t.Fatalf("preserved = %+v", saved["preserved"])
	}
	status, _, got := f.do(t, "GET", "/api/v1/campaigns/"+campID, "sess-owner-a", f.tenA, "")
	if got["title"] != "手写标题" || got["public_content"] != "到店有礼" || got["status"] != "draft" {
		t.Fatalf("campaign after save = %d %+v", status, got)
	}
}

func TestCopyJobWithheldModelTextIsNotSuccess(t *testing.T) {
	f := newFixture(t, false)
	f.s.Cfg.SubscriptionStatus = activityspend.StatusActive
	model := &scriptedModel{ready: true, out: copyjob.GenerateResult{
		Output:       copydraft.ModelOutput{Title: "全市最低仅19.9元", Intro: "10:00开门"},
		ModelID:      "copy-small",
		ModelVersion: "2026-09-29",
		Submitted:    true,
		Priced:       true,
		AmountMinor:  12,
	}}
	f.s.CopyModel = model
	campID, title := seedCopyCampaign(t, f)
	status, _, quoted := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-owner-a", f.tenA, copyJobSample("job-leak"))
	if status != http.StatusCreated {
		t.Fatalf("quote = %d %+v", status, quoted)
	}
	jobID, _ := quoted["id"].(string)
	base := "/api/v1/campaigns/" + campID + "/copy-jobs/" + jobID
	if status, _, _ = f.do(t, "POST", base+"/confirm", "sess-owner-a", f.tenA, `{}`); status != http.StatusOK {
		t.Fatalf("confirm = %d", status)
	}
	status, _, generated := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || generated["success"] != false || generated["real_generation"] != "incomplete" || generated["billed"] != false {
		t.Fatalf("leak generate = %d %+v", status, generated)
	}
	if generated["campaign_title"] != title || generated["campaign_status"] != "draft" || generated["poi_mounted"] != false || generated["balance_mutated"] != false {
		t.Fatalf("leak side effect %+v", generated)
	}
	draft, _ := generated["draft"].(map[string]any)
	leaked := fmt.Sprint(draft["title"], draft["intro"], draft["topics"])
	for _, forbidden := range []string{"19.9", "全市最低", "10:00"} {
		if strings.Contains(leaked, forbidden) {
			t.Fatalf("withheld sentence kept %q: %s", forbidden, leaked)
		}
	}
	if generated["charge_count"] != float64(1) || model.calls != 1 {
		t.Fatalf("submit trace calls %d %+v", model.calls, generated)
	}
	status, _, retry := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || model.calls != 1 || retry["charge_count"] != float64(1) {
		t.Fatalf("retry calls %d %+v", model.calls, retry)
	}
	if status, _, selected := f.do(t, "POST", base+"/select", "sess-owner-a", f.tenA, `{}`); status != http.StatusOK || selected["state"] != "selected" {
		t.Fatalf("select = %d %+v", status, selected)
	}
	status, _, saved := f.do(t, "POST", base+"/save", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusConflict || saved["campaign_title"] != title || saved["campaign_mutated"] != false {
		t.Fatalf("save leak = %d %+v", status, saved)
	}
}

func TestCopyJobStaffDoesNotCallModel(t *testing.T) {
	f := newFixture(t, false)
	f.s.Cfg.SubscriptionStatus = activityspend.StatusActive
	model := &scriptedModel{ready: true, out: copyjob.GenerateResult{Submitted: true, ModelID: "copy-small", ModelVersion: "v"}}
	f.s.CopyModel = model
	campID, title := seedCopyCampaign(t, f)
	status, _, quoted := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-staff-a", f.tenA, copyJobSample("job-staff"))
	if status != http.StatusCreated || quoted["success"] != false {
		t.Fatalf("staff quote = %d %+v", status, quoted)
	}
	jobID, _ := quoted["id"].(string)
	base := "/api/v1/campaigns/" + campID + "/copy-jobs/" + jobID
	if status, _, _ = f.do(t, "POST", base+"/confirm", "sess-staff-a", f.tenA, `{}`); status != http.StatusOK {
		t.Fatalf("confirm = %d", status)
	}
	status, _, generated := f.do(t, "POST", base+"/generate", "sess-staff-a", f.tenA, `{}`)
	if status != http.StatusOK || generated["success"] != false || generated["real_generation"] != "incomplete" || model.calls != 0 {
		t.Fatalf("staff generate calls %d %+v", model.calls, generated)
	}
	if generated["billed"] != false || generated["charge_count"] != float64(0) || generated["campaign_title"] != title {
		t.Fatalf("staff billing %+v", generated)
	}
}

func TestCopyJobTimeoutDoesNotRetryCharge(t *testing.T) {
	f := newFixture(t, false)
	f.s.Cfg.SubscriptionStatus = activityspend.StatusActive
	model := &scriptedModel{ready: true, err: copyjob.ErrTimeout, out: copyjob.GenerateResult{Submitted: true, ModelID: "copy-small", ModelVersion: "v"}}
	f.s.CopyModel = model
	campID, title := seedCopyCampaign(t, f)
	status, _, quoted := f.do(t, "POST", "/api/v1/campaigns/"+campID+"/copy-jobs", "sess-owner-a", f.tenA, copyJobSample("job-timeout"))
	jobID, _ := quoted["id"].(string)
	base := "/api/v1/campaigns/" + campID + "/copy-jobs/" + jobID
	if status != http.StatusCreated {
		t.Fatalf("quote = %d", status)
	}
	if status, _, _ = f.do(t, "POST", base+"/confirm", "sess-owner-a", f.tenA, `{}`); status != http.StatusOK {
		t.Fatalf("confirm = %d", status)
	}
	status, _, timed := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || timed["state"] != "timed_out" || timed["charge_count"] != float64(1) || timed["billed"] != false || timed["success"] != false {
		t.Fatalf("timeout = %d %+v", status, timed)
	}
	if !strings.Contains(fmt.Sprint(timed["notice"]), "生成超时") || timed["campaign_title"] != title {
		t.Fatalf("timeout notice %+v", timed)
	}
	status, _, retry := f.do(t, "POST", base+"/generate", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusOK || retry["charge_count"] != float64(1) || model.calls != 1 || retry["state"] != "timed_out" {
		t.Fatalf("timeout retry calls %d %+v", model.calls, retry)
	}
}

type scriptedModel struct {
	ready   bool
	calls   int
	prompts []string
	out     copyjob.GenerateResult
	err     error
}

func (m *scriptedModel) Ready() bool { return m.ready }

func (m *scriptedModel) Generate(_ context.Context, req copyjob.GenerateRequest) (copyjob.GenerateResult, error) {
	m.calls++
	m.prompts = append(m.prompts, req.Prompt)
	return m.out, m.err
}

func seedCopyCampaign(t *testing.T, f *fixture) (campID, title string) {
	t.Helper()
	status, _, storeRec := f.do(t, "POST", "/api/v1/stores", "sess-owner-a", f.tenA, `{"name":"江边小馆","address":"杭州市湖滨路8号"}`)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("store = %d %+v", status, storeRec)
	}
	storeID, _ := storeRec["id"].(string)
	status, _, camp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		fmt.Sprintf(`{"title":"周末到店","public_content":"到店有礼","store_id":%q}`, storeID))
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("campaign = %d %+v", status, camp)
	}
	campID, _ = camp["id"].(string)
	title, _ = camp["title"].(string)
	return campID, title
}

func copyJobSample(key string) string {
	return fmt.Sprintf(`{
		"idempotency_key":%q,
		"product":{"text":"招牌鱼头","status":"uncertain"},
		"price":{"text":"19.9元","status":"expired"},
		"address":{"text":"上海市南京西路1号","status":"confirmed"},
		"hours":{"text":"10:00-22:00","status":"uncertain"},
		"claims":[{"text":"全市最低","evidence":""}],
		"poi_names":["江边小馆(南京西路店)"],
		"channel_mount":true,
		"asset_ids":[]
	}`, key)
}

func assertFailClosed(t *testing.T, view map[string]any, title, status string) {
	t.Helper()
	if view["success"] != false || view["real_generation"] != "incomplete" || view["billed"] != false || view["poi_mounted"] != false {
		t.Fatalf("labeled success: %+v", view)
	}
	if view["amount_minor"] != nil || view["goboost_project_id"] != "" || view["balance_mutated"] != false || view["rewards_triggered"] != false {
		t.Fatalf("side effect: %+v", view)
	}
	if view["campaign_title"] != title || view["campaign_status"] != status {
		t.Fatalf("campaign view changed: %+v", view)
	}
	draft, _ := view["draft"].(map[string]any)
	raw := fmt.Sprint(draft["title"], draft["intro"], draft["topics"], draft["confirmed_facts"], draft["professional_handoff"])
	for _, forbidden := range []string{"19.9", "南京西路", "10:00", "全市最低", "招牌鱼头"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("response contains %q: %s", forbidden, raw)
		}
	}
	poi, _ := draft["poi"].(map[string]any)
	if poi["mounted"] != false {
		t.Fatalf("poi mounted: %+v", poi)
	}
}

func gapCodes(view map[string]any) map[string]bool {
	codes := map[string]bool{}
	gaps, _ := view["gaps"].([]any)
	for _, g := range gaps {
		item, _ := g.(map[string]any)
		codes[fmt.Sprint(item["code"])] = true
	}
	return codes
}
