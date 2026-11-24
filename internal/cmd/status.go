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
	st, err := loadOwned(cfg)
	if err != nil {
		return err
	}
	if !compose.Running(cfg.ComposeProject) {
		fmt.Printf("%s is not running. Start it: plat5 start\n", cfg.ProjectID)
		return nil
	}
	// What the last start ran, not what plat5.local.yml says now.
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
	if st.AppCompose != "" {
		ar := stk.AppRunnerFor(st.AppCompose)
		ar.Quiet = true
		if out, err := ar.Ps(); err == nil && strings.TrimSpace(out) != "" {
			fmt.Println("\nApp containers")
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				fmt.Println("  " + line)
			}
		}
	}
	return nil
}

// printSummary is what start ends with, and status begins with: the consoles
// first, then Plat5's links, then the dev person's key.
func printSummary(stk *stack.Stack, st state.State) {
	c := stk.Config
	w := &table{}

	fmt.Printf("%s is running.\n\n", c.ProjectID)
	if web := customerConsole(stk, st); web != "" {
		w.row("Customer console", web, loginHint(stk, config.DevEmail))
	}
	if web := operatorConsole(stk, st); web != "" {
		w.row("Operator console", web, config.OperatorEmail+" / "+config.DevPassword)
	}

	w.section("Plat5")
	w.row("Gateway", c.GatewayURL(), "")
	if stk.Has(config.ModuleAuth) {
		w.row("Auth", c.URL(ports.Auth), "sign-up codes: plat5 logs auth | grep code")
	} else {
		w.row("IdP", c.IdP.Issuer, "")
	}
	w.row("Control plane", c.ControlPlaneURL, "admin token "+c.AdminToken)
	if stk.Has(config.ModuleOperator) {
		w.row("Operator gateway", c.URL(ports.Operator), "routes: "+relPath(c.Dir, c.OperatorRoutes))
		w.row("Operator IdP", c.URL(ports.OperatorAuth), "")
	}
	if stk.Has(config.ModuleAuthorizer) {
		note := ""
		if c.Authorizer.Manifest != "" {
			note = "manifest: " + relPath(c.Dir, c.Authorizer.Manifest)
		}
		w.row("Topaz (AuthZEN)", c.URL(ports.Topaz), note)
		w.row("Topaz directory", c.URL(ports.TopazDirectory), "sync: plat5 authorizer sync")
	}
	if stk.Has(config.ModuleObservability) {
		w.row("Grafana", c.URL(ports.Grafana), "Plat5 folder: Service Health, Auth, Debug")
	}

	if d := st.Dev; d != nil {
		w.section("Dev person  " + d.Email)
		w.row("Personal key", d.Key, "acts as them in every tree")
	}
	w.print()
	fmt.Println()
}

// customerConsole is the customer console's origin, when the last start ran it.
func customerConsole(stk *stack.Stack, st state.State) string {
	if st.AppCompose == "" {
		return ""
	}
	return stk.Config.AppWeb
}

// operatorConsole is the operator console's origin, when the last start ran it:
// it runs with the operator module.
func operatorConsole(stk *stack.Stack, st state.State) string {
	if st.AppCompose == "" || !stk.Has(config.ModuleOperator) {
		return ""
	}
	return stk.Config.OperatorWeb
}

// printOpen ends plat5 start: where to go next, when there's a console to open.
func printOpen(stk *stack.Stack, st state.State) {
	web := customerConsole(stk, st)
	if web == "" {
		return
	}
	if login := loginHint(stk, config.DevEmail); login != "" {
		fmt.Printf("Open %s and sign in as %s.\n", web, login)
	} else {
		fmt.Printf("Open %s.\n", web)
	}
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
