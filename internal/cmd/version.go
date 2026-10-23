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
	Short: "Print the CLI version, and the image versions plat5.dev.yml pins",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("plat5 %s\n", cliVersion())
		cfg, err := loadConfig()
		if err != nil {
			return
		}
		fmt.Printf("\nplat5.dev.yml pins (plat5 init wrote the latest release of each):\n")
		for _, k := range cfg.NeededVersions() {
			if src := cfg.Source[k]; src != "" {
				fmt.Printf("  %-17s built from %s\n", k, src)
				continue
			}
			fmt.Printf("  %-17s %s\n", k, cfg.Versions[k])
		}
		fmt.Println("\nChange a pin under versions: in plat5.dev.yml.")
	},
}

// cliVersion is the release version, or the module version of a go install.
func cliVersion() string {
	v := Version
	if v == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			v = info.Main.Version
		}
	}
	return v
}
