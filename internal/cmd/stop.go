package cmd

import (
	"fmt"
	"os"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/stack"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the local stack and your app. Data is kept",
	Args:  cobra.NoArgs,
	RunE:  runStop,
}

func runStop(cmd *cobra.Command, args []string) error {
	if err := compose.DockerAvailable(); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
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

	// The app first: it calls Plat5, not the other way round. The last start's
	// app, else plat5.dev.yml's (state may be gone).
	appFile := firstNonEmpty(st.AppCompose, cfg.AppCompose)
	if appFile != "" {
		if _, err := os.Stat(appFile); err == nil {
			app := compose.Runner{Dir: dirOf(appFile), Files: []string{appFile}, Env: stk.AppEnv()}
			fmt.Println("Stopping your app…")
			if err := app.Down(); err != nil {
				fmt.Println("warning:", err)
			}
		}
	}
	if !compose.Running(cfg.ComposeProject) {
		fmt.Printf("%s is not running.\n", cfg.ProjectID)
		return nil
	}
	if err := stk.Prepare(); err != nil {
		return err
	}
	fmt.Println("Stopping Plat5…")
	if err := stk.Runner().Down(); err != nil {
		return err
	}
	st.AppCompose = ""
	if err := state.Save(st); err != nil {
		return err
	}
	fmt.Println("Stopped. Data is kept for next time.")
	return nil
}
