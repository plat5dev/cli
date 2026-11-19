package cmd

import "errors"

var errNoApp = errors.New("no app: set app.compose in plat5.local.yml")

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
