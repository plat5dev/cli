package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/state"
)

// loadOwned is the project's state, refused when another folder owns its id.
func loadOwned(cfg config.Resolved) (state.State, error) {
	return guardOwner(cfg.ProjectID, cfg.Dir, false)
}

// guardOwner loads projectID's state for the project in folder dir, and refuses
// when another folder owns the id: one folder per project id, since the id names
// both compose projects, the stack's and the app's, their volumes, and the state. A state with no owner yet
// passes; start records one (claimOwner). forInit words the fix for plat5 init,
// which takes the id from --project-id, else the folder's name.
func guardOwner(projectID, dir string, forInit bool) (state.State, error) {
	st, err := state.Load(projectID)
	if err != nil {
		return st, err
	}
	here := projectFolder(dir)
	if st.Owner == "" || st.Owner == here {
		return st, nil
	}
	msg := fmt.Sprintf("project id %q belongs to the project in %s, not this one in %s: the stack and the app (compose projects plat5-%s and %s, their volumes) and the state are that project's.\n",
		projectID, st.Owner, here, projectID, projectID)
	other := otherProjectID(projectID, here)
	if forInit {
		return st, fmt.Errorf("%sGive this project its own id:\n\n  plat5 init --project-id %s", msg, other)
	}
	msg += fmt.Sprintf("Give this project its own id in %s:", filepath.Join(here, config.FileName))
	return st, fmt.Errorf("%s\n\n  project_id: %s", msg, other)
}

// claimOwner records dir as the owner of a state that has none.
func claimOwner(st *state.State, dir string) error {
	if st.Owner != "" {
		return nil
	}
	st.Owner = projectFolder(dir)
	return state.Save(*st)
}

// otherProjectID suggests an id for the project in dir: its parent's name and
// its own.
func otherProjectID(id, dir string) string {
	s := config.SanitizeProjectID(filepath.Base(filepath.Dir(dir)) + "-" + filepath.Base(dir))
	if s == "" || s == id {
		return id + "-2"
	}
	return s
}

// projectFolder is dir as an owner is recorded: absolute and clean, with
// symlinks resolved as far as the path exists (macOS's /tmp is /private/tmp).
func projectFolder(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Clean(dir)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	parent := filepath.Dir(abs)
	if parent == abs {
		return abs
	}
	return filepath.Join(projectFolder(parent), filepath.Base(abs))
}
