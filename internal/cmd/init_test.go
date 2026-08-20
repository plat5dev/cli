package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPlat5YMLAuthDefaults(t *testing.T) {
	body := renderPlat5YML("demo", "", "", true, "", false, nil, nil)
	for _, want := range []string{
		"allowed_clients: [plat5]",
		"http://localhost:5173/callback",
		"https://oauth.pstmn.io/v1/callback",
		"http://localhost:5173",
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
}

func TestRenderPlat5YMLOtelWhenObservability(t *testing.T) {
	body := renderPlat5YML("demo", "", "", false, "", true, nil, nil)
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
