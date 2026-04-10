package utils

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// StepState represents the state of a step in a multi-step process.
type StepState int

const (
	StepPending StepState = iota
	StepRunning
	StepDone
	StepFailed
)

// ProgressStep is a single step in a multi-step operation.
type ProgressStep struct {
	Label    string
	State    StepState
	Err      error
	SubSteps []ProgressStep
}

// StepProgress tracks a multi-step operation for display.
type StepProgress struct {
	Title string
	Steps []ProgressStep
}

// NewStepProgress creates a new progress tracker with the given title and step labels.
func NewStepProgress(title string, labels ...string) *StepProgress {
	steps := make([]ProgressStep, len(labels))
	for i, l := range labels {
		steps[i] = ProgressStep{Label: l, State: StepPending}
	}
	return &StepProgress{Title: title, Steps: steps}
}

// Start marks the given step as running.
func (p *StepProgress) Start(idx int) {
	if idx >= 0 && idx < len(p.Steps) {
		p.Steps[idx].State = StepRunning
	}
}

// Complete marks the given step as done.
func (p *StepProgress) Complete(idx int) {
	if idx >= 0 && idx < len(p.Steps) {
		p.Steps[idx].State = StepDone
	}
}

// Fail marks the given step as failed with an error.
func (p *StepProgress) Fail(idx int, err error) {
	if idx >= 0 && idx < len(p.Steps) {
		p.Steps[idx].State = StepFailed
		p.Steps[idx].Err = err
	}
}

// SetSubSteps sets sub-steps for a step (shown indented under the parent).
func (p *StepProgress) SetSubSteps(idx int, labels ...string) {
	if idx >= 0 && idx < len(p.Steps) {
		subs := make([]ProgressStep, len(labels))
		for i, l := range labels {
			subs[i] = ProgressStep{Label: l, State: StepPending}
		}
		p.Steps[idx].SubSteps = subs
	}
}

// StartSub marks a sub-step as running.
func (p *StepProgress) StartSub(idx, sub int) {
	if idx >= 0 && idx < len(p.Steps) && sub >= 0 && sub < len(p.Steps[idx].SubSteps) {
		p.Steps[idx].SubSteps[sub].State = StepRunning
	}
}

// CompleteSub marks a sub-step as done.
func (p *StepProgress) CompleteSub(idx, sub int) {
	if idx >= 0 && idx < len(p.Steps) && sub >= 0 && sub < len(p.Steps[idx].SubSteps) {
		p.Steps[idx].SubSteps[sub].State = StepDone
	}
}

// FailSub marks a sub-step as failed and also marks the parent step as failed.
func (p *StepProgress) FailSub(idx, sub int, err error) {
	if idx >= 0 && idx < len(p.Steps) {
		p.Steps[idx].State = StepFailed
		if sub >= 0 && sub < len(p.Steps[idx].SubSteps) {
			p.Steps[idx].SubSteps[sub].State = StepFailed
			p.Steps[idx].SubSteps[sub].Err = err
		}
	}
}

// Done returns true if all steps are done.
func (p *StepProgress) Done() bool {
	for _, s := range p.Steps {
		if s.State != StepDone {
			return false
		}
	}
	return true
}

// Failed returns true if any step failed.
func (p *StepProgress) Failed() bool {
	for _, s := range p.Steps {
		if s.State == StepFailed {
			return true
		}
	}
	return false
}

// Error returns the first error from a failed step or sub-step.
func (p *StepProgress) Error() error {
	for _, s := range p.Steps {
		if s.State == StepFailed {
			if s.Err != nil {
				return s.Err
			}
			for _, sub := range s.SubSteps {
				if sub.State == StepFailed && sub.Err != nil {
					return sub.Err
				}
			}
		}
	}
	return nil
}

// View renders the step progress list. spinnerView is the current spinner frame.
func (p *StepProgress) View(spinnerView string, maxWidth int) string {
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	bold := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))

	var b strings.Builder
	b.WriteString(bold.Render("  "+p.Title) + "\n\n")

	for _, s := range p.Steps {
		renderStep(&b, s, spinnerView, maxWidth, "  ", green, red, dim)
	}

	if p.Failed() {
		b.WriteString("\n" + HelpStyle.Render("  Press esc to go back"))
	}

	return b.String()
}

func renderStep(b *strings.Builder, s ProgressStep, spinnerView string, maxWidth int, indent string, green, red, dim lipgloss.Style) {
	switch s.State {
	case StepDone:
		b.WriteString(green.Render(indent+"✓ "+s.Label) + "\n")
	case StepRunning:
		b.WriteString(fmt.Sprintf("%s%s %s\n", indent, spinnerView, s.Label))
	case StepFailed:
		b.WriteString(red.Render(indent+"✗ "+s.Label) + "\n")
		if s.Err != nil {
			errW := maxWidth - len(indent) - 4
			if errW < 30 {
				errW = 30
			}
			b.WriteString(red.Width(errW).Render(indent+"  "+s.Err.Error()) + "\n")
		}
	default:
		b.WriteString(dim.Render(indent+"○ "+s.Label) + "\n")
	}
	subIndent := indent + "  "
	for _, sub := range s.SubSteps {
		renderStep(b, sub, spinnerView, maxWidth, subIndent, green, red, dim)
	}
}
