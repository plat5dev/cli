package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// everyLabel is the label that grants every label.
const everyLabel = "*"

// warnUngrantedLabels names each required label in the routes files that no
// role grants except through "*". That is how an owner-only route is written,
// and also what a typo in a label looks like: it passes apply, and only "*"
// roles can call the route, so the people who hold "*" never notice. Roles off
// (no roles file): every member holds every label, nothing to check.
func warnUngrantedLabels(rolesFile string, routeFiles []string) {
	if rolesFile == "" {
		return
	}
	granted, every, err := grantedLabels(rolesFile)
	if err != nil {
		return
	}
	where := map[string][]string{}
	for _, f := range routeFiles {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var doc any
		if yaml.Unmarshal(data, &doc) != nil {
			continue
		}
		requiredLabels(doc, "", "", func(label, path, method string) {
			at := strings.TrimSpace(method + " " + path)
			if !contains(where[label], at) {
				where[label] = append(where[label], at)
			}
		})
	}
	labels := make([]string, 0, len(where))
	for l := range where {
		if !granted[l] {
			labels = append(labels, l)
		}
	}
	if len(labels) == 0 {
		return
	}
	sort.Strings(labels)
	held := make([]string, 0, len(labels))
	for _, l := range labels {
		held = append(held, fmt.Sprintf("%s (%s)", l, strings.Join(where[l], ", ")))
	}
	who := "no role"
	if len(every) > 0 {
		who = "only " + strings.Join(every, ", ")
	}
	fmt.Printf("  labels no role grants by name, so %s can call them: %s\n", who, strings.Join(held, "; "))
	fmt.Printf("    A typo? Fix the route. Others need it? Add it to a role in %s.\n", filepath.Base(rolesFile))
}

// grantedLabels is every label some role in the roles file grants by name, and
// the roles that hold "*".
func grantedLabels(path string) (map[string]bool, []string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var doc struct {
		Roles map[string][]string `yaml:"roles"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	out := map[string]bool{}
	var every []string
	for role, labels := range doc.Roles {
		for _, l := range labels {
			if l == everyLabel {
				every = append(every, role)
			} else {
				out[l] = true
			}
		}
	}
	sort.Strings(every)
	return out, every, nil
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
