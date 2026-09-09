package core

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrIdempotencyConflict = errors.New("idempotency key conflict with differing request digest")
)

type IdempotencyRecord struct {
	Operation     string
	ResourceScope string
	Key           string
	RequestDigest string
	Status        string
	ResponseCode  int
	ResponseBody  string
	CreatedAt     int64
}

// CanonicalizeJSON parses JSON and reserializes it with sorted keys and compact encoding.
func CanonicalizeJSON(raw []byte) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("{}"), nil
	}
	var val any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&val); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	return json.Marshal(val)
}

// ComputeRequestDigest computes SHA-256 hex digest of canonical compact JSON.
func ComputeRequestDigest(body []byte) (string, error) {
	canonical, err := CanonicalizeJSON(body)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(canonical)
	return hex.EncodeToString(h[:]), nil
}

// CheckIdempotency checks if an operation with (operation, resourceScope, key) was already committed.
// If it exists:
// - If digest matches: returns record and nil error.
// - If digest differs: returns nil record and ErrIdempotencyConflict.
// If it does not exist: returns nil, nil.
func CheckIdempotency(tc *TxnContext, operation, resourceScope, key, requestDigest string) (*IdempotencyRecord, error) {
	query := `
		SELECT operation, resource_scope, key, request_digest, status, response_code, response_body, created_at
		FROM idempotency_record
		WHERE operation = ? AND resource_scope = ? AND key = ?
	`
	row := tc.QueryRow(query, operation, resourceScope, key)

	var rec IdempotencyRecord
	err := row.Scan(
		&rec.Operation,
		&rec.ResourceScope,
		&rec.Key,
		&rec.RequestDigest,
		&rec.Status,
		&rec.ResponseCode,
		&rec.ResponseBody,
		&rec.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query idempotency record: %w", err)
	}

	if rec.RequestDigest != requestDigest {
		return nil, ErrIdempotencyConflict
	}

	return &rec, nil
}

// SaveIdempotencyRecord stores a successful response into idempotency_record.
func SaveIdempotencyRecord(tc *TxnContext, rec *IdempotencyRecord) error {
	query := `
		INSERT INTO idempotency_record (
			operation, resource_scope, key, request_digest, status, response_code, response_body, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`
	_, err := tc.Exec(query,
		rec.Operation,
		rec.ResourceScope,
		rec.Key,
		rec.RequestDigest,
		rec.Status,
		rec.ResponseCode,
		rec.ResponseBody,
		rec.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert idempotency record: %w", err)
	}
	return nil
}
