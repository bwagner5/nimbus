package list

import (
	"context"
	"strings"

	"github.com/bwagner5/nimbus/pkg/logging"
	"github.com/bwagner5/nimbus/pkg/pretty"
	"github.com/bwagner5/nimbus/pkg/providers/instances"
	"github.com/bwagner5/nimbus/pkg/tui/launch"
	"github.com/bwagner5/nimbus/pkg/vm"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/samber/lo"
)

type ListModel struct {
	ctx       context.Context
	vmClient  vm.VMI
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

// type ListModel struct {
// 	table.Model
// }

func NewList(ctx context.Context, vmClient vm.VMI, namespace, name string) *ListModel {
	return &ListModel{
		ctx:       ctx,
		vmClient:  vmClient,
		namesapce: namespace,
		name:      name,
		help:      help.New(),
	}
}

func (m ListModel) Init() tea.Cmd {
	return func() tea.Msg {
		instanceList, err := m.vmClient.List(m.ctx, m.namesapce, m.name)
		if err != nil {
			logging.FromContext(m.ctx).Error("Unable to list instances", "error", err)
		}
		logging.FromContext(m.ctx).Info("Listed VMs", "vms", len(instanceList))
		return listMsg{instances: instanceList}
	}
}

func (m ListModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		// Launch
		case "l":
			return launch.NewLaunch(m.ctx, m.vmClient, m, m.width, m.height), nil

		case "?":
			m.help.ShowAll = !m.help.ShowAll

		case "q", "ctrl+c":
			return m, tea.Quit

		}
	}

	m.table, cmd = m.table.Update(msg)

	return m, cmd
}

func (m ListModel) View() string {
	tableView := m.table.View()
	helpView := m.help.View(keys)

	if m.height == 0 {
		return ""
	}
	// height between rendered models to position help at the bottom
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
