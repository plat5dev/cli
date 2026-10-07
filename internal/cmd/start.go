package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/registry"
	"github.com/plat5dev/cli/internal/state"
	"github.com/plat5dev/cli/internal/upstreams"
	"github.com/spf13/cobra"
)

var (
	startDetach        bool
	startBuild         bool
	startBuildSet      bool
	startAuth          bool
	startObservability bool
	startOperator      bool
	startWait          time.Duration
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start Plat5 (and Auth / Observability / Operator if enabled)",
	Long: `Start local Plat5 stacks with Docker Compose.

Pulls runtime images via plat5_version / PLAT5_VERSION, Auth via
auth.version / AUTH_VERSION, and Operator via operator.version /
OPERATOR_VERSION (independent pins; defaults v0.4.0 / v0.1.11 / v0.4.0) using
compose files embedded in the CLI.

Advanced: set plat5_compose / auth_compose / observability_compose /
operator_compose to local compose trees; --build rebuilds from those trees.

Operator starts after Plat5 with a local staff IdP (Dex) and joins the Plat5
network so http://identity:3000 resolves. Identity is not published. Routes are not
rewritten. Staff sign in at the IdP; consoles in operator.allowed_origins can
use its public operator-console client.

Applies routes listed in plat5.yml after the registry is ready.`,
	RunE: runStart,
}

func init() {
	startCmd.Flags().BoolVarP(&startDetach, "detach", "d", true, "Run containers in the background")
	startCmd.Flags().BoolVar(&startBuild, "build", false, "Build images before starting (local compose trees only)")
	startCmd.Flags().BoolVar(&startAuth, "auth", false, "Also start Plat5 Auth (or set auth.enabled in plat5.yml)")
	startCmd.Flags().BoolVar(&startObservability, "observability", false, "Also start observability stack (or set observability.enabled)")
	startCmd.Flags().BoolVar(&startOperator, "operator", false, "Also start Operator (or set operator.enabled)")
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
	wantOperator := startOperator || cfg.OperatorEnabled
	if wantObs {
		cfg.ObservabilityEnabled = true
	}
	if wantOperator && !startDetach {
		return fmt.Errorf("operator requires detached start (it joins the Plat5 network after Plat5 is up)")
	}

	prev, err := state.Load(cfg.ProjectID)
	if err != nil {
		return err
	}
	owned := runningPorts(cfg, prev)
	if owned != (ports.Set{}) {
		fmt.Println("Plat5 is already running for this project; keeping its ports.")
	}
	if err := config.ResolvePorts(&cfg, owned); err != nil {
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

	var opDir string
	var opImage bool
	if wantOperator {
		opDir, opImage, err = resolveOperatorStack(cfg, stateDir)
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
	buildOperator := startBuild
	if !startBuildSet {
		buildOperator = !opImage
	}

	overrideOpts := compose.OverrideOpts{
		HostGateway: config.NeedsHostGateway(cfg.OtelEndpoint),
	}
	if err := config.CheckRolesFile(cfg.RolesFile); err != nil {
		return err
	}
	rolesHash, err := fileHash(cfg.RolesFile)
	if err != nil {
		return err
	}
	plat5Opts := overrideOpts
	plat5Opts.RolesFile = cfg.RolesFile
	plat5Override := filepath.Join(stateDir, "compose.override.yml")
	if err := compose.WritePlat5Override(plat5Override, cfg.Ports.Gateway, cfg.Ports.Registry, plat5Opts); err != nil {
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
		OperatorPort:             cfg.Ports.Operator,
		OperatorIdPPort:          cfg.Ports.OperatorIdP,
		GrafanaPort:              cfg.Ports.Grafana,
		OTLPGRPCPort:             cfg.Ports.OTLPGRPC,
		OTLPHTTPPort:             cfg.Ports.OTLPHTTP,
		AlloyPort:                cfg.Ports.Alloy,
		RolesHash:                rolesHash,
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
	// Identity reads roles at boot. Up leaves a running container alone when only
	// the mounted file changed, so restart it.
	if startDetach && owned != (ports.Set{}) && prev.RolesHash != rolesHash {
		fmt.Println("Roles changed; restarting identity…")
		if err := edge.Restart("identity"); err != nil {
			return err
		}
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

	if wantOperator {
		dexConfig := filepath.Join(stateDir, "operator-dex.yml")
		if err := compose.WriteOperatorDexConfig(dexConfig, cfg.OperatorIssuerURL, config.OperatorDevEmail, cfg.OperatorAllowedOrigins); err != nil {
			return err
		}
		opOverride := filepath.Join(stateDir, "compose.operator.override.yml")
		if err := compose.WriteOperatorOverride(opOverride, compose.OperatorOverride{
			Port:           cfg.Ports.Operator,
			IdPPort:        cfg.Ports.OperatorIdP,
			IssuerURL:      cfg.OperatorIssuerURL,
			AllowedOrigins: cfg.OperatorAllowedOrigins,
			Plat5Network:   compose.Plat5NetworkName(cfg.ComposeProject),
			DexConfig:      dexConfig,
		}); err != nil {
			return err
		}
		st.OperatorOverride = opOverride
		st.OperatorCompose = opDir
		st.OperatorComposeName = cfg.OperatorComposeName
		st.StartedOperator = true

		fmt.Println("Starting Operator…")
		// Readiness is the operator healthcheck (internal port): it passes once IdP keys are
		// fetched. The API port answers 401 to anything unauthenticated, so it says nothing.
		op := compose.Runner{
			Dir:           opDir,
			ProjectName:   cfg.OperatorComposeName,
			OverrideFiles: []string{opOverride},
			Wait:          true,
		}
		if err := op.Up(true, buildOperator, operatorStackEnv(cfg)); err != nil {
			return fmt.Errorf("operator not ready: %w", err)
		}
		fmt.Println("Operator is up:", cfg.OperatorURL)
		fmt.Println("  staff IdP:", cfg.OperatorIssuerURL)
		fmt.Printf("  login:     %s / %s\n", config.OperatorDevEmail, config.OperatorDevPassword)
	}

	if err := state.Save(st); err != nil {
		return err
	}
	fmt.Println()
	return printStatus(cfg, st)
}

func applyRouteFiles(cfg config.Resolved, client *registry.Client) error {
	return applyFiles(client, cfg.RouteFiles, cfg.Upstreams, true)
}

// applyFiles applies routes files in order, binding plat5.yml upstreams.
// After all files, it warns (non-fatal) about upstreams keys that matched no service.
func applyFiles(client *registry.Client, files []string, ups map[string]string, skipMissing bool) error {
	used := map[string]bool{}
	for _, f := range files {
		if skipMissing {
			if _, err := os.Stat(f); err != nil {
				fmt.Printf("Skipping routes file %s (%v)\n", f, err)
				continue
			}
		}
		fmt.Printf("Applying %s…\n", f)
		results, bound, err := client.Apply(f, ups)
		for _, r := range results {
			printApplyResult(r)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		for _, name := range bound {
			used[name] = true
		}
	}
	for _, name := range upstreams.Unused(ups, used) {
		fmt.Printf("warning: upstreams.%s matches no service in the applied routes files (not applied)\n", name)
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

// runningPorts is the host ports from the last start for each stack of this
// project that still has a running container. Those ports belong to us, so
// a second start keeps them and compose up leaves the containers alone.
func runningPorts(cfg config.Resolved, prev state.State) ports.Set {
	var owned ports.Set
	if compose.ProjectRunning(cfg.ComposeProject) {
		owned.Gateway = prev.GatewayPort
		owned.Registry = prev.RegistryPort
	}
	if compose.ProjectRunning(cfg.AuthComposeName) {
		owned.Auth = prev.AuthPort
	}
	if compose.ProjectRunning(cfg.ObservabilityComposeName) {
		owned.Grafana = prev.GrafanaPort
		owned.OTLPGRPC = prev.OTLPGRPCPort
		owned.OTLPHTTP = prev.OTLPHTTPPort
		owned.Alloy = prev.AlloyPort
	}
	if compose.ProjectRunning(cfg.OperatorComposeName) {
		owned.Operator = prev.OperatorPort
		owned.OperatorIdP = prev.OperatorIdPPort
	}
	return owned
}

// fileHash is the sha256 of path's contents, or "" when path is empty.
func fileHash(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
