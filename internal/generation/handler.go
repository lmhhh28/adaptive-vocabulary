package generation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"adaptive-vocabulary/internal/core"
)

type Handler struct {
	db          *sql.DB
	service     *Service
	workerToken string
}

func NewHandler(db *sql.DB, policy PolicyGenerationConfig, workerToken string) *Handler {
	return &Handler{
		db:          db,
		service:     NewService(db, policy),
		workerToken: workerToken,
	}
}

func (h *Handler) Service() *Service {
	return h.service
}

func findWebPath(relPath string) string {
	candidates := []string{
		relPath,
		filepath.Join("..", relPath),
		filepath.Join("..", "..", relPath),
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates,
			filepath.Join(filepath.Dir(exe), relPath),
			filepath.Join(filepath.Dir(exe), "..", relPath),
		)
	}
	for _, c := range candidates {
		if abs, err := filepath.Abs(c); err == nil {
			if info, err := os.Stat(abs); err == nil && !info.IsDir() {
				return abs
			}
		}
	}
	return relPath
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Static assets
	mux.HandleFunc("/static/generation/", h.ServeStaticGeneration)

	// Web UI
	mux.HandleFunc("GET /generation", h.ServeGenerationUI)

	// Worker internal endpoints (require worker bearer token)
	mux.HandleFunc("POST /internal/v1/generation/claim", h.HandleClaim)
	mux.HandleFunc("POST /internal/v1/generation/renew", h.HandleRenew)
	mux.HandleFunc("POST /internal/v1/generation/calls/reserve", h.HandleReserve)
	mux.HandleFunc("POST /internal/v1/generation/calls/{id}/settle", h.HandleSettle)
	mux.HandleFunc("POST /internal/v1/generation/candidates", h.HandleCandidates)
	mux.HandleFunc("POST /internal/v1/generation/complete", h.HandleComplete)

	// Public generation API endpoints
	mux.HandleFunc("POST /api/v1/banks/{id}/generation-jobs", h.HandleCreateBankJob)
	mux.HandleFunc("GET /api/v1/generation-jobs/{id}", h.HandleGetJob)
	mux.HandleFunc("GET /api/v1/question-candidates/{id}", h.HandleGetCandidate)
	mux.HandleFunc("GET /api/v1/quality-findings", h.HandleListQualityFindings)
	mux.HandleFunc("POST /api/v1/quality-findings/{id}/resolutions", h.HandleResolveQualityFinding)
}

func (h *Handler) verifyWorkerAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	token := strings.TrimPrefix(auth, "Bearer ")
	return h.workerToken != "" && token == h.workerToken
}

// -------------------------------------------------------------------------------------------------
// Static Assets & UI
// -------------------------------------------------------------------------------------------------

func (h *Handler) ServeStaticGeneration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	prefix := "/static/generation/"
	fileName := strings.TrimPrefix(r.URL.Path, prefix)
	if strings.Contains(fileName, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	diskPath := findWebPath(filepath.Join("web/static/generation", fileName))
	content, err := os.ReadFile(diskPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if strings.HasSuffix(fileName, ".css") {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	} else if strings.HasSuffix(fileName, ".js") {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (h *Handler) ServeGenerationUI(w http.ResponseWriter, r *http.Request) {
	tmplPath := findWebPath("web/templates/generation/index.html")
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		http.Error(w, "failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Query jobs
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT j.id, b.name, j.status, j.requested_count, j.published_count, j.rejected_count, j.pending_count,
		       l.authorized_requests, l.max_requests, l.total_tokens, l.max_tokens, l.budget_overrun,
		       j.created_at, j.updated_at
		FROM generation_job j
		JOIN bank b ON b.id = j.bank_id
		JOIN generation_budget_ledger l ON l.job_id = j.id
		ORDER BY j.created_at DESC
		LIMIT 20
	`)
	type JobRow struct {
		ID                 string
		BankName           string
		Status             string
		Requested          int
		Published          int
		Rejected           int
		Pending            int
		AuthorizedRequests int
		MaxRequests        int
		TotalTokens        int
		MaxTokens          int
		BudgetOverrun      bool
		CreatedAtStr       string
	}
	var jobs []JobRow
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var jr JobRow
			var overrun int
			var cAt, uAt int64
			_ = rows.Scan(
				&jr.ID, &jr.BankName, &jr.Status, &jr.Requested, &jr.Published, &jr.Rejected, &jr.Pending,
				&jr.AuthorizedRequests, &jr.MaxRequests, &jr.TotalTokens, &jr.MaxTokens, &overrun,
				&cAt, &uAt,
			)
			jr.BudgetOverrun = (overrun == 1)
			jr.CreatedAtStr = time.UnixMicro(cAt).UTC().Format(time.RFC3339)
			jobs = append(jobs, jr)
		}
	}

	data := map[string]any{
		"Jobs": jobs,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = tmpl.Execute(w, data)
}

// -------------------------------------------------------------------------------------------------
// Worker Internal Endpoints
// -------------------------------------------------------------------------------------------------

func (h *Handler) HandleClaim(w http.ResponseWriter, r *http.Request) {
	if !h.verifyWorkerAuth(r) {
		core.WriteJSONError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "worker authorization required", nil)
		return
	}

	var req ClaimRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json: "+err.Error(), nil)
		return
	}

	now := time.Now().UTC().UnixMicro()
	resp, err := h.service.ClaimItems(r.Context(), req, now)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleRenew(w http.ResponseWriter, r *http.Request) {
	if !h.verifyWorkerAuth(r) {
		core.WriteJSONError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "worker authorization required", nil)
		return
	}

	var req RenewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json: "+err.Error(), nil)
		return
	}

	now := time.Now().UTC().UnixMicro()
	resp, err := h.service.RenewLease(r.Context(), req, now)
	if errors.Is(err, ErrLeaseLost) {
		core.WriteJSONError(w, r, http.StatusConflict, "LEASE_LOST", "lease expired or superseded by newer fencing token", nil)
		return
	}
	if errors.Is(err, ErrItemNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "item not found", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleReserve(w http.ResponseWriter, r *http.Request) {
	if !h.verifyWorkerAuth(r) {
		core.WriteJSONError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "worker authorization required", nil)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read body", nil)
		return
	}

	var req ReserveRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json: "+err.Error(), nil)
		return
	}

	var digest string
	if idempotencyKey != "" {
		d, err := core.ComputeRequestDigest(bodyBytes)
		if err != nil {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json digest: "+err.Error(), nil)
			return
		}
		digest = d
	}

	now := time.Now().UTC().UnixMicro()
	resp, isReplay, err := h.service.ReserveCall(r.Context(), req, idempotencyKey, digest, now)
	if errors.Is(err, core.ErrIdempotencyConflict) {
		core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key reused with differing request digest", nil)
		return
	}
	if errors.Is(err, ErrLeaseLost) {
		core.WriteJSONError(w, r, http.StatusConflict, "LEASE_LOST", "lease expired or fencing token superseded", nil)
		return
	}
	if errors.Is(err, ErrCallAttemptsExhausted) {
		core.WriteJSONError(w, r, http.StatusConflict, "CALL_ATTEMPTS_EXHAUSTED", "network attempts limit reached for logical call", nil)
		return
	}
	if errors.Is(err, ErrBudgetExhausted) {
		core.WriteJSONError(w, r, http.StatusConflict, "BUDGET_EXHAUSTED", "budget exhausted for generation job", nil)
		return
	}
	if errors.Is(err, ErrItemNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "item not found", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if isReplay {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleSettle(w http.ResponseWriter, r *http.Request) {
	if !h.verifyWorkerAuth(r) {
		core.WriteJSONError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "worker authorization required", nil)
		return
	}

	reservationID := r.PathValue("id")
	if reservationID == "" {
		parts := strings.Split(r.URL.Path, "/")
		for i, p := range parts {
			if p == "calls" && i+1 < len(parts) {
				reservationID = parts[i+1]
				break
			}
		}
	}
	if reservationID == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "missing reservation_id", nil)
		return
	}

	var req SettleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json: "+err.Error(), nil)
		return
	}

	now := time.Now().UTC().UnixMicro()
	resp, err := h.service.SettleCall(r.Context(), reservationID, req, now)
	if errors.Is(err, ErrReservationNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "reservation not found", nil)
		return
	}
	if errors.Is(err, ErrCallSettlementConflict) {
		core.WriteJSONError(w, r, http.StatusConflict, "CALL_SETTLEMENT_CONFLICT", "conflicting settlement digest for already settled reservation", nil)
		return
	}
	if errors.Is(err, ErrInvalidInputDigest) {
		core.WriteJSONError(w, r, http.StatusConflict, "INPUT_VERSION_CHANGED", "input digest does not match item", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleCandidates(w http.ResponseWriter, r *http.Request) {
	if !h.verifyWorkerAuth(r) {
		core.WriteJSONError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "worker authorization required", nil)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read body", nil)
		return
	}

	var req SubmitCandidateRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json: "+err.Error(), nil)
		return
	}

	var digest string
	if idempotencyKey != "" {
		d, err := core.ComputeRequestDigest(bodyBytes)
		if err != nil {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json digest: "+err.Error(), nil)
			return
		}
		digest = d
	}

	now := time.Now().UTC().UnixMicro()
	resp, isReplay, err := h.service.SubmitCandidate(r.Context(), req, idempotencyKey, digest, now)
	if errors.Is(err, core.ErrIdempotencyConflict) {
		core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key reused with differing request digest", nil)
		return
	}
	if errors.Is(err, ErrLeaseLost) {
		core.WriteJSONError(w, r, http.StatusConflict, "LEASE_LOST", "lease expired or fencing token superseded", nil)
		return
	}
	if errors.Is(err, ErrItemNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "item not found", nil)
		return
	}
	if errors.Is(err, ErrInvalidCandidateContent) {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if isReplay {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleComplete(w http.ResponseWriter, r *http.Request) {
	if !h.verifyWorkerAuth(r) {
		core.WriteJSONError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "worker authorization required", nil)
		return
	}

	var req CompleteItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json: "+err.Error(), nil)
		return
	}

	now := time.Now().UTC().UnixMicro()
	resp, err := h.service.CompleteItem(r.Context(), req, now)
	if errors.Is(err, ErrLeaseLost) {
		core.WriteJSONError(w, r, http.StatusConflict, "LEASE_LOST", "lease expired or fencing token superseded", nil)
		return
	}
	if errors.Is(err, ErrItemNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "item not found", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// -------------------------------------------------------------------------------------------------
// Public Management Endpoints
// -------------------------------------------------------------------------------------------------

func (h *Handler) HandleCreateBankJob(w http.ResponseWriter, r *http.Request) {
	bankID := r.PathValue("id")
	if bankID == "" {
		parts := strings.Split(r.URL.Path, "/")
		for i, p := range parts {
			if p == "banks" && i+1 < len(parts) {
				bankID = parts[i+1]
				break
			}
		}
	}
	if bankID == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "missing bank_id", nil)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header is required", nil)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read body", nil)
		return
	}
	digest, err := core.ComputeRequestDigest(bodyBytes)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json: "+err.Error(), nil)
		return
	}

	var req CreateJobRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json payload", nil)
		return
	}

	now := time.Now().UTC().UnixMicro()
	res, isReplay, err := h.service.CreateJob(r.Context(), bankID, req, idempotencyKey, digest, now)
	if errors.Is(err, core.ErrIdempotencyConflict) {
		core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "idempotency key reused with differing request digest", nil)
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "bank not found") {
			core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
			return
		}
		if strings.Contains(err.Error(), "revision conflict") {
			core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", err.Error(), nil)
			return
		}
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if isReplay {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) HandleGetJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if jobID == "" {
		parts := strings.Split(r.URL.Path, "/")
		jobID = parts[len(parts)-1]
	}

	job, err := h.service.GetJob(r.Context(), jobID)
	if errors.Is(err, ErrJobNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "job not found", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(job)
}

func (h *Handler) HandleGetCandidate(w http.ResponseWriter, r *http.Request) {
	candID := r.PathValue("id")
	if candID == "" {
		parts := strings.Split(r.URL.Path, "/")
		candID = parts[len(parts)-1]
	}

	cand, err := h.service.GetCandidate(r.Context(), candID)
	if errors.Is(err, ErrSessionActive) {
		core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "active session in progress; content details locked", nil)
		return
	}
	if errors.Is(err, ErrCandidateNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "candidate not found", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(cand)
}

func (h *Handler) HandleListQualityFindings(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("job_id")
	candidateID := r.URL.Query().Get("candidate_id")
	status := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil {
			limit = l
		}
	}

	res, err := h.service.ListQualityFindings(r.Context(), jobID, candidateID, status, limit)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) HandleResolveQualityFinding(w http.ResponseWriter, r *http.Request) {
	findingID := r.PathValue("id")
	if findingID == "" {
		parts := strings.Split(r.URL.Path, "/")
		for i, p := range parts {
			if p == "quality-findings" && i+1 < len(parts) {
				findingID = parts[i+1]
				break
			}
		}
	}

	var req ResolveFindingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid json: "+err.Error(), nil)
		return
	}

	now := time.Now().UTC().UnixMicro()
	res, err := h.service.ResolveFinding(r.Context(), findingID, req, now)
	if errors.Is(err, ErrFindingNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "quality finding not found", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(res)
}
