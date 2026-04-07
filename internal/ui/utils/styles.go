package utils

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var (
	HeaderStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	SelectedStyle  = lipgloss.NewStyle().Background(lipgloss.Color("27")).Foreground(lipgloss.Color("255"))
	StatusBarStyle = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("255"))
	TitleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	HelpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	ErrorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	RunningStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	StoppedStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	ModalStyle     = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("62")).Padding(1, 2).Background(lipgloss.Color("236"))
	DimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	FilterStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	ToastStyle     = lipgloss.NewStyle().Background(lipgloss.Color("52")).Foreground(lipgloss.Color("255")).Padding(0, 2).Bold(true)
)

// Toast represents a dismissable error banner.
type Toast struct {
	messages []string
	expireAt time.Time
}

type ToastExpireMsg struct{}

const ToastDuration = 5 * time.Second

func NewToast(msgs []string) Toast {
	return Toast{messages: msgs, expireAt: time.Now().Add(ToastDuration)}
}

func (t Toast) Active() bool {
	return len(t.messages) > 0 && time.Now().Before(t.expireAt)
}

func (t Toast) View(width int) string {
	if !t.Active() {
		return ""
	}
	text := " ⚠ " + strings.Join(t.messages, " │ ")
	return ToastStyle.Width(width).Render(text)
}

func ScheduleToastExpiry() tea.Cmd {
	return tea.Tick(ToastDuration, func(time.Time) tea.Msg { return ToastExpireMsg{} })
}

// BoolPtr returns a pointer to a bool value.
func BoolPtr(b bool) *bool { return &b }

// StrPtr returns a pointer to a string value.
func StrPtr(s string) *string { return &s }
