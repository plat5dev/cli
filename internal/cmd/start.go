package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/registry"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

var (
	startDetach        bool
	startBuild         bool
	startBuildSet      bool
	startAuth          bool
	startObservability bool
	startWait          time.Duration
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start Plat5 (and Auth / Observability if enabled)",
	Long: `Start local Plat5 stacks with Docker Compose.

Pulls runtime images via plat5_version / PLAT5_VERSION and Auth via
auth.version / AUTH_VERSION (independent pins; defaults v0.1.8 / v0.1.5) using
compose files embedded in the CLI.

Advanced: set plat5_compose / auth_compose / observability_compose to local
compose trees; --build rebuilds from those trees.

Applies routes listed in plat5.yml after the registry is ready.`,
	RunE: runStart,
}

func init() {
	startCmd.Flags().BoolVarP(&startDetach, "detach", "d", true, "Run containers in the background")
	startCmd.Flags().BoolVar(&startBuild, "build", false, "Build images before starting (local compose trees only)")
	startCmd.Flags().BoolVar(&startAuth, "auth", false, "Also start Plat5 Auth (or set auth.enabled in plat5.yml)")
	startCmd.Flags().BoolVar(&startObservability, "observability", false, "Also start observability stack (or set observability.enabled)")
	startCmd.Flags().DurationVar(&startWait, "wait", 5*time.Minute, "Max time to wait for readiness")
}

func runStart(cmd *cobra.Command, args []string) error {
	startBuildSet = cmd.Flags().Changed("build")

	if err := compose.DockerAvailable(); err != nil {
		return err
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	wantAuth := startAuth || cfg.AuthEnabled
	wantObs := startObservability || cfg.ObservabilityEnabled
	if wantObs {
		cfg.ObservabilityEnabled = true
	}

	if err := config.ResolvePorts(&cfg); err != nil {
		return err
	}

	stateDir, err := state.Dir(cfg.ProjectID)
	if err != nil {
		return err
	}

	plat5Dir, plat5Image, err := resolvePlat5Stack(cfg, stateDir)
	if err != nil {
		return err
	}

	var authDir string
	var authImage bool
	if wantAuth {
		authDir, authImage, err = resolveAuthStack(cfg, stateDir)
		if err != nil {
			return err
		}
	}

	var obsDir string
	var obsImage bool
	if wantObs {
		obsDir, obsImage, err = resolveObservabilityStack(cfg, stateDir)
		if err != nil {
			return err
		}
	}

	// --build defaults: on only for local compose trees when flag omitted.
	buildPlat5 := startBuild
	if !startBuildSet {
		buildPlat5 = !plat5Image
	}
	buildAuth := startBuild
	if !startBuildSet {
		buildAuth = !authImage
	}
	buildObs := startBuild
	if !startBuildSet {
		buildObs = !obsImage
	}

	overrideOpts := compose.OverrideOpts{
		HostGateway: config.NeedsHostGateway(cfg.OtelEndpoint),
	}
	plat5Override := filepath.Join(stateDir, "compose.override.yml")
	if err := compose.WritePlat5Override(plat5Override, cfg.Ports.Gateway, cfg.Ports.Registry, overrideOpts); err != nil {
		return err
	}

	st := state.State{
		ProjectID:                cfg.ProjectID,
		ConfigPath:               cfg.ConfigPath,
		Plat5Compose:             plat5Dir,
		ComposeProject:           cfg.ComposeProject,
		AuthComposeName:          cfg.AuthComposeName,
		ObservabilityComposeName: cfg.ObservabilityComposeName,
		Plat5Override:            plat5Override,
		GatewayPort:              cfg.Ports.Gateway,
		RegistryPort:             cfg.Ports.Registry,
		AuthPort:                 cfg.Ports.Auth,
		GrafanaPort:              cfg.Ports.Grafana,
		OTLPGRPCPort:             cfg.Ports.OTLPGRPC,
		OTLPHTTPPort:             cfg.Ports.OTLPHTTP,
		AlloyPort:                cfg.Ports.Alloy,
		StartedAt:                time.Now().UTC(),
	}

	edgeEnv := append([]string{}, plat5StackEnv(cfg)...)
	edgeEnv = append(edgeEnv, fmt.Sprintf("ADMIN_TOKEN=%s", cfg.AdminToken))
	if cfg.OtelEndpoint != "" {
		edgeEnv = append(edgeEnv, fmt.Sprintf("OTEL_EXPORTER_OTLP_ENDPOINT=%s", cfg.OtelEndpoint))
		fmt.Println("OTLP export:", cfg.OtelEndpoint)
	}

	authEnv := append([]string{}, authStackEnv(cfg)...)
	if cfg.OtelEndpoint != "" {
		authEnv = append(authEnv, fmt.Sprintf("OTEL_EXPORTER_OTLP_ENDPOINT=%s", cfg.OtelEndpoint))
	}

	if wantObs {
		obsOverride := filepath.Join(stateDir, "compose.observability.override.yml")
		if err := compose.WriteObservabilityOverride(obsOverride, compose.ObservabilityPorts{
			Grafana:  cfg.Ports.Grafana,
			OTLPGRPC: cfg.Ports.OTLPGRPC,
			OTLPHTTP: cfg.Ports.OTLPHTTP,
			Alloy:    cfg.Ports.Alloy,
		}); err != nil {
			return err
		}
		st.ObservabilityOverride = obsOverride
		st.ObservabilityCompose = obsDir
		st.StartedObservability = true

		fmt.Println("Starting observability…")
		obs := compose.Runner{
			Dir:           obsDir,
			ProjectName:   cfg.ObservabilityComposeName,
			OverrideFiles: []string{obsOverride},
		}
		if err := obs.Up(true, buildObs, nil); err != nil {
			return err
		}
		if err := waitHTTP(cfg.AlloyURL, startWait); err != nil {
			return fmt.Errorf("observability (alloy) not ready: %w", err)
		}
		fmt.Println("Observability is up:")
		fmt.Println("  Grafana:", cfg.GrafanaURL)
		fmt.Println("  OTLP HTTP: localhost:" + fmt.Sprint(cfg.Ports.OTLPHTTP))
		fmt.Println("  Alloy:", cfg.AlloyURL)
	}

	if wantAuth {
		if err := config.CheckAuthThemeFile(cfg.AuthThemeFile); err != nil {
			return err
		}
		authOverrideOpts := overrideOpts
		authOverrideOpts.AuthThemeFile = cfg.AuthThemeFile
		authOverride := filepath.Join(stateDir, "compose.auth.override.yml")
		if err := compose.WriteAuthOverride(authOverride, cfg.Ports.Auth, authOverrideOpts); err != nil {
			return err
		}
		st.AuthOverride = authOverride
		st.AuthCompose = authDir
		st.StartedAuth = true

		fmt.Println("Starting Plat5 Auth…")
		auth := compose.Runner{
			Dir:           authDir,
			ProjectName:   cfg.AuthComposeName,
			OverrideFiles: []string{authOverride},
		}
		if err := auth.Up(true, buildAuth, authEnv); err != nil {
			return err
		}
		if err := waitHTTP(cfg.AuthURL, startWait); err != nil {
			return fmt.Errorf("auth not ready: %w", err)
		}
		fmt.Println("Auth is up:", cfg.AuthURL)

		edgeEnv = append(edgeEnv,
			fmt.Sprintf("AUTH_ISSUER=%s", cfg.AuthURL),
			fmt.Sprintf("AUTH_JWKS_URI=http://host.docker.internal:%d/.well-known/jwks.json", cfg.Ports.Auth),
		)
	}

	fmt.Println("Starting Plat5…")
	edge := compose.Runner{
		Dir:           plat5Dir,
		ProjectName:   cfg.ComposeProject,
		OverrideFiles: []string{plat5Override},
	}
	if err := edge.Up(startDetach, buildPlat5, edgeEnv); err != nil {
		return err
	}

	if !startDetach {
		_ = state.Save(st)
		return nil
	}

	reg := registry.New(cfg.RegistryURL, cfg.AdminToken)
	fmt.Println("Waiting for route registry…")
	if err := reg.WaitReady(startWait); err != nil {
		return err
	}

	if err := applyRouteFiles(cfg, reg); err != nil {
		return err
	}

	fmt.Println("Waiting for gateway…")
	if err := waitHTTP(cfg.GatewayURL, startWait); err != nil {
		return fmt.Errorf("gateway not ready: %w", err)
	}

	if err := state.Save(st); err != nil {
		return err
	}
	fmt.Println()
	return printStatus(cfg, st)
}

func applyRouteFiles(cfg config.Resolved, client *registry.Client) error {
	for _, f := range cfg.RouteFiles {
		if _, err := os.Stat(f); err != nil {
			fmt.Printf("Skipping routes file %s (%v)\n", f, err)
			continue
		}
		fmt.Printf("Applying %s…\n", f)
		results, err := client.Apply(f, cfg.Upstreams)
		for _, r := range results {
			printApplyResult(r)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
	}
	return nil
}

func waitHTTP(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for %s", url)
}
