package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// StepType defines the type of wizard step
type StepType int

const (
	StepSelect StepType = iota
	StepText
	StepTextArea
	StepFilePicker
	StepReview
)

// Option for select steps
type Option struct {
	Value       string
	Label       string
	Description string
	Price       string
}

// Step represents a wizard step
type Step struct {
	Key          string
	Title        string
	Description  string
	Type         StepType
	Options      []Option
	Required     bool
	Optional     bool
	Value        string
	DefaultValue string
	selected     int
	textInput    textinput.Model
	textArea     textarea.Model
	filePath     string
	skipLabel    string
}

// Wizard is a multi-step form wizard
type Wizard struct {
	Title       string
	Steps       []Step
	current     int
	width       int
	height      int
	err         string
	completed   bool
	cancelled   bool
	reviewing   bool
	fileList    []string
	fileCursor  int
	browsingDir string
}

func (w *Wizard) CurrentIndex() int { return w.current }

// NewWizard creates a new wizard
func NewWizard(title string, steps []Step) *Wizard {
	for i := range steps {
		switch steps[i].Type {
		case StepText:
			ti := textinput.New()
			if steps[i].DefaultValue != "" {
				ti.Placeholder = steps[i].DefaultValue
			} else {
				ti.Placeholder = "Enter value..."
			}
			ti.CharLimit = 64
			ti.Width = 40
			steps[i].textInput = ti
		case StepTextArea:
			ta := textarea.New()
			ta.Placeholder = "Enter script or leave empty..."
			ta.Prompt = ""
			ta.SetWidth(56)
			ta.SetHeight(6)
			ta.ShowLineNumbers = false
			steps[i].textArea = ta
		case StepFilePicker:
			ti := textinput.New()
			ti.Placeholder = "Enter file path..."
			ti.CharLimit = 256
			ti.Width = 50
			steps[i].textInput = ti
		}
		if steps[i].skipLabel == "" && steps[i].Optional {
			steps[i].skipLabel = "Skip"
		}
	}
	w := &Wizard{Title: title, Steps: steps}
	// Focus first text input
	if len(steps) > 0 && steps[0].Type == StepText {
		steps[0].textInput.Focus()
	}
	return w
}

func (w *Wizard) SetSize(width, height int) {
	w.width, w.height = width, height
}

func (w *Wizard) Current() *Step {
	if w.current < len(w.Steps) {
		return &w.Steps[w.current]
	}
	return nil
}

func (w *Wizard) Values() map[string]string {
	vals := make(map[string]string)
	for _, s := range w.Steps {
		val := s.Value
		if val == "" && s.DefaultValue != "" {
			val = s.DefaultValue
		}
		vals[s.Key] = val
	}
	return vals
}

func (w *Wizard) IsCompleted() bool { return w.completed }
func (w *Wizard) IsCancelled() bool { return w.cancelled }

func (w *Wizard) Update(msg tea.Msg) (*Wizard, tea.Cmd) {
	step := w.Current()
	if step == nil {
		return w, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			if w.reviewing {
				w.reviewing = false
				w.current = len(w.Steps) - 2 // Go back to last real step
				return w, nil
			}
			w.cancelled = true
			return w, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("ctrl+c"))):
			w.cancelled = true
			return w, nil

		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			return w.handleEnter()

		case key.Matches(msg, key.NewBinding(key.WithKeys("tab"))):
			if step.Optional {
				step.Value = ""
				return w.nextStep()
			}

		case key.Matches(msg, key.NewBinding(key.WithKeys("up", "k"))):
			if step.Type == StepSelect && step.selected > 0 {
				step.selected--
			}

		case key.Matches(msg, key.NewBinding(key.WithKeys("down", "j"))):
			if step.Type == StepSelect && step.selected < len(step.Options)-1 {
				step.selected++
			}

		case key.Matches(msg, key.NewBinding(key.WithKeys("backspace"))):
			if step.Type == StepText || step.Type == StepFilePicker {
				// Let text input handle backspace
				var cmd tea.Cmd
				step.textInput, cmd = step.textInput.Update(msg)
				step.Value = step.textInput.Value()
				return w, cmd
			}
			if step.Type == StepTextArea {
				var cmd tea.Cmd
				step.textArea, cmd = step.textArea.Update(msg)
				step.Value = step.textArea.Value()
				return w, cmd
			}
			if w.current > 0 {
				w.current--
				w.reviewing = false
				return w, nil
			}
		}
	}

	// Update text inputs for all key messages
	switch step.Type {
	case StepText, StepFilePicker:
		var cmd tea.Cmd
		step.textInput, cmd = step.textInput.Update(msg)
		step.Value = step.textInput.Value()
		return w, cmd
	case StepTextArea:
		var cmd tea.Cmd
		step.textArea, cmd = step.textArea.Update(msg)
		step.Value = step.textArea.Value()
		return w, cmd
	}

	return w, nil
}

func (w *Wizard) handleEnter() (*Wizard, tea.Cmd) {
	step := w.Current()

	if w.reviewing {
		w.completed = true
		return w, nil
	}

	switch step.Type {
	case StepSelect:
		if len(step.Options) > 0 {
			step.Value = step.Options[step.selected].Value
		}
		return w.nextStep()

	case StepText, StepFilePicker:
		step.Value = step.textInput.Value()
		if step.Required && step.Value == "" {
			w.err = step.Title + " is required"
			return w, nil
		}
		return w.nextStep()

	case StepTextArea:
		step.Value = step.textArea.Value()
		return w.nextStep()

	case StepReview:
		w.completed = true
		return w, nil
	}

	return w, nil
}

func (w *Wizard) nextStep() (*Wizard, tea.Cmd) {
	w.err = ""
	w.current++

	if w.current >= len(w.Steps) {
		w.reviewing = true
		w.current = len(w.Steps) - 1
		return w, nil
	}

	// Focus text inputs
	step := w.Current()
	if step.Type == StepText || step.Type == StepFilePicker {
		return w, step.textInput.Focus()
	}
	if step.Type == StepTextArea {
		return w, step.textArea.Focus()
	}

	return w, nil
}

func (w *Wizard) View() string {
	var b strings.Builder

	// Progress indicator
	progress := w.renderProgress()
	b.WriteString(progress + "\n\n")

	if w.reviewing {
		b.WriteString(w.renderReview())
	} else {
		step := w.Current()
		if step != nil {
			b.WriteString(w.renderStep(step))
		}
	}

	if w.err != "" {
		b.WriteString("\n" + ErrorStyle.Render("  ⚠ "+w.err))
	}

	b.WriteString("\n\n" + w.renderHelp())

	return b.String()
}

func (w *Wizard) renderProgress() string {
	var dots []string
	for i := range w.Steps {
		if i < w.current {
			dots = append(dots, lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("●"))
		} else if i == w.current {
			dots = append(dots, lipgloss.NewStyle().Foreground(lipgloss.Color("212")).Render("●"))
		} else {
			dots = append(dots, lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("○"))
		}
	}
	return "  " + strings.Join(dots, " ")
}

func (w *Wizard) renderStep(step *Step) string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

	b.WriteString(titleStyle.Render("  "+step.Title) + "\n")
	if step.Description != "" {
		b.WriteString(descStyle.Render("  "+step.Description) + "\n")
	}
	b.WriteString("\n")

	switch step.Type {
	case StepSelect:
		b.WriteString(w.renderSelectOptions(step))
	case StepText, StepFilePicker:
		b.WriteString("  " + step.textInput.View())
	case StepTextArea:
		b.WriteString(step.textArea.View())
	}

	return b.String()
}

func (w *Wizard) renderSelectOptions(step *Step) string {
	var b strings.Builder

	labelStyle := lipgloss.NewStyle().Width(20)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	priceStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	selectedBg := lipgloss.NewStyle().Background(lipgloss.Color("27")).Foreground(lipgloss.Color("255"))

	for i, opt := range step.Options {
		prefix := "  "
		label := labelStyle.Render(opt.Label)
		desc := ""
		if opt.Description != "" {
			desc = descStyle.Render(opt.Description)
		}
		price := ""
		if opt.Price != "" {
			price = priceStyle.Render(" " + opt.Price)
		}

		line := prefix + label + desc + price

		if i == step.selected {
			line = selectedBg.Render("▸ " + labelStyle.Render(opt.Label) + desc + price)
		}
		b.WriteString(line + "\n")
	}

	return b.String()
}

func (w *Wizard) renderReview() string {
	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	labelStyle := lipgloss.NewStyle().Width(20).Foreground(lipgloss.Color("245"))
	valueStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	b.WriteString(titleStyle.Render("  Review Configuration") + "\n\n")

	for _, step := range w.Steps {
		if step.Type == StepReview {
			continue
		}
		val := step.Value
		if val == "" && step.DefaultValue != "" {
			val = step.DefaultValue
		}
		if val == "" {
			val = "(not set)"
		}
		// Find label for select options
		if step.Type == StepSelect {
			for _, opt := range step.Options {
				if opt.Value == step.Value {
					val = opt.Label
					break
				}
			}
		}
		if len(val) > 40 {
			val = val[:37] + "..."
		}
		b.WriteString("  " + labelStyle.Render(step.Title+":") + valueStyle.Render(val) + "\n")
	}

	return b.String()
}

func (w *Wizard) renderHelp() string {
	step := w.Current()
	if w.reviewing {
		return HelpStyle.Render("  enter:create  backspace:back  esc:cancel")
	}
	if step == nil {
		return ""
	}

	var parts []string
	parts = append(parts, "enter:continue")

	if step.Type == StepSelect {
		parts = []string{"↑↓:select", "enter:continue"}
	}

	if step.Optional {
		parts = append(parts, "tab:skip")
	}
	if w.current > 0 && step.Type == StepSelect {
		parts = append(parts, "backspace:back")
	}
	parts = append(parts, "esc:cancel")

	return HelpStyle.Render("  " + strings.Join(parts, "  "))
}
