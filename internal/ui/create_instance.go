package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

// CreateResult is returned when creation completes
type CreateResult struct {
	Success bool
	Message string
	Err     error
}

// CreateInstanceScreen handles Lightsail instance creation
type CreateInstanceScreen struct {
	wizard     *Wizard
	client     *aws.Client
	region     string
	ctx        context.Context
	creating   bool
	result     *CreateResult
	bundles    []types.Bundle
	blueprints []types.Blueprint
	loaded     bool
	errors     []string
	gotBundles bool
	gotBlueprints bool
}

type bundlesMsg struct {
	bundles []types.Bundle
	err     error
}
type blueprintsMsg struct {
	blueprints []types.Blueprint
	err        error
}
type dataLoadedMsg struct{}

func NewCreateInstanceScreen(client *aws.Client, region string, ctx context.Context) *CreateInstanceScreen {
	return &CreateInstanceScreen{
		client: client,
		region: region,
		ctx:    ctx,
	}
}

func (s *CreateInstanceScreen) Init() tea.Cmd {
	return tea.Batch(s.fetchBundles(), s.fetchBlueprints())
}

func (s *CreateInstanceScreen) fetchBundles() tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(s.client.WithRegion(s.region).Config())
		out, err := svc.GetBundles(s.ctx, &lightsail.GetBundlesInput{IncludeInactive: boolPtr(false)})
		if err != nil {
			return bundlesMsg{err: fmt.Errorf("fetch bundles: %w", err)}
		}
		return bundlesMsg{bundles: out.Bundles}
	}
}

func (s *CreateInstanceScreen) fetchBlueprints() tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(s.client.WithRegion(s.region).Config())
		out, err := svc.GetBlueprints(s.ctx, &lightsail.GetBlueprintsInput{IncludeInactive: boolPtr(false)})
		if err != nil {
			return blueprintsMsg{err: fmt.Errorf("fetch blueprints: %w", err)}
		}
		return blueprintsMsg{blueprints: out.Blueprints}
	}
}

func (s *CreateInstanceScreen) initWizard() {
	defaultName := fmt.Sprintf("instance-%s", time.Now().Format("0102-1504"))
	steps := []Step{
		{Key: "name", Title: "Instance Name", Description: "Enter a unique name for your instance", Type: StepText, Required: false, DefaultValue: defaultName},
		{Key: "platform", Title: "Platform", Description: "Select the operating system platform", Type: StepSelect, Options: []Option{
			{Value: "linux", Label: "Linux/Unix", Description: "Amazon Linux, Ubuntu, Debian, etc."},
			{Value: "windows", Label: "Windows", Description: "Windows Server"},
		}},
		{Key: "image_type", Title: "Image Type", Description: "Choose between a pre-configured app or base OS", Type: StepSelect, Options: []Option{
			{Value: "os", Label: "OS Only", Description: "Clean operating system installation"},
			{Value: "app", Label: "App + OS", Description: "Pre-configured application blueprint"},
		}},
		{Key: "blueprint", Title: "Blueprint", Description: "Select the image for your instance", Type: StepSelect, Options: s.blueprintOptions("linux", "os")},
		{Key: "networking", Title: "Networking", Description: "Choose IP address configuration", Type: StepSelect, Options: []Option{
			{Value: "dualstack", Label: "Dual-stack", Description: "IPv4 + IPv6 addresses"},
			{Value: "ipv6", Label: "IPv6 only", Description: "IPv6 address only"},
		}},
		{Key: "bundle", Title: "Instance Size", Description: "Select compute and memory configuration", Type: StepSelect, Options: s.bundleOptions("linux", "dualstack")},
		{Key: "script", Title: "Launch Script", Description: "Optional startup script to run on first boot", Type: StepTextArea, Optional: true},
		{Key: "ssh_key", Title: "SSH Key Path", Description: "Optional path to public SSH key file", Type: StepFilePicker, Optional: true},
		{Key: "snapshots", Title: "Automatic Snapshots", Description: "Enable daily automatic backups", Type: StepSelect, Options: []Option{
			{Value: "enabled", Label: "Enabled", Description: "Daily snapshots", Price: "+$0.05/GB/mo"},
			{Value: "disabled", Label: "Disabled", Description: "No automatic backups"},
		}},
	}
	s.wizard = NewWizard("Create Lightsail Instance", steps)
}

func (s *CreateInstanceScreen) updateBlueprintOptions() {
	vals := s.wizard.Values()
	platform := vals["platform"]
	imageType := vals["image_type"]
	if platform == "" {
		platform = "linux"
	}
	if imageType == "" {
		imageType = "os"
	}
	// Find blueprint step and update options
	for i := range s.wizard.Steps {
		if s.wizard.Steps[i].Key == "blueprint" {
			s.wizard.Steps[i].Options = s.blueprintOptions(platform, imageType)
			s.wizard.Steps[i].selected = 0
			break
		}
	}
}

func (s *CreateInstanceScreen) updateBundleOptions() {
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
			s.wizard.Steps[i].selected = 0
			break
		}
	}
}

func (s *CreateInstanceScreen) bundleOptions(platform, networking string) []Option {
	var opts []Option

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
		// Filter by platform
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
		// Filter by networking: dual-stack has publicIpv4AddressCount > 0, ipv6-only has 0
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

		opts = append(opts, Option{
			Value:       id,
			Label:       formatBundleName(id),
			Description: desc,
			Price:       price,
		})
	}

	if len(opts) == 0 {
		return []Option{
			{Value: "micro_3_0", Label: "Micro", Description: "1 vCPU, 1GB RAM, 40GB SSD", Price: "$5/mo"},
			{Value: "small_3_0", Label: "Small", Description: "1 vCPU, 2GB RAM, 60GB SSD", Price: "$10/mo"},
			{Value: "medium_3_0", Label: "Medium", Description: "2 vCPU, 4GB RAM, 80GB SSD", Price: "$20/mo"},
			{Value: "large_3_0", Label: "Large", Description: "2 vCPU, 8GB RAM, 160GB SSD", Price: "$40/mo"},
		}
	}
	return opts
}

func formatBundleName(id string) string {
	parts := strings.Split(id, "_")
	if len(parts) > 0 {
		name := parts[0]
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return id
}

func (s *CreateInstanceScreen) blueprintOptions(platform, imageType string) []Option {
	var opts []Option

	for _, b := range s.blueprints {
		if b.BlueprintId == nil || b.IsActive == nil || !*b.IsActive {
			continue
		}
		// Filter by platform
		if b.Platform != "" {
			bPlatform := strings.ToLower(string(b.Platform))
			if platform == "windows" && bPlatform != "windows" {
				continue
			}
			if platform == "linux" && bPlatform == "windows" {
				continue
			}
		}
		// Filter by image type (os vs app)
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
		opts = append(opts, Option{
			Value:       *b.BlueprintId,
			Label:       name,
			Description: desc,
		})
	}

	if len(opts) == 0 {
		if platform == "windows" {
			return []Option{
				{Value: "windows_server_2022", Label: "Windows Server 2022", Description: "Latest Windows Server"},
				{Value: "windows_server_2019", Label: "Windows Server 2019", Description: "Windows Server"},
			}
		}
		return []Option{
			{Value: "amazon_linux_2023", Label: "Amazon Linux 2023", Description: "AWS optimized Linux"},
			{Value: "ubuntu_22_04", Label: "Ubuntu 22.04 LTS", Description: "Popular Linux distribution"},
			{Value: "debian_12", Label: "Debian 12", Description: "Stable Linux distribution"},
		}
	}
	return opts
}

func (s *CreateInstanceScreen) Update(msg tea.Msg) (*CreateInstanceScreen, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Allow escape when there's an error
		if s.result != nil && !s.result.Success {
			if msg.String() == "esc" || msg.String() == "ctrl+c" {
				s.wizard.cancelled = true
				return s, nil
			}
		}
	case bundlesMsg:
		s.gotBundles = true
		if msg.err != nil {
			s.errors = append(s.errors, msg.err.Error())
		} else {
			s.bundles = msg.bundles
		}
		if s.gotBlueprints {
			s.initWizard()
			s.loaded = true
		}
	case blueprintsMsg:
		s.gotBlueprints = true
		if msg.err != nil {
			s.errors = append(s.errors, msg.err.Error())
		} else {
			s.blueprints = msg.blueprints
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

	// Update blueprint options when moving to blueprint step
	if s.wizard.CurrentIndex() == 3 && prevStep != 3 {
		s.updateBlueprintOptions()
	}
	// Update bundle options when moving to bundle step
	if s.wizard.CurrentIndex() == 5 && prevStep != 5 {
		s.updateBundleOptions()
	}

	if s.wizard.IsCompleted() {
		return s, s.createInstance()
	}

	return s, cmd
}

func (s *CreateInstanceScreen) createInstance() tea.Cmd {
	s.creating = true
	vals := s.wizard.Values()

	return func() tea.Msg {
		svc := lightsail.NewFromConfig(s.client.WithRegion(s.region).Config())

		input := &lightsail.CreateInstancesInput{
			InstanceNames:    []string{vals["name"]},
			AvailabilityZone: strPtr(s.region + "a"),
			BundleId:         strPtr(vals["bundle"]),
			BlueprintId:      strPtr(vals["blueprint"]),
		}

		if vals["script"] != "" {
			input.UserData = strPtr(vals["script"])
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

		// Enable auto snapshots if requested
		if vals["snapshots"] == "enabled" {
			svc.EnableAddOn(s.ctx, &lightsail.EnableAddOnInput{
				ResourceName: strPtr(vals["name"]),
				AddOnRequest: &types.AddOnRequest{
					AddOnType:              types.AddOnTypeAutoSnapshot,
					AutoSnapshotAddOnRequest: &types.AutoSnapshotAddOnRequest{
						SnapshotTimeOfDay: strPtr("06:00"),
					},
				},
			})
		}

		return CreateResult{Success: true, Message: fmt.Sprintf("Instance '%s' created successfully!", vals["name"])}
	}
}

func (s *CreateInstanceScreen) IsCancelled() bool {
	return s.wizard != nil && s.wizard.IsCancelled()
}

func (s *CreateInstanceScreen) IsComplete() bool {
	return s.result != nil && s.result.Success
}

func (s *CreateInstanceScreen) Errors() []string {
	return s.errors
}

func (s *CreateInstanceScreen) ClearErrors() {
	s.errors = nil
}

func (s *CreateInstanceScreen) View() string {
	if !s.loaded {
		return TitleStyle.Render(" Loading instance options... ")
	}
	if s.creating {
		return TitleStyle.Render(" Creating instance... ")
	}
	if s.result != nil {
		if s.result.Success {
			return RunningStyle.Render("✓ " + s.result.Message)
		}
		return ErrorStyle.Render("✗ Error: " + s.result.Err.Error()) + "\n\n" + HelpStyle.Render("  Press esc to go back")
	}
	return s.wizard.View()
}

func (s *CreateInstanceScreen) SetSize(w, h int) {
	if s.wizard != nil {
		s.wizard.SetSize(w, h)
	}
}

func boolPtr(b bool) *bool    { return &b }
func strPtr(str string) *string { return &str }
