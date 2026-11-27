// Package schema is Plat5's config schema, schema/ in the plat5 repo, copied
// into files/ so plat5 validate needs no deployment. It tracks plat5 HEAD: copy it
// again when plat5's changes (TestMatchesPlat5 checks a sibling checkout).
package schema

import (
	"bytes"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

//go:embed files/*.json
var files embed.FS

const base = "https://plat5.com/schema/"

// FieldError is one problem at a field path, such as services.e10s.scopes.project.routes[2].path.
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func (e FieldError) Error() string {
	if e.Path == "" {
		return e.Message
	}
	return e.Path + ": " + e.Message
}

var (
	compileOnce sync.Once
	config      *jsonschema.Schema
	compileErr  error
)

func compiled() (*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		c := jsonschema.NewCompiler()
		entries, err := files.ReadDir("files")
		if err != nil {
			compileErr = err
			return
		}
		for _, e := range entries {
			data, err := files.ReadFile("files/" + e.Name())
			if err != nil {
				compileErr = err
				return
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				compileErr = fmt.Errorf("schema %s: %w", e.Name(), err)
				return
			}
			if err := c.AddResource(base+e.Name(), doc); err != nil {
				compileErr = err
				return
			}
		}
		config, compileErr = c.Compile(base + "config.json")
	})
	return config, compileErr
}

// ValidateConfig checks the flat config (plat5.yml's keys, services as a map)
// against config.json. instance must be JSON-shaped: maps, slices, strings,
// float64 or json.Number, bools, nil.
func ValidateConfig(instance any) ([]FieldError, error) {
	sch, err := compiled()
	if err != nil {
		return nil, err
	}
	err = sch.Validate(instance)
	if err == nil {
		return nil, nil
	}
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return nil, err
	}
	p := message.NewPrinter(language.English)
	seen := map[string]bool{}
	var out []FieldError
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) > 0 {
			for _, c := range e.Causes {
				walk(c)
			}
			return
		}
		fe := FieldError{Path: FieldPath(instance, e.InstanceLocation), Message: formatError(e, p)}
		if key := fe.Error(); !seen[key] {
			seen[key] = true
			out = append(out, fe)
		}
	}
	walk(ve)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// patternHints maps schema schema-URL fragments to plain-English explanations
// so schema errors don't dump raw regular expressions at users.
var patternHints = map[string]string{
	"services.json#/$defs/service/properties/url":             "must be http://host[:port] or https://host[:port] (no path, query, or credentials; port 1–65535)",
	"services.json#/$defs/service_name":                       "must be 1–128 characters of [A-Za-z0-9._-]",
	"services.json#/$defs/scope_name":                         "must start with a lowercase letter and use only lowercase letters, digits, and _ (max 32 chars)",
	"services.json#/$defs/path":                               "must start with / and contain only visible ASCII characters",
	"services.json#/$defs/upstream":                           "must be an absolute path starting with / (no ? or #)",
	"kinds.json#/$defs/kind_name":                             "must start with a lowercase letter and use only lowercase letters, digits, and _ (max 32 chars)",
	"kinds.json#/$defs/path":                                  "must be a canonical path like /orgs/{organization}",
	"roles.json#/$defs/role_slug":                             "must start with a lowercase letter and use only lowercase letters, digits, :, ., _, and - (max 64 chars)",
	"roles.json#/$defs/permission":                            "must be 1–64 characters of [a-z0-9:._-]",
	"rate_limits.json#/$defs/policy_name":                     "must be 1–64 characters of [a-z0-9:._-]",
	"authorizer.json#/$defs/authorizer/properties/url":        "must be http://host[:port][/path] or https://host[:port][/path] (no trailing /, query, or credentials)",
	"authorizer.json#/$defs/authorizer/properties/token_env":  "must be an uppercase environment variable name ([A-Z_][A-Z0-9_]*)",
	"authorizer.json#/$defs/non_negative_duration":            "must be 0s or a positive duration like 5s, 1m, 24h",
	"authorizer.json#/$defs/resource/properties/type":         "must be 1–64 characters of [A-Za-z0-9:._-]",
	"settings.json#/properties/policy_claims/items":           "must be dot-separated claim path segments of [A-Za-z0-9_-] (max 64 chars)",
	"settings.json#/$defs/duration":                           "must be a positive duration like 10s, 5m, 24h, or 30d",
	"settings.json#/$defs/slug":                               "must be a lowercase slug (letters, digits, hyphens)",
}

func formatError(e *jsonschema.ValidationError, p *message.Printer) string {
	if pk, ok := e.ErrorKind.(*kind.Pattern); ok {
		for suffix, hint := range patternHints {
			if strings.HasSuffix(e.SchemaURL, suffix) {
				return fmt.Sprintf("%q %s", pk.Got, hint)
			}
		}
	}
	return e.ErrorKind.LocalizedString(p)
}

// FieldPath writes an instance location as a field path: [services e10s scopes project routes 2]
// is services.e10s.scopes.project.routes[2]. Index segments are those under an array.
func FieldPath(instance any, location []string) string {
	var b strings.Builder
	at := instance
	for _, seg := range location {
		switch v := at.(type) {
		case []any:
			fmt.Fprintf(&b, "[%s]", seg)
			if i, err := strconv.Atoi(seg); err == nil && i < len(v) {
				at = v[i]
			} else {
				at = nil
			}
		default:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(seg)
			if m, ok := v.(map[string]any); ok {
				at = m[seg]
			} else {
				at = nil
			}
		}
	}
	return b.String()
}

// Files is each schema file's bytes, by name.
func Files() (map[string][]byte, error) {
	entries, err := files.ReadDir("files")
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, e := range entries {
		data, err := files.ReadFile("files/" + e.Name())
		if err != nil {
			return nil, err
		}
		out[e.Name()] = data
	}
	return out, nil
}
