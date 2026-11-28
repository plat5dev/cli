package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"os/exec"
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
	Short: "Check Docker, plat5.local.yml, plat5.yml, and ports",
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
	if _, err := loadOwned(cfg); err != nil {
		check("project_id "+cfg.ProjectID, err)
	} else {
		check("project_id "+cfg.ProjectID+": this folder's", nil)
	}

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
	if cfg.InfraCompose != "" {
		check("infra.compose", config.CheckFile("infra.compose", cfg.InfraCompose))
	}
	check("app.compose", config.CheckFile("app.compose", cfg.AppCompose))
	if cfg.EnvFile != "" {
		checkEnvFileGitIgnored(cfg.Dir, cfg.EnvFile)
	}
	if stk.Has(config.ModuleOperator) {
		check("operator.config", stk.CheckOperator())
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

// checkEnvFileGitIgnored warns when a local secrets file exists inside a git
// repo and is not covered by .gitignore.
func checkEnvFileGitIgnored(dir, envFile string) {
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	inRepo := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree")
	if err := inRepo.Run(); err != nil {
		return
	}
	if err := exec.Command("git", "-C", dir, "check-ignore", "-q", envFile).Run(); err != nil {
		fmt.Printf("! %s is not in .gitignore (local secrets should never be committed)\n", relPath(dir, envFile))
	} else {
		fmt.Printf("✓ %s ignored by git\n", relPath(dir, envFile))
	}
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
