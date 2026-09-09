package platform_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"adaptive-vocabulary/internal/platform"
)

func createTestConfig(t *testing.T, listen string) *platform.Config {
	t.Helper()
	dir := t.TempDir()

	policyPath := filepath.Join(dir, "policy.json")
	policyContent := `{"design_version": "1.1.0", "task_families": ["meaning_recognition"]}`
	if err := os.WriteFile(policyPath, []byte(policyContent), 0600); err != nil {
		t.Fatalf("write policy: %v", err)
	}

	cfg, err := platform.ValidateAndResolveConfig(dir, listen, policyPath, "")
	if err != nil {
		t.Fatalf("ValidateAndResolveConfig failed: %v", err)
	}
	return cfg
}

func TestConfigValidation_LoopbackEnforcement(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "policy.json")
	_ = os.WriteFile(policyPath, []byte(`{"design_version":"1.1.0"}`), 0600)

	// Valid 127.0.0.1
	_, err := platform.ValidateAndResolveConfig(dir, "127.0.0.1:8080", policyPath, "")
	if err != nil {
		t.Errorf("expected valid config, got %v", err)
	}

	// Invalid 0.0.0.0
	_, err = platform.ValidateAndResolveConfig(dir, "0.0.0.0:8080", policyPath, "")
	if err == nil {
		t.Errorf("expected error for 0.0.0.0, got nil")
	}

	// Invalid 192.168.1.100
	_, err = platform.ValidateAndResolveConfig(dir, "192.168.1.100:8080", policyPath, "")
	if err == nil {
		t.Errorf("expected error for non-loopback IP, got nil")
	}
}

func TestServer_HTTPHandlers(t *testing.T) {
	cfg := createTestConfig(t, "127.0.0.1:0")
	server, err := platform.NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	home := &platform.HomeHandler{
		DB:            server.DB,
		Config:        server.Config,
		Security:      server.Security,
		SchemaVersion: server.SchemaVersion,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", home.ServeHome)
	mux.HandleFunc("/client-context", home.ServeClientContext)
	mux.HandleFunc("/api/v1/health", home.ServeHealth)
	mux.HandleFunc("/api/v1/maintenance", home.ServeMaintenance)
	mux.HandleFunc("/api/v1/test/action", home.ServeTestPost)

	var handler http.Handler = mux
	handler = platform.CSRFMiddleware(server.Security)(handler)
	handler = platform.OriginCheckMiddleware(handler)
	handler = platform.HostCheckMiddleware(handler)
	handler = platform.CacheControlMiddleware(handler)
	handler = platform.RequestIDMiddleware(handler)

	ts := httptest.NewServer(handler)
	defer ts.Close()

	client := ts.Client()

	// 1. GET / -> 200 HTML
	req, _ := http.NewRequest("GET", ts.URL+"/", nil)
	req.Host = "127.0.0.1"
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Adaptive Vocabulary") {
		t.Errorf("missing title in home page")
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("expected Cache-Control: no-store, got %s", resp.Header.Get("Cache-Control"))
	}

	// 2. GET /client-context -> returns csrf_token and sets cookie
	req, _ = http.NewRequest("GET", ts.URL+"/client-context", nil)
	req.Host = "127.0.0.1"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /client-context: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	var ctxResp map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&ctxResp)
	csrfToken := ctxResp["csrf_token"]
	if csrfToken == "" {
		t.Errorf("expected non-empty csrf_token")
	}
	var sessionCookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == "avocab_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("expected avocab_session cookie")
	}

	// 3. GET /api/v1/health -> 200 OK
	req, _ = http.NewRequest("GET", ts.URL+"/api/v1/health", nil)
	req.Host = "127.0.0.1"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	defer resp.Body.Close()
	var health platform.HealthResponse
	_ = json.NewDecoder(resp.Body).Decode(&health)
	if health.Status != "ok" || health.ContractVersion != "1.1.0" {
		t.Errorf("unexpected health response: %+v", health)
	}

	// 4. POST without CSRF -> 403 CSRF_REJECTED
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/test/action", bytes.NewReader([]byte("{}")))
	req.Host = "127.0.0.1"
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST without CSRF: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 CSRF_REJECTED, got %d", resp.StatusCode)
	}

	// 5. POST with valid CSRF & cookie -> 200 OK
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/test/action", bytes.NewReader([]byte("{}")))
	req.Host = "127.0.0.1"
	req.AddCookie(sessionCookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST with CSRF: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 6. POST with illegal Origin -> 403 ORIGIN_REJECTED
	req, _ = http.NewRequest("POST", ts.URL+"/api/v1/test/action", bytes.NewReader([]byte("{}")))
	req.Host = "127.0.0.1"
	req.AddCookie(sessionCookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.Header.Set("Origin", "http://evil.com")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("POST with illegal Origin: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 ORIGIN_REJECTED, got %d", resp.StatusCode)
	}
}
