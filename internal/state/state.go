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
	ProjectID string `json:"project_id"`
	// Path is the project's plat5.dev.yml.
	Path string `json:"path,omitempty"`
	// Modules is the modules the last start ran (core is implied).
	Modules []string `json:"modules,omitempty"`
	// AppCompose is the app compose file the last start ran. Empty: none.
	AppCompose string `json:"app_compose,omitempty"`
	// AppJoined is the app's services the last start put on the Plat5 network.
	AppJoined []string  `json:"app_joined,omitempty"`
	Dev       *Dev      `json:"dev,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// Dev is the seeded dev person, their Dev nodes, and a personal key. Reused while
// the key still works.
type Dev struct {
	Email string `json:"email"`
	// UserID is the dev person's user id.
	UserID string    `json:"user_id"`
	Nodes  []DevNode `json:"nodes,omitempty"`
	Key    string    `json:"key"`
}

// DevNode is a seeded root: its kind, id, slug, and path at the gateway.
type DevNode struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Path string `json:"path"`
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
