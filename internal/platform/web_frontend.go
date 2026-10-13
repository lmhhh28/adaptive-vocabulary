package platform

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type WebFrontendHandler struct {
	DistDir     string
	Manifest    map[string]any
	ManifestRaw string
	AssetHash   string
	HomeHandler *HomeHandler
}

func NewWebFrontendHandler(home *HomeHandler) *WebFrontendHandler {
	distDir := findWebDir("web/frontend/dist")
	var manifest map[string]any
	var manifestRaw string
	var assetHash string

	manifestPath := filepath.Join(distDir, ".vite", "manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		manifestRaw = string(data)
		_ = json.Unmarshal(data, &manifest)
		h := sha256.Sum256(data)
		assetHash = hex.EncodeToString(h[:])
	}

	return &WebFrontendHandler{
		DistDir:     distDir,
		Manifest:    manifest,
		ManifestRaw: manifestRaw,
		AssetHash:   assetHash,
		HomeHandler: home,
	}
}

func (h *WebFrontendHandler) IsBuilt() bool {
	indexPath := filepath.Join(h.DistDir, "index.html")
	if info, err := os.Stat(indexPath); err == nil && !info.IsDir() {
		return true
	}
	return false
}

func (h *WebFrontendHandler) ServeAssets() http.Handler {
	assetsDir := filepath.Join(h.DistDir, "assets")
	fileServer := http.StripPrefix("/assets/", http.FileServer(http.Dir(assetsDir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		fileServer.ServeHTTP(w, r)
	})
}

func (h *WebFrontendHandler) ServeSPA(w http.ResponseWriter, r *http.Request) {
	// If dist is not built, fallback to legacy HomeHandler
	if !h.IsBuilt() {
		h.HomeHandler.ServeHome(w, r)
		return
	}

	// Never serve API paths or client-context via SPA
	if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/client-context" {
		http.NotFound(w, r)
		return
	}

	indexPath := filepath.Join(h.DistDir, "index.html")
	content, err := os.ReadFile(indexPath)
	if err != nil {
		h.HomeHandler.ServeHome(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(content)
}

func (h *WebFrontendHandler) ServeManifest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	resp := map[string]any{
		"built":      h.IsBuilt(),
		"dist_dir":   h.DistDir,
		"asset_hash": h.AssetHash,
		"manifest":   h.Manifest,
	}
	_ = json.NewEncoder(w).Encode(resp)
}
