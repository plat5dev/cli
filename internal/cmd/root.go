package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/plat5dev/cli/internal/config"
	"github.com/spf13/cobra"
)

var (
	flagControlPlaneURL string
	flagAdminToken      string
)

var rootCmd = &cobra.Command{
	Use:   "plat5",
	Short: "Run Plat5 locally, and manage a deployment's config",
	Long: `Run Plat5 locally, and manage a deployment's config.

  plat5 init my-app      create a project: plat5.yml, services/, plat5.dev.yml
  cd my-app
  plat5 start            run Plat5, its modules, and your app in Docker
  plat5 status           links, logins, and keys
  plat5 stop

A deployment's config is plat5.yml and the service files it lists:

  plat5 validate         check it against the schema, offline
  plat5 plan             what applying it would change
  plat5 apply            plan, confirm, apply
  plat5 routes generate  identity's and audit's routes for your kinds
  plat5 config pull      an applied revision, as files`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error {
	rootCmd.SetArgs(normalizeArgs(os.Args[1:]))
	if err := rootCmd.Execute(); err != nil {
		var e exitError
		if !errors.As(err, &e) {
			fmt.Fprintln(os.Stderr, "Error:", err)
		}
		return err
	}
	return nil
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagControlPlaneURL, "control-plane-url", "", "Control plane admin URL (or PLAT5_CONTROL_PLANE_URL). Default: the local one, "+defaultControlPlaneURL)
	rootCmd.PersistentFlags().StringVar(&flagAdminToken, "admin-token", "", "Control plane admin token (or PLAT5_ADMIN_TOKEN). Default: the local one")

	rootCmd.Version = cliVersion()
	config.CLIVersion = cliVersion()
	rootCmd.SetVersionTemplate("plat5 {{.Version}}\n")
	rootCmd.AddCommand(initCmd, startCmd, stopCmd, statusCmd, logsCmd, doctorCmd, validateCmd, planCmd, applyCmd, routesCmd, configCmd, versionCmd)
}

func loadConfig() (config.Resolved, error) {
	return config.Load(config.Overrides{ControlPlaneURL: flagControlPlaneURL, AdminToken: flagAdminToken})
}
