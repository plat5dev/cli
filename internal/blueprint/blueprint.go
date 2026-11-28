// Package blueprint is the pure, side-effect-free project composition engine
// shared by `plat5 init` and `plat5 add`. It renders plugin templates, merges
// YAML fragments, regenerates identity/audit routes, validates plat5.yml and
// plat5.local.yml in memory, and returns a Plan of file operations to apply.
package blueprint

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plat5yml"
	"github.com/plat5dev/cli/internal/plugin"
	"github.com/plat5dev/cli/internal/routegen"
	"github.com/plat5dev/cli/internal/schema"
	"gopkg.in/yaml.v3"
)

// DefaultOperatorYML is the baseline operator.yml written when the operator module is enabled.
const DefaultOperatorYML = `# Operator gateway config (https://github.com/plat5dev/operator/blob/master/docs/reference/config.md).
control_plane:
  url: http://control-plane:5003

services:
  identity:
    url: http://identity:3000
  audit:
    url: http://audit:3002
  operator-audit:
    url: http://operator-audit:5005

authorizer:
  mode: disabled
`

// Spec describes a project to build or extend.
type Spec struct {
	ProjectID   string
	SecretBrand string
	NoOperator  bool
	Versions    map[string]string
	Sources     map[string]string
	// SelectedRefs records original CLI references by slot for reproducing the command.
	SelectedRefs map[string][]string
}

// ExistingProject holds the current files of an existing project for Day-2 `plat5 add`.
type ExistingProject struct {
	Dir                string
	Plat5YML           []byte
	LocalYML           []byte
	OperatorYML        []byte
	AppComposeYML      []byte
	InfraComposeYML    []byte
	EnvExample         []byte
	EnvLocal           []byte
	Gitignore          []byte
	ServiceFiles       map[string][]byte // e.g. "services/api.yml" -> bytes
	PermissionSep      string
	HasOperator        bool
	HasCustomerConsole bool
}

// LoadExisting reads an existing project directory into an ExistingProject snapshot.
func LoadExisting(dir string) (*ExistingProject, error) {
	readOpt := func(rel string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			return nil
		}
		return b
	}
	ep := &ExistingProject{
		Dir:             dir,
		Plat5YML:        readOpt("plat5.yml"),
		LocalYML:        readOpt(config.FileName),
		OperatorYML:     readOpt("operator.yml"),
		AppComposeYML:   readOpt("docker-compose.yml"),
		InfraComposeYML: readOpt("docker-compose.infra.yml"),
		EnvExample:      readOpt(".env.example"),
		EnvLocal:        readOpt(config.DefaultEnvFile),
		Gitignore:       readOpt(".gitignore"),
		ServiceFiles:    map[string][]byte{},
		PermissionSep:   config.DefaultPermissionSeparator,
	}
	if len(ep.Plat5YML) == 0 {
		return nil, fmt.Errorf("%s: no plat5.yml found", dir)
	}
	var top map[string]any
	if err := yaml.Unmarshal(ep.Plat5YML, &top); err != nil {
		return nil, fmt.Errorf("plat5.yml: %w", err)
	}
	if listed, err := plat5yml.ServiceFiles(top, "plat5.yml"); err == nil {
		for _, rel := range listed {
			if data := readOpt(rel); data != nil {
				ep.ServiceFiles[filepath.ToSlash(filepath.Clean(rel))] = data
			}
		}
	}
	if len(ep.LocalYML) > 0 {
		if f, err := config.Parse(ep.LocalYML); err == nil {
			if f.Routes.PermissionSeparator != "" {
				ep.PermissionSep = f.Routes.PermissionSeparator
			}
			if op, ok := f.Modules[config.ModuleOperator]; !ok || op {
				ep.HasOperator = true
			}
			if f.App.Web != "" {
				ep.HasCustomerConsole = true
			}
		}
	}
	return ep, nil
}

// OpKind describes what a FileOp does.
type OpKind string

const (
	OpCreate OpKind = "create"
	OpUpdate OpKind = "update"
)

// FileOp is one file write planned by Build.
type FileOp struct {
	Kind    OpKind
	Path    string // project-relative slash path
	Data    []byte
	Mode    os.FileMode
	Summary string
}

// Plan is the validated output of Build.
type Plan struct {
	Ops          []FileOp
	Next         []string
	Warnings     []string
	ReproduceCmd string
}

// FormatDryRun renders a human-readable summary of the plan.
func (p *Plan) FormatDryRun() string {
	var b strings.Builder
	for _, op := range p.Ops {
		sym := "+"
		if op.Kind == OpUpdate {
			sym = "~"
		}
		if op.Summary != "" {
			fmt.Fprintf(&b, "  %s %-36s (%s)\n", sym, op.Path, op.Summary)
		} else {
			fmt.Fprintf(&b, "  %s %s\n", sym, op.Path)
		}
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "  ! %s\n", w)
	}
	return b.String()
}

// Apply writes every FileOp in the plan under dir.
func (p *Plan) Apply(dir string, force bool) ([]string, error) {
	if !force {
		for _, op := range p.Ops {
			if op.Kind != OpCreate {
				continue
			}
			target := filepath.Join(dir, filepath.FromSlash(op.Path))
			if _, err := os.Stat(target); err == nil {
				return nil, fmt.Errorf("%s already exists (--force to overwrite)", op.Path)
			}
		}
	}
	var written []string
	for _, op := range p.Ops {
		target := filepath.Join(dir, filepath.FromSlash(op.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return written, err
		}
		mode := op.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := os.WriteFile(target, op.Data, mode); err != nil {
			return written, err
		}
		written = append(written, op.Path)
	}
	return written, nil
}

// Build executes the deterministic composition pipeline in memory and validates
// the resulting plat5.yml and plat5.local.yml before returning a Plan.
func Build(spec Spec, plugins []*plugin.Plugin, existing *ExistingProject) (*Plan, error) {
	isAdd := existing != nil
	if spec.ProjectID == "" {
		spec.ProjectID = "my-app"
	}
	if spec.SecretBrand == "" {
		spec.SecretBrand = config.DefaultSecretBrand
	}

	// Determine active operator, consoles, and permission separator.
	hasOperator := !spec.NoOperator
	if isAdd {
		hasOperator = existing.HasOperator
	}
	hasCustomerConsole := isAdd && existing.HasCustomerConsole
	for _, p := range plugins {
		if p.Slot == plugin.SlotCustomerConsole {
			hasCustomerConsole = true
		}
	}
	permSep := config.DefaultPermissionSeparator
	if isAdd && existing.PermissionSep != "" {
		permSep = existing.PermissionSep
	}
	for _, p := range plugins {
		if p.Slot == plugin.SlotAuthZ && p.Manifest.Capabilities.PermissionSeparator != "" {
			permSep = p.Manifest.Capabilities.PermissionSeparator
		}
		if p.Slot == plugin.SlotService && p.Manifest.Capabilities.PermissionSeparator != "" {
			permSep = p.Manifest.Capabilities.PermissionSeparator
		}
	}

	// Check conflicts & operator requirement early.
	if err := checkPluginCompatibility(plugins, hasOperator); err != nil {
		return nil, err
	}

	// Sort plugins into deterministic stage order.
	ordered := orderPlugins(plugins)

	// Build Selected map for template context.
	selected := map[string]string{}
	var serviceNames []string
	for _, p := range ordered {
		if p.Slot == plugin.SlotService {
			serviceNames = append(serviceNames, p.ServiceName)
		} else if p.Slot != plugin.SlotCI && p.Slot != plugin.SlotDeploy {
			selected[p.Slot] = p.Manifest.Name
		}
	}

	// Working state for root files.
	var (
		plat5Bytes        []byte
		localBytes        []byte
		operatorBytes     []byte
		appComposeBytes   []byte
		infraComposeBytes []byte
		envExampleBytes   []byte
		envLocalBytes     []byte
		gitignoreBytes    []byte
		serviceFiles      = map[string][]byte{}
		appIncludes       []string
		infraIncludes     []string
		envEntries        []plugin.EnvEntry
		gitignoreLines    []string
		extraFiles        = map[string]FileOp{}
		nextHints         []string
		warnings          []string
		customerWeb       string
		operatorWeb       string
		operatorConsoleC  string
	)

	if isAdd {
		plat5Bytes = append([]byte(nil), existing.Plat5YML...)
		localBytes = append([]byte(nil), existing.LocalYML...)
		operatorBytes = append([]byte(nil), existing.OperatorYML...)
		appComposeBytes = append([]byte(nil), existing.AppComposeYML...)
		infraComposeBytes = append([]byte(nil), existing.InfraComposeYML...)
		envExampleBytes = append([]byte(nil), existing.EnvExample...)
		envLocalBytes = append([]byte(nil), existing.EnvLocal...)
		gitignoreBytes = append([]byte(nil), existing.Gitignore...)
		for k, v := range existing.ServiceFiles {
			serviceFiles[k] = append([]byte(nil), v...)
		}
	} else {
		localBytes = baseLocalYML(spec, hasOperator)
		if hasOperator {
			operatorBytes = []byte(DefaultOperatorYML)
		}
	}

	// Check if switching authz on an existing project (warn about manual app code updates).
	if isAdd {
		for _, p := range ordered {
			if p.Slot == plugin.SlotAuthZ && p.Manifest.Name != "plat5-rbac" {
				warnings = append(warnings, fmt.Sprintf("switching authorizer to %s on an existing project: review existing services/*.yml and app code permissions", p.Manifest.Name))
			}
		}
	}

	// Render & apply each plugin in deterministic order.
	for _, p := range ordered {
		ctx := templateContext(spec, p, permSep, selected, serviceNames, hasOperator, hasCustomerConsole, extractKindNames(plat5Bytes))

		// Resolve prompt defaults.
		resolvedPrompts, err := resolvePrompts(p, ctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Manifest.Name, err)
		}
		ctx.Prompt = resolvedPrompts

		// 1. Copy/render plugin files into target.
		rawTarget := p.Manifest.DefaultTarget()
		targetDir := ""
		if rawTarget != "" {
			targetDir, err = renderString(p.Manifest.Name+":target", rawTarget, ctx)
			if err != nil {
				return nil, err
			}
			targetDir = filepath.ToSlash(filepath.Clean(targetDir))
		}
		if targetDir != "" {
			if err := collectPluginFiles(p, targetDir, ctx, extraFiles); err != nil {
				return nil, fmt.Errorf("%s: %w", p.Manifest.Name, err)
			}
		}

		// 2. Apply fragments.plat5 (unless plugin is in operator-only slot).
		if len(p.Manifest.Fragments.Plat5) > 0 && p.Slot != plugin.SlotOperatorAuthZ && p.Slot != plugin.SlotOperatorAuth {
			rendered, err := renderAny(p.Manifest.Name+":fragments.plat5", p.Manifest.Fragments.Plat5, ctx)
			if err != nil {
				return nil, err
			}
			if patch, ok := rendered.(map[string]any); ok {
				plat5Bytes, err = MergeYAMLNode(plat5Bytes, patch)
				if err != nil {
					return nil, fmt.Errorf("%s: merge plat5.yml: %w", p.Manifest.Name, err)
				}
			}
		}

		// 3. Apply fragments.operator (when operator is enabled).
		if hasOperator && len(p.Manifest.Fragments.Operator) > 0 && p.Slot != plugin.SlotAuthZ {
			rendered, err := renderAny(p.Manifest.Name+":fragments.operator", p.Manifest.Fragments.Operator, ctx)
			if err != nil {
				return nil, err
			}
			if patch, ok := rendered.(map[string]any); ok {
				if len(operatorBytes) == 0 {
					operatorBytes = []byte(DefaultOperatorYML)
				}
				operatorBytes, err = MergeYAMLNode(operatorBytes, patch)
				if err != nil {
					return nil, fmt.Errorf("%s: merge operator.yml: %w", p.Manifest.Name, err)
				}
			}
		}
		// Special case: if an authorizer plugin fills operator-authz AND has operator fragment (or plat5 authorizer).
		if hasOperator && p.Slot == plugin.SlotOperatorAuthZ && len(p.Manifest.Fragments.Operator) == 0 {
			if rawAuthz, ok := p.Manifest.Fragments.Plat5["authorizer"]; ok {
				rendered, err := renderAny(p.Manifest.Name+":operator.authorizer", rawAuthz, ctx)
				if err != nil {
					return nil, err
				}
				if authzMap, ok := rendered.(map[string]any); ok {
					opAuthz := map[string]any{"mode": "authzen"}
					for _, k := range []string{"url", "timeout", "token_env"} {
						if v, ok := authzMap[k]; ok {
							opAuthz[k] = v
						}
					}
					operatorBytes, err = MergeYAMLNode(operatorBytes, map[string]any{"authorizer": opAuthz})
					if err != nil {
						return nil, err
					}
				}
			}
		}

		// 4. Apply fragments.local (with operator-auth slot remapping idp → operator.idp).
		if len(p.Manifest.Fragments.Local) > 0 {
			rendered, err := renderAny(p.Manifest.Name+":fragments.local", p.Manifest.Fragments.Local, ctx)
			if err != nil {
				return nil, err
			}
			if patch, ok := rendered.(map[string]any); ok {
				if p.Slot == plugin.SlotOperatorAuth {
					patch = remapLocalIdPToOperator(patch)
				}
				// Ensure secret prompt values are never written into plat5.local.yml.
				if err := assertNoSecrets(p, resolvedPrompts, patch); err != nil {
					return nil, err
				}
				localBytes, err = MergeYAMLNode(localBytes, patch)
				if err != nil {
					return nil, fmt.Errorf("%s: merge plat5.local.yml: %w", p.Manifest.Name, err)
				}
			}
		}

		// 5. Render and register service files (fragments.services).
		for _, sf := range p.Manifest.Fragments.Services {
			srcPath := filepath.Join(p.Dir, filepath.FromSlash(filepath.Clean(sf.Src)))
			raw, err := os.ReadFile(srcPath)
			if err != nil {
				return nil, fmt.Errorf("%s: read service file %s: %w", p.Manifest.Name, sf.Src, err)
			}
			destRel, err := renderString(p.Manifest.Name+":services.dest", sf.Dest, ctx)
			if err != nil {
				return nil, err
			}
			destRel = filepath.ToSlash(filepath.Clean(destRel))
			if p.IsRendered(sf.Src) {
				rendered, err := renderString(p.Manifest.Name+":"+sf.Src, string(raw), ctx)
				if err != nil {
					return nil, err
				}
				raw = []byte(rendered)
			}
			if _, exists := serviceFiles[destRel]; exists {
				return nil, fmt.Errorf("%s: service file %s already exists (use --as <name> for a unique service name)", p.Manifest.Name, destRel)
			}
			serviceFiles[destRel] = raw
		}

		// 6. Track compose includes (infra vs app).
		if p.Manifest.Fragments.InfraCompose != "" {
			inc := joinRel(targetDir, p.Manifest.Fragments.InfraCompose)
			infraIncludes = append(infraIncludes, inc)
		}
		if p.Manifest.Fragments.Compose != "" {
			inc := joinRel(targetDir, p.Manifest.Fragments.Compose)
			if p.Manifest.Type == plugin.TypeAuthorizer || p.Manifest.Type == plugin.TypeIdP || p.Manifest.Type == plugin.TypeObservability {
				infraIncludes = append(infraIncludes, inc)
			} else {
				appIncludes = append(appIncludes, inc)
			}
		}
		if p.Slot == plugin.SlotCustomerConsole && p.Manifest.App.Web != "" {
			customerWeb = strings.TrimRight(p.Manifest.App.Web, "/")
		}
		if p.Slot == plugin.SlotOperatorConsole && p.Manifest.App.Web != "" {
			operatorWeb = strings.TrimRight(p.Manifest.App.Web, "/")
			if p.Manifest.App.Compose != "" {
				operatorConsoleC = joinRel(targetDir, p.Manifest.App.Compose)
			}
		}

		// 7. Collect env entries, gitignore lines, and next hints.
		for _, e := range p.Manifest.Fragments.Env {
			val, err := renderString(p.Manifest.Name+":env."+e.Name, e.Value, ctx)
			if err != nil {
				return nil, err
			}
			envEntries = append(envEntries, plugin.EnvEntry{
				Name:    e.Name,
				Value:   val,
				Secret:  e.Secret,
				Comment: e.Comment,
			})
		}
		gitignoreLines = append(gitignoreLines, p.Manifest.Fragments.Gitignore...)
		for _, n := range p.Manifest.Next {
			rendered, err := renderString(p.Manifest.Name+":next", n, ctx)
			if err != nil {
				return nil, err
			}
			nextHints = append(nextHints, rendered)
		}
	}

	// Verify requires.kinds against assembled plat5.yml.
	assembledKinds := extractKindNames(plat5Bytes)
	for _, p := range ordered {
		for _, reqKind := range p.Manifest.Requires.Kinds {
			if !contains(assembledKinds, reqKind) {
				return nil, fmt.Errorf("%s requires kind %q in plat5.yml (found: %s)", p.Manifest.Name, reqKind, strings.Join(assembledKinds, ", "))
			}
		}
	}

	// Stage 4d: Run routegen.Generate for identity & audit using assembled kinds + claimed routes.
	kinds, err := routegen.ReadKinds(plat5Bytes)
	if err != nil {
		return nil, fmt.Errorf("plat5.yml kinds: %w", err)
	}
	parsedServices := map[string]any{}
	for rel, raw := range serviceFiles {
		var m map[string]any
		if err := yaml.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		for svcName, body := range m {
			if svcName != routegen.Identity && svcName != routegen.Audit {
				if _, dup := parsedServices[svcName]; dup {
					return nil, fmt.Errorf("duplicate service %q (in %s)", svcName, rel)
				}
				parsedServices[svcName] = body
			}
		}
	}
	claimed := routegen.ClaimedRoutesFromServices(kinds, parsedServices)
	idFile, auditFile := routegen.Generate(kinds, routegen.Options{
		PermissionSeparator: permSep,
		Exclude:             claimed,
	})
	for _, gen := range []routegen.File{idFile, auditFile} {
		rel := "services/" + gen.Service + ".yml"
		rendered, err := routegen.Render(gen, nil)
		if err != nil {
			return nil, fmt.Errorf("render %s: %w", rel, err)
		}
		serviceFiles[rel] = rendered
	}

	// Ensure plat5.yml lists every service file in deterministic order.
	svcPaths := make([]string, 0, len(serviceFiles))
	for rel := range serviceFiles {
		svcPaths = append(svcPaths, rel)
	}
	sort.Slice(svcPaths, func(i, j int) bool {
		// identity first, audit second, rest alphabetically.
		ri, rj := serviceOrderRank(svcPaths[i]), serviceOrderRank(svcPaths[j])
		if ri != rj {
			return ri < rj
		}
		return svcPaths[i] < svcPaths[j]
	})
	plat5Bytes, err = AppendYAMLSequence(plat5Bytes, "services", svcPaths)
	if err != nil {
		return nil, fmt.Errorf("update plat5.yml services: %w", err)
	}

	// Wire compose includes and console settings into plat5.local.yml.
	localPatch := map[string]any{}
	if len(infraIncludes) > 0 {
		infraComposeBytes, err = AppendYAMLSequence(infraComposeBytes, "include", infraIncludes)
		if err != nil {
			return nil, fmt.Errorf("docker-compose.infra.yml: %w", err)
		}
		localPatch["infra"] = map[string]any{"compose": "./docker-compose.infra.yml"}
	}
	if len(appIncludes) > 0 {
		appComposeBytes, err = AppendYAMLSequence(appComposeBytes, "include", appIncludes)
		if err != nil {
			return nil, fmt.Errorf("docker-compose.yml: %w", err)
		}
		appMap := map[string]any{"compose": "./docker-compose.yml"}
		if customerWeb != "" {
			appMap["web"] = customerWeb
		}
		localPatch["app"] = appMap
	}
	if hasOperator {
		opMap := map[string]any{"config": "./operator.yml"}
		if operatorConsoleC != "" {
			opMap["console"] = map[string]any{
				"compose": "./" + operatorConsoleC,
				"web":     operatorWeb,
			}
		}
		localPatch["operator"] = opMap
	}
	if permSep != config.DefaultPermissionSeparator {
		localPatch["routes"] = map[string]any{"permission_separator": permSep}
	}
	if len(localPatch) > 0 {
		localBytes, err = MergeYAMLNode(localBytes, localPatch)
		if err != nil {
			return nil, fmt.Errorf("plat5.local.yml: %w", err)
		}
	}

	// Stage 5: In-memory validation of plat5.yml + all services/*.yml and plat5.local.yml!
	if err := validateAssembledPlat5(plat5Bytes, serviceFiles); err != nil {
		return nil, err
	}
	if _, err := config.Parse(localBytes); err != nil {
		return nil, fmt.Errorf("assembled plat5.local.yml invalid: %w", err)
	}

	// Build final Plan ops.
	plan := &Plan{
		Next:         uniqStrings(nextHints),
		Warnings:     uniqStrings(warnings),
		ReproduceCmd: formatReproduceCmd(spec),
	}
	addRootOp := func(rel string, data, prev []byte, existed bool, mode os.FileMode, summary string) {
		if len(data) == 0 {
			return
		}
		if existed && bytes.Equal(data, prev) {
			return
		}
		kind := OpCreate
		if existed {
			kind = OpUpdate
		}
		plan.Ops = append(plan.Ops, FileOp{Kind: kind, Path: rel, Data: data, Mode: mode, Summary: summary})
	}

	var prevPlat5, prevLocal, prevOp, prevInfra, prevApp, prevEnvEx, prevEnvLoc, prevGI []byte
	if isAdd {
		prevPlat5 = existing.Plat5YML
		prevLocal = existing.LocalYML
		prevOp = existing.OperatorYML
		prevInfra = existing.InfraComposeYML
		prevApp = existing.AppComposeYML
		prevEnvEx = existing.EnvExample
		prevEnvLoc = existing.EnvLocal
		prevGI = existing.Gitignore
	}

	addRootOp("plat5.yml", plat5Bytes, prevPlat5, isAdd && len(existing.Plat5YML) > 0, 0o644, "deployment config")
	for _, rel := range svcPaths {
		var prevSvc []byte
		existed := false
		if isAdd {
			prevSvc, existed = existing.ServiceFiles[rel]
		}
		addRootOp(rel, serviceFiles[rel], prevSvc, existed, 0o644, "service routes")
	}
	addRootOp(config.FileName, localBytes, prevLocal, isAdd && len(existing.LocalYML) > 0, 0o644, "local stack config")
	if hasOperator && len(operatorBytes) > 0 {
		addRootOp("operator.yml", operatorBytes, prevOp, isAdd && len(existing.OperatorYML) > 0, 0o644, "operator gateway config")
	}
	if len(infraComposeBytes) > 0 {
		addRootOp("docker-compose.infra.yml", infraComposeBytes, prevInfra, isAdd && len(existing.InfraComposeYML) > 0, 0o644, "project infra compose")
	}
	if len(appComposeBytes) > 0 {
		addRootOp("docker-compose.yml", appComposeBytes, prevApp, isAdd && len(existing.AppComposeYML) > 0, 0o644, "project apps compose")
	}

	// Plugin files (sorted deterministically).
	extraPaths := make([]string, 0, len(extraFiles))
	for rel := range extraFiles {
		extraPaths = append(extraPaths, rel)
	}
	sort.Strings(extraPaths)
	for _, rel := range extraPaths {
		plan.Ops = append(plan.Ops, extraFiles[rel])
	}

	// Hygiene: .env.example, .env.local (0600), .gitignore.
	if len(envEntries) > 0 {
		gitignoreLines = append(gitignoreLines, config.DefaultEnvFile)
		exData := mergeEnvFile(envExampleBytes, envEntries, false)
		locData := mergeEnvFile(envLocalBytes, envEntries, true)
		addRootOp(".env.example", exData, prevEnvEx, isAdd && len(existing.EnvExample) > 0, 0o644, "env template")
		addRootOp(config.DefaultEnvFile, locData, prevEnvLoc, isAdd && len(existing.EnvLocal) > 0, 0o600, "local secrets (0600)")
	}
	if len(gitignoreLines) > 0 {
		giData := appendLines(gitignoreBytes, gitignoreLines)
		addRootOp(".gitignore", giData, prevGI, isAdd && len(existing.Gitignore) > 0, 0o644, "git ignore rules")
	}

	return plan, nil
}

func validateAssembledPlat5(plat5Bytes []byte, serviceFiles map[string][]byte) error {
	var top map[string]any
	if err := yaml.Unmarshal(plat5Bytes, &top); err != nil {
		return fmt.Errorf("assembled plat5.yml: %w", err)
	}
	svcs := map[string]any{}
	for rel, raw := range serviceFiles {
		var m map[string]any
		if err := yaml.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("assembled %s: %w", rel, err)
		}
		if len(m) != 1 {
			return fmt.Errorf("assembled %s: must hold exactly one service", rel)
		}
		for name, body := range m {
			if _, dup := svcs[name]; dup {
				return fmt.Errorf("duplicate service %q across service files", name)
			}
			svcs[name] = body
		}
	}
	top["services"] = svcs
	errs, err := schema.ValidateConfig(top)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		return fmt.Errorf("assembled plat5.yml failed schema validation:\n  %s", strings.Join(msgs, "\n  "))
	}
	return nil
}

func baseLocalYML(spec Spec, hasOperator bool) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# How plat5 start runs this project on your machine.\n")
	fmt.Fprintf(&b, "project_id: %s\n", spec.ProjectID)
	fmt.Fprintf(&b, "cli: \">=%s\"\n", config.SchemaVersion)
	if len(spec.Versions) > 0 {
		b.WriteString("versions:\n")
		for _, k := range config.Sources {
			if v := spec.Versions[k]; v != "" {
				fmt.Fprintf(&b, "  %s: %s\n", k, v)
			}
		}
	}
	if len(spec.Sources) > 0 {
		b.WriteString("source:\n")
		for _, k := range config.Sources {
			if v := spec.Sources[k]; v != "" {
				fmt.Fprintf(&b, "  %s: %q\n", k, v)
			}
		}
	}
	b.WriteString("modules:\n")
	b.WriteString("  auth: true\n")
	b.WriteString("  observability: true\n")
	fmt.Fprintf(&b, "  operator: %t\n", hasOperator)
	return []byte(b.String())
}

func checkPluginCompatibility(plugins []*plugin.Plugin, hasOperator bool) error {
	singleSlot := map[string]string{}
	byID := map[string]*plugin.Plugin{}
	for _, p := range plugins {
		byID[p.Manifest.Name] = p
		if p.Manifest.Requires.Operator && !hasOperator {
			return fmt.Errorf("%s requires the operator module to be enabled", p.Manifest.Name)
		}
		if p.Slot != plugin.SlotService && p.Slot != plugin.SlotCI && p.Slot != plugin.SlotDeploy {
			if prev, exists := singleSlot[p.Slot]; exists {
				return fmt.Errorf("slot %s cannot have both %s and %s", p.Slot, prev, p.Manifest.Name)
			}
			singleSlot[p.Slot] = p.Manifest.Name
		}
	}
	for _, p := range plugins {
		for _, c := range p.Manifest.Conflicts {
			if c.ID != "" {
				if _, hit := byID[c.ID]; hit {
					return fmt.Errorf("%s conflicts with %s", p.Manifest.Name, c.ID)
				}
			}
			if c.Type != "" {
				for _, other := range plugins {
					if other == p {
						continue
					}
					if other.Manifest.Type == c.Type && (c.AuthZStyle == "" || other.Manifest.Capabilities.AuthZStyle == c.AuthZStyle) {
						return fmt.Errorf("%s conflicts with %s", p.Manifest.Name, other.Manifest.Name)
					}
				}
			}
		}
	}
	return nil
}

func orderPlugins(in []*plugin.Plugin) []*plugin.Plugin {
	rank := map[string]int{
		plugin.SlotPreset:          1,
		plugin.SlotAuthZ:           2,
		plugin.SlotService:         3,
		plugin.SlotAuth:            4,
		plugin.SlotOperatorAuth:    5,
		plugin.SlotOperatorAuthZ:   6,
		plugin.SlotCustomerConsole: 7,
		plugin.SlotOperatorConsole: 8,
		plugin.SlotObservability:   9,
		plugin.SlotCI:              10,
		plugin.SlotDeploy:          11,
	}
	out := append([]*plugin.Plugin(nil), in...)
	sort.SliceStable(out, func(i, j int) bool {
		return rank[out[i].Slot] < rank[out[j].Slot]
	})
	return out
}

// TemplateContext is the typed context exposed to [[ ]] templates.
type TemplateContext struct {
	ProjectID           string
	ServiceName         string
	SecretBrand         string
	PermissionSeparator string
	Prompt              map[string]string
	Slot                string
	Selected            map[string]string
	Services            []string
	HasOperator         bool
	HasCustomerConsole  bool
	Kinds               []string
}

// Perm joins permission segments using the active PermissionSeparator.
func (c *TemplateContext) Perm(parts ...string) string {
	return strings.Join(parts, c.PermissionSeparator)
}

func templateContext(spec Spec, p *plugin.Plugin, permSep string, selected map[string]string, services []string, hasOperator, hasCustomerConsole bool, kinds []string) *TemplateContext {
	return &TemplateContext{
		ProjectID:           spec.ProjectID,
		ServiceName:         p.ServiceName,
		SecretBrand:         spec.SecretBrand,
		PermissionSeparator: permSep,
		Prompt:              map[string]string{},
		Slot:                p.Slot,
		Selected:            selected,
		Services:            services,
		HasOperator:         hasOperator,
		HasCustomerConsole:  hasCustomerConsole,
		Kinds:               kinds,
	}
}

func resolvePrompts(p *plugin.Plugin, ctx *TemplateContext) (map[string]string, error) {
	out := map[string]string{}
	ctx.Prompt = out
	for _, pr := range p.Manifest.Prompts {
		val := strings.TrimSpace(p.Prompts[pr.Key])
		if val == "" && pr.Default != "" {
			rendered, err := renderString(p.Manifest.Name+":prompt."+pr.Key, pr.Default, ctx)
			if err != nil {
				return nil, err
			}
			val = rendered
		}
		if val == "" && pr.IsRequired() {
			return nil, fmt.Errorf("missing required prompt %s.%s (%s); pass --set %s.%s=<value>", p.Manifest.Name, pr.Key, pr.Title, p.Manifest.Name, pr.Key)
		}
		out[pr.Key] = val
	}
	return out, nil
}

func collectPluginFiles(p *plugin.Plugin, targetDir string, ctx *TemplateContext, dst map[string]FileOp) error {
	return filepath.WalkDir(p.Dir, func(src string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(p.Dir, src)
		if err != nil || rel == "." {
			return err
		}
		if p.IsExcluded(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		if p.IsRendered(rel) {
			rendered, err := renderString(p.Manifest.Name+":"+rel, string(data), ctx)
			if err != nil {
				return err
			}
			data = []byte(rendered)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		outPath := joinRel(targetDir, rel)
		if prev, collision := dst[outPath]; collision {
			if bytes.Equal(prev.Data, data) {
				return nil
			}
			return fmt.Errorf("file collision at %s", outPath)
		}
		dst[outPath] = FileOp{
			Kind:    OpCreate,
			Path:    outPath,
			Data:    data,
			Mode:    info.Mode().Perm(),
			Summary: "from " + p.Manifest.Name,
		}
		return nil
	})
}

func remapLocalIdPToOperator(patch map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range patch {
		if k == "modules" || k == "idp" {
			continue
		}
		out[k] = v
	}
	if rawIdP, ok := patch["idp"].(map[string]any); ok {
		opIdP := map[string]any{}
		for _, k := range []string{"issuer", "jwks_uri", "audiences", "client_id"} {
			if v, ok := rawIdP[k]; ok {
				opIdP[k] = v
			}
		}
		if sc, ok := rawIdP["subject_claim"]; ok {
			opIdP["operator_id_claim"] = sc
		}
		opBlock, _ := out["operator"].(map[string]any)
		if opBlock == nil {
			opBlock = map[string]any{}
		}
		opBlock["idp"] = opIdP
		out["operator"] = opBlock
	}
	return out
}

func assertNoSecrets(p *plugin.Plugin, promptVals map[string]string, patch map[string]any) error {
	var secrets []string
	for _, pr := range p.Manifest.Prompts {
		if pr.Type == "secret" {
			if v := promptVals[pr.Key]; len(v) >= 4 {
				secrets = append(secrets, v)
			}
		}
	}
	if len(secrets) == 0 {
		return nil
	}
	raw, _ := yaml.Marshal(patch)
	s := string(raw)
	for _, sec := range secrets {
		if strings.Contains(s, sec) {
			return fmt.Errorf("%s: secret prompt value must not be written into plat5.local.yml (use fragments.env and ${VAR})", p.Manifest.Name)
		}
	}
	return nil
}

func renderString(name, src string, ctx *TemplateContext) (string, error) {
	if !strings.Contains(src, "[[") {
		return src, nil
	}
	funcs := template.FuncMap{
		"lower":      strings.ToLower,
		"upper":      strings.ToUpper,
		"trimSuffix": strings.TrimSuffix,
		"trimPrefix": strings.TrimPrefix,
		"join":       strings.Join,
		"perm":       ctx.Perm,
	}
	t, err := template.New(name).Delims("[[", "]]").Funcs(funcs).Option("missingkey=zero").Parse(src)
	if err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return "", fmt.Errorf("template %s: %w", name, err)
	}
	return buf.String(), nil
}

func renderAny(name string, v any, ctx *TemplateContext) (any, error) {
	switch t := v.(type) {
	case string:
		return renderString(name, t, ctx)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			rk, err := renderString(name+".key", k, ctx)
			if err != nil {
				return nil, err
			}
			rv, err := renderAny(name+"."+rk, val, ctx)
			if err != nil {
				return nil, err
			}
			out[rk] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			rv, err := renderAny(name, item, ctx)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}

func extractKindNames(plat5Bytes []byte) []string {
	if len(plat5Bytes) == 0 {
		return nil
	}
	var doc struct {
		Kinds map[string]any `yaml:"kinds"`
	}
	if yaml.Unmarshal(plat5Bytes, &doc) != nil {
		return nil
	}
	names := make([]string, 0, len(doc.Kinds))
	for k := range doc.Kinds {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func mergeEnvFile(existing []byte, entries []plugin.EnvEntry, includeValues bool) []byte {
	var b strings.Builder
	have := map[string]bool{}
	if len(existing) > 0 {
		text := string(existing)
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteByte('\n')
		}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if k, _, ok := strings.Cut(line, "="); ok {
				have[strings.TrimSpace(strings.TrimPrefix(k, "export "))] = true
			}
		}
	}
	for _, e := range entries {
		if have[e.Name] {
			continue
		}
		have[e.Name] = true
		if e.Comment != "" {
			fmt.Fprintf(&b, "# %s\n", e.Comment)
		}
		if includeValues {
			fmt.Fprintf(&b, "%s=%s\n", e.Name, e.Value)
		} else {
			fmt.Fprintf(&b, "%s=\n", e.Name)
		}
	}
	return []byte(b.String())
}

func appendLines(existing []byte, lines []string) []byte {
	var b strings.Builder
	have := map[string]bool{}
	if len(existing) > 0 {
		text := string(existing)
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteByte('\n')
		}
		for _, l := range strings.Split(text, "\n") {
			have[strings.TrimSpace(l)] = true
		}
	}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || have[l] {
			continue
		}
		have[l] = true
		b.WriteString(l + "\n")
	}
	return []byte(b.String())
}

func joinRel(base, rel string) string {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if base == "" || base == "." {
		return rel
	}
	return filepath.ToSlash(filepath.Clean(base + "/" + rel))
}

func serviceOrderRank(rel string) int {
	switch rel {
	case "services/identity.yml":
		return 0
	case "services/audit.yml":
		return 1
	default:
		return 2
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func uniqStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

func formatReproduceCmd(spec Spec) string {
	parts := []string{"plat5", "init", spec.ProjectID}
	slotFlags := []struct{ slot, flag string }{
		{plugin.SlotPreset, "--preset"},
		{plugin.SlotAuth, "--auth"},
		{plugin.SlotAuthZ, "--authz"},
		{plugin.SlotService, "--service"},
		{plugin.SlotCustomerConsole, "--customer-console"},
		{plugin.SlotOperatorConsole, "--operator-console"},
		{plugin.SlotOperatorAuth, "--operator-auth"},
		{plugin.SlotOperatorAuthZ, "--operator-authz"},
		{plugin.SlotObservability, "--observability"},
		{plugin.SlotCI, "--ci"},
		{plugin.SlotDeploy, "--deploy"},
	}
	for _, sf := range slotFlags {
		for _, ref := range spec.SelectedRefs[sf.slot] {
			if ref != "" {
				parts = append(parts, sf.flag, ref)
			}
		}
	}
	if spec.NoOperator {
		parts = append(parts, "--no-operator")
	}
	return strings.Join(parts, " ")
}
