package cmd

import (
	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/prompt"
	"github.com/plat5dev/cli/internal/stack"
	"github.com/plat5dev/cli/internal/state"
	"github.com/spf13/cobra"
)

var (
	logsApp    bool
	logsFollow bool
)

var logsCmd = &cobra.Command{
	Use:   "logs [service...]",
	Short: "Show container logs (gateway, identity, auth, …; --app for your app)",
	Long: `Show the local stack's container logs. Name services to narrow it:

  plat5 logs gateway identity
  plat5 logs auth | grep code  login codes for new sign-ups
  plat5 logs --app`,
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().BoolVar(&logsApp, "app", false, "Your app's logs")
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", true, "Follow. The default on a terminal; piped, logs print and exit (plat5 logs auth | grep code)")
}

func runLogs(cmd *cobra.Command, args []string) error {
	if err := compose.DockerAvailable(); err != nil {
		return err
	}
	if !cmd.Flags().Changed("follow") {
		logsFollow = prompt.StdoutTerminal()
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	st, err := state.Load(cfg.ProjectID)
	if err != nil {
		return err
	}
	var exclude []string
	for m, on := range cfg.Modules {
		if on && len(st.Modules) > 0 && !contains(st.Modules, m) {
			exclude = append(exclude, m)
		}
	}
	stk, err := stack.New(cfg, exclude)
	if err != nil {
		return err
	}
	if logsApp {
		app := stk.AppRunner()
		if app == nil {
			return errNoApp
		}
		return app.Logs(logsFollow, args...)
	}
	if err := stk.Prepare(); err != nil {
		return err
	}
	return stk.Runner().Logs(logsFollow, args...)
}
