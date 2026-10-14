package cmd

import (
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
	Short: "Run Plat5 locally, and manage its routes and roles",
	Long: `Run Plat5 locally, and manage its routes and roles.

  plat5 init my-app      create a project from a starter
  cd my-app
  plat5 start            run Plat5, its modules, and your app in Docker
  plat5 status           links, logins, and keys
  plat5 stop

Everything runs in Docker. plat5.yml is the project.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command.
func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return err
	}
	return nil
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagControlPlaneURL, "control-plane-url", "", "Control plane admin URL (or PLAT5_CONTROL_PLANE_URL). Default: the local one")
	rootCmd.PersistentFlags().StringVar(&flagAdminToken, "admin-token", "", "Control plane admin token (or PLAT5_ADMIN_TOKEN). Default: plat5.yml admin_token")

	rootCmd.AddCommand(initCmd, startCmd, stopCmd, statusCmd, logsCmd, doctorCmd, routesCmd, rolesCmd, versionCmd)
}

func loadConfig() (config.Resolved, error) {
	return config.Load(config.Overrides{ControlPlaneURL: flagControlPlaneURL, AdminToken: flagAdminToken})
}
