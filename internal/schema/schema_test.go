package schema

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sample() map[string]any {
	return map[string]any{
		"kinds": map[string]any{
			"organization": map[string]any{"path": "/orgs/{organization}", "list_permission": "organization:read"},
		},
		"roles": map[string]any{
			"admin": map[string]any{"permissions": []any{"*"}, "bindable_at": []any{"organization"}},
		},
		"creator_role": "admin",
		"rate_limits": map[string]any{
			"standard": map[string]any{"requests": 60, "window": "60s"},
		},
		"defaults": map[string]any{"rate_limit": []any{"standard"}},
		"services": map[string]any{
			"e10s": map[string]any{
				"url": "http://e10s:3000",
				"scopes": map[string]any{
					"public": map[string]any{"routes": []any{
						map[string]any{"path": "/h", "methods": []any{"GET"}},
						map[string]any{"path": "/x"},
					}},
				},
			},
		},
	}
}

func TestErrorsNameFieldPaths(t *testing.T) {
	errs, err := ValidateConfig(sample())
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 1 || errs[0].Path != "services.e10s.scopes.public.routes[1]" {
		t.Fatalf("got %v", errs)
	}
}

// defaults and rate_limits are optional; when defaults is set, defaults.rate_limit is validated.
func TestDefaultsRateLimit(t *testing.T) {
	c := sample()
	c["services"] = map[string]any{}
	c["rate_limits"] = map[string]any{"default": map[string]any{"requests": 1, "window": "1s"}}
	c["defaults"] = map[string]any{"rate_limit": []any{"default"}}
	if errs, _ := ValidateConfig(c); len(errs) != 0 {
		t.Fatalf("got %v", errs)
	}
	delete(c, "defaults")
	delete(c, "rate_limits")
	if errs, _ := ValidateConfig(c); len(errs) != 0 {
		t.Fatalf("without rate_limits/defaults: got %v", errs)
	}
	for _, defaults := range []any{
		map[string]any{"rate_limit": []any{}},
		map[string]any{"rate_limit": []any{"a", "a"}},
		map[string]any{"rate_limit": false},
	} {
		c["defaults"] = defaults
		if errs, _ := ValidateConfig(c); len(errs) == 0 {
			t.Errorf("defaults %v: accepted", defaults)
		}
	}
}

// What the config held before identity and audit were ordinary services is
// unknown now: permission_name, and resources in identity's file.
func TestNoResourcesOrPermissionName(t *testing.T) {
	c := sample()
	c["kinds"].(map[string]any)["organization"].(map[string]any)["permission_name"] = "org"
	c["services"] = map[string]any{"identity": map[string]any{"url": "http://identity:3000", "resources": map[string]any{}}}
	errs, _ := ValidateConfig(c)
	paths := map[string]bool{}
	for _, e := range errs {
		paths[e.Path] = true
	}
	if !paths["kinds.organization"] || !paths["services.identity"] {
		t.Fatalf("got %v", errs)
	}
}

func TestDurationsAndUnknownKeys(t *testing.T) {
	c := sample()
	c["services"] = map[string]any{}
	for _, d := range []string{"1s", "90m", "12h", "30d"} {
		c["deletion_grace"] = d
		if errs, _ := ValidateConfig(c); len(errs) != 0 {
			t.Fatalf("%s: %v", d, errs)
		}
	}
	c["deletion_grace"] = "1w"
	c["extra"] = true
	errs, _ := ValidateConfig(c)
	paths := map[string]bool{}
	for _, e := range errs {
		paths[e.Path] = true
	}
	if !paths["deletion_grace"] || !paths[""] {
		t.Fatalf("got %v", errs)
	}
}

func TestFriendlyPatternErrorsAndServiceURLs(t *testing.T) {
	mk := func(url string) map[string]any {
		c := sample()
		c["services"] = map[string]any{
			"api": map[string]any{
				"url": url,
				"scopes": map[string]any{
					"public": map[string]any{
						"routes": []any{map[string]any{"path": "/health", "methods": []any{"GET"}}},
					},
				},
			},
		}
		return c
	}
	for _, okURL := range []string{"http://api", "http://api:3000", "https://api.example.com", "https://api.example.com:8443"} {
		if errs, err := ValidateConfig(mk(okURL)); err != nil || len(errs) != 0 {
			t.Fatalf("%s: expected valid, got %v / %v", okURL, errs, err)
		}
	}
	errs, err := ValidateConfig(mk("ftp://api:3000"))
	if err != nil || len(errs) != 1 {
		t.Fatalf("expected 1 error, got %v / %v", errs, err)
	}
	if strings.Contains(errs[0].Message, "does not match pattern") || !strings.Contains(errs[0].Message, "http://host[:port]") {
		t.Fatalf("expected friendly url message, got: %s", errs[0].Message)
	}
}

func TestKindHostAndRateLimitTiers(t *testing.T) {
	c := sample()
	c["services"] = map[string]any{}
	c["kinds"].(map[string]any)["organization"].(map[string]any)["host"] = "{organization}.api.example.com"
	c["rate_limits"] = map[string]any{
		"standard": map[string]any{
			"requests": 60,
			"window":   "60s",
			"tiers": map[string]any{
				"enterprise": map[string]any{"requests": 600, "window": "60s"},
			},
		},
	}
	if errs, err := ValidateConfig(c); err != nil || len(errs) != 0 {
		t.Fatalf("expected valid host and tiers, got %v / %v", errs, err)
	}
	c["kinds"].(map[string]any)["organization"].(map[string]any)["host"] = "not a host"
	if errs, _ := ValidateConfig(c); len(errs) == 0 {
		t.Fatalf("expected invalid host to fail schema validation")
	}
}

func TestTier2CustomRolesPermissionsAndPolicyClaims(t *testing.T) {
	c := sample()
	c["policy_claims"] = []any{"acr", "org.sso"}
	c["custom_roles"] = true
	c["permissions"] = map[string]any{
		"billing:read": map[string]any{
			"description":            "Read billing",
			"bindable_at":            []any{"organization"},
			"custom_role_assignable": true,
		},
	}
	c["services"] = map[string]any{
		"api": map[string]any{
			"url": "http://api:3000",
			"scopes": map[string]any{
				"organization": map[string]any{
					"routes": []any{
						map[string]any{
							"path":        "/status",
							"methods":     []any{"GET"},
							"permissions": "organization:read",
						},
					},
				},
			},
		},
	}
	if errs, err := ValidateConfig(c); err != nil || len(errs) != 0 {
		t.Fatalf("expected valid Tier 2 config, got %v / %v", errs, err)
	}
}

func TestAuthorizerSchema(t *testing.T) {
	c := map[string]any{
		"kinds": map[string]any{
			"organization": map[string]any{"path": "/orgs/{organization}", "list_permission": "organization:read"},
		},
		"authorizer": map[string]any{
			"url":                "http://topaz:8383",
			"timeout":            "2s",
			"cache_ttl":          "5s",
			"negative_cache_ttl": "0s",
			"token_env":          "TOPAZ_API_KEY",
		},
		"services": map[string]any{
			"docs": map[string]any{
				"url": "http://docs:3000",
				"scopes": map[string]any{
					"organization": map[string]any{
						"routes": []any{
							map[string]any{
								"path":        "/docs/{doc_id}",
								"methods":     []any{"GET"},
								"permissions": "docs:read",
								"resource": map[string]any{
									"type": "document",
									"id":   "{organization.id}:{doc_id}",
								},
							},
						},
					},
				},
			},
		},
	}
	if errs, err := ValidateConfig(c); err != nil || len(errs) != 0 {
		t.Fatalf("expected valid authorizer config, got %v / %v", errs, err)
	}
}

func TestStaticRulesOffline(t *testing.T) {
	// audit: true on public route (method-level and route-level) must fail offline.
	c := sample()
	c["services"] = map[string]any{
		"pub": map[string]any{
			"url": "http://pub:3000",
			"scopes": map[string]any{
				"public": map[string]any{
					"routes": []any{
						map[string]any{
							"path":    "/public/audited",
							"methods": map[string]any{"GET": map[string]any{"audit": true}},
						},
						map[string]any{
							"path":    "/public/audited-route",
							"audit":   true,
							"methods": []any{"GET"},
						},
					},
				},
			},
		},
	}
	errs, err := ValidateConfig(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(errs) != 2 ||
		errs[0].Path != "services.pub.scopes.public.routes[0].methods.GET.audit" ||
		errs[1].Path != "services.pub.scopes.public.routes[1].audit" {
		t.Fatalf("expected 2 public-audit errors, got %v", errs)
	}

	// permissions/allow_deleted on user scope and missing permissions on kind scope fail offline.
	c2 := sample()
	c2["services"] = map[string]any{
		"api": map[string]any{
			"url": "http://api:3000",
			"scopes": map[string]any{
				"user": map[string]any{
					"routes": []any{
						map[string]any{"path": "/user/x", "methods": []any{"GET"}, "permissions": "organization:read", "allow_deleted": true},
					},
				},
				"organization": map[string]any{
					"routes": []any{
						map[string]any{"path": "/items", "methods": []any{"GET"}},
					},
				},
			},
		},
	}
	errs, err = ValidateConfig(c2)
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]bool{}
	for _, e := range errs {
		paths[e.Path] = true
	}
	for _, want := range []string{
		"services.api.scopes.user.routes[0].permissions",
		"services.api.scopes.user.routes[0].allow_deleted",
		"services.api.scopes.organization.routes[0].permissions",
	} {
		if !paths[want] {
			t.Errorf("missing expected error at %s; got %v", want, errs)
		}
	}
}

// The copy tracks plat5's schema/. Checked when plat5 is checked out beside
// the CLI, as in plat5dev.
func TestMatchesPlat5(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "plat5", "schema")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no plat5 checkout beside the CLI")
	}
	ours, err := Files()
	if err != nil {
		t.Fatal(err)
	}
	theirs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(theirs) != len(ours) {
		t.Fatalf("plat5 has %d schema files, the CLI %d: copy plat5/schema/*.json into internal/schema/files/", len(theirs), len(ours))
	}
	for _, path := range theirs {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, ours[filepath.Base(path)]) {
			t.Errorf("%s differs from plat5's: copy plat5/schema/*.json into internal/schema/files/", filepath.Base(path))
		}
	}
}
