package applications

import (
	"context"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lstypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/resources"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// CreateScreen handles application creation.
type CreateScreen struct {
	wizard      *utils.Wizard
	client      *aws.Client
	region      string
	ctx         context.Context
	accountID   string
	creating    bool
	result      *CreateAppMsg
	loaded      bool
	instances   []instanceOption
	gotAccount  bool
	gotInstances bool
	errors      []string
}

type instanceOption struct {
	Name   string
	Region string
	State  string
}

// InstancesMsg carries available instances for target selection.
type InstancesMsg struct {
	Instances []instanceOption
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
		svc := lightsail.NewFromConfig(client.WithRegion(region).Config())
		var opts []instanceOption
		var pageToken *string
		for {
			out, err := svc.GetInstances(ctx, &lightsail.GetInstancesInput{PageToken: pageToken})
			if err != nil {
				return InstancesMsg{Err: err}
			}
			for _, inst := range out.Instances {
				state := ""
				if inst.State != nil && inst.State.Name != nil {
					state = *inst.State.Name
				}
				opts = append(opts, instanceOption{
					Name:   *inst.Name,
					Region: region,
					State:  state,
				})
			}
			if out.NextPageToken == nil {
				break
			}
			pageToken = out.NextPageToken
		}
		return InstancesMsg{Instances: opts}
	}
}

func (s *CreateScreen) initWizard() {
	// Build instance options for target selection
	var targetOpts []utils.Option
	targetOpts = append(targetOpts, utils.Option{Value: "skip", Label: "Skip", Description: "No deployment target yet"})
	for _, inst := range s.instances {
		desc := inst.State
		targetOpts = append(targetOpts, utils.Option{Value: inst.Name, Label: inst.Name, Description: desc})
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
		// Create the bucket
		bucketName := fmt.Sprintf("%s%s-%s", resources.AppBucketPrefix, accountID, appName)
		svc := lightsail.NewFromConfig(client.WithRegion(region).Config())
		_, err := svc.CreateBucket(ctx, &lightsail.CreateBucketInput{
			BucketName: &bucketName,
			BundleId:   strPtr("small_1_0"),
		})
		if err != nil {
			return CreateAppMsg{Err: err, Name: appName}
		}

		// Tag instance as target if selected
		if target != "" && target != "skip" {
			tagKey := fmt.Sprintf("nimbus:app:%s:%s", appName, envName)
			tagVal := "true"
			svc.TagResource(ctx, &lightsail.TagResourceInput{
				ResourceName: &target,
				Tags:         []lstypes.Tag{{Key: &tagKey, Value: &tagVal}},
			})
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
