package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/plat5yml"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/stack"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check Docker, plat5.dev.yml, plat5.yml, and ports",
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
		check(config.FileName, err)
		return doctorExit(failed)
	}
	check(config.FileName+" "+cfg.Path, nil)

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
	check("app.compose", config.CheckFile("app.compose", cfg.AppCompose))
	if stk.Has(config.ModuleOperator) {
		check("operator.routes", stk.CheckOperator())
	}
	if conf, err := plat5yml.Load(cfg.ConfigPath); err != nil {
		check(config.ConfigFileName, err)
	} else if problems, err := conf.Validate(); err != nil || len(problems) > 0 {
		if err == nil {
			err = errors.New(strings.Join(problems, "; "))
		}
		check(config.ConfigFileName, err)
	} else {
		check(fmt.Sprintf("%s: %d services", config.ConfigFileName, len(conf.Files)), nil)
	}

	running := compose.Running(cfg.ComposeProject)
	owned := compose.PublishedPorts(cfg.ComposeProject)
	if busy := ports.Busy(stk.Ports(), func(p int) bool { return owned[p] }); len(busy) > 0 {
		check("ports", fmt.Errorf("in use: %s%s", strings.Join(busy, ", "), portHolders(stk.Ports(), cfg.ComposeProject)))
	} else {
		check("ports free", nil)
	}
	// The operator console's port is its compose file's: free before a start,
	// its own once running.
	if p := originPort(cfg.OperatorWeb); stk.RunsOperatorConsole() && p > 0 && !running {
		if ports.Free(p) {
			check(fmt.Sprintf("operator console port %d free", p), nil)
		} else {
			check("operator console port", fmt.Errorf("%d (%s) in use", p, cfg.OperatorWeb))
		}
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

// originPort is an origin's port, or 0.
func originPort(origin string) int {
	u, err := url.Parse(origin)
	if err != nil || u.Port() == "" {
		return 0
	}
	p, _ := strconv.Atoi(u.Port())
	return p
}
