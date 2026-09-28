package activityspend

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestCouponFaceStaysOutOfPlatformCash(t *testing.T) {
	var book Ledger
	fact, err := PostBenefit(&book, BenefitInput{
		CampaignID: "cmp_a",
		Kind:       BenefitCoupon,
		FaceMinor:  2000,
		Op:         OpClaim,
		VisitorRef: "guest_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if fact.Ledger != LedgerActivity || fact.Kind != BenefitCoupon || fact.FaceMinor != 2000 {
		t.Fatalf("fact = %+v", fact)
	}
	if fact.Cost.Kind != CostRestricted || fact.Cost.CashExpenseMinor != 0 || fact.Cost.FaceMinor != 2000 {
		t.Fatalf("cost = %+v", fact.Cost)
	}
	if len(book.CashLines) != 0 || CashTotal(&book) != 0 {
		t.Fatalf("wallet = %+v total %d", book.CashLines, CashTotal(&book))
	}

	_, err = PostBenefit(&book, BenefitInput{
		CampaignID:     "cmp_a",
		Kind:           BenefitCoupon,
		FaceMinor:      2000,
		Op:             OpClaim,
		VisitorRef:     "guest_2",
		AsPlatformCash: true,
	})
	if !errors.Is(err, ErrFaceIsNotCash) {
		t.Fatalf("disguise err = %v", err)
	}
	if len(book.Benefits) != 1 || CashTotal(&book) != 0 {
		t.Fatalf("disguise wrote cash or a second benefit: %+v", book)
	}
}

func TestPointsLotteryAndGroupBuyStayInActivity(t *testing.T) {
	var book Ledger
	for _, kind := range []string{BenefitPoints, BenefitLottery, BenefitGroupBuy} {
		fact, err := PostBenefit(&book, BenefitInput{
			CampaignID: "cmp_a",
			Kind:       kind,
			FaceMinor:  500,
			Op:         OpRedeem,
			VisitorRef: "guest_" + kind,
		})
		if err != nil {
			t.Fatal(err)
		}
		if fact.Ledger != LedgerActivity || fact.Kind != kind {
			t.Fatalf("%s fact = %+v", kind, fact)
		}
	}
	if len(book.Benefits) != 3 || len(book.CashLines) != 0 || CashTotal(&book) != 0 {
		t.Fatalf("book leaked into cash: benefits=%d cash=%+v", len(book.Benefits), book.CashLines)
	}
}

func TestVisitorBrowseLeadAndClaimDoNotOpenPlatformAccount(t *testing.T) {
	var book Ledger
	browse := Participate(&book, VisitInput{Action: ActionBrowse})
	if browse.PlatformUserCreated || browse.WalletDebitedMinor != 0 || browse.OrgBalanceVisible || browse.LeadKept || browse.Benefit != nil {
		t.Fatalf("browse = %+v", browse)
	}
	if len(book.PlatformUsers) != 0 || len(book.LeadIDs) != 0 {
		t.Fatalf("browse wrote identity or lead: %+v", book)
	}

	refused := Participate(&book, VisitInput{Action: ActionLead, Consent: false})
	if refused.LeadKept || refused.PlatformUserCreated || len(book.LeadIDs) != 0 {
		t.Fatalf("lead without consent = %+v book=%+v", refused, book.LeadIDs)
	}

	lead := Participate(&book, VisitInput{Action: ActionLead, Consent: true, LeadID: "lead_1"})
	if !lead.LeadKept || lead.PlatformUserCreated || lead.WalletDebitedMinor != 0 || lead.OrgBalanceVisible {
		t.Fatalf("lead = %+v", lead)
	}
	if len(book.PlatformUsers) != 0 || len(book.LeadIDs) != 1 || book.LeadIDs[0] != "lead_1" {
		t.Fatalf("lead book = %+v", book)
	}

	claim := Participate(&book, VisitInput{
		Action: ActionClaim,
		Benefit: &BenefitInput{
			CampaignID: "cmp_a",
			Kind:       BenefitCoupon,
			FaceMinor:  800,
			Op:         OpClaim,
			VisitorRef: "guest_claim",
		},
	})
	if claim.PlatformUserCreated || claim.WalletDebitedMinor != 0 || claim.OrgBalanceVisible || claim.Benefit == nil {
		t.Fatalf("claim = %+v", claim)
	}
	if claim.Benefit.Ledger != LedgerActivity || CashTotal(&book) != 0 || len(book.PlatformUsers) != 0 {
		t.Fatalf("claim entered wallet or created a user: %+v", book)
	}
}

func TestNoChargeWithoutAnActualPaidCall(t *testing.T) {
	idle := DecideCharge(ChargeInput{
		Capability:      CapProductImage,
		Invoked:         false,
		ActorRole:       "org_owner",
		PayerAuthorized: true,
		PayerAccountID:  "org_bill_1",
		Subscription:    StatusActive,
	})
	if idle.QuoteCreated || idle.UsageCreated || idle.BalanceMutated || idle.CashDebitedMinor != 0 {
		t.Fatalf("idle product image charged: %+v", idle)
	}

	native := DecideCharge(ChargeInput{
		Capability:      "edit_copy",
		Invoked:         true,
		ActorRole:       "org_owner",
		PayerAuthorized: true,
		PayerAccountID:  "org_bill_1",
		Subscription:    StatusActive,
	})
	if native.QuoteCreated || native.UsageCreated || native.BalanceMutated || native.CashDebitedMinor != 0 || native.Reason != ReasonNoChargeableCall {
		t.Fatalf("native edit charged: %+v", native)
	}
}

func TestAgentWithoutPayerAuthCannotDebitPersonalWallet(t *testing.T) {
	decision := DecideCharge(ChargeInput{
		Capability:       CapDigitalHuman,
		Invoked:          true,
		ActorRole:        "agent",
		PayerAuthorized:  false,
		PayerAccountID:   "org_bill_1",
		PersonalWalletID: "wal_agent",
		Subscription:     StatusActive,
	})
	if decision.PersonalWalletDebited || decision.DebitedAccount != "" || decision.BalanceMutated || decision.CashDebitedMinor != 0 || decision.QuoteCreated || decision.UsageCreated {
		t.Fatalf("agent debit = %+v", decision)
	}
	if decision.Reason != ReasonPayerAuthorization {
		t.Fatalf("reason = %s", decision.Reason)
	}

	sameWallet := DecideCharge(ChargeInput{
		Capability:       CapAICut,
		Invoked:          true,
		ActorRole:        "agent",
		PayerAuthorized:  true,
		PayerAccountID:   "wal_agent",
		PersonalWalletID: "wal_agent",
		Subscription:     StatusActive,
	})
	if sameWallet.PersonalWalletDebited || sameWallet.QuoteCreated || sameWallet.Reason != ReasonPersonalWalletRefused {
		t.Fatalf("personal wallet accepted as payer: %+v", sameWallet)
	}
}

func TestAuthorizedQuoteDoesNotMutateBalance(t *testing.T) {
	decision := DecideCharge(ChargeInput{
		Capability:      CapAICopy,
		Invoked:         true,
		ActorRole:       "org_owner",
		PayerAuthorized: true,
		PayerAccountID:  "org_bill_1",
		Subscription:    StatusActive,
	})
	if !decision.QuoteCreated || !decision.UsageCreated {
		t.Fatalf("missing quote context: %+v", decision)
	}
	if decision.BalanceMutated || decision.CashDebitedMinor != 0 || decision.DebitedAccount != "" || decision.PersonalWalletDebited {
		t.Fatalf("touch mutated a balance: %+v", decision)
	}
	if decision.PayerAccountID != "org_bill_1" || decision.Reason != ReasonQuoteWithoutDebit {
		t.Fatalf("payer context = %+v", decision)
	}
}

func TestExpiryRestrictsNewPremiumWithoutDeletingFacts(t *testing.T) {
	var book Ledger
	book.CampaignIDs = []string{"cmp_a"}
	_, err := PostBenefit(&book, BenefitInput{
		CampaignID: "cmp_a", Kind: BenefitLottery, FaceMinor: 100, Op: OpClaim, VisitorRef: "guest_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	lead := Participate(&book, VisitInput{Action: ActionLead, Consent: true, LeadID: "lead_1"})
	if !lead.LeadKept {
		t.Fatal("lead missing")
	}

	kept := RetainAfterExpiry(book)
	if kept.NewPremiumAllowed || kept.ReadRequiresRecharge || kept.DeletedCount != 0 {
		t.Fatalf("retention = %+v", kept)
	}
	if len(kept.CampaignIDs) != 1 || kept.CampaignIDs[0] != "cmp_a" || len(kept.LeadIDs) != 1 || kept.LeadIDs[0] != "lead_1" {
		t.Fatalf("facts dropped: %+v", kept)
	}
	if len(kept.BenefitIDs) != 1 || kept.BenefitIDs[0] == "" {
		t.Fatalf("benefit dropped: %+v", kept.BenefitIDs)
	}
	if len(book.CampaignIDs) != 1 || len(book.LeadIDs) != 1 || len(book.Benefits) != 1 || CashTotal(&book) != 0 {
		t.Fatalf("expiry mutated the book: %+v", book)
	}

	blocked := DecideCharge(ChargeInput{
		Capability: CapGoBoost, Invoked: true, ActorRole: "org_owner",
		PayerAuthorized: true, PayerAccountID: "org_bill_1", Subscription: StatusExpired,
	})
	if blocked.QuoteCreated || blocked.UsageCreated || blocked.BalanceMutated || blocked.Reason != ReasonEntitlementExpired {
		t.Fatalf("expired premium still quoted: %+v", blocked)
	}
}

func TestSeparationKeepsThreeLedgersApart(t *testing.T) {
	var book Ledger
	_, err := PostBenefit(&book, BenefitInput{
		CampaignID: "cmp_a", Kind: BenefitCoupon, FaceMinor: 2000, Op: OpClaim, VisitorRef: "guest_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	view := Separation(book, Subscription{Status: StatusExpired}, ChargeDecision{})
	if view.Package.Kind != KindPackage || view.Package.LocksExisting || !view.Package.RestrictsNewPremium {
		t.Fatalf("package = %+v", view.Package)
	}
	if view.AIFees.Kind != KindAIFee || view.AIFees.IncludesMarketingFace || view.AIFees.BalanceMutated || view.AIFees.PersonalWalletDebited {
		t.Fatalf("fees = %+v", view.AIFees)
	}
	if view.Rewards.Kind != KindReward || view.Rewards.Ledger != LedgerActivity || view.Rewards.AppearsInPlatformWallet || view.Rewards.FaceAsCashExpense {
		t.Fatalf("rewards = %+v", view.Rewards)
	}
	if len(view.Rewards.Items) != 1 || view.Rewards.Items[0].FaceMinor != 2000 || view.Rewards.Items[0].Kind != BenefitCoupon {
		t.Fatalf("reward items = %+v", view.Rewards.Items)
	}

	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	fees, _ := decoded["ai_fees"].(map[string]any)
	if _, ok := fees["face_minor"]; ok {
		t.Fatalf("coupon face folded into ai fees: %s", raw)
	}
	rewards, _ := decoded["rewards"].(map[string]any)
	if rewards["appears_in_platform_wallet"] != false || rewards["ledger"] != LedgerActivity {
		t.Fatalf("rewards json = %v", rewards)
	}
	if _, ok := decoded["wallet"]; ok {
		t.Fatalf("separation invented a platform wallet: %s", raw)
	}
}
