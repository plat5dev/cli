package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/plat5dev/cli/internal/auth"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/plat5yml"
	"github.com/plat5dev/cli/internal/prompt"
	"github.com/plat5dev/cli/internal/schema"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// The deployment's config: plat5.yml and its service files, planned and applied
// as a whole.

const (
	defaultConfigFile      = "plat5.yml"
	defaultControlPlaneURL = "http://localhost:5402"
)

var (
	flagFiles            []string
	flagAuthConfig       string
	flagOut              string
	flagDetailedExitcode bool
	flagAutoApprove      bool
	flagApplyReason      string
	flagApplyRef         string
	flagRevision         int64
)

func addFileFlag(cmd *cobra.Command) {
	cmd.Flags().StringArrayVarP(&flagFiles, "file", "f", nil, "Config file (repeatable: later files merge over earlier ones)")
}

// exitError ends the command with a code, after its output.
type exitError struct{ code int }

func (e exitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// ExitCode is the process exit code for an error from Execute: --detailed-exitcode's
// 2, or 1.
func ExitCode(err error) int {
	var e exitError
	if errors.As(err, &e) {
		return e.code
	}
	return 1
}

var validateCmd = &cobra.Command{
	Use:   "validate [-f file ...] [plat5.yml]",
	Short: "Check plat5.yml and its service files against the schema, offline",
	Long: `Check plat5.yml and its service files against the schema, offline.

Pass -f multiple times to merge environment overlays (RFC 7396 merge-patch):
  plat5 validate -f plat5.yml -f plat5.prod.yml`,
	Args: cobra.MaximumNArgs(1),
	RunE: runValidate,
}

var planCmd = &cobra.Command{
	Use:   "plan [-f file ...] [plat5.yml]",
	Short: "Show what applying the config would change (a dry run)",
	Long: `Show what applying the config would change: a dry run on the control plane.

  -f file               config file; repeatable to merge overlays (-f plat5.yml -f plat5.prod.yml)
  -out plan.json        also save the plan: the apply body, the config with expect
  --detailed-exitcode   exit 0 with no changes, 2 with changes, 1 on an error`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPlan,
}

var applyCmd = &cobra.Command{
	Use:   "apply [-f file ...] [plat5.yml | plan.json]",
	Short: "Plan, confirm, and apply the config",
	Long: `Plan, show the diff, ask to confirm, and apply. Then wait up to 10 seconds
for the gateway's replicas to load the new revision, and say where they stand.
A replica that refused it exits 1, though the revision is applied.

Pass -f multiple times to merge environment overlays (plat5 apply -f plat5.yml -f plat5.prod.yml).
A saved plan (plat5 plan -out plan.json) applies as it is, with no prompt.
--auto-approve skips the prompt. A blocked plan never applies.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runApply,
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read the applied config",
}

var configPullCmd = &cobra.Command{
	Use:   "pull [dir]",
	Short: "Write an applied revision out as plat5.yml and services/<service>.yml",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runConfigPull,
}

func init() {
	addFileFlag(validateCmd)
	addFileFlag(planCmd)
	addFileFlag(applyCmd)
	validateCmd.Flags().StringVar(&flagAuthConfig, "auth-config", "", "Validate a control-plane AUTH_CONFIG_FILE against control_plane_auth.json")
	planCmd.Flags().StringVar(&flagOut, "out", "", "Save the plan to this file")
	planCmd.Flags().BoolVar(&flagDetailedExitcode, "detailed-exitcode", false, "Exit 0 with no changes, 2 with changes, 1 on an error")
	applyCmd.Flags().BoolVar(&flagAutoApprove, "auto-approve", false, "Apply without the prompt")
	applyCmd.Flags().StringVar(&flagApplyReason, "reason", "", "Optional change reason recorded on the revision (X-Plat5-Change-Reason)")
	applyCmd.Flags().StringVar(&flagApplyRef, "ref", "", "Optional change reference such as a git SHA or PR URL (X-Plat5-Change-Ref; auto-detected in CI)")
	configPullCmd.Flags().Int64Var(&flagRevision, "revision", 0, "The revision to pull (default: the current one)")
	configCmd.AddCommand(configPullCmd)
}

// normalizeArgs accepts Terraform's single-dash -out as --out.
func normalizeArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		switch {
		case a == "-out":
			out[i] = "--out"
		case strings.HasPrefix(a, "-out="):
			out[i] = "-" + a
		default:
			out[i] = a
		}
	}
	return out
}

func loadLocalProjectIfPresent(urlOverride string) (*config.Resolved, error) {
	cfg, err := config.Load(config.Overrides{ControlPlaneURL: urlOverride})
	if err == nil {
		return &cfg, nil
	}
	if errors.Is(err, config.ErrNoProject{}) {
		return nil, nil
	}
	return nil, err
}

// configClient reaches the control plane for config commands.
// Credential precedence:
//  1. --token / PLAT5_TOKEN / --admin-token / PLAT5_ADMIN_TOKEN
//  2. --token-file / PLAT5_TOKEN_FILE
//  3. --token-cmd / PLAT5_TOKEN_CMD
//  4. Cached `plat5 login` OIDC credential for the target URL (auto-refreshed)
//  5. Native CI OIDC (GitHub Actions ACTIONS_ID_TOKEN_REQUEST_URL, GitLab PLAT5_OIDC_TOKEN)
//  6. Local plat5.local.yml admin_token, else local default dev-admin-token
func configClient() (*controlplane.Client, error) {
	url := firstNonEmpty(flagControlPlaneURL, os.Getenv("PLAT5_CONTROL_PLANE_URL"))
	token := firstNonEmpty(
		flagToken,
		os.Getenv("PLAT5_TOKEN"),
		flagAdminToken,
		os.Getenv("PLAT5_ADMIN_TOKEN"),
	)
	tokenFile := firstNonEmpty(flagTokenFile, os.Getenv("PLAT5_TOKEN_FILE"))
	tokenCmd := firstNonEmpty(flagTokenCmd, os.Getenv("PLAT5_TOKEN_CMD"))

	if token == "" && tokenFile != "" {
		t, err := auth.ReadTokenFile(tokenFile)
		if err != nil {
			return nil, err
		}
		token = t
	}
	if token == "" && tokenCmd != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		t, err := auth.ExecTokenCmd(ctx, tokenCmd)
		cancel()
		if err != nil {
			return nil, err
		}
		token = t
	}

	var localCfg *config.Resolved
	if url == "" {
		cfg, err := loadLocalProjectIfPresent("")
		if err != nil {
			return nil, err
		}
		localCfg = cfg
		if localCfg != nil && localCfg.ControlPlaneURL != "" {
			url = localCfg.ControlPlaneURL
		}
	}
	url = strings.TrimRight(firstNonEmpty(url, defaultControlPlaneURL), "/")

	if token == "" {
		if store, err := auth.DefaultStore(); err == nil {
			if cred, err := store.Load(url); err == nil && cred != nil {
				if cred.Expired(time.Now()) {
					if cred.RefreshToken != "" {
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						refreshed, rerr := auth.RefreshToken(ctx, nil, cred)
						cancel()
						if rerr != nil {
							return nil, fmt.Errorf("cached session for %s expired and refresh failed (%w); run \"plat5 login\"", url, rerr)
						}
						_ = store.Save(url, refreshed)
						cred = refreshed
					} else {
						return nil, fmt.Errorf("cached session for %s expired; run \"plat5 login\"", url)
					}
				}
				token = cred.ActiveToken()
			}
		}
	}

	if token == "" {
		aud := firstNonEmpty(flagOIDCAudience, os.Getenv("PLAT5_OIDC_AUDIENCE"), url)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		ghTok, err := auth.FetchGitHubOIDC(ctx, nil, aud)
		cancel()
		if err != nil {
			return nil, err
		}
		if ghTok != "" {
			token = ghTok
		} else if glTok := auth.GitLabOIDCToken(); glTok != "" {
			token = glTok
		}
	}

	if token == "" {
		if localCfg == nil {
			cfg, err := loadLocalProjectIfPresent(url)
			if err != nil {
				return nil, err
			}
			localCfg = cfg
		}
		if localCfg != nil && localCfg.AdminToken != "" {
			token = localCfg.AdminToken
		}
	}

	token = firstNonEmpty(token, config.DefaultAdminToken)
	return controlplane.New(url, token), nil
}

func configPaths(args []string) ([]string, error) {
	if len(flagFiles) > 0 {
		if len(args) > 0 {
			return nil, fmt.Errorf("cannot combine -f with a positional file argument")
		}
		return flagFiles, nil
	}
	if len(args) == 1 {
		return []string{args[0]}, nil
	}
	return []string{defaultConfigFile}, nil
}

// loadValid reads the config (base plus any overlays) and checks it against the schema.
func loadValid(paths ...string) (*plat5yml.Config, error) {
	cfg, err := plat5yml.LoadMany(paths...)
	if err != nil {
		return nil, err
	}
	problems, err := cfg.Validate()
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "  "+p)
		}
		noun := "errors"
		if len(problems) == 1 {
			noun = "error"
		}
		return nil, fmt.Errorf("%d schema %s; nothing sent", len(problems), noun)
	}
	return cfg, nil
}

// printMissingIdentityKindHints prints a non-blocking hint when kinds declared
// in plat5.yml have no scope routes in services.identity (e.g. after adding a
// kind without re-running `plat5 routes generate`).
func printMissingIdentityKindHints(w io.Writer, cfg *plat5yml.Config) {
	kinds, _ := cfg.Flat["kinds"].(map[string]any)
	services, _ := cfg.Flat["services"].(map[string]any)
	identity, ok := services["identity"].(map[string]any)
	if !ok || len(kinds) == 0 {
		return
	}
	scopes, _ := identity["scopes"].(map[string]any)
	var missing []string
	for k := range kinds {
		if _, has := scopes[k]; !has {
			missing = append(missing, k)
		}
	}
	if len(missing) == 0 {
		return
	}
	sort.Strings(missing)
	idFile := cfg.FileOf("services.identity")
	if idFile == "" {
		idFile = "services/identity.yml"
	}
	if len(missing) == 1 {
		fmt.Fprintf(w, "Note: kind %q has no routes in %s — run \"plat5 routes generate\" if you want them.\n", missing[0], idFile)
	} else {
		fmt.Fprintf(w, "Note: kinds %s have no routes in %s — run \"plat5 routes generate\" if you want them.\n", strings.Join(missing, ", "), idFile)
	}
}

// explain turns a control plane error into lines naming the file and field.
func explain(cfg *plat5yml.Config, err error) error {
	var ce *controlplane.ConfigError
	if !errors.As(err, &ce) {
		return err
	}
	if len(ce.Fields) > 0 {
		if cfg != nil {
			for _, line := range cfg.Describe(ce.Fields) {
				fmt.Fprintln(os.Stderr, "  "+line)
			}
		} else {
			for _, f := range ce.Fields {
				fmt.Fprintln(os.Stderr, "  "+f.Error())
			}
		}
		return fmt.Errorf("the control plane refused the config (%s)", ce.Code)
	}
	if ce.Code == "PLAN_STALE" {
		return fmt.Errorf("the deployment changed since this plan was made (%v: planned %v, now %v). Nothing applied; plan again",
			ce.Details["precondition"], orNone(ce.Details["expected"]), orNone(ce.Details["actual"]))
	}
	return err
}

func orNone(v any) any {
	if v == nil {
		return "none"
	}
	return v
}

func runValidate(cmd *cobra.Command, args []string) error {
	if flagAuthConfig != "" {
		if len(flagFiles) > 0 || len(args) > 0 {
			return fmt.Errorf("cannot combine --auth-config with -f or a positional plat5.yml path")
		}
		return validateAuthConfigFile(flagAuthConfig)
	}
	paths, err := configPaths(args)
	if err != nil {
		return err
	}
	cfg, err := loadValid(paths...)
	if err != nil {
		return err
	}
	fmt.Printf("%s is valid: %d services.\n", strings.Join(paths, " + "), len(cfg.Files))
	printMissingIdentityKindHints(os.Stdout, cfg)
	return nil
}

func validateAuthConfigFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	// Normalize YAML values into JSON-shaped maps/slices for jsonschema validation.
	jsonBytes, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	var normalized any
	if err := json.Unmarshal(jsonBytes, &normalized); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	errs, err := schema.ValidateControlPlaneAuth(normalized)
	if err != nil {
		return err
	}
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", path, e.Error())
		}
		noun := "errors"
		if len(errs) == 1 {
			noun = "error"
		}
		return fmt.Errorf("%d auth config %s", len(errs), noun)
	}
	issuers := 0
	if m, ok := normalized.(map[string]any); ok {
		if list, ok := m["issuers"].([]any); ok {
			issuers = len(list)
		}
	}
	fmt.Printf("%s is valid: %d issuers.\n", path, issuers)
	return nil
}

func runPlan(cmd *cobra.Command, args []string) error {
	err := plan(args)
	if err != nil && flagDetailedExitcode {
		var e exitError
		if !errors.As(err, &e) {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return exitError{1}
		}
	}
	return err
}

func plan(args []string) error {
	paths, err := configPaths(args)
	if err != nil {
		return err
	}
	cfg, err := loadValid(paths...)
	if err != nil {
		return err
	}
	client, err := configClient()
	if err != nil {
		return err
	}
	p, err := client.DryRun(cfg.Flat)
	if err != nil {
		return explain(cfg, err)
	}
	printPlan(os.Stdout, p)
	printMissingIdentityKindHints(os.Stdout, cfg)
	if flagOut != "" {
		if err := writeSavedPlan(flagOut, cfg.Flat, p); err != nil {
			return err
		}
		fmt.Printf("\nSaved to %s. Apply it with: plat5 apply %s\n", flagOut, flagOut)
	}
	if flagDetailedExitcode && !p.Empty() {
		return exitError{2}
	}
	return nil
}

// applyBody is the config with the apply's precondition.
func applyBody(flat map[string]any, revision int64) map[string]any {
	body := make(map[string]any, len(flat)+1)
	for k, v := range flat {
		body[k] = v
	}
	body["expect"] = map[string]any{"revision": revision}
	return body
}

// writeSavedPlan saves exactly the apply body.
func writeSavedPlan(path string, flat map[string]any, p *controlplane.Plan) error {
	data, err := json.MarshalIndent(applyBody(flat, p.Revision), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func resolveApplyOptions() controlplane.ApplyOptions {
	ref := flagApplyRef
	if ref == "" {
		ref = firstNonEmpty(os.Getenv("GITHUB_SHA"), os.Getenv("CI_COMMIT_SHA"))
	}
	return controlplane.ApplyOptions{
		Reason: flagApplyReason,
		Ref:    ref,
	}
}

func runApply(cmd *cobra.Command, args []string) error {
	if len(flagFiles) > 0 && len(args) > 0 {
		return fmt.Errorf("cannot combine -f with a positional file argument")
	}
	client, err := configClient()
	if err != nil {
		return err
	}
	opts := resolveApplyOptions()
	if len(args) == 1 && strings.EqualFold(filepath.Ext(args[0]), ".json") {
		// A saved plan: saving it was the review.
		data, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			return fmt.Errorf("%s: %w", args[0], err)
		}
		applied, err := client.ApplyConfigWithOptions(body, opts)
		if err != nil {
			return explain(nil, err)
		}
		fmt.Printf("Applied revision %d.\n", applied.Revision)
		return reportGateway(client, applied.Revision, os.Stdout, "")
	}

	paths, err := configPaths(args)
	if err != nil {
		return err
	}
	cfg, err := loadValid(paths...)
	if err != nil {
		return err
	}
	p, err := client.DryRun(cfg.Flat)
	if err != nil {
		return explain(cfg, err)
	}
	printPlan(os.Stdout, p)
	if p.Empty() {
		return nil
	}
	if p.Summary.Blocked > 0 {
		return fmt.Errorf("the plan has blocked changes; nothing applied")
	}
	if !flagAutoApprove {
		if !prompt.Interactive() {
			return fmt.Errorf("no terminal to confirm on: use --auto-approve, or plat5 plan -out and plat5 apply plan.json")
		}
		if !confirmed(os.Stdin, os.Stdout) {
			return fmt.Errorf("not applied")
		}
	}

	applied, err := client.ApplyConfigWithOptions(applyBody(cfg.Flat, p.Revision), opts)
	if err != nil {
		return explain(cfg, err)
	}
	fmt.Printf("\nApplied revision %d.\n", applied.Revision)
	return reportGateway(client, applied.Revision, os.Stdout, "")
}

// confirmed asks to apply. Only yes applies.
func confirmed(in io.Reader, out io.Writer) bool {
	fmt.Fprint(out, "\nApply this plan? Only yes applies: ")
	line, _ := bufio.NewReader(in).ReadString('\n')
	return strings.TrimSpace(line) == "yes"
}

// How long an apply waits for the gateway's replicas to report the new
// revision, and how often it asks.
var (
	gatewayWait = 10 * time.Second
	gatewayPoll = 500 * time.Millisecond
)

// reportGateway waits for every gateway replica to load revision or refuse it,
// up to gatewayWait, then prints where they stand. A refusal is an error: the
// revision is applied, and that replica keeps serving the one it had.
func reportGateway(client *controlplane.Client, revision int64, w io.Writer, indent string) error {
	deadline := time.Now().Add(gatewayWait)
	var st *controlplane.Status
	for {
		var err error
		st, err = client.Status()
		if err != nil {
			return err
		}
		if gatewaySettled(st.Gateway, revision) || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(gatewayPoll)
	}
	if len(st.Gateway) == 0 {
		fmt.Fprintf(w, "%sgateway: no replica has reported in the last minute\n", indent)
		return nil
	}
	on, refused := 0, 0
	for _, r := range st.Gateway {
		if r.Loaded >= revision {
			on++
		}
	}
	fmt.Fprintf(w, "%sgateway: %d/%d replicas on revision %d\n", indent, on, len(st.Gateway), revision)
	for _, r := range st.Gateway {
		switch {
		case r.Loaded >= revision:
		case r.Refused != nil && r.Refused.Revision >= revision:
			refused++
			fmt.Fprintf(w, "%s  %s refused revision %d: %s\n", indent, r.Replica, r.Refused.Revision, r.Refused.Reason)
		default:
			fmt.Fprintf(w, "%s  %s is still on revision %d\n", indent, r.Replica, r.Loaded)
		}
	}
	if refused > 0 {
		return fmt.Errorf("revision %d is applied, but %d of %d gateway replicas refused it and serve the revision they had", revision, refused, len(st.Gateway))
	}
	return nil
}

// gatewaySettled reports whether every replica has loaded revision or refused it.
func gatewaySettled(replicas []controlplane.GatewayReplica, revision int64) bool {
	if len(replicas) == 0 {
		return false
	}
	for _, r := range replicas {
		if r.Loaded < revision && (r.Refused == nil || r.Refused.Revision < revision) {
			return false
		}
	}
	return true
}

var opSigns = map[string]string{"add": "+", "change": "~", "remove": "-"}

// printPlan writes the plan as Terraform does: + add, ~ change, - remove, each
// line with its changed fields as before → after, a service's expanded routes
// the same way, blocked lines with what's in the way, then the summary.
func printPlan(w io.Writer, p *controlplane.Plan) {
	if p.Empty() {
		fmt.Fprintf(w, "No changes. The deployment at revision %d matches the config.\n", p.Revision)
		return
	}
	fmt.Fprintf(w, "Plan against revision %d:\n", p.Revision)
	for _, c := range p.Changes {
		fmt.Fprintln(w)
		head := c.What
		if c.Name != "" && c.Name != c.What {
			head = c.What + " " + c.Name
		}
		printEntry(w, "  ", opSigns[c.Op], head, c.Before, c.After)
		if c.Blocked != nil {
			nodes := "nodes"
			if c.Blocked.InTheWay.Nodes == 1 {
				nodes = "node"
			}
			note := fmt.Sprintf("BLOCKED (%s): %d %s in the way", c.Blocked.Reason, c.Blocked.InTheWay.Nodes, nodes)
			if len(c.Blocked.InTheWay.Sample) > 0 {
				note += ", e.g. " + strings.Join(c.Blocked.InTheWay.Sample, ", ")
			}
			fmt.Fprintln(w, "      "+note)
		}
		for _, r := range c.Routes {
			printEntry(w, "      ", opSigns[r.Op], r.Method+" "+r.Path, r.Before, r.After)
		}
	}
	s := p.Summary
	fmt.Fprintf(w, "\nPlan: %d to add, %d to change, %d to remove.", s.Add, s.Change, s.Remove)
	if s.Blocked > 0 {
		fmt.Fprintf(w, " %d blocked: nothing can apply until it's cleared.", s.Blocked)
	}
	fmt.Fprintln(w)
}

// printEntry writes one plan line or route: its sign and head, then each field
// that differs, leaving out fields that are null or empty. A value that isn't
// an object, such as a setting's, goes on the head:
// ~ setting deletion_grace: "30d" → "7d".
func printEntry(w io.Writer, indent, sign, head string, before, after json.RawMessage) {
	bf, bObj := objectFields(before)
	af, aObj := objectFields(after)
	bf, af = setFields(bf), setFields(af)
	if (len(before) > 0 && !bObj) || (len(after) > 0 && !aObj) {
		fmt.Fprintf(w, "%s%s %s: %s\n", indent, sign, head, beforeAfter(before, after))
		return
	}
	fmt.Fprintf(w, "%s%s %s\n", indent, sign, head)
	inAfter := map[string]json.RawMessage{}
	for _, f := range af {
		inAfter[f.key] = f.raw
	}
	inBefore := map[string]bool{}
	for _, f := range bf {
		inBefore[f.key] = true
		if a, ok := inAfter[f.key]; ok {
			fmt.Fprintf(w, "%s    ~ %s: %s\n", indent, f.key, beforeAfter(f.raw, a))
		} else {
			fmt.Fprintf(w, "%s    - %s: %s\n", indent, f.key, formatValue(f.raw))
		}
	}
	for _, f := range af {
		if !inBefore[f.key] {
			fmt.Fprintf(w, "%s    + %s: %s\n", indent, f.key, formatValue(f.raw))
		}
	}
}

// beforeAfter is "before → after", or the one side there is.
func beforeAfter(before, after json.RawMessage) string {
	switch {
	case len(before) == 0:
		return formatValue(after)
	case len(after) == 0:
		return formatValue(before)
	}
	return formatValue(before) + " → " + formatValue(after)
}

// field is one key of an object, in the order the server sent it.
type field struct {
	key string
	raw json.RawMessage
}

// objectFields is raw's keys in order, or false when raw isn't an object.
func objectFields(raw json.RawMessage) ([]field, bool) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	var out []field
	for dec.More() {
		key, err := dec.Token()
		if err != nil {
			return nil, false
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		out = append(out, field{key: key.(string), raw: v})
	}
	return out, true
}

// setFields is fs without the fields that are null, "", [] or {}.
func setFields(fs []field) []field {
	var out []field
	for _, f := range fs {
		var c bytes.Buffer
		if json.Compact(&c, f.raw) == nil {
			switch c.String() {
			case "null", `""`, "[]", "{}":
				continue
			}
		}
		out = append(out, f)
	}
	return out
}

// formatValue writes a JSON value on one line, keys in order:
// {connect: "10s", read: "30s"}, ["organization", "project"].
func formatValue(raw json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var b strings.Builder
	writeValue(dec, &b)
	return b.String()
}

func writeValue(dec *json.Decoder, b *strings.Builder) {
	tok, err := dec.Token()
	if err != nil {
		return
	}
	switch t := tok.(type) {
	case json.Delim:
		b.WriteRune(rune(t))
		for i := 0; dec.More(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			if t == '{' {
				key, _ := dec.Token()
				fmt.Fprintf(b, "%s: ", key)
			}
			writeValue(dec, b)
		}
		end, _ := dec.Token()
		fmt.Fprint(b, end)
	case string:
		fmt.Fprintf(b, "%q", t)
	case nil:
		b.WriteString("null")
	default:
		fmt.Fprint(b, t)
	}
}

func runConfigPull(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if found := existingConfigTargets(dir); len(found) > 0 {
		return fmt.Errorf("%s already holds %s: pull into a directory without plat5.yml or services/", dir, strings.Join(found, ", "))
	}
	client, err := configClient()
	if err != nil {
		return err
	}
	rev, err := client.GetRevision(flagRevision)
	if err != nil {
		return err
	}
	written, err := plat5yml.Write(dir, rev.Config)
	if err != nil {
		return err
	}
	fmt.Printf("Revision %d (%s) written to %s: %s\n", rev.Revision, rev.CreatedAt, dir, strings.Join(written, ", "))
	return nil
}

// existingConfigTargets reports whether plat5.yml or services/ already exists in dir.
func existingConfigTargets(dir string) []string {
	var out []string
	if _, err := os.Stat(filepath.Join(dir, "plat5.yml")); err == nil {
		out = append(out, "plat5.yml")
	}
	if _, err := os.Stat(filepath.Join(dir, "services")); err == nil {
		out = append(out, "services/")
	}
	return out
}
