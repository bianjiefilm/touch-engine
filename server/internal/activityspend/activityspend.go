// Package activityspend keeps Touch marketing benefits in the activity
// domain. Coupon, points, lottery, and group-buy face values are not platform
// cash. This package never mutates a wallet balance and never creates a
// platform user for a public visitor.
package activityspend

import (
	"errors"
	"strings"
)

const (
	BenefitCoupon   = "coupon"
	BenefitPoints   = "points"
	BenefitLottery  = "lottery"
	BenefitGroupBuy = "group_buy_voucher"

	OpClaim  = "claim"
	OpRedeem = "redeem"

	LedgerActivity = "activity"

	CostRestricted = "restricted_cost_ref"

	ActionBrowse = "browse"
	ActionLead   = "lead"
	ActionClaim  = "claim"

	CapProductImage = "product_image"
	CapGoBoost      = "goboost"
	CapAICut        = "aicut"
	CapDigitalHuman = "digital_human"
	CapAICopy       = "ai_copy"

	StatusActive      = "active"
	StatusExpired     = "expired"
	StatusUnconfirmed = "unconfirmed"

	KindPackage = "touch_subscription"
	KindAIFee   = "ai_tool_fee"
	KindReward  = "marketing_reward"

	ReasonNoChargeableCall       = "no_chargeable_call"
	ReasonPayerAuthorization     = "payer_authorization_required"
	ReasonPersonalWalletRefused  = "personal_wallet_not_payer"
	ReasonQuoteWithoutDebit      = "quote_without_balance_mutation"
	ReasonEntitlementExpired     = "entitlement_expired"
	ReasonEntitlementUnconfirmed = "entitlement_unconfirmed"
)

// ErrFaceIsNotCash refuses to write a benefit face value as platform cash.
var ErrFaceIsNotCash = errors.New("benefit_face_is_not_platform_cash")

// ErrUnknownBenefit refuses a kind outside the activity benefit set.
var ErrUnknownBenefit = errors.New("unknown_benefit_kind")

// CostRef records a marketing face for ROI without becoming a cash expense.
type CostRef struct {
	Kind             string `json:"kind"`
	FaceMinor        int64  `json:"face_minor"`
	CashExpenseMinor int64  `json:"cash_expense_minor"`
}

// BenefitFact is one activity-domain marketing fact.
type BenefitFact struct {
	ID         string  `json:"id"`
	CampaignID string  `json:"campaign_id"`
	Ledger     string  `json:"ledger"`
	Kind       string  `json:"kind"`
	FaceMinor  int64   `json:"face_minor"`
	Op         string  `json:"op"`
	VisitorRef string  `json:"visitor_ref"`
	Cost       CostRef `json:"cost"`
}

// CashLine is a platform cash wallet movement. This package does not append one.
type CashLine struct {
	Account     string
	AmountMinor int64
	Kind        string
}

// Ledger is the in-memory proof of where a fact landed.
type Ledger struct {
	Benefits      []BenefitFact
	CashLines     []CashLine
	PlatformUsers []string
	LeadIDs       []string
	CampaignIDs   []string
}

// BenefitInput asks to record one marketing benefit.
type BenefitInput struct {
	CampaignID     string
	Kind           string
	FaceMinor      int64
	Op             string
	VisitorRef     string
	AsPlatformCash bool
}

// PostBenefit records a benefit on the activity ledger. A request to disguise
// the face value as platform cash is refused and writes nothing.
func PostBenefit(book *Ledger, in BenefitInput) (BenefitFact, error) {
	if book == nil {
		book = &Ledger{}
	}
	if in.AsPlatformCash {
		return BenefitFact{}, ErrFaceIsNotCash
	}
	if !knownBenefit(in.Kind) || strings.TrimSpace(in.CampaignID) == "" || strings.TrimSpace(in.VisitorRef) == "" {
		return BenefitFact{}, ErrUnknownBenefit
	}
	if in.Op != OpClaim && in.Op != OpRedeem {
		return BenefitFact{}, ErrUnknownBenefit
	}
	if in.FaceMinor < 0 {
		return BenefitFact{}, ErrUnknownBenefit
	}
	id := in.CampaignID + "|" + in.Kind + "|" + in.Op + "|" + in.VisitorRef
	for _, existing := range book.Benefits {
		if existing.ID == id {
			return existing, nil
		}
	}
	fact := BenefitFact{
		ID:         id,
		CampaignID: in.CampaignID,
		Ledger:     LedgerActivity,
		Kind:       in.Kind,
		FaceMinor:  in.FaceMinor,
		Op:         in.Op,
		VisitorRef: in.VisitorRef,
		Cost: CostRef{
			Kind:             CostRestricted,
			FaceMinor:        in.FaceMinor,
			CashExpenseMinor: 0,
		},
	}
	book.Benefits = append(book.Benefits, fact)
	return fact, nil
}

// CashTotal is the platform cash movement total. Benefit posts leave it at zero.
func CashTotal(book *Ledger) int64 {
	if book == nil {
		return 0
	}
	var total int64
	for _, line := range book.CashLines {
		total += line.AmountMinor
	}
	return total
}

// VisitInput is one anonymous public action.
type VisitInput struct {
	Action  string
	Consent bool
	LeadID  string
	Benefit *BenefitInput
}

// VisitResult reports what a public action was allowed to change.
type VisitResult struct {
	PlatformUserCreated bool
	WalletDebitedMinor  int64
	OrgBalanceVisible   bool
	LeadKept            bool
	Benefit             *BenefitFact
}

// Participate applies a public browse, lead, or claim. It never creates a
// platform user, never debits a wallet, and never exposes an org balance.
func Participate(book *Ledger, in VisitInput) VisitResult {
	if book == nil {
		book = &Ledger{}
	}
	out := VisitResult{}
	switch in.Action {
	case ActionLead:
		if in.Consent && strings.TrimSpace(in.LeadID) != "" {
			if !contains(book.LeadIDs, in.LeadID) {
				book.LeadIDs = append(book.LeadIDs, in.LeadID)
			}
			out.LeadKept = true
		}
	case ActionClaim:
		if in.Benefit != nil {
			fact, err := PostBenefit(book, *in.Benefit)
			if err == nil {
				out.Benefit = &fact
			}
		}
	}
	return out
}

// ChargeInput asks whether a capability call may produce quote/usage context.
type ChargeInput struct {
	Capability       string
	Invoked          bool
	ActorRole        string
	PayerAuthorized  bool
	PayerAccountID   string
	PersonalWalletID string
	Subscription     string
}

// ChargeDecision is quote context only. Touch does not mutate a balance here.
type ChargeDecision struct {
	QuoteCreated          bool   `json:"quote_created"`
	UsageCreated          bool   `json:"usage_created"`
	DebitedAccount        string `json:"debited_account"`
	PersonalWalletDebited bool   `json:"personal_wallet_debited"`
	BalanceMutated        bool   `json:"balance_mutated"`
	CashDebitedMinor      int64  `json:"cash_debited_minor"`
	Reason                string `json:"reason"`
	PayerAccountID        string `json:"payer_account_id,omitempty"`
}

// DecideCharge refuses a debit when nothing chargeable was invoked, when an
// agent lacks payer authorization, or when the only offered wallet is personal.
// An authorized organization payer receives quote/usage context and no balance write.
func DecideCharge(in ChargeInput) ChargeDecision {
	refused := ChargeDecision{Reason: ReasonNoChargeableCall}
	if !in.Invoked || !chargeable(in.Capability) {
		return refused
	}
	if in.Subscription == StatusExpired {
		refused.Reason = ReasonEntitlementExpired
		return refused
	}
	if in.Subscription != StatusActive {
		refused.Reason = ReasonEntitlementUnconfirmed
		return refused
	}
	personal := strings.TrimSpace(in.PersonalWalletID)
	payer := strings.TrimSpace(in.PayerAccountID)
	if personal != "" && payer == personal {
		refused.Reason = ReasonPersonalWalletRefused
		return refused
	}
	if in.ActorRole == "agent" && !in.PayerAuthorized {
		refused.Reason = ReasonPayerAuthorization
		return refused
	}
	if payer == "" || !in.PayerAuthorized {
		refused.Reason = ReasonPayerAuthorization
		return refused
	}
	return ChargeDecision{
		QuoteCreated:   true,
		UsageCreated:   true,
		PayerAccountID: payer,
		Reason:         ReasonQuoteWithoutDebit,
	}
}

// Retention is what remains readable after a subscription expires.
type Retention struct {
	NewPremiumAllowed    bool
	ReadRequiresRecharge bool
	DeletedCount         int
	CampaignIDs          []string
	LeadIDs              []string
	BenefitIDs           []string
}

// RetainAfterExpiry copies existing facts and closes new premium use.
// It does not delete the source book and does not require a recharge to read.
func RetainAfterExpiry(book Ledger) Retention {
	kept := Retention{
		CampaignIDs: append([]string(nil), book.CampaignIDs...),
		LeadIDs:     append([]string(nil), book.LeadIDs...),
	}
	for _, fact := range book.Benefits {
		kept.BenefitIDs = append(kept.BenefitIDs, fact.ID)
	}
	return kept
}

// Subscription is the Touch package fact, separate from fees and rewards.
type Subscription struct {
	Status string
}

// PackagePane is the Touch subscription entitlement.
type PackagePane struct {
	Kind                string `json:"kind"`
	Status              string `json:"status"`
	RestrictsNewPremium bool   `json:"restricts_new_premium"`
	LocksExisting       bool   `json:"locks_existing"`
}

// AIFeePane is AI tool quote context. It does not carry marketing face value.
type AIFeePane struct {
	Kind                  string `json:"kind"`
	QuoteCreated          bool   `json:"quote_created"`
	UsageCreated          bool   `json:"usage_created"`
	IncludesMarketingFace bool   `json:"includes_marketing_face"`
	BalanceMutated        bool   `json:"balance_mutated"`
	PersonalWalletDebited bool   `json:"personal_wallet_debited"`
	PayerAccountID        string `json:"payer_account_id,omitempty"`
	Reason                string `json:"reason,omitempty"`
}

// RewardItem is one activity benefit shown apart from cash.
type RewardItem struct {
	Kind      string `json:"kind"`
	FaceMinor int64  `json:"face_minor"`
	Ledger    string `json:"ledger"`
}

// RewardPane is the marketing ledger.
type RewardPane struct {
	Kind                    string       `json:"kind"`
	Ledger                  string       `json:"ledger"`
	AppearsInPlatformWallet bool         `json:"appears_in_platform_wallet"`
	FaceAsCashExpense       bool         `json:"face_as_cash_expense"`
	Items                   []RewardItem `json:"items"`
}

// SeparationView is the three-way account split.
type SeparationView struct {
	Package PackagePane `json:"package"`
	AIFees  AIFeePane   `json:"ai_fees"`
	Rewards RewardPane  `json:"rewards"`
}

// Separation builds the three panes. Marketing face never lands on the fee pane.
func Separation(book Ledger, sub Subscription, charge ChargeDecision) SeparationView {
	status := sub.Status
	if status == "" {
		status = StatusUnconfirmed
	}
	items := make([]RewardItem, 0, len(book.Benefits))
	for _, fact := range book.Benefits {
		items = append(items, RewardItem{Kind: fact.Kind, FaceMinor: fact.FaceMinor, Ledger: LedgerActivity})
	}
	return SeparationView{
		Package: PackagePane{
			Kind:                KindPackage,
			Status:              status,
			RestrictsNewPremium: status != StatusActive,
			LocksExisting:       false,
		},
		AIFees: AIFeePane{
			Kind:                  KindAIFee,
			QuoteCreated:          charge.QuoteCreated,
			UsageCreated:          charge.UsageCreated,
			IncludesMarketingFace: false,
			BalanceMutated:        false,
			PersonalWalletDebited: false,
			PayerAccountID:        charge.PayerAccountID,
			Reason:                charge.Reason,
		},
		Rewards: RewardPane{
			Kind:                    KindReward,
			Ledger:                  LedgerActivity,
			AppearsInPlatformWallet: false,
			FaceAsCashExpense:       false,
			Items:                   items,
		},
	}
}

func knownBenefit(kind string) bool {
	switch kind {
	case BenefitCoupon, BenefitPoints, BenefitLottery, BenefitGroupBuy:
		return true
	default:
		return false
	}
}

func chargeable(capability string) bool {
	switch capability {
	case CapProductImage, CapGoBoost, CapAICut, CapDigitalHuman, CapAICopy:
		return true
	default:
		return false
	}
}

func contains(ids []string, id string) bool {
	for _, existing := range ids {
		if existing == id {
			return true
		}
	}
	return false
}
