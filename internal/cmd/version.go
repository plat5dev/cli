package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Version is set via -ldflags at release build time.
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print plat5 CLI version",
	Run: func(cmd *cobra.Command, args []string) {
		v := Version
		if v == "dev" {
			if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
				v = info.Main.Version
			}
		}
		fmt.Println(v)
	},
}
