package cmd

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/devseed"
	"github.com/plat5dev/cli/internal/plat5yml"
	"gopkg.in/yaml.v3"
)

// applyForStart applies an already-loaded plat5.yml as plat5 apply does,
// without the prompt: start is not interactive. The first apply, against
// revision 0, prints only the summary: the whole config is new. A blocked plan
// is printed and refused. Then it reports where the gateway's replicas stand.
func applyForStart(client *controlplane.Client, cfg *plat5yml.Config, w io.Writer) error {
	p, err := client.DryRun(cfg.Flat)
	if err != nil {
		return explain(cfg, err)
	}
	if p.Empty() {
		fmt.Fprintf(w, "  No changes: revision %d matches %s.\n", p.Revision, cfg.Path)
		return nil
	}
	if p.Revision == 0 && p.Summary.Blocked == 0 {
		s := p.Summary
		fmt.Fprintf(w, "  First apply: %d to add.\n", s.Add)
	} else {
		printPlan(w, p)
	}
	if p.Summary.Blocked > 0 {
		return fmt.Errorf("the plan has blocked changes; nothing applied")
	}
	applied, err := client.ApplyConfig(applyBody(cfg.Flat, p.Revision))
	if err != nil {
		return explain(cfg, err)
	}
	fmt.Fprintf(w, "  Applied revision %d.\n", applied.Revision)
	return reportGateway(client, applied.Revision, w, "  ")
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

// urlHosts is the host of every service url and optional authorizer url in the config, sorted.
func urlHosts(flat map[string]any) []string {
	return collectURLHosts(flat, config.Resolved{})
}

// collectURLHosts discovers container hostnames across plat5.yml (services.*.url,
// authorizer.url), operator.yml (services.*.url, authorizer.url), and plat5.local.yml
// (idp.jwks_uri, operator.idp.jwks_uri, otel.endpoint), sorted and deduplicated.
func collectURLHosts(flat map[string]any, cfg config.Resolved) []string {
	seen := map[string]bool{}
	var out []string
	add := func(rawURL string) {
		u, err := url.Parse(strings.TrimSpace(rawURL))
		if err != nil || u.Hostname() == "" || seen[u.Hostname()] {
			return
		}
		seen[u.Hostname()] = true
		out = append(out, u.Hostname())
	}
	addMapURLs := func(doc map[string]any) {
		services, _ := doc["services"].(map[string]any)
		for _, raw := range services {
			svc, _ := raw.(map[string]any)
			s, _ := svc["url"].(string)
			add(s)
		}
		if authz, ok := doc["authorizer"].(map[string]any); ok {
			s, _ := authz["url"].(string)
			add(s)
		}
	}
	addMapURLs(flat)
	if cfg.OperatorConfig != "" {
		if data, err := os.ReadFile(cfg.OperatorConfig); err == nil {
			var opDoc map[string]any
			if yaml.Unmarshal(data, &opDoc) == nil {
				addMapURLs(opDoc)
			}
		}
	}
	add(cfg.IdP.JWKSURI)
	add(cfg.OperatorIdP.JWKSURI)
	add(cfg.OtelEndpoint)
	sort.Strings(out)
	return out
}
