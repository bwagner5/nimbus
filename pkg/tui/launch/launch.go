package launch

import (
	"context"

	"github.com/bwagner5/nimbus/pkg/vm"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

type launchModel struct {
	ctx       context.Context
	vmClient  vm.VMI
	backModel tea.Model
	form      *huh.Form
	width     int
	height    int
}

func NewLaunch(ctx context.Context, vmClient vm.VMI, backModel tea.Model, width, height int) launchModel {
	return launchModel{
		ctx:       ctx,
		vmClient:  vmClient,
		backModel: backModel,
		form: huh.NewForm(
			huh.NewGroup(
				huh.NewInput().Title("Name"),
				huh.NewInput().Title("Namespace"),
				huh.NewSelect[string]().
					Options(huh.NewOptions("Spot", "On-Demand")...).
					Title("Choose a Capacity Type"),
			).WithHide(false).Title("Launch Instance"),
		).WithWidth(width).WithHeight(height),
	}
}

func (m launchModel) Init() tea.Cmd {
	return m.form.Init()
}

func (m launchModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		// This is only triggered if its the first model or on a resize
		m.width = msg.Width
		m.height = msg.Height
		m.form = m.form.WithWidth(msg.Width).WithHeight(msg.Height)

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Interrupt
		case "q":
			return m, tea.Quit
		case "esc":
			return m.backModel, nil
		}
	}

	form, cmd := m.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		m.form = f

	}

	if m.form.State == huh.StateCompleted {
		return m.backModel, nil
	}
	return m, cmd
}

func (m launchModel) View() string {
	return m.form.View()
}
