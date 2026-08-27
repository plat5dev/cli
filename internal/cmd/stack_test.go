package cmd

import (
	"slices"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
)

func TestPlat5StackEnv(t *testing.T) {
	env := plat5StackEnv(config.Resolved{Plat5Version: "v1.2.3", APIKeyBrand: "acme"})
	want := map[string]string{
		"PLAT5_VERSION": "v1.2.3",
		"APIKEY_BRAND":  "acme",
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

func TestAuthStackEnvMinimal(t *testing.T) {
	env := authStackEnv(config.Resolved{AuthVersion: "v1.2.3"})
	if !slices.Contains(env, "AUTH_VERSION=v1.2.3") {
		t.Fatalf("version missing: %v", env)
	}
	for _, e := range env {
		if strings.HasPrefix(e, "AUTH_ALLOWED_") || strings.HasPrefix(e, "PUBLIC_ISSUER_URL=") {
			t.Fatalf("unexpected %q when OAuth surface unset", e)
		}
		if strings.HasPrefix(e, "AUTH_THEME_FILE=") {
			t.Fatalf("unexpected theme env when theme_file unset: %v", env)
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
	if _, ok := got["AUTH_THEME_FILE"]; ok {
		t.Fatalf("AUTH_THEME_FILE should be omitted without theme_file: %v", env)
	}
}

func TestAuthStackEnvThemeFile(t *testing.T) {
	env := authStackEnv(config.Resolved{
		AuthVersion:   "v9",
		AuthThemeFile: "/host/project/theme.json",
	})
	got := map[string]string{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("bad env entry %q", e)
		}
		got[k] = v
	}
	if got["AUTH_THEME_FILE"] != compose.AuthThemeContainerPath {
		t.Fatalf("AUTH_THEME_FILE got %q want in-container %q (env %v)", got["AUTH_THEME_FILE"], compose.AuthThemeContainerPath, env)
	}
	if got["AUTH_THEME_FILE"] == "/host/project/theme.json" {
		t.Fatal("must not pass host path as AUTH_THEME_FILE")
	}
	if _, ok := got["AUTH_LOGO_URL"]; ok {
		t.Fatal("must not set AUTH_LOGO_URL")
	}
	if _, ok := got["AUTH_FAVICON_URL"]; ok {
		t.Fatal("must not set AUTH_FAVICON_URL")
	}
	if _, ok := got["AUTH_DISPLAY_NAME"]; ok {
		t.Fatal("must not invent AUTH_DISPLAY_NAME")
	}
}
