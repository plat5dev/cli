package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/routegen"
	"github.com/spf13/cobra"
)

var flagPermissionSeparator string

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "The routes the gateway serves for the config",
}

var routesListCmd = &cobra.Command{
	Use:   "list [-f file ...] [plat5.yml]",
	Short: "Every service's expanded routes, identity's and audit's included",
	Long: `Every service's expanded routes, identity's and audit's included, as the
gateway matches them: method, public path, scope, upstream and permissions,
sorted by path. The config goes to the control plane as a dry run, and the
routes are the ones it expanded, so it needs a control plane, local or
deployed. Nothing is applied.

Pass -f multiple times to merge environment overlays (-f plat5.yml -f plat5.prod.yml).`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRoutesList,
}

var routesGenerateCmd = &cobra.Command{
	Use:   "generate [-f file ...] [plat5.yml]",
	Short: "Write identity's and audit's routes for every kind",
	Long: `Write identity's and audit's routes for every kind in plat5.yml, overwriting
services/identity.yml and services/audit.yml beside it. Each file's url,
timeouts and max_body_bytes are kept when it exists; everything else is
written afresh. Offline.

Pass -f multiple times to merge environment overlays (-f plat5.yml -f plat5.prod.yml).

Regenerate after adding or removing a kind, or changing a kind's path or
list_permission. Routes already claimed by another service file listed in
plat5.yml, or listed under routes.exclude in plat5.local.yml, are skipped
automatically. plat5.yml isn't changed: the files publish their routes once
its services list names them.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runRoutesGenerate,
}

func init() {
	addFileFlag(routesListCmd)
	addFileFlag(routesGenerateCmd)
	routesGenerateCmd.Flags().StringVar(&flagPermissionSeparator, "permission-separator", "", "Separator for generated permissions (:, _, ., or -; defaults to plat5.local.yml routes.permission_separator or :)")
	routesCmd.AddCommand(routesListCmd, routesGenerateCmd)
}

func resolveRouteOptions(configPath string) (routegen.Options, error) {
	opts := routegen.Options{PermissionSeparator: config.DefaultPermissionSeparator}
	localPath := filepath.Join(filepath.Dir(configPath), config.FileName)
	data, err := os.ReadFile(localPath)
	if err == nil {
		f, err := config.Parse(data)
		if err != nil {
			return opts, fmt.Errorf("%s: %w", config.FileName, err)
		}
		if s := strings.TrimSpace(f.Routes.PermissionSeparator); s != "" {
			if err := config.ValidatePermissionSeparator(s); err != nil {
				return opts, fmt.Errorf("%s: %w", config.FileName, err)
			}
			opts.PermissionSeparator = s
		}
		for _, raw := range f.Routes.Exclude {
			norm, err := config.ValidateRouteExclude(raw)
			if err != nil {
				return opts, fmt.Errorf("%s: %w", config.FileName, err)
			}
			opts.Exclude = append(opts.Exclude, norm)
		}
	} else if !os.IsNotExist(err) {
		return opts, err
	}
	if s := strings.TrimSpace(flagPermissionSeparator); s != "" {
		if err := config.ValidatePermissionSeparator(s); err != nil {
			return opts, err
		}
		opts.PermissionSeparator = s
	}
	return opts, nil
}

func runRoutesGenerate(cmd *cobra.Command, args []string) error {
	paths, err := configPaths(args)
	if err != nil {
		return err
	}
	opts, err := resolveRouteOptions(paths[0])
	if err != nil {
		return err
	}
	written, err := routegen.WriteFiles(opts, paths...)
	if err != nil {
		return err
	}
	for _, rel := range written {
		fmt.Println("  wrote", filepath.ToSlash(rel))
	}
	fmt.Println("Review the diff with git diff.")
	return nil
}

func runRoutesList(cmd *cobra.Command, args []string) error {
	paths, err := configPaths(args)
	if err != nil {
		return err
	}
	cfg, err := loadValid(paths...)
	if err != nil {
		return err
	}
	client, err := configClient()
	if err != nil {
		return err
	}
	p, err := client.DryRun(cfg.Flat)
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
			if r.Permissions != nil {
				perms = r.Permissions.String()
			}
			if r.Resource != nil {
				if perms != "" {
					perms += "  "
				}
				perms += r.Resource.String()
			}
			line := fmt.Sprintf("  %-*s  %-*s  %-*s  %-*s  %s", width[0], r.Method, width[1], r.Path, width[2], r.Scope, width[3], r.Upstream, perms)
			fmt.Fprintln(w, strings.TrimRight(line, " "))
		}
	}
}
