package cmd

import (
	"fmt"
	"os"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/upstreams"
)

// applyFiles applies routes files in order, binding plat5.yml upstreams.
// After all files, it warns about upstreams keys that matched no service, here or
// in the operator gateway's route list.
func applyFiles(client *controlplane.Client, cfg config.Resolved, files []string, skipMissing bool) error {
	ups := cfg.Upstreams
	used := operatorUpstreams(cfg)
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

// operatorUpstreams is the upstreams names the operator route list binds. A file
// that does not bind is reported where the stack writes it, not here.
func operatorUpstreams(cfg config.Resolved) map[string]bool {
	used := map[string]bool{}
	if !cfg.Modules[config.ModuleOperator] || cfg.OperatorRoutesFile == "" {
		return used
	}
	data, err := os.ReadFile(cfg.OperatorRoutesFile)
	if err != nil {
		return used
	}
	_, bound, _ := upstreams.BindOperator(data, cfg.Upstreams)
	for _, name := range bound {
		used[name] = true
	}
	return used
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
