package cmd

import (
	"fmt"
	"os"
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
	startVerbose bool
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Run Plat5, its modules, and your app in Docker",
	Long: `Run the project's local stack in Docker, then print its links, logins, and keys.

Plat5 always runs. Beside it, the modules plat5.dev.yml turns on (all by default):
  auth           Plat5 Auth, the customer IdP, in dev mode
  observability  Grafana with logs, traces, and metrics (grafana/otel-lgtm + Alloy)
Then your app, from app.compose in plat5.dev.yml.

start applies plat5.yml as plat5 apply does, without a prompt; a blocked plan
is printed and refused. Then it seeds the dev person, a node labelled Dev of
each root kind, and a personal key. Running it again updates the stack in place.

A service file's url is what the gateway reaches, as in any deployment. When
its host is a service in your app's compose file (http://api:3000), start puts
that service on the Plat5 network, so the gateway reaches it by name and its
port on your machine can be bound to 127.0.0.1. With -x app there is no
container: start maps the name to your machine instead, so the app must listen
there on the url's port (beyond loopback on Linux).

  plat5 start -x observability    skip a module for this run
  plat5 start -x app              run your app yourself`,
	Args: cobra.NoArgs,
	RunE: runStart,
}

func init() {
	startCmd.Flags().StringSliceVarP(&startExclude, "exclude", "x", nil, "Skip modules for this run: auth, observability, app")
	startCmd.Flags().DurationVar(&startWait, "wait", 10*time.Minute, "How long to wait for the stack to be healthy")
	startCmd.Flags().BoolVarP(&startVerbose, "verbose", "v", false, "Show docker compose's pull, build, and container output (otherwise it goes to a log file)")
}

func runStart(cmd *cobra.Command, args []string) error {
	if err := compose.DockerAvailable(); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	// Offline first: a config that can't apply stops start before Docker does anything.
	conf, err := loadValid(cfg.ConfigPath)
	if err != nil {
		return err
	}
	hosts := urlHosts(conf.Flat)

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
	var joined []string
	if app != nil {
		if err := config.CheckFile("app.compose", cfg.AppCompose); err != nil {
			return err
		}
		if skipApp {
			if _, err := stk.AppOnHost(app, hosts); err != nil {
				return err
			}
			app = nil
		} else if joined, err = stk.JoinApp(app, hosts); err != nil {
			return err
		}
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
		return fmt.Errorf("%w%s", err, portHolders(stk.Ports(), cfg.ComposeProject))
	}
	if err := stk.Prepare(); err != nil {
		return err
	}

	what := "Plat5"
	if len(stk.Modules) > 0 {
		what += " + " + strings.Join(stk.Modules, ", ")
	}
	out, err := newStartLog(stk.Dir, startVerbose)
	if err != nil {
		return err
	}
	defer out.Close()

	fmt.Printf("Starting %s…\n", what)
	runner := stk.Runner()
	runner.Output = out.w
	if err := pullMissing(runner, out); err != nil {
		return err
	}
	if stk.Builds() {
		fmt.Println("  Building from source: checkouts (the first build takes a while)…")
	}
	// Identity and the gateway are ready only once they have a config, so the
	// stack starts, the config is applied, and then start waits for health.
	if err := runner.Up(compose.UpOptions{Build: stk.Builds(), RemoveOrphans: true}); err != nil {
		return out.failed(err, "plat5 logs")
	}
	cp := controlplane.New(cfg.ControlPlaneURL, cfg.AdminToken)
	if err := cp.WaitReady(time.Minute); err != nil {
		return err
	}
	fmt.Printf("Applying %s…\n", relPath(cfg.Dir, cfg.ConfigPath))
	for _, svc := range joined {
		fmt.Printf("  %s: your app's container, on the Plat5 network\n", svc)
	}
	for _, svc := range stk.OnHost {
		fmt.Printf("  %s: your machine (-x app)\n", svc)
	}
	applied, err := applyForStart(cp, cfg.ConfigPath, os.Stdout)
	if err != nil {
		return err
	}
	warnUngrantedLabels(applied.Flat, relPath(cfg.Dir, cfg.ConfigPath))

	fmt.Println("  Waiting for every container to be healthy…")
	if err := runner.Up(compose.UpOptions{Wait: startWait}); err != nil {
		return out.failed(err, "plat5 logs")
	}

	st := state.State{ProjectID: cfg.ProjectID, Path: cfg.Path, Modules: stk.Modules, StartedAt: time.Now().UTC()}

	if stk.Has(config.ModuleAuth) {
		dev, err := devseed.Ensure(devseed.Client{
			GatewayURL: cfg.GatewayURL(),
			AuthURL:    cfg.URL(ports.Auth),
			ClientID:   config.DefaultAuthClient,
			Email:      config.DevEmail,
			Name:       config.DevName,
			NodeLabel:  config.DevNodeLabel,
			Roots:      rootKinds(applied.Flat),
		}, prev.Dev)
		if err != nil {
			hint := ""
			if devseed.IsNotFound(err) {
				hint = "\n  (A route the dev seed uses isn't in the config: plat5 routes list shows the routes it has.)"
			}
			fmt.Printf("warning: could not seed dev data: %v%s\n", err, hint)
		}
		st.Dev = dev
	}

	if app != nil {
		fmt.Printf("Starting your app (%s)…\n", relPath(cfg.Dir, cfg.AppCompose))
		app.Output = out.w
		if err := pullMissing(*app, out); err != nil {
			return err
		}
		if err := app.Up(compose.UpOptions{Build: true, Wait: startWait}); err != nil {
			_ = state.Save(st)
			return out.failed(fmt.Errorf("your app: %w", err), "plat5 logs --app")
		}
		st.AppCompose = cfg.AppCompose
		st.AppJoined = joined
	}

	if err := state.Save(st); err != nil {
		return err
	}
	fmt.Println()
	printSummary(stk, st)
	printWhatYouGot(stk, st)
	return nil
}
