package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
)

func TestAuthStackEnvMinimal(t *testing.T) {
	env := authStackEnv(config.Resolved{AuthVersion: "v1.2.3"})
	if !slices.Contains(env, "AUTH_VERSION=v1.2.3") {
		t.Fatalf("version missing: %v", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "AUTH_ALLOWED_") || strings.HasPrefix(e, "PUBLIC_ISSUER_URL=") {
			t.Fatalf("unexpected %q when OAuth surface unset", e)
		}
	}
}

func TestAuthStackEnvFull(t *testing.T) {
	env := authStackEnv(config.Resolved{
		AuthVersion:             "v9",
		AuthAllowedClients:      []string{"my-app", "cli"},
		AuthAllowedRedirectURIs: []string{"http://localhost:3000/callback", "https://oauth.pstmn.io/v1/callback"},
		AuthAllowedOrigins:      []string{"http://localhost:3000"},
		AuthPublicIssuerURL:     "http://localhost:5100",
	})
	want := map[string]string{
		"AUTH_VERSION":               "v9",
		"AUTH_ALLOWED_CLIENTS":       "my-app,cli",
		"AUTH_ALLOWED_REDIRECT_URIS": "http://localhost:3000/callback,https://oauth.pstmn.io/v1/callback",
		"AUTH_ALLOWED_ORIGINS":       "http://localhost:3000",
		"PUBLIC_ISSUER_URL":          "http://localhost:5100",
	}
	got := map[string]string{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("bad env entry %q", e)
		}
		got[k] = v
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s: got %q want %q (env %v)", k, got[k], v, env)
		}
	}
}
