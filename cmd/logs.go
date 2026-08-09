package cmd

import (
	"fmt"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

var (
	logsAuth          bool
	logsObservability bool
	logsFollow        bool
)

var logsCmd = &cobra.Command{
	Use:   "logs [service...]",
	Short: "Tail Docker Compose logs (Plat5 by default)",
	RunE:  runLogs,
}

func init() {
	logsCmd.Flags().BoolVar(&logsAuth, "auth", false, "Show Plat5 Auth compose logs")
	logsCmd.Flags().BoolVar(&logsObservability, "observability", false, "Show observability compose logs")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", true, "Follow log output")
}

func runLogs(cmd *cobra.Command, args []string) error {
	if err := compose.DockerAvailable(); err != nil {
		return err
	}
	cfg, st, err := loadConfigWithState()
	if err != nil {
		return err
	}

	if logsAuth && logsObservability {
		return fmt.Errorf("use only one of --auth or --observability")
	}

	stateDir, err := state.Dir(cfg.ProjectID)
	if err != nil {
		return err
	}

	if logsAuth {
		dir, err := resolveAuthStackForOps(cfg, st, stateDir)
		if err != nil {
			return err
		}
		if dir == "" {
			return fmt.Errorf("auth compose not configured")
		}
		r := composeRunner(dir, st.AuthComposeName, cfg.AuthComposeName, st.AuthOverride)
		if r == nil {
			return fmt.Errorf("auth compose not configured")
		}
		return r.Logs(logsFollow, args...)
	}

	if logsObservability {
		dir, err := resolveObservabilityStackForOps(cfg, st, stateDir)
		if err != nil {
			return err
		}
		if dir == "" {
			return fmt.Errorf("observability compose not configured")
		}
		r := composeRunner(dir, st.ObservabilityComposeName, cfg.ObservabilityComposeName, st.ObservabilityOverride)
		if r == nil {
			return fmt.Errorf("observability compose not configured")
		}
		return r.Logs(logsFollow, args...)
	}

	dir, err := resolvePlat5StackForOps(cfg, st, stateDir)
	if err != nil {
		return err
	}
	r := composeRunner(dir, st.ComposeProject, cfg.ComposeProject, st.Plat5Override)
	if r == nil {
		return fmt.Errorf("plat5 compose not configured")
	}
	return r.Logs(logsFollow, args...)
}
