package cmd

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/spf13/cobra"
)

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "The routes the gateway serves for the config",
}

var routesListCmd = &cobra.Command{
	Use:   "list [plat5.yml]",
	Short: "Every service's expanded routes, identity's and audit's included",
	Long: `Every service's expanded routes, identity's and audit's included, as the
gateway matches them: method, public path, scope, upstream and permissions,
sorted by path. The config goes to the control plane as a dry run, and the
routes are the ones it expanded, so it needs a control plane, local or
deployed. Nothing is applied.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRoutesList,
}

func init() {
	routesCmd.AddCommand(routesListCmd)
}

func runRoutesList(cmd *cobra.Command, args []string) error {
	cfg, err := loadValid(configPath(args))
	if err != nil {
		return err
	}
	p, err := configClient().DryRun(cfg.Flat)
	if err != nil {
		return explain(cfg, err)
	}
	printRoutes(os.Stdout, p.Services)
	return nil
}

// printRoutes writes each service, by name, and under it one route per line:
// method, public path, scope, upstream, permissions, in the order the control
// plane serves them: by path, then method. Columns line up across services.
func printRoutes(w io.Writer, services map[string]controlplane.Service) {
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)
	var width [4]int
	for _, s := range services {
		for _, r := range s.Routes {
			for i, cell := range []string{r.Method, r.Path, r.Scope, r.Upstream} {
				width[i] = max(width[i], len(cell))
			}
		}
	}
	for i, n := range names {
		if i > 0 {
			fmt.Fprintln(w)
		}
		s := services[n]
		fmt.Fprintf(w, "%s  %s\n", n, s.URL)
		for _, r := range s.Routes {
			perms := ""
			if r.RequiredPermissions != nil {
				perms = r.RequiredPermissions.String()
			}
			line := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %s", width[0], r.Method, width[1], r.Path, width[2], r.Scope, width[3], r.Upstream, perms)
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}
}
