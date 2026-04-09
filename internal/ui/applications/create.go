package applications

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// CreateScreen handles application creation.
type CreateScreen struct {
	wizard       *utils.Wizard
	client       *aws.Client
	region       string
	ctx          context.Context
	accountID    string
	creating     bool
	result       *CreateAppMsg
	loaded       bool
	instances    []applications.Target
	gotAccount   bool
	gotInstances bool
	errors       []string
}

// InstancesMsg carries available instances for target selection.
type InstancesMsg struct {
	Instances []applications.Target
	Err       error
}

func NewCreateScreen(client *aws.Client, region string, ctx context.Context) *CreateScreen {
	return &CreateScreen{client: client, region: region, ctx: ctx}
}

func (s *CreateScreen) Init() tea.Cmd {
	return tea.Batch(
		FetchAccountID(s.ctx, s.client),
		s.fetchInstances(),
	)
}

func (s *CreateScreen) fetchInstances() tea.Cmd {
	client := s.client
	region := s.region
	ctx := s.ctx
	return func() tea.Msg {
		targets, err := applications.NewClient(client).ListInstances(ctx, region)
		if err != nil {
			return InstancesMsg{Err: err}
		}
		return InstancesMsg{Instances: targets}
	}
}

func (s *CreateScreen) initWizard() {
	var targetOpts []utils.Option
	targetOpts = append(targetOpts, utils.Option{Value: "skip", Label: "Skip", Description: "No deployment target yet"})
	for _, inst := range s.instances {
		targetOpts = append(targetOpts, utils.Option{Value: inst.Name, Label: inst.Name, Description: inst.State})
	}

	steps := []utils.Step{
		{Key: "name", Title: "Application Name", Description: "A short name for your application (lowercase, no spaces)", Type: utils.StepText, Required: true},
		{Key: "env", Title: "Initial Environment", Description: "Name of the first environment (e.g. dev, staging, prod)", Type: utils.StepText, Required: true, DefaultValue: "dev"},
		{Key: "target", Title: "Deployment Target", Description: "Select a Lightsail instance to deploy to", Type: utils.StepSelect, Options: targetOpts},
	}
	s.wizard = utils.NewWizard("Create Application", steps)
}

func (s *CreateScreen) Update(msg tea.Msg) (*CreateScreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if s.result != nil {
			if msg.String() == "esc" || msg.String() == "ctrl+c" {
				s.wizard.SetCancelled()
				return s, nil
			}
		}
	case AccountIDMsg:
		s.gotAccount = true
		if msg.Err != nil {
			s.errors = append(s.errors, msg.Err.Error())
		} else {
			s.accountID = msg.AccountID
		}
		if s.gotInstances {
			s.initWizard()
			s.loaded = true
		}
	case InstancesMsg:
		s.gotInstances = true
		if msg.Err != nil {
			s.errors = append(s.errors, msg.Err.Error())
		} else {
			s.instances = msg.Instances
		}
		if s.gotAccount {
			s.initWizard()
			s.loaded = true
		}
	case CreateAppMsg:
		s.creating = false
		s.result = &msg
		return s, nil
	}

	if !s.loaded || s.creating {
		return s, nil
	}
	if s.result != nil {
		return s, nil
	}

	var cmd tea.Cmd
	s.wizard, cmd = s.wizard.Update(msg)

	if s.wizard.IsCompleted() {
		return s, s.createApp()
	}

	return s, cmd
}

func (s *CreateScreen) createApp() tea.Cmd {
	s.creating = true
	vals := s.wizard.Values()
	appName := vals["name"]
	envName := vals["env"]
	target := vals["target"]

	client := s.client
	accountID := s.accountID
	region := s.region
	ctx := s.ctx

	return func() tea.Msg {
		appClient := applications.NewClient(client)
		if err := appClient.Create(ctx, accountID, appName, region); err != nil {
			return CreateAppMsg{Err: err, Name: appName}
		}
		if target != "" && target != "skip" {
			appClient.AddTarget(ctx, target, appName, envName, region)
		}
		return CreateAppMsg{Name: appName}
	}
}

func (s *CreateScreen) IsCancelled() bool { return s.wizard != nil && s.wizard.IsCancelled() }
func (s *CreateScreen) IsComplete() bool  { return s.result != nil && s.result.Err == nil }
func (s *CreateScreen) Errors() []string  { return s.errors }
func (s *CreateScreen) ClearErrors()      { s.errors = nil }

func (s *CreateScreen) View() string {
	if !s.loaded {
		return utils.TitleStyle.Render(" Loading application options... ")
	}
	if s.creating {
		return utils.TitleStyle.Render(" Creating application... ")
	}
	if s.result != nil {
		if s.result.Err == nil {
			return utils.RunningStyle.Render(fmt.Sprintf("✓ Application '%s' created!", s.result.Name))
		}
		return utils.ErrorStyle.Render("✗ Error: "+s.result.Err.Error()) + "\n\n" + utils.HelpStyle.Render("  Press esc to go back")
	}
	return s.wizard.View()
}

func (s *CreateScreen) SetSize(w, h int) {
	if s.wizard != nil {
		s.wizard.SetSize(w, h)
	}
}
