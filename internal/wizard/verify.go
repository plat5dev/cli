package wizard

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/plat5dev/cli/internal/plugin"
)

// VerifyOutcome is the result of a plugin's read-only HTTP preflight check.
type VerifyOutcome struct {
	PluginName string
	Label      string
	URL        string
	StatusCode int
	Elapsed    time.Duration
	OK         bool
	Err        error
}

// RunVerifyCheck executes a plugin's declarative verify probe using answered prompts.
func RunVerifyCheck(ctx context.Context, p *plugin.Plugin, client *http.Client) VerifyOutcome {
	v := p.Manifest.Verify
	if v == nil {
		return VerifyOutcome{PluginName: p.Manifest.Name, OK: true}
	}
	method := http.MethodGet
	rawURL := v.HTTPGet
	if rawURL == "" && v.HTTPHead != "" {
		method = http.MethodHead
		rawURL = v.HTTPHead
	}
	urlStr, err := renderVerifyString(rawURL, p.Prompts)
	if err != nil {
		return VerifyOutcome{PluginName: p.Manifest.Name, Label: v.Label, Err: err}
	}
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		return VerifyOutcome{PluginName: p.Manifest.Name, Label: v.Label, URL: urlStr, Err: fmt.Errorf("verify URL must be http:// or https://")}
	}

	timeout := 5 * time.Second
	if v.Timeout != "" {
		if d, err := time.ParseDuration(v.Timeout); err == nil && d > 0 && d <= 15*time.Second {
			timeout = d
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, urlStr, nil)
	if err != nil {
		return VerifyOutcome{PluginName: p.Manifest.Name, Label: v.Label, URL: urlStr, Err: err}
	}
	for k, val := range v.Headers {
		rv, err := renderVerifyString(val, p.Prompts)
		if err != nil {
			return VerifyOutcome{PluginName: p.Manifest.Name, Label: v.Label, URL: urlStr, Err: err}
		}
		req.Header.Set(k, rv)
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	wantStatus := v.ExpectStatus
	if wantStatus == 0 {
		wantStatus = http.StatusOK
	}

	start := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return VerifyOutcome{PluginName: p.Manifest.Name, Label: v.Label, URL: urlStr, Elapsed: elapsed, Err: err}
	}
	defer resp.Body.Close()

	ok := resp.StatusCode == wantStatus
	var statusErr error
	if !ok {
		statusErr = fmt.Errorf("HTTP %d (expected %d)", resp.StatusCode, wantStatus)
	}
	return VerifyOutcome{
		PluginName: p.Manifest.Name,
		Label:      v.Label,
		URL:        urlStr,
		StatusCode: resp.StatusCode,
		Elapsed:    elapsed,
		OK:         ok,
		Err:        statusErr,
	}
}

func renderVerifyString(src string, prompts map[string]string) (string, error) {
	if !strings.Contains(src, "[[") {
		return src, nil
	}
	funcs := template.FuncMap{
		"lower":      strings.ToLower,
		"upper":      strings.ToUpper,
		"trimSuffix": strings.TrimSuffix,
		"trimPrefix": strings.TrimPrefix,
		"join":       strings.Join,
	}
	t, err := template.New("verify").Delims("[[", "]]").Funcs(funcs).Option("missingkey=zero").Parse(src)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, struct{ Prompt map[string]string }{Prompt: prompts}); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// VerifyAction is what the user chose when a verify probe failed.
type VerifyAction int

const (
	VerifyContinue VerifyAction = iota
	VerifyRetryEdit
	VerifyCancel
)

// RunVerifySpinner runs an animated Bubble Tea preflight check for p.
func RunVerifySpinner(p *plugin.Plugin, client *http.Client) (VerifyOutcome, VerifyAction, error) {
	if p.Manifest.Verify == nil {
		return VerifyOutcome{PluginName: p.Manifest.Name, OK: true}, VerifyContinue, nil
	}
	m := newVerifyModel(p, client)
	finalModel, err := tea.NewProgram(m).Run()
	if err != nil {
		return VerifyOutcome{}, VerifyCancel, err
	}
	vm := finalModel.(verifyModel)
	return vm.outcome, vm.action, nil
}

type verifyDoneMsg struct{ outcome VerifyOutcome }

type verifyModel struct {
	plugin  *plugin.Plugin
	client  *http.Client
	spin    spinner.Model
	done    bool
	outcome VerifyOutcome
	action  VerifyAction
}

func newVerifyModel(p *plugin.Plugin, client *http.Client) verifyModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	return verifyModel{plugin: p, client: client, spin: sp, action: VerifyContinue}
}

func (m verifyModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, func() tea.Msg {
		return verifyDoneMsg{outcome: RunVerifyCheck(context.Background(), m.plugin, m.client)}
	})
}

func (m verifyModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case verifyDoneMsg:
		m.done = true
		m.outcome = msg.outcome
		if m.outcome.OK {
			m.action = VerifyContinue
			return m, tea.Quit
		}
		return m, nil
	case spinner.TickMsg:
		if !m.done {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.action = VerifyCancel
			return m, tea.Quit
		case "r", "R":
			if m.done && !m.outcome.OK {
				m.action = VerifyRetryEdit
				return m, tea.Quit
			}
		case "enter":
			if m.done {
				m.action = VerifyContinue
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m verifyModel) View() string {
	label := m.plugin.Manifest.Verify.Label
	if label == "" {
		label = "Verifying " + m.plugin.Manifest.Name
	}
	if !m.done {
		return fmt.Sprintf("  %s %s…\n", m.spin.View(), label)
	}
	if m.outcome.OK {
		okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
		return fmt.Sprintf("  %s %s (%d OK, %dms)\n", okStyle.Render("✓"), m.outcome.URL, m.outcome.StatusCode, m.outcome.Elapsed.Milliseconds())
	}
	warnStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	return fmt.Sprintf("  %s %s failed: %v\n    [r] re-edit prompts · [enter] continue anyway · [ctrl+c] cancel\n",
		warnStyle.Render("!"), label, m.outcome.Err)
}
