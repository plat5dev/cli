package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/plat5dev/cli/internal/auth"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/spf13/cobra"
)

var (
	flagLoginIssuer     string
	flagLoginDeviceCode bool
	flagLoginNoBrowser  bool
	flagLogoutAll       bool
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Sign in to a control plane via OIDC (PKCE or device code)",
	Long: `Authenticate interactively against an OIDC-enabled Plat5 control plane.

Discovers public CLI OAuth 2.0 metadata from GET /auth/info on the target
control plane, runs RFC 8252 Authorization Code + PKCE (or RFC 8628 Device
Authorization Grant with --device-code), and stores the token in
$XDG_STATE_HOME/plat5/credentials.json (0600).`,
	Args: cobra.NoArgs,
	RunE: runLogin,
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove cached control-plane login credentials",
	Args:  cobra.NoArgs,
	RunE:  runLogout,
}

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Show the verified control-plane identity and access level",
	Args:  cobra.NoArgs,
	RunE:  runWhoAmI,
}

func init() {
	loginCmd.Flags().StringVar(&flagLoginIssuer, "issuer", "", "Issuer name from GET /auth/info (required when multiple CLI issuers are configured)")
	loginCmd.Flags().BoolVar(&flagLoginDeviceCode, "device-code", false, "Use RFC 8628 OAuth 2.0 Device Authorization Grant (for headless/SSH sessions)")
	loginCmd.Flags().BoolVar(&flagLoginNoBrowser, "no-browser", false, "Print the sign-in URL instead of opening a browser")
	logoutCmd.Flags().BoolVar(&flagLogoutAll, "all", false, "Remove cached credentials for all control planes")
}

func resolveControlPlaneURL() (string, error) {
	url := firstNonEmpty(flagControlPlaneURL, os.Getenv("PLAT5_CONTROL_PLANE_URL"))
	if url != "" {
		return strings.TrimRight(url, "/"), nil
	}
	cfg, err := loadLocalProjectIfPresent("")
	if err != nil {
		return "", err
	}
	if cfg != nil && cfg.ControlPlaneURL != "" {
		return strings.TrimRight(cfg.ControlPlaneURL, "/"), nil
	}
	return defaultControlPlaneURL, nil
}

func runLogin(cmd *cobra.Command, args []string) error {
	baseURL, err := resolveControlPlaneURL()
	if err != nil {
		return err
	}
	cp := controlplane.New(baseURL, "")
	info, err := cp.AuthInfo()
	if err != nil {
		return fmt.Errorf("fetch auth info from %s: %w", baseURL, err)
	}

	var candidates []controlplane.AuthInfoIssuer
	for _, iss := range info.Issuers {
		if iss.CLI != nil {
			candidates = append(candidates, iss)
		}
	}
	if len(candidates) == 0 {
		return fmt.Errorf("control plane at %s has no interactive OIDC CLI issuer configured under cli: in AUTH_CONFIG_FILE", baseURL)
	}

	var chosen *controlplane.AuthInfoIssuer
	if flagLoginIssuer != "" {
		for i := range candidates {
			if candidates[i].Name == flagLoginIssuer {
				chosen = &candidates[i]
				break
			}
		}
		if chosen == nil {
			var names []string
			for _, c := range candidates {
				names = append(names, c.Name)
			}
			return fmt.Errorf("issuer %q not found among CLI-enabled issuers (%s)", flagLoginIssuer, strings.Join(names, ", "))
		}
	} else if len(candidates) == 1 {
		chosen = &candidates[0]
	} else {
		var names []string
		for _, c := range candidates {
			names = append(names, c.Name)
		}
		return fmt.Errorf("multiple CLI issuers configured (%s); pass --issuer <name>", strings.Join(names, ", "))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	params := auth.LoginParams{
		Issuer:                      chosen.Issuer,
		IssuerName:                  chosen.Name,
		ClientID:                    chosen.CLI.ClientID,
		Audience:                    chosen.CLI.Audience,
		Scopes:                      chosen.CLI.Scopes,
		TokenSource:                 chosen.CLI.TokenSource,
		AuthorizationEndpoint:       chosen.CLI.AuthorizationEndpoint,
		TokenEndpoint:               chosen.CLI.TokenEndpoint,
		DeviceAuthorizationEndpoint: chosen.CLI.DeviceAuthorizationEndpoint,
		NoBrowser:                   flagLoginNoBrowser,
		Out:                         os.Stdout,
	}

	var cred *auth.StoredCredential
	if flagLoginDeviceCode {
		cred, err = auth.LoginDeviceCode(ctx, params)
	} else {
		cred, err = auth.LoginPKCE(ctx, params)
	}
	if err != nil {
		return err
	}

	store, err := auth.DefaultStore()
	if err != nil {
		return err
	}
	if err := store.Save(baseURL, cred); err != nil {
		return err
	}

	authedCP := controlplane.New(baseURL, cred.ActiveToken())
	if who, err := authedCP.WhoAmI(); err == nil {
		fmt.Printf("Signed in to %s as %s (%s, access: %s).\n", baseURL, who.Actor.Subject, chosen.Name, who.Access)
		return nil
	}
	fmt.Printf("Saved credentials for %s (%s).\n", baseURL, chosen.Name)
	return nil
}

func runLogout(cmd *cobra.Command, args []string) error {
	store, err := auth.DefaultStore()
	if err != nil {
		return err
	}
	if flagLogoutAll {
		if err := store.ClearAll(); err != nil {
			return err
		}
		fmt.Println("Cleared all cached control-plane credentials.")
		return nil
	}
	baseURL, err := resolveControlPlaneURL()
	if err != nil {
		return err
	}
	deleted, err := store.Delete(baseURL)
	if err != nil {
		return err
	}
	if deleted {
		fmt.Printf("Logged out of %s.\n", baseURL)
	} else {
		fmt.Printf("No cached credentials for %s.\n", baseURL)
	}
	return nil
}

func runWhoAmI(cmd *cobra.Command, args []string) error {
	client, err := configClient()
	if err != nil {
		return err
	}
	who, err := client.WhoAmI()
	if err != nil {
		return err
	}
	fmt.Printf("Control plane: %s\n", client.BaseURL)
	fmt.Printf("Actor type:    %s\n", who.Actor.Type)
	if who.Actor.IssuerName != "" {
		fmt.Printf("Issuer name:   %s\n", who.Actor.IssuerName)
	}
	if who.Actor.Issuer != "" {
		fmt.Printf("Issuer:        %s\n", who.Actor.Issuer)
	}
	if who.Actor.Subject != "" {
		fmt.Printf("Subject:       %s\n", who.Actor.Subject)
	}
	fmt.Printf("Access:        %s\n", who.Access)
	return nil
}
