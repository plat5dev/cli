package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/stack"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Links, logins, keys, and container health",
	Args:  cobra.NoArgs,
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	st, err := state.Load(cfg.ProjectID)
	if err != nil {
		return err
	}
	if !compose.Running(cfg.ComposeProject) {
		fmt.Printf("%s is not running. Start it: plat5 start\n", cfg.ProjectID)
		return nil
	}
	// What the last start ran, not what plat5.dev.yml says now.
	var exclude []string
	for _, m := range config.Modules {
		if cfg.Modules[m] && !contains(st.Modules, m) {
			exclude = append(exclude, m)
		}
	}
	stk, err := stack.New(cfg, exclude)
	if err != nil {
		return err
	}
	printSummary(stk, st)

	r := stk.Runner()
	r.Quiet = true
	if out, err := r.Ps(); err == nil {
		fmt.Println("Containers")
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			fmt.Println("  " + line)
		}
	}
	return nil
}

// printSummary is what start ends with: every link, login, and key.
func printSummary(stk *stack.Stack, st state.State) {
	c := stk.Config
	w := &table{}

	fmt.Printf("%s is running.\n\n", c.ProjectID)
	if st.AppCompose != "" && c.AppWeb != "" {
		w.section("Your app")
		w.row("Web", c.AppWeb, loginHint(stk, config.DevEmail))
		for _, svc := range st.AppJoined {
			w.row("API ("+svc+")", "through the gateway", "its own port skips the gateway: no auth, this machine only")
		}
	}

	w.section("Plat5")
	w.row("Gateway", c.GatewayURL(), "")
	if stk.Has(config.ModuleAuth) {
		w.row("Auth", c.URL(ports.Auth), config.DevEmail+" / "+config.DevPassword)
		w.row("", "sign-up codes: plat5 logs auth | grep code", "")
	} else {
		w.row("IdP", c.IdP.Issuer, "")
	}
	w.row("Control plane", c.ControlPlaneURL, "admin token "+c.AdminToken)

	if stk.Has(config.ModuleOperator) {
		login := config.OperatorEmail + " / " + config.DevPassword
		w.section("Operator")
		w.row("Gateway", c.URL(ports.Operator), "routes: "+relPath(c.Dir, c.OperatorRoutes))
		w.row("IdP", c.URL(ports.OperatorAuth), login)
		if st.AppCompose != "" && c.OperatorWeb != "" {
			w.row("Console", c.OperatorWeb, login)
		}
	}

	if stk.Has(config.ModuleObservability) {
		w.section("Observability")
		w.row("Grafana", c.URL(ports.Grafana), "Plat5 folder: Service Health, Auth, Debug")
		w.row("OTLP", fmt.Sprintf("http://localhost:%d", c.Ports[ports.OTLPHTTP]), fmt.Sprintf("gRPC localhost:%d", c.Ports[ports.OTLPGRPC]))
	}

	if d := st.Dev; d != nil {
		w.section(fmt.Sprintf("Dev (%s)", d.Email))
		for _, n := range d.Nodes {
			w.row(n.Kind, n.Path, fmt.Sprintf("%q, which they own", config.DevNodeName))
		}
		w.row("Personal key", d.Key, "acts as them in every tree")
	}
	w.print()

	if d := st.Dev; d != nil {
		path := "/user"
		if len(d.Nodes) > 0 {
			path = d.Nodes[0].Path
		}
		fmt.Printf("\nTry it:\n  curl %s%s -H \"Authorization: Bearer %s\"\n", c.GatewayURL(), path, d.Key)
	}
	if stk.Has(config.ModuleOperator) {
		path := "/nodes"
		if d := st.Dev; d != nil && len(d.Nodes) > 0 {
			path = "/nodes/" + d.Nodes[0].ID
		}
		fmt.Printf("\nAs an operator, with a dev token from the operator IdP:\n")
		fmt.Printf("  TOKEN=$(curl -s %s/dev/token -H 'Content-Type: application/json' \\\n", c.URL(ports.OperatorAuth))
		fmt.Printf("    -d '{\"email\":\"%s\",\"client_id\":\"%s\"}' | jq -r .access_token)\n", config.OperatorEmail, config.OperatorClient)
		fmt.Printf("  curl %s%s -H \"Authorization: Bearer $TOKEN\"\n", c.URL(ports.Operator), path)
	}
	fmt.Println()
}

func loginHint(stk *stack.Stack, email string) string {
	if !stk.Has(config.ModuleAuth) {
		return ""
	}
	return email + " / " + config.DevPassword
}

// table prints aligned rows under section headings.
type table struct {
	lines [][3]string // heading has only [0]
}

func (t *table) section(name string) { t.lines = append(t.lines, [3]string{name}) }

func (t *table) row(label, value, note string) {
	t.lines = append(t.lines, [3]string{"  " + label, value, note})
}

func (t *table) print() {
	w0, w1 := 0, 0
	for _, l := range t.lines {
		if l[1] == "" && l[2] == "" && !strings.HasPrefix(l[0], "  ") {
			continue
		}
		w0 = max(w0, len(l[0]))
		// A key is too long to set the column: it overflows into its note instead.
		if len(l[1]) <= 40 {
			w1 = max(w1, len(l[1]))
		}
	}
	for i, l := range t.lines {
		if l[1] == "" && l[2] == "" && !strings.HasPrefix(l[0], "  ") {
			if i > 0 {
				fmt.Println()
			}
			fmt.Println(l[0])
			continue
		}
		line := fmt.Sprintf("%-*s  %-*s  %s", w0, l[0], w1, l[1], l[2])
		fmt.Println(strings.TrimRight(line, " "))
	}
}

func relPath(base, p string) string {
	if r, err := filepath.Rel(base, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// printWhatYouGot ends plat5 start: what Plat5 does for the app, in a few lines,
// each pointing at where to see it.
func printWhatYouGot(stk *stack.Stack, st state.State) {
	c := stk.Config
	file := relPath(c.Dir, c.ConfigPath)
	fmt.Println("What Plat5 does for your API")
	if stk.Has(config.ModuleAuth) {
		fmt.Println("  Sign-in        Auth signs people in; the gateway checks every token and key. Your API never sees one.")
	} else {
		fmt.Println("  Sign-in        The gateway checks every token and key. Your API never sees one.")
	}
	fmt.Println("  Tenancy        The path names the node: /orgs/acme/projects/web reaches your API with the org's and")
	fmt.Println("                 project's ids and the caller's member id. Your handler reads them; no membership checks.")
	fmt.Printf("  Permissions    required_permissions in your routes, granted by roles in %s, are checked before your code runs.\n", file)
	fmt.Printf("  Rate limits    Policies in %s, per caller unless count_at shares one budget, across every gateway replica.\n", file)
	fmt.Println("  Audit          Every write is recorded: who, where, and what changed (GET /orgs/{organization}/audit-events).")
	if stk.Has(config.ModuleOperator) {
		fmt.Printf("  Operators      Your operators reach your API by node id through the operator gateway, on the routes in\n")
		fmt.Printf("                 %s. Every call is in the operator audit log (GET /operator-audit-events).\n", relPath(c.Dir, c.OperatorRoutes))
	}
	if stk.Has(config.ModuleObservability) {
		fmt.Printf("  Traces         One request, gateway to your SQL, in Grafana, %s.\n", c.URL(ports.Grafana))
	}
	fmt.Println()
}
