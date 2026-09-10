package platform

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"adaptive-vocabulary/internal/core"
	"github.com/google/uuid"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	sessionIDKey contextKey = "session_id"

	// DefaultCSRFTokenTTL is the valid lifetime of a CSRF token.
	DefaultCSRFTokenTTL = 24 * time.Hour
)

func GetRequestID(r *http.Request) string {
	if v, ok := r.Context().Value(requestIDKey).(string); ok && v != "" {
		return v
	}
	return uuid.NewString()
}

type SecurityConfig struct {
	CSRFSecret  []byte
	WorkerToken string
	TokenTTL    time.Duration
}

// NewSecurityConfig generates a fresh SecurityConfig with a cryptographically secure 32-byte secret.
func NewSecurityConfig(workerToken string) (*SecurityConfig, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("generate CSRF secret: %w", err)
	}
	return &SecurityConfig{
		CSRFSecret:  secret,
		WorkerToken: workerToken,
		TokenTTL:    DefaultCSRFTokenTTL,
	}, nil
}

// LoadOrGenerateSecurityConfig loads the CSRF secret from dataDir/csrf.secret or generates and persists a new one.
func LoadOrGenerateSecurityConfig(dataDir, workerToken string) (*SecurityConfig, error) {
	if dataDir != "" {
		secretPath := filepath.Join(dataDir, "csrf.secret")
		if data, err := os.ReadFile(secretPath); err == nil && len(data) == 32 {
			return &SecurityConfig{
				CSRFSecret:  data,
				WorkerToken: workerToken,
				TokenTTL:    DefaultCSRFTokenTTL,
			}, nil
		}
	}

	sec, err := NewSecurityConfig(workerToken)
	if err != nil {
		return nil, err
	}
	if dataDir != "" {
		secretPath := filepath.Join(dataDir, "csrf.secret")
		_ = os.WriteFile(secretPath, sec.CSRFSecret, 0600)
	}
	return sec, nil
}

// GenerateCSRFToken creates a time-stamped HMAC token for the session.
func (sec *SecurityConfig) GenerateCSRFToken(sessionID string) string {
	nowSec := time.Now().UTC().Unix()
	msg := fmt.Sprintf("%s:%d", sessionID, nowSec)
	h := hmac.New(sha256.New, sec.CSRFSecret)
	h.Write([]byte(msg))
	sig := hex.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("%d.%s", nowSec, sig)
}

// ValidateCSRFToken validates that the token is authentic, belongs to sessionID, and has not expired.
func (sec *SecurityConfig) ValidateCSRFToken(sessionID, token string) bool {
	ttl := sec.TokenTTL
	if ttl == 0 {
		ttl = DefaultCSRFTokenTTL
	}

	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		// Fallback for legacy format during transitions
		h := hmac.New(sha256.New, sec.CSRFSecret)
		h.Write([]byte(sessionID))
		legacy := hex.EncodeToString(h.Sum(nil))
		return subtle.ConstantTimeCompare([]byte(legacy), []byte(token)) == 1
	}

	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}

	nowSec := time.Now().UTC().Unix()
	// Allow 60s clock skew forward, and reject if older than TTL
	if ts > nowSec+60 || nowSec-ts > int64(ttl.Seconds()) {
		return false
	}

	msg := fmt.Sprintf("%s:%d", sessionID, ts)
	h := hmac.New(sha256.New, sec.CSRFSecret)
	h.Write([]byte(msg))
	expected := hex.EncodeToString(h.Sum(nil))

	return subtle.ConstantTimeCompare([]byte(expected), []byte(parts[1])) == 1
}

func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-Id")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, reqID)
		w.Header().Set("X-Request-Id", reqID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func CacheControlMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func MaxBytesMiddleware(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// isLoopbackHost tests if the host is a local loopback address (IPv4 or IPv6).
func isLoopbackHost(host string) bool {
	if host == "127.0.0.1" || host == "localhost" || host == "::1" || host == "[::1]" {
		return true
	}
	h := strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func HostCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if !isLoopbackHost(host) {
			WriteJSONError(w, r, http.StatusBadRequest, "INVALID_REQUEST", "invalid host header: only loopback allowed", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func OriginCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isWriteMethod(r.Method) {
			origin := r.Header.Get("Origin")
			// Design Decision: Non-browser HTTP clients (e.g. Python workers, CLI, tests)
			// and certain same-origin requests omit the Origin header.
			// CSRF protection for browsers is enforced by session cookie + X-CSRF-Token header.
			if origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil {
					WriteJSONError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "invalid Origin header", nil)
					return
				}
				h := parsed.Hostname()
				if !isLoopbackHost(h) {
					WriteJSONError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "forbidden Origin: loopback required", nil)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func CSRFMiddleware(sec *SecurityConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Skip CSRF check for safe methods or client-context / health / maintenance
			if !isWriteMethod(r.Method) || r.URL.Path == "/client-context" || r.URL.Path == "/api/v1/health" || r.URL.Path == "/api/v1/maintenance" {
				next.ServeHTTP(w, r)
				return
			}

			// Allow worker authenticated via Authorization: Bearer <token>
			// Constant-time comparison to prevent timing side-channel attacks
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				token := strings.TrimPrefix(auth, "Bearer ")
				if sec.WorkerToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(sec.WorkerToken)) == 1 {
					next.ServeHTTP(w, r)
					return
				}
				WriteJSONError(w, r, http.StatusUnauthorized, "UNAUTHORIZED", "invalid worker authorization token", nil)
				return
			}

			// Validate browser session cookie and X-CSRF-Token
			cookie, err := r.Cookie("avocab_session")
			if err != nil || cookie.Value == "" {
				WriteJSONError(w, r, http.StatusForbidden, "CSRF_REJECTED", "missing session cookie for CSRF validation", nil)
				return
			}

			token := r.Header.Get("X-CSRF-Token")
			if token == "" || !sec.ValidateCSRFToken(cookie.Value, token) {
				WriteJSONError(w, r, http.StatusForbidden, "CSRF_REJECTED", "invalid or missing CSRF token", nil)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func isWriteMethod(m string) bool {
	return m == http.MethodPost || m == http.MethodPut || m == http.MethodDelete || m == http.MethodPatch
}

type maintenanceCache struct {
	db        *sql.DB
	expiresAt int64
	isMaint   bool
	err       error
	mu        sync.RWMutex
}

var maintCacheTTLMicro int64 = 50 * 1000 // 50ms TTL

func (mc *maintenanceCache) Check() error {
	now := time.Now().UTC().UnixMicro()
	mc.mu.RLock()
	if now < mc.expiresAt {
		err := mc.err
		isM := mc.isMaint
		mc.mu.RUnlock()
		if isM {
			return core.ErrUnderMaintenance
		}
		return err
	}
	mc.mu.RUnlock()

	mc.mu.Lock()
	defer mc.mu.Unlock()
	now = time.Now().UTC().UnixMicro()
	if now < mc.expiresAt {
		if mc.isMaint {
			return core.ErrUnderMaintenance
		}
		return mc.err
	}

	err := core.CheckMaintenance(mc.db)
	mc.err = err
	mc.isMaint = errors.Is(err, core.ErrUnderMaintenance)
	mc.expiresAt = now + maintCacheTTLMicro
	return err
}

func MaintenanceGateMiddleware(db *sql.DB) func(http.Handler) http.Handler {
	cache := &maintenanceCache{db: db}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isWriteMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			// Exempt worker internal generation endpoints
			if strings.HasPrefix(r.URL.Path, "/internal/v1/generation/") {
				next.ServeHTTP(w, r)
				return
			}
			// Exempt maintenance / client-context
			if r.URL.Path == "/api/v1/maintenance" || r.URL.Path == "/client-context" {
				next.ServeHTTP(w, r)
				return
			}
			if err := cache.Check(); err != nil {
				WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "system is currently under maintenance for projection rebuild", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

