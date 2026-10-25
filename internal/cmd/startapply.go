package cmd

import (
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/devseed"
	"github.com/plat5dev/cli/internal/plat5yml"
)

// applyForStart applies plat5.yml as plat5 apply does, without the prompt: start
// is not interactive. A blocked plan is printed and refused. Then it reports
// where the gateway's replicas stand (plat5 docs/reference/cli.md#the-local-stack).
func applyForStart(client *controlplane.Client, path string, w io.Writer) (*plat5yml.Config, error) {
	cfg, err := loadValid(path)
	if err != nil {
		return nil, err
	}
	p, err := client.DryRun(cfg.Flat)
	if err != nil {
		return nil, explain(cfg, err)
	}
	if p.Empty() {
		fmt.Fprintf(w, "  No changes: revision %d matches %s.\n", p.Revision, path)
		return cfg, nil
	}
	printPlan(w, p)
	if p.Summary.Blocked > 0 {
		return nil, fmt.Errorf("the plan has blocked changes; nothing applied")
	}
	applied, err := client.ApplyConfig(applyBody(cfg.Flat, p.Revision))
	if err != nil {
		return nil, explain(cfg, err)
	}
	fmt.Fprintf(w, "  Applied revision %d.\n", applied.Revision)
	if err := reportGateway(client, applied.Revision, w, "  "); err != nil {
		return nil, err
	}
	return cfg, nil
}

// rootKinds is the config's root kinds, by name, with each one's collection at
// the gateway: the first piece of its path (/orgs/{organization} is orgs).
func rootKinds(flat map[string]any) []devseed.RootKind {
	kinds, _ := flat["kinds"].(map[string]any)
	var out []devseed.RootKind
	for name, raw := range kinds {
		k, _ := raw.(map[string]any)
		if _, ok := k["parent"]; ok {
			continue
		}
		path, _ := k["path"].(string)
		collection, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
		out = append(out, devseed.RootKind{Kind: name, Collection: collection})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// urlHosts is the host of every service's url in the config, sorted.
func urlHosts(flat map[string]any) []string {
	services, _ := flat["services"].(map[string]any)
	seen := map[string]bool{}
	var out []string
	for _, raw := range services {
		svc, _ := raw.(map[string]any)
		s, _ := svc["url"].(string)
		u, err := url.Parse(s)
		if err != nil || u.Hostname() == "" || seen[u.Hostname()] {
			continue
		}
		seen[u.Hostname()] = true
		out = append(out, u.Hostname())
	}
	sort.Strings(out)
	return out
}
