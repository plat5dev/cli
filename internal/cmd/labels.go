package cmd

import (
	"fmt"
	"sort"
	"strings"
)

// everyLabel is the label that grants every label.
const everyLabel = "*"

// warnUngrantedLabels checks the labels the config's routes require against the
// labels its roles grant.
//
// A label on one side only, a letter or two from a label on the other, is
// likely a typo, on either side: it passes apply, and the route is then
// owner-only, or the role silently lacks the label. Those are warned. A
// required label that no role grants by name, and that is near nothing, is how
// an owner-only route is written ("*" roles call it): that is a quiet note. A
// granted label no route requires yet is fine.
func warnUngrantedLabels(flat map[string]any, configFile string) {
	granted, every := grantedLabels(flat)
	required := map[string][]string{}
	requiredLabels(flat["services"], "", "", func(label, path, method string) {
		at := strings.TrimSpace(method + " " + path)
		if !contains(required[label], at) {
			required[label] = append(required[label], at)
		}
	})
	where := func(label string) string {
		if at, ok := required[label]; ok {
			return strings.Join(at, ", ")
		}
		return "role " + strings.Join(granted[label], ", ")
	}

	// Typos: a label only one side has, near a label the other side has.
	var typos []string
	near := map[string]bool{}
	for _, a := range sortedKeys(required) {
		for _, b := range sortedKeys(granted) {
			if a == b || labelDistance(a, b) > 2 {
				continue
			}
			_, aGranted := granted[a]
			_, bRequired := required[b]
			if aGranted && bRequired {
				continue // both are real labels, on both sides
			}
			typos = append(typos, fmt.Sprintf("%s (%s) and %s (%s)", a, where(a), b, where(b)))
			near[a], near[b] = true, true
		}
	}
	if len(typos) > 0 {
		fmt.Printf("  warning: labels a letter or two apart, one in routes and one in %s's roles. A typo?\n", configFile)
		for _, t := range typos {
			fmt.Printf("    %s\n", t)
		}
	}

	// Owner-only: required, granted by no role by name, and not a typo above.
	var only []string
	for _, l := range sortedKeys(required) {
		if _, ok := granted[l]; !ok && !near[l] {
			only = append(only, fmt.Sprintf("%s (%s)", l, where(l)))
		}
	}
	if len(only) == 0 {
		return
	}
	if len(every) == 0 {
		fmt.Printf("  warning: labels no role grants, so no member can call these routes: %s\n", strings.Join(only, "; "))
		fmt.Printf("    Add them to a role in %s.\n", configFile)
		return
	}
	fmt.Printf("  %s only (no role grants these by name): %s\n", strings.Join(every, ", "), strings.Join(only, "; "))
}

// labelDistance is the edit distance between two labels, counting a swap of
// two neighbouring characters as one edit (raed / read).
func labelDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// grantedLabels is every label some role in the config grants by name, with the
// roles that grant it, and the roles that hold "*".
func grantedLabels(flat map[string]any) (map[string][]string, []string) {
	roles, _ := flat["roles"].(map[string]any)
	out := map[string][]string{}
	var every []string
	for _, name := range sortedKeys(roles) {
		role, _ := roles[name].(map[string]any)
		labels, _ := role["labels"].([]any)
		for _, l := range labels {
			s, _ := l.(string)
			switch {
			case s == everyLabel:
				every = append(every, name)
			case s != "":
				out[s] = append(out[s], name)
			}
		}
	}
	return out, every
}

// requiredLabels calls found for each required_labels entry in a routes
// document, with the nearest route path and method above it.
func requiredLabels(node any, path, method string, found func(label, path, method string)) {
	switch n := node.(type) {
	case map[string]any:
		if p, ok := n["path"].(string); ok {
			path = p
		}
		if ls, ok := n["required_labels"].([]any); ok {
			for _, l := range ls {
				if s, ok := l.(string); ok {
					found(s, path, method)
				}
			}
		}
		for k, v := range n {
			m := method
			if k == strings.ToUpper(k) && k != "" {
				m = k // a verb in a nested methods map
			}
			requiredLabels(v, path, m, found)
		}
	case []any:
		for _, v := range n {
			requiredLabels(v, path, method, found)
		}
	}
}
