package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTemplateResolveOptsLocalDir(t *testing.T) {
	prevDir := initTemplatesDir
	prevVer := initPlat5Version
	initTemplatesDir = ""
	initPlat5Version = ""
	t.Cleanup(func() {
		initTemplatesDir = prevDir
		initPlat5Version = prevVer
	})
	t.Setenv("PLAT5_TEMPLATES", "")
	t.Setenv("PLAT5_VERSION", "")

	root := t.TempDir()
	templates := filepath.Join(root, "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}

	initTemplatesDir = templates
	opts, err := templateResolveOpts()
	if err != nil {
		t.Fatal(err)
	}
	if opts.LocalRoot != templates {
		t.Fatalf("got %s want %s", opts.LocalRoot, templates)
	}
}

func TestTemplateResolveOptsRemoteDefault(t *testing.T) {
	prevDir := initTemplatesDir
	prevVer := initPlat5Version
	initTemplatesDir = ""
	initPlat5Version = ""
	t.Cleanup(func() {
		initTemplatesDir = prevDir
		initPlat5Version = prevVer
	})
	t.Setenv("PLAT5_TEMPLATES", "")
	t.Setenv("PLAT5_VERSION", "")

	opts, err := templateResolveOpts()
	if err != nil {
		t.Fatal(err)
	}
	if opts.LocalRoot != "" {
		t.Fatalf("expected remote mode, local=%s", opts.LocalRoot)
	}
}
