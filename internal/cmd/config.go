package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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

  -out plan.json        also save the plan: the apply body, the config with expect
  --detailed-exitcode   exit 0 with no changes, 2 with changes, 1 on an error`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPlan,
}

var applyCmd = &cobra.Command{
	Use:   "apply [plat5.yml | plan.json]",
	Short: "Plan, confirm, and apply the config",
	Long: `Plan, show the diff, ask to confirm, and apply. Then wait up to 10 seconds
for the gateway's replicas to load the new revision, and say where they stand.
A replica that refused it exits 1, though the revision is applied.

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
	planCmd.Flags().StringVar(&flagOut, "out", "", "Save the plan to this file")
	planCmd.Flags().BoolVar(&flagDetailedExitcode, "detailed-exitcode", false, "Exit 0 with no changes, 2 with changes, 1 on an error")
	applyCmd.Flags().BoolVar(&flagAutoApprove, "auto-approve", false, "Apply without the prompt")
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
		return reportGateway(client, applied.Revision, os.Stdout, "")
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
	if !flagAutoApprove {
		if !prompt.Interactive() {
			return fmt.Errorf("no terminal to confirm on: use --auto-approve, or plat5 plan -out and plat5 apply plan.json")
		}
		if !confirmed(os.Stdin, os.Stdout) {
			return fmt.Errorf("not applied")
		}
	}

	applied, err := client.ApplyConfig(applyBody(cfg.Flat, p.Revision))
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
		printEntry(w, "  ", opSigns[c.Op], c.What+" "+c.Name, c.Before, c.After)
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
// that differs. A value that isn't an object, such as a setting's, goes on the
// head: ~ setting deletion_grace: "30d" → "7d".
func printEntry(w io.Writer, indent, sign, head string, before, after json.RawMessage) {
	bf, bObj := objectFields(before)
	af, aObj := objectFields(after)
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
