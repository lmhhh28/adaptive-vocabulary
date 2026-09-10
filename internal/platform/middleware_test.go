package platform_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"adaptive-vocabulary/internal/platform"
	_ "modernc.org/sqlite"
)

func TestWorkerTokenSecurity(t *testing.T) {
	sec, err := platform.NewSecurityConfig("secret-worker-token-xyz")
	if err != nil {
		t.Fatalf("failed to create security config: %v", err)
	}

	handler := platform.CSRFMiddleware(sec)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))

	// 1. Valid worker token
	req := httptest.NewRequest(http.MethodPost, "/api/v1/anything", nil)
	req.Header.Set("Authorization", "Bearer secret-worker-token-xyz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 for valid worker token, got %d", rec.Code)
	}

	// 2. Invalid worker token
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/anything", nil)
	reqBad.Header.Set("Authorization", "Bearer wrong-token")
	recBad := httptest.NewRecorder()
	handler.ServeHTTP(recBad, reqBad)
	if recBad.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad worker token, got %d", recBad.Code)
	}
}

func TestCSRFTokenLifecycleAndExpiration(t *testing.T) {
	sec, err := platform.NewSecurityConfig("test-worker")
	if err != nil {
		t.Fatalf("new security config: %v", err)
	}
	sec.TokenTTL = 2 * time.Second

	sessionID := "session-12345"
	token := sec.GenerateCSRFToken(sessionID)

	// Valid token immediately
	if !sec.ValidateCSRFToken(sessionID, token) {
		t.Errorf("expected token to be valid immediately")
	}

	// Mismatched session
	if sec.ValidateCSRFToken("different-session", token) {
		t.Errorf("expected token to be rejected for different session")
	}

	// Tampered signature
	tampered := token[:len(token)-4] + "0000"
	if sec.ValidateCSRFToken(sessionID, tampered) {
		t.Errorf("expected tampered token to be rejected")
	}

	// Expired token (set TTL to -1 second to simulate expiration)
	sec.TokenTTL = -1 * time.Second
	if sec.ValidateCSRFToken(sessionID, token) {
		t.Errorf("expected expired token to be rejected")
	}
}

func TestHostCheckMiddleware_IPv4AndIPv6(t *testing.T) {
	handler := platform.HostCheckMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		host string
		want int
	}{
		{"127.0.0.1", http.StatusOK},
		{"127.0.0.1:8080", http.StatusOK},
		{"localhost", http.StatusOK},
		{"localhost:3000", http.StatusOK},
		{"[::1]", http.StatusOK},
		{"[::1]:8080", http.StatusOK},
		{"example.com", http.StatusBadRequest},
		{"192.168.1.100:8080", http.StatusBadRequest},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(http.MethodGet, "http://"+tt.host+"/", nil)
		req.Host = tt.host
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("HostCheck(%q): got status %d, want %d", tt.host, rec.Code, tt.want)
		}
	}
}

func TestOriginCheckMiddleware_LoopbackAndForeign(t *testing.T) {
	handler := platform.OriginCheckMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		origin string
		method string
		want   int
	}{
		{"", http.MethodPost, http.StatusOK},
		{"http://127.0.0.1:8080", http.MethodPost, http.StatusOK},
		{"http://localhost:3000", http.MethodPost, http.StatusOK},
		{"http://[::1]:8080", http.MethodPost, http.StatusOK},
		{"http://evil.com", http.MethodPost, http.StatusForbidden},
		{"http://evil.com", http.MethodGet, http.StatusOK},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, "/api/v1/action", nil)
		if tt.origin != "" {
			req.Header.Set("Origin", tt.origin)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != tt.want {
			t.Errorf("OriginCheck(method=%s, origin=%q): got status %d, want %d", tt.method, tt.origin, rec.Code, tt.want)
		}
	}
}

func TestMaintenanceGateMiddleware_Caching(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open memory db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE projection_control (
			id INTEGER PRIMARY KEY,
			mode TEXT NOT NULL,
			active_version TEXT NOT NULL,
			run_id TEXT,
			run_status TEXT,
			requires_rebuild INTEGER NOT NULL DEFAULT 0,
			fixed_watermark INTEGER NOT NULL DEFAULT 0,
			updated_at INTEGER NOT NULL
		);
		INSERT INTO projection_control (id, mode, active_version, requires_rebuild, fixed_watermark, updated_at)
		VALUES (1, 'active', 'v1', 0, 0, 1000);
	`)
	if err != nil {
		t.Fatalf("init schema: %v", err)
	}

	handler := platform.MaintenanceGateMiddleware(db)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Initial active status -> 200
	req := httptest.NewRequest(http.MethodPost, "/api/v1/bank", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	// Update DB to rebuilding
	_, _ = db.Exec("UPDATE projection_control SET mode = 'rebuilding' WHERE id = 1")

	// Wait 60ms for cache to expire
	time.Sleep(60 * time.Millisecond)

	recMaint := httptest.NewRecorder()
	handler.ServeHTTP(recMaint, req)
	if recMaint.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 during maintenance, got %d", recMaint.Code)
	}

	// Exempt endpoints should still pass during maintenance
	exemptReq := httptest.NewRequest(http.MethodPost, "/internal/v1/generation/claim", nil)
	recExempt := httptest.NewRecorder()
	handler.ServeHTTP(recExempt, exemptReq)
	if recExempt.Code != http.StatusOK {
		t.Fatalf("expected 200 on exempt route, got %d", recExempt.Code)
	}
}
