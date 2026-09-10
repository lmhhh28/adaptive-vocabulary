package platform

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"adaptive-vocabulary/internal/core"
	_ "embed"
)

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

func findWebDir(relPath string) string {
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
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				return abs
			}
		}
	}
	return relPath
}

//go:embed home.html
var homeHTML string

var homeTmpl = template.Must(template.New("home").Parse(homeHTML))

type HomeHandler struct {
	DB              *DB
	Config          *Config
	Security        *SecurityConfig
	SchemaVersion   int
}

type HomeViewModel struct {
	ContractVersion string
	SchemaVersion   int
	SQLiteVersion   string
	Mode            string
}

func (h *HomeHandler) ServeHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	mode := "active"
	if pc, err := core.GetProjectionControl(h.DB); err == nil && pc != nil {
		mode = pc.Mode
	}

	vm := HomeViewModel{
		ContractVersion: h.Config.ContractVersion,
		SchemaVersion:   h.SchemaVersion,
		SQLiteVersion:   h.DB.Version,
		Mode:            mode,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	shellPath := findWebPath("web/templates/shell/shell.html")
	if tmpl, err := template.ParseFiles(shellPath); err == nil {
		if err := tmpl.Execute(w, vm); err == nil {
			return
		}
	}

	_ = homeTmpl.Execute(w, vm)
}

func (h *HomeHandler) ServeClientContext(w http.ResponseWriter, r *http.Request) {
	sessionID := ""
	if cookie, err := r.Cookie("avocab_session"); err == nil && cookie.Value != "" {
		sessionID = cookie.Value
	} else {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			WriteJSONError(w, r, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to generate session id", nil)
			return
		}
		sessionID = hex.EncodeToString(b)
		http.SetCookie(w, &http.Cookie{
			Name:     "avocab_session",
			Value:    sessionID,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   false, // Local HTTP loopback
		})
	}

	token := h.Security.GenerateCSRFToken(sessionID)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"csrf_token": token,
	})
}

type HealthResponse struct {
	Status          string `json:"status"`
	SchemaVersion   int    `json:"schema_version"`
	AppVersion      string `json:"app_version"`
	ContractVersion string `json:"contract_version"`
	Mode            string `json:"mode"`
}

func (h *HomeHandler) ServeHealth(w http.ResponseWriter, r *http.Request) {
	mode := "active"
	if pc, err := core.GetProjectionControl(h.DB); err == nil && pc != nil {
		mode = pc.Mode
	}

	resp := HealthResponse{
		Status:          "ok",
		SchemaVersion:   h.SchemaVersion,
		AppVersion:      "0.1.0",
		ContractVersion: h.Config.ContractVersion,
		Mode:            mode,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

type MaintenanceResponse struct {
	Mode            string `json:"mode"`
	ActiveVersion   string `json:"active_version"`
	RequiresRebuild bool   `json:"requires_rebuild"`
	RunID           string `json:"run_id,omitempty"`
	RunStatus       string `json:"run_status,omitempty"`
	NextAction      string `json:"next_action,omitempty"`
}

func (h *HomeHandler) ServeMaintenance(w http.ResponseWriter, r *http.Request) {
	pc, err := core.GetProjectionControl(h.DB)
	if err != nil {
		WriteJSONError(w, r, http.StatusInternalServerError, "DATABASE_ERROR", "failed to read maintenance status", nil)
		return
	}

	resp := MaintenanceResponse{
		Mode:            pc.Mode,
		ActiveVersion:   pc.ActiveVersion,
		RequiresRebuild: pc.RequiresRebuild,
	}
	if pc.RunID.Valid {
		resp.RunID = pc.RunID.String
	}
	if pc.RunStatus.Valid {
		resp.RunStatus = pc.RunStatus.String
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

// ServeTestPost is a test endpoint to verify write permissions and CSRF validation in tests
func (h *HomeHandler) ServeTestPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteJSONError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "POST required", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
		"time":   time.Now().UTC().Unix(),
	})
}
