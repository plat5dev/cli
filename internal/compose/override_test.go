package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePlat5Override(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.override.yml")
	if err := WritePlat5Override(p, 5011, 5012, OverrideOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`ports: !override`,
		`"5011:5001"`,
		`"5012:5002"`,
		`gateway:`,
		`route-registry:`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "extra_hosts") {
		t.Fatalf("unexpected extra_hosts without HostGateway:\n%s", s)
	}
	if strings.Contains(s, "api-keys") {
		t.Fatalf("unexpected api-keys without HostGateway:\n%s", s)
	}
}

func TestWritePlat5OverrideHostGateway(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.override.yml")
	if err := WritePlat5Override(p, 5001, 5002, OverrideOpts{HostGateway: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`extra_hosts:`,
		`host.docker.internal:host-gateway`,
		`api-keys:`,
		`organizations:`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestWriteAuthOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.auth.override.yml")
	if err := WriteAuthOverride(p, 5100, OverrideOpts{}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, `"5100:5000"`) || !strings.Contains(s, `issuer:`) {
		t.Fatalf("unexpected:\n%s", s)
	}
	if strings.Contains(s, "extra_hosts") {
		t.Fatalf("unexpected extra_hosts:\n%s", s)
	}
}

func TestWriteAuthOverrideHostGateway(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.auth.override.yml")
	if err := WriteAuthOverride(p, 5000, OverrideOpts{HostGateway: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "extra_hosts:") || !strings.Contains(s, "host.docker.internal:host-gateway") {
		t.Fatalf("unexpected:\n%s", s)
	}
}

func TestResolveDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "docker-compose.yml"), []byte("name: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("empty path")
	}
	if _, err := ResolveDir(filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteObservabilityOverride(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "compose.observability.override.yml")
	if err := WriteObservabilityOverride(p, ObservabilityPorts{
		Grafana: 3102, OTLPGRPC: 4417, OTLPHTTP: 4418, Alloy: 13345,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{
		`ports: !override`,
		`127.0.0.1:3102:3000`,
		`127.0.0.1:4417:4317`,
		`127.0.0.1:4418:4318`,
		`127.0.0.1:13345:12345`,
		`grafana:`,
		`alloy:`,
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}
