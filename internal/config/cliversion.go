package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SchemaVersion is the oldest CLI that reads every key plat5 init writes today.
// plat5 init records it as plat5.dev.yml's cli: ">=…". Raise it with a release that
// adds a key to plat5.dev.yml, so an older CLI says to upgrade instead of failing on
// the key.
const SchemaVersion = "0.8.0"

// CLIVersion is the running CLI's version (cmd sets it). "dev", or anything
// that isn't X.Y.Z, skips the cli: check.
var CLIVersion = "dev"

// UpgradeHint is how to get a newer CLI.
const UpgradeHint = "brew upgrade plat5, or curl -fsSL https://plat5.com/install.sh | sh"

var semverRE = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func parseSemver(v string) ([3]int, bool) {
	m := semverRE.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

func semverLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// checkCLI reads plat5.dev.yml's cli: before anything else, so a CLI too old for
// the file says so, rather than failing on a key it doesn't know.
func checkCLI(data []byte) error {
	var head struct {
		CLI string `yaml:"cli"`
	}
	if yaml.Unmarshal(data, &head) != nil || strings.TrimSpace(head.CLI) == "" {
		return nil
	}
	req := strings.TrimSpace(head.CLI)
	min, ok := parseSemver(strings.TrimSpace(strings.TrimPrefix(req, ">=")))
	if !strings.HasPrefix(req, ">=") || !ok {
		return fmt.Errorf("cli: %q: want \">=X.Y.Z\", the oldest plat5 CLI this project works with", req)
	}
	have, ok := parseSemver(CLIVersion)
	if !ok {
		return nil
	}
	if semverLess(have, min) {
		return fmt.Errorf("this project needs plat5 %d.%d.%d or newer (plat5.dev.yml cli: %s), and this is plat5 %s. Upgrade: %s",
			min[0], min[1], min[2], req, strings.TrimPrefix(CLIVersion, "v"), UpgradeHint)
	}
	return nil
}

// unknownKeyHint is said after an unknown key: a newer CLI may know it.
func unknownKeyHint() string {
	v := strings.TrimPrefix(CLIVersion, "v")
	if _, ok := parseSemver(v); !ok {
		return "a newer plat5 may know it"
	}
	return fmt.Sprintf("this is plat5 %s; a newer one may know it: %s", v, UpgradeHint)
}
