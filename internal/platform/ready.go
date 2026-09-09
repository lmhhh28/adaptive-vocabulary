package platform

import (
	"encoding/json"
	"fmt"
	"os"
)

type ReadySignal struct {
	Ready           bool           `json:"ready"`
	BaseURL         string         `json:"base_url"`
	PID             int            `json:"pid"`
	SchemaVersion   int            `json:"schema_version"`
	ContractVersion string         `json:"contract_version"`
	PolicyVersions  map[string]any `json:"policy_versions"`
	DataDir         string         `json:"data_dir"`
}

func PrintReadySignal(signal ReadySignal) error {
	line, err := json.Marshal(signal)
	if err != nil {
		return fmt.Errorf("marshal ready signal: %w", err)
	}
	fmt.Fprintln(os.Stdout, string(line))
	return nil
}
