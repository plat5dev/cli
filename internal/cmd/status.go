package cmd

import (
	"fmt"
	"sort"
	"time"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/output"
	"github.com/plat5dev/cli/internal/registry"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show local Plat5 URLs and health",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, st, err := loadConfigWithState()
	if err != nil {
		return err
	}
	return printStatus(cfg, st)
}

func printStatus(cfg config.Resolved, st state.State) error {
	stateDir, err := state.Dir(cfg.ProjectID)
	if err != nil {
		return err
	}

	plat5Dir, _ := resolvePlat5StackForOps(cfg, st, stateDir)
	authDir, _ := resolveAuthStackForOps(cfg, st, stateDir)
	obsDir, _ := resolveObservabilityStackForOps(cfg, st, stateDir)

	edgeRunner := composeRunner(plat5Dir, st.ComposeProject, cfg.ComposeProject, st.Plat5Override)
	authRunner := composeRunner(authDir, st.AuthComposeName, cfg.AuthComposeName, st.AuthOverride)
	obsRunner := composeRunner(obsDir, st.ObservabilityComposeName, cfg.ObservabilityComposeName, st.ObservabilityOverride)

	edgeRunning := false
	if edgeRunner != nil {
		edgeRunning, _ = edgeRunner.Running()
	}
	authRunning := false
	if authRunner != nil {
		authRunning, _ = authRunner.Running()
	}
	obsRunning := false
	if obsRunner != nil {
		obsRunning, _ = obsRunner.Running()
	}

	gw := output.ProbeHTTP(cfg.GatewayURL, 2*time.Second)
	regClient := registry.New(cfg.RegistryURL, cfg.AdminToken)
	regStatus := "down"
	var routeNames []string
	if services, err := regClient.List(); err == nil {
		regStatus = "up"
		for name := range services {
			routeNames = append(routeNames, name)
		}
		sort.Strings(routeNames)
	}

	authProbe := "skipped"
	if st.StartedAuth || authRunning || cfg.AuthEnabled {
		authProbe = output.ProbeHTTP(cfg.AuthURL, 2*time.Second)
	}

	obsProbe := "skipped"
	if st.StartedObservability || obsRunning || cfg.ObservabilityEnabled {
		if cfg.GrafanaURL != "" {
			obsProbe = output.ProbeHTTP(cfg.GrafanaURL, 2*time.Second)
		} else {
			obsProbe = "n/a"
		}
	}

	lines := []string{
		fmt.Sprintf("Project:         %s", cfg.ProjectID),
		fmt.Sprintf("Plat5 version:   %s", cfg.Plat5Version),
	}
	if cfg.AuthEnabled || cfg.AuthCompose != "" {
		lines = append(lines, fmt.Sprintf("Auth version:    %s", cfg.AuthVersion))
	}
	if plat5Dir != "" {
		runLabel := "stopped"
		if edgeRunning {
			runLabel = "running"
		}
		if cfg.Plat5Compose != "" {
			lines = append(lines, fmt.Sprintf("Plat5:           %s (%s)", plat5Dir, runLabel))
		} else {
			lines = append(lines, fmt.Sprintf("Plat5:           %s", runLabel))
		}
	}
	if authDir != "" {
		runLabel := "stopped"
		if authRunning {
			runLabel = "running"
		}
		if cfg.AuthCompose != "" {
			lines = append(lines, fmt.Sprintf("Auth:            %s (%s)", authDir, runLabel))
		} else {
			lines = append(lines, fmt.Sprintf("Auth:            %s", runLabel))
		}
	}
	if obsDir != "" {
		runLabel := "stopped"
		if obsRunning {
			runLabel = "running"
		}
		lines = append(lines, fmt.Sprintf("Obs compose:     %s (%s)", obsDir, runLabel))
	} else {
		lines = append(lines, "Obs compose:     (not set)")
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Gateway:         %s  (%s)", cfg.GatewayURL, gw))
	lines = append(lines, fmt.Sprintf("Route registry:  %s  (%s)", cfg.RegistryURL, regStatus))
	lines = append(lines, fmt.Sprintf("Auth (IdP):      %s  (%s)", cfg.AuthURL, authProbe))
	if cfg.GrafanaURL != "" {
		lines = append(lines, fmt.Sprintf("Grafana:         %s  (%s)", cfg.GrafanaURL, obsProbe))
	}
	if cfg.OtelEndpoint != "" {
		lines = append(lines, fmt.Sprintf("OTLP endpoint:   %s", cfg.OtelEndpoint))
	}
	if cfg.AlloyURL != "" && (st.StartedObservability || obsRunning || cfg.ObservabilityEnabled) {
		lines = append(lines, fmt.Sprintf("Alloy UI:        %s", cfg.AlloyURL))
	}
	lines = append(lines, "")
	lines = append(lines, fmt.Sprintf("Admin token:     %s (from %s)", cfg.AdminToken, output.TokenSource(cfg.AdminToken, config.DefaultAdminToken)))
	if regStatus == "up" {
		lines = append(lines, fmt.Sprintf("Routes:          %s", output.JoinNames(routeNames)))
	} else {
		lines = append(lines, "Routes:          (registry unavailable)")
	}
	lines = append(lines, fmt.Sprintf("Config:          %s", cfg.ConfigPath))

	output.PrintStatus(nil, lines)
	return nil
}

func composeRunner(dir, stProject, cfgProject, override string) *compose.Runner {
	if dir == "" || compose.ValidateComposeDir(dir) != nil {
		return nil
	}
	project := stProject
	if project == "" {
		project = cfgProject
	}
	var overrides []string
	if override != "" {
		overrides = []string{override}
	}
	return &compose.Runner{Dir: dir, ProjectName: project, OverrideFiles: overrides}
}
