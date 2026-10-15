package cmd

import (
	"fmt"
	"os"
	"sort"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/state"
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
	warnUngrantedLabels(cfg.RolesFile, files)
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

// withAppUpstreams binds the upstreams the last start pointed at the app's
// containers the same way, while plat5.yml still names the same port. Without
// it, routes apply would point them back at the host.
func withAppUpstreams(cfg config.Resolved, st state.State) (config.Resolved, map[string]state.AppUpstream) {
	if st.AppCompose == "" || len(st.AppUpstreams) == 0 {
		return cfg, nil
	}
	ups := make(map[string]string, len(cfg.Upstreams))
	for k, v := range cfg.Upstreams {
		ups[k] = v
	}
	used := map[string]state.AppUpstream{}
	for name, u := range st.AppUpstreams {
		if ups[name] == u.From {
			ups[name] = u.To
			used[name] = u
		}
	}
	cfg.Upstreams = ups
	return cfg, used
}

// printAppUpstreams says which upstreams go to the app's containers, not the host.
func printAppUpstreams(joined map[string]state.AppUpstream, from string) {
	names := make([]string, 0, len(joined))
	for n := range joined {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		u := joined[n]
		fmt.Printf("  upstreams.%s: %s → http://%s (your app's container, on the Plat5 network%s)\n", n, u.From, u.To, from)
	}
}
