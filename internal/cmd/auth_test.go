package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/plat5dev/cli/internal/auth"
)

func TestConfigClientAuthPrecedenceAndCachedLogin(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("PLAT5_CONTROL_PLANE_URL", "")
	t.Setenv("PLAT5_TOKEN", "")
	t.Setenv("PLAT5_ADMIN_TOKEN", "")
	t.Setenv("PLAT5_TOKEN_FILE", "")
	t.Setenv("PLAT5_TOKEN_CMD", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "")
	t.Setenv("PLAT5_OIDC_TOKEN", "")
	t.Chdir(t.TempDir())

	oldURL, oldTok, oldAdmin, oldFile, oldCmd := flagControlPlaneURL, flagToken, flagAdminToken, flagTokenFile, flagTokenCmd
	flagControlPlaneURL, flagToken, flagAdminToken, flagTokenFile, flagTokenCmd = "", "", "", "", ""
	t.Cleanup(func() {
		flagControlPlaneURL, flagToken, flagAdminToken, flagTokenFile, flagTokenCmd = oldURL, oldTok, oldAdmin, oldFile, oldCmd
	})

	// 1. Cached login session is picked up for its control-plane URL.
	store, err := auth.DefaultStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("https://cp.prod.example.com", &auth.StoredCredential{
		Issuer:      "https://idp.example.com",
		ClientID:    "cli",
		AccessToken: "cached-oidc-jwt",
		ExpiresAt:   time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	flagControlPlaneURL = "https://cp.prod.example.com"
	c, err := configClient()
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminToken != "cached-oidc-jwt" {
		t.Fatalf("expected cached token, got %q", c.AdminToken)
	}

	// 2. --token-file overrides cached login.
	tokFile := filepath.Join(t.TempDir(), "sa.jwt")
	_ = os.WriteFile(tokFile, []byte("file-jwt\n"), 0o600)
	flagTokenFile = tokFile
	c, err = configClient()
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminToken != "file-jwt" {
		t.Fatalf("expected token-file, got %q", c.AdminToken)
	}
	flagTokenFile = ""

	// 3. --token overrides everything.
	flagToken = "explicit-token"
	c, err = configClient()
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminToken != "explicit-token" {
		t.Fatalf("expected explicit token, got %q", c.AdminToken)
	}
	flagToken = ""

	// 4. GitLab CI OIDC token is picked up when no cached login exists.
	flagControlPlaneURL = "https://cp.ci.example.com"
	t.Setenv("PLAT5_OIDC_TOKEN", "gitlab-oidc-jwt")
	c, err = configClient()
	if err != nil {
		t.Fatal(err)
	}
	if c.AdminToken != "gitlab-oidc-jwt" {
		t.Fatalf("expected gitlab oidc token, got %q", c.AdminToken)
	}
}

func TestValidateAuthConfigOffline(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "control-plane-auth.yml")
	validYAML := `issuers:
  - name: github-actions
    issuer: https://token.actions.githubusercontent.com
    audiences: [https://cp.prod.example.com]
    rules:
      - access: read
        claims:
          repository: ["acme/*"]
      - access: apply
        claims:
          repository: [acme/platform]
          ref: [refs/heads/main]
`
	_ = os.WriteFile(validPath, []byte(validYAML), 0o644)

	oldAuthCfg := flagAuthConfig
	t.Cleanup(func() { flagAuthConfig = oldAuthCfg })

	flagAuthConfig = validPath
	out := captureStdout(t, func() {
		if err := runValidate(validateCmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "is valid: 1 issuers.") {
		t.Fatalf("unexpected output: %q", out)
	}

	// Rejects wildcard audience and bare "*" claim pattern.
	badPath := filepath.Join(dir, "bad-auth.yml")
	badYAML := `issuers:
  - name: gh
    issuer: https://token.actions.githubusercontent.com
    audiences: ["*"]
    rules:
      - access: apply
        claims:
          repository: ["*"]
`
	_ = os.WriteFile(badPath, []byte(badYAML), 0o644)
	flagAuthConfig = badPath
	var err error
	_ = captureStderr(t, func() { err = runValidate(validateCmd, nil) })
	if err == nil {
		t.Fatal("expected bad auth config to fail validation")
	}
}

func TestWhoAmIAndApplyChangeHeaders(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("GITHUB_SHA", "deadbeef1234")
	var gotReason, gotRef string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/whoami":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"actor": map[string]any{
					"type":        "jwt",
					"issuer":      "https://idp.example.com",
					"issuer_name": "okta",
					"subject":     "alice@example.com",
				},
				"access": "apply",
			})
		case "/apply":
			gotReason = r.Header.Get("X-Plat5-Change-Reason")
			gotRef = r.Header.Get("X-Plat5-Change-Ref")
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": 9, "summary": map[string]any{}})
		case "/status":
			_ = json.NewEncoder(w).Encode(map[string]any{"revision": 9, "gateway": []any{}})
		}
	}))
	defer srv.Close()

	oldURL, oldTok, oldReason, oldRef := flagControlPlaneURL, flagToken, flagApplyReason, flagApplyRef
	flagControlPlaneURL, flagToken, flagApplyReason, flagApplyRef = srv.URL, "tok", "INC-42 rollback", ""
	t.Cleanup(func() {
		flagControlPlaneURL, flagToken, flagApplyReason, flagApplyRef = oldURL, oldTok, oldReason, oldRef
	})

	out := captureStdout(t, func() {
		if err := runWhoAmI(whoamiCmd, nil); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "alice@example.com") || !strings.Contains(out, "Access:        apply") {
		t.Fatalf("unexpected whoami output: %q", out)
	}

	planPath := filepath.Join(t.TempDir(), "plan.json")
	_ = os.WriteFile(planPath, []byte(`{"kinds":{},"expect":{"revision":8}}`), 0o644)
	_ = captureStdout(t, func() {
		if err := runApply(applyCmd, []string{planPath}); err != nil {
			t.Fatal(err)
		}
	})
	if gotReason != "INC-42 rollback" || gotRef != "deadbeef1234" {
		t.Fatalf("expected headers reason/ref, got %q / %q", gotReason, gotRef)
	}
}
