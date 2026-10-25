package routes

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// The parts of the flat config the expansion reads (plat5
// docs/reference/config/). Read after the schema passed, so these types hold
// shape the schema already checked.

const (
	userScope     = "user"
	publicScope   = "public"
	defaultPolicy = "default"
	identity      = "identity"
	audit         = "audit"

	defaultConnectTimeout = "10s"
	defaultReadTimeout    = "30s"
	defaultMaxBodyBytes   = 10_485_760
)

// verbs are the verbs a route can have, in the order expanded routes list them.
var verbs = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}

func verbOrder(method string) int {
	for i, v := range verbs {
		if v == method {
			return i
		}
	}
	return len(verbs)
}

// auditedByDefault: the writes.
func auditedByDefault(method string) bool {
	switch method {
	case "POST", "PUT", "PATCH", "DELETE":
		return true
	}
	return false
}

type config struct {
	Defaults struct {
		Timeouts     timeouts `json:"timeouts"`
		MaxBodyBytes *uint64  `json:"max_body_bytes"`
	} `json:"defaults"`
	Kinds    map[string]kind    `json:"kinds"`
	Services map[string]service `json:"services"`
}

type timeouts struct {
	Connect *string `json:"connect"`
	Read    *string `json:"read"`
}

type kind struct {
	Parent *string `json:"parent"`
	Path   string  `json:"path"`
}

func (k kind) isRoot() bool { return k.Parent == nil }

// collection is the literal of the kind's path piece: /projects for
// /projects/{project}.
func (k kind) collection() string {
	for i := 1; i < len(k.Path); i++ {
		if k.Path[i] == '/' {
			return k.Path[:i]
		}
	}
	return k.Path
}

// chain is the kinds from the root down to k, root first.
func (c *config) chain(k string) []string {
	chain := []string{k}
	at := k
	for {
		p := c.Kinds[at].Parent
		if p == nil || contains(chain, *p) {
			break
		}
		chain = append([]string{*p}, chain...)
		at = *p
	}
	return chain
}

// nodePath is the public path of a node of kind k: each kind's piece, root first.
func (c *config) nodePath(k string) string {
	var path string
	for _, k := range c.chain(k) {
		path += c.Kinds[k].Path
	}
	return path
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// service is one service: identity and audit take resources and kinds, every
// other service scopes.
type service struct {
	URL          string                   `json:"url"`
	Timeouts     timeouts                 `json:"timeouts"`
	MaxBodyBytes *uint64                  `json:"max_body_bytes"`
	Scopes       map[string]scope         `json:"scopes"`
	Resources    map[string]resource      `json:"resources"`
	Kinds        map[string]kindResources `json:"kinds"`
}

type scope struct {
	UpstreamPrefix string  `json:"upstream_prefix"`
	Routes         []route `json:"routes"`
}

type route struct {
	Path     string  `json:"path"`
	Upstream *string `json:"upstream"`
	Methods  methods `json:"methods"`
	methodConfig
	AllowDeleted bool `json:"allow_deleted"`
}

// methods is a list of verbs, or a map of verb to its overrides (nil for a verb
// written with no body).
type methods map[string]*methodConfig

func (m *methods) UnmarshalJSON(data []byte) error {
	var list []string
	if json.Unmarshal(data, &list) == nil {
		*m = methods{}
		for _, v := range list {
			(*m)[v] = nil
		}
		return nil
	}
	return json.Unmarshal(data, (*map[string]*methodConfig)(m))
}

type methodConfig struct {
	RequiredPermissions *Required  `json:"required_permissions"`
	RateLimit           *rateLimit `json:"rate_limit"`
	Audit               *bool      `json:"audit"`
}

// Required is required_permissions: a list, any one of which admits, or
// any_member.
type Required struct {
	AnyMember   bool
	Permissions []string
}

func (r *Required) UnmarshalJSON(data []byte) error {
	var s string
	if json.Unmarshal(data, &s) == nil {
		if s != "any_member" {
			return fmt.Errorf("required_permissions: %q", s)
		}
		r.AnyMember = true
		return nil
	}
	return json.Unmarshal(data, &r.Permissions)
}

func (r Required) MarshalJSON() ([]byte, error) {
	if r.AnyMember {
		return []byte(`"any_member"`), nil
	}
	return json.Marshal(r.Permissions)
}

func (r Required) String() string {
	if r.AnyMember {
		return "any_member"
	}
	var b bytes.Buffer
	for i, p := range r.Permissions {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p)
	}
	return b.String()
}

// rateLimit is policy names, or false for none.
type rateLimit struct {
	Policies []string
}

func (r *rateLimit) UnmarshalJSON(data []byte) error {
	var off bool
	if json.Unmarshal(data, &off) == nil {
		r.Policies = []string{}
		return nil
	}
	return json.Unmarshal(data, &r.Policies)
}

// resource is an override of one of identity's or audit's resources: Off
// (false) removes it.
type resource struct {
	Off bool
	resourceOverride
}

type resourceOverride struct {
	Path    *string                  `json:"path"`
	Methods map[string]*verbOverride `json:"methods"`
	methodConfig
}

func (r *resource) UnmarshalJSON(data []byte) error {
	var off bool
	if json.Unmarshal(data, &off) == nil {
		r.Off = true
		return nil
	}
	return json.Unmarshal(data, &r.resourceOverride)
}

// verbOverride is one verb of a resource override: Off (false) removes the verb.
type verbOverride struct {
	Off bool
	methodConfig
}

func (v *verbOverride) UnmarshalJSON(data []byte) error {
	var off bool
	if json.Unmarshal(data, &off) == nil {
		v.Off = true
		return nil
	}
	return json.Unmarshal(data, &v.methodConfig)
}

type kindResources struct {
	Resources map[string]resource `json:"resources"`
}
