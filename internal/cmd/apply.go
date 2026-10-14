package cmd

import (
	"fmt"
	"os"

	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/upstreams"
)

// applyFiles applies routes files in order, binding plat5.yml upstreams.
// After all files, it warns about upstreams keys that matched no service.
func applyFiles(client *controlplane.Client, files []string, ups map[string]string, skipMissing bool) error {
	used := map[string]bool{}
	for _, f := range files {
		if skipMissing {
			if _, err := os.Stat(f); err != nil {
				fmt.Printf("  skipping %s (%v)\n", f, err)
				continue
			}
		}
		results, bound, err := client.Apply(f, ups)
		for _, r := range results {
			printApplyResult(r)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		for _, name := range bound {
			used[name] = true
		}
	}
	for _, name := range upstreams.Unused(ups, used) {
		fmt.Printf("  warning: upstreams.%s matches no service in the routes files (not applied)\n", name)
	}
	return nil
}

// applyRolesFile writes the roles file as the control plane's roles document.
// No file is roles off: nothing to apply.
func applyRolesFile(client *controlplane.Client, path string) error {
	if path == "" {
		return nil
	}
	rev, err := client.PutDocument("roles", path)
	if err != nil {
		return fmt.Errorf("roles %s: %w", path, err)
	}
	fmt.Printf("  roles: revision %d\n", rev)
	return nil
}
