package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/plat5dev/cli/internal/xdg"
)

// State records what the CLI started for one project.
type State struct {
	ProjectID                string    `json:"project_id"`
	ConfigPath               string    `json:"config_path,omitempty"`
	Plat5Compose             string    `json:"plat5_compose,omitempty"`
	AuthCompose              string    `json:"auth_compose,omitempty"`
	ObservabilityCompose     string    `json:"observability_compose,omitempty"`
	StartedAuth              bool      `json:"started_auth"`
	StartedObservability     bool      `json:"started_observability"`
	ComposeProject           string    `json:"compose_project,omitempty"`
	AuthComposeName          string    `json:"auth_compose_name,omitempty"`
	ObservabilityComposeName string    `json:"observability_compose_name,omitempty"`
	Plat5Override            string    `json:"plat5_override,omitempty"`
	AuthOverride             string    `json:"auth_override,omitempty"`
	ObservabilityOverride    string    `json:"observability_override,omitempty"`
	GatewayPort              int       `json:"gateway_port,omitempty"`
	RegistryPort             int       `json:"registry_port,omitempty"`
	AuthPort                 int       `json:"auth_port,omitempty"`
	GrafanaPort              int       `json:"grafana_port,omitempty"`
	OTLPGRPCPort             int       `json:"otlp_grpc_port,omitempty"`
	OTLPHTTPPort             int       `json:"otlp_http_port,omitempty"`
	AlloyPort                int       `json:"alloy_port,omitempty"`
	StartedAt                time.Time `json:"started_at"`
}

func projectPath(projectID string) (string, error) {
	dir, err := xdg.ProjectDir(projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

// Load reads state for projectID; empty state if missing.
func Load(projectID string) (State, error) {
	p, err := projectPath(projectID)
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return State{}, nil
		}
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("parse state: %w", err)
	}
	return s, nil
}

// Save writes state for the project.
func Save(s State) error {
	if s.ProjectID == "" {
		return fmt.Errorf("state: empty project_id")
	}
	dir, err := xdg.ProjectDir(s.ProjectID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, "state.json")
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}

// Clear removes state file for projectID.
func Clear(projectID string) error {
	p, err := projectPath(projectID)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Dir returns the project state directory.
func Dir(projectID string) (string, error) {
	return xdg.ProjectDir(projectID)
}
