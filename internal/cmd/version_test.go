package cmd

import "testing"

// Only a tagged release's module version is a release: go build from a
// checkout stamps a pseudo-version, or +dirty, and is a dev build.
func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.9.0":                               true,
		"v1.2.3-rc.1":                          true,
		"":                                     false,
		"(devel)":                              false,
		"v0.9.0+dirty":                         false,
		"v0.9.1-0.20261104120000-b5e774d1a2b3": false,
		"v0.9.1-0.20261104120000-b5e774d1a2b3+dirty": false,
		"v0.0.0-20261104120000-b5e774d1a2b3":         false,
	} {
		if got := isRelease(v); got != want {
			t.Errorf("%q: got %v", v, got)
		}
	}
}
