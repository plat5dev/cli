package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/controlplane"
	"github.com/plat5dev/cli/internal/plat5yml"
	"github.com/plat5dev/cli/internal/prompt"
	"github.com/spf13/cobra"
)

// The deployment's config: plat5.yml and its service files, planned and applied
// as a whole (plat5 docs/reference/cli.md).

const (
	defaultConfigFile      = "plat5.yml"
	defaultControlPlaneURL = "http://localhost:5402"
)

var (
	flagOut              string
	flagDetailedExitcode bool
	flagAutoApprove      bool
	flagConfirm          []string
	flagRevision         int64
)

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
	Use:   "validate [plat5.yml]",
	Short: "Check plat5.yml and its service files against the schema, offline",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runValidate,
}

var planCmd = &cobra.Command{
	Use:   "plan [plat5.yml]",
	Short: "Show what applying the config would change (a dry run)",
	Long: `Show what applying the config would change: a dry run on the control plane.

  -out plan.json        also save the plan: the apply body, config with expect and confirm
  --detailed-exitcode   exit 0 with no changes, 2 with changes, 1 on an error`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPlan,
}

var applyCmd = &cobra.Command{
	Use:   "apply [plat5.yml | plan.json]",
	Short: "Plan, confirm, and apply the config",
	Long: `Plan, show the diff, ask to confirm, and apply.

A saved plan (plat5 plan -out plan.json) applies as it is, with no prompt.
--auto-approve skips the prompt; a destructive plan also needs --confirm kind=nodes
for each kind it removes.`,
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
	planCmd.Flags().StringVar(&flagOut, "out", "", "Save the plan to this file")
	planCmd.Flags().BoolVar(&flagDetailedExitcode, "detailed-exitcode", false, "Exit 0 with no changes, 2 with changes, 1 on an error")
	applyCmd.Flags().BoolVar(&flagAutoApprove, "auto-approve", false, "Apply without the prompt")
	applyCmd.Flags().StringArrayVar(&flagConfirm, "confirm", nil, "Confirm a destructive change: kind=nodes, e.g. folder=14")
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

// configClient reaches the control plane for config commands: --control-plane-url
// or PLAT5_CONTROL_PLANE_URL, --admin-token or PLAT5_ADMIN_TOKEN, else the local
// stack's.
func configClient() *controlplane.Client {
	url := firstNonEmpty(flagControlPlaneURL, os.Getenv("PLAT5_CONTROL_PLANE_URL"), defaultControlPlaneURL)
	token := firstNonEmpty(flagAdminToken, os.Getenv("PLAT5_ADMIN_TOKEN"), config.DefaultAdminToken)
	return controlplane.New(url, token)
}

func configPath(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return defaultConfigFile
}

// loadValid reads the config and checks it against the schema.
func loadValid(path string) (*plat5yml.Config, error) {
	cfg, err := plat5yml.Load(path)
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
		return nil, fmt.Errorf("%d schema errors; nothing sent", len(problems))
	}
	return cfg, nil
}

// explain turns a control plane error into lines naming the file and field.
func explain(cfg *plat5yml.Config, err error) error {
	var ce *controlplane.ConfigError
	if !errors.As(err, &ce) {
		return err
	}
	if len(ce.Fields) > 0 && cfg != nil {
		for _, line := range cfg.Describe(ce.Fields) {
			fmt.Fprintln(os.Stderr, "  "+line)
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
	path := configPath(args)
	cfg, err := loadValid(path)
	if err != nil {
		return err
	}
	fmt.Printf("%s is valid: %d services.\n", path, len(cfg.Files))
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
	cfg, err := loadValid(configPath(args))
	if err != nil {
		return err
	}
	p, err := configClient().DryRun(cfg.Flat)
	if err != nil {
		return explain(cfg, err)
	}
	printPlan(os.Stdout, p)
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

// applyBody is the config with the apply's preconditions.
func applyBody(flat map[string]any, revision int64, confirm map[string]controlplane.Confirm) map[string]any {
	body := make(map[string]any, len(flat)+2)
	for k, v := range flat {
		body[k] = v
	}
	body["expect"] = map[string]any{"revision": revision}
	if len(confirm) > 0 {
		body["confirm"] = confirm
	}
	return body
}

// writeSavedPlan saves exactly the apply body.
func writeSavedPlan(path string, flat map[string]any, p *controlplane.Plan) error {
	data, err := json.MarshalIndent(applyBody(flat, p.Revision, p.Confirm), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func runApply(cmd *cobra.Command, args []string) error {
	client := configClient()
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
		applied, err := client.ApplyConfig(body)
		if err != nil {
			return explain(nil, err)
		}
		fmt.Printf("Applied revision %d.\n", applied.Revision)
		return nil
	}

	cfg, err := loadValid(configPath(args))
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

	var confirm map[string]controlplane.Confirm
	if flagAutoApprove {
		confirm, err = confirmFromFlags(p, flagConfirm)
		if err != nil {
			return err
		}
	} else {
		if !prompt.Interactive() {
			return fmt.Errorf("no terminal to confirm on: use --auto-approve, or plat5 plan -out and plat5 apply plan.json")
		}
		confirm, err = confirmInteractively(os.Stdin, os.Stdout, p)
		if err != nil {
			return err
		}
	}

	applied, err := client.ApplyConfig(applyBody(cfg.Flat, p.Revision, confirm))
	if err != nil {
		return explain(cfg, err)
	}
	fmt.Printf("\nApplied revision %d.\n", applied.Revision)
	return nil
}

// confirmFromFlags takes --confirm kind=nodes for each kind the plan removes.
// Counts that no longer match are the control plane's to refuse.
func confirmFromFlags(p *controlplane.Plan, flags []string) (map[string]controlplane.Confirm, error) {
	confirm := map[string]controlplane.Confirm{}
	for _, f := range flags {
		kind, n, ok := strings.Cut(f, "=")
		nodes, err := strconv.ParseInt(n, 10, 64)
		if !ok || kind == "" || err != nil {
			return nil, fmt.Errorf("--confirm %s: want kind=nodes, e.g. folder=14", f)
		}
		confirm[kind] = controlplane.Confirm{Nodes: nodes}
	}
	var missing []string
	for kind, c := range p.Confirm {
		if _, ok := confirm[kind]; !ok {
			missing = append(missing, fmt.Sprintf("--confirm %s=%d", kind, c.Nodes))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("the plan is destructive; --auto-approve also needs %s", strings.Join(missing, " "))
	}
	return confirm, nil
}

// confirmInteractively asks for each destructive kind's node count, then yes.
func confirmInteractively(in io.Reader, out io.Writer, p *controlplane.Plan) (map[string]controlplane.Confirm, error) {
	r := bufio.NewReader(in)
	ask := func(q string) string {
		fmt.Fprint(out, q)
		line, _ := r.ReadString('\n')
		return strings.TrimSpace(line)
	}
	kinds := make([]string, 0, len(p.Confirm))
	for k := range p.Confirm {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		n := p.Confirm[kind].Nodes
		if ask(fmt.Sprintf("\nRemoving kind %s soft-deletes %d nodes. Type %d to confirm: ", kind, n, n)) != strconv.FormatInt(n, 10) {
			return nil, fmt.Errorf("not confirmed; nothing applied")
		}
	}
	if ask("\nApply this plan? Only yes applies: ") != "yes" {
		return nil, fmt.Errorf("not applied")
	}
	return p.Confirm, nil
}

var opSigns = map[string]string{"add": "+", "change": "~", "remove": "-"}

// printPlan writes the plan as Terraform does: + add, ~ change, - remove, each
// destructive and blocked line marked, then the summary.
func printPlan(w io.Writer, p *controlplane.Plan) {
	if p.Empty() {
		fmt.Fprintf(w, "No changes. The deployment at revision %d matches the config.\n", p.Revision)
		return
	}
	fmt.Fprintf(w, "Plan against revision %d:\n\n", p.Revision)
	for _, c := range p.Changes {
		line := fmt.Sprintf("  %s %s %s", opSigns[c.Op], c.What, c.Name)
		var note string
		switch {
		case c.Class == "destructive" && c.Effects != nil:
			note = fmt.Sprintf("DESTRUCTIVE: soft-deletes %d nodes (%d bindings, %d service accounts, %d keys)",
				c.Effects.Nodes, c.Effects.Bindings, c.Effects.ServiceAccounts, c.Effects.Keys)
		case c.Class == "blocked" && c.InTheWay != nil:
			note = fmt.Sprintf("BLOCKED (%s): %d nodes in the way", c.Reason, c.InTheWay.Nodes)
			if len(c.InTheWay.Sample) > 0 {
				note += ", e.g. " + strings.Join(c.InTheWay.Sample, ", ")
			}
		case c.Routes != nil && c.Op == "change":
			note = fmt.Sprintf("routes +%d ~%d -%d", c.Routes.Add, c.Routes.Change, c.Routes.Remove)
		case c.Routes != nil && c.Op == "add":
			note = fmt.Sprintf("%d routes", c.Routes.Add)
		case c.Routes != nil && c.Op == "remove":
			note = fmt.Sprintf("%d routes", c.Routes.Remove)
		}
		if note != "" {
			line = fmt.Sprintf("%-40s %s", line, note)
		}
		fmt.Fprintln(w, line)
	}
	s := p.Summary
	fmt.Fprintf(w, "\nPlan: %d to add, %d to change, %d to remove.", s.Add, s.Change, s.Remove)
	if s.Blocked > 0 {
		fmt.Fprintf(w, " %d blocked: nothing can apply until it's cleared.", s.Blocked)
	}
	fmt.Fprintln(w)
}

func runConfigPull(cmd *cobra.Command, args []string) error {
	dir := "."
	if len(args) == 1 {
		dir = args[0]
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if found := yamlFiles(dir); len(found) > 0 {
		return fmt.Errorf("%s already holds %s: pull into a directory with no .yml files", dir, strings.Join(found, ", "))
	}
	rev, err := configClient().GetRevision(flagRevision)
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

// yamlFiles is every .yml or .yaml file in dir and dir/services.
func yamlFiles(dir string) []string {
	var out []string
	for _, d := range []string{dir, filepath.Join(dir, "services")} {
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			ext := strings.ToLower(filepath.Ext(e.Name()))
			if !e.IsDir() && (ext == ".yml" || ext == ".yaml") {
				rel, _ := filepath.Rel(dir, filepath.Join(d, e.Name()))
				out = append(out, rel)
			}
		}
	}
	return out
}
