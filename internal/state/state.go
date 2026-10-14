// Package state is what the CLI remembers about one project's local stack, under
// $XDG_STATE_HOME/plat5/projects/<project_id>: what the last start ran, and the dev
// data it seeded. Local only, and written 0600: it holds the dev keys.
package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/plat5dev/cli/internal/xdg"
)

// State records what plat5 start did for one project.
type State struct {
	ProjectID  string `json:"project_id"`
	ConfigPath string `json:"config_path,omitempty"`
	// Modules is the modules the last start ran (core is implied).
	Modules []string `json:"modules,omitempty"`
	// AppCompose is the app compose file the last start ran. Empty: none.
	AppCompose string    `json:"app_compose,omitempty"`
	Dev        *Dev      `json:"dev,omitempty"`
	StartedAt  time.Time `json:"started_at"`
}

// Dev is the seeded dev person, their org, and a key for each. Reused while
// the keys still work.
type Dev struct {
	Email          string `json:"email"`
	UserID         string `json:"user_id"`
	OrganizationID string `json:"organization_id"`
	MemberID       string `json:"member_id"`
	UserKey        string `json:"user_key"`
	MemberKey      string `json:"member_key"`
}

// Dir is the project's state directory.
func Dir(projectID string) (string, error) {
	return xdg.ProjectDir(projectID)
}

func path(projectID string) (string, error) {
	dir, err := Dir(projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "state.json"), nil
}

// Load reads the project's state. None yet is an empty State.
func Load(projectID string) (State, error) {
	p, err := path(projectID)
	if err != nil {
		return State{}, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return State{ProjectID: projectID}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// Written by an older CLI, or damaged: start over rather than fail.
		return State{ProjectID: projectID}, nil
	}
	s.ProjectID = projectID
	return s, nil
}

// Save writes the project's state.
func Save(s State) error {
	if s.ProjectID == "" {
		return fmt.Errorf("state: empty project_id")
	}
	p, err := path(s.ProjectID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
