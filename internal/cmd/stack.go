package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/plat5dev/cli/internal/bundle"
	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/state"
)

// resolvePlat5Stack returns the compose directory for Plat5.
// Path mode: exact plat5_compose dir. Image mode: materialize embedded stack under stateDir.
func resolvePlat5Stack(cfg config.Resolved, stateDir string) (dir string, imageMode bool, err error) {
	if cfg.Plat5Compose != "" {
		dir, err = compose.ResolveDir(cfg.Plat5Compose)
		if err != nil {
			return "", false, fmt.Errorf("plat5_compose: %w", err)
		}
		return dir, false, nil
	}
	dir = filepath.Join(stateDir, "bundle", "plat5")
	if err := bundle.MaterializePlat5(dir); err != nil {
		return "", true, fmt.Errorf("materialize plat5 bundle: %w", err)
	}
	return dir, true, nil
}

// resolveAuthStack returns the compose directory for Auth.
// Path mode when auth_compose set; otherwise embedded image stack.
func resolveAuthStack(cfg config.Resolved, stateDir string) (dir string, imageMode bool, err error) {
	if cfg.AuthCompose != "" {
		dir, err = compose.ResolveDir(cfg.AuthCompose)
		if err != nil {
			return "", false, fmt.Errorf("auth_compose: %w", err)
		}
		return dir, false, nil
	}
	dir = filepath.Join(stateDir, "bundle", "auth")
	if err := bundle.MaterializeAuth(dir); err != nil {
		return "", true, fmt.Errorf("materialize auth bundle: %w", err)
	}
	return dir, true, nil
}

// resolveObservabilityStack returns the compose directory for observability.
func resolveObservabilityStack(cfg config.Resolved, stateDir string) (dir string, imageMode bool, err error) {
	if cfg.ObservabilityCompose != "" {
		dir, err = compose.ResolveDir(cfg.ObservabilityCompose)
		if err != nil {
			return "", false, fmt.Errorf("observability_compose: %w", err)
		}
		return dir, false, nil
	}
	dir = filepath.Join(stateDir, "bundle", "observability")
	if err := bundle.MaterializeObservability(dir); err != nil {
		return "", true, fmt.Errorf("materialize observability bundle: %w", err)
	}
	return dir, true, nil
}

// resolvePlat5StackForOps prefers a valid state path, else config/path or re-materialize.
func resolvePlat5StackForOps(cfg config.Resolved, st state.State, stateDir string) (string, error) {
	if st.Plat5Compose != "" && compose.ValidateComposeDir(st.Plat5Compose) == nil {
		return st.Plat5Compose, nil
	}
	dir, _, err := resolvePlat5Stack(cfg, stateDir)
	return dir, err
}

func resolveAuthStackForOps(cfg config.Resolved, st state.State, stateDir string) (string, error) {
	if st.AuthCompose != "" && compose.ValidateComposeDir(st.AuthCompose) == nil {
		return st.AuthCompose, nil
	}
	if cfg.AuthCompose == "" && !cfg.AuthEnabled && !st.StartedAuth {
		return "", nil
	}
	dir, _, err := resolveAuthStack(cfg, stateDir)
	return dir, err
}

func resolveObservabilityStackForOps(cfg config.Resolved, st state.State, stateDir string) (string, error) {
	if st.ObservabilityCompose != "" && compose.ValidateComposeDir(st.ObservabilityCompose) == nil {
		return st.ObservabilityCompose, nil
	}
	if cfg.ObservabilityCompose == "" && !cfg.ObservabilityEnabled && !st.StartedObservability {
		return "", nil
	}
	dir, _, err := resolveObservabilityStack(cfg, stateDir)
	return dir, err
}

func plat5VersionEnv(cfg config.Resolved) []string {
	return []string{fmt.Sprintf("PLAT5_VERSION=%s", cfg.Plat5Version)}
}

func plat5StackEnv(cfg config.Resolved) []string {
	return append(plat5VersionEnv(cfg), "APIKEY_BRAND="+cfg.APIKeyBrand)
}

func authVersionEnv(cfg config.Resolved) []string {
	return []string{fmt.Sprintf("AUTH_VERSION=%s", cfg.AuthVersion)}
}

// authStackEnv is compose env for the Auth issuer (version + project OAuth surface).
func authStackEnv(cfg config.Resolved) []string {
	env := append([]string{}, authVersionEnv(cfg)...)
	if len(cfg.AuthAllowedClients) > 0 {
		env = append(env, "AUTH_ALLOWED_CLIENTS="+strings.Join(cfg.AuthAllowedClients, ","))
	}
	if len(cfg.AuthAllowedRedirectURIs) > 0 {
		env = append(env, "AUTH_ALLOWED_REDIRECT_URIS="+strings.Join(cfg.AuthAllowedRedirectURIs, ","))
	}
	if len(cfg.AuthAllowedOrigins) > 0 {
		env = append(env, "AUTH_ALLOWED_ORIGINS="+strings.Join(cfg.AuthAllowedOrigins, ","))
	}
	if cfg.AuthPublicIssuerURL != "" {
		env = append(env, "PUBLIC_ISSUER_URL="+cfg.AuthPublicIssuerURL)
	}
	return env
}
