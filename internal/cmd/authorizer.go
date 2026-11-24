package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/plat5dev/cli/internal/authorizer"
	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/stack"
	"github.com/spf13/cobra"
)

var (
	authzDirectoryURL       string
	authzGatewayInternalURL string
	authzInvAll             bool
	authzInvName            string
	authzInvSubject         string
	authzInvResourceType    string
	authzInvResourceID      string
)

var authorizerCmd = &cobra.Command{
	Use:   "authorizer",
	Short: "Manage the local AuthZEN authorizer (Topaz) and gateway decision cache",
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

var authorizerInvalidateCmd = &cobra.Command{
	Use:   "invalidate",
	Short: "Flush cached outbound authorizer decisions on the gateway",
	Long: `Invalidate cached outbound AuthZEN decisions on the gateway
(POST /internal/authorizer-cache/invalidate).

Pass --all, or any combination of --authorizer, --subject, --resource-type, and
--resource-id. Against the local stack, runs inside the gateway container so
INTERNAL_PORT never needs to be published on the host; pass
--gateway-internal-url (or PLAT5_GATEWAY_INTERNAL_URL) to call an internal URL
directly.`,
	Args: cobra.NoArgs,
	RunE: runAuthorizerInvalidate,
}

func init() {
	authorizerSyncCmd.Flags().StringVar(&authzDirectoryURL, "directory-url", "", "Topaz Directory v3 REST URL (or PLAT5_TOPAZ_DIRECTORY_URL). Default: the local Topaz directory port")

	f := authorizerInvalidateCmd.Flags()
	f.StringVar(&authzGatewayInternalURL, "gateway-internal-url", "", "Gateway INTERNAL_PORT URL (or PLAT5_GATEWAY_INTERNAL_URL). Default: exec inside the local gateway container")
	f.BoolVar(&authzInvAll, "all", false, "Flush all cached authorizer decisions")
	f.StringVar(&authzInvName, "authorizer", "", "Flush cached decisions for one authorizer name")
	f.StringVar(&authzInvSubject, "subject", "", "Flush cached decisions for one subject id (e.g. member id)")
	f.StringVar(&authzInvResourceType, "resource-type", "", "Flush cached decisions for one resource type (e.g. document)")
	f.StringVar(&authzInvResourceID, "resource-id", "", "Flush cached decisions for one resource id")

	authorizerCmd.AddCommand(authorizerSyncCmd, authorizerInvalidateCmd)
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

func runAuthorizerInvalidate(cmd *cobra.Command, args []string) error {
	req := authorizer.InvalidateRequest{
		All:          authzInvAll,
		Authorizer:   strings.TrimSpace(authzInvName),
		SubjectID:    strings.TrimSpace(authzInvSubject),
		ResourceType: strings.TrimSpace(authzInvResourceType),
		ResourceID:   strings.TrimSpace(authzInvResourceID),
	}
	if req.Empty() {
		return fmt.Errorf("set --all or at least one of --authorizer, --subject, --resource-type, --resource-id")
	}

	gwInternal := firstNonEmpty(strings.TrimSpace(authzGatewayInternalURL), strings.TrimSpace(os.Getenv("PLAT5_GATEWAY_INTERNAL_URL")))
	if gwInternal != "" {
		if err := (authorizer.Client{}).InvalidateGatewayCache(gwInternal, req); err != nil {
			return err
		}
		fmt.Println("Invalidated gateway authorizer cache.")
		return nil
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if _, err := loadOwned(cfg); err != nil {
		return err
	}
	if !compose.Running(cfg.ComposeProject) {
		return fmt.Errorf("%s is not running (start it with `plat5 start`, or pass --gateway-internal-url)", cfg.ProjectID)
	}
	stk, err := stack.New(cfg, nil)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	r := stk.Runner()
	r.Quiet = true
	if _, err := r.ExecOutput("gateway",
		"curl", "-fsS", "-X", "POST",
		"http://localhost:8000/internal/authorizer-cache/invalidate",
		"-H", "Content-Type: application/json",
		"-d", string(raw),
	); err != nil {
		return err
	}
	fmt.Println("Invalidated gateway authorizer cache.")
	return nil
}
