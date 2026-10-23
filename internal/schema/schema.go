// Package schema is Plat5's config schema, schema/v2/ in the plat5 repo, copied
// into v2/ so plat5 validate needs no deployment. It tracks plat5 HEAD: copy it
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
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

//go:embed v2/*.json
var files embed.FS

const base = "https://plat5.com/schema/v2/"

// FieldError is one problem at a field path, such as services.e10s.routes[2].scope.
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
		entries, err := files.ReadDir("v2")
		if err != nil {
			compileErr = err
			return
		}
		for _, e := range entries {
			data, err := files.ReadFile("v2/" + e.Name())
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
		fe := FieldError{Path: FieldPath(instance, e.InstanceLocation), Message: e.ErrorKind.LocalizedString(p)}
		if key := fe.Error(); !seen[key] {
			seen[key] = true
			out = append(out, fe)
		}
	}
	walk(ve)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// FieldPath writes an instance location as a field path: [services e10s routes 2]
// is services.e10s.routes[2]. Index segments are those under an array.
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
	entries, err := files.ReadDir("v2")
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	for _, e := range entries {
		data, err := files.ReadFile("v2/" + e.Name())
		if err != nil {
			return nil, err
		}
		out[e.Name()] = data
	}
	return out, nil
}
