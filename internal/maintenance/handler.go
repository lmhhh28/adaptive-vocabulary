package maintenance

import (
	"encoding/json"
	"net/http"

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

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/maintenance", h.ServeMaintenance)
}

func (h *Handler) ServeMaintenance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		core.WriteJSONError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Only GET is allowed", nil)
		return
	}

	state, err := h.service.GetMaintenanceState(r.Context())
	if err != nil {
		core.WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(state)
}
