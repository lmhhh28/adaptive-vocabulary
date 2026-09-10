package generation

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
)

type Service struct {
	db     *sql.DB
	policy PolicyGenerationConfig
}

func NewService(db *sql.DB, policy PolicyGenerationConfig) *Service {
	return &Service{
		db:     db,
		policy: policy,
	}
}

// ComputeFixedItemInputDigest computes the SHA-256 hex digest of a canonical FixedItemInput.
func ComputeFixedItemInputDigest(input FixedItemInput) (string, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	canonical, err := core.CanonicalizeJSON(data)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(canonical)
	return hex.EncodeToString(h[:]), nil
}

// ComputeSettlementDigest computes the SHA-256 hex digest of settlement parameters.
func ComputeSettlementDigest(outcome string, providerRequestID, responseSHA256 *string, usage *TokenUsage) (string, error) {
	payload := map[string]any{
		"outcome": outcome,
	}
	if providerRequestID != nil {
		payload["provider_request_id"] = *providerRequestID
	}
	if responseSHA256 != nil {
		payload["response_sha256"] = *responseSHA256
	}
	if usage != nil {
		payload["usage"] = map[string]any{
			"input_tokens":  usage.InputTokens,
			"output_tokens": usage.OutputTokens,
		}
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	canonical, err := core.CanonicalizeJSON(data)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(canonical)
	return hex.EncodeToString(h[:]), nil
}

func (s *Service) CreateJob(ctx context.Context, bankID string, req CreateJobRequest, idempotencyKey, digest string, nowMicro int64) (*CreateJobResponse, bool, error) {
	if len(req.SenseRefs) == 0 {
		return nil, false, errors.New("sense_refs must not be empty")
	}
	if len(req.SenseRefs) > s.policy.MaxUnitsPerJob {
		return nil, false, fmt.Errorf("number of senses exceeds max units per job (%d)", s.policy.MaxUnitsPerJob)
	}

	var resp *CreateJobResponse
	var isReplay bool

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		scope := fmt.Sprintf("/api/v1/banks/%s/generation-jobs", bankID)
		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", scope, idempotencyKey, digest)
			if err != nil {
				return err
			}
			if rec != nil {
				var cached CreateJobResponse
				if err := json.Unmarshal([]byte(rec.ResponseBody), &cached); err == nil {
					resp = &cached
					isReplay = true
					return nil
				}
			}
		}

		// 1. Check maintenance mode
		var maintMode string
		err := tc.QueryRow("SELECT mode FROM projection_control WHERE id = 1").Scan(&maintMode)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check maintenance mode: %w", err)
		}

		// 2. Verify bank and check expected revision
		var bankRev int
		var archived int
		err = tc.QueryRow("SELECT revision, archived FROM bank WHERE id = ?", bankID).Scan(&bankRev, &archived)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("bank not found")
		}
		if err != nil {
			return fmt.Errorf("query bank: %w", err)
		}
		if archived == 1 {
			return errors.New("bank is archived")
		}
		if bankRev != req.ExpectedRevision {
			return fmt.Errorf("bank revision conflict: expected %d, got %d", req.ExpectedRevision, bankRev)
		}

		// Update bank revision
		_, err = tc.Exec("UPDATE bank SET revision = revision + 1, updated_at = ? WHERE id = ?", nowMicro, bankID)
		if err != nil {
			return fmt.Errorf("update bank revision: %w", err)
		}

		// 3. For each senseRef, query details and construct FixedItemInput
		type itemPrep struct {
			senseRef    SenseRef
			fixedInput  FixedItemInput
			inputDigest string
		}
		var prepped []itemPrep
		var itemDigests []string

		for _, ref := range req.SenseRefs {
			// Verify membership
			var memCount int
			err = tc.QueryRow("SELECT COUNT(*) FROM bank_sense WHERE bank_id = ? AND sense_id = ?", bankID, ref.SenseID).Scan(&memCount)
			if err != nil || memCount == 0 {
				return fmt.Errorf("sense %s is not a member of bank %s", ref.SenseID, bankID)
			}

			// Query lexeme canonical form and sense info
			var lexemeID, canonicalForm, pos, targetMeaning, sourceID, sourceVersion string
			querySense := `
				SELECT s.lexeme_id, l.canonical_form, s.part_of_speech, sv.meaning, s.source_id, src.source_version
				FROM sense s
				JOIN lexeme l ON l.id = s.lexeme_id
				JOIN sense_version sv ON sv.sense_id = s.id AND sv.id = ?
				JOIN source src ON src.id = s.source_id
				WHERE s.id = ?
			`
			err = tc.QueryRow(querySense, ref.SenseVersionID, ref.SenseID).Scan(
				&lexemeID, &canonicalForm, &pos, &targetMeaning, &sourceID, &sourceVersion,
			)
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("sense %s version %s not found", ref.SenseID, ref.SenseVersionID)
			}
			if err != nil {
				return fmt.Errorf("query sense details: %w", err)
			}

			fixedInput := FixedItemInput{
				SenseID:                 ref.SenseID,
				SenseVersionID:          ref.SenseVersionID,
				LexemeID:                lexemeID,
				CanonicalForm:           canonicalForm,
				PartOfSpeech:            pos,
				TargetMeaning:           targetMeaning,
				SourceID:                sourceID,
				SourceVersion:           sourceVersion,
				CurriculumPolicyVersion: "curriculum-v1",
				RequiredTaskFamilies:    []string{"meaning_recognition", "reverse_selection", "context_selection"},
				RequestedFormCounts:     s.policy.FormsPerUnit,
				GenerationConfigVersion: req.GenerationConfigVersion,
				QualityPolicyVersion:    req.QualityPolicyVersion,
			}

			digest, err := ComputeFixedItemInputDigest(fixedInput)
			if err != nil {
				return fmt.Errorf("compute item input digest: %w", err)
			}

			prepped = append(prepped, itemPrep{
				senseRef:    ref,
				fixedInput:  fixedInput,
				inputDigest: digest,
			})
			itemDigests = append(itemDigests, digest)
		}

		sort.Strings(itemDigests)
		h := sha256.Sum256([]byte(strings.Join(itemDigests, "\n")))
		fixedInputDigest := hex.EncodeToString(h[:])

		// 4. Insert generation_job
		jobID := core.NewUUID()
		requestedCount := len(prepped) * s.policy.FormsPerUnit
		insertJob := `
			INSERT INTO generation_job (
				id, bank_id, generation_config_version, quality_policy_version, status,
				fixed_input_digest, requested_count, published_count, rejected_count, pending_count,
				revision, created_at, updated_at
			) VALUES (?, ?, ?, ?, 'pending', ?, ?, 0, 0, ?, 0, ?, ?)
		`
		_, err = tc.Exec(insertJob,
			jobID, bankID, req.GenerationConfigVersion, req.QualityPolicyVersion,
			fixedInputDigest, requestedCount, len(prepped), nowMicro, nowMicro,
		)
		if err != nil {
			return fmt.Errorf("insert generation_job: %w", err)
		}

		// 5. Insert generation_budget_ledger
		insertLedger := `
			INSERT INTO generation_budget_ledger (
				job_id, max_requests, max_tokens, authorized_requests, total_tokens, budget_overrun, updated_at
			) VALUES (?, ?, ?, 0, 0, 0, ?)
		`
		_, err = tc.Exec(insertLedger, jobID, s.policy.MaxRequestsPerJob, s.policy.MaxTokensPerJob, nowMicro)
		if err != nil {
			return fmt.Errorf("insert generation_budget_ledger: %w", err)
		}

		// 6. Insert generation_items
		insertItem := `
			INSERT INTO generation_item (
				id, job_id, bank_id, sense_id, sense_version_id, fixed_input_json, input_digest,
				status, lease_owner, lease_until, fencing_token, attempt_number, revision, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 'pending', NULL, NULL, 0, 1, 0, ?, ?)
		`
		for _, item := range prepped {
			itemID := core.NewUUID()
			fixedJSON, _ := json.Marshal(item.fixedInput)
			_, err = tc.Exec(insertItem,
				itemID, jobID, bankID, item.senseRef.SenseID, item.senseRef.SenseVersionID,
				string(fixedJSON), item.inputDigest, nowMicro, nowMicro,
			)
			if err != nil {
				return fmt.Errorf("insert generation_item: %w", err)
			}
		}

		resp = &CreateJobResponse{
			JobID:            jobID,
			Status:           "pending",
			FixedInputDigest: fixedInputDigest,
		}

		if idempotencyKey != "" {
			respJSON, _ := json.Marshal(resp)
			if err := core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
				Operation:     "POST",
				ResourceScope: scope,
				Key:           idempotencyKey,
				RequestDigest: digest,
				Status:        "committed",
				ResponseCode:  http.StatusAccepted,
				ResponseBody:  string(respJSON),
				CreatedAt:     nowMicro,
			}); err != nil {
				return fmt.Errorf("save idempotency: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}
	return resp, isReplay, nil
}

func (s *Service) ClaimItems(ctx context.Context, req ClaimRequest, nowMicro int64) (*ClaimResponse, error) {
	limit := req.Limit
	if limit <= 0 || limit > s.policy.MaxConcurrentItems {
		limit = s.policy.MaxConcurrentItems
	}
	if req.WorkerID == "" {
		return nil, errors.New("worker_id required")
	}

	var resp ClaimResponse
	resp.Items = make([]ClaimedItem, 0)

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// Enforce in-flight concurrency cap: count active unexpired leases
		var inFlightCount int
		err := tc.QueryRow(`
			SELECT COUNT(*)
			FROM generation_item
			WHERE status = 'claimed' AND lease_until >= ?
		`, nowMicro).Scan(&inFlightCount)
		if err != nil {
			return fmt.Errorf("count in-flight items: %w", err)
		}

		availableSlots := s.policy.MaxConcurrentItems - inFlightCount
		if availableSlots <= 0 {
			return nil
		}
		claimLimit := limit
		if claimLimit > availableSlots {
			claimLimit = availableSlots
		}

		// Find items that are pending or expired claimed items
		query := `
			SELECT id, job_id, fixed_input_json, input_digest, status, fencing_token, attempt_number, lease_until
			FROM generation_item
			WHERE status = 'pending' OR (status = 'claimed' AND lease_until < ?)
			ORDER BY created_at ASC
			LIMIT ?
		`
		rows, err := tc.Query(query, nowMicro, claimLimit)
		if err != nil {
			return fmt.Errorf("query claimable items: %w", err)
		}
		defer rows.Close()

		type candidateItem struct {
			id             string
			jobID          string
			fixedInputJSON string
			inputDigest    string
			status         string
			fencingToken   int64
			attemptNumber  int
			leaseUntil     sql.NullInt64
		}
		var candidates []candidateItem
		for rows.Next() {
			var c candidateItem
			if err := rows.Scan(&c.id, &c.jobID, &c.fixedInputJSON, &c.inputDigest, &c.status, &c.fencingToken, &c.attemptNumber, &c.leaseUntil); err != nil {
				return fmt.Errorf("scan claimable item: %w", err)
			}
			candidates = append(candidates, c)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate claimable items: %w", err)
		}

		leaseDurationMicro := int64(s.policy.LeaseSeconds) * 1000000

		for _, c := range candidates {
			newFencingToken := c.fencingToken + 1
			newAttemptNumber := c.attemptNumber

			// If taking over an expired lease:
			// Per 10 R02: Go启动恢复和claim接管事务将已过期/被作废fence的reserved调用条件更新为unknown，并保留预留；这与递增fence/授予新租约同事务完成。
			if c.status == "claimed" {
				updateCalls := `
					UPDATE generation_call
					SET status = 'unknown', updated_at = ?
					WHERE item_id = ? AND status = 'reserved'
				`
				if _, err := tc.Exec(updateCalls, nowMicro, c.id); err != nil {
					return fmt.Errorf("mark expired reserved calls as unknown: %w", err)
				}
				newAttemptNumber++
			}

			newLeaseUntil := nowMicro + leaseDurationMicro

			updateItem := `
				UPDATE generation_item
				SET status = 'claimed', lease_owner = ?, lease_until = ?, fencing_token = ?, attempt_number = ?, revision = revision + 1, updated_at = ?
				WHERE id = ?
			`
			_, err = tc.Exec(updateItem, req.WorkerID, newLeaseUntil, newFencingToken, newAttemptNumber, nowMicro, c.id)
			if err != nil {
				return fmt.Errorf("claim item %s: %w", c.id, err)
			}

			// Update job status to processing if it was pending
			_, _ = tc.Exec(`UPDATE generation_job SET status = 'processing', updated_at = ? WHERE id = ? AND status = 'pending'`, nowMicro, c.jobID)

			var fixedInput FixedItemInput
			if err := json.Unmarshal([]byte(c.fixedInputJSON), &fixedInput); err != nil {
				return fmt.Errorf("unmarshal fixed input for item %s: %w", c.id, err)
			}

			resp.Items = append(resp.Items, ClaimedItem{
				ItemID:        c.id,
				JobID:         c.jobID,
				FixedInput:    fixedInput,
				InputDigest:   c.inputDigest,
				LeaseUntil:    newLeaseUntil,
				FencingToken:  newFencingToken,
				AttemptNumber: newAttemptNumber,
			})
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return &resp, nil
}

func (s *Service) RenewLease(ctx context.Context, req RenewRequest, nowMicro int64) (*RenewResponse, error) {
	var newUntil int64

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		var leaseOwner sql.NullString
		var leaseUntil sql.NullInt64
		var fencingToken int64
		var inputDigest string
		var status string

		query := `
			SELECT lease_owner, lease_until, fencing_token, input_digest, status
			FROM generation_item
			WHERE id = ?
		`
		err := tc.QueryRow(query, req.ItemID).Scan(&leaseOwner, &leaseUntil, &fencingToken, &inputDigest, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup item: %w", err)
		}

		if status != "claimed" || !leaseOwner.Valid || leaseOwner.String != req.WorkerID ||
			fencingToken != req.FencingToken || inputDigest != req.InputDigest ||
			!leaseUntil.Valid || nowMicro > leaseUntil.Int64 {
			return ErrLeaseLost
		}

		newUntil = nowMicro + int64(s.policy.LeaseSeconds)*1000000
		_, err = tc.Exec("UPDATE generation_item SET lease_until = ?, updated_at = ? WHERE id = ?", newUntil, nowMicro, req.ItemID)
		if err != nil {
			return fmt.Errorf("renew lease: %w", err)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return &RenewResponse{LeaseUntil: newUntil}, nil
}

func (s *Service) ReserveCall(ctx context.Context, req ReserveRequest, idempotencyKey, digest string, nowMicro int64) (*ReserveResponse, bool, error) {
	var resp *ReserveResponse
	var isReplay bool

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		scope := "/internal/v1/generation/calls/reserve"
		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", scope, idempotencyKey, digest)
			if err != nil {
				return err
			}
			if rec != nil {
				var cached ReserveResponse
				if err := json.Unmarshal([]byte(rec.ResponseBody), &cached); err == nil {
					resp = &cached
					isReplay = true
					return nil
				}
			}
		}

		// 1. Verify valid item lease
		var jobID string
		var leaseOwner sql.NullString
		var leaseUntil sql.NullInt64
		var fencingToken int64
		var inputDigest string
		var status string

		queryItem := `
			SELECT job_id, lease_owner, lease_until, fencing_token, input_digest, status
			FROM generation_item
			WHERE id = ?
		`
		err := tc.QueryRow(queryItem, req.ItemID).Scan(&jobID, &leaseOwner, &leaseUntil, &fencingToken, &inputDigest, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup item for reserve: %w", err)
		}

		if status != "claimed" || !leaseOwner.Valid || leaseOwner.String != req.WorkerID ||
			fencingToken != req.FencingToken || inputDigest != req.InputDigest ||
			!leaseUntil.Valid || nowMicro > leaseUntil.Int64 {
			return ErrLeaseLost
		}

		// 2. Check logical call key attempts
		logicalKeyStr := req.LogicalCallKey.String()
		var attemptsCount int
		queryAttempts := `
			SELECT COUNT(*)
			FROM generation_call
			WHERE item_id = ? AND logical_call_key = ?
		`
		err = tc.QueryRow(queryAttempts, req.ItemID, logicalKeyStr).Scan(&attemptsCount)
		if err != nil {
			return fmt.Errorf("count call attempts: %w", err)
		}
		if attemptsCount >= s.policy.MaxNetworkAttemptsPerCall {
			return ErrCallAttemptsExhausted
		}

		// 3. Check network_attempt_id uniqueness in job
		var existingResID string
		queryNet := `SELECT reservation_id FROM generation_call WHERE job_id = ? AND network_attempt_id = ?`
		err = tc.QueryRow(queryNet, jobID, req.NetworkAttemptID).Scan(&existingResID)
		if err == nil {
			return fmt.Errorf("%w: duplicate network_attempt_id in job", ErrCallAttemptsExhausted)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check network_attempt_id: %w", err)
		}

		// 4. Check budget ledger
		var maxReqs, maxToks, authReqs, totToks, overrun int
		queryLedger := `
			SELECT max_requests, max_tokens, authorized_requests, total_tokens, budget_overrun
			FROM generation_budget_ledger
			WHERE job_id = ?
		`
		err = tc.QueryRow(queryLedger, jobID).Scan(&maxReqs, &maxToks, &authReqs, &totToks, &overrun)
		if err != nil {
			return fmt.Errorf("query budget ledger: %w", err)
		}

		if overrun == 1 {
			return ErrBudgetExhausted
		}

		reservedTokens := req.InputTokenEstimate + req.MaxOutputTokens
		if reservedTokens <= 0 {
			reservedTokens = 1
		}

		if authReqs+1 > maxReqs || totToks+reservedTokens > maxToks {
			return ErrBudgetExhausted
		}

		// 5. Update budget ledger
		newAuthReqs := authReqs + 1
		newTotToks := totToks + reservedTokens
		updateLedger := `
			UPDATE generation_budget_ledger
			SET authorized_requests = ?, total_tokens = ?, updated_at = ?
			WHERE job_id = ?
		`
		_, err = tc.Exec(updateLedger, newAuthReqs, newTotToks, nowMicro, jobID)
		if err != nil {
			return fmt.Errorf("update budget ledger: %w", err)
		}

		// 6. Insert generation_call
		reservationID := core.NewUUID()
		insertCall := `
			INSERT INTO generation_call (
				reservation_id, job_id, item_id, network_attempt_id, attempt_number,
				stage, candidate_index, evaluation_index, logical_call_key, profile_version,
				input_sha256, input_token_estimate, max_output_tokens, reserved_tokens,
				owner_worker_id, fencing_token, status, outcome, accounted_tokens,
				created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'reserved', NULL, ?, ?, ?)
		`
		_, err = tc.Exec(insertCall,
			reservationID, jobID, req.ItemID, req.NetworkAttemptID, req.LogicalCallKey.AttemptNumber,
			req.LogicalCallKey.Stage, req.LogicalCallKey.CandidateIndex, req.LogicalCallKey.EvaluationIndex,
			logicalKeyStr, req.ProfileVersion, req.InputSHA256, req.InputTokenEstimate, req.MaxOutputTokens,
			reservedTokens, req.WorkerID, req.FencingToken, reservedTokens, nowMicro, nowMicro,
		)
		if err != nil {
			return fmt.Errorf("insert generation_call: %w", err)
		}

		resp = &ReserveResponse{
			ReservationID:      reservationID,
			ReservedTokens:     reservedTokens,
			AuthorizedRequests: newAuthReqs,
			BudgetRemaining: BudgetRemaining{
				Requests: maxReqs - newAuthReqs,
				Tokens:   maxToks - newTotToks,
			},
		}

		if idempotencyKey != "" {
			respBytes, _ := json.Marshal(resp)
			if err := core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
				Operation:     "POST",
				ResourceScope: scope,
				Key:           idempotencyKey,
				RequestDigest: digest,
				Status:        "committed",
				ResponseCode:  http.StatusOK,
				ResponseBody:  string(respBytes),
				CreatedAt:     nowMicro,
			}); err != nil {
				return fmt.Errorf("save idempotency: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}
	return resp, isReplay, nil
}

func (s *Service) SettleCall(ctx context.Context, reservationID string, req SettleRequest, nowMicro int64) (*SettleResponse, error) {
	computedDigest, err := ComputeSettlementDigest(req.Outcome, req.ProviderRequestID, req.ResponseSHA256, req.Usage)
	if err != nil {
		return nil, fmt.Errorf("compute settlement digest: %w", err)
	}

	var resp *SettleResponse

	err = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		// 1. Query reservation
		var jobID, itemID, ownerWorkerID string
		var fencingToken int64
		var reservedTokens, accountedTokens int
		var status string
		var existingDigest sql.NullString

		query := `
			SELECT job_id, item_id, owner_worker_id, fencing_token, reserved_tokens, accounted_tokens, status, settlement_digest
			FROM generation_call
			WHERE reservation_id = ?
		`
		err := tc.QueryRow(query, reservationID).Scan(
			&jobID, &itemID, &ownerWorkerID, &fencingToken, &reservedTokens, &accountedTokens, &status, &existingDigest,
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrReservationNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup reservation: %w", err)
		}

		// Verify caller matches reservation creation identity
		if itemID != req.ItemID || ownerWorkerID != req.WorkerID || fencingToken != req.FencingToken {
			return errors.New("caller identity does not match reservation")
		}

		// Also verify item input digest matches
		var itemInputDigest string
		err = tc.QueryRow("SELECT input_digest FROM generation_item WHERE id = ?", req.ItemID).Scan(&itemInputDigest)
		if err != nil {
			return fmt.Errorf("lookup item input digest: %w", err)
		}
		if itemInputDigest != req.InputDigest {
			return ErrInvalidInputDigest
		}

		// 2. Check current status and existing digest
		if status == "settled" {
			if existingDigest.Valid && existingDigest.String == computedDigest {
				// Idempotent replay of settled call
				var authReqs, totToks, overrun int
				_ = tc.QueryRow("SELECT authorized_requests, total_tokens, budget_overrun FROM generation_budget_ledger WHERE job_id = ?", jobID).
					Scan(&authReqs, &totToks, &overrun)
				resp = &SettleResponse{
					CallStatus:      status,
					AccountedTokens: accountedTokens,
					JobBudgetStatus: JobBudgetStatus{
						AuthorizedRequests: authReqs,
						TotalTokens:        totToks,
						BudgetOverrun:      overrun == 1,
					},
				}
				return nil
			}
			return ErrCallSettlementConflict
		}

		if status == "unknown" && req.Outcome == "unknown" {
			if existingDigest.Valid && existingDigest.String == computedDigest {
				// Idempotent replay of unknown outcome
				var authReqs, totToks, overrun int
				_ = tc.QueryRow("SELECT authorized_requests, total_tokens, budget_overrun FROM generation_budget_ledger WHERE job_id = ?", jobID).
					Scan(&authReqs, &totToks, &overrun)
				resp = &SettleResponse{
					CallStatus:      status,
					AccountedTokens: accountedTokens,
					JobBudgetStatus: JobBudgetStatus{
						AuthorizedRequests: authReqs,
						TotalTokens:        totToks,
						BudgetOverrun:      overrun == 1,
					},
				}
				return nil
			}
		}

		// 3. Calculate accounted tokens and adjust ledger
		newAccountedTokens := accountedTokens
		if newAccountedTokens <= 0 {
			newAccountedTokens = reservedTokens
		}
		newCallStatus := "settled"
		if req.Outcome == "unknown" {
			newCallStatus = "unknown"
		}

		var inputToks, outputToks sql.NullInt64
		if req.Usage != nil {
			inputToks = sql.NullInt64{Int64: int64(req.Usage.InputTokens), Valid: true}
			outputToks = sql.NullInt64{Int64: int64(req.Usage.OutputTokens), Valid: true}
			actualTokens := req.Usage.InputTokens + req.Usage.OutputTokens
			newAccountedTokens = actualTokens

			tokenDelta := actualTokens - accountedTokens

			// Query ledger
			var maxToks, totToks, authReqs, overrun int
			queryLedger := `SELECT max_tokens, total_tokens, authorized_requests, budget_overrun FROM generation_budget_ledger WHERE job_id = ?`
			if err := tc.QueryRow(queryLedger, jobID).Scan(&maxToks, &totToks, &authReqs, &overrun); err != nil {
				return fmt.Errorf("query budget ledger: %w", err)
			}

			newTotalTokens := totToks + tokenDelta
			newOverrun := overrun
			if newTotalTokens > maxToks {
				newOverrun = 1
			}

			updateLedger := `UPDATE generation_budget_ledger SET total_tokens = ?, budget_overrun = ?, updated_at = ? WHERE job_id = ?`
			if _, err := tc.Exec(updateLedger, newTotalTokens, newOverrun, nowMicro, jobID); err != nil {
				return fmt.Errorf("update budget ledger on settle: %w", err)
			}
		}

		var providerReqID, respSHA sql.NullString
		if req.ProviderRequestID != nil {
			providerReqID = sql.NullString{String: *req.ProviderRequestID, Valid: true}
		}
		if req.ResponseSHA256 != nil {
			respSHA = sql.NullString{String: *req.ResponseSHA256, Valid: true}
		}

		updateCall := `
			UPDATE generation_call
			SET status = ?, outcome = ?, provider_request_id = ?, response_sha256 = ?,
			    input_tokens = ?, output_tokens = ?, accounted_tokens = ?,
			    settlement_digest = ?, settled_at = ?, updated_at = ?
			WHERE reservation_id = ?
		`
		_, err = tc.Exec(updateCall,
			newCallStatus, req.Outcome, providerReqID, respSHA,
			inputToks, outputToks, newAccountedTokens,
			computedDigest, nowMicro, nowMicro,
			reservationID,
		)
		if err != nil {
			return fmt.Errorf("update call settlement: %w", err)
		}

		var finAuthReqs, finTotToks, finOverrun int
		_ = tc.QueryRow("SELECT authorized_requests, total_tokens, budget_overrun FROM generation_budget_ledger WHERE job_id = ?", jobID).
			Scan(&finAuthReqs, &finTotToks, &finOverrun)

		resp = &SettleResponse{
			CallStatus:      newCallStatus,
			AccountedTokens: newAccountedTokens,
			JobBudgetStatus: JobBudgetStatus{
				AuthorizedRequests: finAuthReqs,
				TotalTokens:        finTotToks,
				BudgetOverrun:      finOverrun == 1,
			},
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) SubmitCandidate(ctx context.Context, req SubmitCandidateRequest, idempotencyKey, digest string, nowMicro int64) (*SubmitCandidateResponse, bool, error) {
	if len(req.Content.Options) != 4 {
		return nil, false, fmt.Errorf("%w: must have exactly 4 options", ErrInvalidCandidateContent)
	}

	var resp *SubmitCandidateResponse
	var isReplay bool

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		scope := "/internal/v1/generation/candidates"
		if idempotencyKey != "" {
			rec, err := core.CheckIdempotency(tc, "POST", scope, idempotencyKey, digest)
			if err != nil {
				return err
			}
			if rec != nil {
				var cached SubmitCandidateResponse
				if err := json.Unmarshal([]byte(rec.ResponseBody), &cached); err == nil {
					resp = &cached
					isReplay = true
					return nil
				}
			}
		}

		// 1. Verify item lease
		var jobID, senseID, senseVersionID string
		var leaseOwner sql.NullString
		var leaseUntil sql.NullInt64
		var fencingToken int64
		var inputDigest string
		var status string

		queryItem := `
			SELECT job_id, sense_id, sense_version_id, lease_owner, lease_until, fencing_token, input_digest, status
			FROM generation_item
			WHERE id = ?
		`
		err := tc.QueryRow(queryItem, req.ItemID).Scan(&jobID, &senseID, &senseVersionID, &leaseOwner, &leaseUntil, &fencingToken, &inputDigest, &status)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup item: %w", err)
		}

		if status != "claimed" || !leaseOwner.Valid || leaseOwner.String != req.WorkerID ||
			fencingToken != req.FencingToken || inputDigest != req.InputDigest ||
			!leaseUntil.Valid || nowMicro > leaseUntil.Int64 {
			return ErrLeaseLost
		}

		// 2. Verify reservations in metadata
		verifyCall := func(reservationID, stage string) (int, string, error) {
			var evaluationIndex int
			var profile string
			err := tc.QueryRow(`SELECT evaluation_index, profile_version FROM generation_call
				WHERE reservation_id = ? AND item_id = ? AND stage = ?
				AND status = 'settled' AND outcome = 'response'
				AND owner_worker_id = ? AND fencing_token = ?
				AND attempt_number = ? AND candidate_index = ? AND input_sha256 <> ''`,
				reservationID, req.ItemID, stage, req.WorkerID, req.FencingToken,
				req.AttemptNumber, req.CandidateIndex).Scan(&evaluationIndex, &profile)
			if errors.Is(err, sql.ErrNoRows) {
				return 0, "", fmt.Errorf("%w: %s reservation does not prove a successful call for this candidate", ErrInvalidCandidateContent, stage)
			}
			return evaluationIndex, profile, err
		}
		if _, _, err := verifyCall(req.GenerationMetadata.GeneratorReservationID, "generator"); err != nil {
			return err
		}
		if len(req.GenerationMetadata.EvaluatorReservationIDs) != s.policy.SemanticEvaluations {
			return fmt.Errorf("%w: requires exactly %d evaluator reservations", ErrInvalidCandidateContent, s.policy.SemanticEvaluations)
		}
		evaluationIndexes := make(map[int]bool)
		for index, evalID := range req.GenerationMetadata.EvaluatorReservationIDs {
			evaluationIndex, profile, err := verifyCall(evalID, "evaluator")
			if err != nil {
				return err
			}
			if evaluationIndexes[evaluationIndex] {
				return fmt.Errorf("%w: evaluator calls must have distinct evaluation indexes", ErrInvalidCandidateContent)
			}
			evaluationIndexes[evaluationIndex] = true
			if index < len(req.Checks.SemanticEvaluations) && req.Checks.SemanticEvaluations[index].ProfileVersion != profile {
				return fmt.Errorf("%w: evaluator profile does not match reservation", ErrInvalidCandidateContent)
			}
		}

		// 3. Process Checks and Ratings
		allPass := true
		hasUncertain := false
		if req.Checks.Result != "pass" {
			allPass = false
		}
		if len(req.Checks.SemanticEvaluations) != s.policy.SemanticEvaluations {
			allPass = false
		}
		for _, eval := range req.Checks.SemanticEvaluations {
			for _, dimension := range []string{"target_meaning", "unique_answer", "grammar", "naturalness", "explanation_consistency", "distractor_validity"} {
				if eval.Ratings[dimension] != "pass" {
					allPass = false
				}
			}
			for _, rat := range eval.Ratings {
				if rat != "pass" {
					allPass = false
				}
				if rat == "uncertain" {
					hasUncertain = true
				}
			}
		}

		if req.Checks.RuleChecks != nil {
			for _, v := range req.Checks.RuleChecks {
				if b, ok := v.(bool); ok && !b {
					allPass = false
				}
			}
		}

		// Enforce structural rules
		if strings.TrimSpace(req.Content.Stem) == "" {
			allPass = false
		}
		if len(req.Content.Options) != 4 {
			allPass = false
		}
		correctCount := 0
		optIDs := make(map[string]bool)
		for _, o := range req.Content.Options {
			if o.ID != "" {
				if optIDs[o.ID] {
					allPass = false
				}
				optIDs[o.ID] = true
			}
			if o.IsCorrect {
				correctCount++
			}
		}
		if correctCount != 1 {
			allPass = false
		}

		// Check maintenance mode
		var maintMode string
		_ = tc.QueryRow("SELECT mode FROM projection_control WHERE id = 1").Scan(&maintMode)

		candidateID := core.NewUUID()
		contentJSON, _ := json.Marshal(req.Content)
		metaJSON, _ := json.Marshal(req.GenerationMetadata)
		checksJSON, _ := json.Marshal(req.Checks)

		var disposition string
		var publishedVersionID *string

		if maintMode == "rebuilding" {
			disposition = "pending_maintenance"
		} else if allPass {
			var optionKeys []string
			type optInfo struct {
				id           string
				identityID   string
				canonicalKey string
				text         string
				isCorrect    bool
				explanation  string
			}
			var processedOpts []optInfo
			var correctOptionID string

			for _, o := range req.Content.Options {
				optID := o.ID
				if optID == "" {
					optID = core.NewUUID()
				}
				if o.IsCorrect {
					correctOptionID = optID
				}

				var optSenseID, optSourceID, optSourceVer sql.NullString
				var srcIDStr, srcVerStr string
				if o.SenseID != "" {
					optSenseID = sql.NullString{String: o.SenseID, Valid: true}
					if err := tc.QueryRow(`
						SELECT s.source_id, src.source_version
						FROM sense s
						JOIN source src ON s.source_id = src.id
						WHERE s.id = ?
					`, o.SenseID).Scan(&srcIDStr, &srcVerStr); err == nil {
						optSourceID = sql.NullString{String: srcIDStr, Valid: true}
						optSourceVer = sql.NullString{String: srcVerStr, Valid: true}
					}
				}

				canonicalKey := content.ComputeOptionCanonicalKey(o.Language, o.Text, o.SenseID, srcIDStr, srcVerStr)
				optionKeys = append(optionKeys, canonicalKey)

				identityID := o.OptionIdentityID
				if identityID == "" {
					identityID = core.NewUUID()
				}

				_, err := tc.Exec(`
					INSERT INTO option_identity (id, canonical_key, language, normalized_text, sense_id, source_id, source_version, created_at)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT(canonical_key) DO UPDATE SET id=id
				`, identityID, canonicalKey, o.Language, content.NormalizeText(o.Text), optSenseID, optSourceID, optSourceVer, nowMicro)
				if err != nil {
					return fmt.Errorf("register option identity: %w", err)
				}

				var actualIdentityID string
				if err := tc.QueryRow("SELECT id FROM option_identity WHERE canonical_key = ?", canonicalKey).Scan(&actualIdentityID); err != nil {
					return fmt.Errorf("lookup registered option identity: %w", err)
				}

				processedOpts = append(processedOpts, optInfo{
					id:           optID,
					identityID:   actualIdentityID,
					canonicalKey: canonicalKey,
					text:         o.Text,
					isCorrect:    o.IsCorrect,
					explanation:  o.Explanation,
				})
			}

			if correctOptionID == "" {
				return errors.New("candidate has no correct option")
			}

			// Form deduplication
			dedupHash := content.ComputeFormDedupHash(senseID, req.Content.TaskFamily, req.Content.Stem, optionKeys)
			var formID string
			err = tc.QueryRow("SELECT id FROM question_form WHERE dedup_hash = ?", dedupHash).Scan(&formID)
			if errors.Is(err, sql.ErrNoRows) {
				formID = core.NewUUID()
				_, err = tc.Exec(`
					INSERT INTO question_form (id, sense_id, task_family, dedup_hash, normalized_stem, created_at)
					VALUES (?, ?, ?, ?, ?, ?)
				`, formID, senseID, req.Content.TaskFamily, dedupHash, content.NormalizeText(req.Content.Stem), nowMicro)
				if err != nil {
					return fmt.Errorf("insert question form: %w", err)
				}
			} else if err != nil {
				return fmt.Errorf("lookup question form: %w", err)
			}

			// Create question version
			qvID := core.NewUUID()
			optsToSave := make([]map[string]any, len(processedOpts))
			for i, po := range processedOpts {
				optsToSave[i] = map[string]any{
					"id":                 po.id,
					"text":               po.text,
					"option_identity_id": po.identityID,
					"is_correct":         po.isCorrect,
					"explanation":        po.explanation,
				}
			}
			optsJSON, _ := json.Marshal(optsToSave)

			insertQV := `
				INSERT INTO question_version (
					id, form_id, sense_id, sense_version_id, task_family, stem, options_json,
					correct_option_id, explanation, candidate_id, status, revision, created_at, updated_at
				) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'published', 0, ?, ?)
			`
			_, err = tc.Exec(insertQV,
				qvID, formID, senseID, senseVersionID, req.Content.TaskFamily, req.Content.Stem,
				string(optsJSON), correctOptionID, req.Content.Explanation, candidateID,
				nowMicro, nowMicro,
			)
			if err != nil {
				return fmt.Errorf("insert question version: %w", err)
			}

			disposition = "published"
			publishedVersionID = &qvID

			// Increment published count on job
			_, _ = tc.Exec("UPDATE generation_job SET published_count = published_count + 1, updated_at = ? WHERE id = ?", nowMicro, jobID)
		} else {
			if hasUncertain || req.Checks.Result == "uncertain" {
				disposition = "uncertain"
			} else {
				disposition = "rejected"
				_, _ = tc.Exec("UPDATE generation_job SET rejected_count = rejected_count + 1, updated_at = ? WHERE id = ?", nowMicro, jobID)
			}
		}

		// 5. Insert question_candidate
		insertCand := `
			INSERT INTO question_candidate (
				id, item_id, job_id, attempt_number, candidate_index, parent_candidate_id,
				content_json, generation_metadata_json, checks_json, disposition,
				published_version_id, revision, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		`
		var pubIDStr sql.NullString
		if publishedVersionID != nil {
			pubIDStr = sql.NullString{String: *publishedVersionID, Valid: true}
		}
		var parentCandStr sql.NullString
		if req.ParentCandidateID != nil {
			parentCandStr = sql.NullString{String: *req.ParentCandidateID, Valid: true}
		}

		_, err = tc.Exec(insertCand,
			candidateID, req.ItemID, jobID, req.AttemptNumber, req.CandidateIndex, parentCandStr,
			string(contentJSON), string(metaJSON), string(checksJSON), disposition,
			pubIDStr, nowMicro, nowMicro,
		)
		if err != nil {
			return fmt.Errorf("insert question_candidate: %w", err)
		}

		// 6. If uncertain, create quality_finding
		if disposition == "uncertain" {
			findingID := core.NewUUID()
			insertFinding := `
				INSERT INTO quality_finding (
					id, job_id, candidate_id, finding_type, details_json, checker_version, status, revision, created_at, updated_at
				) VALUES (?, ?, ?, 'uncertain_evaluation', ?, 'v1', 'open', 0, ?, ?)
			`
			_, err = tc.Exec(insertFinding, findingID, jobID, candidateID, string(checksJSON), nowMicro, nowMicro)
			if err != nil {
				return fmt.Errorf("insert quality_finding: %w", err)
			}
		}

		resp = &SubmitCandidateResponse{
			CandidateID:        candidateID,
			Disposition:        disposition,
			PublishedVersionID: publishedVersionID,
		}

		if idempotencyKey != "" {
			respBytes, _ := json.Marshal(resp)
			if err := core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
				Operation:     "POST",
				ResourceScope: scope,
				Key:           idempotencyKey,
				RequestDigest: digest,
				Status:        "committed",
				ResponseCode:  http.StatusOK,
				ResponseBody:  string(respBytes),
				CreatedAt:     nowMicro,
			}); err != nil {
				return fmt.Errorf("save idempotency: %w", err)
			}
		}

		return nil
	})

	if err != nil {
		return nil, false, err
	}
	return resp, isReplay, nil
}

func (s *Service) CompleteItem(ctx context.Context, req CompleteItemRequest, nowMicro int64) (*CompleteItemResponse, error) {
	switch req.Outcome {
	case "completed", "needs_attention", "budget_exhausted", "failed":
	default:
		return nil, fmt.Errorf("%w: invalid completion outcome", ErrInvalidCandidateContent)
	}
	var resp *CompleteItemResponse
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	digest, err := core.ComputeRequestDigest(body)
	if err != nil {
		return nil, err
	}
	scope := "/internal/v1/generation/complete/" + req.ItemID
	key := fmt.Sprintf("%s/%d", req.WorkerID, req.FencingToken)

	err = core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		rec, err := core.CheckIdempotency(tc, "POST", scope, key, digest)
		if err != nil {
			return err
		}
		if rec != nil {
			return json.Unmarshal([]byte(rec.ResponseBody), &resp)
		}
		var jobID string
		var leaseOwner sql.NullString
		var fencingToken int64
		var inputDigest string
		var status string
		var leaseUntil sql.NullInt64

		queryItem := `
			SELECT job_id, lease_owner, fencing_token, input_digest, status, lease_until
			FROM generation_item
			WHERE id = ?
		`
		err = tc.QueryRow(queryItem, req.ItemID).Scan(&jobID, &leaseOwner, &fencingToken, &inputDigest, &status, &leaseUntil)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrItemNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup item: %w", err)
		}

		if status != "claimed" || !leaseUntil.Valid || nowMicro >= leaseUntil.Int64 ||
			!leaseOwner.Valid || leaseOwner.String != req.WorkerID || fencingToken != req.FencingToken || inputDigest != req.InputDigest {
			return ErrLeaseLost
		}

		// Update item status
		_, err = tc.Exec("UPDATE generation_item SET status = ?, revision = revision + 1, updated_at = ? WHERE id = ?",
			req.Outcome, nowMicro, req.ItemID)
		if err != nil {
			return fmt.Errorf("update item status: %w", err)
		}

		// Update pending_count on job
		_, _ = tc.Exec("UPDATE generation_job SET pending_count = MAX(0, pending_count - 1), updated_at = ? WHERE id = ?", nowMicro, jobID)

		// Check if all items for this job are finished
		var activeCount int
		_ = tc.QueryRow("SELECT COUNT(*) FROM generation_item WHERE job_id = ? AND status IN ('pending', 'claimed')", jobID).Scan(&activeCount)

		var jobRev int
		_ = tc.QueryRow("SELECT revision FROM generation_job WHERE id = ?", jobID).Scan(&jobRev)

		if activeCount == 0 {
			var hasExhausted, hasNeedsAttention, hasFailed int
			_ = tc.QueryRow("SELECT COUNT(*) FROM generation_item WHERE job_id = ? AND status = 'budget_exhausted'", jobID).Scan(&hasExhausted)
			_ = tc.QueryRow("SELECT COUNT(*) FROM generation_item WHERE job_id = ? AND status = 'needs_attention'", jobID).Scan(&hasNeedsAttention)
			_ = tc.QueryRow("SELECT COUNT(*) FROM generation_item WHERE job_id = ? AND status = 'failed'", jobID).Scan(&hasFailed)

			finalStatus := "completed"
			if hasExhausted > 0 {
				finalStatus = "budget_exhausted"
			} else if hasNeedsAttention > 0 {
				finalStatus = "needs_attention"
			} else if hasFailed > 0 {
				finalStatus = "failed"
			}

			_, _ = tc.Exec("UPDATE generation_job SET status = ?, revision = revision + 1, updated_at = ? WHERE id = ?", finalStatus, nowMicro, jobID)
			jobRev++
		}

		resp = &CompleteItemResponse{
			ItemStatus: req.Outcome,
			Revision:   jobRev,
		}
		responseBody, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		return core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
			Operation: "POST", ResourceScope: scope, Key: key, RequestDigest: digest,
			Status: "completed", ResponseCode: 200, ResponseBody: string(responseBody), CreatedAt: nowMicro,
		})
	})

	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) GetJob(ctx context.Context, jobID string) (*JobSummary, error) {
	query := `
		SELECT j.id, j.bank_id, j.status, j.requested_count, j.published_count, j.rejected_count, j.pending_count,
		       j.revision, j.fixed_input_digest, j.generation_config_version, j.quality_policy_version, j.created_at, j.updated_at,
		       l.authorized_requests, l.max_requests, l.total_tokens, l.max_tokens, l.budget_overrun
		FROM generation_job j
		JOIN generation_budget_ledger l ON l.job_id = j.id
		WHERE j.id = ?
	`
	var job JobSummary
	var overrun int
	err := s.db.QueryRowContext(ctx, query, jobID).Scan(
		&job.ID, &job.BankID, &job.Status, &job.Requested, &job.Published, &job.Rejected, &job.Pending,
		&job.Revision, &job.FixedInputDigest, &job.GenerationConfigVersion, &job.QualityPolicyVersion, &job.CreatedAt, &job.UpdatedAt,
		&job.BudgetUsage.AuthorizedRequests, &job.BudgetUsage.MaxRequests,
		&job.BudgetUsage.TotalTokens, &job.BudgetUsage.MaxTokens, &overrun,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrJobNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query job: %w", err)
	}
	job.BudgetUsage.BudgetOverrun = (overrun == 1)
	return &job, nil
}

func (s *Service) GetCandidate(ctx context.Context, candidateID string) (*CandidateDetailResponse, error) {
	// Check active session boundary
	var activeSessionCount int
	_ = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM session WHERE status = 'open'").Scan(&activeSessionCount)
	if activeSessionCount > 0 {
		return nil, ErrSessionActive
	}

	query := `
		SELECT content_json, checks_json, disposition, revision
		FROM question_candidate
		WHERE id = ?
	`
	var contentJSON, checksJSON, disposition string
	var revision int
	err := s.db.QueryRowContext(ctx, query, candidateID).Scan(&contentJSON, &checksJSON, &disposition, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrCandidateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query candidate: %w", err)
	}

	return &CandidateDetailResponse{
		Candidate:   json.RawMessage(contentJSON),
		Checks:      json.RawMessage(checksJSON),
		Provenance:  map[string]any{"candidate_id": candidateID},
		Disposition: disposition,
		Revision:    revision,
	}, nil
}

func (s *Service) ListQualityFindings(ctx context.Context, jobID, candidateID, status string, limit int) (*QualityFindingsResponse, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	query := "SELECT id, job_id, candidate_id, finding_type, details_json, checker_version, status, resolution_id, revision, created_at, updated_at FROM quality_finding WHERE 1=1"
	var args []any

	if jobID != "" {
		query += " AND job_id = ?"
		args = append(args, jobID)
	}
	if candidateID != "" {
		query += " AND candidate_id = ?"
		args = append(args, candidateID)
	}
	if status != "" {
		query += " AND status = ?"
		args = append(args, status)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query quality findings: %w", err)
	}
	defer rows.Close()

	var items []QualityFindingItem
	for rows.Next() {
		var item QualityFindingItem
		var detailsStr string
		var resID sql.NullString
		if err := rows.Scan(
			&item.ID, &item.JobID, &item.CandidateID, &item.FindingType, &detailsStr,
			&item.CheckerVersion, &item.Status, &resID, &item.Revision, &item.CreatedAt, &item.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan quality finding: %w", err)
		}
		item.Details = json.RawMessage(detailsStr)
		if resID.Valid {
			item.ResolutionID = &resID.String
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quality findings: %w", err)
	}

	return &QualityFindingsResponse{Items: items}, nil
}

func (s *Service) ResolveFinding(ctx context.Context, findingID string, req ResolveFindingRequest, nowMicro int64) (*ResolveFindingResponse, error) {
	var resp *ResolveFindingResponse

	err := core.RunInTx(ctx, s.db, func(tc *core.TxnContext) error {
		var jobID, candidateID, status string
		var revision int
		err := tc.QueryRow("SELECT job_id, candidate_id, status, revision FROM quality_finding WHERE id = ?", findingID).
			Scan(&jobID, &candidateID, &status, &revision)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFindingNotFound
		}
		if err != nil {
			return fmt.Errorf("lookup finding: %w", err)
		}

		if revision != req.ExpectedRevision {
			return fmt.Errorf("finding revision conflict: expected %d, got %d", req.ExpectedRevision, revision)
		}

		resID := core.NewUUID()
		var contentJSONStr sql.NullString
		if req.Content != nil {
			b, _ := json.Marshal(req.Content)
			contentJSONStr = sql.NullString{String: string(b), Valid: true}
		}

		insertRes := `
			INSERT INTO quality_finding_resolution (id, finding_id, action, reason, content_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`
		_, err = tc.Exec(insertRes, resID, findingID, req.Action, req.Reason, contentJSONStr, nowMicro)
		if err != nil {
			return fmt.Errorf("insert finding resolution: %w", err)
		}

		newStatus := "resolved"
		_, err = tc.Exec("UPDATE quality_finding SET status = ?, resolution_id = ?, revision = revision + 1, updated_at = ? WHERE id = ?",
			newStatus, resID, nowMicro, findingID)
		if err != nil {
			return fmt.Errorf("update finding status: %w", err)
		}

		resp = &ResolveFindingResponse{
			ResolutionID: resID,
			Status:       newStatus,
			Revision:     revision + 1,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return resp, nil
}
