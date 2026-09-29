// Package rewardrule is the HUI-1671 R3 reward-rule kernel.
//
// A rule with evidence_level official_publish is satisfied only by
// official_publish_success. click, export, and submit_publish do not grant
// and are never labeled 已发布. manual_credential stays pending_review.
// Unconfirmed publish status text is 待核实.
//
// The idempotency key is tenant, activity, rule version, subject, and fact
// id. A fixture match is an engineering record, not a channel receipt. This
// package does not call billing, send coupons, or mass-publish. The older
// publishreward package is unchanged.
package rewardrule

import (
	"errors"
	"strconv"
	"strings"
	"sync"
)

const (
	EventClick                  = "click"
	EventExport                 = "export"
	EventSubmitPublish          = "submit_publish"
	EventOfficialPublishSuccess = "official_publish_success"
	EventManualCredential       = "manual_credential"

	EvidenceOfficialPublish = "official_publish"

	StatusPendingReview    = "pending_review"
	StatusNotSatisfied     = "not_satisfied"
	StatusEngineeringMatch = "engineering_match"
	StatusRevoked          = "revoked"
	StatusRejected         = "rejected"

	StatusTextUnconfirmed = "待核实"
	StatusTextFixture     = "工程样例，不是渠道回执"

	LabelPublished     = "已发布"
	LabelUnmet         = "未满足"
	LabelNotPublished  = "未发布"
	LabelPendingVerify = "待核实"
	LabelRuleMatched   = "规则已匹配"
	LabelRevoked       = "已撤销"

	AccountMarketingFace   = "marketing_face_value"
	AccountAICash          = "ai_cash"
	AccountPublicAIBalance = "public_ai_balance"
	AccountCustomerCharge  = "customer_charge"

	ProductionNotAuthorized = "NOT_AUTHORIZED"
	ServiceNotVerified      = "NOT_VERIFIED"
	BillingNotVerified      = "NOT_VERIFIED"
	HumanUnknown            = "UNKNOWN"
	BrowserNotRun           = "NOT_RUN"

	ReasonEvidenceNotMet    = "evidence_not_met"
	ReasonManualPending     = "manual_credential_pending_review"
	ReasonTenantRejected    = "tenant_rejected"
	ReasonActivityMismatch  = "activity_mismatch"
	ReasonVersionMismatch   = "rule_version_mismatch"
	ReasonSubjectMismatch   = "subject_mismatch"
	ReasonRevokedNoRegrant  = "revoked_no_regrant"
	ReasonNotRetroactive    = "rule_version_not_retroactive"
	ReasonEngineeringMatch  = "engineering_fixture_matched"
	ReasonChannelUnverified = "channel_not_verified"
	ReasonMissingField      = "missing_field"
	ReasonSocialSecret      = "social_secret_refused"
	ReasonUnknownEvent      = "unknown_event_kind"
	ReasonUnknownEvidence   = "unknown_evidence_level"
	ReasonFaceNotCharge     = "face_value_is_not_a_charge"
	ReasonMassPublish       = "mass_publish_refused"
	ReasonNoticeRequired    = "user_notice_required"
	ReasonNegativeFace      = "negative_face_value"
	ReasonGrantNotFound     = "grant_not_found"
	ReasonMissingKernel     = "missing_kernel"
)

var (
	ErrMissingField     = errors.New(ReasonMissingField)
	ErrTenantRejected   = errors.New(ReasonTenantRejected)
	ErrActivityMismatch = errors.New(ReasonActivityMismatch)
	ErrVersionMismatch  = errors.New(ReasonVersionMismatch)
	ErrSubjectMismatch  = errors.New(ReasonSubjectMismatch)
	ErrSocialSecret     = errors.New(ReasonSocialSecret)
	ErrUnknownEvent     = errors.New(ReasonUnknownEvent)
	ErrUnknownEvidence  = errors.New(ReasonUnknownEvidence)
	ErrNegativeFace     = errors.New(ReasonNegativeFace)
	ErrFaceNotCharge    = errors.New(ReasonFaceNotCharge)
	ErrMassPublish      = errors.New(ReasonMassPublish)
	ErrNoticeRequired   = errors.New(ReasonNoticeRequired)
	ErrGrantNotFound    = errors.New(ReasonGrantNotFound)
	ErrMissingKernel    = errors.New(ReasonMissingKernel)
)

// Rule is one activity reward version. CouponFaceValue is a marketing amount
// in minor units. It is not an AI cash charge, a public-ai balance movement,
// or a customer charge.
type Rule struct {
	TenantID        string
	ActivityID      string
	Version         int
	EvidenceLevel   string
	CouponFaceValue int64
	FaceCurrency    string
	UserNotice      string
}

// Event is one participation fact. Fixture marks an engineering sample.
// ClaimedChannelReceipt is ignored: this package cannot verify a platform.
// SocialSecret is refused and is not copied onto the decision.
type Event struct {
	TenantID              string
	ActivityID            string
	RuleVersion           int
	SubjectID             string
	FactID                string
	Kind                  string
	Fixture               bool
	ClaimedChannelReceipt bool
	SocialSecret          string
}

// Decision is one kernel verdict. Granted is an engineering ledger mark.
// ChannelReceipt and PlatformSuccess stay false. CouponsSent stays 0.
type Decision struct {
	Granted              bool
	Applied              bool
	Replay               bool
	Rejected             bool
	Revoked              bool
	PendingReview        bool
	RuleSatisfied        bool
	EngineeringOnly      bool
	Fixture              bool
	ChannelReceipt       bool
	PlatformSuccess      bool
	MassPublish          bool
	Status               string
	StatusText           string
	Label                string
	Reason               string
	CouponsSent          int
	BillingCalls         int
	AICashMinor          int64
	PublicAIBalanceMinor int64
	CustomerChargeMinor  int64
	MarketingFaceMinor   int64
	FaceCurrency         string
	Production           string
	Service              string
	Billing              string
}

// FacePosting records a marketing face without moving cash or a wallet.
type FacePosting struct {
	AccountClass         string
	Currency             string
	MarketingMinor       int64
	AICashMinor          int64
	PublicAIBalanceMinor int64
	CustomerChargeMinor  int64
	CouponsSent          int
	BillingCalls         int
}

type record struct {
	tenant   string
	activity string
	subject  string
	fact     string
	kind     string
	version  int
	live     bool
	revoked  bool
	dec      Decision
}

// Kernel is the in-memory reward ledger for one process.
type Kernel struct {
	mu     sync.Mutex
	byKey  map[string]*record
	byFact map[string]*record
}

// New returns an empty kernel.
func New() *Kernel {
	return &Kernel{byKey: map[string]*record{}, byFact: map[string]*record{}}
}

// IdempotencyKey joins tenant, activity, rule version, subject, and fact id.
// Apply trims those strings before calling this.
func IdempotencyKey(tenant, activity string, version int, subject, fact string) string {
	return tenant + "\x00" + activity + "\x00" + strconv.Itoa(version) + "\x00" + subject + "\x00" + fact
}

// Apply records ev against rule once. A second call with the same key returns
// the first verdict. A fact id stays bound to the first tenant, activity, and
// subject; a later rule version does not grant on that fact.
func (k *Kernel) Apply(rule Rule, ev Event) (Decision, error) {
	if k == nil {
		return refuse(ReasonMissingKernel, ErrMissingKernel)
	}
	rule, ev, err := normalize(rule, ev)
	if err != nil {
		return refuse(reasonOf(err), err)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ensure()
	key := IdempotencyKey(ev.TenantID, ev.ActivityID, ev.RuleVersion, ev.SubjectID, ev.FactID)
	if rec := k.byKey[key]; rec != nil {
		return seal(rec.view(true)), nil
	}
	if rec := k.byFact[ev.FactID]; rec != nil {
		switch {
		case rec.tenant != ev.TenantID:
			return refuse(ReasonTenantRejected, ErrTenantRejected)
		case rec.activity != ev.ActivityID:
			return refuse(ReasonActivityMismatch, ErrActivityMismatch)
		case rec.subject != ev.SubjectID:
			return refuse(ReasonSubjectMismatch, ErrSubjectMismatch)
		case rec.version != ev.RuleVersion:
			return seal(Decision{
				Rejected: true,
				Status:   StatusRejected,
				Reason:   ReasonNotRetroactive,
				Label:    LabelNotPublished,
				Fixture:  ev.Fixture,
			}), nil
		default:
			return seal(rec.view(true)), nil
		}
	}
	dec := seal(evaluate(rule, ev))
	k.byKey[key] = &record{
		tenant: ev.TenantID, activity: ev.ActivityID, subject: ev.SubjectID,
		fact: ev.FactID, kind: ev.Kind, version: ev.RuleVersion,
		live: dec.Granted, dec: dec,
	}
	k.byFact[ev.FactID] = k.byKey[key]
	dec.Applied = true
	return dec, nil
}

// Revoke sticks on an existing key. Replaying that event does not grant again.
func (k *Kernel) Revoke(tenant, activity string, version int, subject, fact string) (Decision, error) {
	if k == nil {
		return refuse(ReasonMissingKernel, ErrMissingKernel)
	}
	tenant = strings.TrimSpace(tenant)
	activity = strings.TrimSpace(activity)
	subject = strings.TrimSpace(subject)
	fact = strings.TrimSpace(fact)
	if tenant == "" || activity == "" || subject == "" || fact == "" || version < 1 {
		return refuse(ReasonMissingField, ErrMissingField)
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.ensure()
	rec := k.byKey[IdempotencyKey(tenant, activity, version, subject, fact)]
	if rec == nil {
		return refuse(ReasonGrantNotFound, ErrGrantNotFound)
	}
	rec.revoked = true
	rec.live = false
	d := seal(rec.view(false))
	rec.dec = d
	return d, nil
}

// GrantCount is 1 only while that key has a live, unrevoked engineering grant.
func (k *Kernel) GrantCount(tenant, activity string, version int, subject, fact string) int {
	if k == nil {
		return 0
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	rec := k.byKey[IdempotencyKey(strings.TrimSpace(tenant), strings.TrimSpace(activity), version, strings.TrimSpace(subject), strings.TrimSpace(fact))]
	if rec == nil || !rec.live || rec.revoked {
		return 0
	}
	return 1
}

// LiveGrants is the number of unrevoked engineering matches in this kernel.
func (k *Kernel) LiveGrants() int {
	if k == nil {
		return 0
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, rec := range k.byKey {
		if rec.live && !rec.revoked {
			n++
		}
	}
	return n
}

// Revise bumps the rule version. The caller must tell the user. Old facts are
// not replayed; Apply rejects them when the version no longer matches.
func Revise(rule Rule, notice string) (Rule, error) {
	notice = strings.TrimSpace(notice)
	if notice == "" {
		return rule, ErrNoticeRequired
	}
	rule.TenantID = strings.TrimSpace(rule.TenantID)
	rule.ActivityID = strings.TrimSpace(rule.ActivityID)
	rule.EvidenceLevel = strings.TrimSpace(rule.EvidenceLevel)
	rule.FaceCurrency = strings.TrimSpace(rule.FaceCurrency)
	if rule.TenantID == "" || rule.ActivityID == "" || rule.Version < 1 {
		return rule, ErrMissingField
	}
	if rule.EvidenceLevel == "" {
		return rule, ErrMissingField
	}
	if rule.EvidenceLevel != EvidenceOfficialPublish {
		return rule, ErrUnknownEvidence
	}
	if rule.CouponFaceValue < 0 {
		return rule, ErrNegativeFace
	}
	rule.Version++
	rule.UserNotice = notice
	return rule, nil
}

// PostFace accepts only a marketing-face posting. Cash, public-ai balance,
// and customer-charge classes are refused and write nothing.
func PostFace(rule Rule, accountClass string) (FacePosting, error) {
	if rule.CouponFaceValue < 0 {
		return FacePosting{}, ErrNegativeFace
	}
	if accountClass != AccountMarketingFace {
		return FacePosting{}, ErrFaceNotCharge
	}
	return FacePosting{
		AccountClass:   AccountMarketingFace,
		Currency:       strings.TrimSpace(rule.FaceCurrency),
		MarketingMinor: rule.CouponFaceValue,
	}, nil
}

// MassPublish always refuses. subjects is not read and nothing is granted.
func (k *Kernel) MassPublish(Rule, []string) (Decision, error) {
	return refuse(ReasonMassPublish, ErrMassPublish)
}

func (k *Kernel) ensure() {
	if k.byKey == nil {
		k.byKey = map[string]*record{}
	}
	if k.byFact == nil {
		k.byFact = map[string]*record{}
	}
}

func (rec *record) view(replay bool) Decision {
	d := rec.dec
	d.Applied = false
	d.Replay = replay
	if rec.revoked {
		d.Granted = false
		d.RuleSatisfied = false
		d.EngineeringOnly = false
		d.PendingReview = false
		d.Revoked = true
		d.Rejected = false
		d.Status = StatusRevoked
		d.Reason = ReasonRevokedNoRegrant
		d.Label = LabelRevoked
		d.StatusText = ""
		d.MarketingFaceMinor = 0
		d.FaceCurrency = ""
	} else {
		d.Granted = rec.live
	}
	return d
}

func normalize(rule Rule, ev Event) (Rule, Event, error) {
	if strings.TrimSpace(ev.SocialSecret) != "" {
		ev.SocialSecret = ""
		return Rule{}, Event{}, ErrSocialSecret
	}
	ev.SocialSecret = ""
	rule.TenantID = strings.TrimSpace(rule.TenantID)
	rule.ActivityID = strings.TrimSpace(rule.ActivityID)
	rule.EvidenceLevel = strings.TrimSpace(rule.EvidenceLevel)
	rule.FaceCurrency = strings.TrimSpace(rule.FaceCurrency)
	rule.UserNotice = strings.TrimSpace(rule.UserNotice)
	ev.TenantID = strings.TrimSpace(ev.TenantID)
	ev.ActivityID = strings.TrimSpace(ev.ActivityID)
	ev.SubjectID = strings.TrimSpace(ev.SubjectID)
	ev.FactID = strings.TrimSpace(ev.FactID)
	ev.Kind = strings.TrimSpace(ev.Kind)
	if rule.TenantID == "" || rule.ActivityID == "" || rule.Version < 1 || ev.TenantID == "" || ev.ActivityID == "" || ev.SubjectID == "" || ev.FactID == "" || ev.Kind == "" {
		return Rule{}, Event{}, ErrMissingField
	}
	if rule.CouponFaceValue < 0 {
		return Rule{}, Event{}, ErrNegativeFace
	}
	if rule.EvidenceLevel == "" {
		return Rule{}, Event{}, ErrMissingField
	}
	if rule.EvidenceLevel != EvidenceOfficialPublish {
		return Rule{}, Event{}, ErrUnknownEvidence
	}
	switch ev.Kind {
	case EventClick, EventExport, EventSubmitPublish, EventOfficialPublishSuccess, EventManualCredential:
	default:
		return Rule{}, Event{}, ErrUnknownEvent
	}
	if ev.TenantID != rule.TenantID {
		return Rule{}, Event{}, ErrTenantRejected
	}
	if ev.ActivityID != rule.ActivityID {
		return Rule{}, Event{}, ErrActivityMismatch
	}
	if ev.RuleVersion != rule.Version {
		return Rule{}, Event{}, ErrVersionMismatch
	}
	return rule, ev, nil
}

func evaluate(rule Rule, ev Event) Decision {
	d := Decision{
		Fixture: ev.Fixture,
		Status:  StatusNotSatisfied,
		Reason:  ReasonEvidenceNotMet,
		Label:   LabelUnmet,
	}
	switch ev.Kind {
	case EventClick, EventExport:
	case EventSubmitPublish:
		d.Label = LabelNotPublished
		d.StatusText = StatusTextUnconfirmed
	case EventManualCredential:
		d.Status = StatusPendingReview
		d.PendingReview = true
		d.Reason = ReasonManualPending
		d.Label = LabelPendingVerify
		d.StatusText = StatusTextUnconfirmed
	case EventOfficialPublishSuccess:
		if ev.Fixture {
			d.Status = StatusEngineeringMatch
			d.Reason = ReasonEngineeringMatch
			d.Label = LabelRuleMatched
			d.StatusText = StatusTextFixture
			d.RuleSatisfied = true
			d.Granted = true
			d.EngineeringOnly = true
			d.MarketingFaceMinor = rule.CouponFaceValue
			d.FaceCurrency = rule.FaceCurrency
		} else {
			d.Label = LabelNotPublished
			d.StatusText = StatusTextUnconfirmed
			d.Reason = ReasonChannelUnverified
		}
	}
	return d
}

func seal(d Decision) Decision {
	d.ChannelReceipt = false
	d.PlatformSuccess = false
	d.CouponsSent = 0
	d.BillingCalls = 0
	d.AICashMinor = 0
	d.PublicAIBalanceMinor = 0
	d.CustomerChargeMinor = 0
	d.MassPublish = false
	d.Production = ProductionNotAuthorized
	d.Service = ServiceNotVerified
	d.Billing = BillingNotVerified
	if strings.Contains(d.Label, LabelPublished) || strings.Contains(d.StatusText, LabelPublished) {
		d.Granted = false
		d.RuleSatisfied = false
		d.EngineeringOnly = false
		d.Label = LabelNotPublished
		d.StatusText = StatusTextUnconfirmed
		d.Status = StatusNotSatisfied
		d.Reason = ReasonEvidenceNotMet
		d.MarketingFaceMinor = 0
	}
	return d
}

func refuse(reason string, err error) (Decision, error) {
	return seal(Decision{
		Rejected: true,
		Status:   StatusRejected,
		Reason:   reason,
		Label:    LabelNotPublished,
	}), err
}

func reasonOf(err error) string {
	switch {
	case errors.Is(err, ErrSocialSecret):
		return ReasonSocialSecret
	case errors.Is(err, ErrMissingField):
		return ReasonMissingField
	case errors.Is(err, ErrNegativeFace):
		return ReasonNegativeFace
	case errors.Is(err, ErrUnknownEvidence):
		return ReasonUnknownEvidence
	case errors.Is(err, ErrUnknownEvent):
		return ReasonUnknownEvent
	case errors.Is(err, ErrTenantRejected):
		return ReasonTenantRejected
	case errors.Is(err, ErrActivityMismatch):
		return ReasonActivityMismatch
	case errors.Is(err, ErrVersionMismatch):
		return ReasonVersionMismatch
	case errors.Is(err, ErrSubjectMismatch):
		return ReasonSubjectMismatch
	default:
		if err != nil {
			return err.Error()
		}
		return ReasonMissingField
	}
}
