package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseManifestAndValidation(t *testing.T) {
	raw := `schema: 1
name: stripe-billing
type: service
service_name: billing
description: Stripe billing slice
cli: ">=0.18.0"
requires:
  kinds: [organization]
prompts:
  - key: stripe_key
    type: secret
    env: STRIPE_SECRET_KEY
    title: Stripe Secret Key
fragments:
  services:
    - src: services/service.yml
      dest: "services/[[.ServiceName]].yml"
  compose: docker-compose.yml
  env:
    - name: STRIPE_SECRET_KEY
      value: "[[.Prompt.stripe_key]]"
      secret: true
`
	m, err := ParseManifest([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if m.DefaultServiceName() != "billing" || m.DefaultTarget() != "[[.ServiceName]]" {
		t.Fatalf("defaults: %q %q", m.DefaultServiceName(), m.DefaultTarget())
	}
	if len(m.Fragments.Services) != 1 || m.Fragments.Services[0].Src != "services/service.yml" || m.Fragments.Services[0].Dest != "services/[[.ServiceName]].yml" {
		t.Fatalf("services: %+v", m.Fragments.Services)
	}
	if !AllowsSlot(TypeIdP, SlotOperatorAuth) || !AllowsSlot(TypeAuthorizer, SlotOperatorAuthZ) || AllowsSlot(TypeService, SlotAuth) {
		t.Fatal("slot rules mismatch")
	}

	// Secret prompt without env is rejected.
	bad := `schema: 1
name: bad
type: idp
description: bad
prompts:
  - key: tok
    type: secret
    title: Token
`
	if _, err := ParseManifest([]byte(bad)); err == nil || !strings.Contains(err.Error(), "env var") {
		t.Fatalf("want secret env error, got %v", err)
	}

	// Unknown field is rejected.
	if _, err := ParseManifest([]byte("schema: 1\nname: x\ntype: idp\ndescription: x\nbogus: 1\n")); err == nil {
		t.Fatal("unknown field must fail")
	}
}

func TestLoadSiblingRegistryAndTemplates(t *testing.T) {
	regDir := filepath.Join("..", "..", "..", "registry")
	if _, err := os.Stat(filepath.Join(regDir, "registry.yml")); err != nil {
		t.Skip("no sibling registry checkout")
	}
	err := filepath.WalkDir(regDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != ManifestName {
			return err
		}
		if _, err := LoadDir(filepath.Dir(path)); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
