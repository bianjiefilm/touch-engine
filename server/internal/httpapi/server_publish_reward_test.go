package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

func TestPublishRewardStaysPendingOnPreviewAndExport(t *testing.T) {
	f := newFixture(t, false)
	_, code := rewardCampaign(t, f)

	status, _, reward := f.do(t, "GET", "/api/v1/public/links/"+code+"/publish-reward", "", "", "")
	if status != http.StatusOK || reward["grant_enabled"] != false || reward["issuance_enabled"] != false || reward["status"] != "pending_verification" {
		t.Fatalf("reward = %d %v", status, reward)
	}
	if reward["coupons_issued"].(float64) != 0 || reward["fees_charged"].(float64) != 0 || reward["reason"] != "channel_cannot_prove_publish" {
		t.Fatalf("reward issued or proved: %v", reward)
	}
	platforms, _ := reward["platforms"].([]any)
	if len(platforms) != 4 {
		t.Fatalf("platforms = %d", len(platforms))
	}
	for _, raw := range platforms {
		row := raw.(map[string]any)
		if row["grant_enabled"] != false || row["reward_status"] != "pending_verification" {
			t.Fatalf("%v reward opened", row["platform"])
		}
		url, _ := row["evidence_url"].(string)
		if len(url) < 8 || url[:8] != "https://" {
			t.Fatalf("%v evidence = %q", row["platform"], url)
		}
	}

	status, _, created := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts", "", "",
		`{"platform":"douyin","publisher":"activity_customer","copy":"到店打卡","account_label":"顾客自己的抖音"}`)
	if status != http.StatusCreated {
		t.Fatalf("preview = %d %v", status, created)
	}
	previewID := created["attempt_id"].(string)
	claim := func(id, body string) (int, map[string]any) {
		t.Helper()
		st, _, out := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/reward-claim", "", "", body)
		return st, out
	}
	status, first := claim(previewID, `{"kind":"official_confirmed_publish","post_id":"dy_forged","publisher":"merchant"}`)
	if status != http.StatusOK || first["grant"] != false || first["eligible"] != false || first["publish_success"] != false || first["platform_post_id"] != "" || first["applied"] != true {
		t.Fatalf("preview claim = %d %v", status, first)
	}
	if first["coupons_issued"].(float64) != 0 || first["fees_charged"].(float64) != 0 || first["outbound_calls"].(float64) != 0 {
		t.Fatalf("preview claim moved money: %v", first)
	}
	if first["reason"] != "preview_is_not_publish" || first["account_class"] != "marketing_reward" {
		t.Fatalf("preview reason = %v", first)
	}
	status, replay := claim(previewID, `{"post_id":"dy_again"}`)
	if status != http.StatusOK || replay["applied"] != false || replay["grant"] != false || replay["platform_post_id"] != "" {
		t.Fatalf("replay = %d %v", status, replay)
	}

	_, _, exportedAttempt := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts", "", "",
		`{"platform":"douyin","publisher":"activity_customer","copy":"导出文案","account_label":"顾客自己的抖音"}`)
	exportID := exportedAttempt["attempt_id"].(string)
	f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+exportID+"/export", "", "", `{}`)
	status, exported := claim(exportID, `{}`)
	if status != http.StatusOK || exported["reason"] != "export_is_not_publish" || exported["grant"] != false || exported["platform_post_id"] != "" {
		t.Fatalf("export claim = %d %v", status, exported)
	}

	_, _, reportedAttempt := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts", "", "",
		`{"platform":"douyin","publisher":"activity_customer","copy":"自报文案","account_label":"顾客自己的抖音"}`)
	reportID := reportedAttempt["attempt_id"].(string)
	f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+reportID+"/self-report", "", "", `{"post_id":"dy_user"}`)
	status, reported := claim(reportID, `{"post_id":"dy_user"}`)
	if status != http.StatusOK || reported["reason"] != "self_report_is_not_publish" || reported["grant"] != false || reported["publish_success"] != false {
		t.Fatalf("self-report claim = %d %v", status, reported)
	}
}

func TestManualProofDoesNotConfirmPublish(t *testing.T) {
	f := newFixture(t, false)
	campaign, code := rewardCampaign(t, f)
	_, _, created := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts", "", "",
		`{"platform":"kuaishou","publisher":"activity_customer","copy":"新品","account_label":"顾客快手"}`)
	id := created["attempt_id"].(string)

	status, _, proof := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/reward-proof", "", "",
		`{"note":"聊天截图","post_id":"ks_fake"}`)
	if status != http.StatusCreated || proof["status"] != "pending_review" || proof["platform_confirmed"] != false || proof["grant"] != false || proof["post_id"] != "" {
		t.Fatalf("proof = %d %v", status, proof)
	}
	proofID := proof["proof_id"].(string)

	status, _, denied := f.do(t, "POST", "/api/v1/campaigns/"+campaign+"/publish-reward/proofs/"+proofID+"/review", "sess-staff-a", f.tenA,
		`{"approve":true,"basis":"我觉得发了"}`)
	if status != http.StatusForbidden || denied["platform_confirmed"] == true {
		t.Fatalf("staff review = %d %v", status, denied)
	}
	status, _, basis := f.do(t, "POST", "/api/v1/campaigns/"+campaign+"/publish-reward/proofs/"+proofID+"/review", "sess-owner-a", f.tenA,
		`{"approve":true,"basis":" "}`)
	if status != http.StatusUnprocessableEntity || basis["error"] != "review_basis_required" {
		t.Fatalf("empty basis = %d %v", status, basis)
	}
	status, _, reviewed := f.do(t, "POST", "/api/v1/campaigns/"+campaign+"/publish-reward/proofs/"+proofID+"/review", "sess-owner-a", f.tenA,
		`{"approve":true,"basis":"只看到截图，没有平台回执"}`)
	if status != http.StatusOK || reviewed["platform_confirmed"] != false || reviewed["grant"] != false || reviewed["status"] != "reviewed_not_confirmed" || reviewed["post_id"] != "" {
		t.Fatalf("owner review = %d %v", status, reviewed)
	}
	if reviewed["audit_decision"] != "not_platform_confirmed" {
		t.Fatalf("audit = %v", reviewed)
	}
}

func TestParticipationRuleNeedsNewVersionAndNotice(t *testing.T) {
	f := newFixture(t, false)
	campaign, code := rewardCampaign(t, f)

	status, _, saved := f.do(t, "PUT", "/api/v1/campaigns/"+campaign+"/publish-reward", "sess-owner-a", f.tenA,
		`{"trigger":"official_confirmed_publish","request_issuance":true}`)
	if status != http.StatusOK || saved["version"].(float64) != 1 || saved["issuance_enabled"] != false || saved["grant_enabled"] != false || saved["status"] != "pending_verification" {
		t.Fatalf("configure = %d %v", status, saved)
	}
	status, _, silent := f.do(t, "PUT", "/api/v1/campaigns/"+campaign+"/publish-reward", "sess-owner-a", f.tenA,
		`{"trigger":"activity_participation","user_notice":""}`)
	if status != http.StatusUnprocessableEntity || silent["error"] != "user_notice_required" {
		t.Fatalf("silent retarget = %d %v", status, silent)
	}
	status, _, next := f.do(t, "PUT", "/api/v1/campaigns/"+campaign+"/publish-reward", "sess-owner-a", f.tenA,
		`{"trigger":"activity_participation","user_notice":"这次改成参与奖励，以前的预览和导出不算"}`)
	if status != http.StatusOK || next["version"].(float64) != 2 || next["trigger"] != "activity_participation" || next["issuance_enabled"] != false {
		t.Fatalf("next = %d %v", status, next)
	}

	_, _, created := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts", "", "",
		`{"platform":"xiaohongshu","publisher":"activity_customer","copy":"原文案","account_label":"顾客小红书"}`)
	id := created["attempt_id"].(string)
	status, _, claimed := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/reward-claim", "", "",
		`{"kind":"verified_participation"}`)
	if status != http.StatusOK || claimed["grant"] != false || claimed["reason"] != "participation_evidence_missing" || claimed["coupons_issued"].(float64) != 0 {
		t.Fatalf("renamed preview = %d %v", status, claimed)
	}
}

func TestRewardLedgerRejectsServiceFunds(t *testing.T) {
	f := newFixture(t, false)
	_, code := rewardCampaign(t, f)
	_, _, created := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts", "", "",
		`{"platform":"channels","publisher":"activity_customer","copy":"视频号文案","account_label":"顾客视频号"}`)
	id := created["attempt_id"].(string)
	for _, class := range []string{"order_service", "tool_balance", "unsettled"} {
		status, _, body := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/reward-claim", "", "",
			fmt.Sprintf(`{"account_class":%q,"deduct_from":%q}`, class, class))
		if status != http.StatusUnprocessableEntity || body["grant"] == true || body["coupons_issued"] == float64(1) {
			t.Fatalf("%s claim = %d %v", class, status, body)
		}
		if body["error"] != "account_class_forbidden" && body["error"] != "must_not_deduct_unsettled" {
			t.Fatalf("%s error = %v", class, body)
		}
	}
}

func rewardCampaign(t *testing.T, f *fixture) (campaignID, code string) {
	t.Helper()
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"发布奖励","public_content":"到店打卡","starts_at":"2026-01-01T00:00:00Z","ends_at":"2030-01-01T00:00:00Z"}`)
	if status != http.StatusCreated {
		t.Fatalf("campaign = %d %v", status, cmp)
	}
	campaignID = cmp["id"].(string)
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	if status != http.StatusOK {
		t.Fatalf("activate = %d %v", status, body)
	}
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+campaignID+"/links", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusCreated {
		t.Fatalf("link = %d %v", status, link)
	}
	return campaignID, link["code"].(string)
}
