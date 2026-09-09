package revision

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"adaptive-vocabulary/internal/core"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
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
	mux.HandleFunc("/static/quality/", h.ServeStaticQuality)

	// Web UI
	mux.HandleFunc("/quality", h.ServeQualityUI)

	// Question versions evidence dispositions and retirement
	mux.HandleFunc("/api/v1/question-versions/", h.HandleQuestionVersions)

	// Learning queries
	mux.HandleFunc("/api/v1/learning/senses/", h.HandleLearningSense)
	mux.HandleFunc("/api/v1/learning/summary", h.HandleLearningSummary)
}

func (h *Handler) ServeStaticQuality(w http.ResponseWriter, r *http.Request) {
	subPath := strings.TrimPrefix(r.URL.Path, "/static/quality/")
	filePath := findWebPath(filepath.Join("web", "static", "quality", subPath))
	http.ServeFile(w, r, filePath)
}

func (h *Handler) ServeQualityUI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET is allowed", nil)
		return
	}

	data, err := h.service.LoadQualityPageData(r.Context())
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	tmplPath := findWebPath(filepath.Join("web", "templates", "quality", "quality.html"))
	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "TEMPLATE_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = tmpl.Execute(w, data)
}

func (h *Handler) HandleQuestionVersions(w http.ResponseWriter, r *http.Request) {
	// Paths:
	// POST /api/v1/question-versions/{id}/evidence-dispositions
	// POST /api/v1/question-versions/{id}/retire
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/question-versions/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Resource not found", nil)
		return
	}

	versionID := parts[0]
	actionType := parts[1]

	switch actionType {
	case "evidence-dispositions":
		h.handleEvidenceDisposition(w, r, versionID)
	case "retire":
		h.handleRetire(w, r, versionID)
	default:
		core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Unknown question-version action", nil)
	}
}

func (h *Handler) handleEvidenceDisposition(w http.ResponseWriter, r *http.Request, versionID string) {
	if r.Method != http.MethodPost {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST is allowed", nil)
		return
	}

	var req EvidenceDispositionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Failed to parse request JSON", nil)
		return
	}

	if req.Action == "" || req.Reason == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "action and reason are required", nil)
		return
	}

	resp, err := h.service.ApplyEvidenceDisposition(r.Context(), versionID, req.Action, req.Reason, req.ExpectedRevision)
	if err != nil {
		if errors.Is(err, ErrSessionActive) {
			core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "Active session exists; please end or submit session before applying disposition", nil)
			return
		}
		if errors.Is(err, ErrRevisionConflict) {
			core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", "Question version revision conflict", nil)
			return
		}
		if errors.Is(err, ErrVersionNotFound) {
			core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Question version not found", nil)
			return
		}
		if errors.Is(err, ErrInvalidAction) {
			core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
			return
		}
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleRetire(w http.ResponseWriter, r *http.Request, versionID string) {
	if r.Method != http.MethodPost {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only POST is allowed", nil)
		return
	}

	var req RetireRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "Failed to parse request JSON", nil)
		return
	}

	resp, err := h.service.RetireQuestionVersion(r.Context(), versionID, req.Reason, req.ExpectedRevision)
	if err != nil {
		if errors.Is(err, ErrRevisionConflict) {
			core.WriteJSONError(w, r, http.StatusConflict, "REVISION_CONFLICT", "Question version revision conflict", nil)
			return
		}
		if errors.Is(err, ErrVersionNotFound) {
			core.WriteJSONError(w, r, http.StatusNotFound, "NOT_FOUND", "Question version not found", nil)
			return
		}
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) HandleLearningSense(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET is allowed", nil)
		return
	}

	senseID := strings.TrimPrefix(r.URL.Path, "/api/v1/learning/senses/")
	if senseID == "" {
		core.WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "sense ID is required", nil)
		return
	}

	detail, err := h.service.GetLearningSenseDetail(r.Context(), senseID)
	if err != nil {
		if errors.Is(err, ErrSessionActive) {
			core.WriteJSONError(w, r, http.StatusConflict, "SESSION_ACTIVE", "Active session exists; learning sense detail is disabled during active session", nil)
			return
		}
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(detail)
}

func (h *Handler) HandleLearningSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET is allowed", nil)
		return
	}

	summary, err := h.service.GetLearningSummary(r.Context())
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(summary)
}
