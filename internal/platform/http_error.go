package platform

import (
	"net/http"

	"adaptive-vocabulary/internal/core"
)

type ErrorBody = core.ErrorBody
type ErrorResponse = core.ErrorResponse

func FilterDetails(raw map[string]any) map[string]any {
	return core.FilterDetails(raw)
}

func WriteJSONError(w http.ResponseWriter, r *http.Request, statusCode int, code, message string, details map[string]any) {
	core.WriteJSONError(w, r, statusCode, code, message, details)
}
