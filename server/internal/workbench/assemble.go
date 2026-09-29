package workbench

import (
	"strings"

	"github.com/bianjiefilm/touch-engine/server/internal/activityspend"
)

// Assemble builds the merchant workbench from touch-domain facts and explicit
// capability inputs. It does not read another app's database, invent a zero
// for an unknown metric, or treat the caller's own membership as a delegation.
func Assemble(f Facts) View {
	byID := map[string]CampaignFact{}
	for _, campaign := range f.Campaigns {
		byID[campaign.ID] = campaign
	}

	view := View{
		WorkingFor: workingFor(f.Seat),
		Seat:       f.Seat,
		MyTasks:    []Task{},
		Content:    contentSection(f),
		Activities: activitySection(f.Campaigns),
		Customers:  customerSection(f, byID),
		Billing: BillingView{
			Known:           false,
			Availability:    "unknown",
			Reason:          "billing_account_not_in_touch",
			PerToolBalances: []string{},
			CouponsMerged:   false,
		},
		AccountSeparation: accountSeparation(f),
	}
	view.MyTasks = append(view.MyTasks, campaignTasks(f.Campaigns)...)
	if f.LeadsCapture {
		view.MyTasks = append(view.MyTasks, leadTasks(f.Leads, byID)...)
	}
	if f.ContentTasksKnown {
		view.MyTasks = append(view.MyTasks, contentTasks(f.ContentTasks, byID)...)
	}
	return view
}

func accountSeparation(f Facts) activityspend.SeparationView {
	book := activityspend.Ledger{}
	for _, campaign := range f.Campaigns {
		book.CampaignIDs = append(book.CampaignIDs, campaign.ID)
	}
	for _, benefit := range f.Benefits {
		book.Benefits = append(book.Benefits, activityspend.BenefitFact{
			ID:         benefit.ID,
			CampaignID: benefit.CampaignID,
			Ledger:     activityspend.LedgerActivity,
			Kind:       benefit.Kind,
			FaceMinor:  benefit.FaceMinor,
		})
	}
	return activityspend.Separation(book, activityspend.Subscription{Status: f.SubscriptionStatus}, activityspend.ChargeDecision{})
}

func workingFor(seat Seat) string {
	if seat.Source == "membership" || seat.Source == "" || strings.TrimSpace(seat.RelationID) == "" {
		return ""
	}
	switch seat.Source {
	case "agency":
		if seat.Role != "agent" {
			return ""
		}
	case "delegation":
		if seat.DelegationType != "ops_collab" {
			return ""
		}
	default:
		return ""
	}
	name := strings.TrimSpace(seat.MerchantName)
	if name == "" {
		return "正在为该商家工作"
	}
	return "正在为 " + name + " 商家工作"
}

func campaignTasks(campaigns []CampaignFact) []Task {
	out := []Task{}
	for _, campaign := range campaigns {
		if campaign.Status != "draft" {
			continue
		}
		out = append(out, Task{
			Kind: "unpublished_campaign", CampaignID: campaign.ID, Title: "待发布活动",
			ObjectKind: "campaign", ObjectID: campaign.ID, State: "draft",
		})
	}
	return out
}

func leadTasks(leads []LeadCount, campaigns map[string]CampaignFact) []Task {
	out := []Task{}
	pending := map[string]int{}
	failed := map[string]int{}
	unknown := map[string]int{}
	for _, row := range leads {
		if _, ok := campaigns[row.CampaignID]; !ok || row.Count <= 0 {
			continue
		}
		switch row.SyncState {
		case "accepted", "pending_sync":
			pending[row.CampaignID] += row.Count
		case "rejected":
			failed[row.CampaignID] += row.Count
		case "revoked", "crm_received":
		default:
			unknown[row.CampaignID] += row.Count
		}
	}
	for _, campaign := range orderedCampaigns(campaigns) {
		if pending[campaign.ID] > 0 {
			out = append(out, Task{
				Kind: "pending_lead", CampaignID: campaign.ID, Title: "有待同步的授权线索",
				ObjectKind: "campaign", ObjectID: campaign.ID, State: "pending_sync",
			})
		}
		if failed[campaign.ID] > 0 {
			out = append(out, Task{
				Kind: "failed_lead", CampaignID: campaign.ID, Title: "线索同步失败，需要恢复",
				ObjectKind: "campaign", ObjectID: campaign.ID, State: "rejected",
			})
		}
		if unknown[campaign.ID] > 0 {
			out = append(out, Task{
				Kind: "lead_sync_unknown", CampaignID: campaign.ID, Title: "线索同步状态未知",
				ObjectKind: "campaign", ObjectID: campaign.ID, State: "unknown",
			})
		}
	}
	return out
}

func contentTasks(tasks []ContentTask, campaigns map[string]CampaignFact) []Task {
	out := []Task{}
	for _, task := range tasks {
		if _, ok := campaigns[task.CampaignID]; !ok {
			continue
		}
		switch task.State {
		case "failed":
			out = append(out, Task{
				Kind: "content_failed", CampaignID: task.CampaignID, Title: "内容任务失败，需要恢复",
				ObjectKind: "campaign", ObjectID: task.CampaignID, State: "failed",
			})
		case "in_progress":
			out = append(out, Task{
				Kind: "content_in_progress", CampaignID: task.CampaignID, Title: "内容任务进行中",
				ObjectKind: "campaign", ObjectID: task.CampaignID, State: "in_progress",
			})
		case "unknown", "":
			out = append(out, Task{
				Kind: "content_unknown", CampaignID: task.CampaignID, Title: "内容任务状态未知",
				ObjectKind: "campaign", ObjectID: task.CampaignID, State: "unknown",
			})
		}
	}
	return out
}

func orderedCampaigns(campaigns map[string]CampaignFact) []CampaignFact {
	out := make([]CampaignFact, 0, len(campaigns))
	for _, campaign := range campaigns {
		out = append(out, campaign)
	}
	// Map iteration is random. Lead tasks must be stable for a given input,
	// so order by id.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func activitySection(campaigns []CampaignFact) ActivitySection {
	section := ActivitySection{Draft: []Activity{}, InProgress: []Activity{}, Ended: []Activity{}, Unclassified: []Activity{}}
	if len(campaigns) == 0 {
		section.NextStep = "创建第一个活动"
		return section
	}
	for _, campaign := range campaigns {
		item := Activity{
			ID: campaign.ID, Title: campaign.Title, Status: campaign.Status,
			ObjectKind: "campaign", ObjectID: campaign.ID, CopyInline: true,
		}
		switch campaign.Status {
		case "draft":
			section.Draft = append(section.Draft, item)
		case "active", "paused":
			section.InProgress = append(section.InProgress, item)
		case "ended":
			section.Ended = append(section.Ended, item)
		default:
			section.Unclassified = append(section.Unclassified, item)
		}
	}
	return section
}

func contentSection(f Facts) ContentSection {
	section := ContentSection{
		Selections:     append([]AssetFact{}, f.Assets...),
		Actions:        []ContentAction{},
		Withheld:       []Withheld{},
		LocksExisting:  false,
		TasksKnown:     f.ContentTasksKnown,
		MaterialsKnown: f.LibraryEnabled,
	}
	hasSelection := len(f.Assets) > 0
	hasLibrary := len(f.Library) > 0
	switch {
	case hasLibrary || hasSelection:
		section.Gap = "has_material"
		if hasLibrary || f.LibraryEnabled {
			section.Materials = append([]LibraryItem{}, f.Library...)
			section.MaterialsKnown = true
		}
	case f.LibraryEnabled:
		section.Gap = "needs_material"
		section.NextStep = "登记或选择已有素材"
		section.Materials = []LibraryItem{}
		section.MaterialsKnown = true
	default:
		section.Gap = "unknown"
		section.NextStep = "素材摘要未知。这里不显示 0。"
		section.MaterialsKnown = false
	}
	if len(f.Campaigns) > 0 {
		section.Actions = append(section.Actions, ContentAction{ID: "edit_copy", Mode: "inline", Shown: true})
	}
	if section.Gap == "has_material" {
		section.Actions = append(section.Actions, ContentAction{ID: "select_asset", Mode: "inline", Shown: true})
	}
	for _, tool := range f.Tools {
		if tool.State == "entitlement_required" {
			section.UpgradeNote = "未开通的制作能力不会锁住已有素材，开通后只增加新的制作动作。"
		}
	}
	if !f.ReturnToCampaignProven {
		section.Withheld = append(section.Withheld, Withheld{ID: "return_to_campaign", Reason: "not_proven"})
	}
	return section
}

func customerSection(f Facts, campaigns map[string]CampaignFact) CustomerSection {
	if !f.LeadsCapture {
		return CustomerSection{Available: false, Reason: "leads_capture_off"}
	}
	follow := Metric{Available: false, Reason: "follow_up_source_not_connected"}
	if f.FollowUpKnown {
		n := f.FollowUpCount
		follow = Metric{Available: true, Value: &n}
	}
	cards := make([]CustomerCard, 0, len(f.Campaigns))
	for _, campaign := range f.Campaigns {
		if _, ok := campaigns[campaign.ID]; !ok {
			continue
		}
		authorized, sales, pending := 0, 0, 0
		sawReceipt := false
		incomplete := false
		for _, row := range f.Leads {
			if row.CampaignID != campaign.ID || row.Count <= 0 {
				continue
			}
			switch row.SyncState {
			case "revoked":
				continue
			case "crm_received":
				authorized += row.Count
				sales += row.Count
				sawReceipt = true
			case "accepted", "pending_sync":
				authorized += row.Count
				pending += row.Count
			case "rejected":
				authorized += row.Count
			default:
				incomplete = true
			}
		}
		var authorizedN *int
		var pendingN *int
		if !incomplete {
			n := authorized
			p := pending
			authorizedN = &n
			pendingN = &p
		}
		cardFollow := follow
		var openLeads []string
		if fact, ok := f.FollowUps[campaign.ID]; ok && fact.Known {
			n := fact.Count
			cardFollow = Metric{Available: true, Value: &n, Reason: "leads_follow_up_summary"}
			openLeads = append([]string{}, fact.LeadIDs...)
		}
		cards = append(cards, CustomerCard{
			CampaignID:    campaign.ID,
			Title:         campaign.Title,
			Authorized:    authorizedN,
			SalesReceived: salesReceipt(sawReceipt, sales, pending, incomplete),
			PendingSync:   pendingN,
			FollowUp:      cardFollow,
			OpenLeadIDs:   openLeads,
		})
	}
	return CustomerSection{Available: true, Cards: cards}
}

// salesReceipt reports only a confirmed CRM receipt. Pending sync is not a
// receipt and is not a known zero: writing 0 would tell the merchant that
// sales received nothing while the handoff is still open.
func salesReceipt(sawReceipt bool, sales, pending int, incomplete bool) Metric {
	if incomplete {
		return Metric{Available: false, Reason: "sync_state_unknown"}
	}
	if sawReceipt && sales > 0 {
		n := sales
		return Metric{Available: true, Value: &n, Reason: "crm_received"}
	}
	if pending > 0 {
		return Metric{Available: false, Reason: "pending_sync_not_a_receipt"}
	}
	return Metric{Available: false, Reason: "sales_receipt_unknown"}
}
