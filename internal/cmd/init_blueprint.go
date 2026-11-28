package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/plat5dev/cli/internal/blueprint"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plugin"
	"github.com/plat5dev/cli/internal/prompt"
	"github.com/plat5dev/cli/internal/registry"
	"github.com/plat5dev/cli/internal/wizard"
	"github.com/spf13/cobra"
)

// Composable init flags (plugin/registry blueprint path).
var (
	initRecipe          string
	initServices        []string
	initAuth            string
	initAuthZ           string
	initOperatorAuth    string
	initOperatorAuthZ   string
	initCustomerConsole string
	initOperatorConsole string
	initObservability   string
	initCI              []string
	initDeploy          []string
	initSet             []string
	initRegistrySrc     string
	initDryRun          bool
	initNoVerify        bool
)

func registerComposableInitFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&initRecipe, "recipe", "", "Named starter recipe from a registry (e.g. quickstart, b2b-saas, enterprise-sso)")
	f.StringArrayVar(&initServices, "service", nil, "Add a type: service plugin (repeatable; optional :name alias, e.g. go-fiber-api:orders)")
	f.StringVar(&initAuth, "auth", "", "Customer auth IdP plugin (plat5-auth, auth0, okta, keycloak, custom-oidc)")
	f.StringVar(&initAuthZ, "authz", "", "Customer authorizer plugin (plat5-rbac, cerbos, topaz, custom-authzen)")
	f.StringVar(&initOperatorAuth, "operator-auth", "", "Operator IdP plugin (plat5-auth, okta, auth0, custom-oidc)")
	f.StringVar(&initOperatorAuthZ, "operator-authz", "", "Operator authorizer plugin (cerbos, topaz, custom-authzen)")
	f.StringVar(&initCustomerConsole, "customer-console", "", "Customer console plugin (solid-customer-console, owner/repo, or none)")
	f.StringVar(&initOperatorConsole, "operator-console", "", "Operator console plugin (solid-operator-console, owner/repo, or none)")
	f.StringVar(&initObservability, "observability", "", "Observability plugin (local-lgtm, grafana-cloud, custom-otlp, or none)")
	f.StringArrayVar(&initCI, "ci", nil, "CI workflow plugin (repeatable, e.g. github-actions)")
	f.StringArrayVar(&initDeploy, "deploy", nil, "Deploy / overlay plugin (repeatable, e.g. env-overlays)")
	f.StringArrayVar(&initSet, "set", nil, "Set a plugin prompt value: plugin.key=value (repeatable)")
	f.StringVar(&initRegistrySrc, "registry", "", "Override the default registry source (owner/repo or local dir)")
	f.BoolVar(&initDryRun, "dry-run", false, "Preview planned files without writing to disk")
	f.BoolVar(&initNoVerify, "no-verify", false, "Skip live HTTP preflight checks (JWKS, OTLP) during setup")
}

func usesComposableInit(cmd *cobra.Command) bool {
	for _, name := range []string{
		"recipe", "service", "auth", "authz", "operator-auth", "operator-authz",
		"customer-console", "operator-console", "observability", "ci", "deploy",
		"set", "registry", "dry-run", "no-verify",
	} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func newInitResolver() *registry.Resolver {
	return &registry.Resolver{
		SourcesDir:       absIfSet(initSourcesDir),
		RegistryOverride: initRegistrySrc,
		Ref:              firstNonEmpty(initTemplateRef, strings.TrimSpace(os.Getenv("PLAT5_TEMPLATE_REF"))),
	}
}

func runInteractiveWizardInit(cmd *cobra.Command, args []string) error {
	res := newInitResolver()
	cat, err := wizard.LoadCatalog(res)
	if err != nil {
		return fmt.Errorf("load plugin catalog: %w", err)
	}
	initialDir := ""
	if len(args) == 1 {
		initialDir = args[0]
	}
	wres, err := wizard.Run(cat, wizard.Options{
		Dir:       initialDir,
		ProjectID: initProjectID,
		NoVerify:  initNoVerify,
	})
	if err != nil {
		return err
	}
	return executeBlueprintInit(res, wres.Dir, wres.Recipe, wres.Slots, wres.NoOperator)
}

func runComposableInit(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	res := newInitResolver()

	slots := map[string][]string{}
	if cmd.Flags().Changed("preset") {
		slots[plugin.SlotPreset] = []string{initPreset}
	}
	if initAuth != "" {
		slots[plugin.SlotAuth] = []string{initAuth}
	}
	if initAuthZ != "" {
		slots[plugin.SlotAuthZ] = []string{initAuthZ}
	}
	if initOperatorAuth != "" {
		slots[plugin.SlotOperatorAuth] = []string{initOperatorAuth}
	}
	if initOperatorAuthZ != "" {
		slots[plugin.SlotOperatorAuthZ] = []string{initOperatorAuthZ}
	}
	if initCustomerConsole != "" {
		slots[plugin.SlotCustomerConsole] = []string{initCustomerConsole}
	}
	if initOperatorConsole != "" {
		slots[plugin.SlotOperatorConsole] = []string{initOperatorConsole}
	}
	if initObservability != "" {
		slots[plugin.SlotObservability] = []string{initObservability}
	}
	if len(initServices) > 0 {
		slots[plugin.SlotService] = initServices
	}
	if len(initCI) > 0 {
		slots[plugin.SlotCI] = append(slots[plugin.SlotCI], initCI...)
	}
	if len(initDeploy) > 0 {
		slots[plugin.SlotDeploy] = append(slots[plugin.SlotDeploy], initDeploy...)
	}

	return executeBlueprintInit(res, dir, initRecipe, slots, initNoOperator)
}

func executeBlueprintInit(res *registry.Resolver, dir, recipeRef string, explicitSlots map[string][]string, noOperator bool) error {
	absDir, err := filepath.Abs(prompt.ExpandPath(dir))
	if err != nil {
		return err
	}
	devPath := filepath.Join(absDir, config.FileName)
	if !initDryRun {
		if _, err := os.Stat(devPath); err == nil && !initForce {
			return fmt.Errorf("%s already exists (--force to overwrite it)", devPath)
		}
	}
	projectID, err := initProjectIDFor(absDir)
	if err != nil {
		return err
	}
	if !initDryRun {
		if _, err := guardOwner(projectID, absDir, true); err != nil {
			return err
		}
	}

	// Expand recipe first, then apply explicit slot selections on top.
	slots := map[string][]string{}
	if recipeRef != "" {
		rp, err := res.Resolve(recipeRef)
		if err != nil {
			return err
		}
		defer rp.Close()
		if rp.Manifest.Type != plugin.TypeRecipe {
			return fmt.Errorf("--recipe %q is type %s, want recipe", recipeRef, rp.Manifest.Type)
		}
		for slot, refs := range rp.Manifest.Recipe {
			slots[slot] = append([]string(nil), refs...)
		}
	}
	for slot, refs := range explicitSlots {
		slots[slot] = append([]string(nil), refs...)
	}

	// Apply sensible defaults if unset.
	if len(slots[plugin.SlotPreset]) == 0 {
		if len(slots[plugin.SlotService]) > 0 {
			slots[plugin.SlotPreset] = []string{presetSaaS}
		} else {
			slots[plugin.SlotPreset] = []string{presetMinimal}
		}
	}
	if initNoCustomerConsole {
		delete(slots, plugin.SlotCustomerConsole)
	}
	if initNoOperatorConsole || noOperator {
		delete(slots, plugin.SlotOperatorConsole)
	}
	if initNoObservability {
		slots[plugin.SlotObservability] = []string{"none"}
	}

	// Resolve all selected plugins.
	var plugins []*plugin.Plugin
	defer func() {
		for _, p := range plugins {
			p.Close()
		}
	}()
	selectedRefs := map[string][]string{}
	for slot, refs := range slots {
		for _, rawRef := range refs {
			rawRef = strings.TrimSpace(rawRef)
			if rawRef == "" || rawRef == "none" {
				continue
			}
			ref, asName := rawRef, ""
			if slot == plugin.SlotService && !strings.Contains(rawRef, "://") {
				if before, after, ok := strings.Cut(rawRef, ":"); ok && after != "" {
					ref, asName = before, after
				}
			}
			p, err := res.Resolve(ref)
			if err != nil {
				return fmt.Errorf("--%s %s: %w", slot, rawRef, err)
			}
			if !plugin.AllowsSlot(p.Manifest.Type, slot) {
				p.Close()
				return fmt.Errorf("--%s %s: plugin type %s cannot fill slot %s", slot, rawRef, p.Manifest.Type, slot)
			}
			p.Slot = slot
			if asName != "" {
				p.ServiceName = asName
			}
			plugins = append(plugins, p)
			selectedRefs[slot] = append(selectedRefs[slot], rawRef)
		}
	}

	if err := applyPromptFlags(plugins, initSet); err != nil {
		return err
	}
	if !initYes && prompt.Interactive() {
		if err := collectPromptsAndVerify(plugins, initNoVerify); err != nil {
			return err
		}
	}

	// Resolve image versions / --sources checkouts.
	ch := initChoices{
		modules: map[string]bool{
			config.ModuleAuth:          !initNoAuth,
			config.ModuleObservability: !initNoObservability,
			config.ModuleOperator:      !noOperator,
		},
		sources:  map[string]string{},
		versions: map[string]string{},
	}
	if err := parsePairs("--version", initVersions, config.Sources, ch.versions); err != nil {
		return err
	}
	if initSourcesDir != "" {
		abs := absIfSet(initSourcesDir)
		ch.sourcesDir = abs
		ch.sources = sourcesFrom(abs)
	}
	versions, err := resolveVersions(ch)
	if err != nil {
		return err
	}

	spec := blueprint.Spec{
		ProjectID:    projectID,
		NoOperator:   noOperator,
		Versions:     versions,
		Sources:      ch.sources,
		SelectedRefs: selectedRefs,
	}
	plan, err := blueprint.Build(spec, plugins, nil)
	if err != nil {
		return err
	}

	if initDryRun {
		fmt.Printf("Plan for %s:\n", projectID)
		fmt.Print(plan.FormatDryRun())
		fmt.Printf("\nReproduce:\n  %s\n", plan.ReproduceCmd)
		return nil
	}

	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return err
	}
	written, err := plan.Apply(absDir, initForce)
	if err != nil {
		return err
	}
	fmt.Println("Created", relPath(mustGetwd(), absDir)+":")
	for _, rel := range written {
		fmt.Println("  wrote", rel)
	}
	fmt.Printf("\nReproduce this setup:\n  %s\n", plan.ReproduceCmd)
	fmt.Println("\nNext:")
	if wd, _ := os.Getwd(); filepath.Clean(wd) != filepath.Clean(absDir) {
		fmt.Printf("  cd %s\n", relPath(wd, absDir))
	}
	fmt.Println("  plat5 start")
	for _, n := range plan.Next {
		fmt.Println("  " + n)
	}
	return nil
}

// collectPromptsAndVerify interactively collects missing prompts per plugin and
// runs any declarative HTTP verify probe with retry support.
func collectPromptsAndVerify(plugins []*plugin.Plugin, noVerify bool) error {
	for _, p := range plugins {
		if len(p.Manifest.Prompts) == 0 && p.Manifest.Verify == nil {
			continue
		}
		for {
			if err := collectMissingPrompts([]*plugin.Plugin{p}); err != nil {
				return err
			}
			if noVerify || p.Manifest.Verify == nil {
				break
			}
			_, action, err := wizard.RunVerifySpinner(p, nil)
			if err != nil {
				return err
			}
			switch action {
			case wizard.VerifyCancel:
				return errors.New("cancelled")
			case wizard.VerifyRetryEdit:
				// Clear answered prompts for this plugin so the user can re-enter them.
				p.Prompts = map[string]string{}
				continue
			default:
				// VerifyContinue
			}
			break
		}
	}
	return nil
}
