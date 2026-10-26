package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/charmbracelet/huh"
	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/prompt"
	"github.com/plat5dev/cli/internal/stack"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

var resetYes bool

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Stop the local stack and your app, and delete their data",
	Long: `Stop the local stack and your app, and delete the project's volumes: Plat5's
and the operator's databases, your apps' volumes, Grafana's data. The next
plat5 start begins empty and seeds a fresh dev person.

For a stack whose schema changed in place, or to start over. Asks first; -y
doesn't, and without a terminal -y is required.`,
	Args: cobra.NoArgs,
	RunE: runReset,
}

func init() {
	resetCmd.Flags().BoolVarP(&resetYes, "yes", "y", false, "Delete without asking")
}

func runReset(cmd *cobra.Command, args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if !resetYes {
		if !prompt.Interactive() {
			return errors.New("reset deletes the project's data: pass -y to confirm")
		}
		ok := false
		err := huh.NewConfirm().
			Title(fmt.Sprintf("Delete all local data for %s?", cfg.ProjectID)).
			Description("Its databases, your apps' volumes and the dev person. plat5 start begins empty.").
			Affirmative("Delete").Negative("Cancel").Value(&ok).Run()
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Nothing deleted.")
			return nil
		}
	}
	if err := compose.DockerAvailable(); err != nil {
		return err
	}
	st, err := state.Load(cfg.ProjectID)
	if err != nil {
		return err
	}
	stk, err := stack.New(cfg, nil)
	if err != nil {
		return err
	}

	// The app first, as in stop. The last start's app, else plat5.dev.yml's.
	appFile := firstNonEmpty(st.AppCompose, cfg.AppCompose)
	if appFile != "" {
		if _, err := os.Stat(appFile); err == nil {
			app := compose.Runner{Dir: dirOf(appFile), Files: []string{appFile}, Env: stk.AppEnv()}
			fmt.Println("Deleting your app's containers and volumes…")
			if err := app.DownVolumes(); err != nil {
				return err
			}
		}
	}
	// Plat5's volumes go whether or not it's running.
	if err := stk.Prepare(); err != nil {
		return err
	}
	fmt.Println("Deleting Plat5's containers and volumes…")
	if err := stk.Runner().DownVolumes(); err != nil {
		return err
	}
	// The dev person's key and nodes were in the deleted databases.
	st.AppCompose = ""
	st.Dev = nil
	if err := state.Save(st); err != nil {
		return err
	}
	fmt.Println("Reset. plat5 start begins empty.")
	return nil
}
