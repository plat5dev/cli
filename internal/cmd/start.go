package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/devseed"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/stack"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

// appModule is -x app: skip the app's compose file for this run.
const appModule = "app"

var (
	startExclude []string
	startWait    time.Duration
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Run Plat5, its modules, and your app in Docker",
	Long: `Run the project's local stack in Docker, then print its links, logins, and keys.

Plat5 always runs. Beside it, the modules plat5.yml turns on (all by default):
  auth           Plat5 Auth, the customer IdP, in dev mode
  operator       the staff front door: operator gateway, console, staff sign-in
  observability  Grafana with logs, traces, and metrics (grafana/otel-lgtm + Alloy)
Then your app, from app.compose in plat5.yml.

start applies roles.yml and the routes files, and seeds a dev person, an org they
own, and a key for each. Running it again updates the stack in place.

A bare-port upstream (api: 3000) that your app's compose file publishes is
reached in Docker: start puts that container on the Plat5 network and binds the
upstream to it by service name (http://api:3000). The port on your machine is
then only for you, and can be bound to 127.0.0.1. With -x app there is no
container, so the upstream is a process on your machine (host.docker.internal).

  plat5 start -x operator         skip a module for this run
  plat5 start -x app              run your app yourself`,
	Args: cobra.NoArgs,
	RunE: runStart,
}

func init() {
	startCmd.Flags().StringSliceVarP(&startExclude, "exclude", "x", nil, "Skip modules for this run: auth, operator, observability, app")
	startCmd.Flags().DurationVar(&startWait, "wait", 10*time.Minute, "How long to wait for the stack to be healthy")
}

func runStart(cmd *cobra.Command, args []string) error {
	if err := compose.DockerAvailable(); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	skipApp := false
	var exclude []string
	for _, x := range startExclude {
		if x == appModule {
			skipApp = true
			continue
		}
		exclude = append(exclude, x)
	}
	stk, err := stack.New(cfg, exclude)
	if err != nil {
		return err
	}
	app := stk.AppRunner()
	if skipApp {
		app = nil
	}
	var joined map[string]state.AppUpstream
	if app != nil {
		if err := config.CheckFile("app.compose", cfg.AppCompose); err != nil {
			return err
		}
		if joined, err = stk.JoinApp(app); err != nil {
			return err
		}
		cfg = stk.Config
	}

	prev, err := state.Load(cfg.ProjectID)
	if err != nil {
		return err
	}
	// -x app: you run the app yourself, so the last start's app containers go,
	// and free its ports.
	if app == nil && prev.AppCompose != "" {
		fmt.Println("Stopping your app's containers (-x app)…")
		old := compose.Runner{Dir: dirOf(prev.AppCompose), Files: []string{prev.AppCompose}, Env: stk.AppEnv()}
		if err := old.Down(); err != nil {
			fmt.Println("warning:", err)
		}
	}

	owned := compose.PublishedPorts(cfg.ComposeProject)
	if err := ports.Check(stk.Ports(), func(p int) bool { return owned[p] }); err != nil {
		return err
	}
	if err := stk.Prepare(); err != nil {
		return err
	}

	what := "Plat5"
	if len(stk.Modules) > 0 {
		what += " + " + strings.Join(stk.Modules, ", ")
	}
	fmt.Printf("Starting %s…\n", what)
	if stk.Builds() {
		fmt.Println("Building from source: checkouts (the first build takes a while)…")
	}
	runner := stk.Runner()
	if err := runner.Up(compose.UpOptions{Build: stk.Builds(), Wait: startWait, RemoveOrphans: true}); err != nil {
		return fmt.Errorf("%w\n  See what failed: plat5 logs", err)
	}

	cp := controlplane.New(cfg.ControlPlaneURL, cfg.AdminToken)
	if err := cp.WaitReady(time.Minute); err != nil {
		return err
	}
	fmt.Println("Applying roles and routes…")
	printAppUpstreams(joined, "")
	if err := applyRolesFile(cp, cfg.RolesFile); err != nil {
		return err
	}
	if err := applyFiles(cp, cfg, cfg.RouteFiles, true); err != nil {
		return err
	}

	st := state.State{ProjectID: cfg.ProjectID, ConfigPath: cfg.ConfigPath, Modules: stk.Modules, StartedAt: time.Now().UTC()}

	if stk.Has(config.ModuleAuth) {
		dev, err := devseed.Ensure(devseed.Client{
			GatewayURL: cfg.GatewayURL(),
			AuthURL:    cfg.URL(ports.Auth),
			ClientID:   config.DefaultAuthClient,
			Email:      config.DevEmail,
			OrgName:    config.DevOrgName,
		}, prev.Dev)
		if err != nil {
			fmt.Printf("warning: could not seed dev keys: %v\n  (The identity routes may not be applied: check routes: in plat5.yml.)\n", err)
		}
		st.Dev = dev
	}

	if app != nil {
		fmt.Printf("Starting your app (%s)…\n", relPath(cfg.ConfigDir, cfg.AppCompose))
		if err := app.Up(compose.UpOptions{Build: true, Wait: startWait}); err != nil {
			_ = state.Save(st)
			return fmt.Errorf("your app: %w\n  See what failed: plat5 logs --app", err)
		}
		st.AppCompose = cfg.AppCompose
		st.AppUpstreams = joined
	}

	if err := state.Save(st); err != nil {
		return err
	}
	fmt.Println()
	printSummary(stk, st)
	return nil
}
