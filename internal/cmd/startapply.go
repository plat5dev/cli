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

// rootKinds is the config's root kinds other than the sign-in kind, by name,
// with each one's path segment at the gateway.
func rootKinds(flat map[string]any) []devseed.RootKind {
	kinds, _ := flat["kinds"].(map[string]any)
	var out []devseed.RootKind
	for name, raw := range kinds {
		k, _ := raw.(map[string]any)
		if k == nil {
			k = map[string]any{}
		}
		if _, ok := k["parent"]; ok {
			continue
		}
		if _, ok := k["sign_in"]; ok {
			continue
		}
		seg, _ := k["segment"].(string)
		if seg == "" {
			seg = defaultSegment(name)
		}
		out = append(out, devseed.RootKind{Kind: name, Segment: seg})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// defaultSegment is a kind's segment when the config sets none: its name with
// _ as -, plus s (plat5 docs/reference/config/kinds.md).
func defaultSegment(kind string) string {
	return strings.ReplaceAll(kind, "_", "-") + "s"
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
