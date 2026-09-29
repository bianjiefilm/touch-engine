package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/workbench"
)

var openLeadID = regexp.MustCompile(`^lead_[0-9a-f]{32}$`)

// followUpSummaries reads the restricted leads summary. A transport or
// contract failure returns ok=false so the workbench keeps the count unknown
// instead of writing zero.
func (s *Server) followUpSummaries(ctx context.Context, tenantID string, campaignIDs []string) (map[string]workbench.FollowUpFact, bool) {
	base := strings.TrimSpace(s.Cfg.LeadsFollowUpBaseURL)
	token := strings.TrimSpace(s.Cfg.LeadsFollowUpToken)
	if base == "" || token == "" || tenantID == "" || len(campaignIDs) == 0 {
		return nil, false
	}
	u, err := url.Parse(base + "/internal/v1/campaign-follow-ups")
	if err != nil {
		return nil, false
	}
	q := u.Query()
	asked := map[string]bool{}
	for _, id := range campaignIDs {
		id = strings.TrimSpace(id)
		if id == "" || asked[id] {
			continue
		}
		asked[id] = true
		q.Add("campaign_ref", id)
	}
	if len(asked) == 0 {
		return nil, false
	}
	if app := strings.TrimSpace(s.Cfg.AppID); app != "" {
		q.Set("source_app", app)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("X-Internal-Token", token)
	req.Header.Set("X-Tenant-ID", tenantID)
	client := &http.Client{
		Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are disabled")
		},
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil || res.StatusCode != http.StatusOK {
		return nil, false
	}
	var parsed struct {
		Known     bool `json:"known"`
		Campaigns []struct {
			CampaignRef string   `json:"campaign_ref"`
			Pending     int      `json:"pending_follow_up"`
			LeadIDs     []string `json:"lead_ids"`
		} `json:"campaigns"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil || !parsed.Known {
		return nil, false
	}
	out := map[string]workbench.FollowUpFact{}
	for _, row := range parsed.Campaigns {
		if !asked[row.CampaignRef] || row.Pending < 0 {
			continue
		}
		ids := make([]string, 0, len(row.LeadIDs))
		for _, id := range row.LeadIDs {
			if openLeadID.MatchString(id) {
				ids = append(ids, id)
			}
		}
		out[row.CampaignRef] = workbench.FollowUpFact{Known: true, Count: row.Pending, LeadIDs: ids}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
