package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/plat5dev/cli/internal/compose"
	"github.com/plat5dev/cli/internal/ports"
	"github.com/plat5dev/cli/internal/state"
)

// portHolders names the other Plat5 projects holding any of want, and how to
// stop each. Empty when no Plat5 project holds them.
func portHolders(want map[ports.Name]int, self string) string {
	owners := compose.PortProjects()
	held := map[string][]string{}
	for n, p := range want {
		project := owners[p]
		if project == "" || project == self || !strings.HasPrefix(project, "plat5-") {
			continue
		}
		held[project] = append(held[project], fmt.Sprintf("%s %d", n, p))
	}
	projects := make([]string, 0, len(held))
	for p := range held {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	var b strings.Builder
	for _, project := range projects {
		id := strings.TrimPrefix(project, "plat5-")
		sort.Strings(held[project])
		fmt.Fprintf(&b, "\n  Plat5 project %q holds %s.", id, strings.Join(held[project], ", "))
		if st, err := state.Load(id); err == nil && st.ConfigPath != "" {
			fmt.Fprintf(&b, " Stop it: cd %s && plat5 stop", dirOf(st.ConfigPath))
		} else {
			fmt.Fprintf(&b, " Stop it: docker compose -p %s down", project)
		}
	}
	return b.String()
}
