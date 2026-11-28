package wizard

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/plat5dev/cli/internal/config"
	"github.com/plat5dev/cli/internal/plugin"
)

type stepID int

const (
	stepProject stepID = iota
	stepMode
	stepPreset
	stepAuth
	stepAuthZ
	stepServices
	stepConsoles
	stepOperator
	stepOperatorAuthZ
	stepObservability
	stepCIDeploy
	stepConfirm
)

type choice struct {
	ID          string
	Label       string
	Description string
	Disabled    bool
	Tag         string // optional slot tag for combined steps (e.g. ci vs deploy)
}

// Model is the Bubble Tea state machine for the interactive `plat5 init` wizard.
type Model struct {
	cat  *Catalog
	opts Options

	width  int
	height int

	step    stepID
	history []stepID

	// Project directory text input.
	dirInput textinput.Model
	dirErr   string

	// Active picker state (single-select or multi-select).
	choices   []choice
	cursor    int
	selected  map[string]bool // used on multi-select steps
	filtering bool
	filterIn  textinput.Model

	// Accumulated selections.
	dir             string
	customMode      bool
	recipe          string
	preset          string
	auth            string
	authz           string
	services        []string
	customerConsole string
	operatorConsole string
	operatorMode    string // "plat5-auth", "<external-idp>", or "none"
	operatorAuthZ   string // "disabled" or "<authorizer>"
	observability   string
	ciPacks         []string
	deployPacks     []string
	done            bool
	cancelled       bool
}

// Run launches the interactive Bubble Tea setup wizard and returns the user's selections.
func Run(cat *Catalog, opts Options) (Result, error) {
	m := NewModel(cat, opts)
	finalModel, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	if err != nil {
		return Result{}, err
	}
	fm := finalModel.(Model)
	if fm.cancelled {
		return Result{Cancelled: true}, errors.New("cancelled")
	}
	return fm.Result(), nil
}

// NewModel creates an initialized wizard Model (exported for unit tests).
func NewModel(cat *Catalog, opts Options) Model {
	ti := textinput.New()
	ti.Placeholder = "my-app"
	ti.SetValue("my-app")
	ti.Focus()
	ti.CharLimit = 64

	fi := textinput.New()
	fi.Placeholder = "filter..."
	fi.CharLimit = 40

	m := Model{
		cat:             cat,
		opts:            opts,
		width:           100,
		height:          30,
		dirInput:        ti,
		filterIn:        fi,
		dir:             opts.Dir,
		preset:          "saas",
		auth:            "plat5-auth",
		authz:           "plat5-rbac",
		services:        []string{"bun-effect-api"},
		customerConsole: "solid-customer-console",
		operatorConsole: "solid-operator-console",
		operatorMode:    "plat5-auth",
		operatorAuthZ:   "disabled",
		observability:   "local-lgtm",
	}
	if strings.TrimSpace(opts.Dir) != "" {
		m.enterStep(stepMode, false)
	} else {
		m.enterStep(stepProject, false)
	}
	return m
}

func (m Model) Init() tea.Cmd { return textinput.Blink }

func (m *Model) enterStep(next stepID, pushHistory bool) {
	if pushHistory {
		m.history = append(m.history, m.step)
	}
	m.step = next
	m.cursor = 0
	m.filtering = false
	m.filterIn.SetValue("")
	m.filterIn.Blur()
	m.selected = map[string]bool{}
	m.choices = m.choicesFor(next)

	switch next {
	case stepProject:
		if m.dir != "" {
			m.dirInput.SetValue(m.dir)
		}
		m.dirInput.Focus()
	case stepMode:
		m.dirInput.Blur()
		if m.customMode {
			m.selectCursor("custom")
		} else if m.recipe != "" {
			m.selectCursor(m.recipe)
		}
	case stepPreset:
		m.selectCursor(m.preset)
	case stepAuth:
		m.selectCursor(m.auth)
	case stepAuthZ:
		m.selectCursor(m.authz)
	case stepServices:
		for _, s := range m.services {
			m.selected[s] = true
		}
	case stepConsoles:
		if m.customerConsole != "" && m.customerConsole != "none" {
			m.selected[m.customerConsole] = true
		}
		if m.operatorConsole != "" && m.operatorConsole != "none" && m.operatorMode != "none" {
			m.selected[m.operatorConsole] = true
		}
	case stepOperator:
		m.selectCursor(m.operatorMode)
	case stepOperatorAuthZ:
		m.selectCursor(m.operatorAuthZ)
	case stepObservability:
		m.selectCursor(m.observability)
	case stepCIDeploy:
		for _, c := range m.ciPacks {
			m.selected[c] = true
		}
		for _, d := range m.deployPacks {
			m.selected[d] = true
		}
	}
}

func (m *Model) selectCursor(id string) {
	for i, c := range m.choices {
		if c.ID == id {
			m.cursor = i
			return
		}
	}
}

func (m Model) isMultiStep() bool {
	return m.step == stepServices || m.step == stepConsoles || m.step == stepCIDeploy
}

func (m Model) choicesFor(s stepID) []choice {
	fromCat := func(typ string) []choice {
		var out []choice
		for _, it := range m.cat.Items(typ) {
			desc := it.Description
			if !it.Compatible {
				desc += " (requires newer CLI)"
			}
			out = append(out, choice{
				ID:          it.ID,
				Label:       it.ID,
				Description: desc,
				Disabled:    !it.Compatible,
				Tag:         typ,
			})
		}
		return out
	}

	switch s {
	case stepMode:
		out := fromCat(plugin.TypeRecipe)
		out = append(out, choice{
			ID:          "custom",
			Label:       "Custom setup",
			Description: "Choose every layer step-by-step (hierarchy, IdP, AuthZ, services, operator, observability, CI)",
		})
		return out
	case stepPreset:
		return fromCat(plugin.TypePreset)
	case stepAuth:
		return fromCat(plugin.TypeIdP)
	case stepAuthZ:
		return fromCat(plugin.TypeAuthorizer)
	case stepServices:
		return fromCat(plugin.TypeService)
	case stepConsoles:
		out := fromCat(plugin.TypeCustomerConsole)
		out = append(out, fromCat(plugin.TypeOperatorConsole)...)
		return out
	case stepOperator:
		out := fromCat(plugin.TypeIdP)
		out = append(out, choice{ID: "none", Label: "none", Description: "Disable the operator gateway and operator console"})
		return out
	case stepOperatorAuthZ:
		out := []choice{{ID: "disabled", Label: "disabled", Description: "Allow all authenticated operators in local dev"}}
		for _, c := range fromCat(plugin.TypeAuthorizer) {
			if c.ID == "plat5-rbac" {
				continue
			}
			out = append(out, c)
		}
		return out
	case stepObservability:
		out := fromCat(plugin.TypeObservability)
		out = append(out, choice{ID: "none", Label: "none", Description: "Disable observability"})
		return out
	case stepCIDeploy:
		out := fromCat(plugin.TypeCI)
		out = append(out, fromCat(plugin.TypeDeploy)...)
		return out
	default:
		return nil
	}
}

func (m Model) filteredChoices() []choice {
	q := strings.ToLower(strings.TrimSpace(m.filterIn.Value()))
	if q == "" {
		return m.choices
	}
	var out []choice
	for _, c := range m.choices {
		if strings.Contains(strings.ToLower(c.Label), q) || strings.Contains(strings.ToLower(c.Description), q) {
			out = append(out, c)
		}
	}
	return out
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		k := msg.String()
		if k == "ctrl+c" {
			m.cancelled = true
			return m, tea.Quit
		}

		// Step 1: project directory text input.
		if m.step == stepProject {
			switch k {
			case "esc":
				m.cancelled = true
				return m, tea.Quit
			case "enter":
				val := strings.TrimSpace(m.dirInput.Value())
				if val == "" {
					val = "my-app"
				}
				if m.opts.ProjectID == "" && config.SanitizeProjectID(filepath.Base(val)) == "" {
					m.dirErr = "pick a directory name containing letters or digits"
					return m, nil
				}
				m.dirErr = ""
				m.dir = val
				m.enterStep(stepMode, true)
				return m, nil
			}
			var cmd tea.Cmd
			m.dirInput, cmd = m.dirInput.Update(msg)
			return m, cmd
		}

		// Active filter input mode.
		if m.filtering {
			switch k {
			case "esc", "enter":
				m.filtering = false
				m.filterIn.Blur()
				m.cursor = 0
				return m, nil
			}
			var cmd tea.Cmd
			m.filterIn, cmd = m.filterIn.Update(msg)
			m.cursor = 0
			return m, cmd
		}

		// Global back navigation.
		if k == "esc" || k == "shift+tab" {
			if len(m.history) > 0 {
				prev := m.history[len(m.history)-1]
				m.history = m.history[:len(m.history)-1]
				m.enterStep(prev, false)
				return m, nil
			}
			m.cancelled = true
			return m, tea.Quit
		}

		// Confirm screen.
		if m.step == stepConfirm {
			if k == "enter" || k == "y" || k == "Y" {
				m.done = true
				return m, tea.Quit
			}
			return m, nil
		}

		visible := m.filteredChoices()
		switch k {
		case "/":
			if len(m.choices) > 3 {
				m.filtering = true
				m.filterIn.Focus()
				return m, textinput.Blink
			}
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(visible)-1 {
				m.cursor++
			}
		case " ":
			if m.isMultiStep() && m.cursor < len(visible) {
				c := visible[m.cursor]
				if !c.Disabled {
					m.selected[c.ID] = !m.selected[c.ID]
				}
			}
		case "enter":
			return m.advance(visible)
		}
	}
	return m, nil
}

func (m Model) advance(visible []choice) (tea.Model, tea.Cmd) {
	if !m.isMultiStep() {
		if len(visible) == 0 || m.cursor >= len(visible) || visible[m.cursor].Disabled {
			return m, nil
		}
		picked := visible[m.cursor].ID
		switch m.step {
		case stepMode:
			if picked == "custom" {
				m.customMode = true
				m.recipe = ""
				m.enterStep(stepPreset, true)
			} else {
				m.customMode = false
				m.recipe = picked
				m.enterStep(stepConfirm, true)
			}
		case stepPreset:
			m.preset = picked
			m.enterStep(stepAuth, true)
		case stepAuth:
			m.auth = picked
			m.enterStep(stepAuthZ, true)
		case stepAuthZ:
			m.authz = picked
			m.enterStep(stepServices, true)
		case stepOperator:
			m.operatorMode = picked
			if picked == "none" {
				m.operatorConsole = "none"
				m.operatorAuthZ = "disabled"
				m.enterStep(stepObservability, true)
			} else {
				m.enterStep(stepOperatorAuthZ, true)
			}
		case stepOperatorAuthZ:
			m.operatorAuthZ = picked
			m.enterStep(stepObservability, true)
		case stepObservability:
			m.observability = picked
			m.enterStep(stepCIDeploy, true)
		}
		return m, nil
	}

	// Multi-select steps.
	switch m.step {
	case stepServices:
		m.services = m.selectedOrdered(m.choices, "")
		m.enterStep(stepConsoles, true)
	case stepConsoles:
		cust := m.selectedOrdered(m.choices, plugin.TypeCustomerConsole)
		if len(cust) > 0 {
			m.customerConsole = cust[0]
		} else {
			m.customerConsole = "none"
		}
		op := m.selectedOrdered(m.choices, plugin.TypeOperatorConsole)
		if len(op) > 0 {
			m.operatorConsole = op[0]
		} else {
			m.operatorConsole = "none"
		}
		m.enterStep(stepOperator, true)
	case stepCIDeploy:
		m.ciPacks = m.selectedOrdered(m.choices, plugin.TypeCI)
		m.deployPacks = m.selectedOrdered(m.choices, plugin.TypeDeploy)
		m.enterStep(stepConfirm, true)
	}
	return m, nil
}

func (m Model) selectedOrdered(all []choice, wantTag string) []string {
	var out []string
	for _, c := range all {
		if wantTag != "" && c.Tag != wantTag {
			continue
		}
		if m.selected[c.ID] {
			out = append(out, c.ID)
		}
	}
	return out
}

// Result converts the final wizard state into a Result.
func (m Model) Result() Result {
	dir := strings.TrimSpace(m.dir)
	if dir == "" {
		dir = "my-app"
	}
	if !m.customMode && m.recipe != "" {
		return Result{
			Dir:    dir,
			Recipe: m.recipe,
			Slots:  map[string][]string{},
		}
	}
	slots := map[string][]string{
		plugin.SlotPreset: {m.preset},
		plugin.SlotAuth:   {m.auth},
		plugin.SlotAuthZ:  {m.authz},
	}
	if len(m.services) > 0 {
		slots[plugin.SlotService] = append([]string(nil), m.services...)
	}
	if m.customerConsole != "" && m.customerConsole != "none" {
		slots[plugin.SlotCustomerConsole] = []string{m.customerConsole}
	}
	noOp := m.operatorMode == "none"
	if !noOp {
		if m.operatorConsole != "" && m.operatorConsole != "none" {
			slots[plugin.SlotOperatorConsole] = []string{m.operatorConsole}
		}
		if m.operatorMode != "" && m.operatorMode != "plat5-auth" {
			slots[plugin.SlotOperatorAuth] = []string{m.operatorMode}
		}
		if m.operatorAuthZ != "" && m.operatorAuthZ != "disabled" {
			slots[plugin.SlotOperatorAuthZ] = []string{m.operatorAuthZ}
		}
	}
	if m.observability != "" {
		slots[plugin.SlotObservability] = []string{m.observability}
	}
	if len(m.ciPacks) > 0 {
		slots[plugin.SlotCI] = append([]string(nil), m.ciPacks...)
	}
	if len(m.deployPacks) > 0 {
		slots[plugin.SlotDeploy] = append([]string(nil), m.deployPacks...)
	}
	return Result{
		Dir:        dir,
		Slots:      slots,
		NoOperator: noOp,
	}
}

// --- Rendering ---

var (
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	styleHeading = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	styleCheck   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleActive  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	styleErr     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	styleBox     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("238")).Padding(0, 1)
)

func (m Model) View() string {
	projName := m.dir
	if projName == "" {
		projName = strings.TrimSpace(m.dirInput.Value())
	}
	if projName == "" {
		projName = "new project"
	}
	header := styleTitle.Render("plat5 init") + styleDim.Render(" · "+projName)

	mainPane := m.renderMain()
	footer := styleDim.Render(m.footerHelp())

	if m.width >= 90 {
		leftW := m.width - 36
		if leftW < 48 {
			leftW = 48
		}
		left := lipgloss.NewStyle().Width(leftW).Render(mainPane)
		right := styleBox.Width(32).Render(m.renderSummary())
		body := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
		return "\n" + header + "\n\n" + body + "\n\n" + footer + "\n"
	}
	return "\n" + header + "\n\n" + mainPane + "\n\n" + footer + "\n"
}

func (m Model) renderMain() string {
	var b strings.Builder
	b.WriteString(m.renderStepRail())
	b.WriteString("\n\n")
	b.WriteString(styleHeading.Render(m.stepTitle()))
	b.WriteString("\n")

	if m.step == stepProject {
		b.WriteString("\n" + m.dirInput.View() + "\n")
		if m.dirErr != "" {
			b.WriteString(styleErr.Render(m.dirErr) + "\n")
		}
		return b.String()
	}

	if m.step == stepConfirm {
		b.WriteString("\n" + m.renderSummary() + "\n\n")
		b.WriteString(styleActive.Render("Press Enter to create this project") + styleDim.Render(" (or Esc to go back)"))
		return b.String()
	}

	if m.filtering || m.filterIn.Value() != "" {
		b.WriteString(styleDim.Render("/ ") + m.filterIn.View() + "\n")
	}
	b.WriteString("\n")
	visible := m.filteredChoices()
	if len(visible) == 0 {
		b.WriteString(styleDim.Render("  (no matching plugins)"))
		return b.String()
	}
	for i, c := range visible {
		cursor := "  "
		if i == m.cursor {
			cursor = styleActive.Render("▸ ")
		}
		box := ""
		if m.isMultiStep() {
			if m.selected[c.ID] {
				box = styleCheck.Render("[✓] ")
			} else {
				box = styleDim.Render("[ ] ")
			}
		}
		label := fmt.Sprintf("%-24s", c.Label)
		if i == m.cursor {
			label = styleActive.Render(label)
		} else if c.Disabled {
			label = styleDim.Render(label)
		}
		b.WriteString(fmt.Sprintf("%s%s%s %s\n", cursor, box, label, styleDim.Render(c.Description)))
	}
	return b.String()
}

func (m Model) renderStepRail() string {
	type railStep struct {
		id    stepID
		label string
	}
	steps := []railStep{{stepMode, "Mode"}}
	if m.opts.Dir == "" {
		steps = append([]railStep{{stepProject, "Project"}}, steps...)
	}
	if m.customMode || m.step > stepMode && m.recipe == "" {
		steps = append(steps,
			railStep{stepPreset, "Hierarchy"},
			railStep{stepAuth, "Auth"},
			railStep{stepAuthZ, "AuthZ"},
			railStep{stepServices, "Services"},
			railStep{stepConsoles, "Consoles"},
			railStep{stepOperator, "Operator"},
			railStep{stepObservability, "Obs"},
			railStep{stepCIDeploy, "CI"},
		)
	}
	steps = append(steps, railStep{stepConfirm, "Confirm"})

	parts := make([]string, 0, len(steps))
	for _, s := range steps {
		switch {
		case s.id == m.step || (s.id == stepOperator && m.step == stepOperatorAuthZ):
			parts = append(parts, styleActive.Render("● "+s.label))
		case s.id < m.step:
			parts = append(parts, styleCheck.Render("✓ "+s.label))
		default:
			parts = append(parts, styleDim.Render("○ "+s.label))
		}
	}
	return strings.Join(parts, styleDim.Render(" · "))
}

func (m Model) renderSummary() string {
	var b strings.Builder
	b.WriteString(styleHeading.Render("Stack preview") + "\n")
	b.WriteString(styleDim.Render(strings.Repeat("─", 28)) + "\n")
	row := func(k, v string) {
		fmt.Fprintf(&b, "%-11s %s\n", styleDim.Render(k), v)
	}
	dir := m.dir
	if dir == "" {
		dir = strings.TrimSpace(m.dirInput.Value())
	}
	row("Project", config.SanitizeProjectID(filepath.Base(dir)))
	if !m.customMode && m.recipe != "" {
		row("Recipe", m.recipe)
		return b.String()
	}
	row("Hierarchy", m.preset)
	row("Auth", m.auth)
	row("AuthZ", m.authz)
	if len(m.services) == 0 {
		row("Services", "none")
	} else {
		row("Services", strings.Join(m.services, ", "))
	}
	var consoles []string
	if m.customerConsole != "" && m.customerConsole != "none" {
		consoles = append(consoles, "customer")
	}
	if m.operatorMode != "none" && m.operatorConsole != "" && m.operatorConsole != "none" {
		consoles = append(consoles, "operator")
	}
	if len(consoles) == 0 {
		row("Consoles", "none")
	} else {
		row("Consoles", strings.Join(consoles, ", "))
	}
	if m.operatorMode == "none" {
		row("Operator", "off")
	} else {
		op := m.operatorMode
		if m.operatorAuthZ != "" && m.operatorAuthZ != "disabled" {
			op += " + " + m.operatorAuthZ
		}
		row("Operator", op)
	}
	row("Obs", m.observability)
	var ciDep []string
	ciDep = append(ciDep, m.ciPacks...)
	ciDep = append(ciDep, m.deployPacks...)
	if len(ciDep) == 0 {
		row("CI/Deploy", "none")
	} else {
		row("CI/Deploy", strings.Join(ciDep, ", "))
	}
	return b.String()
}

func (m Model) stepTitle() string {
	switch m.step {
	case stepProject:
		return "Project directory"
	case stepMode:
		return "Choose a quickstart recipe or customize every layer"
	case stepPreset:
		return "Hierarchy & access preset"
	case stepAuth:
		return "Customer authentication (IdP)"
	case stepAuthZ:
		return "Customer authorization (RBAC / AuthZEN 1.0)"
	case stepServices:
		return "Application services (space toggles, 0 or more)"
	case stepConsoles:
		return "Web consoles (space toggles)"
	case stepOperator:
		return "Operator plane authentication"
	case stepOperatorAuthZ:
		return "Operator plane authorization (AuthZEN 1.0)"
	case stepObservability:
		return "Observability (OpenTelemetry)"
	case stepCIDeploy:
		return "CI workflows & deployment overlays (space toggles)"
	case stepConfirm:
		return "Review your stack"
	default:
		return ""
	}
}

func (m Model) footerHelp() string {
	switch {
	case m.step == stepProject:
		return "enter continue · esc cancel"
	case m.step == stepConfirm:
		return "enter create project · esc back · ctrl+c cancel"
	case m.isMultiStep():
		return "↑/↓ move · space toggle · / filter · enter continue · esc back"
	default:
		return "↑/↓ move · / filter · enter select · esc back"
	}
}
