package instances

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"charm.land/bubbles/v2/filepicker"
	tea "charm.land/bubbletea/v2"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// CreateResult is returned when creation completes.
type CreateResult struct {
	Success bool
	Message string
	Err     error
}

// CreateDoneMsg signals auto-return after successful creation.
type CreateDoneMsg struct{}

// EditorResultMsg is returned when the external editor exits.
type EditorResultMsg struct{ Err error }

// CreateScreen handles Lightsail instance creation.
type CreateScreen struct {
	wizard        *utils.Wizard
	client        *aws.Client
	region        string
	ctx           context.Context
	creating      bool
	result        *CreateResult
	bundles       []types.Bundle
	blueprints    []types.Blueprint
	loaded        bool
	errors        []string
	gotBundles    bool
	gotBlueprints bool
	filePicker    *filepicker.Model
	editorTmp     string // temp file path for editor
}

// BundlesMsg carries fetched bundle data.
type BundlesMsg struct {
	Bundles []types.Bundle
	Err     error
}

// BlueprintsMsg carries fetched blueprint data.
type BlueprintsMsg struct {
	Blueprints []types.Blueprint
	Err        error
}

func NewCreateScreen(client *aws.Client, region string, ctx context.Context) *CreateScreen {
	return &CreateScreen{
		client: client,
		region: region,
		ctx:    ctx,
	}
}

func (s *CreateScreen) Init() tea.Cmd {
	return tea.Batch(s.fetchBundles(), s.fetchBlueprints())
}

func (s *CreateScreen) fetchBundles() tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(s.client.WithRegion(s.region).Config())
		out, err := svc.GetBundles(s.ctx, &lightsail.GetBundlesInput{IncludeInactive: utils.BoolPtr(false)})
		if err != nil {
			return BundlesMsg{Err: fmt.Errorf("fetch bundles: %w", err)}
		}
		return BundlesMsg{Bundles: out.Bundles}
	}
}

func (s *CreateScreen) fetchBlueprints() tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(s.client.WithRegion(s.region).Config())
		out, err := svc.GetBlueprints(s.ctx, &lightsail.GetBlueprintsInput{IncludeInactive: utils.BoolPtr(false)})
		if err != nil {
			return BlueprintsMsg{Err: fmt.Errorf("fetch blueprints: %w", err)}
		}
		return BlueprintsMsg{Blueprints: out.Blueprints}
	}
}

func (s *CreateScreen) initWizard() {
	defaultName := RandomName()
	steps := []utils.Step{
		{Key: "name", Title: "Instance Name", Description: "Enter a unique name for your instance", Type: utils.StepText, Required: false, DefaultValue: defaultName},
		{Key: "platform", Title: "Platform", Description: "Select the operating system platform", Type: utils.StepSelect, Options: []utils.Option{
			{Value: "linux", Label: "Linux/Unix", Description: "Amazon Linux, Ubuntu, Debian, etc."},
			{Value: "windows", Label: "Windows", Description: "Windows Server"},
		}},
		{Key: "image_type", Title: "Image Type", Description: "Choose between a pre-configured app or base OS", Type: utils.StepSelect, Options: []utils.Option{
			{Value: "os", Label: "OS Only", Description: "Clean operating system installation"},
			{Value: "app", Label: "App + OS", Description: "Pre-configured application blueprint"},
		}},
		{Key: "blueprint", Title: "Blueprint", Description: "Select the image for your instance", Type: utils.StepSelect, Options: s.blueprintOptions("linux", "os")},
		{Key: "networking", Title: "Networking", Description: "Choose IP address configuration", Type: utils.StepSelect, Options: []utils.Option{
			{Value: "dualstack", Label: "Dual-stack", Description: "IPv4 + IPv6 addresses"},
			{Value: "ipv6", Label: "IPv6 only", Description: "IPv6 address only"},
		}},
		{Key: "bundle", Title: "Instance Size", Description: "Select compute and memory configuration", Type: utils.StepSelect, Options: s.bundleOptions("linux", "dualstack")},
		{Key: "script", Title: "Launch Script", Description: "Optional startup script to run on first boot", Type: utils.StepScriptSource, Optional: true, Options: []utils.Option{
			{Value: "skip", Label: "Skip", Description: "No launch script"},
			{Value: "file", Label: "Pick a file", Description: "Select an existing script from disk"},
			{Value: "editor", Label: "Write in editor", Description: "Open $EDITOR to write a script"},
		}},
		{Key: "ssh_key", Title: "SSH Key", Description: "Add a public SSH key to the instance", Type: utils.StepScriptSource, Optional: true, Options: []utils.Option{
			{Value: "skip", Label: "Skip", Description: "Can still SSH with dynamic keys"},
			{Value: "file", Label: "Pick a file", Description: "Select a public key from disk"},
		}},
		{Key: "snapshots", Title: "Automatic Snapshots", Description: "Enable daily automatic backups", Type: utils.StepSelect, Options: []utils.Option{
			{Value: "enabled", Label: "Enabled", Description: "Daily snapshots", Price: "+$0.05/GB/mo"},
			{Value: "disabled", Label: "Disabled", Description: "No automatic backups"},
		}},
	}
	s.wizard = utils.NewWizard("Create Lightsail Instance", steps)
}

func (s *CreateScreen) updateBlueprintOptions() {
	vals := s.wizard.Values()
	platform := vals["platform"]
	imageType := vals["image_type"]
	if platform == "" {
		platform = "linux"
	}
	if imageType == "" {
		imageType = "os"
	}
	for i := range s.wizard.Steps {
		if s.wizard.Steps[i].Key == "blueprint" {
			s.wizard.Steps[i].Options = s.blueprintOptions(platform, imageType)
			s.wizard.Steps[i].ResetSelected()
			break
		}
	}
}

func (s *CreateScreen) updateBundleOptions() {
	vals := s.wizard.Values()
	platform := vals["platform"]
	networking := vals["networking"]
	if platform == "" {
		platform = "linux"
	}
	if networking == "" {
		networking = "dualstack"
	}
	for i := range s.wizard.Steps {
		if s.wizard.Steps[i].Key == "bundle" {
			s.wizard.Steps[i].Options = s.bundleOptions(platform, networking)
			s.wizard.Steps[i].ResetSelected()
			break
		}
	}
}

func (s *CreateScreen) bundleOptions(platform, networking string) []utils.Option {
	var opts []utils.Option

	wantPlatform := "LINUX_UNIX"
	if platform == "windows" {
		wantPlatform = "WINDOWS"
	}
	wantIPv6Only := networking == "ipv6"

	sorted := make([]types.Bundle, len(s.bundles))
	copy(sorted, s.bundles)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Price != nil && sorted[j].Price != nil && *sorted[i].Price < *sorted[j].Price
	})

	for _, b := range sorted {
		if b.BundleId == nil || b.Price == nil || b.IsActive == nil || !*b.IsActive {
			continue
		}
		platformMatch := false
		for _, p := range b.SupportedPlatforms {
			if string(p) == wantPlatform {
				platformMatch = true
				break
			}
		}
		if !platformMatch {
			continue
		}
		id := *b.BundleId
		hasIPv4 := b.PublicIpv4AddressCount != nil && *b.PublicIpv4AddressCount > 0
		if wantIPv6Only == hasIPv4 {
			continue
		}

		cpu := int32(1)
		if b.CpuCount != nil {
			cpu = *b.CpuCount
		}
		ram := float32(0.5)
		if b.RamSizeInGb != nil {
			ram = *b.RamSizeInGb
		}
		disk := int32(20)
		if b.DiskSizeInGb != nil {
			disk = *b.DiskSizeInGb
		}

		desc := fmt.Sprintf("%d vCPU, %.0fGB RAM, %dGB SSD", cpu, ram, disk)
		price := fmt.Sprintf("$%.2f/mo", *b.Price)

		opts = append(opts, utils.Option{
			Value:       id,
			Label:       formatBundleName(b),
			Description: desc,
			Price:       price,
		})
	}

	if len(opts) == 0 {
		return []utils.Option{
			{Value: "micro_3_0", Label: "Micro", Description: "1 vCPU, 1GB RAM, 40GB SSD", Price: "$5/mo"},
			{Value: "small_3_0", Label: "Small", Description: "1 vCPU, 2GB RAM, 60GB SSD", Price: "$10/mo"},
			{Value: "medium_3_0", Label: "Medium", Description: "2 vCPU, 4GB RAM, 80GB SSD", Price: "$20/mo"},
			{Value: "large_3_0", Label: "Large", Description: "2 vCPU, 8GB RAM, 160GB SSD", Price: "$40/mo"},
		}
	}
	return opts
}

func formatBundleName(b types.Bundle) string {
	name := ""
	if b.Name != nil {
		name = *b.Name
	} else {
		name = *b.BundleId
	}
	id := *b.BundleId
	switch {
	case strings.HasPrefix(id, "c_"):
		return name + " (Compute)"
	case strings.HasPrefix(id, "m_"):
		return name + " (Memory)"
	default:
		return name
	}
}

func (s *CreateScreen) blueprintOptions(platform, imageType string) []utils.Option {
	var opts []utils.Option

	for _, b := range s.blueprints {
		if b.BlueprintId == nil || b.IsActive == nil || !*b.IsActive {
			continue
		}
		if b.Platform != "" {
			bPlatform := strings.ToLower(string(b.Platform))
			if platform == "windows" && bPlatform != "windows" {
				continue
			}
			if platform == "linux" && bPlatform == "windows" {
				continue
			}
		}
		if b.Type != "" {
			bType := strings.ToLower(string(b.Type))
			if imageType == "os" && bType != "os" {
				continue
			}
			if imageType == "app" && bType != "app" {
				continue
			}
		}

		name := *b.BlueprintId
		if b.Name != nil {
			name = *b.Name
		}
		desc := ""
		if b.Description != nil {
			desc = *b.Description
			if len(desc) > 40 {
				desc = desc[:37] + "..."
			}
		}
		opts = append(opts, utils.Option{
			Value:       *b.BlueprintId,
			Label:       name,
			Description: desc,
		})
	}

	if len(opts) == 0 {
		if platform == "windows" {
			return []utils.Option{
				{Value: "windows_server_2022", Label: "Windows Server 2022", Description: "Latest Windows Server"},
				{Value: "windows_server_2019", Label: "Windows Server 2019", Description: "Windows Server"},
			}
		}
		return []utils.Option{
			{Value: "amazon_linux_2023", Label: "Amazon Linux 2023", Description: "AWS optimized Linux"},
			{Value: "ubuntu_22_04", Label: "Ubuntu 22.04 LTS", Description: "Popular Linux distribution"},
			{Value: "debian_12", Label: "Debian 12", Description: "Stable Linux distribution"},
		}
	}
	return opts
}

func (s *CreateScreen) Update(msg tea.Msg) (*CreateScreen, tea.Cmd) {
	// Handle file picker sub-state
	if s.filePicker != nil {
		return s.updateFilePicker(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if s.result != nil && !s.result.Success {
			if msg.String() == "esc" || msg.String() == "ctrl+c" {
				s.wizard.SetCancelled()
				return s, nil
			}
		}
	case EditorResultMsg:
		if msg.Err == nil && s.editorTmp != "" {
			data, err := os.ReadFile(s.editorTmp)
			if err == nil && len(data) > 0 {
				s.wizard.SetCurrentValue(string(data))
			}
			os.Remove(s.editorTmp)
		}
		s.editorTmp = ""
		s.wizard.AdvanceStep()
		return s, nil
	case BundlesMsg:
		s.gotBundles = true
		if msg.Err != nil {
			s.errors = append(s.errors, msg.Err.Error())
		} else {
			s.bundles = msg.Bundles
		}
		if s.gotBlueprints {
			s.initWizard()
			s.loaded = true
		}
	case BlueprintsMsg:
		s.gotBlueprints = true
		if msg.Err != nil {
			s.errors = append(s.errors, msg.Err.Error())
		} else {
			s.blueprints = msg.Blueprints
		}
		if s.gotBundles {
			s.initWizard()
			s.loaded = true
		}
	case CreateResult:
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

	prevStep := s.wizard.CurrentIndex()
	var cmd tea.Cmd
	s.wizard, cmd = s.wizard.Update(msg)

	if s.wizard.CurrentIndex() == 3 && prevStep != 3 {
		s.updateBlueprintOptions()
	}
	if s.wizard.CurrentIndex() == 5 && prevStep != 5 {
		s.updateBundleOptions()
	}

	// Check if user just selected a script/file source option
	if step := s.wizard.Current(); step != nil && step.Type == utils.StepScriptSource && step.Value != "" {
		switch step.Value {
		case "skip":
			step.Value = ""
			s.wizard.AdvanceStep()
			return s, nil
		case "file":
			step.Value = ""
			return s, s.openFilePicker()
		case "editor":
			step.Value = ""
			return s, s.openEditor()
		}
	}

	// Auto-open file picker for StepFilePicker steps
	if step := s.wizard.Current(); step != nil && step.Type == utils.StepFilePicker && s.filePicker == nil && s.wizard.CurrentIndex() != prevStep {
		return s, s.openFilePicker()
	}

	if s.wizard.IsCompleted() {
		return s, s.createInstance()
	}

	return s, cmd
}

func (s *CreateScreen) updateFilePicker(msg tea.Msg) (*CreateScreen, tea.Cmd) {
	if kmsg, ok := msg.(tea.KeyPressMsg); ok && kmsg.String() == "esc" {
		s.filePicker = nil
		return s, nil
	}
	fp, cmd := s.filePicker.Update(msg)
	s.filePicker = &fp
	if didSelect, path := s.filePicker.DidSelectFile(msg); didSelect {
		step := s.wizard.Current()
		if step != nil && step.Key == "script" {
			data, err := os.ReadFile(path)
			if err == nil {
				s.wizard.SetCurrentValue(string(data))
			}
		} else {
			s.wizard.SetCurrentValue(path)
		}
		s.filePicker = nil
		s.wizard.AdvanceStep()
		return s, nil
	}
	return s, cmd
}

func (s *CreateScreen) openFilePicker() tea.Cmd {
	fp := filepicker.New()
	fp.CurrentDirectory, _ = os.Getwd()
	fp.AutoHeight = false
	fp.SetHeight(15)
	s.filePicker = &fp
	return s.filePicker.Init()
}

func (s *CreateScreen) openEditor() tea.Cmd {
	tmpFile, err := os.CreateTemp("", "nimbus-script-*.sh")
	if err != nil {
		return nil
	}
	tmpFile.Close()
	s.editorTmp = tmpFile.Name()

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	c := exec.Command(editor, s.editorTmp)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return EditorResultMsg{Err: err}
	})
}

func (s *CreateScreen) createInstance() tea.Cmd {
	s.creating = true
	vals := s.wizard.Values()

	return func() tea.Msg {
		svc := lightsail.NewFromConfig(s.client.WithRegion(s.region).Config())

		input := &lightsail.CreateInstancesInput{
			InstanceNames:    []string{vals["name"]},
			AvailabilityZone: utils.StrPtr(s.region + "a"),
			BundleId:         utils.StrPtr(vals["bundle"]),
			BlueprintId:      utils.StrPtr(vals["blueprint"]),
		}

		if vals["script"] != "" {
			input.UserData = utils.StrPtr(vals["script"])
		}

		if vals["networking"] == "ipv6" {
			input.IpAddressType = types.IpAddressType("ipv6")
		} else {
			input.IpAddressType = types.IpAddressTypeDualstack
		}

		_, err := svc.CreateInstances(s.ctx, input)
		if err != nil {
			return CreateResult{Success: false, Err: err}
		}

		if vals["snapshots"] == "enabled" {
			svc.EnableAddOn(s.ctx, &lightsail.EnableAddOnInput{
				ResourceName: utils.StrPtr(vals["name"]),
				AddOnRequest: &types.AddOnRequest{
					AddOnType: types.AddOnTypeAutoSnapshot,
					AutoSnapshotAddOnRequest: &types.AutoSnapshotAddOnRequest{
						SnapshotTimeOfDay: utils.StrPtr("06:00"),
					},
				},
			})
		}

		return CreateResult{Success: true, Message: fmt.Sprintf("Instance '%s' created successfully!", vals["name"])}
	}
}

func (s *CreateScreen) IsCancelled() bool {
	return s.wizard != nil && s.wizard.IsCancelled()
}

func (s *CreateScreen) IsComplete() bool {
	return s.result != nil && s.result.Success
}

func (s *CreateScreen) Errors() []string {
	return s.errors
}

func (s *CreateScreen) ClearErrors() {
	s.errors = nil
}

// Region returns the region this create screen targets.
func (s *CreateScreen) Region() string {
	return s.region
}

func (s *CreateScreen) View() string {
	if !s.loaded {
		return utils.TitleStyle.Render(" Loading instance options... ")
	}
	if s.creating {
		return utils.TitleStyle.Render(" Creating instance... ")
	}
	if s.result != nil {
		if s.result.Success {
			return utils.RunningStyle.Render("✓ " + s.result.Message)
		}
		return utils.ErrorStyle.Render("✗ Error: "+s.result.Err.Error()) + "\n\n" + utils.HelpStyle.Render("  Press esc to go back")
	}
	if s.filePicker != nil {
		return utils.TitleStyle.Render("  Select Script File") + "\n\n" + s.filePicker.View() + "\n\n" + utils.HelpStyle.Render("  enter:select  esc:cancel")
	}
	if s.editorTmp != "" {
		return utils.TitleStyle.Render(" Opening editor... ")
	}
	return s.wizard.View()
}

func (s *CreateScreen) SetSize(w, h int) {
	if s.wizard != nil {
		s.wizard.SetSize(w, h)
	}
}
