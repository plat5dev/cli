package cmd

import (
	"fmt"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

var (
	stopAuth          bool
	stopObservability bool
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop Plat5 (and Auth / Observability if this CLI started them)",
	RunE:  runStop,
}

func init() {
	stopCmd.Flags().BoolVar(&stopAuth, "auth", false, "Also stop Plat5 Auth even if not recorded in state")
	stopCmd.Flags().BoolVar(&stopObservability, "observability", false, "Also stop observability even if not recorded in state")
}

func runStop(cmd *cobra.Command, args []string) error {
	if err := compose.DockerAvailable(); err != nil {
		return err
	}

	cfg, st, err := loadConfigWithState()
	if err != nil {
		return err
	}

	stateDir, err := state.Dir(cfg.ProjectID)
	if err != nil {
		return err
	}

	plat5Dir, err := resolvePlat5StackForOps(cfg, st, stateDir)
	if err != nil {
		return fmt.Errorf("plat5: %w", err)
	}

	project := st.ComposeProject
	if project == "" {
		project = cfg.ComposeProject
	}
	var plat5Overrides []string
	if st.Plat5Override != "" {
		plat5Overrides = []string{st.Plat5Override}
	}

	fmt.Println("Stopping Plat5…")
	if err := (compose.Runner{
		Dir:           plat5Dir,
		ProjectName:   project,
		OverrideFiles: plat5Overrides,
	}).Down(); err != nil {
		return err
	}

	shouldStopAuth := stopAuth || st.StartedAuth || cfg.AuthEnabled
	if shouldStopAuth {
		authDir, err := resolveAuthStackForOps(cfg, st, stateDir)
		if err != nil {
			fmt.Println("Warning: could not resolve auth compose:", err)
		} else if authDir != "" {
			authProject := st.AuthComposeName
			if authProject == "" {
				authProject = cfg.AuthComposeName
			}
			var authOverrides []string
			if st.AuthOverride != "" {
				authOverrides = []string{st.AuthOverride}
			}
			fmt.Println("Stopping Plat5 Auth…")
			if err := (compose.Runner{
				Dir:           authDir,
				ProjectName:   authProject,
				OverrideFiles: authOverrides,
			}).Down(); err != nil {
				return err
			}
		}
	}

	shouldStopObs := stopObservability || st.StartedObservability || cfg.ObservabilityEnabled
	if shouldStopObs {
		obsDir, err := resolveObservabilityStackForOps(cfg, st, stateDir)
		if err != nil {
			fmt.Println("Warning: could not resolve observability compose:", err)
		} else if obsDir != "" {
			obsProject := st.ObservabilityComposeName
			if obsProject == "" {
				obsProject = cfg.ObservabilityComposeName
			}
			var obsOverrides []string
			if st.ObservabilityOverride != "" {
				obsOverrides = []string{st.ObservabilityOverride}
			}
			fmt.Println("Stopping observability…")
			if err := (compose.Runner{
				Dir:           obsDir,
				ProjectName:   obsProject,
				OverrideFiles: obsOverrides,
			}).Down(); err != nil {
				return err
			}
		}
	}

	if err := state.Clear(cfg.ProjectID); err != nil {
		return err
	}
	fmt.Println("Stopped.")
	return nil
}
