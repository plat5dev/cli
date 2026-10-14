package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/stack"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check Docker, plat5.yml, and ports",
	Args:  cobra.NoArgs,
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

	check("docker", compose.DockerAvailable())

	cfg, err := loadConfig()
	if err != nil {
		check("plat5.yml", err)
		return doctorExit(failed)
	}
	check("plat5.yml "+cfg.ConfigPath, nil)

	stk, err := stack.New(cfg, nil)
	if err != nil {
		check("modules", err)
		return doctorExit(failed)
	}
	check("modules: core "+strings.Join(stk.Modules, " "), nil)
	for _, k := range cfg.NeededVersions() {
		if src := cfg.Source[k]; src != "" {
			fmt.Printf("· %s: built from %s\n", k, src)
			continue
		}
		fmt.Printf("· %s: %s\n", k, cfg.Versions[k])
	}
	check("roles", config.CheckFile("roles", cfg.RolesFile))
	check("app.compose", config.CheckFile("app.compose", cfg.AppCompose))
	for _, f := range cfg.RouteFiles {
		check("routes "+relPath(cfg.ConfigDir, f), config.CheckFile("routes", f))
	}

	running := compose.Running(cfg.ComposeProject)
	owned := compose.PublishedPorts(cfg.ComposeProject)
	if busy := ports.Busy(stk.Ports(), func(p int) bool { return owned[p] }); len(busy) > 0 {
		check("ports", fmt.Errorf("in use: %s", strings.Join(busy, ", ")))
	} else {
		check("ports free", nil)
	}

	if running {
		cp := controlplane.New(cfg.ControlPlaneURL, cfg.AdminToken)
		if cp.Ready() {
			check("control plane "+cfg.ControlPlaneURL, nil)
		} else {
			check("control plane "+cfg.ControlPlaneURL, errors.New("not ready (plat5 logs control-plane)"))
		}
	} else {
		fmt.Println("· not running (plat5 start)")
	}
	return doctorExit(failed)
}

func doctorExit(failed bool) error {
	if failed {
		return errors.New("doctor found problems")
	}
	return nil
}
