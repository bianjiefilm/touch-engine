package rewardrule

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func baseRule() Rule {
	return Rule{
		TenantID: "tenant_a", ActivityID: "act_1", Version: 1,
		EvidenceLevel: EvidenceOfficialPublish, CouponFaceValue: 500, FaceCurrency: "CNY",
	}
}

func official(rule Rule, fact string) Event {
	return Event{
		TenantID: rule.TenantID, ActivityID: rule.ActivityID, RuleVersion: rule.Version,
		SubjectID: "subject_1", FactID: fact, Kind: EventOfficialPublishSuccess, Fixture: true,
	}
}

func assertClosed(t *testing.T, d Decision) {
	t.Helper()
	if d.Label == LabelPublished || strings.Contains(d.Label, LabelPublished) || strings.Contains(d.StatusText, LabelPublished) {
		t.Fatalf("labeled 已发布: %+v", d)
	}
	if d.ChannelReceipt || d.PlatformSuccess || d.CouponsSent != 0 || d.BillingCalls != 0 || d.AICashMinor != 0 || d.PublicAIBalanceMinor != 0 || d.CustomerChargeMinor != 0 || d.MassPublish {
		t.Fatalf("platform success or charge: %+v", d)
	}
	if d.Production != ProductionNotAuthorized || d.Service != ServiceNotVerified || d.Billing != BillingNotVerified {
		t.Fatalf("verification flags: %+v", d)
	}
	if d.Granted && (!d.EngineeringOnly || !d.Fixture || d.Status != StatusEngineeringMatch) {
		t.Fatalf("grant is not an engineering fixture match: %+v", d)
	}
}

func TestEventKinds(t *testing.T) {
	rule := baseRule()
	cases := []struct {
		name      string
		kind      string
		fixture   bool
		grant     bool
		status    string
		text      string
		label     string
		pending   bool
		satisfied bool
		reason    string
	}{
		{"click", EventClick, true, false, StatusNotSatisfied, "", LabelUnmet, false, false, ReasonEvidenceNotMet},
		{"export", EventExport, false, false, StatusNotSatisfied, "", LabelUnmet, false, false, ReasonEvidenceNotMet},
		{"submit", EventSubmitPublish, true, false, StatusNotSatisfied, StatusTextUnconfirmed, LabelNotPublished, false, false, ReasonEvidenceNotMet},
		{"manual", EventManualCredential, false, false, StatusPendingReview, StatusTextUnconfirmed, LabelPendingVerify, true, false, ReasonManualPending},
		{"official fixture", EventOfficialPublishSuccess, true, true, StatusEngineeringMatch, StatusTextFixture, LabelRuleMatched, false, true, ReasonEngineeringMatch},
		{"official unverified", EventOfficialPublishSuccess, false, false, StatusNotSatisfied, StatusTextUnconfirmed, LabelNotPublished, false, false, ReasonChannelUnverified},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := New()
			ev := official(rule, "fact_"+tc.name)
			ev.Kind = tc.kind
			ev.Fixture = tc.fixture
			ev.ClaimedChannelReceipt = true
			d, err := k.Apply(rule, ev)
			if err != nil {
				t.Fatal(err)
			}
			assertClosed(t, d)
			if d.Granted != tc.grant || d.Applied != true || d.Replay || d.Status != tc.status || d.StatusText != tc.text || d.Label != tc.label || d.PendingReview != tc.pending || d.RuleSatisfied != tc.satisfied || d.Reason != tc.reason {
				t.Fatalf("decision = %+v", d)
			}
			if d.ChannelReceipt || d.PlatformSuccess {
				t.Fatalf("receipt flags = %+v", d)
			}
			if tc.grant {
				if d.MarketingFaceMinor != rule.CouponFaceValue || d.FaceCurrency != "CNY" || !d.EngineeringOnly {
					t.Fatalf("face = %+v", d)
				}
			} else if d.MarketingFaceMinor != 0 || k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID) != 0 {
				t.Fatalf("non-grant posted face or grant: %+v count=%d", d, k.LiveGrants())
			}
		})
	}
}

func TestPublishedWordIsNotAnEvent(t *testing.T) {
	k := New()
	rule := baseRule()
	ev := official(rule, "fact_word")
	ev.Kind = LabelPublished
	d, err := k.Apply(rule, ev)
	if !errors.Is(err, ErrUnknownEvent) || d.Granted || d.Applied {
		t.Fatalf("kind 已发布 = %v %+v", err, d)
	}
	assertClosed(t, d)
	if k.LiveGrants() != 0 {
		t.Fatalf("stored %d", k.LiveGrants())
	}
	rule.EvidenceLevel = LabelPublished
	ev.Kind = EventOfficialPublishSuccess
	if _, err = k.Apply(rule, ev); !errors.Is(err, ErrUnknownEvidence) {
		t.Fatalf("evidence = %v", err)
	}
}

func TestReplayDoesNotGrantTwiceOrRelabel(t *testing.T) {
	k := New()
	rule := baseRule()
	ev := official(rule, "fact_replay")
	ev.Kind = EventClick
	first, err := k.Apply(rule, ev)
	if err != nil || first.Granted || !first.Applied || first.Label != LabelUnmet {
		t.Fatalf("first = %v %+v", err, first)
	}
	ev.Kind = EventOfficialPublishSuccess
	ev.Fixture = true
	second, err := k.Apply(rule, ev)
	if err != nil || second.Granted || second.Applied || !second.Replay || second.RuleSatisfied || second.Label != LabelUnmet || second.StatusText == StatusTextUnconfirmed {
		t.Fatalf("relabel = %v %+v", err, second)
	}
	assertClosed(t, second)
	if k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID) != 0 {
		t.Fatal("click became a grant")
	}

	k = New()
	ok := official(rule, "fact_once")
	got, err := k.Apply(rule, ok)
	if err != nil || !got.Granted || !got.Applied {
		t.Fatalf("grant = %v %+v", err, got)
	}
	ok.FactID = " fact_once "
	again, err := k.Apply(rule, ok)
	if err != nil || !again.Replay || again.Applied || !again.Granted {
		t.Fatalf("replay = %v %+v", err, again)
	}
	assertClosed(t, again)
	if k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ok.SubjectID, "fact_once") != 1 || k.LiveGrants() != 1 {
		t.Fatalf("count = %d live=%d", k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ok.SubjectID, "fact_once"), k.LiveGrants())
	}
	other := official(rule, "fact_two")
	if _, err = k.Apply(rule, other); err != nil {
		t.Fatal(err)
	}
	if k.LiveGrants() != 2 || k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, other.SubjectID, other.FactID) != 1 {
		t.Fatalf("second fact live=%d", k.LiveGrants())
	}
}

func TestConcurrentReplayGrantsOnce(t *testing.T) {
	k := New()
	rule := baseRule()
	ev := official(rule, "fact_race")
	var applied atomic.Int32
	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := k.Apply(rule, ev)
			if err != nil {
				errCh <- err
				return
			}
			if d.Label == LabelPublished || d.CouponsSent != 0 || d.PlatformSuccess || d.ChannelReceipt {
				errCh <- errors.New("concurrent fake success")
			}
			if d.Applied {
				applied.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	if applied.Load() != 1 || k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID) != 1 || k.LiveGrants() != 1 {
		t.Fatalf("applied=%d count=%d live=%d", applied.Load(), k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID), k.LiveGrants())
	}
}

func TestOtherTenantIsRejected(t *testing.T) {
	k := New()
	rule := baseRule()
	ev := official(rule, "fact_shared")
	if _, err := k.Apply(rule, ev); err != nil {
		t.Fatal(err)
	}
	fresh := official(rule, "fact_foreign")
	fresh.TenantID = "tenant_b"
	d, err := k.Apply(rule, fresh)
	if !errors.Is(err, ErrTenantRejected) || d.Granted || d.Applied || d.Reason != ReasonTenantRejected {
		t.Fatalf("mismatched tenant = %v %+v", err, d)
	}
	assertClosed(t, d)
	other := rule
	other.TenantID = "tenant_b"
	copied := ev
	copied.TenantID = "tenant_b"
	d, err = k.Apply(other, copied)
	if !errors.Is(err, ErrTenantRejected) || d.Granted || d.Applied {
		t.Fatalf("same fact other tenant = %v %+v", err, d)
	}
	if k.LiveGrants() != 1 || k.GrantCount(other.TenantID, other.ActivityID, other.Version, copied.SubjectID, copied.FactID) != 0 {
		t.Fatalf("live=%d", k.LiveGrants())
	}
	if k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID) != 1 {
		t.Fatal("owner grant lost")
	}
}

func TestFactStaysWithActivityAndSubject(t *testing.T) {
	k := New()
	rule := baseRule()
	ev := official(rule, "fact_bound")
	if _, err := k.Apply(rule, ev); err != nil {
		t.Fatal(err)
	}
	moved := rule
	moved.ActivityID = "act_2"
	evMoved := ev
	evMoved.ActivityID = "act_2"
	if _, err := k.Apply(moved, evMoved); !errors.Is(err, ErrActivityMismatch) {
		t.Fatalf("activity = %v", err)
	}
	evSub := ev
	evSub.SubjectID = "subject_2"
	if _, err := k.Apply(rule, evSub); !errors.Is(err, ErrSubjectMismatch) {
		t.Fatalf("subject = %v", err)
	}
	bad := official(rule, "fact_new")
	bad.ActivityID = "somewhere_else"
	if _, err := k.Apply(rule, bad); !errors.Is(err, ErrActivityMismatch) {
		t.Fatalf("event activity = %v", err)
	}
	bad = official(rule, "fact_ver")
	bad.RuleVersion = rule.Version + 3
	if _, err := k.Apply(rule, bad); !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("version = %v", err)
	}
	if k.LiveGrants() != 1 {
		t.Fatalf("live=%d", k.LiveGrants())
	}
}

func TestRevokeBlocksReplay(t *testing.T) {
	k := New()
	rule := baseRule()
	ev := official(rule, "fact_revoke")
	if _, err := k.Apply(rule, ev); err != nil {
		t.Fatal(err)
	}
	revoked, err := k.Revoke(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID)
	if err != nil || revoked.Granted || !revoked.Revoked || revoked.Reason != ReasonRevokedNoRegrant || revoked.CouponsSent != 0 {
		t.Fatalf("revoke = %v %+v", err, revoked)
	}
	assertClosed(t, revoked)
	again, err := k.Revoke(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, " "+ev.FactID+" ")
	if err != nil || again.Granted || !again.Revoked {
		t.Fatalf("second revoke = %v %+v", err, again)
	}
	replay, err := k.Apply(rule, ev)
	if err != nil || replay.Granted || replay.Applied || !replay.Replay || !replay.Revoked || replay.Reason != ReasonRevokedNoRegrant {
		t.Fatalf("replay after revoke = %v %+v", err, replay)
	}
	assertClosed(t, replay)
	if replay.Label == LabelPublished || replay.PlatformSuccess {
		t.Fatalf("revoked label = %+v", replay)
	}
	if k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID) != 0 || k.LiveGrants() != 0 {
		t.Fatalf("count live=%d", k.LiveGrants())
	}
	if _, err = k.Revoke("tenant_b", rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("other tenant revoke = %v", err)
	}
	next := official(rule, "fact_after_revoke")
	got, err := k.Apply(rule, next)
	if err != nil || !got.Granted || k.LiveGrants() != 1 {
		t.Fatalf("later fact = %v %+v live=%d", err, got, k.LiveGrants())
	}
}

func TestRuleVersionDoesNotRetroactivelyGrant(t *testing.T) {
	k := New()
	rule := baseRule()
	oldClick := official(rule, "fact_old")
	oldClick.Kind = EventClick
	oldClick.Fixture = true
	if d, err := k.Apply(rule, oldClick); err != nil || d.Granted {
		t.Fatalf("click = %v %+v", err, d)
	}
	matched := official(rule, "fact_matched")
	if _, err := k.Apply(rule, matched); err != nil {
		t.Fatal(err)
	}
	if _, err := Revise(rule, " "); !errors.Is(err, ErrNoticeRequired) {
		t.Fatalf("blank notice = %v", err)
	}
	next, err := Revise(rule, " 版本更新，旧事实不补发 ")
	if err != nil || next.Version != 2 || next.EvidenceLevel != EvidenceOfficialPublish || next.UserNotice != "版本更新，旧事实不补发" || next.CouponFaceValue != rule.CouponFaceValue {
		t.Fatalf("revise = %v %+v", err, next)
	}
	if k.GrantCount(rule.TenantID, rule.ActivityID, 1, matched.SubjectID, matched.FactID) != 1 || k.GrantCount(next.TenantID, next.ActivityID, next.Version, matched.SubjectID, matched.FactID) != 0 {
		t.Fatal("revise rewrote the ledger")
	}
	reclick := oldClick
	reclick.RuleVersion = next.Version
	reclick.Kind = EventOfficialPublishSuccess
	d, err := k.Apply(next, reclick)
	if err != nil || d.Granted || d.Applied || d.Reason != ReasonNotRetroactive {
		t.Fatalf("old click on v2 = %v %+v", err, d)
	}
	assertClosed(t, d)
	rematch := matched
	rematch.RuleVersion = next.Version
	d, err = k.Apply(next, rematch)
	if err != nil || d.Granted || d.Applied || d.Reason != ReasonNotRetroactive {
		t.Fatalf("old match on v2 = %v %+v", err, d)
	}
	if k.LiveGrants() != 1 {
		t.Fatalf("live=%d", k.LiveGrants())
	}
	fresh := official(next, "fact_new_version")
	got, err := k.Apply(next, fresh)
	if err != nil || !got.Granted || !got.Applied || !got.EngineeringOnly {
		t.Fatalf("new fact = %v %+v", err, got)
	}
	assertClosed(t, got)
	if k.GrantCount(next.TenantID, next.ActivityID, next.Version, fresh.SubjectID, fresh.FactID) != 1 || k.LiveGrants() != 2 {
		t.Fatalf("live=%d", k.LiveGrants())
	}
	if k.GrantCount(rule.TenantID, rule.ActivityID, 1, oldClick.SubjectID, oldClick.FactID) != 0 {
		t.Fatal("old click granted")
	}
}

func TestFaceValueIsMarketingAmount(t *testing.T) {
	rule := baseRule()
	got, err := PostFace(rule, AccountMarketingFace)
	if err != nil || got.AccountClass != AccountMarketingFace || got.MarketingMinor != 500 || got.Currency != "CNY" || got.AICashMinor != 0 || got.PublicAIBalanceMinor != 0 || got.CustomerChargeMinor != 0 || got.CouponsSent != 0 || got.BillingCalls != 0 {
		t.Fatalf("marketing = %v %+v", err, got)
	}
	for _, class := range []string{AccountAICash, AccountPublicAIBalance, AccountCustomerCharge, ""} {
		got, err = PostFace(rule, class)
		if !errors.Is(err, ErrFaceNotCharge) || got != (FacePosting{}) {
			t.Fatalf("%s = %v %+v", class, err, got)
		}
	}
	rule.CouponFaceValue = -5
	if _, err = PostFace(rule, AccountMarketingFace); !errors.Is(err, ErrNegativeFace) {
		t.Fatalf("negative = %v", err)
	}
	k := New()
	rule = baseRule()
	rule.CouponFaceValue = -1
	ev := official(rule, "fact_face")
	d, err := k.Apply(rule, ev)
	if !errors.Is(err, ErrNegativeFace) || d.Granted || d.MarketingFaceMinor != 0 || d.AICashMinor != 0 {
		t.Fatalf("apply negative = %v %+v", err, d)
	}
	if k.LiveGrants() != 0 {
		t.Fatal("negative face stored a grant")
	}
}

func TestNoMassPublishAndNoSocialSecret(t *testing.T) {
	if ProductionNotAuthorized != "NOT_AUTHORIZED" || ServiceNotVerified != "NOT_VERIFIED" || BillingNotVerified != "NOT_VERIFIED" || HumanUnknown != "UNKNOWN" || BrowserNotRun != "NOT_RUN" {
		t.Fatal("slice flags drifted")
	}
	if StatusTextUnconfirmed != "待核实" || LabelPublished != "已发布" || StatusPendingReview != "pending_review" || StatusTextFixture == "已发布" {
		t.Fatal("status copy drifted")
	}
	for _, kind := range []string{EventClick, EventExport, EventSubmitPublish, EventOfficialPublishSuccess, EventManualCredential} {
		if kind == LabelPublished || strings.Contains(kind, LabelPublished) {
			t.Fatalf("event kind %q is a publish label", kind)
		}
	}
	k := New()
	rule := baseRule()
	d, err := k.MassPublish(rule, []string{"subject_a", "subject_b", "subject_c"})
	if !errors.Is(err, ErrMassPublish) || d.Reason != ReasonMassPublish || d.MassPublish || d.CouponsSent != 0 || d.Granted {
		t.Fatalf("mass = %v %+v", err, d)
	}
	assertClosed(t, d)
	if _, err = (*Kernel)(nil).MassPublish(rule, nil); !errors.Is(err, ErrMassPublish) {
		t.Fatalf("nil mass = %v", err)
	}
	if k.LiveGrants() != 0 {
		t.Fatal("mass publish wrote grants")
	}
	secret := "sek-hui1671-do-not-keep"
	ev := official(rule, "fact_secret")
	ev.SocialSecret = secret
	d, err = k.Apply(rule, ev)
	if !errors.Is(err, ErrSocialSecret) || d.Granted || d.Applied {
		t.Fatalf("secret = %v %+v", err, d)
	}
	blob := err.Error() + d.Reason + d.Label + d.StatusText + d.Status
	if strings.Contains(blob, secret) || strings.Contains(blob, "sek-") {
		t.Fatalf("secret echoed: %s", blob)
	}
	assertClosed(t, d)
	ev.SocialSecret = ""
	if _, err = k.Apply(rule, ev); err != nil {
		t.Fatal(err)
	}
	ev.SocialSecret = " " + secret
	d, err = k.Apply(rule, ev)
	if !errors.Is(err, ErrSocialSecret) || strings.Contains(err.Error(), secret) {
		t.Fatalf("replay secret = %v %+v", err, d)
	}
	if k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID) != 1 || k.LiveGrants() != 1 {
		t.Fatalf("secret replay changed ledger live=%d", k.LiveGrants())
	}
}

func TestMissingKernelAndFields(t *testing.T) {
	rule := baseRule()
	ev := official(rule, "fact_missing")
	var k *Kernel
	if _, err := k.Apply(rule, ev); !errors.Is(err, ErrMissingKernel) {
		t.Fatalf("nil apply = %v", err)
	}
	if k.GrantCount(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID) != 0 {
		t.Fatal("nil count")
	}
	if _, err := k.Revoke(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID); !errors.Is(err, ErrMissingKernel) {
		t.Fatalf("nil revoke = %v", err)
	}
	k = New()
	rule.ActivityID = " "
	if d, err := k.Apply(rule, ev); !errors.Is(err, ErrMissingField) || d.Granted {
		t.Fatalf("blank activity = %v %+v", err, d)
	}
	rule = baseRule()
	ev.FactID = " "
	if _, err := k.Apply(rule, ev); !errors.Is(err, ErrMissingField) {
		t.Fatalf("blank fact = %v", err)
	}
	ev = official(rule, "fact_unknown_kind")
	ev.Kind = "preview"
	if _, err := k.Apply(rule, ev); !errors.Is(err, ErrUnknownEvent) {
		t.Fatalf("preview = %v", err)
	}
	if _, err := k.Revoke(rule.TenantID, rule.ActivityID, rule.Version, ev.SubjectID, ev.FactID); !errors.Is(err, ErrGrantNotFound) {
		t.Fatalf("missing revoke = %v", err)
	}
	if k.LiveGrants() != 0 {
		t.Fatal("invalid events stored a grant")
	}
}

func TestIdempotencyKeyParts(t *testing.T) {
	base := IdempotencyKey("t", "a", 1, "s", "f")
	if base == "" ||
		base == IdempotencyKey("t2", "a", 1, "s", "f") ||
		base == IdempotencyKey("t", "a2", 1, "s", "f") ||
		base == IdempotencyKey("t", "a", 2, "s", "f") ||
		base == IdempotencyKey("t", "a", 1, "s2", "f") ||
		base == IdempotencyKey("t", "a", 1, "s", "f2") {
		t.Fatalf("key = %q", base)
	}
}
