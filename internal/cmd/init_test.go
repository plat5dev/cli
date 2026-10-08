package cmd

import (
	"github.com/plat5dev/cli/internal/bundle"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestIdentityRoutesCatalogIncludesInvites(t *testing.T) {
	for _, want := range []string{
		"/org/invites",
		"/org/invites/{invite_id}",
		"/user/invites/redeem",
		"/user/organizations/{organization_id}/session",
		"/org/service-accounts/{service_account_id}/api-keys",
		"/org/service-accounts/{service_account_id}/api-keys/{key_id}",
		"/organizations/{subject.organization_id}/service-accounts/{path.service_account_id}/api-keys",
	} {
		if !strings.Contains(identityRoutesCatalog, want) {
			t.Fatalf("identity catalog missing %q", want)
		}
	}
}

func TestRenderPlat5YMLAuthDefaults(t *testing.T) {
	body := renderPlat5YML("demo", "", "", true, "", false, "", false, nil, nil)
	for _, want := range []string{
		"allowed_clients: [plat5]",
		"http://localhost:5173/callback",
		"https://oauth.pstmn.io/v1/callback",
		"http://localhost:5173",
		"version: " + bundle.DefaultAuthVersion,
		"plat5_version: v0.4.1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "otel:\n  endpoint:") {
		t.Fatalf("otel should stay commented when observability off:\n%s", body)
	}
	if !strings.Contains(body, "# apikey_brand: plat5") {
		t.Fatalf("missing commented apikey_brand:\n%s", body)
	}
	if strings.Contains(body, "theme_file") {
		t.Fatalf("init --auth must not invent a theme file:\n%s", body)
	}
	if strings.Contains(body, "bootstrap_") {
		t.Fatalf("operator has no bootstrap login:\n%s", body)
	}
	if !strings.Contains(body, "operator:\n  enabled: false") {
		t.Fatalf("operator block missing:\n%s", body)
	}
	if strings.Contains(body, "version: v0.1.8") {
		t.Fatalf("stale auth.version v0.1.8:\n%s", body)
	}
	if strings.Contains(body, "plat5_version: v0.2.0") {
		t.Fatalf("stale plat5_version v0.2.0:\n%s", body)
	}
}

func TestRenderPlat5YMLOperator(t *testing.T) {
	body := renderPlat5YML("demo", "", "", false, "", false, "/tmp/operator/compose", true, nil, nil)
	for _, want := range []string{
		"operator_compose: /tmp/operator/compose",
		"operator:\n  enabled: true",
		"version: v0.4.0",
		"allowed_origins:\n    - http://localhost:5173",
		"#   operator: 5004",
		"#   operator_idp: 5556",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "bootstrap_") {
		t.Fatalf("operator has no bootstrap login:\n%s", body)
	}
}

func TestRenderPlat5YMLOtelWhenObservability(t *testing.T) {
	body := renderPlat5YML("demo", "", "", false, "", true, "", false, nil, nil)
	if !strings.Contains(body, "otel:\n  endpoint: http://host.docker.internal:4318") {
		t.Fatalf("expected active otel block:\n%s", body)
	}
	if strings.Contains(body, "# otel:") {
		t.Fatalf("otel should not be commented when observability on:\n%s", body)
	}
}

func TestUncommentOTLPEndpoint(t *testing.T) {
	src := `    environment:
      PORT: 3000
      # OTLP destination (traces + metrics default on when set):
      # OTEL_EXPORTER_OTLP_ENDPOINT: http://host.docker.internal:4318
`
	out, n := uncommentOTLPEndpoint(src)
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	if !strings.Contains(out, "      OTEL_EXPORTER_OTLP_ENDPOINT: http://host.docker.internal:4318") {
		t.Fatalf("out:\n%s", out)
	}
	if strings.Contains(out, "# OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Fatal("still commented")
	}
	// comment-only description line stays
	if !strings.Contains(out, "# OTLP destination") {
		t.Fatal("description comment should remain")
	}
}

func TestEnableTemplateOTLP(t *testing.T) {
	root := t.TempDir()
	compose := filepath.Join(root, "docker-compose.yml")
	body := `services:
  api:
    environment:
      # OTEL_EXPORTER_OTLP_ENDPOINT: http://host.docker.internal:4318
`
	if err := os.WriteFile(compose, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := enableTemplateOTLP(root)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("changed %d", n)
	}
	got, err := os.ReadFile(compose)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "      OTEL_EXPORTER_OTLP_ENDPOINT: http://host.docker.internal:4318") {
		t.Fatalf("%s", got)
	}
}

func TestIdentityRoutesCatalogLabelsOrgWrites(t *testing.T) {
	for _, want := range []string{
		"/org/members/{member_id}",
		"/organizations/{subject.organization_id}/members/{path.member_id}",
		"/org/roles",
		"required_labels: [org:write]",
		"required_labels: [org:delete]",
		"required_labels: [org:members:write]",
		"required_labels: [org:service-accounts:write]",
	} {
		if !strings.Contains(identityRoutesCatalog, want) {
			t.Fatalf("identity catalog missing %q", want)
		}
	}
}

func TestRenderPlat5YMLRoles(t *testing.T) {
	body := renderPlat5YML("demo", "", "", false, "", false, "", false, nil, nil)
	if !strings.Contains(body, "\nroles: ./roles.yml\n") {
		t.Fatalf("plat5.yml should point at roles.yml:\n%s", body)
	}
}

func TestStarterRolesShape(t *testing.T) {
	var doc struct {
		Roles       map[string][]string `yaml:"roles"`
		CreatorRole string              `yaml:"creator_role"`
		DefaultRole string              `yaml:"default_role"`
	}
	if err := yaml.Unmarshal([]byte(starterRoles), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Roles[doc.CreatorRole]; !ok {
		t.Fatalf("creator_role %q is not a role", doc.CreatorRole)
	}
	if _, ok := doc.Roles[doc.DefaultRole]; !ok {
		t.Fatalf("default_role %q is not a role", doc.DefaultRole)
	}
	if got := doc.Roles["owner"]; len(got) != 1 || got[0] != "*" {
		t.Fatalf("owner: %v", got)
	}
}
