package importer

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"adaptive-vocabulary/internal/core"
)

type Handler struct {
	db      *sql.DB
	service *Service
}

func NewHandler(db *sql.DB) *Handler {
	return &Handler{
		db:      db,
		service: NewService(db),
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

// RegisterRoutes registers all import UI and API routes on mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	// Static assets
	mux.HandleFunc("/static/import/", h.ServeStaticImport)

	// Web UI
	mux.HandleFunc("GET /banks/{id}/import", h.ServeBankImportUI)
	mux.HandleFunc("/imports/", h.ServeImportDetailUI)

	// REST APIs
	mux.HandleFunc("POST /api/v1/sources/{id}/imports", h.HandleSourceImports)
	mux.HandleFunc("POST /api/v1/banks/{id}/imports", h.HandleBankImports)
	mux.HandleFunc("/api/v1/imports/", h.HandleImportItems)
	mux.HandleFunc("/api/v1/import-items/", h.HandleImportItemResolve)
}

func (h *Handler) ServeStaticImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	prefix := "/static/import/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	fileName := strings.TrimPrefix(r.URL.Path, prefix)
	if strings.Contains(fileName, "..") {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	diskPath := findWebPath(filepath.Join("web/static/import", fileName))
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

func (h *Handler) ServeBankImportUI(w http.ResponseWriter, r *http.Request) {
	bankID := r.PathValue("id")
	if bankID == "" {
		path := strings.TrimPrefix(r.URL.Path, "/banks/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 1 {
			bankID = parts[0]
		}
	}
	if bankID == "" {
		http.NotFound(w, r)
		return
	}

	tmplPath := findWebPath("web/templates/import/import.html")
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		http.Error(w, "failed to load import template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var bankName string
	_ = h.db.QueryRowContext(r.Context(), `SELECT name FROM bank WHERE id = ?`, bankID).Scan(&bankName)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = tmpl.Execute(w, map[string]any{
		"BankID":   bankID,
		"BankName": bankName,
	})
}

func (h *Handler) ServeImportDetailUI(w http.ResponseWriter, r *http.Request) {
	importID := strings.TrimPrefix(r.URL.Path, "/imports/")
	if importID == "" || strings.Contains(importID, "/") {
		http.NotFound(w, r)
		return
	}

	tmplPath := findWebPath("web/templates/import/import_detail.html")
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		http.Error(w, "failed to load import detail template: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = tmpl.Execute(w, map[string]any{
		"ImportID": importID,
	})
}

func (h *Handler) HandleSourceImports(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("id")
	if sourceID == "" {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/sources/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 1 {
			sourceID = parts[0]
		}
	}
	if sourceID == "" {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "source not found", nil)
		return
	}

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

	var req SourceImportRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body", nil)
		return
	}

	digest, _ := core.ComputeRequestDigest(bodyBytes)
	job, err := h.service.ImportSourceEntries(r.Context(), sourceID, req, idempotencyKey, digest)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"import_id": job.ID,
		"status":    job.Status,
	})
}

func (h *Handler) HandleBankImports(w http.ResponseWriter, r *http.Request) {
	bankID := r.PathValue("id")
	if bankID == "" {
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/banks/")
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 1 {
			bankID = parts[0]
		}
	}
	if bankID == "" {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "bank not found", nil)
		return
	}

	if r.Method != http.MethodPost {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")

	contentType := r.Header.Get("Content-Type")
	var entries []ParsedEntry
	var digest string
	var expectedRev int64 = -1
	var resolutionPolicyVer string = "v1"

	if strings.HasPrefix(contentType, "multipart/form-data") {
		// Multipart file upload
		err := r.ParseMultipartForm(4 * 1024 * 1024)
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) || strings.Contains(err.Error(), "too large") {
				core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "file or request body too large (exceeds 2 MiB limit)", nil)
				return
			}
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to parse multipart form: "+err.Error(), nil)
			return
		}

		if revStr := r.FormValue("expected_revision"); revStr != "" {
			if v, err := strconv.ParseInt(revStr, 10, 64); err == nil {
				expectedRev = v
			}
		}
		if pol := r.FormValue("resolution_policy_version"); pol != "" {
			resolutionPolicyVer = pol
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "missing 'file' field in form", nil)
			return
		}
		defer file.Close()

		fileBytes, err := io.ReadAll(file)
		if err != nil {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read uploaded file", nil)
			return
		}

		parsed, err := ParseRawInput(fileBytes, header.Filename)
		if err != nil {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
			return
		}
		entries = parsed

		// Compute multipart digest from metadata JSON + file SHA-256
		fileSum := sha256.Sum256(fileBytes)
		metaJSON, _ := json.Marshal(map[string]any{
			"filename":                  header.Filename,
			"expected_revision":         expectedRev,
			"resolution_policy_version": resolutionPolicyVer,
			"file_sha256":               hex.EncodeToString(fileSum[:]),
		})
		sum := sha256.Sum256(metaJSON)
		digest = hex.EncodeToString(sum[:])
	} else {
		// JSON payload
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "failed to read request body", nil)
			return
		}

		var req BankImportRequest
		if err := json.Unmarshal(bodyBytes, &req); err != nil {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body", nil)
			return
		}

		expectedRev = req.ExpectedRevision
		if req.ResolutionPolicyVersion != "" {
			resolutionPolicyVer = req.ResolutionPolicyVersion
		}

		if len(req.Words) > 0 {
			parsed, err := ParseWordSlice(req.Words)
			if err != nil {
				core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
				return
			}
			entries = parsed
		} else if strings.TrimSpace(req.Text) != "" {
			parsed, err := ParseRawInput([]byte(req.Text), "input.txt")
			if err != nil {
				core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
				return
			}
			entries = parsed
		} else {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "must provide words, text, or file", nil)
			return
		}

		d, _ := core.ComputeRequestDigest(bodyBytes)
		digest = d
	}

	job, err := h.service.ImportBankVocabulary(r.Context(), bankID, entries, resolutionPolicyVer, expectedRev, idempotencyKey, digest)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"import_id":        job.ID,
		"status":           job.Status,
		"total_count":      job.TotalCount,
		"resolved_count":   job.ResolvedCount,
		"ambiguous_count":  job.AmbiguousCount,
		"not_found_count":  job.NotFoundCount,
		"invalid_count":    job.InvalidCount,
		"excluded_count":   job.ExcludedCount,
	})
}

func (h *Handler) HandleImportItems(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/imports/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[1] != "items" {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found", nil)
		return
	}

	importID := parts[0]
	if r.Method != http.MethodGet {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "INVALID_REQUEST", "Method not allowed", nil)
		return
	}

	statusFilter := r.URL.Query().Get("status")
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	limit := 50
	offset := 0
	if limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil {
			limit = v
		}
	}
	if offsetStr != "" {
		if v, err := strconv.Atoi(offsetStr); err == nil {
			offset = v
		}
	}

	resp, err := h.service.GetImportItems(r.Context(), importID, statusFilter, limit, offset)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleImportItemResolve(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/import-items/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[1] != "resolve" {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "resource not found", nil)
		return
	}

	itemID := parts[0]
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

	var req ResolveItemRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "malformed JSON body", nil)
		return
	}

	digest, _ := core.ComputeRequestDigest(bodyBytes)
	item, err := h.service.ResolveImportItem(r.Context(), itemID, req, idempotencyKey, digest)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"item":     item,
		"revision": item.Revision,
	})
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrSourceNotFound) || errors.Is(err, ErrBankNotFound) ||
		errors.Is(err, ErrImportNotFound) || errors.Is(err, ErrImportItemNotFound) ||
		errors.Is(err, ErrSenseNotFound):
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
	case errors.Is(err, ErrRevisionConflict):
		core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", err.Error(), nil)
	case errors.Is(err, ErrInvalidItemStatus):
		core.WriteJSONError(w, r, http.StatusConflict, "INVALID_STATE", err.Error(), nil)
	case errors.Is(err, ErrIdempotencyConflict):
		core.WriteJSONError(w, r, http.StatusConflict, "IDEMPOTENCY_CONFLICT", err.Error(), nil)
	case errors.Is(err, ErrSourceConflict):
		core.WriteJSONError(w, r, http.StatusConflict, "SOURCE_CONFLICT", err.Error(), nil)
	case errors.Is(err, ErrMaintenance):
		core.WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", err.Error(), nil)
	case errors.Is(err, ErrBankArchived):
		core.WriteJSONError(w, r, http.StatusBadRequest, "BANK_ARCHIVED", err.Error(), nil)
	case errors.Is(err, ErrInvalidAction) || errors.Is(err, ErrMissingSenseRefs) ||
		errors.Is(err, ErrFileTooLarge) || errors.Is(err, ErrTooManyRows) ||
		errors.Is(err, ErrEmptyInput) || errors.Is(err, ErrInvalidEncoding) ||
		errors.Is(err, ErrInvalidCSVFormat):
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
	default:
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}
