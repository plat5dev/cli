package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plugin"
	"github.com/plat5dev/cli/internal/registry"
)

func siblingResolver(t *testing.T) *registry.Resolver {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "registry", "registry.yml")); err != nil {
		t.Skip("no sibling registry checkout")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	return &registry.Resolver{SourcesDir: root, CLIVersion: "0.20.0"}
}

func mustLoad(t *testing.T, res *registry.Resolver, ref, slot, as string, prompts map[string]string) *plugin.Plugin {
	t.Helper()
	p, err := res.Resolve(ref)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", ref, err)
	}
	t.Cleanup(p.Close)
	if slot != "" {
		p.Slot = slot
	}
	if as != "" {
		p.ServiceName = as
	}
	for k, v := range prompts {
		p.Prompts[k] = v
	}
	return p
}

func TestMatrixBuildAndValidate(t *testing.T) {
	res := siblingResolver(t)
	baseSpec := Spec{
		ProjectID: "acme-cloud",
		Versions:  map[string]string{"plat5": "v0.19.0", "auth": "v0.4.0", "operator": "v0.9.0"},
	}

	t.Run("enterprise + auth0 + cerbos + go-fiber + okta-op + grafana-cloud + ci + overlays", func(t *testing.T) {
		plugins := []*plugin.Plugin{
			mustLoad(t, res, "enterprise", plugin.SlotPreset, "", nil),
			mustLoad(t, res, "auth0", plugin.SlotAuth, "", map[string]string{
				"domain": "acme.us.auth0.com", "audience": "https://api.acme.com", "client_id": "spa123",
			}),
			mustLoad(t, res, "cerbos", plugin.SlotAuthZ, "", nil),
			mustLoad(t, res, "go-fiber-api", plugin.SlotService, "api", nil),
			mustLoad(t, res, "solid-customer-console", plugin.SlotCustomerConsole, "", nil),
			mustLoad(t, res, "solid-operator-console", plugin.SlotOperatorConsole, "", nil),
			mustLoad(t, res, "okta", plugin.SlotOperatorAuth, "", map[string]string{
				"issuer": "https://acme.okta.com/oauth2/default", "audience": "plat5-op", "client_id": "op123",
			}),
			mustLoad(t, res, "cerbos", plugin.SlotOperatorAuthZ, "", nil),
			mustLoad(t, res, "grafana-cloud", plugin.SlotObservability, "", map[string]string{
				"otlp_endpoint": "https://otlp.grafana.net/otlp", "instance_id": "12345", "api_token": "glc_supersecret",
			}),
			mustLoad(t, res, "github-actions", plugin.SlotCI, "", nil),
			mustLoad(t, res, "env-overlays", plugin.SlotDeploy, "", nil),
		}
		plan, err := Build(baseSpec, plugins, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		dir := t.TempDir()
		if _, err := plan.Apply(dir, false); err != nil {
			t.Fatalf("Apply: %v", err)
		}

		// Verify secret hygiene: glc_supersecret MUST be in .env.local (0600) and NEVER in plat5.local.yml or .env.example.
		localYML, _ := os.ReadFile(filepath.Join(dir, config.FileName))
		if strings.Contains(string(localYML), "glc_supersecret") {
			t.Fatal("secret leaked into plat5.local.yml!")
		}
		envEx, _ := os.ReadFile(filepath.Join(dir, ".env.example"))
		if strings.Contains(string(envEx), "glc_supersecret") {
			t.Fatal("secret leaked into .env.example!")
		}
		envLoc, err := os.ReadFile(filepath.Join(dir, ".env.local"))
		if err != nil || !strings.Contains(string(envLoc), "GRAFANA_CLOUD_TOKEN=glc_supersecret") {
			t.Fatalf(".env.local missing secret: %v %q", err, string(envLoc))
		}
		if fi, _ := os.Stat(filepath.Join(dir, ".env.local")); fi.Mode().Perm() != 0o600 {
			t.Fatalf(".env.local mode = %o, want 0600", fi.Mode().Perm())
		}

		// Verify plat5.local.yml actually resolves cleanly with config.Resolve!
		f, err := config.Parse(localYML)
		if err != nil {
			t.Fatalf("config.Parse: %v\n%s", err, localYML)
		}
		resolved, err := config.Resolve(f, filepath.Join(dir, config.FileName), config.Overrides{})
		if err != nil {
			t.Fatalf("config.Resolve: %v\n%s", err, localYML)
		}
		if resolved.IdP.Issuer != "https://acme.us.auth0.com/" || resolved.OperatorIdP.Issuer != "https://acme.okta.com/oauth2/default" {
			t.Fatalf("resolved IdPs: cust=%+v op=%+v", resolved.IdP, resolved.OperatorIdP)
		}
		if resolved.ObservabilityMode != config.ObservabilityAlloyOnly || resolved.InfraCompose == "" {
			t.Fatalf("obs=%q infra=%q", resolved.ObservabilityMode, resolved.InfraCompose)
		}

		// Verify GitHub Actions ${{ ... }} survived intact!
		wf, err := os.ReadFile(filepath.Join(dir, ".github", "workflows", "plat5-config.yml"))
		if err != nil || !strings.Contains(string(wf), "${{ github.sha }}") {
			t.Fatalf("workflow corrupted: %v\n%s", err, wf)
		}
	})

	t.Run("saas + topaz + bun-effect adapts permission_separator to underscore", func(t *testing.T) {
		plugins := []*plugin.Plugin{
			mustLoad(t, res, "saas", plugin.SlotPreset, "", nil),
			mustLoad(t, res, "topaz", plugin.SlotAuthZ, "", nil),
			mustLoad(t, res, "bun-effect-api", plugin.SlotService, "api", nil),
		}
		plan, err := Build(baseSpec, plugins, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		dir := t.TempDir()
		if _, err := plan.Apply(dir, false); err != nil {
			t.Fatal(err)
		}
		apiSvc, _ := os.ReadFile(filepath.Join(dir, "services", "api.yml"))
		if !strings.Contains(string(apiSvc), "project_read") || strings.Contains(string(apiSvc), "project:read") {
			t.Fatalf("api.yml should use underscore separator:\n%s", apiSvc)
		}
		idSvc, _ := os.ReadFile(filepath.Join(dir, "services", "identity.yml"))
		if !strings.Contains(string(idSvc), "organization_read") {
			t.Fatalf("identity.yml should use underscore separator:\n%s", idSvc)
		}
	})
}

func TestDay2AddPreservesCommentsAndMultiService(t *testing.T) {
	res := siblingResolver(t)
	spec := Spec{
		ProjectID: "day2",
		Versions:  map[string]string{"plat5": "v0.19.0", "auth": "v0.4.0", "operator": "v0.9.0"},
	}
	initPlan, err := Build(spec, []*plugin.Plugin{
		mustLoad(t, res, "saas", plugin.SlotPreset, "", nil),
		mustLoad(t, res, "go-fiber-api", plugin.SlotService, "api", nil),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if _, err := initPlan.Apply(dir, false); err != nil {
		t.Fatal(err)
	}

	// Simulate user hand-editing plat5.yml with a custom comment!
	p5Path := filepath.Join(dir, "plat5.yml")
	orig, _ := os.ReadFile(p5Path)
	withComment := "# MY IMPORTANT CUSTOM COMMENT\n" + string(orig)
	if err := os.WriteFile(p5Path, []byte(withComment), 0o644); err != nil {
		t.Fatal(err)
	}

	// Day-2 add: github-actions + env-overlays must preserve user's custom comment in plat5.yml!
	existing, err := LoadExisting(dir)
	if err != nil {
		t.Fatal(err)
	}
	addPlan, err := Build(spec, []*plugin.Plugin{
		mustLoad(t, res, "github-actions", plugin.SlotCI, "", nil),
		mustLoad(t, res, "env-overlays", plugin.SlotDeploy, "", nil),
	}, existing)
	if err != nil {
		t.Fatalf("day-2 add Build: %v", err)
	}
	if _, err := addPlan.Apply(dir, false); err != nil {
		t.Fatalf("day-2 add Apply: %v", err)
	}
	after, _ := os.ReadFile(p5Path)
	if !strings.Contains(string(after), "# MY IMPORTANT CUSTOM COMMENT") {
		t.Fatalf("user comment lost on Day-2 add:\n%s", after)
	}

	// Day-2 add: adding a duplicate service name ("api") must fail cleanly before writing!
	existing, _ = LoadExisting(dir)
	if _, err := Build(spec, []*plugin.Plugin{
		mustLoad(t, res, "go-fiber-api", plugin.SlotService, "api", nil),
	}, existing); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate service failure, got %v", err)
	}
}
