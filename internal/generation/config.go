package generation

import (
	"encoding/json"
	"os"
)

type PolicyGenerationConfig struct {
	Enabled                     bool   `json:"enabled"`
	FormsPerUnit                int    `json:"forms_per_unit"`
	SemanticEvaluations         int    `json:"semantic_evaluations"`
	MaxSemanticRevisions        int    `json:"max_semantic_revisions"`
	MaxNetworkAttemptsPerCall   int    `json:"max_network_attempts_per_call"`
	RequestTimeoutSeconds       int    `json:"request_timeout_seconds"`
	LeaseSeconds                int    `json:"lease_seconds"`
	RenewEverySeconds           int    `json:"renew_every_seconds"`
	MaxConcurrentItems          int    `json:"max_concurrent_items"`
	MaxUnitsPerJob              int    `json:"max_units_per_job"`
	MaxRequestsPerJob           int    `json:"max_requests_per_job"`
	MaxTokensPerJob             int    `json:"max_tokens_per_job"`
	BudgetPolicyVersion         string `json:"budget_policy_version"`
	UnknownCallsKeepReservation bool   `json:"unknown_calls_keep_reservation"`
}

func DefaultGenerationConfig() PolicyGenerationConfig {
	return PolicyGenerationConfig{
		Enabled:                     false,
		FormsPerUnit:                3,
		SemanticEvaluations:         2,
		MaxSemanticRevisions:        2,
		MaxNetworkAttemptsPerCall:   3,
		RequestTimeoutSeconds:       45,
		LeaseSeconds:                120,
		RenewEverySeconds:           30,
		MaxConcurrentItems:          2,
		MaxUnitsPerJob:              100,
		MaxRequestsPerJob:           100,
		MaxTokensPerJob:             200000,
		BudgetPolicyVersion:         "budget-v2",
		UnknownCallsKeepReservation: true,
	}
}

func LoadGenerationConfigFromPath(path string) (*PolicyGenerationConfig, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		def := DefaultGenerationConfig()
		return &def, err
	}

	var root struct {
		Generation PolicyGenerationConfig `json:"generation"`
	}
	if err := json.Unmarshal(bytes, &root); err != nil {
		def := DefaultGenerationConfig()
		return &def, err
	}

	cfg := root.Generation
	if cfg.FormsPerUnit <= 0 {
		cfg.FormsPerUnit = 3
	}
	if cfg.LeaseSeconds <= 0 {
		cfg.LeaseSeconds = 120
	}
	if cfg.MaxNetworkAttemptsPerCall <= 0 {
		cfg.MaxNetworkAttemptsPerCall = 3
	}
	if cfg.MaxRequestsPerJob <= 0 {
		cfg.MaxRequestsPerJob = 100
	}
	if cfg.MaxTokensPerJob <= 0 {
		cfg.MaxTokensPerJob = 200000
	}
	if cfg.BudgetPolicyVersion == "" {
		cfg.BudgetPolicyVersion = "budget-v2"
	}
	return &cfg, nil
}
