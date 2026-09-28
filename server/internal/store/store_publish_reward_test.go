package store

import (
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
	"github.com/bianjiefilm/touch-engine/server/internal/publishreward"
)

func TestRewardLedgerSeparatesOpsAndRejectsIssuance(t *testing.T) {
	s := openCustomerPublishStore(t)
	tenant, campaign, code := seedPublishCampaign(t, s)
	preview, err := custpublish.Preview(custpublish.PreviewInput{
		Platform: custpublish.PlatformDouyin, Publisher: custpublish.PublisherActivityCustomer,
		Copy: "到店打卡", AccountLabel: "顾客抖音",
	})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.CreateCustomerPublishPreview(tenant, campaign, code, preview)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := s.FreezeRewardSubject(tenant, campaign, saved.ID, "activity_customer:"+saved.AccountLabel)
	if err != nil || !strings.HasPrefix(subject, "activity_customer:") {
		t.Fatalf("subject = %q %v", subject, err)
	}
	base := publishreward.Entry{
		CampaignID: campaign, SubjectID: subject, RuleVersion: 1, FactID: saved.ID,
		AccountClass: publishreward.AccountMarketing,
	}
	decision := publishreward.Decision{Reason: publishreward.ReasonPreviewNotPublish}
	for _, op := range []string{publishreward.OpClaim, publishreward.OpRedeem, publishreward.OpRevoke} {
		entry := base
		entry.Op = op
		posted, reason, err := s.RecordRewardPosting(tenant, entry, decision)
		if err != nil || !posted.Applied || reason != publishreward.ReasonPreviewNotPublish || posted.CouponsIssued != 0 || posted.FeesCharged != 0 {
			t.Fatalf("%s = %v %q %+v", op, err, reason, posted)
		}
		again, _, err := s.RecordRewardPosting(tenant, entry, decision)
		if err != nil || again.Applied {
			t.Fatalf("%s replay = %v %+v", op, err, again)
		}
		n, err := s.CountRewardLedger(entry, op)
		if err != nil || n != 1 {
			t.Fatalf("%s count = %d %v", op, n, err)
		}
	}
	changed, err := s.FreezeRewardSubject(tenant, campaign, saved.ID, "activity_customer:另一个账号")
	if err != nil || changed != subject {
		t.Fatalf("subject changed = %q want %q (%v)", changed, subject, err)
	}
	_, err = s.DB.Exec(`INSERT INTO publish_reward_ledger(
		id,tenant_id,campaign_id,subject_id,rule_version,fact_id,op,account_class,outcome,reason,coupons_issued,created_at
	) VALUES('prl_fake',?,?,?,1,?,'claim','marketing_reward','recorded','x',1,'2026-09-28T00:00:00Z')`,
		tenant, campaign, subject, saved.ID)
	if err == nil || !strings.Contains(err.Error(), "CHECK") {
		t.Fatalf("coupon insert err = %v", err)
	}
	_, err = s.DB.Exec(`INSERT INTO publish_reward_proofs(
		id,tenant_id,campaign_id,fact_id,status,platform_confirmed,post_id,created_at
	) VALUES('prp_fake',?,?,?,'pending_review',1,'dy_fake','2026-09-28T00:00:00Z')`,
		tenant, campaign, saved.ID)
	if err == nil {
		t.Fatal("proof row accepted a platform confirmation")
	}
}
