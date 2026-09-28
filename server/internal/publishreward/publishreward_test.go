package publishreward

import (
	"sync"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
)

func closedChannels() []ChannelProof {
	return MatrixChannels([]custpublish.AdapterNote{{Platform: "douyin", Name: "douyin-openapi"}})
}

func TestMatrixKeepsPublishRewardPending(t *testing.T) {
	channels := closedChannels()
	if len(channels) != 4 {
		t.Fatalf("channels = %d", len(channels))
	}
	for _, ch := range channels {
		if ch.ConfirmPublish {
			t.Fatalf("%s confirm_publish opened by an adapter note", ch.Platform)
		}
		if len(ch.EvidenceURL) < 8 || ch.EvidenceURL[:8] != "https://" {
			t.Fatalf("%s missing official evidence: %q", ch.Platform, ch.EvidenceURL)
		}
	}
	rule, err := Configure(ConfigureInput{
		CampaignID: "cmp_a", Trigger: TriggerOfficialPublish, RequestIssuance: true,
		Channels: channels,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rule.Version != 1 || rule.Status != StatusPendingVerification || rule.GrantEnabled || rule.IssuanceEnabled {
		t.Fatalf("rule = %+v", rule)
	}
	if rule.Reason != ReasonChannelCannotProve || rule.EvidenceLevel != EvidenceOfficialQuery {
		t.Fatalf("rule binding = %+v", rule)
	}
}

func TestConsumeRejectsNonPublishFacts(t *testing.T) {
	rule, err := Configure(ConfigureInput{
		CampaignID: "cmp_a", Trigger: TriggerOfficialPublish, Channels: closedChannels(),
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		kind   string
		reason string
	}{
		{KindPreview, ReasonPreviewNotPublish},
		{KindExport, ReasonExportNotPublish},
		{KindEditorOpened, ReasonEditorNotPublish},
		{KindUnknown, ReasonUnknownNotPublish},
		{KindSelfReport, ReasonSelfReportNotPublish},
		{KindClick, ReasonClickNotPublish},
		{KindMerchantPublish, ReasonMerchantNotCustomer},
	}
	for _, tc := range cases {
		d := Consume(rule, Fact{
			ID: "fact_1", CampaignID: "cmp_a", SubjectID: "cust_1", Platform: "douyin",
			Kind: tc.kind, Publisher: custpublish.PublisherActivityCustomer,
			Status: tc.kind, PlatformPostID: "forged_post", ReceiptSource: "client",
		})
		if d.Grant || d.Eligible || d.PlatformConfirmed || d.PostID != "" || d.CouponsIssued != 0 || d.FeesCharged != 0 || d.OutboundCalls != 0 {
			t.Fatalf("%s consumed: %+v", tc.kind, d)
		}
		if d.Reason != tc.reason {
			t.Fatalf("%s reason = %s", tc.kind, d.Reason)
		}
	}
}

func TestConsumeRequiresThisCampaignAndOfficialFact(t *testing.T) {
	rule, err := Configure(ConfigureInput{
		CampaignID: "cmp_a", Trigger: TriggerOfficialPublish, Channels: closedChannels(),
	})
	if err != nil {
		t.Fatal(err)
	}
	other := Consume(rule, officialFact("cmp_other"))
	if other.Reason != ReasonCampaignMismatch || other.Grant || other.PostID != "" {
		t.Fatalf("other campaign = %+v", other)
	}
	// 渠道确认能力关闭时，即使事实长得像官方回执，也不能当成发布成功。
	closed := Consume(rule, officialFact("cmp_a"))
	if closed.Grant || closed.Eligible || closed.PlatformConfirmed || closed.PostID != "" || closed.Reason != ReasonChannelCannotProve {
		t.Fatalf("closed channel = %+v", closed)
	}

	open := rule
	open.Channels = []ChannelProof{{Platform: "douyin", ConfirmPublish: true}}
	held := Consume(open, officialFact("cmp_a"))
	if !held.Eligible || held.Grant || held.CouponsIssued != 0 || held.FeesCharged != 0 || held.OutboundCalls != 0 || held.Reason != ReasonIssuanceClosed {
		t.Fatalf("issuance must stay closed: %+v", held)
	}
	if held.AccountClass != AccountMarketing {
		t.Fatalf("account = %s", held.AccountClass)
	}
}

func TestManualProofStaysPending(t *testing.T) {
	fact := Fact{
		ID: "fact_1", CampaignID: "cmp_a", SubjectID: "cust_1", Kind: KindSelfReport,
		Publisher: custpublish.PublisherActivityCustomer, PlatformPostID: "typed_by_user",
	}
	proof := SubmitManualProof(fact, "聊天截图")
	if proof.Status != StatusPendingReview || proof.PlatformConfirmed || proof.Grant || proof.PostID != "" {
		t.Fatalf("submit = %+v", proof)
	}
	unchanged, _, err := ReviewProof(proof, ReviewInput{Actor: "staff", Allowed: false, Basis: "我觉得发了", Approve: true})
	if err == nil || ReasonOf(err) != ReasonReviewPermission || unchanged.Status != StatusPendingReview || unchanged.PlatformConfirmed {
		t.Fatalf("default review = %v %+v", err, unchanged)
	}
	still, _, err := ReviewProof(proof, ReviewInput{Actor: "owner", Allowed: true, Basis: "  ", Approve: true})
	if err == nil || ReasonOf(err) != ReasonReviewBasis || still.PlatformConfirmed {
		t.Fatalf("empty basis = %v %+v", err, still)
	}
	reviewed, audit, err := ReviewProof(proof, ReviewInput{Actor: "owner", Allowed: true, Basis: "只核对了截图，没有平台回执", Approve: true})
	if err != nil || reviewed.PlatformConfirmed || reviewed.Grant || reviewed.Status != StatusReviewedNotConfirmed || reviewed.PostID != "" {
		t.Fatalf("approve = %v %+v", err, reviewed)
	}
	if audit.Actor != "owner" || audit.Basis == "" || audit.PlatformConfirmed || audit.Decision != DecisionNotPlatformConfirmed {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestCannotRenamePreviewIntoParticipation(t *testing.T) {
	preview := Fact{ID: "fact_1", CampaignID: "cmp_a", Kind: KindPreview, Publisher: custpublish.PublisherActivityCustomer}
	renamed, err := RenameFactKind(preview, KindParticipation)
	if err == nil || ReasonOf(err) != ReasonFactRename || renamed.Kind != KindPreview {
		t.Fatalf("rename = %v %+v", err, renamed)
	}
	current, err := Configure(ConfigureInput{CampaignID: "cmp_a", Trigger: TriggerOfficialPublish, Channels: closedChannels()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ReviseTrigger(current, TriggerParticipation, "")
	if err == nil || ReasonOf(err) != ReasonUserNotice {
		t.Fatalf("silent retarget = %v", err)
	}
	next, err := ReviseTrigger(current, TriggerParticipation, "这次改成参与奖励，以前的预览和导出不算")
	if err != nil || next.Version != 2 || next.Trigger != TriggerParticipation || next.UserNotice == "" || next.IssuanceEnabled {
		t.Fatalf("next = %v %+v", err, next)
	}
	d := Consume(next, preview)
	if d.Grant || d.Eligible || d.Reason != ReasonParticipationEvidence {
		t.Fatalf("old preview consumed by new rule: %+v", d)
	}
}

func TestLedgerIsIdempotentAndSeparate(t *testing.T) {
	book := NewBook(1)
	claim := Entry{
		CampaignID: "cmp_a", SubjectID: "cust_1", RuleVersion: 1, FactID: "fact_1",
		Op: OpClaim, AccountClass: AccountMarketing,
	}
	first, err := book.Post(claim)
	if err != nil || !first.Applied || first.CouponsIssued != 0 || first.FeesCharged != 0 || first.AccountClass != AccountMarketing {
		t.Fatalf("first = %v %+v", err, first)
	}
	replay, err := book.Post(claim)
	if err != nil || replay.Applied || book.Count(claim.Key(), OpClaim) != 1 {
		t.Fatalf("replay = %v %+v count=%d", err, replay, book.Count(claim.Key(), OpClaim))
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = book.Post(claim)
		}()
	}
	wg.Wait()
	if book.Count(claim.Key(), OpClaim) != 1 {
		t.Fatalf("concurrent claims = %d", book.Count(claim.Key(), OpClaim))
	}

	redeem := claim
	redeem.Op = OpRedeem
	redeemed, err := book.Post(redeem)
	if err != nil || !redeemed.Applied || book.Count(claim.Key(), OpClaim) != 1 || book.Count(claim.Key(), OpRedeem) != 1 {
		t.Fatalf("redeem = %v %+v", err, redeemed)
	}
	redeemReplay, err := book.Post(redeem)
	if err != nil || redeemReplay.Applied || book.Count(claim.Key(), OpRedeem) != 1 {
		t.Fatalf("redeem replay = %v count=%d", err, book.Count(claim.Key(), OpRedeem))
	}

	revoke := claim
	revoke.Op = OpRevoke
	revoked, err := book.Post(revoke)
	if err != nil || !revoked.Applied || book.Count(claim.Key(), OpClaim) != 1 || book.Count(claim.Key(), OpRevoke) != 1 {
		t.Fatalf("revoke = %v %+v", err, revoked)
	}
	again, err := book.Post(revoke)
	if err != nil || again.Applied || book.Count(claim.Key(), OpRevoke) != 1 {
		t.Fatalf("revoke replay = %v count=%d", err, book.Count(claim.Key(), OpRevoke))
	}

	second := claim
	second.FactID = "fact_2"
	over, err := book.Post(second)
	if err != nil || over.Outcome != OutcomeOverLimit || book.Count(second.Key(), OpOverLimit) != 1 || book.Count(second.Key(), OpClaim) != 0 {
		t.Fatalf("over limit = %v %+v", err, over)
	}
	overReplay, err := book.Post(second)
	if err != nil || overReplay.Applied || book.Count(second.Key(), OpOverLimit) != 1 {
		t.Fatalf("over limit replay = %v %+v", err, overReplay)
	}

	for _, class := range []string{AccountOrderService, AccountToolBalance, AccountUnsettled} {
		bad := claim
		bad.FactID = "fact_" + class
		bad.AccountClass = class
		bad.DeductFrom = class
		if _, err := book.Post(bad); err == nil || ReasonOf(err) != ReasonAccountClass {
			t.Fatalf("%s accepted: %v", class, err)
		}
		if book.Count(bad.Key(), OpClaim) != 0 {
			t.Fatalf("%s wrote a row", class)
		}
	}
	deduct := claim
	deduct.FactID = "fact_deduct"
	deduct.DeductFrom = AccountUnsettled
	if _, err := book.Post(deduct); err == nil || ReasonOf(err) != ReasonMustNotDeduct {
		t.Fatalf("unsettled deduct = %v", err)
	}
}

func TestIssueNeverSendsCoupons(t *testing.T) {
	gw := &Gateway{}
	out := gw.Issue(Decision{Eligible: true, Grant: true, Reason: ReasonIssuanceClosed})
	if out.CouponsIssued != 0 || out.FeesCharged != 0 || out.OutboundCalls != 0 || gw.Calls != 0 {
		t.Fatalf("issued = %+v calls=%d", out, gw.Calls)
	}
}

func officialFact(campaign string) Fact {
	return Fact{
		ID: "fact_official", CampaignID: campaign, SubjectID: "cust_1", Platform: "douyin",
		Kind: KindOfficial, Publisher: custpublish.PublisherActivityCustomer,
		Status: custpublish.StatusPublishConfirmed, ReceiptSource: custpublish.ReceiptOfficialQuery,
		PlatformPostID: "dy_real",
	}
}
