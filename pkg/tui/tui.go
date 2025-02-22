package tui

import (
	"context"
	"fmt"

	"github.com/bwagner5/nimbus/pkg/logging"
	"github.com/bwagner5/nimbus/pkg/pretty"
	"github.com/bwagner5/nimbus/pkg/providers/instances"
	"github.com/bwagner5/nimbus/pkg/vm"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/samber/lo"
)

type model struct {
	ctx       context.Context
	vmClient  vm.AWSVM
	namesapce string
	name      string
	table     table.Model
}

type ListMsg struct {
	instances []instances.Instance
}

type ListModel struct {
	table.Model
}

func Launch(ctx context.Context, vmClient vm.AWSVM, namespace, name string) error {
	p := tea.NewProgram(model{
		ctx:       ctx,
		vmClient:  vmClient,
		namesapce: namespace,
		name:      name,
	}, tea.WithContext(ctx))
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		return err
	}
	return nil
}

func (m model) Init() tea.Cmd {
	return func() tea.Msg {
		instances, err := m.vmClient.List(context.TODO(), m.namesapce, m.name)
		if err != nil {
			logging.FromContext(m.ctx).Error("Unable to list instances", "error", err)
		}
		return ListMsg{instances: instances}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {

	case ListMsg:
		m.table = instancesToTable(msg.instances)
	// Is it a key press?
	case tea.KeyMsg:

		// Cool, what was the actual key pressed?
		switch msg.String() {

		case "q", "ctrl+c":
			return m, tea.Quit

		}
	}

	m.table, cmd = m.table.Update(msg)

	return m, cmd
}

func (m model) View() string {
	return m.table.View() + "\n"
}

func instancesToTable(instanceList []instances.Instance) table.Model {
	t := table.New()
	prettyInstances := lo.FilterMap(instanceList, func(instance instances.Instance, _ int) (instances.PretyInstance, bool) {
		return instance.Prettify(), true
	})
	headers, rows := pretty.HeadersAndRow(prettyInstances, false)
	t.SetColumns(lo.Map(headers, func(header string, _ int) table.Column {
		return table.Column{Title: header}
	}))
	t.SetRows(lo.Map(rows, func(row []string, _ int) table.Row { return row }))
	return t
}
