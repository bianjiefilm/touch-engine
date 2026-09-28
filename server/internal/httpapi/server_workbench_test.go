package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

func TestWorkbenchRequiresASession(t *testing.T) {
	f := newFixture(t, false)
	status, _, _ := f.do(t, "GET", "/api/v1/workbench", "", f.tenA, "")
	if status != 401 {
		t.Fatalf("status %d, want 401", status)
	}
}

func TestWorkbenchDraftCampaignEntersMyTasks(t *testing.T) {
	f := newFixture(t, false)
	cmp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "春季店庆", CreatedBy: "usr_owner_a"})
	if err != nil {
		t.Fatal(err)
	}
	status, _, body := f.do(t, "GET", "/api/v1/workbench", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("status %d body %#v", status, body)
	}
	var found bool
	for _, item := range body["my_tasks"].([]any) {
		task := item.(map[string]any)
		if task["kind"] == "unpublished_campaign" && task["object_id"] == cmp.ID && task["state"] == "draft" {
			found = true
		}
	}
	if !found {
		t.Fatalf("draft missing from todos: %#v", body["my_tasks"])
	}
	activities := body["activities"].(map[string]any)
	drafts := activities["draft"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("draft bucket %#v", activities["draft"])
	}
}

func TestWorkbenchOwnerSeesDraftTaskWithoutOpsBannerOrContact(t *testing.T) {
	f := newFixture(t, false)
	cmp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "春季店庆", CreatedBy: "usr_owner_a"})
	if err != nil {
		t.Fatal(err)
	}
	sto, err := f.s.St.CreateStore(f.tenA, "门店", "", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.TransitionCampaign(cmp.ID, f.tenA, "active"); err != nil {
		t.Fatal(err)
	}
	lnk, err := f.s.St.CreateLink(f.tenA, cmp.ID, "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.St.SubmitLead(store.NewLeadSubmission{
		TenantID: f.tenA, CampaignID: cmp.ID, StoreID: sto.ID, LinkID: lnk.ID,
		SubmissionRef: "sub_secret", DedupKey: "dk_secret",
		Name: "张三", Phone: "13800139999",
		NoticeVersion: "v1", ConsentAt: "2026-09-27T00:00:00Z",
		OutboxEventID: "ev_secret", OutboxPayload: `{}`,
	}); err != nil {
		t.Fatal(err)
	}

	status, _, body := f.do(t, "GET", "/api/v1/workbench", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("status %d body %#v", status, body)
	}
	raw, _ := json.Marshal(body)
	text := string(raw)
	if strings.Contains(text, "13800139999") || strings.Contains(text, "张三") || strings.Contains(text, "sub_secret") {
		t.Fatalf("workbench leaked a contact: %s", text)
	}
	if strings.Contains(text, "正在为") {
		t.Fatalf("owner membership presented as ops: %s", text)
	}
	if _, ok := body["working_for"]; ok {
		t.Fatalf("working_for present for owner: %#v", body["working_for"])
	}
	activities, _ := body["activities"].(map[string]any)
	inProgress, _ := activities["in_progress"].([]any)
	if len(inProgress) != 1 {
		t.Fatalf("in_progress = %#v", activities)
	}
	customers, _ := body["customers"].(map[string]any)
	if customers["available"] != false {
		t.Fatalf("leads-off customers = %#v", customers)
	}
	if strings.Contains(text, "/c/") {
		t.Fatalf("visitor route embedded: %s", text)
	}
}

func TestWorkbenchAcceptedLeadIsNotSalesReceived(t *testing.T) {
	f := newFixture(t, false)
	f.s.Cfg.FeatureLeadsCapture = true
	f.s.Cfg.NotifyBaseURL = "http://notify.test"
	f.s.Cfg.NotifyToken = "notify-token"
	f.s.Cfg.LeadsTargetApp = "leads-app"
	f.s.Cfg.LeadsPhonePepper = "pepper"

	cmp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "店庆", CreatedBy: "usr_owner_a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.TransitionCampaign(cmp.ID, f.tenA, "active"); err != nil {
		t.Fatal(err)
	}
	sto, err := f.s.St.CreateStore(f.tenA, "门店", "", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	lnk, err := f.s.St.CreateLink(f.tenA, cmp.ID, "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.St.SubmitLead(store.NewLeadSubmission{
		TenantID: f.tenA, CampaignID: cmp.ID, StoreID: sto.ID, LinkID: lnk.ID,
		SubmissionRef: "sub_1", DedupKey: "dk_1",
		Name: "李四", Phone: "13900001111",
		NoticeVersion: "v1", ConsentAt: "2026-09-27T00:00:00Z",
		OutboxEventID: "ev_1", OutboxPayload: `{}`,
	}); err != nil {
		t.Fatal(err)
	}

	status, _, body := f.do(t, "GET", "/api/v1/workbench", "sess-owner-a", f.tenA, "")
	if status != 200 {
		t.Fatalf("status %d body %#v", status, body)
	}
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), "13900001111") || strings.Contains(string(raw), "李四") {
		t.Fatalf("contact leaked: %s", raw)
	}
	cards := body["customers"].(map[string]any)["cards"].([]any)
	card := cards[0].(map[string]any)
	sales := card["sales_received"].(map[string]any)
	if sales["available"] != false {
		t.Fatalf("accepted lead treated as a known sales receipt: %#v", sales)
	}
	if _, ok := sales["value"]; ok {
		t.Fatalf("pending sync written as a receipt count: %#v", sales)
	}
	if card["pending_sync"] != float64(1) {
		t.Fatalf("pending = %#v", card["pending_sync"])
	}
	var pendingTask bool
	for _, item := range body["my_tasks"].([]any) {
		task := item.(map[string]any)
		if task["kind"] != "pending_lead" {
			continue
		}
		pendingTask = true
		title, _ := task["title"].(string)
		if strings.Contains(title, "没有销售接收") || strings.Contains(title, "成功") {
			t.Fatalf("pending task wording %q", title)
		}
	}
	if !pendingTask {
		t.Fatalf("accepted lead missing from todos: %#v", body["my_tasks"])
	}
	follow := card["follow_up"].(map[string]any)
	if follow["available"] != false {
		t.Fatalf("follow-up invented: %#v", follow)
	}
	if _, ok := follow["value"]; ok {
		t.Fatalf("unknown follow-up has a value: %#v", follow)
	}
}

func TestWorkbenchSeatUsesTheActiveRelation(t *testing.T) {
	f := newFixture(t, false)
	owner := &caller{Member: &store.Member{TenantID: f.tenA, PrincipalRef: "usr_owner_a", Role: "org_owner", Enabled: true}}
	ownerSeat := f.s.workbenchSeat(owner)
	if ownerSeat.Source != "membership" || ownerSeat.RelationID != "" {
		t.Fatalf("owner seat = %+v", ownerSeat)
	}
	if _, err := f.s.St.CreateMember(f.tenA, "usr_agent", "agent", "代理", "test", true); err != nil {
		t.Fatal(err)
	}
	agent := &caller{Member: &store.Member{TenantID: f.tenA, PrincipalRef: "usr_agent", Role: "agent", Enabled: true}}
	idle := f.s.workbenchSeat(agent)
	if idle.Source == "agency" || idle.RelationID != "" {
		t.Fatalf("agent without a relation was authorized as ops: %+v", idle)
	}
	rel, err := f.s.St.CreateAgencyRelation(f.tenA, "usr_agent", "usr_owner_a", "")
	if err != nil {
		t.Fatal(err)
	}
	live := f.s.workbenchSeat(agent)
	if live.Source != "agency" || live.RelationID != rel.ID || live.Role != "agent" {
		t.Fatalf("live seat = %+v, relation %s", live, rel.ID)
	}
	ten, err := f.s.St.GetTenant(f.tenA)
	if err != nil {
		t.Fatal(err)
	}
	if live.MerchantName != ten.Name {
		t.Fatalf("merchant name %q", live.MerchantName)
	}
}

func TestWorkbenchStoreManagerDoesNotSeeAnotherStore(t *testing.T) {
	f := newFixture(t, false)
	own, err := f.s.St.CreateStore(f.tenA, "本店", "", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.s.St.CreateStore(f.tenA, "他店", "", "usr_owner_a")
	if err != nil {
		t.Fatal(err)
	}
	ownCmp, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "本店活动", StoreID: own.ID, CreatedBy: "usr_owner_a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.CreateCampaign(store.NewCampaign{TenantID: f.tenA, Title: "他店活动", StoreID: other.ID, CreatedBy: "usr_owner_a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.St.CreateMemberScoped(f.tenA, "usr_mgr", "store_manager", "经理", "test", true, own.ID); err != nil {
		t.Fatal(err)
	}
	manager := &caller{Member: &store.Member{TenantID: f.tenA, PrincipalRef: "usr_mgr", Role: "store_manager", StoreScope: own.ID, Enabled: true}}
	facts, err := f.s.workbenchFacts(context.Background(), manager)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts.Campaigns) != 1 || facts.Campaigns[0].ID != ownCmp.ID {
		t.Fatalf("campaigns = %+v", facts.Campaigns)
	}
}
