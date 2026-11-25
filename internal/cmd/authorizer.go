package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/plat5dev/cli/internal/authorizer"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/spf13/cobra"
)

var authzDirectoryURL string

var authorizerCmd = &cobra.Command{
	Use:   "authorizer",
	Short: "Manage the local AuthZEN authorizer (Topaz)",
}

var authorizerSyncCmd = &cobra.Command{
	Use:   "sync [manifest.yaml]",
	Short: "Upload the Topaz ReBAC manifest without restarting the stack",
	Long: `Upload a Topaz ReBAC manifest (YAML) to the local Topaz Directory v3 REST API.

Without an argument, uses authorizer.manifest from plat5.local.yml (typically
./topaz/manifest.yaml). Pass --directory-url (or PLAT5_TOPAZ_DIRECTORY_URL) to
target a different Topaz directory endpoint.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runAuthorizerSync,
}

func init() {
	authorizerSyncCmd.Flags().StringVar(&authzDirectoryURL, "directory-url", "", "Topaz Directory v3 REST URL (or PLAT5_TOPAZ_DIRECTORY_URL). Default: the local Topaz directory port")
	authorizerCmd.AddCommand(authorizerSyncCmd)
}

func runAuthorizerSync(cmd *cobra.Command, args []string) error {
	dirURL := firstNonEmpty(strings.TrimSpace(authzDirectoryURL), strings.TrimSpace(os.Getenv("PLAT5_TOPAZ_DIRECTORY_URL")))
	manifest := ""
	if len(args) == 1 {
		manifest = args[0]
	}

	if dirURL == "" || manifest == "" {
		cfg, err := loadConfig()
		if err != nil {
			return err
		}
		if dirURL == "" {
			dirURL = cfg.URL(ports.TopazDirectory)
		}
		if manifest == "" {
			manifest = cfg.Authorizer.Manifest
		}
	}
	if manifest == "" {
		return fmt.Errorf("no manifest path: pass a file argument or set authorizer.manifest in %s", config.FileName)
	}
	if err := (authorizer.Client{}).SyncTopazManifest(dirURL, manifest); err != nil {
		return err
	}
	fmt.Printf("Synced %s to %s\n", manifest, dirURL)
	return nil
}
