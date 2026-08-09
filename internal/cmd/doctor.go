package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/registry"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check local prerequisites and project config",
	RunE:  runDoctor,
}

func runDoctor(cmd *cobra.Command, args []string) error {
	failed := false
	check := func(name string, err error) {
		if err != nil {
			fmt.Printf("✗ %s: %v\n", name, err)
			failed = true
			return
		}
		fmt.Printf("✓ %s\n", name)
	}

	check("docker compose", compose.DockerAvailable())

	cfg, err := loadConfig()
	if err != nil {
		var np config.ErrNoProject
		if errors.As(err, &np) {
			check("plat5.yml", err)
			return doctorExit(failed)
		}
		check("config", err)
		return doctorExit(failed)
	}
	fmt.Printf("✓ plat5.yml: %s (project %s)\n", cfg.ConfigPath, cfg.ProjectID)

	fmt.Printf("✓ plat5_version: %s\n", cfg.Plat5Version)
	if cfg.Plat5Compose == "" {
		fmt.Println("✓ plat5: published images (GHCR)")
	} else if dir, err := compose.ResolveDir(cfg.Plat5Compose); err != nil {
		check("plat5_compose", err)
	} else {
		fmt.Printf("✓ plat5_compose: %s\n", dir)
	}

	if cfg.AuthEnabled || cfg.AuthCompose != "" {
		fmt.Printf("✓ auth.version: %s\n", cfg.AuthVersion)
		if cfg.AuthCompose == "" {
			fmt.Println("✓ auth: published images (GHCR)")
		} else if dir, err := compose.ResolveDir(cfg.AuthCompose); err != nil {
			check("auth_compose", err)
		} else {
			fmt.Printf("✓ auth_compose: %s\n", dir)
		}
	} else {
		fmt.Println("· auth: not enabled (optional)")
	}

	if cfg.ObservabilityEnabled || cfg.ObservabilityCompose != "" {
		if cfg.ObservabilityCompose == "" {
			fmt.Println("✓ observability: embedded stack")
		} else if dir, err := compose.ResolveDir(cfg.ObservabilityCompose); err != nil {
			check("observability_compose", err)
		} else {
			fmt.Printf("✓ observability_compose: %s\n", dir)
		}
		if cfg.OtelEndpoint != "" {
			fmt.Printf("· otel.endpoint: %s\n", cfg.OtelEndpoint)
		}
	} else {
		fmt.Println("· observability: not enabled (optional)")
	}

	for _, p := range []struct {
		name string
		port int
		pin  bool
	}{
		{"gateway port", cfg.Ports.Gateway, cfg.PortsExplicit.Gateway},
		{"registry port", cfg.Ports.Registry, cfg.PortsExplicit.Registry},
		{"auth port", cfg.Ports.Auth, cfg.PortsExplicit.Auth},
		{"grafana port", cfg.Ports.Grafana, cfg.PortsExplicit.Grafana},
		{"otlp_http port", cfg.Ports.OTLPHTTP, cfg.PortsExplicit.OTLPHTTP},
		{"alloy port", cfg.Ports.Alloy, cfg.PortsExplicit.Alloy},
	} {
		if !ports.Free(p.port) {
			if p.pin {
				fmt.Printf("· %s %d: in use (pinned — start will fail if still busy)\n", p.name, p.port)
			} else {
				fmt.Printf("· %s %d: in use (start may auto-allocate)\n", p.name, p.port)
			}
		} else {
			fmt.Printf("✓ %s %d: free\n", p.name, p.port)
		}
	}

	reg := registry.New(cfg.RegistryURL, cfg.AdminToken)
	if reg.Ready() {
		fmt.Printf("✓ route registry reachable at %s\n", cfg.RegistryURL)
	} else {
		fmt.Printf("· route registry not reachable at %s (start with plat5 start)\n", cfg.RegistryURL)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Get(cfg.GatewayURL); err == nil {
		resp.Body.Close()
		fmt.Printf("✓ gateway reachable at %s\n", cfg.GatewayURL)
	} else {
		fmt.Printf("· gateway not reachable at %s\n", cfg.GatewayURL)
	}

	return doctorExit(failed)
}

func doctorExit(failed bool) error {
	if failed {
		return fmt.Errorf("doctor found problems")
	}
	return nil
}
