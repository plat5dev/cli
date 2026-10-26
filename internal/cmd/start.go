package cmd

import (
	"fmt"
	"os"
	"sort"
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
  operator       the operator gateway on operator.routes.yml, operator-audit, and
                 the operator IdP (a second Plat5 Auth, in dev mode)
Then your app, from app.compose in plat5.dev.yml. The operator console, which
app.compose includes, runs only with the operator module.

start applies plat5.yml as plat5 apply does, without a prompt; a blocked plan
is printed and refused. Then, through identity's user routes like any client,
it seeds the dev person, a node named Dev of each root kind (found through
/user/nodes, made at its collection, such as POST /orgs), and a personal key
(POST /user/keys), which callers send as Authorization: Bearer. Running it
again updates the stack in place.

A service file's url is what the gateway reaches, as in any deployment. When
its host is a service in your app's compose file (http://api:3000), start puts
that service on the Plat5 network, so the gateway reaches it by name and its
port on your machine can be bound to 127.0.0.1. With -x app there is no
container: start maps the name to your machine instead, so the app must listen
there on the url's port (beyond loopback on Linux).

  plat5 start -x observability    skip a module for this run
  plat5 start -x operator         no operator gateway, IdP, audit log, or console
  plat5 start -x app              run your app yourself`,
	Args: cobra.NoArgs,
	RunE: runStart,
}

func init() {
	startCmd.Flags().StringSliceVarP(&startExclude, "exclude", "x", nil, "Skip modules for this run: auth, observability, operator, app")
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
	if err := stk.CheckOperator(); err != nil {
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
			NodeName:   config.DevNodeName,
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
		run, err := appServices(stk, *app)
		if err != nil {
			return err
		}
		if run == nil || len(run) > 0 {
			if err := pullMissing(*app, out); err != nil {
				return err
			}
			if err := app.Up(compose.UpOptions{Build: true, Wait: startWait, Services: run}); err != nil {
				_ = state.Save(st)
				return out.failed(fmt.Errorf("your app: %w", err), "plat5 logs --app")
			}
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

// appServices is the app's services to run: nil for all of them, or, without
// the operator module, every one but the operator console's (empty when that is
// all there is), whose last containers it removes.
func appServices(stk *stack.Stack, app compose.Runner) ([]string, error) {
	skip, err := stk.SkippedAppServices()
	if err != nil || len(skip) == 0 {
		return nil, err
	}
	networks, err := app.ServiceNetworks()
	if err != nil {
		return nil, fmt.Errorf("your app: %w", err)
	}
	run := []string{}
	for name := range networks {
		if !contains(skip, name) {
			run = append(run, name)
		}
	}
	sort.Strings(run)
	fmt.Printf("  %s: the operator console, which runs with the operator module: skipped\n", strings.Join(skip, ", "))
	if err := app.Remove(skip...); err != nil {
		fmt.Println("warning:", err)
	}
	return run, nil
}
