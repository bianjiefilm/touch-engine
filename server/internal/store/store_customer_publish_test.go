package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
	"github.com/bianjiefilm/touch-engine/server/internal/db"
)

func TestCustomerPublishStoresPreviewNotSuccess(t *testing.T) {
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
	if saved.Status != custpublish.StatusPreviewed || saved.PlatformPostID != "" || saved.OutboundCalls != 0 || saved.RewardTriggered {
		t.Fatalf("stored preview looked like a publish: %+v", saved)
	}

	exported, err := s.MarkCustomerPublishExported(saved.ID, tenant)
	if err != nil || exported.Status != custpublish.StatusExported || exported.PlatformPostID != "" {
		t.Fatalf("export = %v %+v", err, exported)
	}
	reported, err := s.MarkCustomerPublishSelfReport(saved.ID, tenant, "dy_fake")
	if err != nil || reported.PlatformPostID != "" || !reported.SelfReported || reported.Status == custpublish.StatusPublishConfirmed {
		t.Fatalf("self-report stored a receipt: %v %+v", err, reported)
	}

	n, err := s.CountOfficialCustomerPublishes(tenant, "")
	if err != nil || n != 0 {
		t.Fatalf("unverified rows counted as published: %d %v", n, err)
	}
}

func TestCustomerPublishSchemaRejectsFabricatedSuccess(t *testing.T) {
	s := openCustomerPublishStore(t)
	tenant, campaign, code := seedPublishCampaign(t, s)
	_, err := s.DB.Exec(`INSERT INTO customer_publish_attempts(
		id,tenant_id,campaign_id,link_code,platform,publisher_subject,copy_text,content_version,account_label,status,platform_post_id,receipt_source,outbound_calls,reward_triggered,created_at,updated_at
	) VALUES('cpa_fake',?,?,?,?,?,?,?,?,?,?,?,0,0,'2026-09-28T00:00:00Z','2026-09-28T00:00:00Z')`,
		tenant, campaign, code, "douyin", "activity_customer", "文案", "v", "顾客", "publish_confirmed", "dy_fake", "official_query")
	if err == nil {
		t.Fatal("database accepted a fabricated publish_confirmed receipt")
	}
}

func TestPublishAdapterNoteDoesNotEnable(t *testing.T) {
	s := openCustomerPublishStore(t)
	tenant, _, _ := seedPublishCampaign(t, s)
	if err := s.NotePublishAdapter(tenant, "douyin", "douyin-openapi"); err != nil {
		t.Fatal(err)
	}
	notes, err := s.ListPublishAdapterNotes(tenant)
	if err != nil || len(notes) != 1 || notes[0].Name != "douyin-openapi" {
		t.Fatalf("notes = %v %v", notes, err)
	}
	for _, row := range custpublish.Matrix(notes) {
		if row.Capability(custpublish.CapAuthorizedPublish).Enabled {
			t.Fatalf("stored adapter enabled %s", row.Platform)
		}
	}
}

func TestCustomerPublishTenantIsolation(t *testing.T) {
	s := openCustomerPublishStore(t)
	tenantA, campaignA, codeA := seedPublishCampaign(t, s)
	tenantB, _, _ := seedPublishCampaign(t, s)
	preview, err := custpublish.Preview(custpublish.PreviewInput{
		Platform: custpublish.PlatformKuaishou, Publisher: custpublish.PublisherActivityCustomer,
		Copy: "文案", AccountLabel: "顾客快手",
	})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.CreateCustomerPublishPreview(tenantA, campaignA, codeA, preview)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCustomerPublish(saved.ID, tenantB); err != ErrNotFound {
		t.Fatalf("cross-tenant get = %v", err)
	}
	if _, err := s.MarkCustomerPublishExported(saved.ID, tenantB); err != ErrNotFound {
		t.Fatalf("cross-tenant export = %v", err)
	}
	if strings.TrimSpace(saved.ID) == "" {
		t.Fatal("missing id")
	}
}

func openCustomerPublishStore(t *testing.T) *Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "touch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return New(d)
}

func seedPublishCampaign(t *testing.T, s *Store) (tenantID, campaignID, code string) {
	t.Helper()
	ten, err := s.CreateTenant("商家")
	if err != nil {
		t.Fatal(err)
	}
	mem, err := s.CreateMember(ten.ID, "usr_owner", "org_owner", "老板", "seed", true)
	if err != nil {
		t.Fatal(err)
	}
	camp, err := s.CreateCampaign(NewCampaign{TenantID: ten.ID, Title: "周末", PublicContent: "到店", CreatedBy: mem.PrincipalRef})
	if err != nil {
		t.Fatal(err)
	}
	link, err := s.CreateLink(ten.ID, camp.ID, mem.PrincipalRef)
	if err != nil {
		t.Fatal(err)
	}
	return ten.ID, camp.ID, link.Code
}
