package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/plat5dev/cli/internal/registry"
	"github.com/spf13/cobra"
)

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Manage gateway routes via route-registry",
}

var routesApplyCmd = &cobra.Command{
	Use:   "apply [file...]",
	Short: "Apply routes.yml file(s) to the route registry",
	RunE:  runRoutesApply,
}

var routesListCmd = &cobra.Command{
	Use:   "list",
	Short: "List registered services",
	RunE:  runRoutesList,
}

var routesGetCmd = &cobra.Command{
	Use:   "get <name>",
	Short: "Get one service route config",
	Args:  cobra.ExactArgs(1),
	RunE:  runRoutesGet,
}

var routesRmForce bool

var routesRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Delete a service from the route registry",
	Args:  cobra.ExactArgs(1),
	RunE:  runRoutesRm,
}

func init() {
	routesRmCmd.Flags().BoolVar(&routesRmForce, "force", false, "Allow deleting platform services (identity)")
	routesCmd.AddCommand(routesApplyCmd)
	routesCmd.AddCommand(routesListCmd)
	routesCmd.AddCommand(routesGetCmd)
	routesCmd.AddCommand(routesRmCmd)
}

func registryClient() (*registry.Client, error) {
	cfg, _, err := loadConfigWithState()
	if err != nil {
		return nil, err
	}
	return registry.New(cfg.RegistryURL, cfg.AdminToken), nil
}

func runRoutesApply(cmd *cobra.Command, args []string) error {
	cfg, _, err := loadConfigWithState()
	if err != nil {
		return err
	}
	files := args
	if len(files) == 0 {
		files = cfg.RouteFiles
	}

	client := registry.New(cfg.RegistryURL, cfg.AdminToken)
	for _, f := range files {
		fmt.Printf("Applying %s…\n", f)
		results, err := client.Apply(f, cfg.Upstreams)
		for _, r := range results {
			if r.Error != "" {
				fmt.Printf("  %s: %s (%s)\n", r.Service, r.Status, r.Error)
			} else {
				fmt.Printf("  %s: %s\n", r.Service, r.Status)
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
	}
	return nil
}

func runRoutesList(cmd *cobra.Command, args []string) error {
	client, err := registryClient()
	if err != nil {
		return err
	}
	services, err := client.List()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Println("(no services)")
		return nil
	}
	for _, name := range names {
		fmt.Println(name)
	}
	return nil
}

func runRoutesGet(cmd *cobra.Command, args []string) error {
	client, err := registryClient()
	if err != nil {
		return err
	}
	raw, err := client.Get(args[0])
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		fmt.Println(string(raw))
		return nil
	}
	fmt.Println(pretty.String())
	return nil
}

func runRoutesRm(cmd *cobra.Command, args []string) error {
	client, err := registryClient()
	if err != nil {
		return err
	}
	if err := client.Delete(args[0], routesRmForce); err != nil {
		return err
	}
	fmt.Printf("Deleted %s\n", args[0])
	return nil
}
