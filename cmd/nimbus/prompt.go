package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

// ── bubbletea select model ──────────────────────────────────────────────

type selectModel struct {
	label    string
	items    []string
	cursor   int
	selected int
	aborted  bool
}

func (m selectModel) Init() tea.Cmd { return nil }

func (m selectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "enter":
			m.selected = m.cursor
			return m, tea.Quit
		case "ctrl+c", "esc", "q":
			m.aborted = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m selectModel) View() tea.View {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("\033[36m?\033[0m \033[1m%s\033[0m\n", m.label))
	for i, item := range m.items {
		if i == m.cursor {
			b.WriteString(fmt.Sprintf("  \033[36m❯\033[0m %s\n", item))
		} else {
			b.WriteString(fmt.Sprintf("    %s\n", item))
		}
	}
	b.WriteString("\033[2m  ↑/↓ navigate • enter select • esc cancel\033[0m\n")
	return tea.NewView(b.String())
}

// promptSelect runs a bubbletea select list and returns the selected index.
func promptSelect(label string, items []string) int {
	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "No options available")
		os.Exit(1)
	}
	if len(items) == 1 {
		return 0
	}
	m := selectModel{label: label, items: items, selected: -1}
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	final := result.(selectModel)
	if final.aborted {
		fmt.Fprintln(os.Stderr, "Cancelled")
		os.Exit(0)
	}
	return final.selected
}

// promptInput runs a bubbletea text input and returns the entered string.
func promptInput(label string) string {
	m := inputModel{label: label}
	p := tea.NewProgram(m)
	result, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	final := result.(inputModel)
	if final.aborted {
		fmt.Fprintln(os.Stderr, "Cancelled")
		os.Exit(0)
	}
	return final.value
}

// ── bubbletea input model ───────────────────────────────────────────────

type inputModel struct {
	label   string
	value   string
	aborted bool
}

func (m inputModel) Init() tea.Cmd { return nil }

func (m inputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "enter":
			return m, tea.Quit
		case "ctrl+c", "esc":
			m.aborted = true
			return m, tea.Quit
		case "backspace":
			if len(m.value) > 0 {
				m.value = m.value[:len(m.value)-1]
			}
		default:
			if len(msg.String()) == 1 {
				m.value += msg.String()
			}
		}
	}
	return m, nil
}

func (m inputModel) View() tea.View {
	cursor := "\033[7m \033[0m"
	return tea.NewView(fmt.Sprintf("\033[36m?\033[0m \033[1m%s\033[0m %s%s\n", m.label, m.value, cursor))
}

// ── select helpers ──────────────────────────────────────────────────────

// selectApp prompts the user to select an app if name is empty.
func selectApp(ctx context.Context, client *aws.Client, name, region string) (applications.App, error) {
	appClient := applications.NewClient(client)
	apps, err := appClient.List(ctx, region)
	if err != nil {
		return applications.App{}, err
	}
	if len(apps) == 0 {
		return applications.App{}, fmt.Errorf("no applications found")
	}
	if name != "" {
		for _, a := range apps {
			if a.Name == name {
				return a, nil
			}
		}
		return applications.App{}, fmt.Errorf("application %q not found", name)
	}
	names := make([]string, len(apps))
	for i, a := range apps {
		names[i] = a.Name
	}
	idx := promptSelect("Select application", names)
	return apps[idx], nil
}

// selectEnv prompts the user to select an env if env is empty.
func selectEnv(app applications.App, env string) string {
	if env != "" {
		return env
	}
	if len(app.Envs) == 1 {
		return app.Envs[0]
	}
	idx := promptSelect("Select environment", app.Envs)
	return app.Envs[idx]
}

// selectTarget prompts the user to select a target instance if instance is empty.
func selectTarget(ctx context.Context, client *aws.Client, appName, envName, region string, instance string) (string, error) {
	if instance != "" {
		return instance, nil
	}
	appClient := applications.NewClient(client)
	targets, err := appClient.ListInstances(ctx, region)
	if err != nil {
		return "", err
	}
	var matched []applications.Target
	detail, err := appClient.GetDetail(ctx, appName, "", region)
	if err == nil {
		for _, env := range detail.Environments {
			if env.Name == envName {
				matched = env.Targets
				break
			}
		}
	}
	if len(matched) == 0 {
		matched = targets
	}
	if len(matched) == 0 {
		return "", fmt.Errorf("no instances found")
	}
	names := make([]string, len(matched))
	for i, t := range matched {
		names[i] = fmt.Sprintf("%-24s %-10s %s", t.Name, t.State, t.IP)
	}
	idx := promptSelect("Select instance", names)
	return matched[idx].Name, nil
}
