package platform

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

type PolicyData struct {
	DesignVersion   string         `json:"design_version"`
	TaskFamilies    []string       `json:"task_families"`
	FSRS            map[string]any `json:"fsrs"`
	Observation     map[string]any `json:"observation_eligibility"`
	Selection       map[string]any `json:"selection"`
	Generation      map[string]any `json:"generation"`
	Raw             map[string]any `json:"-"`
}

type Config struct {
	DataDir        string
	ListenAddr     string
	PolicyPath     string
	Seed           string
	Policy         *PolicyData
	WorkerToken    string
	ContractVersion string
}

func ValidateAndResolveConfig(dataDir, listenAddr, policyPath, seed string) (*Config, error) {
	if dataDir == "" {
		dataDir = "data/local"
	}
	absDataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data-dir path: %w", err)
	}

	// Ensure data directory exists with 0700 permissions
	if err := os.MkdirAll(absDataDir, 0700); err != nil {
		return nil, fmt.Errorf("create data-dir: %w", err)
	}
	if err := os.Chmod(absDataDir, 0700); err != nil {
		return nil, fmt.Errorf("chmod data-dir: %w", err)
	}

	// Validate listen address
	if listenAddr == "" {
		listenAddr = "127.0.0.1:8080"
	}
	host, _, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid listen address %q: %w", listenAddr, err)
	}
	if host != "127.0.0.1" {
		return nil, fmt.Errorf("listen host must be strictly 127.0.0.1 (loopback only), got %q", host)
	}

	// Validate policy path
	if policyPath == "" || policyPath == "config/policy-v2.json" {
		if _, err := os.Stat("config/policy-v2.json"); err != nil {
			if _, err2 := os.Stat("../config/policy-v2.json"); err2 == nil {
				policyPath = "../config/policy-v2.json"
			}
		} else {
			policyPath = "config/policy-v2.json"
		}
	}
	absPolicyPath, err := filepath.Abs(policyPath)
	if err != nil {
		return nil, fmt.Errorf("resolve policy path: %w", err)
	}
	policyBytes, err := os.ReadFile(absPolicyPath)
	if err != nil {
		return nil, fmt.Errorf("read policy file %q: %w", policyPath, err)
	}

	var policy PolicyData
	if err := json.Unmarshal(policyBytes, &policy); err != nil {
		return nil, fmt.Errorf("parse policy json: %w", err)
	}
	if policy.DesignVersion == "" {
		return nil, errors.New("policy file missing design_version")
	}

	// Initialize or load worker token with 0600 permissions
	tokenPath := filepath.Join(absDataDir, "worker.token")
	var token string
	if existingToken, err := os.ReadFile(tokenPath); err == nil && len(strings.TrimSpace(string(existingToken))) > 0 {
		token = strings.TrimSpace(string(existingToken))
	} else {
		tokenBytes := make([]byte, 32)
		if _, err := rand.Read(tokenBytes); err != nil {
			return nil, fmt.Errorf("generate worker token: %w", err)
		}
		token = hex.EncodeToString(tokenBytes)
		if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0600); err != nil {
			return nil, fmt.Errorf("write worker token file: %w", err)
		}
	}
	_ = os.Chmod(tokenPath, 0600)

	return &Config{
		DataDir:         absDataDir,
		ListenAddr:      listenAddr,
		PolicyPath:      absPolicyPath,
		Seed:            seed,
		Policy:          &policy,
		WorkerToken:     token,
		ContractVersion: "1.1.0",
	}, nil
}
