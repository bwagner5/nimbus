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
	progress     *utils.StepProgress
	result       *CreateAppMsg
	loaded       bool
	instances    []applications.Target
	gotAccount   bool
	gotInstances bool
	errors       []string
	width        int
	// stored from wizard for multi-step create
	appName string
	envName string
	target  string
	// intermediate state for chained target steps
	targetKeyID    string
	targetSecret   string
	bucketCreated  bool
}

// InstancesMsg carries available instances for target selection.
type InstancesMsg struct {
	Instances []applications.Target
	Err       error
}

// internal step messages
type createBucketDoneMsg struct{ Err error }
type tagTargetDoneMsg struct{ Err error }
type createKeyDoneMsg struct {
	Err    error
	KeyID  string
	Secret string
}
type writeCredsDoneMsg struct{ Err error }
type uploadBinaryDoneMsg struct{ Err error }
type installWatchDoneMsg struct{ Err error }

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
		if (s.progress != nil && s.progress.Failed()) || (s.result != nil) {
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

	// Step: create bucket
	case createBucketDoneMsg:
		if msg.Err != nil {
			s.progress.Fail(0, msg.Err)
			s.creating = false
			return s, nil
		}
		s.bucketCreated = true
		s.progress.Complete(0)
		if s.target != "" && s.target != "skip" {
			s.progress.Start(1)
			s.progress.StartSub(1, 0)
			return s, s.doTagTarget()
		}
		// No target — done
		s.result = &CreateAppMsg{Name: s.appName}
		return s, nil

	// Sub-step: tag target
	case tagTargetDoneMsg:
		if msg.Err != nil {
			s.progress.FailSub(1, 0, msg.Err)
			s.creating = false
			return s, nil
		}
		s.progress.CompleteSub(1, 0)
		s.progress.StartSub(1, 1)
		return s, s.doCreateKey()

	// Sub-step: create access key
	case createKeyDoneMsg:
		if msg.Err != nil {
			s.progress.FailSub(1, 1, msg.Err)
			s.creating = false
			return s, nil
		}
		s.targetKeyID = msg.KeyID
		s.targetSecret = msg.Secret
		s.progress.CompleteSub(1, 1)
		s.progress.StartSub(1, 2)
		return s, s.doWriteCreds()

	// Sub-step: write credentials
	case writeCredsDoneMsg:
		if msg.Err != nil {
			s.progress.FailSub(1, 2, msg.Err)
			s.creating = false
			return s, nil
		}
		s.progress.CompleteSub(1, 2)
		s.progress.StartSub(1, 3)
		return s, s.doUploadBinary()

	// Sub-step: upload binary
	case uploadBinaryDoneMsg:
		if msg.Err != nil {
			s.progress.FailSub(1, 3, msg.Err)
			s.creating = false
			return s, nil
		}
		s.progress.CompleteSub(1, 3)
		s.progress.StartSub(1, 4)
		return s, s.doInstallWatch()

	// Sub-step: install watch service
	case installWatchDoneMsg:
		s.creating = false
		if msg.Err != nil {
			s.progress.FailSub(1, 4, msg.Err)
			return s, nil
		}
		s.progress.CompleteSub(1, 4)
		s.progress.Complete(1)
		s.result = &CreateAppMsg{Name: s.appName}
		return s, nil
	}

	if !s.loaded || s.creating {
		return s, nil
	}
	if s.result != nil || (s.progress != nil && s.progress.Failed()) {
		return s, nil
	}

	var cmd tea.Cmd
	s.wizard, cmd = s.wizard.Update(msg)

	if s.wizard.IsCompleted() {
		return s, s.startCreate()
	}

	return s, cmd
}

func (s *CreateScreen) startCreate() tea.Cmd {
	s.creating = true
	vals := s.wizard.Values()
	s.appName = vals["name"]
	s.envName = vals["env"]
	s.target = vals["target"]

	target := s.target
	if target == "skip" {
		target = ""
	}
	labels, targetSubs := applications.CreateSteps(s.appName, s.envName, target)
	s.progress = utils.NewStepProgress("Creating Application", labels...)
	if len(targetSubs) > 0 {
		s.progress.SetSubSteps(1, targetSubs...)
	}
	s.progress.Start(0)

	client := s.client
	accountID := s.accountID
	appName := s.appName
	envName := s.envName
	region := s.region
	ctx := s.ctx

	return func() tea.Msg {
		appClient := applications.NewClient(client)
		if err := appClient.CreateAppBucket(ctx, accountID, appName, region); err != nil {
			return createBucketDoneMsg{Err: err}
		}
		err := appClient.Create(ctx, accountID, appName, envName, region)
		return createBucketDoneMsg{Err: err}
	}
}

func (s *CreateScreen) doTagTarget() tea.Cmd {
	client, target, appName, envName, region, ctx := s.client, s.target, s.appName, s.envName, s.region, s.ctx
	return func() tea.Msg {
		err := applications.NewClient(client).TagTarget(ctx, target, appName, envName, region)
		return tagTargetDoneMsg{Err: err}
	}
}

func (s *CreateScreen) doCreateKey() tea.Cmd {
	client, appName, envName, accountID, region, ctx := s.client, s.appName, s.envName, s.accountID, s.region, s.ctx
	return func() tea.Msg {
		keyID, secret, err := applications.NewClient(client).CreateTargetKey(ctx, appName, envName, accountID, region)
		return createKeyDoneMsg{Err: err, KeyID: keyID, Secret: secret}
	}
}

func (s *CreateScreen) doWriteCreds() tea.Cmd {
	client := s.client
	target, appName, envName := s.target, s.appName, s.envName
	keyID, secret := s.targetKeyID, s.targetSecret
	accountID, region, ctx := s.accountID, s.region, s.ctx
	bucketName := applications.BucketName(accountID, appName, envName)
	return func() tea.Msg {
		err := applications.NewClient(client).WriteTargetCredentials(ctx, target, appName, envName, keyID, secret, bucketName, region)
		return writeCredsDoneMsg{Err: err}
	}
}

func (s *CreateScreen) doUploadBinary() tea.Cmd {
	client, target, region, ctx := s.client, s.target, s.region, s.ctx
	return func() tea.Msg {
		err := applications.NewClient(client).UploadBinary(ctx, target, region)
		return uploadBinaryDoneMsg{Err: err}
	}
}

func (s *CreateScreen) doInstallWatch() tea.Cmd {
	client, target, appName, envName, region, ctx := s.client, s.target, s.appName, s.envName, s.region, s.ctx
	return func() tea.Msg {
		err := applications.NewClient(client).RemoteUp(ctx, target, appName, envName, region)
		return installWatchDoneMsg{Err: err}
	}
}

func (s *CreateScreen) IsCancelled() bool {
	return s.wizard != nil && s.wizard.IsCancelled()
}
func (s *CreateScreen) IsComplete() bool { return s.result != nil && s.result.Err == nil }
func (s *CreateScreen) BucketCreated() bool { return s.bucketCreated }
func (s *CreateScreen) Errors() []string { return s.errors }
func (s *CreateScreen) ClearErrors()     { s.errors = nil }

// Progress returns the step progress for external rendering (needs spinner).
func (s *CreateScreen) Progress() *utils.StepProgress { return s.progress }
func (s *CreateScreen) Creating() bool                { return s.creating }
func (s *CreateScreen) AppName() string               { return s.appName }
func (s *CreateScreen) EnvName() string               { return s.envName }
func (s *CreateScreen) Region() string                { return s.region }

func (s *CreateScreen) View() string {
	return s.ViewWithSpinner("")
}

// ViewWithSpinner renders the screen, using the provided spinner frame for progress.
func (s *CreateScreen) ViewWithSpinner(spinnerView string) string {
	if !s.loaded {
		return utils.TitleStyle.Render(fmt.Sprintf(" %s Loading application options... ", spinnerView))
	}
	if s.progress != nil && (s.creating || s.progress.Failed() || s.progress.Done()) {
		if s.progress.Done() && s.result != nil && s.result.Err == nil {
			return s.progress.View(spinnerView, s.width) + "\n" +
				utils.RunningStyle.Render(fmt.Sprintf("  ✓ Application '%s' created!", s.result.Name))
		}
		return s.progress.View(spinnerView, s.width)
	}
	return s.wizard.View()
}

func (s *CreateScreen) SetSize(w, h int) {
	s.width = w
	if s.wizard != nil {
		s.wizard.SetSize(w, h)
	}
}
