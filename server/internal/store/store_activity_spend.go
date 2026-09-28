package store

import (
	"database/sql"

	"github.com/bianjiefilm/touch-engine/server/internal/activityspend"
)

// InsertActivityBenefit persists one activity-domain benefit. The row checks
// refuse a platform user id and a non-zero cash expense.
func (s *Store) InsertActivityBenefit(tenantID string, fact activityspend.BenefitFact) error {
	_, err := s.DB.Exec(`INSERT INTO activity_benefit_facts(
		id,tenant_id,campaign_id,kind,face_minor,op,visitor_ref,ledger,cash_expense_minor,platform_user_id,created_at
	) VALUES(?,?,?,?,?,?,?,?,0,'',?)`,
		fact.ID, tenantID, fact.CampaignID, fact.Kind, fact.FaceMinor, fact.Op, fact.VisitorRef, activityspend.LedgerActivity, now())
	return err
}

// ListActivityBenefits returns activity facts for one tenant. There is no wallet column to read.
func (s *Store) ListActivityBenefits(tenantID string) ([]activityspend.BenefitFact, error) {
	rows, err := s.DB.Query(`SELECT id,campaign_id,kind,face_minor,op,visitor_ref,ledger
		FROM activity_benefit_facts WHERE tenant_id=? ORDER BY created_at`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []activityspend.BenefitFact{}
	for rows.Next() {
		var fact activityspend.BenefitFact
		if err := rows.Scan(&fact.ID, &fact.CampaignID, &fact.Kind, &fact.FaceMinor, &fact.Op, &fact.VisitorRef, &fact.Ledger); err != nil {
			return nil, err
		}
		fact.Cost = activityspend.CostRef{
			Kind:             activityspend.CostRestricted,
			FaceMinor:        fact.FaceMinor,
			CashExpenseMinor: 0,
		}
		out = append(out, fact)
	}
	return out, rows.Err()
}

// ActivityBenefitCashTotal is the stored cash expense for a tenant. The schema forces zero.
func (s *Store) ActivityBenefitCashTotal(tenantID string) (int64, error) {
	var total sql.NullInt64
	err := s.DB.QueryRow(`SELECT COALESCE(SUM(cash_expense_minor),0) FROM activity_benefit_facts WHERE tenant_id=?`, tenantID).Scan(&total)
	if err != nil {
		return 0, err
	}
	return total.Int64, nil
}
