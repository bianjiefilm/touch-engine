package matrixhandoff

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func videoFixture() *FixtureClient { return &FixtureClient{VideoOnly: true} }

func publisher() Actor {
	return Actor{TenantID: "ten_a", BrandID: "brand_a", Role: RoleBrandPublisher}
}

func baseReq() Request {
	return Request{
		TenantID:            "ten_a",
		BrandID:             "brand_a",
		StoreID:             "store_a",
		ActivityID:          "act_a",
		ActivityVersion:     3,
		Assets:              []Asset{{ID: "asset_video", Hash: "hash_v1", Kind: "video"}},
		OfferText:           "第二杯半价",
		OfferExpiry:         time.Date(2026, 10, 8, 16, 0, 0, 0, time.UTC),
		Disclosure:          "商家授权账号发布",
		ReturnLocationToken: "return_act_a",
		AccountPool:         []string{"pool_account_should_not_copy"},
		SocialSecret:        "social-secret-should-not-store",
		ActivityStatus:      "locked",
	}
}

var handoffNow = time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

func mustNotClaimPublishSuccess(t *testing.T, copy string) {
	t.Helper()
	if strings.Contains(copy, "发布成功") {
		t.Fatalf("copy claims publish success: %s", copy)
	}
}

func assertClean(t *testing.T, doc Draft) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, bad := range []string{
		"social-secret-should-not-store",
		"pool_account_should_not_copy",
		"access_token",
		"refresh_token",
		"app_secret",
		"password",
		"cookie",
		"account_pool",
		"accounts",
		"locked",
		"EcoTopNav",
		"发布成功",
	} {
		if strings.Contains(body, bad) {
			t.Fatalf("document contains %s: %s", bad, body)
		}
	}
	if doc.ActivityStatus != ActivityOpen {
		t.Fatalf("activity status = %s", doc.ActivityStatus)
	}
	if doc.Executed || doc.CustomerPublish || doc.Charged || doc.Transcoded || doc.AICutCalled {
		t.Fatalf("draft executed work: %+v", doc)
	}
	if doc.Activity.TenantID == "" || doc.ReturnLocationToken == "" || doc.Activity.Disclosure == "" {
		t.Fatalf("handoff missing activity facts: %+v", doc)
	}
}

func activityJSON(t *testing.T, doc Draft) string {
	t.Helper()
	raw, err := json.Marshal(doc.Activity)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTouchActivityIsNotMatrixPlan(t *testing.T) {
	if reflect.TypeOf(TouchActivity{}) == reflect.TypeOf(MatrixPlanRef{}) {
		t.Fatal("touch activity and matrix plan are the same type")
	}
	rt := reflect.TypeOf(MatrixPlanRef{})
	for i := 0; i < rt.NumField(); i++ {
		name := strings.ToLower(rt.Field(i).Name + rt.Field(i).Tag.Get("json"))
		if strings.Contains(name, "account") || strings.Contains(name, "secret") || strings.Contains(name, "token") {
			t.Fatalf("plan ref copies account material: %s", rt.Field(i).Name)
		}
	}
}

func TestDraftStoresReferenceAndDoesNotPublish(t *testing.T) {
	client := videoFixture()
	sink := &EffectSink{}
	svc := New(client, sink)
	res, err := svc.CreateDraft(handoffNow, publisher(), baseReq())
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Draft.Status != StatusDraft || res.Draft.Approved || res.Draft.Plan.ID == "" {
		t.Fatalf("draft = %+v success=%v", res.Draft, res.Success)
	}
	if !strings.HasPrefix(res.Draft.Plan.ID, "planref:") {
		t.Fatalf("plan ref = %s", res.Draft.Plan.ID)
	}
	mustNotClaimPublishSuccess(t, res.Draft.Copy)
	assertClean(t, res.Draft)
	if client.Puts != 1 || client.Records != 0 {
		t.Fatalf("client puts=%d records=%d", client.Puts, client.Records)
	}
	if svc.LiveMatrix() || client.LiveMatrix() {
		t.Fatal("fixture is live")
	}
	if VerificationService != "NOT_VERIFIED" || VerificationBilling != "NOT_VERIFIED" || VerificationProduction != "NOT_AUTHORIZED" || VerificationHuman != "UNKNOWN" {
		t.Fatal("verification flags drifted")
	}
	if svc.ChargeCount() != 0 || svc.TranscodeCount() != 0 || svc.AICutCount() != 0 {
		t.Fatal("draft charged, transcoded, or called AICut")
	}
	if sink.RewardWrites != 0 || sink.LeadWrites != 0 || sink.UGCWrites != 0 || sink.GrantCalls != 0 {
		t.Fatalf("sink = %+v", sink)
	}
}

func TestVideoOnlyFixtureWithoutVideoIsNeedsVideo(t *testing.T) {
	client := videoFixture()
	svc := New(client, &EffectSink{})
	req := baseReq()
	req.Assets = []Asset{{ID: "asset_img", Hash: "hash_img", Kind: "image"}}
	res, err := svc.CreateDraft(handoffNow, publisher(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || res.Draft.Status != StatusNeedsVideo || res.Draft.Plan.ID != "" {
		t.Fatalf("needs_video = %+v success=%v", res.Draft, res.Success)
	}
	mustNotClaimPublishSuccess(t, res.Draft.Copy)
	assertClean(t, res.Draft)
	if client.Puts != 0 || svc.ChargeCount() != 0 || svc.TranscodeCount() != 0 || svc.AICutCount() != 0 {
		t.Fatalf("needs_video did work puts=%d charge=%d transcode=%d aicut=%d", client.Puts, svc.ChargeCount(), svc.TranscodeCount(), svc.AICutCount())
	}
	again, err := svc.CreateDraft(handoffNow, publisher(), req)
	if err != nil || again.Draft.ID != res.Draft.ID || again.Draft.Status != StatusNeedsVideo || again.Success {
		t.Fatalf("retry = %+v %v", again, err)
	}

	empty := baseReq()
	empty.Assets = nil
	noAsset, err := svc.CreateDraft(handoffNow, publisher(), empty)
	if err != nil || noAsset.Success || noAsset.Draft.Status != StatusNeedsVideo || noAsset.Draft.ID == res.Draft.ID {
		t.Fatalf("empty assets = %+v %v", noAsset, err)
	}
	if client.Puts != 0 {
		t.Fatalf("puts = %d", client.Puts)
	}
}

func TestSameKeyReturnsSameDraftWithoutOverwrite(t *testing.T) {
	client := videoFixture()
	svc := New(client, &EffectSink{})
	first, err := svc.CreateDraft(handoffNow, publisher(), baseReq())
	if err != nil {
		t.Fatal(err)
	}
	replayReq := baseReq()
	replayReq.OfferText = "改过的卖点"
	replayReq.StoreID = "store_other"
	replayReq.Disclosure = "另一份声明"
	replayReq.ReturnLocationToken = "return_other"
	replayReq.Assets[0].ID = "other_asset"
	second, err := svc.CreateDraft(handoffNow, publisher(), replayReq)
	if err != nil {
		t.Fatal(err)
	}
	if second.Draft.ID != first.Draft.ID || second.Draft.Version != 1 || second.Success {
		t.Fatalf("replay = %+v", second)
	}
	if second.Draft.Activity.OfferText != "第二杯半价" || second.Draft.Activity.StoreID != "store_a" || second.Draft.ReturnLocationToken != "return_act_a" {
		t.Fatalf("replay overwrote draft: %+v", second.Draft)
	}
	if client.Puts != 1 || len(svc.List()) != 1 {
		t.Fatalf("puts=%d drafts=%d", client.Puts, len(svc.List()))
	}
	mustNotClaimPublishSuccess(t, second.Draft.Copy)
	assertClean(t, second.Draft)
}

func TestNewVersionDoesNotOverwriteApprovedPlan(t *testing.T) {
	client := videoFixture()
	sink := &EffectSink{}
	svc := New(client, sink)
	first, err := svc.CreateDraft(handoffNow, publisher(), baseReq())
	if err != nil {
		t.Fatal(err)
	}
	approved, err := svc.Confirm(handoffNow, publisher(), first.Draft.ID)
	if err != nil || !approved.Draft.Approved || approved.Success || approved.Draft.Status != StatusApproved {
		t.Fatalf("confirm = %+v %v", approved, err)
	}
	mustNotClaimPublishSuccess(t, approved.Draft.Copy)
	beforeActivity := activityJSON(t, approved.Draft)
	beforeRef := approved.Draft.Plan.ID
	beforeReturn := approved.Draft.ReturnLocationToken

	changed := baseReq()
	changed.Assets = []Asset{{ID: "asset_video", Hash: "hash_v2", Kind: "video"}}
	next, err := svc.CreateDraft(handoffNow, publisher(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if next.Draft.ID == first.Draft.ID || next.Draft.Version != 2 || next.Draft.Approved || next.Success {
		t.Fatalf("new hash draft = %+v", next.Draft)
	}
	kept, ok := svc.Get(first.Draft.ID)
	if !ok || !kept.Approved || kept.Superseded || activityJSON(t, kept) != beforeActivity || kept.Plan.ID != beforeRef || kept.ReturnLocationToken != beforeReturn {
		t.Fatalf("approved plan changed before new confirmation: %+v", kept)
	}

	later := baseReq()
	later.OfferExpiry = later.OfferExpiry.Add(24 * time.Hour)
	expiryDraft, err := svc.CreateDraft(handoffNow, publisher(), later)
	if err != nil {
		t.Fatal(err)
	}
	if expiryDraft.Draft.ID == first.Draft.ID || expiryDraft.Draft.ID == next.Draft.ID || expiryDraft.Draft.Approved || expiryDraft.Draft.Version != 3 {
		t.Fatalf("new expiry draft = %+v", expiryDraft.Draft)
	}
	kept, _ = svc.Get(first.Draft.ID)
	if !kept.Approved || activityJSON(t, kept) != beforeActivity || kept.Plan.ID != beforeRef {
		t.Fatalf("expiry draft overwrote approved plan: %+v", kept)
	}

	bumped := baseReq()
	bumped.ActivityVersion = 4
	versionDraft, err := svc.CreateDraft(handoffNow, publisher(), bumped)
	if err != nil || versionDraft.Draft.ID == first.Draft.ID || versionDraft.Draft.Approved {
		t.Fatalf("activity version draft = %+v %v", versionDraft, err)
	}

	confirmed, err := svc.Confirm(handoffNow, publisher(), next.Draft.ID)
	if err != nil || !confirmed.Draft.Approved || confirmed.Draft.ID != next.Draft.ID || confirmed.Success {
		t.Fatalf("new confirmation = %+v %v", confirmed, err)
	}
	kept, _ = svc.Get(first.Draft.ID)
	if kept.Approved || !kept.Superseded || kept.ActivityStatus != ActivityOpen {
		t.Fatalf("old plan approval = %+v", kept)
	}
	if activityJSON(t, kept) != beforeActivity || kept.Plan.ID != beforeRef || kept.ReturnLocationToken != beforeReturn || kept.ID != first.Draft.ID {
		t.Fatalf("new confirmation overwrote approved plan content: %+v", kept)
	}
	if sink.RewardWrites != 0 || sink.LeadWrites != 0 || sink.UGCWrites != 0 || sink.GrantCalls != 0 {
		t.Fatalf("confirmation wrote customer effects: %+v", sink)
	}
}

func TestAuthorityRejections(t *testing.T) {
	cases := []struct {
		name   string
		actor  Actor
		reason string
	}{
		{name: "operator", actor: Actor{TenantID: "ten_a", BrandID: "brand_a", Role: RoleOperatorAdmin}, reason: ReasonOperatorNotPublisher},
		{name: "revoked", actor: Actor{TenantID: "ten_a", BrandID: "brand_a", Role: RoleDelegate, Revoked: true}, reason: ReasonDelegateRevoked},
		{name: "cross-tenant", actor: Actor{TenantID: "ten_b", BrandID: "brand_a", Role: RoleBrandPublisher}, reason: ReasonCrossTenant},
		{name: "cross-brand", actor: Actor{TenantID: "ten_a", BrandID: "brand_b", Role: RoleBrandPublisher}, reason: ReasonCrossBrand},
		{name: "staff", actor: Actor{TenantID: "ten_a", BrandID: "brand_a", Role: "staff"}, reason: ReasonForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(videoFixture(), &EffectSink{})
			res, err := svc.CreateDraft(handoffNow, tc.actor, baseReq())
			if ReasonOf(err) != tc.reason || res.Success || res.Draft.ID != "" {
				t.Fatalf("got %v %+v", err, res)
			}
			if len(svc.List()) != 0 || svc.RewardWrites() != 0 || svc.LeadWrites() != 0 {
				t.Fatal("rejection stored a draft or customer write")
			}
		})
	}

	svc := New(videoFixture(), &EffectSink{})
	delegate := Actor{TenantID: "ten_a", BrandID: "brand_a", Role: RoleDelegate}
	res, err := svc.CreateDraft(handoffNow, delegate, baseReq())
	if err != nil || res.Draft.Status != StatusDraft || res.Success {
		t.Fatalf("active delegate = %+v %v", res, err)
	}
}

func TestExpiredOfferIsNotPublished(t *testing.T) {
	sink := &EffectSink{}
	svc := New(videoFixture(), sink)
	expired := baseReq()
	expired.OfferExpiry = handoffNow
	res, err := svc.CreateDraft(handoffNow, publisher(), expired)
	if ReasonOf(err) != ReasonOfferExpired || res.Success || res.Draft.ID != "" || len(svc.List()) != 0 {
		t.Fatalf("expired create = %+v %v", res, err)
	}

	created, err := svc.CreateDraft(handoffNow, publisher(), baseReq())
	if err != nil {
		t.Fatal(err)
	}
	atExpiry := baseReq().OfferExpiry
	confirmed, err := svc.Confirm(atExpiry, publisher(), created.Draft.ID)
	if ReasonOf(err) != ReasonOfferExpired || confirmed.Draft.Approved || confirmed.Success {
		t.Fatalf("expired confirm = %+v %v", confirmed, err)
	}
	noted, err := svc.RecordMerchantPublish(atExpiry, publisher(), created.Draft.ID)
	if ReasonOf(err) != ReasonOfferExpired || noted.Draft.MerchantNoted || noted.Draft.CustomerPublish || noted.Success {
		t.Fatalf("expired merchant = %+v %v", noted, err)
	}
	if noted.Draft.ActivityStatus != ActivityOpen {
		t.Fatalf("status = %s", noted.Draft.ActivityStatus)
	}
	if sink.RewardWrites != 0 || sink.LeadWrites != 0 || sink.UGCWrites != 0 || sink.GrantCalls != 0 {
		t.Fatalf("expired offer wrote customer effects: %+v", sink)
	}
}

func TestEffectCountersObserveDirectCalls(t *testing.T) {
	sink := &EffectSink{}
	sink.GrantCustomerReward("act_a")
	sink.WriteLead("act_a")
	sink.WriteCustomerUGC("act_a")
	if sink.RewardWrites != 1 || sink.LeadWrites != 1 || sink.UGCWrites != 1 || sink.GrantCalls != 1 {
		t.Fatalf("counters do not observe writes: %+v", sink)
	}
}

func TestMerchantPublishWritesNoRewardLeadOrUGC(t *testing.T) {
	client := videoFixture()
	sink := &EffectSink{}
	svc := New(client, sink)
	created, err := svc.CreateDraft(handoffNow, publisher(), baseReq())
	if err != nil {
		t.Fatal(err)
	}
	early, err := svc.RecordMerchantPublish(handoffNow, publisher(), created.Draft.ID)
	if ReasonOf(err) != ReasonNotApproved || early.Draft.MerchantNoted || early.Success {
		t.Fatalf("unapproved merchant = %+v %v", early, err)
	}
	if _, err := svc.Confirm(handoffNow, publisher(), created.Draft.ID); err != nil {
		t.Fatal(err)
	}
	client.Err = errors.New("matrix offline")
	failed, err := svc.RecordMerchantPublish(handoffNow, publisher(), created.Draft.ID)
	if err == nil || err.Error() != "matrix offline" || failed.Draft.MerchantNoted || failed.Success || failed.Draft.ActivityStatus != ActivityOpen || !failed.Draft.Approved {
		t.Fatalf("merchant client error = %+v %v", failed, err)
	}
	client.Err = nil
	noted, err := svc.RecordMerchantPublish(handoffNow, publisher(), created.Draft.ID)
	if err != nil || !noted.Draft.MerchantNoted || noted.Success || noted.Draft.CustomerPublish || noted.Draft.Executed {
		t.Fatalf("merchant note = %+v %v", noted, err)
	}
	mustNotClaimPublishSuccess(t, noted.Draft.Copy)
	assertClean(t, noted.Draft)
	if noted.Draft.ActivityStatus != ActivityOpen || !noted.Draft.Approved {
		t.Fatalf("merchant note locked or dropped approval: %+v", noted.Draft)
	}
	if sink.RewardWrites != 0 || sink.LeadWrites != 0 || sink.UGCWrites != 0 || sink.GrantCalls != 0 {
		t.Fatalf("merchant publish wrote customer effects: %+v", sink)
	}
	if svc.RewardWrites() != 0 || svc.LeadWrites() != 0 || svc.UGCWrites() != 0 || svc.GrantCustomerRewardCalls() != 0 {
		t.Fatal("service counters moved")
	}
	if svc.ChargeCount() != 0 || svc.TranscodeCount() != 0 || svc.AICutCount() != 0 {
		t.Fatal("merchant note charged or transcoded")
	}
}

func TestMatrixErrorDoesNotLockActivity(t *testing.T) {
	client := videoFixture()
	client.Err = errors.New("matrix offline")
	sink := &EffectSink{}
	svc := New(client, sink)
	res, err := svc.CreateDraft(handoffNow, publisher(), baseReq())
	if err == nil || err.Error() != "matrix offline" || res.Success {
		t.Fatalf("create = %+v %v", res, err)
	}
	if res.Draft.ID == "" || res.Draft.Plan.ID != "" || res.Draft.ActivityStatus != ActivityOpen || res.Draft.Status != StatusDraft {
		t.Fatalf("draft after client error = %+v", res.Draft)
	}
	mustNotClaimPublishSuccess(t, res.Draft.Copy)
	assertClean(t, res.Draft)
	if strings.Contains(res.Draft.ActivityStatus, "lock") {
		t.Fatal("activity locked")
	}

	client.Err = nil
	retry, err := svc.CreateDraft(handoffNow, publisher(), baseReq())
	if err != nil || retry.Draft.ID != res.Draft.ID || retry.Draft.Plan.ID == "" || retry.Success || retry.Draft.ActivityStatus != ActivityOpen {
		t.Fatalf("retry = %+v %v", retry, err)
	}
	if client.Puts != 2 || len(svc.List()) != 1 {
		t.Fatalf("puts=%d drafts=%d", client.Puts, len(svc.List()))
	}

	late := errors.New("late receipt failed")
	applied, err := svc.ApplyClientError(retry.Draft.ID, late)
	if !errors.Is(err, late) || applied.Success || applied.Draft.ActivityStatus != ActivityOpen || applied.Draft.Plan.ID != retry.Draft.Plan.ID {
		t.Fatalf("late = %+v %v", applied, err)
	}
	if _, err := svc.ApplyClientError("missing", late); ReasonOf(err) != ReasonNotFound {
		t.Fatalf("missing = %v", err)
	}
	if sink.RewardWrites != 0 || sink.LeadWrites != 0 || sink.GrantCalls != 0 {
		t.Fatalf("client error wrote customer effects: %+v", sink)
	}
}

func TestPackageHasNoCustomerSurface(t *testing.T) {
	raw, err := os.ReadFile("matrixhandoff.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, bad := range []string{
		"net/http",
		"internal/httpapi",
		"internal/publishreward",
		"internal/rewardrule",
		"internal/extrajump",
		"EcoTopNav",
		".GrantCustomerReward(",
		".WriteLead(",
		".WriteCustomerUGC(",
	} {
		if strings.Contains(body, bad) {
			t.Fatalf("implementation contains %s", bad)
		}
	}
	for _, rt := range []reflect.Type{reflect.TypeOf(Draft{}), reflect.TypeOf(Request{}), reflect.TypeOf(TouchActivity{})} {
		for i := 0; i < rt.NumField(); i++ {
			name := strings.ToLower(rt.Field(i).Name)
			if strings.Contains(name, "ecotopnav") || strings.Contains(name, "nfc") {
				t.Fatalf("%s has %s", rt.Name(), rt.Field(i).Name)
			}
		}
	}
}
