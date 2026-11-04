package cmd

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

// Version is set via -ldflags at release build time. Unset, the build is a dev
// build, unless go install stamped a release's module version.
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

// cliVersion is the release version: the -ldflags one, or the module version of
// a go install of a release. Anything else is "dev": a go build from a checkout
// stamps a pseudo-version (or one marked +dirty), which names no release.
func cliVersion() string {
	if Version != "dev" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && isRelease(info.Main.Version) {
		return info.Main.Version
	}
	return "dev"
}

// pseudoRE matches the timestamp and commit that end a pseudo-version.
var pseudoRE = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)

// isRelease is whether a module version names a tagged release: vX.Y.Z…, no
// pseudo-version, no +dirty.
func isRelease(v string) bool {
	return strings.HasPrefix(v, "v") && !strings.Contains(v, "+") && !pseudoRE.MatchString(v)
}
