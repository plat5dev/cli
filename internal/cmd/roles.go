package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/spf13/cobra"
)

var rolesCmd = &cobra.Command{
	Use:   "roles",
	Short: "Manage the deployment's roles on the control plane",
	Long: `Manage the deployment's roles document on the control plane.

Identity reads roles from the control plane and picks up a change without a
restart. plat5 start applies the roles file named in plat5.yml.`,
}

var rolesApplyCmd = &cobra.Command{
	Use:   "apply [file]",
	Short: "Apply a roles file (default: roles in plat5.yml)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runRolesApply,
}

var rolesShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the current roles document",
	Args:  cobra.NoArgs,
	RunE:  runRolesShow,
}

func init() {
	rolesCmd.AddCommand(rolesApplyCmd)
	rolesCmd.AddCommand(rolesShowCmd)
}

func runRolesApply(cmd *cobra.Command, args []string) error {
	cfg, _, err := loadConfigWithState()
	if err != nil {
		return err
	}
	path := cfg.RolesFile
	if len(args) == 1 {
		path = args[0]
	}
	if path == "" {
		return fmt.Errorf("no roles file: pass one, or set roles in plat5.yml (roles are off without it)")
	}
	if err := config.CheckRolesFile(path); err != nil {
		return err
	}
	return applyRolesFile(controlplane.New(cfg.ControlPlaneURL, cfg.AdminToken), path)
}

func runRolesShow(cmd *cobra.Command, args []string) error {
	client, err := controlPlaneClient()
	if err != nil {
		return err
	}
	raw, err := client.GetDocument("roles")
	var apiErr *controlplane.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
		fmt.Println("(no roles document)")
		return nil
	}
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
