package httpapi

import (
	"github.com/bianjiefilm/touch-engine/server/internal/leads"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

// campaignLeadTrace is the AG06-readable source on one submission.
// Asset and version come from the campaign's own asset rows, never from the visitor body.
func (s *Server) campaignLeadTrace(campaign store.Campaign, linkCode, submissionRef string) leads.SourceTrace {
	trace := leads.SourceTrace{
		TraceID:      "lead:" + submissionRef,
		ReturnTarget: "/c/" + linkCode,
	}
	if campaign.OrderRef != "" {
		trace.GrantRef = campaign.OrderRef
	}
	assets, err := s.St.ListCampaignAssets(campaign.TenantID, campaign.ID)
	if err != nil || len(assets) == 0 {
		return trace
	}
	last := assets[len(assets)-1]
	trace.CampaignVersion = last.Version
	trace.AssetRef = last.AssetID
	return trace
}
