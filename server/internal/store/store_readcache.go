// store_readcache.go: HUI-2981 缓存支撑层。
//
// ResolveLink(上游唯一全局点查)拆成两半:
//   - ResolvedRows/LoadResolvedRows:数据获取(缓存 miss 回源路径使用);
//   - DecideLink:纯函数五态裁决(rows, at)——开始/结束窗口按调用时刻现判,
//     绝不把「available」永久缓存。
//
// ResolveLink 本体保持逐字节原行为(flag off = 现状);DecideLink 与它的
// 一致性由 store_readcache_test.go 的等价表驱动测试钉死(全场景组合下
// DecideLink(LoadResolvedRows(code)) == ResolveLink(code, at))。
package store

import (
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/campaign"
)

// ResolvedRows carries the rows the public five-state decision consumes (plus
// the display names the public payload attaches). Zero rows are meaningful:
// Found=false is the cached form of "no such code" and is validated by the
// same epoch as everything else, so a later CreateLink invalidates it.
type ResolvedRows struct {
	// Found: the campaign_links row exists for the code.
	Found bool
	Link  CampaignLink

	// HasCampaign: the campaign row was fetched. A link without a campaign
	// decides NotFound (identical to ResolveLink).
	HasCampaign bool
	Campaign    Campaign

	// HasStore: the campaign binds a store, its status is active, and the
	// store row was fetched (same condition under which ResolveLink reads
	// the store at all).
	HasStore bool
	Store    StoreRecord

	// HasTenant: the tenant row was fetched (display name only; it never
	// changes the outcome).
	HasTenant bool
	Tenant    Tenant
}

// DecideLink is the pure five-state decision over already-fetched rows,
// judged at `at` (callers pass time.Now(); the cache re-derives on every
// hit, so start/end windows are never frozen into a cached entry).
func DecideLink(rows ResolvedRows, at time.Time) ResolvedLink {
	if !rows.Found || !rows.HasCampaign {
		return ResolvedLink{Outcome: OutcomeNotFound}
	}
	if !rows.Link.Enabled {
		return ResolvedLink{Outcome: OutcomeLinkDisabled, Campaign: rows.Campaign, Link: rows.Link}
	}
	switch campaign.Status(rows.Campaign.Status) {
	case campaign.StatusActive:
		switch w := (campaign.Window{StartsAt: rows.Campaign.StartsAt, EndsAt: rows.Campaign.EndsAt}).ResolveTime(at); w {
		case "expired":
			return ResolvedLink{Outcome: OutcomeExpired, Campaign: rows.Campaign, Link: rows.Link}
		case "not_started":
			return ResolvedLink{Outcome: OutcomeNotStarted, Campaign: rows.Campaign, Link: rows.Link}
		}
		return ResolvedLink{Outcome: OutcomeAvailable, Campaign: rows.Campaign, Link: rows.Link,
			StoreUnavailable: rows.HasStore && rows.Store.Status == StoreStatusDisabled}
	case campaign.StatusPaused:
		return ResolvedLink{Outcome: OutcomePaused, Campaign: rows.Campaign, Link: rows.Link}
	case campaign.StatusDraft:
		return ResolvedLink{Outcome: OutcomeDraft, Campaign: rows.Campaign, Link: rows.Link}
	case campaign.StatusEnded:
		return ResolvedLink{Outcome: OutcomeEnded, Campaign: rows.Campaign, Link: rows.Link}
	default:
		return ResolvedLink{Outcome: OutcomeNotFound}
	}
}

// LoadResolvedRows is the cache-miss backfill: one set of point queries for
// everything the public page needs (outcome inputs + display names). Failure
// semantics match ResolveLink exactly: any row that cannot be read simply
// stays absent, and the decision fails closed to not_found — a lookup error
// must never look available.
func (s *Store) LoadResolvedRows(code string) ResolvedRows {
	var rows ResolvedRows
	if !campaign.ValidShortcode(code) {
		return rows // malformed is indistinguishable from unknown (NotFound)
	}
	l, err := scanLink(s.DB.QueryRow(`SELECT `+linkCols+` FROM campaign_links WHERE code=?`, code))
	if err != nil { // ErrNoRows OR real error: both fail closed to NotFound
		return rows
	}
	rows.Found, rows.Link = true, l
	c, err := s.GetCampaign(l.CampaignID, l.TenantID)
	if err != nil {
		return rows
	}
	rows.HasCampaign, rows.Campaign = true, c
	if c.StoreID != "" && campaign.Status(c.Status) == campaign.StatusActive {
		if st, err := s.GetStore(c.StoreID, c.TenantID); err == nil {
			rows.HasStore, rows.Store = true, st
		}
	}
	if t, err := s.GetTenant(c.TenantID); err == nil {
		rows.HasTenant, rows.Tenant = true, t
	}
	return rows
}
