package copyjob

import (
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/activityspend"
	"github.com/bianjiefilm/touch-engine/server/internal/copydraft"
)

func sampleFacts() Facts {
	return Facts{
		Product:  Product{Text: "招牌鱼头", Status: copydraft.StatusUncertain},
		Price:    copydraft.Fact{Text: "19.9元", Status: copydraft.StatusExpired},
		Address:  copydraft.Fact{Text: "上海市南京西路1号", Status: copydraft.StatusConfirmed},
		Hours:    copydraft.Fact{Text: "10:00-22:00", Status: copydraft.StatusUncertain},
		Claims:   []copydraft.Claim{{Text: "全市最低", Evidence: ""}},
		POINames: []string{"江边小馆(南京西路店)"},
		AssetIDs: []string{"ast_1"},
	}
}

func sampleLive() Live {
	return Live{StoreName: "江边小馆", StoreAddress: "杭州市湖滨路8号", CampaignTitle: "周末到店", PublicContent: "到店有礼"}
}

func quoteJob(ready bool, charge activityspend.ChargeDecision) Job {
	return Quote(QuoteInput{
		Key: "job-1", Live: sampleLive(), Facts: sampleFacts(), AssetIDs: []string{"ast_1"},
		PayerAccountID: "ten_a", Charge: charge, ModelReady: ready,
	})
}

func TestQuoteDoesNotSucceedOrMountPOIOrInventProduct(t *testing.T) {
	job := quoteJob(false, activityspend.ChargeDecision{Reason: activityspend.ReasonEntitlementUnconfirmed})
	if job.State != StateQuoted || job.Trace.Success || job.Trace.Billed || job.Trace.ChargeCount != 0 {
		t.Fatalf("quote labeled success: %+v", job.Trace)
	}
	if job.Trace.RealGeneration != RealIncomplete || job.Trace.GoBoostProjectID != "" || job.Trace.BalanceMutated {
		t.Fatalf("trace = %+v", job.Trace)
	}
	if job.Trace.AmountMinor != nil || job.Trace.Priced {
		t.Fatalf("unpriced quote has an amount: %+v", job.Trace)
	}
	if !strings.Contains(job.Notice, "不会扣费") {
		t.Fatalf("notice = %q", job.Notice)
	}
	blob := job.Draft.Title + job.Draft.Intro + strings.Join(job.Draft.Topics, "") + strings.Join(job.Draft.ConfirmedFacts, "")
	for _, forbidden := range []string{"19.9", "南京西路", "10:00", "全市最低", "招牌鱼头"} {
		if strings.Contains(blob, forbidden) {
			t.Fatalf("draft contains %q: %s", forbidden, blob)
		}
	}
	if job.Draft.POI.Mounted {
		t.Fatalf("poi mounted: %+v", job.Draft.POI)
	}
	codes := map[string]bool{}
	for _, g := range job.Draft.Gaps {
		codes[g.Code] = true
		if strings.Contains(g.Message, "招牌鱼头") || strings.Contains(g.Message, "19.9") {
			t.Fatalf("gap repeats withheld text: %+v", g)
		}
	}
	if !codes["product_unconfirmed"] || !codes["price_expired"] {
		t.Fatalf("gaps = %+v", job.Draft.Gaps)
	}
	if strings.Contains(Prompt(job.Snapshot), "招牌鱼头") || strings.Contains(Prompt(job.Snapshot), "19.9") || strings.Contains(Prompt(job.Snapshot), "全市最低") {
		t.Fatalf("prompt leaked: %s", Prompt(job.Snapshot))
	}
}

func TestConfirmedProductIsPromptOnlyAndStillNotAUsableDraft(t *testing.T) {
	facts := sampleFacts()
	facts.Product = Product{Text: "西湖醋鱼", Status: copydraft.StatusConfirmed}
	job := Quote(QuoteInput{Key: "p", Live: sampleLive(), Facts: facts, PayerAccountID: "ten_a", Charge: activityspend.ChargeDecision{Reason: activityspend.ReasonNoChargeableCall}})
	if !strings.Contains(Prompt(job.Snapshot), "西湖醋鱼") {
		t.Fatalf("prompt = %s", Prompt(job.Snapshot))
	}
	if strings.Contains(job.Draft.Title+job.Draft.Intro, "西湖醋鱼") || job.Trace.Success {
		t.Fatalf("product became copy: %+v", job.Draft)
	}
}

func TestStateMachineFailClosedWithoutModel(t *testing.T) {
	job := quoteJob(false, activityspend.ChargeDecision{Reason: activityspend.ReasonEntitlementUnconfirmed})
	hash := job.SnapshotHash
	again := Quote(QuoteInput{Key: "job-1", Live: sampleLive(), Facts: sampleFacts(), AssetIDs: []string{"ast_1"}, PayerAccountID: "ten_a", Charge: activityspend.ChargeDecision{Reason: activityspend.ReasonEntitlementUnconfirmed}})
	if again.SnapshotHash != hash {
		t.Fatal("same snapshot changed hash")
	}
	early := PlanGenerate(job, hash, true, false)
	if early.Blocked != "not_confirmed" || early.Compose || early.CallModel {
		t.Fatalf("generate before confirm: %+v", early)
	}
	job = Confirm(job, hash)
	if job.State != StateConfirmed || job.Trace.ChargeCount != 0 {
		t.Fatalf("confirm = %+v", job)
	}
	job = Confirm(job, hash)
	if job.State != StateConfirmed {
		t.Fatalf("replay confirm = %s", job.State)
	}
	plan := PlanGenerate(job, hash, true, false)
	if !plan.Compose || plan.CallModel || plan.Job.State != StateIncomplete {
		t.Fatalf("plan = %+v", plan)
	}
	job = FinishIncomplete(plan.Job, copydraft.Compose(job.Snapshot.Input()))
	if job.Trace.Success || job.Trace.RealGeneration != RealIncomplete || job.Draft.Usable || job.Trace.Billed || job.Trace.ChargeCount != 0 {
		t.Fatalf("incomplete = %+v draft %+v", job.Trace, job.Draft)
	}
	if !strings.Contains(job.Notice, "真实生成未完成") {
		t.Fatalf("notice = %q", job.Notice)
	}
	replay := PlanGenerate(job, hash, true, true)
	if replay.Compose || replay.CallModel || replay.Job.Trace.ChargeCount != 0 {
		t.Fatalf("retry called the model: %+v", replay)
	}
	job.DraftID = "cdf_1"
	selected, err := Select(job)
	if err != nil || selected.State != StateSelected || selected.Trace.RewardsTriggered || selected.Trace.Success {
		t.Fatalf("select incomplete = %+v %v", selected, err)
	}
	save := PlanSave(selected, "周末到店", "到店有礼")
	if save.Allow || save.Reason != "generation_incomplete" || save.WriteTitle != nil {
		t.Fatalf("save incomplete = %+v", save)
	}
}

func TestInputChangeLateQuotaTimeoutRevokeDoNotDoubleChargeOrOverwrite(t *testing.T) {
	charge := activityspend.ChargeDecision{QuoteCreated: true, Reason: activityspend.ReasonQuoteWithoutDebit}
	job := quoteJob(true, charge)
	changed := Confirm(job, "deadbeef")
	if changed.State != StateInputChanged || changed.Trace.ChargeCount != 0 || changed.Trace.Success {
		t.Fatalf("input change = %+v", changed.Trace)
	}

	job = Confirm(job, job.SnapshotHash)
	plan := PlanGenerate(job, job.SnapshotHash, false, true)
	if plan.Job.State != StateQuota || plan.CallModel || plan.Job.Trace.ChargeCount != 0 {
		t.Fatalf("quota = %+v", plan)
	}

	plan = PlanGenerate(job, job.SnapshotHash, true, true)
	if !plan.CallModel || plan.Job.State != StateGenerating || plan.Job.Trace.ChargeCount != 0 {
		t.Fatalf("generating = %+v", plan.Job.Trace)
	}
	timed := Timeout(plan.Job, true)
	if timed.State != StateTimedOut || timed.Trace.ChargeCount != 1 || timed.Trace.Billed || timed.Trace.Success {
		t.Fatalf("timeout = %+v", timed.Trace)
	}
	again := Timeout(timed, true)
	if again.Trace.ChargeCount != 1 {
		t.Fatalf("timeout retry charge = %d", again.Trace.ChargeCount)
	}
	replay := PlanGenerate(again, again.SnapshotHash, true, true)
	if replay.CallModel || replay.Compose {
		t.Fatal("timeout retry resubmitted")
	}

	revoked := Revoke(plan.Job)
	if revoked.State != StateRevoked || revoked.Trace.ChargeCount != 0 {
		t.Fatalf("revoke before submit = %+v", revoked.Trace)
	}
	lateDraft := copydraft.ApplyModel(authorizedInput(job.Snapshot), copydraft.ModelOutput{Title: "新手写不该被盖住", Intro: "简介"})
	late := FinishModel(revoked, lateDraft, ModelMeta{ModelID: "m", ModelVersion: "v1", Submitted: true})
	if !late.LateIgnored || late.State != StateRevoked || late.Trace.ChargeCount != 0 || late.Draft.Title == "新手写不该被盖住" {
		t.Fatalf("late result applied: state %s title %q charge %d", late.State, late.Draft.Title, late.Trace.ChargeCount)
	}

	done := FinishModel(plan.Job, lateDraft, ModelMeta{ModelID: "m", ModelVersion: "v1", Submitted: true, Priced: true, AmountMinor: 12, HoldID: "hold_1"})
	if !done.Trace.Success || done.Trace.ChargeCount != 1 || done.Trace.Billed || done.Trace.Settlement != SettlementHeld || done.Trace.AmountMinor == nil || *done.Trace.AmountMinor != 12 {
		t.Fatalf("model finish = %+v", done.Trace)
	}
	if done.Trace.GoBoostProjectID != "" || done.Draft.POI.Mounted {
		t.Fatalf("project or poi: %+v %+v", done.Trace, done.Draft.POI)
	}
	second := FinishModel(done, lateDraft, ModelMeta{Submitted: true})
	if second.Trace.ChargeCount != 1 || !second.LateIgnored {
		t.Fatalf("second result = %+v late %v", second.Trace, second.LateIgnored)
	}
	done.DraftID = "cdf_ok"
	selected, err := Select(done)
	if err != nil {
		t.Fatal(err)
	}
	kept := PlanSave(selected, "我改过的标题", "到店有礼")
	if !kept.Allow || kept.WriteTitle != nil || !contains(kept.Preserved, "title") || kept.WriteIntro == nil || *kept.WriteIntro != "简介" {
		t.Fatalf("handwritten plan = %+v", kept)
	}
	blocked := PlanSave(selected, "我改过的标题", "我也改了简介")
	if blocked.Allow || blocked.Reason != "handwritten_kept" {
		t.Fatalf("full handwritten = %+v", blocked)
	}
	saved := MarkSaved(selected, []string{"title"})
	if saved.State != StateSaved || saved.Trace.RewardsTriggered || saved.Trace.ChargeCount != 1 {
		t.Fatalf("saved = %+v", saved.Trace)
	}
	if Revoke(saved).State != StateSaved || Revoke(saved).Trace.ChargeCount != 1 {
		t.Fatal("revoke rewrote a save or charged again")
	}
}

func TestWithheldModelSentenceDoesNotBecomeSuccess(t *testing.T) {
	job := quoteJob(true, activityspend.ChargeDecision{QuoteCreated: true, Reason: activityspend.ReasonQuoteWithoutDebit})
	job = Confirm(job, job.SnapshotHash)
	plan := PlanGenerate(job, job.SnapshotHash, true, true)
	in := authorizedInput(job.Snapshot)
	draft := copydraft.ApplyModel(in, copydraft.ModelOutput{Title: "全市最低仅19.9元", Intro: "10:00开门"})
	got := FinishModel(plan.Job, draft, ModelMeta{ModelID: "m", ModelVersion: "v1", Submitted: true})
	if got.Trace.Success || got.Draft.Usable || strings.Contains(got.Draft.Title+got.Draft.Intro, "19.9") {
		t.Fatalf("leaked success: %+v %+v", got.Trace, got.Draft)
	}
	save := PlanSave(got, job.BaselineTitle, job.BaselinePublic)
	if save.Allow {
		t.Fatal("unusable draft was savable")
	}
}

func authorizedInput(s Snapshot) copydraft.Input {
	in := s.Input()
	in.ModelAuthorized = true
	return in
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
