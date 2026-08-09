package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

var (
	flagPlat5Compose         string
	flagAuthCompose          string
	flagObservabilityCompose string
	flagPlat5Version         string
	flagRegistryURL          string
	flagGatewayURL           string
	flagAuthURL              string
	flagAdminToken           string
)

var rootCmd = &cobra.Command{
	Use:   "plat5",
	Short: "Plat5 local development CLI",
	Long: `Plat5 CLI — local platform + project tooling.

  mkdir my-app && cd my-app
  plat5 init --auth -y
  plat5 start
  # then run your app (init prints the exact commands)

Requires plat5.yml (from init). start pulls published images from GHCR.
See: plat5 init --help`,
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
	rootCmd.PersistentFlags().StringVar(&flagPlat5Compose, "plat5-compose", "", "Advanced: local Plat5 compose directory (or PLAT5_COMPOSE)")
	rootCmd.PersistentFlags().StringVar(&flagAuthCompose, "auth-compose", "", "Advanced: local Auth compose directory (or PLAT5_AUTH_COMPOSE)")
	rootCmd.PersistentFlags().StringVar(&flagObservabilityCompose, "observability-compose", "", "Advanced: local observability compose directory")
	rootCmd.PersistentFlags().StringVar(&flagPlat5Version, "plat5-version", "", "Image tag for GHCR pulls (or PLAT5_VERSION / plat5_version in yml)")
	rootCmd.PersistentFlags().StringVar(&flagRegistryURL, "registry-url", "", "Route registry URL")
	rootCmd.PersistentFlags().StringVar(&flagGatewayURL, "gateway-url", "", "Gateway URL")
	rootCmd.PersistentFlags().StringVar(&flagAuthURL, "auth-url", "", "Auth IdP URL")
	rootCmd.PersistentFlags().StringVar(&flagAdminToken, "admin-token", "", "Route registry admin token")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(routesCmd)
	rootCmd.AddCommand(versionCmd)
}

func loadConfig() (config.Resolved, error) {
	cfg, err := config.Load(config.Flags{
		Plat5Compose:         flagPlat5Compose,
		AuthCompose:          flagAuthCompose,
		ObservabilityCompose: flagObservabilityCompose,
		Plat5Version:         flagPlat5Version,
		RegistryURL:          flagRegistryURL,
		GatewayURL:           flagGatewayURL,
		AuthURL:              flagAuthURL,
		AdminToken:           flagAdminToken,
	})
	if err != nil {
		var np config.ErrNoProject
		if errors.As(err, &np) {
			return config.Resolved{}, err
		}
		return config.Resolved{}, err
	}
	return cfg, nil
}

// loadConfigWithState merges saved runtime ports into config when present.
func loadConfigWithState() (config.Resolved, state.State, error) {
	cfg, err := loadConfig()
	if err != nil {
		return config.Resolved{}, state.State{}, err
	}
	st, err := state.Load(cfg.ProjectID)
	if err != nil {
		return config.Resolved{}, state.State{}, err
	}
	if st.GatewayPort > 0 || st.RegistryPort > 0 || st.AuthPort > 0 ||
		st.GrafanaPort > 0 || st.OTLPGRPCPort > 0 || st.OTLPHTTPPort > 0 || st.AlloyPort > 0 {
		config.ApplySavedPorts(&cfg, ports.Set{
			Gateway:  st.GatewayPort,
			Registry: st.RegistryPort,
			Auth:     st.AuthPort,
			Grafana:  st.GrafanaPort,
			OTLPGRPC: st.OTLPGRPCPort,
			OTLPHTTP: st.OTLPHTTPPort,
			Alloy:    st.AlloyPort,
		})
	}
	return cfg, st, nil
}
