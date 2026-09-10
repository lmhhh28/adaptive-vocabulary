package exam

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"adaptive-vocabulary/internal/core"
	"adaptive-vocabulary/internal/learning"
)

type Handler struct {
	db      *sql.DB
	service *Service
}

func NewHandler(db *sql.DB, policyPath string) (*Handler, error) {
	fsrsPolicy, err := learning.LoadFSRSPolicyFromPath(policyPath)
	if err != nil {
		candidates := []string{
			policyPath,
			filepath.Join("..", policyPath),
			filepath.Join("..", "..", policyPath),
		}
		for _, c := range candidates {
			if p, err2 := learning.LoadFSRSPolicyFromPath(c); err2 == nil {
				fsrsPolicy = p
				break
			}
		}
		if fsrsPolicy == nil {
			return nil, fmt.Errorf("load fsrs policy from %s: %w", policyPath, err)
		}
	}

	examPolicy, err := LoadExamPolicyFromPath(policyPath)
	if err != nil {
		candidates := []string{
			policyPath,
			filepath.Join("..", policyPath),
			filepath.Join("..", "..", policyPath),
		}
		for _, c := range candidates {
			if p, err2 := LoadExamPolicyFromPath(c); err2 == nil {
				examPolicy = p
				break
			}
		}
		if examPolicy == nil {
			def := DefaultExamPolicyConfig()
			examPolicy = &def
		}
	}

	learningSvc := learning.NewLearningService(db, *fsrsPolicy, "observation-v2", "")
	assembler := NewAssembler(db, examPolicy.MaxSearchStates)
	svc := NewServiceWithPolicy(db, learningSvc, assembler, *examPolicy)

	return &Handler{
		db:      db,
		service: svc,
	}, nil
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

// RegisterRoutes registers all exam web pages and API endpoints on mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Static assets
	mux.HandleFunc("/static/exam/", h.ServeStaticExam)

	// Web UI
	mux.HandleFunc("/exams/new", h.ServeNewExamPage)
	mux.HandleFunc("/exams/", h.ServeExamPages)

	// REST APIs
	mux.HandleFunc("/api/v1/exams", h.HandleExamsRoot)
	mux.HandleFunc("/api/v1/exams/", h.HandleExamSubroutes)
}

func (h *Handler) ServeStaticExam(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	prefix := "/static/exam/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	fileName := strings.TrimPrefix(r.URL.Path, prefix)
	if strings.Contains(fileName, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	diskPath := findWebPath(filepath.Join("web/static/exam", fileName))
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

func (h *Handler) ServeNewExamPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tmplPath := findWebPath("web/templates/exam/exam.html")
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		http.Error(w, "failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Query available banks
	type BankOption struct {
		ID   string
		Name string
	}
	var banks []BankOption
	rows, err := h.db.QueryContext(r.Context(), "SELECT id, name FROM bank WHERE archived = 0 ORDER BY created_at ASC")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var b BankOption
			if err := rows.Scan(&b.ID, &b.Name); err == nil {
				banks = append(banks, b)
			}
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = tmpl.Execute(w, map[string]any{
		"Mode":  "new",
		"Banks": banks,
	})
}

func (h *Handler) ServeExamPages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/exams/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}

	examID := parts[0]
	if len(parts) == 1 {
		// Exam active view
		tmplPath := findWebPath("web/templates/exam/exam.html")
		tmpl, err := template.ParseFiles(tmplPath)
		if err != nil {
			http.Error(w, "failed to load template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = tmpl.Execute(w, map[string]any{
			"Mode":   "take",
			"ExamID": examID,
		})
		return
	}

	if len(parts) == 2 && parts[1] == "result" {
		// Exam result report
		tmplPath := findWebPath("web/templates/exam/result.html")
		tmpl, err := template.ParseFiles(tmplPath)
		if err != nil {
			http.Error(w, "failed to load template: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_ = tmpl.Execute(w, map[string]any{
			"ExamID": examID,
		})
		return
	}

	http.NotFound(w, r)
}

func (h *Handler) HandleExamsRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read body", nil)
		return
	}

	var req CreateExamRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body", nil)
		return
	}

	digest, _ := core.ComputeRequestDigest(bodyBytes)
	resp, isReplay, err := h.service.CreateExam(r.Context(), req, idempotencyKey, digest)
	if err != nil {
		h.writeExamError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if isReplay {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleExamSubroutes(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/exams/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found", nil)
		return
	}

	examID := parts[0]
	if len(parts) == 1 {
		// GET /api/v1/exams/{id}
		if r.Method == http.MethodGet {
			h.handleGetExam(w, r, examID)
			return
		}
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	sub := parts[1]
	switch sub {
	case "response-events":
		if len(parts) == 2 && r.Method == http.MethodPost {
			h.handlePostResponseEvent(w, r, examID)
			return
		}
		if len(parts) == 3 && r.Method == http.MethodGet {
			clientEventID := parts[2]
			h.handleGetResponseEventReceipt(w, r, examID, clientEventID)
			return
		}
	case "telemetry-gaps":
		if len(parts) == 2 && r.Method == http.MethodPost {
			h.handlePostTelemetryGaps(w, r, examID)
			return
		}
	case "submit":
		if len(parts) == 2 && r.Method == http.MethodPost {
			h.handleSubmitExam(w, r, examID)
			return
		}
	case "abandon":
		if len(parts) == 2 && r.Method == http.MethodPost {
			h.handleAbandonExam(w, r, examID)
			return
		}
	case "result":
		if len(parts) == 2 && r.Method == http.MethodGet {
			h.handleGetExamResult(w, r, examID)
			return
		}
	}

	core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "subroute not found", nil)
}

func (h *Handler) handleGetExam(w http.ResponseWriter, r *http.Request, examID string) {
	detail, err := h.service.GetExam(r.Context(), examID)
	if err != nil {
		h.writeExamError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(detail)
}

func (h *Handler) handlePostResponseEvent(w http.ResponseWriter, r *http.Request, examID string) {
	idempotencyKey := r.Header.Get("Idempotency-Key")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read body", nil)
		return
	}

	var req ResponseEventRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body", nil)
		return
	}

	if idempotencyKey != "" && req.ClientEventID != "" && idempotencyKey != req.ClientEventID {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header must match client_event_id in body", nil)
		return
	}
	if idempotencyKey == "" {
		idempotencyKey = req.ClientEventID
	}

	digest, _ := core.ComputeRequestDigest(bodyBytes)
	resp, isReplay, err := h.service.RecordResponseEvent(r.Context(), examID, req, idempotencyKey, digest)
	if err != nil {
		h.writeExamError(w, r, err)
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

func (h *Handler) handleGetResponseEventReceipt(w http.ResponseWriter, r *http.Request, examID, clientEventID string) {
	receipt, err := h.service.GetResponseEventReceipt(r.Context(), examID, clientEventID)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "receipt not found", nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(receipt)
}

func (h *Handler) handlePostTelemetryGaps(w http.ResponseWriter, r *http.Request, examID string) {
	idempotencyKey := r.Header.Get("Idempotency-Key")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read body", nil)
		return
	}

	var req TelemetryGapsRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body", nil)
		return
	}

	digest, _ := core.ComputeRequestDigest(bodyBytes)
	resp, err := h.service.RecordTelemetryGap(r.Context(), examID, req, idempotencyKey, digest)
	if err != nil {
		h.writeExamError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleSubmitExam(w http.ResponseWriter, r *http.Request, examID string) {
	idempotencyKey := r.Header.Get("Idempotency-Key")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read body", nil)
		return
	}

	var req SubmitExamRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body", nil)
		return
	}

	if idempotencyKey != "" && req.SubmissionID != "" && idempotencyKey != req.SubmissionID {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header must match submission_id in body", nil)
		return
	}
	if idempotencyKey == "" {
		idempotencyKey = req.SubmissionID
	}

	digest, _ := core.ComputeRequestDigest(bodyBytes)
	resp, isReplay, err := h.service.SubmitExam(r.Context(), examID, req, idempotencyKey, digest)
	if err != nil {
		h.writeExamError(w, r, err)
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

func (h *Handler) handleAbandonExam(w http.ResponseWriter, r *http.Request, examID string) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body", nil)
		return
	}
	if len(bodyBytes) == 0 {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "request body cannot be empty", nil)
		return
	}
	var raw struct {
		ExpectedRevision *int64 `json:"expected_revision"`
	}
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON request body", nil)
		return
	}
	if raw.ExpectedRevision == nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "expected_revision is required", nil)
		return
	}

	req := AbandonExamRequest{
		ExpectedRevision: *raw.ExpectedRevision,
	}

	resp, err := h.service.AbandonExam(r.Context(), examID, req)
	if err != nil {
		h.writeExamError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleGetExamResult(w http.ResponseWriter, r *http.Request, examID string) {
	res, err := h.service.GetExamResult(r.Context(), examID)
	if err != nil {
		h.writeExamError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) writeExamError(w http.ResponseWriter, r *http.Request, err error) {
	var activeErr *ActiveSessionConflictError
	if errors.As(err, &activeErr) {
		core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", activeErr.Error(), map[string]any{
			"session_id":   activeErr.ActiveSession.ID,
			"session_kind": activeErr.ActiveSession.Kind,
		})
		return
	}

	var revErr *RevisionConflictNotAcceptedError
	if errors.As(err, &revErr) {
		core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", revErr.Error(), map[string]any{
			"current_revision":  revErr.CurrentRevision,
			"last_ack_sequence": revErr.LastAckSequence,
			"acceptance":        "not_accepted",
		})
		return
	}

	var seqErr *SequenceConflictNotAcceptedError
	if errors.As(err, &seqErr) {
		core.WriteJSONError(w, r, http.StatusConflict, "EVENT_SEQUENCE_CONFLICT", seqErr.Error(), map[string]any{
			"current_revision":  seqErr.CurrentRevision,
			"last_ack_sequence": seqErr.LastAckSequence,
			"acceptance":        "not_accepted",
		})
		return
	}

	var gapErr *SequenceGapNotAcceptedError
	if errors.As(err, &gapErr) {
		core.WriteJSONError(w, r, http.StatusConflict, "EVENT_SEQUENCE_GAP", gapErr.Error(), map[string]any{
			"expected_next_sequence": gapErr.ExpectedNextSequence,
			"last_ack_sequence":      gapErr.LastAckSequence,
			"acceptance":             "not_accepted",
		})
		return
	}

	if errors.Is(err, core.ErrIdempotencyConflict) {
		core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key reused with different request payload", nil)
		return
	}

	if errors.Is(err, ErrConstraintUnsatisfied) {
		core.WriteJSONError(w, r, http.StatusUnprocessableEntity, "EXAM_CONSTRAINT_UNSATISFIED", "Exam constraints could not be satisfied with the available bank questions", nil)
		return
	}

	if errors.Is(err, ErrAssemblyLimitReached) {
		core.WriteJSONError(w, r, http.StatusUnprocessableEntity, "ASSEMBLY_LIMIT_REACHED", "Assembly search limit reached", nil)
		return
	}

	if errors.Is(err, ErrInvalidQuotas) {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	if errors.Is(err, ErrBankNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Bank not found", nil)
		return
	}

	if errors.Is(err, ErrExamNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Exam session not found", nil)
		return
	}

	if errors.Is(err, ErrInstanceNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Question instance not found in this exam", nil)
		return
	}

	if errors.Is(err, ErrBankArchived) {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Bank is archived", nil)
		return
	}

	if errors.Is(err, ErrSessionClosed) {
		core.WriteJSONError(w, r, http.StatusConflict, "SESSION_CLOSED", "Exam session is closed", nil)
		return
	}

	if errors.Is(err, ErrExamSubmittedDeadline) {
		core.WriteJSONError(w, r, http.StatusConflict, "EXAM_SUBMITTED", "Exam has been submitted due to deadline expiration", nil)
		return
	}

	if errors.Is(err, ErrExamNotSubmitted) {
		core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "Exam has not been submitted yet", nil)
		return
	}

	if errors.Is(err, ErrMaintenance) {
		core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "System under maintenance", nil)
		return
	}

	if errors.Is(err, ErrPreconditionRequired) {
		core.WriteJSONError(w, r, http.StatusPreconditionRequired, "PRECONDITION_REQUIRED", err.Error(), nil)
		return
	}

	if errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrInvalidOption) {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}
