package practice

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
	"adaptive-vocabulary/internal/selection"
)

type Handler struct {
	db      *sql.DB
	service *Service
}

func NewHandler(db *sql.DB, policyPath string) (*Handler, error) {
	fsrsPolicy, err := learning.LoadFSRSPolicyFromPath(policyPath)
	if err != nil {
		// Try fallback candidates
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

	selCfg, err := selection.LoadSelectionConfigFromPath(policyPath)
	if err != nil {
		candidates := []string{
			policyPath,
			filepath.Join("..", policyPath),
			filepath.Join("..", "..", policyPath),
		}
		for _, c := range candidates {
			if sc, err2 := selection.LoadSelectionConfigFromPath(c); err2 == nil {
				selCfg = sc
				break
			}
		}
		if selCfg == nil {
			def := selection.DefaultSelectionConfig()
			selCfg = &def
		}
	}

	learningSvc := learning.NewLearningService(db, *fsrsPolicy, "observation-v2", "")
	selector := selection.NewSelector(db, *selCfg)
	svc := NewService(db, learningSvc, selector)

	return &Handler{
		db:      db,
		service: svc,
	}, nil
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

// RegisterRoutes registers all practice Web UI and REST API endpoints.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Web UI
	mux.HandleFunc("/practice", h.ServePracticePage)
	mux.HandleFunc("/static/practice/", h.ServeStaticPractice)

	// REST APIs
	mux.HandleFunc("/api/v1/sessions/active", h.HandleActiveSession)
	mux.HandleFunc("/api/v1/practice-sessions", h.HandlePracticeSessions)
	mux.HandleFunc("/api/v1/practice-sessions/", h.HandlePracticeSessionSubroutes)
}

func (h *Handler) ServeStaticPractice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	prefix := "/static/practice/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	fileName := strings.TrimPrefix(r.URL.Path, prefix)
	if strings.Contains(fileName, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	diskPath := findWebPath(filepath.Join("web/static/practice", fileName))
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

func (h *Handler) ServePracticePage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bankID := r.URL.Query().Get("bank_id")
	tmplPath := findWebPath("web/templates/practice/practice.html")
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		http.Error(w, "failed to load template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Query available banks for selection if needed
	type BankOption struct {
		ID          string
		Name        string
		MemberCount int
	}
	var banks []BankOption
	queryBanks := `
		SELECT b.id, b.name, count(bs.sense_id)
		FROM bank b
		LEFT JOIN bank_sense bs ON bs.bank_id = b.id
		WHERE b.archived = 0
		GROUP BY b.id
		ORDER BY b.created_at ASC
	`
	rows, err := h.db.QueryContext(r.Context(), queryBanks)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var b BankOption
			if err := rows.Scan(&b.ID, &b.Name, &b.MemberCount); err == nil {
				banks = append(banks, b)
			}
		}
	}

	data := map[string]any{
		"InitialBankID": bankID,
		"Banks":         banks,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = tmpl.Execute(w, data)
}

func (h *Handler) HandleActiveSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	sess, err := core.GetActiveSession(h.db)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)

	if sess == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session": nil,
		})
		return
	}

	sessionMap := map[string]any{
		"id":                sess.ID,
		"kind":              sess.Kind,
		"status":            sess.Status,
		"cursor":            sess.Cursor,
		"revision":          sess.Revision,
		"last_ack_sequence": sess.LastAckSequence,
		"created_at":        sess.CreatedAt,
		"updated_at":        sess.UpdatedAt,
	}
	if sess.BankID.Valid {
		sessionMap["bank_id"] = sess.BankID.String
	} else {
		sessionMap["bank_id"] = nil
	}
	if sess.NormalPosition.Valid {
		sessionMap["normal_position"] = sess.NormalPosition.Int64
	} else {
		sessionMap["normal_position"] = nil
	}
	if sess.DeadlineAt.Valid {
		sessionMap["deadline_at"] = sess.DeadlineAt.Int64
	} else {
		sessionMap["deadline_at"] = nil
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"session": sessionMap,
	})
}

func (h *Handler) HandlePracticeSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Failed to read request body", nil)
		return
	}

	digest := ""
	if idempotencyKey != "" {
		d, err := core.ComputeRequestDigest(bodyBytes)
		if err != nil {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON payload for idempotency", nil)
			return
		}
		digest = d
	}

	var req CreateSessionRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil || req.BankID == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "bank_id is required", nil)
		return
	}

	sess, isResumed, err := h.service.CreateOrResumeSession(r.Context(), req.BankID, idempotencyKey, digest)
	if err != nil {
		var activeErr *ActiveSessionConflictError
		if errors.As(err, &activeErr) {
			details := map[string]any{
				"session_id":   activeErr.ActiveSession.ID,
				"session_kind": activeErr.ActiveSession.Kind,
			}
			core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "已有活动会话", details)
			return
		}
		if errors.Is(err, ErrBankNotFound) {
			core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "题库不存在", nil)
			return
		}
		if errors.Is(err, ErrBankArchived) {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "该题库已归档，无法开始练习", nil)
			return
		}
		if errors.Is(err, ErrMaintenance) {
			core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "系统正在维护中", nil)
			return
		}
		if errors.Is(err, core.ErrIdempotencyConflict) {
			core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "幂等键请求摘要冲突", nil)
			return
		}
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	sessionMap := map[string]any{
		"id":                sess.ID,
		"kind":              sess.Kind,
		"status":            sess.Status,
		"cursor":            sess.Cursor,
		"revision":          sess.Revision,
		"last_ack_sequence": sess.LastAckSequence,
		"created_at":        sess.CreatedAt,
		"updated_at":        sess.UpdatedAt,
	}
	if sess.BankID.Valid {
		sessionMap["bank_id"] = sess.BankID.String
	} else {
		sessionMap["bank_id"] = nil
	}
	if sess.NormalPosition.Valid {
		sessionMap["normal_position"] = sess.NormalPosition.Int64
	} else {
		sessionMap["normal_position"] = nil
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if isResumed {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"session": sessionMap,
	})
}

func (h *Handler) HandlePracticeSessionSubroutes(w http.ResponseWriter, r *http.Request) {
	prefix := "/api/v1/practice-sessions/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, prefix)
	parts := strings.Split(rest, "/")
	sessionID := parts[0]
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}

	if len(parts) == 1 {
		// GET /api/v1/practice-sessions/{id}
		if r.Method == http.MethodGet {
			h.handleGetSession(w, r, sessionID)
			return
		}
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	subaction := parts[1]
	switch subaction {
	case "next":
		if r.Method == http.MethodPost {
			h.handleNextQuestion(w, r, sessionID)
			return
		}
	case "attempts":
		if r.Method == http.MethodPost {
			h.handleSubmitAttempt(w, r, sessionID)
			return
		}
	case "end":
		if r.Method == http.MethodPost {
			h.handleEndSession(w, r, sessionID, "ended")
			return
		}
	case "abandon":
		if r.Method == http.MethodPost {
			h.handleEndSession(w, r, sessionID, "abandoned")
			return
		}
	default:
		http.NotFound(w, r)
		return
	}

	core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
}

func (h *Handler) handleGetSession(w http.ResponseWriter, r *http.Request, sessionID string) {
	detail, err := h.service.GetSessionDetail(r.Context(), sessionID)
	if errors.Is(err, ErrSessionNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "会话不存在", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	sessionMap := map[string]any{
		"id":                detail.Session.ID,
		"kind":              detail.Session.Kind,
		"status":            detail.Session.Status,
		"cursor":            detail.Session.Cursor,
		"revision":          detail.Session.Revision,
		"last_ack_sequence": detail.Session.LastAckSequence,
		"created_at":        detail.Session.CreatedAt,
		"updated_at":        detail.Session.UpdatedAt,
	}
	if detail.Session.BankID.Valid {
		sessionMap["bank_id"] = detail.Session.BankID.String
	} else {
		sessionMap["bank_id"] = nil
	}
	if detail.Session.NormalPosition.Valid {
		sessionMap["normal_position"] = detail.Session.NormalPosition.Int64
	} else {
		sessionMap["normal_position"] = nil
	}

	resp := map[string]any{
		"session":          sessionMap,
		"current_question": detail.CurrentQuestion,
		"last_feedback":    detail.LastFeedback,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleNextQuestion(w http.ResponseWriter, r *http.Request, sessionID string) {
	var req NextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ExpectedCursor == nil {
		core.WriteJSONError(w, r, http.StatusPreconditionRequired, "PRECONDITION_REQUIRED", "expected_cursor 必填", nil)
		return
	}

	res, err := h.service.NextQuestion(r.Context(), sessionID, *req.ExpectedCursor)
	if err != nil {
		var cursorErr *CursorConflictError
		if errors.As(err, &cursorErr) {
			details := map[string]any{
				"cursor": cursorErr.CurrentCursor,
			}
			core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", "游标状态已变更", details)
			return
		}
		if errors.Is(err, ErrSessionNotFound) {
			core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "会话不存在", nil)
			return
		}
		if errors.Is(err, ErrSessionClosed) {
			details := map[string]any{
				"session_id":   sessionID,
				"session_kind": "practice",
			}
			core.WriteJSONError(w, r, http.StatusConflict, "SESSION_CLOSED", "会话已关闭", details)
			return
		}
		if errors.Is(err, ErrMaintenance) {
			core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "系统维护中", nil)
			return
		}
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) handleSubmitAttempt(w http.ResponseWriter, r *http.Request, sessionID string) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	testFault := r.Header.Get("X-Test-Fault")

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Failed to read request body", nil)
		return
	}

	digest := ""
	if idempotencyKey != "" {
		d, err := core.ComputeRequestDigest(bodyBytes)
		if err != nil {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON payload for idempotency", nil)
			return
		}
		digest = d
	}

	var req SubmitAttemptRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil || req.InstanceID == "" || req.OptionID == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "instance_id and option_id are required", nil)
		return
	}

	feedback, isReplayed, err := h.service.SubmitAttempt(r.Context(), sessionID, req, idempotencyKey, digest, testFault)
	if err != nil {
		var answeredErr *AlreadyAnsweredError
		if errors.As(err, &answeredErr) {
			details := map[string]any{
				"session_id": answeredErr.SessionID,
				"cursor":     answeredErr.Cursor,
			}
			core.WriteJSONError(w, r, http.StatusConflict, "ALREADY_ANSWERED", "该题目已作答，不能更改答案", details)
			return
		}
		var cursorErr *CursorConflictError
		if errors.As(err, &cursorErr) {
			details := map[string]any{
				"cursor": cursorErr.CurrentCursor,
			}
			core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", "游标状态冲突", details)
			return
		}
		if errors.Is(err, ErrSessionNotFound) {
			core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "会话不存在", nil)
			return
		}
		if errors.Is(err, ErrInstanceNotFound) {
			core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "题目实例不存在", nil)
			return
		}
		if errors.Is(err, ErrSessionClosed) {
			details := map[string]any{
				"session_id":   sessionID,
				"session_kind": "practice",
			}
			core.WriteJSONError(w, r, http.StatusConflict, "SESSION_CLOSED", "会话已关闭", details)
			return
		}
		if errors.Is(err, ErrInvalidOption) {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "选择项无效", nil)
			return
		}
		if errors.Is(err, ErrMaintenance) {
			core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "系统维护中", nil)
			return
		}
		if errors.Is(err, core.ErrIdempotencyConflict) {
			core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "幂等键请求摘要冲突", nil)
			return
		}
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if isReplayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(feedback)
}

func (h *Handler) handleEndSession(w http.ResponseWriter, r *http.Request, sessionID string, defaultReason string) {
	var req EndSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "JSON 请求无效", nil)
		return
	}
	if req.Reason == "" {
		req.Reason = defaultReason
	}
	if req.ExpectedRevision == nil {
		core.WriteJSONError(w, r, http.StatusPreconditionRequired, "PRECONDITION_REQUIRED", "expected_revision 必填", nil)
		return
	}

	body, _ := json.Marshal(req)
	digest, _ := core.ComputeRequestDigest(body)
	res, replayed, err := h.service.EndSessionIdempotent(r.Context(), sessionID, req.ExpectedRevision, req.Reason, r.URL.Path, r.Header.Get("Idempotency-Key"), digest)
	if err != nil {
		if errors.Is(err, core.ErrIdempotencyConflict) {
			core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "幂等键请求摘要冲突", nil)
			return
		}
		if errors.Is(err, ErrInvalidRequest) {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "结束原因无效", nil)
			return
		}
		if errors.Is(err, ErrMaintenance) {
			core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "系统维护中", nil)
			return
		}
		var revErr *RevisionConflictError
		if errors.As(err, &revErr) {
			details := map[string]any{
				"current_revision": revErr.CurrentRevision,
			}
			core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", "会话版本冲突", details)
			return
		}
		if errors.Is(err, ErrSessionNotFound) {
			core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "会话不存在", nil)
			return
		}
		if errors.Is(err, ErrSessionClosed) {
			details := map[string]any{
				"session_id":   sessionID,
				"session_kind": "practice",
			}
			core.WriteJSONError(w, r, http.StatusConflict, "SESSION_CLOSED", "会话已关闭", details)
			return
		}
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	sessionMap := map[string]any{
		"id":                res.Session.ID,
		"kind":              res.Session.Kind,
		"status":            res.Session.Status,
		"cursor":            res.Session.Cursor,
		"revision":          res.Session.Revision,
		"last_ack_sequence": res.Session.LastAckSequence,
		"created_at":        res.Session.CreatedAt,
		"updated_at":        res.Session.UpdatedAt,
	}
	if res.Session.BankID.Valid {
		sessionMap["bank_id"] = res.Session.BankID.String
	} else {
		sessionMap["bank_id"] = nil
	}
	if res.Session.NormalPosition.Valid {
		sessionMap["normal_position"] = res.Session.NormalPosition.Int64
	} else {
		sessionMap["normal_position"] = nil
	}

	resp := map[string]any{
		"session":               sessionMap,
		"unresolved_unit_count": res.UnresolvedUnitCount,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if replayed {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
