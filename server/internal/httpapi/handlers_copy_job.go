package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bianjiefilm/touch-engine/server/internal/activityspend"
	"github.com/bianjiefilm/touch-engine/server/internal/authz"
	"github.com/bianjiefilm/touch-engine/server/internal/copydraft"
	"github.com/bianjiefilm/touch-engine/server/internal/copyjob"
	"github.com/bianjiefilm/touch-engine/server/internal/store"
)

const (
	noticeConfirmFirst   = "请先确认报价。本次没有扣费。"
	noticeInputChanged   = "资料已变更。请重新报价。本次没有扣费，也不会覆盖已写的标题或简介。"
	noticeQuota          = "今日文案额度已用完。本次没有扣费。"
	noticeRealIncomplete = "真实生成未完成。没有可用的模型凭证，不能把结果当成成功文案。"
	noticeTimeout        = "生成超时。没有重复扣费。"
	noticeRevoked        = "已撤销。迟到的结果不会写入活动。"
	noticeSaveBlocked    = "模型未完成，不能把文案写入活动。"
	noticeHandwritten    = "标题或简介已被手写修改，未覆盖。"
	noticeSaved          = "已写入活动文案。活动状态未改变，没有发奖励。"
	noticeSavedPartial   = "标题或简介已被手写修改，未覆盖。已写入活动文案。活动状态未改变，没有发奖励。"
)

type copyJobBody struct {
	IdempotencyKey string            `json:"idempotency_key"`
	Product        copyjob.Product   `json:"product"`
	Price          copydraft.Fact    `json:"price"`
	Address        copydraft.Fact    `json:"address"`
	Hours          copydraft.Fact    `json:"hours"`
	Claims         []copydraft.Claim `json:"claims"`
	POINames       []string          `json:"poi_names"`
	ChannelMount   bool              `json:"channel_mount"`
	AssetIDs       []string          `json:"asset_ids"`
}

func (s *Server) textModel() copyjob.TextModel {
	if s != nil && s.CopyModel != nil {
		return s.CopyModel
	}
	cfg := s.Cfg
	return copyjob.NewPlatformClient(cfg)
}

func (s *Server) copyCharge(c *caller) activityspend.ChargeDecision {
	role, account := "", ""
	if c != nil && c.Member != nil {
		role = c.Member.Role
		account = c.Member.TenantID
	}
	payer := role == string(authz.RoleOrgOwner) || role == string(authz.RoleStoreManager)
	sub := s.Cfg.SubscriptionStatus
	if sub == "" {
		sub = "unconfirmed"
	}
	return activityspend.DecideCharge(activityspend.ChargeInput{
		Capability:      activityspend.CapAICopy,
		Invoked:         true,
		ActorRole:       role,
		PayerAuthorized: payer,
		PayerAccountID:  account,
		Subscription:    sub,
	})
}

func (s *Server) handleCopyJobQuote(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	var body copyJobBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "bad_request", "copy job body must be JSON")
		return
	}
	key := strings.TrimSpace(body.IdempotencyKey)
	if !validIdempotencyKey(key) {
		fail(w, http.StatusBadRequest, "bad_request", "idempotency_key is required and must be 1-80 letters, digits, _ or -")
		return
	}
	assetIDs, ok := s.confirmedAssetIDs(c.Member.TenantID, camp.ID, body.AssetIDs, w)
	if !ok {
		return
	}
	storeName, storeAddress := s.campaignStoreFacts(c.Member.TenantID, camp.StoreID)
	job := copyjob.Quote(copyjob.QuoteInput{
		Key: key,
		Live: copyjob.Live{
			StoreName: storeName, StoreAddress: storeAddress,
			CampaignTitle: camp.Title, PublicContent: camp.PublicContent,
		},
		Facts: copyjob.Facts{
			Product: body.Product, Price: body.Price, Address: body.Address, Hours: body.Hours,
			Claims: body.Claims, POINames: body.POINames,
		},
		AssetIDs:       assetIDs,
		PayerAccountID: c.Member.TenantID,
		Charge:         s.copyCharge(c),
		ModelReady:     s.textModel().Ready(),
	})
	// channel_mount is ignored. Place names stay suggestions.
	_ = body.ChannelMount
	raw, err := json.Marshal(job)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy job encode failed")
		return
	}
	saved, err := s.St.InsertCopyJob(store.CopyJobRow{
		TenantID: c.Member.TenantID, CampaignID: camp.ID, IdempotencyKey: key,
		SnapshotHash: job.SnapshotHash, State: job.State, ChargeCount: job.Trace.ChargeCount,
		Payload: string(raw), CreatedBy: c.Member.PrincipalRef,
	})
	if errors.Is(err, store.ErrIdempotencyConflict) {
		existing, findErr := s.St.FindCopyJobByKey(c.Member.TenantID, camp.ID, key)
		if findErr != nil {
			fail(w, http.StatusConflict, "idempotency_conflict", "这个请求键已经用于另一份资料。请换一个新的键后重试。本次没有扣费。")
			return
		}
		prev, decErr := jobFromRow(existing)
		if decErr != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy job decode failed")
			return
		}
		s.writeCopyJob(w, http.StatusConflict, prev, camp, false, "idempotency_conflict", "这个请求键已经用于另一份资料。请换一个新的键后重试。本次没有扣费。")
		return
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
		return
	}
	if saved.Payload != string(raw) {
		prev, decErr := jobFromRow(saved)
		if decErr != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy job decode failed")
			return
		}
		s.writeCopyJob(w, http.StatusOK, prev, camp, false, "", "")
		return
	}
	job.ID = saved.ID
	if err := s.saveJob(saved, job); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
		return
	}
	s.writeCopyJob(w, http.StatusCreated, job, camp, false, "", "")
}

func (s *Server) handleCopyJobGet(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionReadRecord, w)
	if !ok {
		return
	}
	_, job, ok := s.loadCopyJob(w, c, camp, r.PathValue("jobId"))
	if !ok {
		return
	}
	s.writeCopyJob(w, http.StatusOK, job, camp, false, "", "")
}

func (s *Server) handleCopyJobConfirm(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	row, job, ok := s.loadCopyJob(w, c, camp, r.PathValue("jobId"))
	if !ok {
		return
	}
	snap, err := s.copySnapshot(c.Member.TenantID, camp, job.Facts)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy snapshot failed")
		return
	}
	job = copyjob.Confirm(job, snap.Hash())
	if job.State == copyjob.StateInputChanged {
		job.Notice = noticeInputChanged
		if err := s.saveJob(row, job); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
			return
		}
		s.writeCopyJob(w, http.StatusConflict, job, camp, false, "input_changed", noticeInputChanged)
		return
	}
	if err := s.saveJob(row, job); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
		return
	}
	s.writeCopyJob(w, http.StatusOK, job, camp, false, "", "")
}

func (s *Server) handleCopyJobGenerate(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	row, job, ok := s.loadCopyJob(w, c, camp, r.PathValue("jobId"))
	if !ok {
		return
	}
	snap, err := s.copySnapshot(c.Member.TenantID, camp, job.Facts)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy snapshot failed")
		return
	}
	since := time.Now().UTC().Format("2006-01-02") + "T00:00:00Z"
	n, err := s.St.CountCopyDraftsSince(c.Member.TenantID, since)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy draft quota lookup failed")
		return
	}
	model := s.textModel()
	charge := s.copyCharge(c)
	ready := model.Ready() && charge.QuoteCreated
	plan := copyjob.PlanGenerate(job, snap.Hash(), n < copyDraftDailyQuota, ready)
	if plan.Blocked == "not_confirmed" {
		s.writeCopyJob(w, http.StatusConflict, plan.Job, camp, false, "not_confirmed", noticeConfirmFirst)
		return
	}
	if plan.Busy {
		s.writeCopyJob(w, http.StatusConflict, plan.Job, camp, false, "generation_busy", "生成进行中。请稍后刷新，或撤销后再报价。这次不会重复扣费。")
		return
	}
	if plan.Job.State == copyjob.StateInputChanged {
		plan.Job.Notice = noticeInputChanged
		if err := s.saveJob(row, plan.Job); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
			return
		}
		s.writeCopyJob(w, http.StatusConflict, plan.Job, camp, false, "input_changed", noticeInputChanged)
		return
	}
	if plan.Job.State == copyjob.StateQuota {
		plan.Job.Notice = noticeQuota
		if err := s.saveJob(row, plan.Job); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
			return
		}
		s.writeCopyJob(w, http.StatusTooManyRequests, plan.Job, camp, false, "quota_exceeded", noticeQuota)
		return
	}
	if plan.Compose || plan.CallModel {
		swapped, err := s.swapJob(row, plan.Job)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
			return
		}
		if !swapped {
			fresh, ferr := s.St.GetCopyJob(row.ID, row.TenantID, camp.ID)
			if ferr != nil {
				fail(w, http.StatusInternalServerError, "internal", "copy job lookup failed")
				return
			}
			current, decErr := jobFromRow(fresh)
			if decErr != nil {
				fail(w, http.StatusInternalServerError, "internal", "copy job decode failed")
				return
			}
			s.writeCopyJob(w, http.StatusOK, current, camp, false, "", "")
			return
		}
		job = plan.Job
		if plan.CallModel {
			ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
			res, callErr := model.Generate(ctx, copyjob.GenerateRequest{
				IdempotencyKey: job.IdempotencyKey,
				PayerAccountID: job.Trace.PayerAccountID,
				SnapshotHash:   job.SnapshotHash,
				Prompt:         copyjob.Prompt(job.Snapshot),
			})
			cancel()
			job = finishModelCall(job, res, callErr)
			if job.State == copyjob.StateQuota {
				if err := s.saveJob(row, job); err != nil {
					fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
					return
				}
				s.writeCopyJob(w, http.StatusTooManyRequests, job, camp, false, "quota_exceeded", job.Notice)
				return
			}
		} else {
			job = copyjob.FinishIncomplete(job, copydraft.Compose(job.Snapshot.Input()))
			if !model.Ready() {
				job.Notice = noticeRealIncomplete
			}
		}
		job, err = s.attachCopyDraft(c, camp, job)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy draft save failed")
			return
		}
		if err := s.saveJob(row, job); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
			return
		}
		s.writeCopyJob(w, http.StatusOK, job, camp, false, "", "")
		return
	}
	if job.DraftID == "" && (job.State == copyjob.StateIncomplete || job.State == copyjob.StateGenerated) {
		job, err = s.attachCopyDraft(c, camp, job)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy draft save failed")
			return
		}
		if err := s.saveJob(row, job); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
			return
		}
	}
	s.writeCopyJob(w, http.StatusOK, job, camp, false, "", "")
}

func finishModelCall(job copyjob.Job, res copyjob.GenerateResult, callErr error) copyjob.Job {
	meta := copyjob.ModelMeta{
		ModelID: res.ModelID, ModelVersion: res.ModelVersion, TaskID: res.TaskID, HoldID: res.HoldID,
		AmountMinor: res.AmountMinor, Priced: res.Priced, Submitted: res.Submitted,
	}
	switch {
	case errors.Is(callErr, copyjob.ErrTimeout):
		job = copyjob.Timeout(job, res.Submitted)
		job.Notice = noticeTimeout
	case errors.Is(callErr, copyjob.ErrQuota):
		if res.Submitted {
			job = copyjob.Timeout(job, true)
			job.State = copyjob.StateQuota
			job.Trace.RealGeneration = copyjob.RealIncomplete
			job.Trace.Success = false
		} else {
			job = copyjob.MarkQuota(job)
		}
		job.Notice = noticeQuota
	case callErr != nil && res.Submitted:
		job = copyjob.FinishModel(job, copydraft.Compose(job.Snapshot.Input()), meta)
		job.Notice = noticeRealIncomplete
	case callErr != nil:
		job = copyjob.FinishIncomplete(job, copydraft.Compose(job.Snapshot.Input()))
		job.Notice = noticeRealIncomplete
	default:
		in := job.Snapshot.Input()
		in.ModelAuthorized = strings.TrimSpace(res.ModelID) != "" && strings.TrimSpace(res.ModelVersion) != ""
		job = copyjob.FinishModel(job, copydraft.ApplyModel(in, res.Output), meta)
	}
	return job
}

func (s *Server) handleCopyJobSelect(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	row, job, ok := s.loadCopyJob(w, c, camp, r.PathValue("jobId"))
	if !ok {
		return
	}
	next, err := copyjob.Select(job)
	if err != nil {
		msg := "请先生成草稿后再选定。本次没有扣费，也不会写入活动。"
		s.writeCopyJob(w, http.StatusConflict, next, camp, false, "not_selectable", msg)
		return
	}
	draft, err := s.St.GetCopyDraft(next.DraftID, c.Member.TenantID, camp.ID)
	if err != nil {
		fail(w, http.StatusConflict, "not_selectable", "请先生成草稿后再选定。本次没有扣费，也不会写入活动。")
		return
	}
	if _, _, err := s.St.AcceptCopyVersion(c.Member.TenantID, camp.ID, draft.ID, draft.Payload, c.Member.PrincipalRef); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy version save failed")
		return
	}
	if next.Trace.Success {
		next.Notice = "已选定草稿。还没有写入活动，也没有发奖励。"
	} else {
		next.Notice = "已选定草稿。模型未完成，不能把文案写入活动。活动状态未改变，没有发奖励。"
	}
	if err := s.saveJob(row, next); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
		return
	}
	fresh, err := s.St.GetCampaign(camp.ID, c.Member.TenantID)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "campaign lookup failed")
		return
	}
	s.writeCopyJob(w, http.StatusOK, next, fresh, false, "", "")
}

func (s *Server) handleCopyJobRevoke(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	row, job, ok := s.loadCopyJob(w, c, camp, r.PathValue("jobId"))
	if !ok {
		return
	}
	prev := job.State
	job = copyjob.Revoke(job)
	if job.State == copyjob.StateRevoked && prev != copyjob.StateRevoked {
		job.Notice = noticeRevoked
	}
	if err := s.saveJob(row, job); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
		return
	}
	s.writeCopyJob(w, http.StatusOK, job, camp, false, "", "")
}

func (s *Server) handleCopyJobSave(w http.ResponseWriter, r *http.Request) {
	c := callerFrom(r)
	camp, ok := s.campaignScoped(c, r.PathValue("id"), authz.ActionUpdate, w)
	if !ok {
		return
	}
	row, job, ok := s.loadCopyJob(w, c, camp, r.PathValue("jobId"))
	if !ok {
		return
	}
	plan := copyjob.PlanSave(job, camp.Title, camp.PublicContent)
	if !plan.Allow {
		msg := noticeSaveBlocked
		switch plan.Reason {
		case "handwritten_kept":
			msg = noticeHandwritten
		case "not_selected":
			msg = "请先选定草稿。本次没有写入活动，也没有扣费。"
		}
		job.Notice = msg
		if err := s.saveJob(row, job); err != nil {
			fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
			return
		}
		s.writeCopyJob(w, http.StatusConflict, job, camp, false, plan.Reason, msg)
		return
	}
	patch := store.CampaignPatch{Title: plan.WriteTitle, PublicContent: plan.WriteIntro}
	updated := camp
	if plan.WriteTitle != nil || plan.WriteIntro != nil {
		var err error
		updated, err = s.St.UpdateCampaign(camp.ID, c.Member.TenantID, patch)
		if err != nil {
			fail(w, http.StatusInternalServerError, "internal", "campaign save failed")
			return
		}
	}
	topics := plan.Topics
	if topics == nil {
		topics = []string{}
	}
	rawTopics, err := json.Marshal(topics)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy topics encode failed")
		return
	}
	if err := s.St.UpsertSavedCopy(store.SavedCopy{
		CampaignID: camp.ID, TenantID: c.Member.TenantID,
		TitleWritten: plan.WriteTitle != nil, IntroWritten: plan.WriteIntro != nil,
		Topics: string(rawTopics), SourceJobID: job.ID,
		ModelID: job.Trace.ModelID, ModelVersion: job.Trace.ModelVersion, SourceHash: job.SnapshotHash,
	}); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "saved copy failed")
		return
	}
	job = copyjob.MarkSaved(job, plan.Preserved)
	if len(plan.Preserved) > 0 {
		job.Notice = noticeSavedPartial
	} else {
		job.Notice = noticeSaved
	}
	if err := s.saveJob(row, job); err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy job save failed")
		return
	}
	s.writeCopyJob(w, http.StatusOK, job, updated, true, "", "")
}

func (s *Server) copySnapshot(tenantID string, camp store.Campaign, facts copyjob.Facts) (copyjob.Snapshot, error) {
	name, addr := s.campaignStoreFacts(tenantID, camp.StoreID)
	listed, err := s.St.ListCampaignAssets(tenantID, camp.ID)
	if err != nil {
		return copyjob.Snapshot{}, err
	}
	have := map[string]bool{}
	for _, a := range listed {
		have[a.AssetID] = true
	}
	ids := make([]string, 0, len(facts.AssetIDs))
	for _, id := range facts.AssetIDs {
		if have[id] {
			ids = append(ids, id)
		}
	}
	return copyjob.BuildSnapshot(copyjob.Live{
		StoreName: name, StoreAddress: addr,
		CampaignTitle: camp.Title, PublicContent: camp.PublicContent,
	}, facts, ids), nil
}

func (s *Server) loadCopyJob(w http.ResponseWriter, c *caller, camp store.Campaign, jobID string) (store.CopyJobRow, copyjob.Job, bool) {
	row, err := s.St.GetCopyJob(jobID, c.Member.TenantID, camp.ID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "文案任务不存在。若刚切换了商家，请在当前商家下重新报价。")
		return store.CopyJobRow{}, copyjob.Job{}, false
	}
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy job lookup failed")
		return store.CopyJobRow{}, copyjob.Job{}, false
	}
	job, err := jobFromRow(row)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "copy job decode failed")
		return store.CopyJobRow{}, copyjob.Job{}, false
	}
	return row, job, true
}

func (s *Server) saveJob(row store.CopyJobRow, job copyjob.Job) error {
	job.ID = row.ID
	if job.IdempotencyKey == "" {
		job.IdempotencyKey = row.IdempotencyKey
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return err
	}
	row.State = job.State
	row.ChargeCount = job.Trace.ChargeCount
	row.SnapshotHash = job.SnapshotHash
	row.Payload = string(raw)
	return s.St.UpdateCopyJob(row)
}

func (s *Server) swapJob(row store.CopyJobRow, job copyjob.Job) (bool, error) {
	job.ID = row.ID
	if job.IdempotencyKey == "" {
		job.IdempotencyKey = row.IdempotencyKey
	}
	raw, err := json.Marshal(job)
	if err != nil {
		return false, err
	}
	return s.St.SwapCopyJob(row.ID, row.TenantID, copyjob.StateConfirmed, store.CopyJobRow{
		State: job.State, ChargeCount: job.Trace.ChargeCount, SnapshotHash: job.SnapshotHash, Payload: string(raw),
	})
}

func (s *Server) attachCopyDraft(c *caller, camp store.Campaign, job copyjob.Job) (copyjob.Job, error) {
	if job.DraftID != "" || job.ID == "" {
		return job, nil
	}
	job.Draft.Billed = false
	job.Draft.POI.Mounted = false
	payload, err := json.Marshal(copyDraftView{Draft: job.Draft})
	if err != nil {
		return job, err
	}
	saved, err := s.St.InsertCopyDraft(store.CopyDraft{
		TenantID: c.Member.TenantID, CampaignID: camp.ID, IdempotencyKey: "job-" + job.ID,
		SnapshotHash: job.SnapshotHash, Payload: string(payload), CreatedBy: c.Member.PrincipalRef,
	})
	if err != nil {
		return job, err
	}
	job.DraftID = saved.ID
	return job, nil
}

func jobFromRow(row store.CopyJobRow) (copyjob.Job, error) {
	var job copyjob.Job
	if err := json.Unmarshal([]byte(row.Payload), &job); err != nil {
		return copyjob.Job{}, err
	}
	job.ID = row.ID
	job.State = row.State
	job.Trace.ChargeCount = row.ChargeCount
	job.SnapshotHash = row.SnapshotHash
	job.IdempotencyKey = row.IdempotencyKey
	job.Draft.Billed = false
	job.Draft.POI.Mounted = false
	return job, nil
}

func (s *Server) writeCopyJob(w http.ResponseWriter, status int, job copyjob.Job, camp store.Campaign, mutated bool, code, message string) {
	job.Draft.Billed = false
	job.Draft.POI.Mounted = false
	job.Trace.Billed = false
	job.Trace.BalanceMutated = false
	job.Trace.RewardsTriggered = false
	job.Trace.GoBoostProjectID = ""
	if job.Draft.Topics == nil {
		job.Draft.Topics = []string{}
	}
	if job.Draft.Gaps == nil {
		job.Draft.Gaps = []copydraft.Gap{}
	}
	if job.Preserved == nil {
		job.Preserved = []string{}
	}
	var amount any
	if job.Trace.AmountMinor != nil && job.Trace.Priced {
		amount = *job.Trace.AmountMinor
	}
	currency := job.Trace.Currency
	if currency == "" {
		currency = "CNY"
	}
	real := job.Trace.RealGeneration
	if real == "" {
		real = copyjob.RealIncomplete
	}
	view := map[string]any{
		"id": job.ID, "state": job.State, "real_generation": real,
		"success": job.Trace.Success && real == copyjob.RealCompleted, "billed": false,
		"charge_count": job.Trace.ChargeCount, "rewards_triggered": false, "goboost_project_id": "",
		"model_id": job.Trace.ModelID, "model_version": job.Trace.ModelVersion,
		"pricing_basis": job.Trace.PricingBasis, "priced": job.Trace.Priced, "amount_minor": amount,
		"currency": currency, "payer_account_id": job.Trace.PayerAccountID, "source_hash": job.SnapshotHash,
		"quote_reason": job.Trace.Reason, "notice": job.Notice, "gaps": job.Draft.Gaps, "draft": job.Draft,
		"draft_id": job.DraftID, "poi_mounted": false,
		"campaign_status": camp.Status, "campaign_title": camp.Title, "campaign_public_content": camp.PublicContent,
		"balance_mutated": false, "settlement": job.Trace.Settlement, "preserved": job.Preserved,
		"campaign_mutated": mutated, "task_id": job.Trace.TaskID, "hold_id": job.Trace.HoldID,
		"late_ignored": job.LateIgnored,
	}
	if code != "" {
		view["error"] = code
		view["message"] = message
	}
	writeJSON(w, status, view)
}
