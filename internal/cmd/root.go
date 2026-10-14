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
	flagOperatorCompose      string
	flagPlat5Version         string
	flagAuthVersion          string
	flagOperatorVersion      string
	flagControlPlaneURL      string
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
	rootCmd.PersistentFlags().StringVar(&flagOperatorCompose, "operator-compose", "", "Advanced: local Operator compose directory (or PLAT5_OPERATOR_COMPOSE)")
	rootCmd.PersistentFlags().StringVar(&flagPlat5Version, "plat5-version", "", "Runtime GHCR tag (or PLAT5_VERSION / plat5_version in yml)")
	rootCmd.PersistentFlags().StringVar(&flagAuthVersion, "auth-version", "", "Auth GHCR tag (or AUTH_VERSION / auth.version in yml)")
	rootCmd.PersistentFlags().StringVar(&flagOperatorVersion, "operator-version", "", "Operator GHCR tag (or OPERATOR_VERSION / operator.version in yml)")
	rootCmd.PersistentFlags().StringVar(&flagControlPlaneURL, "control-plane-url", "", "Control plane admin URL (or PLAT5_CONTROL_PLANE_URL / control_plane_url in yml)")
	// --registry-url is the name from before the route registry became the control plane.
	rootCmd.PersistentFlags().StringVar(&flagControlPlaneURL, "registry-url", "", "Control plane admin URL")
	_ = rootCmd.PersistentFlags().MarkDeprecated("registry-url", "use --control-plane-url")
	rootCmd.PersistentFlags().StringVar(&flagGatewayURL, "gateway-url", "", "Gateway URL")
	rootCmd.PersistentFlags().StringVar(&flagAuthURL, "auth-url", "", "Auth IdP URL")
	rootCmd.PersistentFlags().StringVar(&flagAdminToken, "admin-token", "", "Control plane admin token")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(routesCmd)
	rootCmd.AddCommand(rolesCmd)
	rootCmd.AddCommand(versionCmd)
}

func loadConfig() (config.Resolved, error) {
	cfg, err := config.Load(config.Flags{
		Plat5Compose:         flagPlat5Compose,
		AuthCompose:          flagAuthCompose,
		ObservabilityCompose: flagObservabilityCompose,
		OperatorCompose:      flagOperatorCompose,
		Plat5Version:         flagPlat5Version,
		AuthVersion:          flagAuthVersion,
		OperatorVersion:      flagOperatorVersion,
		ControlPlaneURL:      flagControlPlaneURL,
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
	if st.GatewayPort > 0 || st.ControlPlanePort > 0 || st.AuthPort > 0 || st.OperatorPort > 0 || st.OperatorIdPPort > 0 ||
		st.GrafanaPort > 0 || st.OTLPGRPCPort > 0 || st.OTLPHTTPPort > 0 || st.AlloyPort > 0 {
		config.ApplySavedPorts(&cfg, ports.Set{
			Gateway:      st.GatewayPort,
			ControlPlane: st.ControlPlanePort,
			Auth:         st.AuthPort,
			Operator:     st.OperatorPort,
			OperatorIdP:  st.OperatorIdPPort,
			Grafana:      st.GrafanaPort,
			OTLPGRPC:     st.OTLPGRPCPort,
			OTLPHTTP:     st.OTLPHTTPPort,
			Alloy:        st.AlloyPort,
		})
	}
	return cfg, st, nil
}
