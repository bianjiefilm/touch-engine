// Package publishreward is the HUI-1671 publish-reward gate.
//
// A reward may consume only an HUI-1670 publish fact that is officially
// confirmed and belongs to the same campaign. Preview, export, opening an
// editor, an unknown result, a click, a merchant post, and a customer
// self-report are not that fact.
//
// All four channels in this deployment can preview and export only. Their
// publish reward stays pending verification and does not grant. Real coupon
// issuance and fee charges stay off; finishing this package is not permission
// to send either.
package publishreward

import (
	"errors"
	"strings"
	"sync"

	"github.com/bianjiefilm/touch-engine/server/internal/custpublish"
)

const (
	TriggerOfficialPublish = "official_confirmed_publish"
	TriggerParticipation   = "activity_participation"

	EvidenceOfficialQuery = "official_query"

	StatusPendingVerification  = "pending_verification"
	StatusPendingReview        = "pending_review"
	StatusReviewedNotConfirmed = "reviewed_not_confirmed"

	KindPreview         = "preview"
	KindExport          = "export"
	KindEditorOpened    = "editor_opened"
	KindUnknown         = "unknown"
	KindSelfReport      = "self_report"
	KindClick           = "click"
	KindMerchantPublish = "merchant_publish"
	KindOfficial        = "official_confirmed_publish"
	KindParticipation   = "verified_participation"

	ReasonChannelCannotProve    = "channel_cannot_prove_publish"
	ReasonPreviewNotPublish     = "preview_is_not_publish"
	ReasonExportNotPublish      = "export_is_not_publish"
	ReasonEditorNotPublish      = "editor_open_is_not_publish"
	ReasonUnknownNotPublish     = "unknown_is_not_publish"
	ReasonSelfReportNotPublish  = "self_report_is_not_publish"
	ReasonClickNotPublish       = "click_is_not_publish"
	ReasonMerchantNotCustomer   = "merchant_publish_is_not_customer"
	ReasonCampaignMismatch      = "campaign_mismatch"
	ReasonIssuanceClosed        = "issuance_closed"
	ReasonParticipationEvidence = "participation_evidence_missing"
	ReasonFactRename            = "fact_rename_forbidden"
	ReasonUserNotice            = "user_notice_required"
	ReasonReviewPermission      = "review_permission_required"
	ReasonReviewBasis           = "review_basis_required"
	ReasonAccountClass          = "account_class_forbidden"
	ReasonMustNotDeduct         = "must_not_deduct_unsettled"

	AccountMarketing    = "marketing_reward"
	AccountOrderService = "order_service"
	AccountToolBalance  = "tool_balance"
	AccountUnsettled    = "unsettled"

	OpClaim     = "claim"
	OpRedeem    = "redeem"
	OpRevoke    = "revoke"
	OpOverLimit = "over_limit"

	OutcomeRecorded  = "recorded"
	OutcomeOverLimit = "over_limit"
	OutcomeReplay    = "replay"

	DecisionNotPlatformConfirmed = "not_platform_confirmed"
)

// realIssuanceAllowed stays false. A green rule test is not authorization to
// send coupons or charge fees.
const realIssuanceAllowed = false

// ChannelProof is one platform's ability to prove a customer publish.
type ChannelProof struct {
	Platform       string
	ConfirmPublish bool
	EvidenceURL    string
}

// Rule is one version of a reward rule. GrantEnabled and IssuanceEnabled stay
// false while the channel cannot prove publish success, and issuance stays
// false even when a caller asks to turn it on.
type Rule struct {
	CampaignID      string
	Version         int
	Trigger         string
	EvidenceLevel   string
	Status          string
	GrantEnabled    bool
	IssuanceEnabled bool
	UserNotice      string
	Reason          string
	Channels        []ChannelProof
}

// ConfigureInput asks for a reward rule. RequestIssuance is ignored.
type ConfigureInput struct {
	CampaignID      string
	Trigger         string
	UserNotice      string
	RequestIssuance bool
	Channels        []ChannelProof
}

// Fact is an HUI-1670 publish fact offered for a reward. Kind is original and
// is not rewritten when a later rule version changes trigger.
type Fact struct {
	ID             string
	CampaignID     string
	SubjectID      string
	Platform       string
	Kind           string
	Publisher      string
	Status         string
	ReceiptSource  string
	PlatformPostID string
}

// Decision is the reward verdict. PostID is empty unless the fact is an
// official receipt on an open confirm channel; this package still does not
// issue a coupon.
type Decision struct {
	Grant             bool
	Eligible          bool
	PlatformConfirmed bool
	PostID            string
	Reason            string
	CouponsIssued     int
	FeesCharged       int
	OutboundCalls     int
	AccountClass      string
}

// Proof is a human-submitted claim. It starts pending and never becomes a
// platform confirmation.
type Proof struct {
	FactID            string
	Status            string
	PlatformConfirmed bool
	Grant             bool
	PostID            string
	Note              string
}

// ReviewInput is a manual review. Allowed defaults false; an empty basis
// cannot pass.
type ReviewInput struct {
	Actor   string
	Allowed bool
	Basis   string
	Approve bool
}

// Audit records who reviewed a proof and that it was not platform-confirmed.
type Audit struct {
	Actor             string
	Basis             string
	Decision          string
	PlatformConfirmed bool
}

// Entry is one ledger posting. Claim, redeem, revoke, and over-limit are
// different operations and do not collapse into one row.
type Entry struct {
	CampaignID   string
	SubjectID    string
	RuleVersion  int
	FactID       string
	Op           string
	AccountClass string
	DeductFrom   string
}

// Key is the anti-duplicate grant key: campaign, subject, rule version, fact.
func (e Entry) Key() string {
	return e.CampaignID + "\x00" + e.SubjectID + "\x00" + itoa(e.RuleVersion) + "\x00" + e.FactID
}

// Result is the effect of one posting. Applied is false when the same key and
// operation was already recorded.
type Result struct {
	Applied       bool
	Outcome       string
	CouponsIssued int
	FeesCharged   int
	AccountClass  string
}

// Issuance is what left the building. This deployment always returns zeros.
type Issuance struct {
	CouponsIssued int
	FeesCharged   int
	OutboundCalls int
}

type gateError struct{ Reason string }

func (e *gateError) Error() string {
	if e == nil {
		return ""
	}
	return e.Reason
}

func closed(reason string) error { return &gateError{Reason: reason} }

// ReasonOf returns a machine reason when err came from this package.
func ReasonOf(err error) string {
	var g *gateError
	if errors.As(err, &g) && g != nil {
		return g.Reason
	}
	return ""
}

// MatrixChannels reads the HUI-1670 capability matrix. Adapter notes do not
// open confirm_publish.
func MatrixChannels(notes []custpublish.AdapterNote) []ChannelProof {
	rows := custpublish.Matrix(notes)
	out := make([]ChannelProof, 0, len(rows))
	for _, row := range rows {
		cell := row.Capability(custpublish.CapConfirmPublish)
		out = append(out, ChannelProof{
			Platform: string(row.Platform), ConfirmPublish: cell.Enabled, EvidenceURL: cell.EvidenceURL,
		})
	}
	return out
}

// Configure builds a publish-reward rule. When no channel can prove a
// customer publish, the rule is pending verification and cannot grant.
// RequestIssuance does not turn real coupons on.
func Configure(in ConfigureInput) (Rule, error) {
	if strings.TrimSpace(in.CampaignID) == "" {
		return Rule{}, closed("missing_campaign")
	}
	trigger := in.Trigger
	if trigger == "" {
		trigger = TriggerOfficialPublish
	}
	reason := ""
	status := StatusPendingVerification
	if trigger == TriggerOfficialPublish && !anyConfirm(in.Channels) {
		reason = ReasonChannelCannotProve
	}
	return Rule{
		CampaignID: in.CampaignID, Version: 1, Trigger: trigger,
		EvidenceLevel: EvidenceOfficialQuery, Status: status,
		GrantEnabled: false, IssuanceEnabled: false,
		UserNotice: strings.TrimSpace(in.UserNotice), Reason: reason,
		Channels: append([]ChannelProof(nil), in.Channels...),
	}, nil
}

func anyConfirm(channels []ChannelProof) bool {
	for _, ch := range channels {
		if ch.ConfirmPublish {
			return true
		}
	}
	return false
}

// Consume decides whether fact can fund rule. It does not copy a client post
// id onto the decision unless the fact is an official receipt and that
// platform's confirm capability is open. Even then it does not grant.
func Consume(rule Rule, fact Fact) Decision {
	deny := func(reason string) Decision {
		return Decision{Reason: reason, AccountClass: AccountMarketing}
	}
	if fact.CampaignID != rule.CampaignID {
		return deny(ReasonCampaignMismatch)
	}
	if rule.Trigger == TriggerParticipation {
		if fact.Kind != KindParticipation {
			return deny(ReasonParticipationEvidence)
		}
		return deny(ReasonIssuanceClosed)
	}
	switch fact.Kind {
	case KindPreview:
		return deny(ReasonPreviewNotPublish)
	case KindExport:
		return deny(ReasonExportNotPublish)
	case KindEditorOpened:
		return deny(ReasonEditorNotPublish)
	case KindUnknown:
		return deny(ReasonUnknownNotPublish)
	case KindSelfReport:
		return deny(ReasonSelfReportNotPublish)
	case KindClick:
		return deny(ReasonClickNotPublish)
	case KindMerchantPublish:
		return deny(ReasonMerchantNotCustomer)
	}
	if fact.Publisher != custpublish.PublisherActivityCustomer {
		return deny(ReasonMerchantNotCustomer)
	}
	if !channelOpen(rule.Channels, fact.Platform) {
		return deny(ReasonChannelCannotProve)
	}
	if !official(fact) {
		return deny(ReasonUnknownNotPublish)
	}
	return Decision{
		Eligible: true, Grant: false, PlatformConfirmed: true,
		PostID: strings.TrimSpace(fact.PlatformPostID), Reason: ReasonIssuanceClosed,
		AccountClass: AccountMarketing,
	}
}

func channelOpen(channels []ChannelProof, platform string) bool {
	for _, ch := range channels {
		if ch.Platform == platform && ch.ConfirmPublish {
			return true
		}
	}
	return false
}

func official(fact Fact) bool {
	return fact.Kind == KindOfficial &&
		fact.Status == custpublish.StatusPublishConfirmed &&
		fact.ReceiptSource == custpublish.ReceiptOfficialQuery &&
		strings.TrimSpace(fact.PlatformPostID) != "" &&
		fact.Publisher == custpublish.PublisherActivityCustomer
}

// SubmitManualProof records a human claim. The typed post id is dropped.
func SubmitManualProof(fact Fact, note string) Proof {
	return Proof{
		FactID: fact.ID, Status: StatusPendingReview, Note: strings.TrimSpace(note),
	}
}

// ReviewProof keeps a proof out of platform-confirmed. Missing permission or
// basis leaves the proof pending and writes no approval.
func ReviewProof(p Proof, in ReviewInput) (Proof, Audit, error) {
	if !in.Allowed {
		return p, Audit{}, closed(ReasonReviewPermission)
	}
	basis := strings.TrimSpace(in.Basis)
	if basis == "" {
		return p, Audit{}, closed(ReasonReviewBasis)
	}
	p.Status = StatusReviewedNotConfirmed
	p.PlatformConfirmed = false
	p.Grant = false
	p.PostID = ""
	return p, Audit{
		Actor: in.Actor, Basis: basis, Decision: DecisionNotPlatformConfirmed,
	}, nil
}

// RenameFactKind refuses to relabel an existing fact. Preview and export stay
// what they were.
func RenameFactKind(fact Fact, next string) (Fact, error) {
	if next != fact.Kind {
		return fact, closed(ReasonFactRename)
	}
	return fact, nil
}

// ReviseTrigger starts a new rule version. Changing the trigger requires a
// non-empty notice to the user. Issuance stays closed.
func ReviseTrigger(current Rule, trigger, notice string) (Rule, error) {
	notice = strings.TrimSpace(notice)
	if trigger != current.Trigger && notice == "" {
		return Rule{}, closed(ReasonUserNotice)
	}
	next := current
	next.Version = current.Version + 1
	next.Trigger = trigger
	next.UserNotice = notice
	next.GrantEnabled = false
	next.IssuanceEnabled = false
	if trigger == TriggerOfficialPublish && !anyConfirm(next.Channels) {
		next.Status = StatusPendingVerification
		next.Reason = ReasonChannelCannotProve
	}
	if trigger == TriggerParticipation {
		next.Status = StatusPendingVerification
		next.Reason = ReasonParticipationEvidence
	}
	return next, nil
}

// Book records marketing-reward postings once per key and operation.
type Book struct {
	mu    sync.Mutex
	limit int
	rows  map[string]Result
}

// NewBook limits how many claim postings one subject may take on one campaign
// and rule version. Extra facts become a single over-limit row.
func NewBook(claimLimit int) *Book {
	if claimLimit < 0 {
		claimLimit = 0
	}
	return &Book{limit: claimLimit, rows: map[string]Result{}}
}

func rowKey(e Entry, op string) string { return e.Key() + "\x00" + op }

// Count is the number of recorded rows for one key and operation.
func (b *Book) Count(key, op string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.rows[key+"\x00"+op]; ok {
		return 1
	}
	return 0
}

// ValidateLedgerAccount rejects anything that is not a marketing-reward
// posting, and any attempt to deduct another balance.
func ValidateLedgerAccount(class, deductFrom string) error {
	if class != AccountMarketing {
		return closed(ReasonAccountClass)
	}
	if deductFrom != "" {
		return closed(ReasonMustNotDeduct)
	}
	return nil
}

// FactFromPreparation maps a stored HUI-1670 attempt onto a reward fact.
// The client does not get to choose the kind or supply a post id.
func FactFromPreparation(id, campaignID, subjectID, platform, status string, selfReported bool) Fact {
	kind := KindPreview
	switch {
	case selfReported:
		kind = KindSelfReport
	case status == custpublish.StatusExported:
		kind = KindExport
	case status == custpublish.StatusUnknown:
		kind = KindUnknown
	case status == "editor_opened":
		kind = KindEditorOpened
	}
	return Fact{
		ID: id, CampaignID: campaignID, SubjectID: subjectID, Platform: platform,
		Kind: kind, Publisher: custpublish.PublisherActivityCustomer, Status: status,
	}
}

// Post records entry once. Replays return the original result with Applied
// false. Order-service, tool-balance, and unsettled accounts are rejected and
// leave no row. Nothing here deducts unsettled funds or issues a coupon.
func (b *Book) Post(e Entry) (Result, error) {
	if err := ValidateLedgerAccount(e.AccountClass, e.DeductFrom); err != nil {
		return Result{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if prev, ok := b.rows[rowKey(e, e.Op)]; ok {
		prev.Applied = false
		prev.Outcome = OutcomeReplay
		return prev, nil
	}
	op := e.Op
	if op == OpClaim && b.claims(e) >= b.limit {
		op = OpOverLimit
		if prev, ok := b.rows[rowKey(e, op)]; ok {
			prev.Applied = false
			prev.Outcome = OutcomeReplay
			return prev, nil
		}
	}
	rk := rowKey(e, op)
	outcome := OutcomeRecorded
	if op == OpOverLimit {
		outcome = OutcomeOverLimit
	}
	res := Result{Applied: true, Outcome: outcome, AccountClass: AccountMarketing}
	b.rows[rk] = res
	return res, nil
}

func (b *Book) claims(e Entry) int {
	n := 0
	prefix := e.CampaignID + "\x00" + e.SubjectID + "\x00" + itoa(e.RuleVersion) + "\x00"
	suffix := "\x00" + OpClaim
	for k := range b.rows {
		if strings.HasPrefix(k, prefix) && strings.HasSuffix(k, suffix) {
			n++
		}
	}
	return n
}

// Gateway is the outbound coupon door. Calls stays zero.
type Gateway struct {
	Calls int
}

// Issue refuses real coupons and fees regardless of the decision.
func (g *Gateway) Issue(Decision) Issuance {
	if g == nil || !realIssuanceAllowed {
		return Issuance{}
	}
	g.Calls++
	return Issuance{CouponsIssued: g.Calls}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
