//go:build !e2e

package core

// IsE2EEnabled returns whether E2E test-specific features and fault injections are enabled.
// In production builds (without -tags e2e), this always returns false.
func IsE2EEnabled() bool {
	return false
}
