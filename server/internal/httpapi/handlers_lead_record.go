package httpapi

import (
	"net/http"
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// GET /internal/v1/lead-records/{submissionRef}
//
// Restricted read for a registered leads ingest source. The contact leaves
// this process only on this authorized fetch, never on the notify event.
// Callers must present the BFF internal token and the tenant they are
// ingesting for. A mismatch is a uniform 404.
func (s *Server) handleLeadRecord(w http.ResponseWriter, r *http.Request) {
	if !s.leadsGate(w) {
		return
	}
	ref := strings.TrimSpace(r.PathValue("submissionRef"))
	tenantID := strings.TrimSpace(r.Header.Get("X-Leads-Tenant"))
	if ref == "" || tenantID == "" {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	lead, err := s.St.GetLeadSubmissionByRef(ref)
	if err != nil || lead.TenantID != tenantID {
		fail(w, http.StatusNotFound, "not_found", "record not found")
		return
	}
	writeJSON(w, http.StatusOK, leadRecordView(lead))
}

func leadRecordView(lead store.LeadSubmission) map[string]any {
	revoked := lead.RevokedAt != "" || lead.SyncState == "revoked"
	authorization := "granted"
	if revoked {
		authorization = "revoked"
	}
	marketing := lead.MarketingOptin && !revoked
	return map[string]any{
		"submission_ref":      lead.SubmissionRef,
		"tenant_id":           lead.TenantID,
		"name":                lead.Name,
		"phone":               lead.Phone,
		"email":               "",
		"wechat":              lead.Wechat,
		"channel_subject_ref": "",
		"authorization":       authorization,
		"purpose":             "lead_submission",
		"consents": map[string]any{
			"form_authorized": !revoked,
			"channel_reply":   false,
			"marketing_phone": marketing,
			"marketing_sms":   marketing,
			"notice_version":  lead.ConsentNoticeVersion,
		},
	}
}
