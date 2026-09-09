package core

import (
	"encoding/json"
	"net/http"
)

var allowedDetailKeys = map[string]bool{
	"current_revision":       true,
	"expected_next_sequence": true,
	"last_ack_sequence":      true,
	"cursor":                 true,
	"session_id":             true,
	"session_kind":           true,
	"counts":                 true,
	"limits":                 true,
	"retryable":              true,
	"acceptance":             true,
	"requires_rebuild":       true,
}

type ErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id"`
	Details   map[string]any `json:"details,omitempty"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

func FilterDetails(raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	filtered := make(map[string]any)
	for k, v := range raw {
		if allowedDetailKeys[k] {
			filtered[k] = v
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func WriteJSONError(w http.ResponseWriter, r *http.Request, statusCode int, code, message string, details map[string]any) {
	reqID := r.Header.Get("X-Request-ID")
	if reqID == "" {
		reqID = NewUUID()
	}
	resp := ErrorResponse{
		Error: ErrorBody{
			Code:      code,
			Message:   message,
			RequestID: reqID,
			Details:   FilterDetails(details),
		},
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(resp)
}
