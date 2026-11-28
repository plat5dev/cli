package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/plat5dev/cli/internal/blueprint"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plugin"
	"github.com/plat5dev/cli/internal/prompt"
	"github.com/plat5dev/cli/internal/registry"
	"github.com/spf13/cobra"
)

type addOptions struct {
	slot       string
	asName     string
	setPairs   []string
	sourcesDir string
	regSrc     string
	dryRun     bool
	force      bool
	yes        bool
	noVerify   bool
}

func newAddCmd() *cobra.Command {
	opts := &addOptions{}
	cmd := &cobra.Command{
		Use:   "add <plugin>",
		Short: "Add a service, provider, observability, CI, or deploy plugin to an existing project",
		Long: `Add a plugin from a registry, GitHub repo (owner/repo//subdir@ref), .tar.gz URL,
or local directory to an existing Plat5 project.

  plat5 add go-fiber-api --as orders
  plat5 add cerbos --slot operator-authz
  plat5 add grafana-cloud
  plat5 add github-actions --dry-run`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(args[0], opts)
		},
	}
	f := cmd.Flags()
	f.StringVar(&opts.slot, "slot", "", "Target slot (e.g. operator-auth, operator-authz; default inferred from plugin type)")
	f.StringVar(&opts.asName, "as", "", "Custom service name for type: service plugins (e.g. --as orders)")
	f.StringArrayVar(&opts.setPairs, "set", nil, "Set a plugin prompt value: key=value or plugin.key=value (repeatable)")
	f.StringVar(&opts.sourcesDir, "sources", "", "Resolve first-party repos from local checkouts in this directory")
	f.StringVar(&opts.regSrc, "registry", "", "Override the default registry source")
	f.BoolVar(&opts.dryRun, "dry-run", false, "Preview planned file changes without writing to disk")
	f.BoolVar(&opts.force, "force", false, "Allow overwriting existing files")
	f.BoolVarP(&opts.yes, "yes", "y", false, "Don't prompt; use defaults and --set values")
	f.BoolVar(&opts.noVerify, "no-verify", false, "Skip live HTTP preflight checks")
	return cmd
}

func runAdd(ref string, opts *addOptions) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	projDir := findProjectDir(wd)
	existing, err := blueprint.LoadExisting(projDir)
	if err != nil {
		return err
	}

	res := &registry.Resolver{
		SourcesDir:       absIfSet(opts.sourcesDir),
		RegistryOverride: opts.regSrc,
	}
	p, err := res.Resolve(ref)
	if err != nil {
		return err
	}
	defer p.Close()

	if opts.slot != "" {
		if !plugin.AllowsSlot(p.Manifest.Type, opts.slot) {
			return fmt.Errorf("plugin %s (type %s) cannot attach to slot %q", p.Manifest.Name, p.Manifest.Type, opts.slot)
		}
		p.Slot = opts.slot
	}
	if opts.asName != "" {
		if p.Manifest.Type != plugin.TypeService {
			return fmt.Errorf("--as applies only to type: service plugins (%s is %s)", p.Manifest.Name, p.Manifest.Type)
		}
		p.ServiceName = strings.TrimSpace(opts.asName)
	}

	// Apply --set values and interactively collect any missing prompts.
	if err := applyPromptFlags([]*plugin.Plugin{p}, opts.setPairs); err != nil {
		return err
	}
	if !opts.yes && prompt.Interactive() {
		if err := collectPromptsAndVerify([]*plugin.Plugin{p}, opts.noVerify); err != nil {
			return err
		}
	}

	projectID := config.SanitizeProjectID(filepath.Base(projDir))
	if f, err := config.Parse(existing.LocalYML); err == nil && f.ProjectID != "" {
		projectID = f.ProjectID
	}
	spec := blueprint.Spec{ProjectID: projectID}
	plan, err := blueprint.Build(spec, []*plugin.Plugin{p}, existing)
	if err != nil {
		return err
	}

	if opts.dryRun {
		fmt.Printf("Plan for %s (%s, slot %s):\n", p.Manifest.Name, p.Manifest.Type, p.Slot)
		fmt.Print(plan.FormatDryRun())
		return nil
	}

	written, err := plan.Apply(projDir, opts.force)
	if err != nil {
		return err
	}
	fmt.Printf("Added %s (%s) to %s:\n", p.Manifest.Name, p.Manifest.Type, relPath(wd, projDir))
	for _, rel := range written {
		fmt.Println("  wrote", rel)
	}
	for _, w := range plan.Warnings {
		fmt.Println("  warning:", w)
	}
	if len(plan.Next) > 0 {
		fmt.Println("\nNext:")
		for _, n := range plan.Next {
			fmt.Println("  " + n)
		}
	}
	return nil
}

func findProjectDir(start string) string {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, config.ConfigFileName)); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return start
		}
		dir = parent
	}
}

func applyPromptFlags(plugins []*plugin.Plugin, pairs []string) error {
	byName := map[string]*plugin.Plugin{}
	for _, p := range plugins {
		byName[p.Manifest.Name] = p
	}
	for _, raw := range pairs {
		k, v, ok := strings.Cut(raw, "=")
		if !ok {
			return fmt.Errorf("--set %q: want key=value or plugin.key=value", raw)
		}
		if pName, pKey, hasDot := strings.Cut(k, "."); hasDot {
			p, ok := byName[pName]
			if !ok {
				return fmt.Errorf("--set %q: plugin %q is not selected", raw, pName)
			}
			p.Prompts[pKey] = v
			continue
		}
		if len(plugins) == 1 {
			plugins[0].Prompts[k] = v
			continue
		}
		return fmt.Errorf("--set %q: qualify with <plugin>.%s=<value> when multiple plugins are selected", raw, k)
	}
	return nil
}

func collectMissingPrompts(plugins []*plugin.Plugin) error {
	for _, p := range plugins {
		var fields []huh.Field
		for _, pr := range p.Manifest.Prompts {
			if _, already := p.Prompts[pr.Key]; already {
				continue
			}
			key := pr.Key
			val := pr.Default
			if strings.Contains(val, "[[") {
				val = ""
			}
			valPtr := new(string)
			*valPtr = val
			p.Prompts[key] = val

			switch pr.Type {
			case "select":
				opts := make([]huh.Option[string], 0, len(pr.Options))
				for _, o := range pr.Options {
					opts = append(opts, huh.NewOption(o.Label, o.Value))
				}
				fields = append(fields, huh.NewSelect[string]().
					Title(pr.Title).
					Description(pr.Help).
					Options(opts...).
					Value(valPtr))
			case "secret":
				fields = append(fields, huh.NewInput().
					Title(pr.Title).
					Description(pr.Help).
					EchoMode(huh.EchoModePassword).
					Value(valPtr))
			default:
				fields = append(fields, huh.NewInput().
					Title(pr.Title).
					Description(pr.Help).
					Placeholder(pr.Placeholder).
					Value(valPtr))
			}
			// Capture pointer for post-form assignment.
			prCopy := pr
			fields[len(fields)-1] = wrapFieldSave(fields[len(fields)-1], p, prCopy.Key, valPtr)
		}
		if len(fields) > 0 {
			if err := runForm(fields); err != nil {
				return err
			}
		}
	}
	return nil
}

func wrapFieldSave(f huh.Field, p *plugin.Plugin, key string, valPtr *string) huh.Field {
	// huh updates *valPtr during Run(); validate hook copies the final value into p.Prompts.
	switch field := f.(type) {
	case *huh.Input:
		return field.Validate(func(s string) error {
			p.Prompts[key] = s
			return nil
		})
	case *huh.Select[string]:
		return field.Validate(func(s string) error {
			p.Prompts[key] = s
			return nil
		})
	default:
		return f
	}
}
