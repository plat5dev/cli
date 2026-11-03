package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/routegen"
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

var routesGenerateCmd = &cobra.Command{
	Use:   "generate [plat5.yml]",
	Short: "Write identity's and audit's routes for every kind",
	Long: `Write identity's and audit's routes for every kind in plat5.yml, overwriting
services/identity.yml and services/audit.yml beside it. Each file's url,
timeouts and max_body_bytes are kept when it exists; everything else is
written afresh. Offline.

Regenerate after adding or removing a kind, or changing a kind's path or
list_permission. Then review the diff (git diff) and delete again any route
you had dropped. plat5.yml isn't changed: the files publish their routes once
its services list names them.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRoutesGenerate,
}

func init() {
	routesCmd.AddCommand(routesListCmd, routesGenerateCmd)
}

func runRoutesGenerate(cmd *cobra.Command, args []string) error {
	written, err := routegen.WriteFiles(configPath(args))
	if err != nil {
		return err
	}
	for _, rel := range written {
		fmt.Println("  wrote", filepath.ToSlash(rel))
	}
	fmt.Println("Review the diff, and delete again any route you had dropped.")
	return nil
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
