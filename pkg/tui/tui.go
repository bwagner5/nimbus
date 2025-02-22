package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/bwagner5/nimbus/pkg/logging"
	"github.com/bwagner5/nimbus/pkg/pretty"
	"github.com/bwagner5/nimbus/pkg/providers/instances"
	"github.com/bwagner5/nimbus/pkg/vm"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/samber/lo"
)

type model struct {
	ctx       context.Context
	vmClient  vm.AWSVM
	namesapce string
	name      string
	// window
	height int
	width  int
	// models
	table     table.Model
	instances []instances.Instance
	help      help.Model
}

type listMsg struct {
	instances []instances.Instance
}

type updatedMsg struct{}

type ListModel struct {
	table.Model
}

func Launch(ctx context.Context, vmClient vm.AWSVM, namespace, name string, verbose bool) error {
	// can't log to the terminal, so log to a file
	if verbose {
		f, err := tea.LogToFile("debug.log", "debug")
		if err != nil {
			return err
		}
		defer f.Close()
		ctx = logging.ToContext(ctx, logging.DefaultFileLogger(verbose, f))
	} else {
		ctx = logging.ToContext(ctx, logging.NoOpLogger())
	}
	p := tea.NewProgram(model{
		ctx:       ctx,
		vmClient:  vmClient,
		namesapce: namespace,
		name:      name,
		help:      help.New(),
	}, tea.WithContext(ctx), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		return err
	}
	return nil
}

func (m model) Init() tea.Cmd {
	return func() tea.Msg {
		instanceList, err := m.vmClient.List(m.ctx, m.namesapce, m.name)
		if err != nil {
			logging.FromContext(m.ctx).Error("Unable to list instances", "error", err)
		}
		logging.FromContext(m.ctx).Info("Listed VMs", "vms", len(instanceList))
		return listMsg{instances: instanceList}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		// If we set a width on the help menu it can gracefully truncate
		// its view as needed.
		m.help.Width = msg.Width
		m.width = msg.Width
		m.height = msg.Height

	case listMsg:
		m.table = instancesToTable(msg.instances)
		m.instances = msg.instances

	case updatedMsg:
		return m, nil

	// Is it a key press?
	case tea.KeyMsg:

		// Cool, what was the actual key pressed?
		switch msg.String() {

		// Terminate
		case "t":
			return m, func() tea.Msg {
				selectedInstance := m.instances[m.table.Cursor()]
				deletionPlan, err := m.vmClient.DeletionPlan(m.ctx, selectedInstance.Namespace(), selectedInstance.Name())
				if err != nil {
					logging.FromContext(m.ctx).Error("Unable to construct deletion plan", "error", err)
					return nil
				}
				deletionPlan, err = m.vmClient.Delete(m.ctx, deletionPlan)
				if err != nil {
					logging.FromContext(m.ctx).Error("Unable to execute deletion plan", "error", err)
					return nil
				}
				return updatedMsg{}
			}

		case "?":
			m.help.ShowAll = !m.help.ShowAll

		case "q", "ctrl+c":
			return m, tea.Quit

		}
	}

	m.table, cmd = m.table.Update(msg)

	return m, cmd
}

func (m model) View() string {
	tableView := m.table.View()
	helpView := m.help.View(keys)

	if m.height == 0 {
		return ""
	}
	// height beween rendered models to position help at the bottom
	height := m.height - strings.Count(tableView, "\n") - strings.Count(helpView, "\n") - 1

	return tableView + strings.Repeat("\n", height) + helpView
}

func instancesToTable(instanceList []instances.Instance) table.Model {
	t := table.New()
	prettyInstances := lo.FilterMap(instanceList, func(instance instances.Instance, _ int) (instances.PrettyInstance, bool) {
		return instance.Prettify(), true
	})
	headers, rows := pretty.HeadersAndRows(prettyInstances, false)
	t.SetColumns(lo.Map(headers, func(header string, _ int) table.Column {
		return table.Column{Title: header, Width: 20}
	}))
	t.SetRows(lo.Map(rows, func(row []string, _ int) table.Row { return row }))
	t.Focus()
	return t
}
