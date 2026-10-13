package bank

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"adaptive-vocabulary/internal/content"
	"adaptive-vocabulary/internal/core"
)

type Handler struct {
	db             *sql.DB
	service        *Service
	questionService *content.QuestionService
}

func NewHandler(db *sql.DB) *Handler {
	return &Handler{
		db:              db,
		service:         NewService(db),
		questionService: content.NewQuestionService(db),
	}
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

// RegisterRoutes mounts the bank and content routes onto the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Web UI
	mux.HandleFunc("/banks", h.ServeBanksPage)
	mux.HandleFunc("/banks/", h.ServeBankDetailPage)
	mux.HandleFunc("/static/content/", h.ServeStaticContent)

	// REST API v1
	mux.HandleFunc("/api/v1/sources", h.HandleSources)
	mux.HandleFunc("/api/v1/sources/", h.HandleSourceByID)
	mux.HandleFunc("/api/v1/banks", h.HandleBanks)
	mux.HandleFunc("/api/v1/banks/", h.HandleBankSubroutes)
	mux.HandleFunc("/api/v1/content/sense-versions", h.HandleSenseVersions)
	mux.HandleFunc("/api/v1/content/question-versions", h.HandleQuestionVersions)
	mux.HandleFunc("/api/v1/test/questions", h.HandleTestQuestions)
}

func (h *Handler) HandleTestQuestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	var draft content.QuestionDraft
	if err := json.NewDecoder(r.Body).Decode(&draft); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid question draft JSON", nil)
		return
	}

	validTasks := []string{"meaning_recognition", "reverse_selection", "context_selection"}
	version, err := h.questionService.CreateQuestionVersion(r.Context(), &draft, validTasks)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"version_id":  version.ID,
		"form_id":     version.FormID,
		"task_family": version.TaskFamily,
		"stem":        version.Stem,
	})
}

func (h *Handler) ServeStaticContent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	prefix := "/static/content/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	fileName := strings.TrimPrefix(r.URL.Path, prefix)
	if strings.Contains(fileName, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	diskPath := findWebPath(filepath.Join("web/static/content", fileName))
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

func (h *Handler) ServeBanksPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/banks" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	distIndex := findWebPath("web/frontend/dist/index.html")
	if content, err := os.ReadFile(distIndex); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(content)
		return
	}

	banks, err := h.service.ListBanks(r.Context())
	if err != nil {
		http.Error(w, "failed to list banks", http.StatusInternalServerError)
		return
	}

	tmplPath := findWebPath("web/templates/content/banks.html")
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		http.Error(w, "failed to load template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = tmpl.Execute(w, map[string]any{
		"Banks": banks,
	})
}

func (h *Handler) ServeBankDetailPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimPrefix(r.URL.Path, "/banks/")
	if id == "" || strings.Contains(id, "/") {
		http.NotFound(w, r)
		return
	}

	distIndex := findWebPath("web/frontend/dist/index.html")
	if content, err := os.ReadFile(distIndex); err == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(content)
		return
	}

	bankDetail, err := h.service.GetBank(r.Context(), id)
	if errors.Is(err, ErrBankNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "failed to get bank details", http.StatusInternalServerError)
		return
	}

	tmplPath := findWebPath("web/templates/content/bank_detail.html")
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		http.Error(w, "failed to load template", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = tmpl.Execute(w, map[string]any{
		"Bank": bankDetail,
	})
}

func (h *Handler) HandleSources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listSources(w, r)
	case http.MethodPost:
		h.createSource(w, r)
	default:
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
	}
}

func (h *Handler) listSources(w http.ResponseWriter, r *http.Request) {
	rows, err := h.db.QueryContext(r.Context(), `
		SELECT id, name, location, source_version, license, usage_scope, checksum, revision, created_at, updated_at
		FROM source
		ORDER BY created_at ASC
	`)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, name, loc, ver, lic, scope string
		var checksum sql.NullString
		var rev, cat, uat int64
		if err := rows.Scan(&id, &name, &loc, &ver, &lic, &scope, &checksum, &rev, &cat, &uat); err == nil {
			item := map[string]any{
				"id":             id,
				"name":           name,
				"location":       loc,
				"source_version": ver,
				"license":        lic,
				"usage_scope":    scope,
				"revision":       rev,
				"created_at":     cat,
				"updated_at":     uat,
			}
			if checksum.Valid {
				item["checksum"] = checksum.String
			}
			items = append(items, item)
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": items,
	})
}

func (h *Handler) createSource(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header is required", nil)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Failed to read body", nil)
		return
	}
	digest, err := core.ComputeRequestDigest(bodyBytes)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON body", nil)
		return
	}

	var req struct {
		Name          string  `json:"name"`
		Location      string  `json:"location"`
		SourceVersion string  `json:"source_version"`
		License       string  `json:"license"`
		UsageScope    string  `json:"usage_scope"`
		Checksum      *string `json:"checksum,omitempty"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON payload", nil)
		return
	}
	if req.Name == "" || req.Location == "" || req.SourceVersion == "" || req.License == "" || req.UsageScope == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Missing required source fields", nil)
		return
	}

	sourceID := core.NewUUID()
	now := time.Now().UTC().UnixMicro()

	var respBody string
	var respCode int

	txErr := core.RunInTx(r.Context(), h.db, func(tc *core.TxnContext) error {
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrUnderMaintenance
		}

		rec, err := core.CheckIdempotency(tc, "POST", "/api/v1/sources", idempotencyKey, digest)
		if errors.Is(err, core.ErrIdempotencyConflict) {
			return err
		}
		if rec != nil {
			respBody = rec.ResponseBody
			respCode = rec.ResponseCode
			return nil
		}

		_, err = tc.Exec(`
			INSERT INTO source (id, name, location, source_version, license, usage_scope, checksum, revision, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		`, sourceID, req.Name, req.Location, req.SourceVersion, req.License, req.UsageScope, req.Checksum, now, now)
		if err != nil {
			return err
		}

		outJSON, _ := json.Marshal(map[string]any{
			"source_id": sourceID,
			"revision":  0,
		})
		respBody = string(outJSON)
		respCode = http.StatusCreated

		return core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
			Operation:     "POST",
			ResourceScope: "/api/v1/sources",
			Key:           idempotencyKey,
			RequestDigest: digest,
			Status:        "committed",
			ResponseCode:  respCode,
			ResponseBody:  respBody,
			CreatedAt:     now,
		})
	})

	if errors.Is(txErr, core.ErrIdempotencyConflict) {
		core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key reused with different body", nil)
		return
	}
	if errors.Is(txErr, ErrUnderMaintenance) {
		core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "System under maintenance", nil)
		return
	}
	if txErr != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", txErr.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(respCode)
	_, _ = w.Write([]byte(respBody))
}

func (h *Handler) HandleSourceByID(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimPrefix(r.URL.Path, "/api/v1/sources/")
	if sourceID == "" || strings.Contains(sourceID, "/") {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Source not found", nil)
		return
	}

	var s struct {
		ID            string         `json:"id"`
		Name          string         `json:"name"`
		Location      string         `json:"location"`
		SourceVersion string         `json:"source_version"`
		License       string         `json:"license"`
		UsageScope    string         `json:"usage_scope"`
		Checksum      sql.NullString `json:"-"`
		Revision      int64          `json:"revision"`
		CreatedAt     int64          `json:"created_at"`
		UpdatedAt     int64          `json:"updated_at"`
	}

	err := h.db.QueryRowContext(r.Context(), `
		SELECT id, name, location, source_version, license, usage_scope, checksum, revision, created_at, updated_at
		FROM source
		WHERE id = ?
	`, sourceID).Scan(&s.ID, &s.Name, &s.Location, &s.SourceVersion, &s.License, &s.UsageScope, &s.Checksum, &s.Revision, &s.CreatedAt, &s.UpdatedAt)

	if errors.Is(err, sql.ErrNoRows) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Source not found", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	res := map[string]any{
		"source": map[string]any{
			"id":             s.ID,
			"name":           s.Name,
			"location":       s.Location,
			"source_version": s.SourceVersion,
			"license":        s.License,
			"usage_scope":    s.UsageScope,
			"revision":       s.Revision,
			"created_at":     s.CreatedAt,
			"updated_at":     s.UpdatedAt,
		},
	}
	if s.Checksum.Valid {
		res["source"].(map[string]any)["checksum"] = s.Checksum.String
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(res)
}

func (h *Handler) HandleBanks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listBanksJSON(w, r)
	case http.MethodPost:
		h.createBankJSON(w, r)
	default:
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
	}
}

func (h *Handler) listBanksJSON(w http.ResponseWriter, r *http.Request) {
	banks, err := h.service.ListBanks(r.Context())
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items": banks,
	})
}

func (h *Handler) createBankJSON(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header is required", nil)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Failed to read body", nil)
		return
	}
	digest, err := core.ComputeRequestDigest(bodyBytes)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON body", nil)
		return
	}

	var req struct {
		Name string `json:"name"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON payload", nil)
		return
	}
	trimmed := strings.TrimSpace(req.Name)
	if len(trimmed) < 1 || len(trimmed) > 120 {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Bank name must be 1 to 120 characters", nil)
		return
	}

	bankID := core.NewUUID()
	now := time.Now().UTC().UnixMicro()

	var respBody string
	var respCode int
	var isReplay bool

	txErr := core.RunInTx(r.Context(), h.db, func(tc *core.TxnContext) error {
		if err := core.CheckMaintenance(tc); err != nil {
			return ErrUnderMaintenance
		}

		rec, err := core.CheckIdempotency(tc, "POST", "/api/v1/banks", idempotencyKey, digest)
		if errors.Is(err, core.ErrIdempotencyConflict) {
			return err
		}
		if rec != nil {
			respBody = rec.ResponseBody
			respCode = rec.ResponseCode
			isReplay = true
			return nil
		}

		_, err = tc.Exec(`
			INSERT INTO bank (id, name, note, archived, revision, created_at, updated_at)
			VALUES (?, ?, ?, 0, 0, ?, ?)
		`, bankID, trimmed, strings.TrimSpace(req.Note), now, now)
		if err != nil {
			return err
		}

		outJSON, _ := json.Marshal(map[string]any{
			"bank_id":  bankID,
			"revision": 0,
		})
		respBody = string(outJSON)
		respCode = http.StatusCreated

		return core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
			Operation:     "POST",
			ResourceScope: "/api/v1/banks",
			Key:           idempotencyKey,
			RequestDigest: digest,
			Status:        "committed",
			ResponseCode:  respCode,
			ResponseBody:  respBody,
			CreatedAt:     now,
		})
	})

	if errors.Is(txErr, core.ErrIdempotencyConflict) {
		core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key reused with different payload", nil)
		return
	}
	if errors.Is(txErr, ErrUnderMaintenance) {
		core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "System under maintenance", nil)
		return
	}
	if txErr != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", txErr.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if isReplay {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(respCode)
	_, _ = w.Write([]byte(respBody))
}

func (h *Handler) HandleBankSubroutes(w http.ResponseWriter, r *http.Request) {
	sub := strings.TrimPrefix(r.URL.Path, "/api/v1/banks/")
	parts := strings.Split(sub, "/")

	bankID := parts[0]
	if bankID == "" {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Bank not found", nil)
		return
	}

	if len(parts) == 1 {
		if r.Method == http.MethodGet {
			h.getBankDetailJSON(w, r, bankID)
			return
		}
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	switch parts[1] {
	case "members":
		if len(parts) == 3 && parts[2] == "remove" && r.Method == http.MethodPost {
			h.removeBankMembersJSON(w, r, bankID)
			return
		}
	case "archive":
		if len(parts) == 2 && r.Method == http.MethodPost {
			h.archiveBankJSON(w, r, bankID)
			return
		}
	}

	core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Route not found", nil)
}

func (h *Handler) getBankDetailJSON(w http.ResponseWriter, r *http.Request, bankID string) {
	// Check active session: detailed content reading during active session is forbidden!
	active, err := core.GetActiveSession(h.db)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if active != nil {
		core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "Active session in progress; content details locked", map[string]any{
			"session_id":   active.ID,
			"session_kind": active.Kind,
		})
		return
	}

	bankDetail, err := h.service.GetBank(r.Context(), bankID)
	if errors.Is(err, ErrBankNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Bank not found", nil)
		return
	}
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"bank": bankDetail,
	})
}

func (h *Handler) removeBankMembersJSON(w http.ResponseWriter, r *http.Request, bankID string) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header is required", nil)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Failed to read body", nil)
		return
	}
	digest, err := core.ComputeRequestDigest(bodyBytes)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON body", nil)
		return
	}

	var req struct {
		ExpectedRevision *int64   `json:"expected_revision"`
		SenseIDs         []string `json:"sense_ids"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON payload", nil)
		return
	}
	if req.ExpectedRevision == nil {
		core.WriteJSONError(w, r, 428, "PRECONDITION_REQUIRED", "expected_revision is required", nil)
		return
	}
	expectedRevision := *req.ExpectedRevision

	if len(req.SenseIDs) < 1 || len(req.SenseIDs) > 1000 {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "sense_ids must contain 1 to 1000 items", nil)
		return
	}

	resourceScope := fmt.Sprintf("/api/v1/banks/%s/members/remove", bankID)
	var respBody string
	var respCode int
	var isReplay bool

	txErr := core.RunInTx(r.Context(), h.db, func(tc *core.TxnContext) error {
		// Idempotency check
		rec, err := core.CheckIdempotency(tc, "POST", resourceScope, idempotencyKey, digest)
		if errors.Is(err, core.ErrIdempotencyConflict) {
			return err
		}
		if rec != nil {
			respBody = rec.ResponseBody
			respCode = rec.ResponseCode
			isReplay = true
			return nil
		}

		removed, newRev, currentRev, err := h.service.RemoveMembersTx(tc, bankID, req.SenseIDs, expectedRevision)
		if err != nil {
			if errors.Is(err, ErrRevisionConflict) {
				return fmt.Errorf("REVISION_CONFLICT:%d", currentRev)
			}
			return err
		}

		outJSON, _ := json.Marshal(map[string]any{
			"bank_id":       bankID,
			"revision":      newRev,
			"removed_count": removed,
		})
		respBody = string(outJSON)
		respCode = http.StatusOK

		now := time.Now().UTC().UnixMicro()
		return core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
			Operation:     "POST",
			ResourceScope: resourceScope,
			Key:           idempotencyKey,
			RequestDigest: digest,
			Status:        "committed",
			ResponseCode:  respCode,
			ResponseBody:  respBody,
			CreatedAt:     now,
		})
	})

	if errors.Is(txErr, core.ErrIdempotencyConflict) {
		core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key reused with different payload", nil)
		return
	}
	if errors.Is(txErr, ErrSessionActive) {
		core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "Active session exists; member modifications blocked", nil)
		return
	}
	if errors.Is(txErr, ErrUnderMaintenance) {
		core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "System under maintenance", nil)
		return
	}
	if errors.Is(txErr, ErrBankNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Bank not found", nil)
		return
	}
	if txErr != nil && strings.HasPrefix(txErr.Error(), "REVISION_CONFLICT:") {
		var curRev int64
		_, _ = fmt.Sscanf(txErr.Error(), "REVISION_CONFLICT:%d", &curRev)
		core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", "Bank revision conflict", map[string]any{
			"current_revision": curRev,
		})
		return
	}
	if txErr != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", txErr.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if isReplay {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(respCode)
	_, _ = w.Write([]byte(respBody))
}

func (h *Handler) archiveBankJSON(w http.ResponseWriter, r *http.Request, bankID string) {
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Idempotency-Key header is required", nil)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Failed to read body", nil)
		return
	}
	digest, err := core.ComputeRequestDigest(bodyBytes)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON body", nil)
		return
	}

	var req struct {
		ExpectedRevision *int64 `json:"expected_revision"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid JSON payload", nil)
		return
	}
	if req.ExpectedRevision == nil {
		core.WriteJSONError(w, r, 428, "PRECONDITION_REQUIRED", "expected_revision is required", nil)
		return
	}
	expectedRevision := *req.ExpectedRevision

	resourceScope := fmt.Sprintf("/api/v1/banks/%s/archive", bankID)
	var respBody string
	var respCode int
	var isReplay bool

	txErr := core.RunInTx(r.Context(), h.db, func(tc *core.TxnContext) error {
		rec, err := core.CheckIdempotency(tc, "POST", resourceScope, idempotencyKey, digest)
		if errors.Is(err, core.ErrIdempotencyConflict) {
			return err
		}
		if rec != nil {
			respBody = rec.ResponseBody
			respCode = rec.ResponseCode
			isReplay = true
			return nil
		}

		newRev, currentRev, err := h.service.ArchiveBankTx(tc, bankID, expectedRevision)
		if err != nil {
			if errors.Is(err, ErrRevisionConflict) {
				return fmt.Errorf("REVISION_CONFLICT:%d", currentRev)
			}
			return err
		}

		outJSON, _ := json.Marshal(map[string]any{
			"bank_id":  bankID,
			"archived": true,
			"revision": newRev,
		})
		respBody = string(outJSON)
		respCode = http.StatusOK

		now := time.Now().UTC().UnixMicro()
		return core.SaveIdempotencyRecord(tc, &core.IdempotencyRecord{
			Operation:     "POST",
			ResourceScope: resourceScope,
			Key:           idempotencyKey,
			RequestDigest: digest,
			Status:        "committed",
			ResponseCode:  respCode,
			ResponseBody:  respBody,
			CreatedAt:     now,
		})
	})

	if errors.Is(txErr, core.ErrIdempotencyConflict) {
		core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", "Idempotency key reused with different payload", nil)
		return
	}
	if errors.Is(txErr, ErrSessionActive) {
		core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "Active session exists; archive blocked", nil)
		return
	}
	if errors.Is(txErr, ErrUnderMaintenance) {
		core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "System under maintenance", nil)
		return
	}
	if errors.Is(txErr, ErrBankNotFound) {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Bank not found", nil)
		return
	}
	if txErr != nil && strings.HasPrefix(txErr.Error(), "REVISION_CONFLICT:") {
		var curRev int64
		_, _ = fmt.Sscanf(txErr.Error(), "REVISION_CONFLICT:%d", &curRev)
		core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", "Bank revision conflict", map[string]any{
			"current_revision": curRev,
		})
		return
	}
	if txErr != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", txErr.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if isReplay {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	w.WriteHeader(respCode)
	_, _ = w.Write([]byte(respBody))
}

func (h *Handler) HandleSenseVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	active, err := core.GetActiveSession(h.db)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if active != nil {
		core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "Active session in progress; content version discovery locked", map[string]any{
			"session_id":   active.ID,
			"session_kind": active.Kind,
		})
		return
	}

	bankID := r.URL.Query().Get("bank_id")
	cursorParam := r.URL.Query().Get("cursor")
	limitParam := r.URL.Query().Get("limit")

	limit := 30
	if limitParam != "" {
		l, err := strconv.Atoi(limitParam)
		if err != nil || l <= 0 || l > 100 {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Limit must be between 1 and 100", nil)
			return
		}
		limit = l
	}

	afterID := ""
	if cursorParam != "" {
		decoded, err := base64.URLEncoding.DecodeString(cursorParam)
		if err != nil || len(decoded) == 0 {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid cursor", nil)
			return
		}
		afterID = string(decoded)
	}

	items, err := h.service.ListSenseVersions(r.Context(), bankID, afterID, limit+1)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	hasMore := false
	var nextCursor *string
	if len(items) > limit {
		hasMore = true
		items = items[:limit]
		encoded := base64.URLEncoding.EncodeToString([]byte(items[len(items)-1].SenseVersionID))
		nextCursor = &encoded
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
		"has_more":    hasMore,
	})
}

func (h *Handler) HandleQuestionVersions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	active, err := core.GetActiveSession(h.db)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if active != nil {
		core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "Active session in progress; content version discovery locked", map[string]any{
			"session_id":   active.ID,
			"session_kind": active.Kind,
		})
		return
	}

	bankID := r.URL.Query().Get("bank_id")
	cursorParam := r.URL.Query().Get("cursor")
	limitParam := r.URL.Query().Get("limit")

	limit := 30
	if limitParam != "" {
		l, err := strconv.Atoi(limitParam)
		if err != nil || l <= 0 || l > 100 {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Limit must be between 1 and 100", nil)
			return
		}
		limit = l
	}

	afterID := ""
	if cursorParam != "" {
		decoded, err := base64.URLEncoding.DecodeString(cursorParam)
		if err != nil || len(decoded) == 0 {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Invalid cursor", nil)
			return
		}
		afterID = string(decoded)
	}

	items, err := h.service.ListQuestionVersions(r.Context(), bankID, afterID, limit+1)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	hasMore := false
	var nextCursor *string
	if len(items) > limit {
		hasMore = true
		items = items[:limit]
		encoded := base64.URLEncoding.EncodeToString([]byte(items[len(items)-1].QuestionVersionID))
		nextCursor = &encoded
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"items":       items,
		"next_cursor": nextCursor,
		"has_more":    hasMore,
	})
}

