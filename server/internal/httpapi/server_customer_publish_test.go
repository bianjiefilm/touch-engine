package httpapi

import (
	"fmt"
	"net/http"
	"testing"
)

func TestCustomerPublishMatrixPreviewExportAndCounterexamples(t *testing.T) {
	f := newFixture(t, false)
	code := publishLink(t, f)

	status, _, matrix := f.do(t, "GET", "/api/v1/public/links/"+code+"/publish-capabilities", "", "", "")
	if status != http.StatusOK {
		t.Fatalf("matrix = %d %v", status, matrix)
	}
	if matrix["publisher"] != "activity_customer" || matrix["adapters_do_not_enable"] != true {
		t.Fatalf("matrix header = %v", matrix)
	}
	platforms, _ := matrix["platforms"].([]any)
	if len(platforms) != 4 {
		t.Fatalf("platforms = %d", len(platforms))
	}
	for _, raw := range platforms {
		row := raw.(map[string]any)
		caps := row["capabilities"].(map[string]any)
		if !capEnabled(caps, "preview") || !capEnabled(caps, "export") {
			t.Fatalf("%v preview/export closed", row["platform"])
		}
		for _, kind := range []string{"open_editor", "authorized_publish", "confirm_publish"} {
			cell := caps[kind].(map[string]any)
			if cell["enabled"] != false || cell["reason"] == "" || cell["evidence_url"] == "" {
				t.Fatalf("%v %s = %v", row["platform"], kind, cell)
			}
		}
		guide, _ := row["manual_guide"].(string)
		if guide == "" {
			t.Fatalf("%v missing manual guide", row["platform"])
		}
	}

	status, _, created := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts", "", "",
		`{"platform":"douyin","publisher":"activity_customer","copy":"到店打卡","account_label":"顾客自己的抖音"}`)
	if status != http.StatusCreated || created["status"] != "previewed" || created["publish_success"] != false || created["platform_post_id"] != "" {
		t.Fatalf("preview = %d %v", status, created)
	}
	id := created["attempt_id"].(string)
	if created["counts_as_published"] != false || created["reward_triggered"] != false {
		t.Fatalf("preview counted: %v", created)
	}

	status, _, exported := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/export", "", "", `{}`)
	if status != http.StatusOK || exported["status"] != "exported" || exported["publish_success"] != false {
		t.Fatalf("export = %d %v", status, exported)
	}
	pkg := exported["package"].(map[string]any)
	if pkg["post_id"] != "" {
		t.Fatalf("export invented a post id: %v", pkg)
	}
	steps, _ := pkg["steps"].([]any)
	if len(steps) == 0 {
		t.Fatal("export missing manual steps")
	}

	version := exported["content_version"].(string)
	status, _, confirmed := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/confirm", "", "",
		fmt.Sprintf(`{"content_version":%q,"account_label":"顾客自己的抖音","publisher":"activity_customer","asset_use_accepted":true}`, version))
	if status != http.StatusOK || confirmed["status"] == "publish_confirmed" || confirmed["publish_success"] != false || confirmed["confirmation_current"] != true {
		t.Fatalf("confirm = %d %v", status, confirmed)
	}

	status, _, reported := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/self-report", "", "",
		`{"post_id":"dy_fake"}`)
	if status != http.StatusOK || reported["publish_success"] != false || reported["platform_post_id"] != "" || reported["counts_as_published"] != false || reported["reward_triggered"] != false {
		t.Fatalf("self-report counted as success: %d %v", status, reported)
	}
	if reported["self_reported"] != true {
		t.Fatalf("self-report dropped: %v", reported)
	}

	status, _, denied := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/publish", "", "", `{}`)
	if status != http.StatusConflict || denied["error"] != "capability_closed" || denied["publish_success"] == true {
		t.Fatalf("publish = %d %v", status, denied)
	}
	status, _, editor := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/open-editor", "", "", `{}`)
	if status != http.StatusConflict || editor["error"] != "capability_closed" {
		t.Fatalf("editor = %d %v", status, editor)
	}

	engagement := reported["engagement"].(map[string]any)
	for _, key := range []string{"completion", "likes", "plays", "poi_exposure"} {
		cell := engagement[key].(map[string]any)
		if cell["available"] != false || cell["value"] != nil {
			t.Fatalf("%s engagement = %v", key, cell)
		}
	}
}

func TestCustomerPublishChangeRequiresReconfirm(t *testing.T) {
	f := newFixture(t, false)
	code := publishLink(t, f)
	_, _, created := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts", "", "",
		`{"platform":"kuaishou","publisher":"activity_customer","copy":"原文案","account_label":"顾客快手"}`)
	id := created["attempt_id"].(string)
	version := created["content_version"].(string)
	f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts/"+id+"/confirm", "", "",
		fmt.Sprintf(`{"content_version":%q,"account_label":"顾客快手","publisher":"activity_customer","asset_use_accepted":true}`, version))
	status, _, changed := f.do(t, "PATCH", "/api/v1/public/links/"+code+"/publish-attempts/"+id, "", "",
		`{"copy":"换稿之后"}`)
	if status != http.StatusOK || changed["confirmation_current"] != false || changed["status"] != "previewed" || changed["publish_success"] != false {
		t.Fatalf("copy change = %d %v", status, changed)
	}
	status, _, swapped := f.do(t, "PATCH", "/api/v1/public/links/"+code+"/publish-attempts/"+id, "", "",
		`{"account_label":"另一个顾客账号"}`)
	if status != http.StatusOK || swapped["confirmation_current"] != false {
		t.Fatalf("account change = %d %v", status, swapped)
	}
}

func TestCustomerPublishRejectsMerchantAndUnknownLink(t *testing.T) {
	f := newFixture(t, false)
	code := publishLink(t, f)
	status, _, body := f.do(t, "POST", "/api/v1/public/links/"+code+"/publish-attempts", "", "",
		`{"platform":"channels","publisher":"merchant","copy":"文案","account_label":"商家视频号"}`)
	if status != http.StatusUnprocessableEntity || body["error"] != "publisher_must_be_activity_customer" {
		t.Fatalf("merchant = %d %v", status, body)
	}
	status, _, missing := f.do(t, "GET", "/api/v1/public/links/ZZZZZZZZZZZZ/publish-capabilities", "", "", "")
	if status != http.StatusNotFound {
		t.Fatalf("missing link = %d %v", status, missing)
	}
}

func TestRegisteredAdapterStaysUnavailable(t *testing.T) {
	f := newFixture(t, false)
	code := publishLink(t, f)
	status, _, noted := f.do(t, "POST", "/api/v1/publish-adapters", "sess-owner-a", f.tenA,
		`{"platform":"douyin","name":"douyin-openapi"}`)
	if status != http.StatusCreated || noted["enables_capabilities"] != false {
		t.Fatalf("note adapter = %d %v", status, noted)
	}
	_, _, matrix := f.do(t, "GET", "/api/v1/public/links/"+code+"/publish-capabilities", "", "", "")
	names, _ := matrix["registered_adapters"].([]any)
	if len(names) != 1 {
		t.Fatalf("registered = %v", matrix["registered_adapters"])
	}
	for _, raw := range matrix["platforms"].([]any) {
		row := raw.(map[string]any)
		if capEnabled(row["capabilities"].(map[string]any), "authorized_publish") {
			t.Fatalf("adapter enabled %v", row["platform"])
		}
	}
}

func capEnabled(caps map[string]any, kind string) bool {
	cell, _ := caps[kind].(map[string]any)
	return cell["enabled"] == true
}

func publishLink(t *testing.T, f *fixture) string {
	t.Helper()
	status, _, cmp := f.do(t, "POST", "/api/v1/campaigns", "sess-owner-a", f.tenA,
		`{"title":"周末","public_content":"到店打卡","starts_at":"2026-01-01T00:00:00Z","ends_at":"2030-01-01T00:00:00Z"}`)
	if status != http.StatusCreated {
		t.Fatalf("campaign = %d %v", status, cmp)
	}
	id := cmp["id"].(string)
	status, _, body := f.do(t, "POST", "/api/v1/campaigns/"+id+"/status", "sess-owner-a", f.tenA, `{"status":"active"}`)
	if status != http.StatusOK {
		t.Fatalf("activate = %d %v", status, body)
	}
	status, _, link := f.do(t, "POST", "/api/v1/campaigns/"+id+"/links", "sess-owner-a", f.tenA, `{}`)
	if status != http.StatusCreated {
		t.Fatalf("link = %d %v", status, link)
	}
	return link["code"].(string)
}
