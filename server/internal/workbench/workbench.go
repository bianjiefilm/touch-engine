package workbench

import "github.com/bianjiefilm/touch-engine/server/internal/activityspend"

type Facts struct {
	Campaigns              []CampaignFact
	Leads                  []LeadCount
	Assets                 []AssetFact
	Library                []LibraryItem
	LibraryEnabled         bool
	UploadEnabled          bool
	LeadsCapture           bool
	ContentTasks           []ContentTask
	ContentTasksKnown      bool
	Tools                  []ToolState
	Intent                 string
	ReturnToCampaignProven bool
	FollowUpKnown          bool
	FollowUpCount          int
	BillingKnown           bool
	BillingAvailability    string
	Seat                   Seat
	Benefits               []BenefitFact
	SubscriptionStatus     string
}

// BenefitFact is an activity-domain marketing fact. It is not a wallet line.
type BenefitFact struct {
	ID, CampaignID, Kind string
	FaceMinor            int64
}

type CampaignFact struct {
	ID, Title, Status string
}

type LeadCount struct {
	CampaignID, SyncState string
	Count                 int
}

type AssetFact struct {
	CampaignID string `json:"campaign_id"`
	AssetID    string `json:"asset_id"`
	Version    string `json:"version,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
}

type LibraryItem struct {
	ID        string `json:"id"`
	MediaType string `json:"media_type"`
	Purpose   string `json:"purpose"`
	CreatedAt string `json:"created_at,omitempty"`
}

type ContentTask struct {
	ID, CampaignID, State string
}

type ToolState struct {
	AppID, State string
}

type Seat struct {
	Role           string `json:"role,omitempty"`
	Source         string `json:"source,omitempty"`
	RelationID     string `json:"relation_id,omitempty"`
	DelegationType string `json:"delegation_type,omitempty"`
	MerchantName   string `json:"merchant_name,omitempty"`
}

type View struct {
	WorkingFor        string                       `json:"working_for,omitempty"`
	Seat              Seat                         `json:"seat"`
	MyTasks           []Task                       `json:"my_tasks"`
	Content           ContentSection               `json:"content"`
	Activities        ActivitySection              `json:"activities"`
	Customers         CustomerSection              `json:"customers"`
	Billing           BillingView                  `json:"billing"`
	AccountSeparation activityspend.SeparationView `json:"account_separation"`
}

type Task struct {
	Kind       string `json:"kind"`
	CampaignID string `json:"campaign_id,omitempty"`
	Title      string `json:"title"`
	ObjectKind string `json:"object_kind"`
	ObjectID   string `json:"object_id"`
	State      string `json:"state"`
}

type ContentSection struct {
	Gap           string          `json:"gap"`
	NextStep      string          `json:"next_step,omitempty"`
	UpgradeNote   string          `json:"upgrade_note,omitempty"`
	LocksExisting bool            `json:"locks_existing"`
	Materials     []LibraryItem   `json:"materials"`
	Selections    []AssetFact     `json:"selections"`
	Actions       []ContentAction `json:"actions"`
	Withheld      []Withheld      `json:"withheld"`
}

type Withheld struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

type ContentAction struct {
	ID    string `json:"id"`
	AppID string `json:"app_id,omitempty"`
	Mode  string `json:"mode"`
	Shown bool   `json:"shown"`
}

type ActivitySection struct {
	Draft      []Activity `json:"draft"`
	InProgress []Activity `json:"in_progress"`
	Ended      []Activity `json:"ended"`
	NextStep   string     `json:"next_step,omitempty"`
}

type Activity struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	ObjectKind string `json:"object_kind"`
	ObjectID   string `json:"object_id"`
	CopyInline bool   `json:"copy_inline"`
}

type CustomerSection struct {
	Available bool           `json:"available"`
	Reason    string         `json:"reason,omitempty"`
	Cards     []CustomerCard `json:"cards,omitempty"`
	NextStep  string         `json:"next_step,omitempty"`
}

type CustomerCard struct {
	CampaignID    string `json:"campaign_id"`
	Title         string `json:"title"`
	Authorized    *int   `json:"authorized,omitempty"`
	SalesReceived Metric `json:"sales_received"`
	PendingSync   int    `json:"pending_sync"`
	FollowUp      Metric `json:"follow_up"`
}

type Metric struct {
	Available bool   `json:"available"`
	Value     *int   `json:"value,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type BillingView struct {
	Known           bool     `json:"known"`
	Availability    string   `json:"availability"`
	Reason          string   `json:"reason,omitempty"`
	PerToolBalances []string `json:"per_tool_balances"`
	CouponsMerged   bool     `json:"promotions_folded"`
}
