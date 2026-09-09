//go:build e2e

package core

// IsE2EEnabled returns whether E2E test-specific features and fault injections are enabled.
// In test builds (compiled with -tags e2e), this returns true.
func IsE2EEnabled() bool {
	return true
}
