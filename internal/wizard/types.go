// Package wizard implements the interactive Bubble Tea setup wizard for `plat5 init`.
// Available plugins are loaded dynamically from configured registries so first-party
// and third-party plugins appear side-by-side with zero hardcoded lists.
package wizard

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/plat5dev/cli/internal/plugin"
	"github.com/plat5dev/cli/internal/registry"
)

// Item is one selectable plugin in the wizard catalog.
type Item struct {
	ID          string // bare ID for default registry, or "reg/id" for 3rd-party
	Name        string
	Registry    string
	Type        string
	Description string
	Tags        []string
	Compatible  bool
}

// Catalog holds all available plugins grouped by plugin type.
type Catalog struct {
	ByType map[string][]Item
}

// LoadCatalog queries all configured registries via res and groups entries by type.
func LoadCatalog(res *registry.Resolver) (*Catalog, error) {
	results, err := res.Search("", "")
	if err != nil {
		return nil, err
	}
	cat := &Catalog{ByType: map[string][]Item{}}
	for _, r := range results {
		if r.Entry.Deprecated != "" {
			continue
		}
		id := r.Entry.ID
		if r.Registry != registry.DefaultRegistryName {
			id = r.Registry + "/" + r.Entry.ID
		}
		cat.ByType[r.Entry.Type] = append(cat.ByType[r.Entry.Type], Item{
			ID:          id,
			Name:        r.Entry.ID,
			Registry:    r.Registry,
			Type:        r.Entry.Type,
			Description: r.Entry.Description,
			Tags:        r.Entry.Tags,
			Compatible:  r.Compatible,
		})
	}
	// Put recommended defaults first in each category for ergonomic Enter-through flow.
	preferred := map[string][]string{
		plugin.TypeRecipe:        {"quickstart", "b2b-saas", "enterprise-sso"},
		plugin.TypePreset:        {"saas", "minimal", "enterprise", "public"},
		plugin.TypeIdP:           {"plat5-auth", "auth0", "okta", "keycloak", "custom-oidc"},
		plugin.TypeAuthorizer:    {"plat5-rbac", "cerbos", "topaz", "custom-authzen"},
		plugin.TypeService:       {"bun-effect-api", "go-fiber-api", "node-fastify-api", "go-topaz-api"},
		plugin.TypeObservability: {"local-lgtm", "grafana-cloud", "custom-otlp"},
	}
	for typ, items := range cat.ByType {
		order := preferred[typ]
		rankOf := func(id string) int {
			for i, want := range order {
				if id == want {
					return i
				}
			}
			return len(order) + 1
		}
		sort.SliceStable(items, func(i, j int) bool {
			ri, rj := rankOf(items[i].ID), rankOf(items[j].ID)
			if ri != rj {
				return ri < rj
			}
			return items[i].ID < items[j].ID
		})
		cat.ByType[typ] = items
	}
	return cat, nil
}

// Items returns catalog items for a plugin type.
func (c *Catalog) Items(typ string) []Item {
	if c == nil {
		return nil
	}
	return c.ByType[typ]
}

// Options configures an interactive wizard session.
type Options struct {
	// Dir is the positional directory arg if already provided on the CLI.
	Dir string
	// ProjectID is --project-id if explicitly set on the CLI.
	ProjectID string
	// NoVerify skips live HTTP verify probes after plugin prompts.
	NoVerify bool
	// HTTPClient overrides the HTTP client used for verify probes (tests).
	HTTPClient *http.Client
}

// Result is the finalized selection produced by the wizard.
type Result struct {
	Dir        string
	Recipe     string
	Slots      map[string][]string
	NoOperator bool
	Cancelled  bool
}

// SummaryLine formats a compact value for the sidebar preview.
func (r Result) SummaryLine(slot, fallback string) string {
	vals := r.Slots[slot]
	if len(vals) == 0 {
		return fallback
	}
	if len(vals) == 1 {
		return vals[0]
	}
	return fmt.Sprintf("%s (+%d)", vals[0], len(vals)-1)
}
