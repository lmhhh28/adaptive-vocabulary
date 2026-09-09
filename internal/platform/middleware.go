package platform

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net"
	"net/http"
	"net/url"
	"strings"

	"adaptive-vocabulary/internal/core"
	"github.com/google/uuid"
)

type contextKey string

const (
	requestIDKey contextKey = "request_id"
	sessionIDKey contextKey = "session_id"
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
}

func NewSecurityConfig(workerToken string) *SecurityConfig {
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	return &SecurityConfig{
		CSRFSecret:  secret,
		WorkerToken: workerToken,
	}
}

func (sec *SecurityConfig) GenerateCSRFToken(sessionID string) string {
	h := hmac.New(sha256.New, sec.CSRFSecret)
	h.Write([]byte(sessionID))
	return hex.EncodeToString(h.Sum(nil))
}

func (sec *SecurityConfig) ValidateCSRFToken(sessionID, token string) bool {
	expected := sec.GenerateCSRFToken(sessionID)
	return hmac.Equal([]byte(expected), []byte(token))
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

func HostCheckMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" {
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
			if origin != "" {
				parsed, err := url.Parse(origin)
				if err != nil {
					WriteJSONError(w, r, http.StatusForbidden, "ORIGIN_REJECTED", "invalid Origin header", nil)
					return
				}
				h := parsed.Hostname()
				if h != "127.0.0.1" && h != "localhost" {
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
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				token := strings.TrimPrefix(auth, "Bearer ")
				if sec.WorkerToken != "" && token == sec.WorkerToken {
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

func MaintenanceGateMiddleware(db *sql.DB) func(http.Handler) http.Handler {
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
			if err := core.CheckMaintenance(db); err != nil {
				WriteJSONError(w, r, http.StatusServiceUnavailable, "MAINTENANCE", "system is currently under maintenance for projection rebuild", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
