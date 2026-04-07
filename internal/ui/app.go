package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/resources"
	"github.com/wagnerbm/nimbusv2/internal/trace"
	"github.com/wagnerbm/nimbusv2/internal/ui/instances"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

type view int

const (
	viewResources view = iota
	viewRegions
	viewProviders
	viewFilter
	viewCreate
	viewConfirm
)

// Model is the top-level application model.
type Model struct {
	client          *aws.Client
	providers       []resources.Provider
	providerIdx     int
	resources       []resources.Resource
	filtered        []resources.Resource
	cursor          int
	region          string
	regions         []string
	regCursor       int
	provCursor      int
	view            view
	width           int
	height          int
	err             error
	loading         bool
	progress        string
	filter          string
	ctx             context.Context
	cancel          context.CancelFunc
	createScreen    *instances.CreateScreen
	toast           utils.Toast
	confirm         *instances.ConfirmAction
	pollRegion      string
	refreshGen      int
	refreshing      bool
	lastRefreshDone time.Time
	spinner         spinner.Model
	trace           *trace.Logger
}

func (v view) String() string {
	switch v {
	case viewResources:
		return "resources"
	case viewRegions:
		return "regions"
	case viewProviders:
		return "providers"
	case viewFilter:
		return "filter"
	case viewCreate:
		return "create"
	case viewConfirm:
		return "confirm"
	default:
		return fmt.Sprintf("unknown(%d)", int(v))
	}
}

func NewModel(logger *trace.Logger) Model {
	ctx, cancel := context.WithCancel(context.Background())
	client, _ := aws.NewClient(ctx, "us-east-1")
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	return Model{
		client: client,
		providers: []resources.Provider{
			resources.NewLightsailProvider(client),
			resources.NewLightsailDatabaseProvider(client),
			resources.NewLightsailLoadBalancerProvider(client),
			resources.NewLightsailBucketProvider(client),
			resources.NewLightsailContainerProvider(client),
			resources.NewLightsailDistributionProvider(client),
		},
		region:  "global",
		loading: true,
		ctx:     ctx,
		cancel:  cancel,
		spinner: s,
		trace:   logger,
	}
}

func (m Model) provider() resources.Provider { return m.providers[m.providerIdx] }

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		instances.FetchResources(m.ctx, m.client, m.provider(), m.region),
		instances.FetchRegions(m.ctx, m.client),
		instances.ScheduleRefresh(m.refreshGen, m.pollRegion),
	)
}

// --- Update: message routing ---

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.trace.Log("msg=WindowSize width=%d height=%d", msg.Width, msg.Height)
		m.width, m.height = msg.Width, msg.Height
		if m.createScreen != nil {
			m.createScreen.SetSize(m.width, m.height)
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case tea.KeyPressMsg:
		m.trace.Log("msg=KeyMsg key=%q view=%s", msg.String(), m.view)
		return m.handleKey(msg)

	// Resource data
	case instances.ResourcesMsg:
		m.trace.Log("msg=ResourcesMsg count=%d region=%q", len(msg.Resources), msg.Region)
		m.resources = instances.MergeResources(m.resources, msg.Resources, msg.Region)
		m.loading, m.err, m.progress = false, nil, ""
		m.refreshing = false
		m.lastRefreshDone = time.Now()
		m.pollRegion = instances.CheckPollSettled(m.resources, m.pollRegion)
		m.applyFilter()
		m.refreshGen++
		return m, instances.ScheduleRefresh(m.refreshGen, m.pollRegion)

	case instances.RegionsMsg:
		m.trace.Log("msg=RegionsMsg count=%d", len(msg))
		m.regions = msg

	case instances.StreamRegionsCmd:
		m.trace.Log("msg=StreamRegionsCmd idx=%d total=%d", msg.Idx, len(msg.Regions))
		return m, msg.Fetch

	case instances.PartialMsg:
		m.trace.Log("msg=PartialMsg completed=%d/%d done=%v", msg.Completed, msg.Total, msg.Done)
		if m.ctx.Err() != nil {
			m.loading, m.progress, m.refreshing = false, "", false
			return m, nil
		}
		if !msg.Done {
			m.resources = instances.MergeResources(m.resources, msg.Resources, "")
			m.applyFilter()
			m.progress = instances.FormatProgress(msg.Completed, msg.Total)
			if msg.Next != nil && msg.Completed < msg.Total {
				return m, msg.Next.Fetch
			}
		}
		if msg.Done || msg.Completed >= msg.Total {
			m.loading, m.progress, m.refreshing = false, "", false
			m.lastRefreshDone = time.Now()
		}

	// Create flow
	case instances.CreateResult:
		m.trace.Log("msg=CreateResult success=%v", msg.Success)
		if m.createScreen != nil {
			m.createScreen, _ = m.createScreen.Update(msg)
			if m.createScreen.IsComplete() {
				return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return instances.CreateDoneMsg{} })
			}
		}

	case instances.CreateDoneMsg:
		m.trace.Log("msg=CreateDoneMsg view=%s screenNil=%v", m.view, m.createScreen == nil)
		if m.view == viewCreate && m.createScreen != nil && m.createScreen.IsComplete() {
			m.view = viewResources
			m.createScreen = nil
			m.loading = true
			m.resources = nil
			return m, instances.FetchResources(m.ctx, m.client, m.provider(), m.region)
		}

	case instances.BundlesMsg, instances.BlueprintsMsg:
		m.trace.Log("msg=BundlesOrBlueprints type=%T screenNil=%v", msg, m.createScreen == nil)
		if m.createScreen != nil {
			m.createScreen, _ = m.createScreen.Update(msg)
			if errs := m.createScreen.Errors(); len(errs) > 0 {
				m.toast = utils.NewToast(errs)
				m.createScreen.ClearErrors()
				return m, utils.ScheduleToastExpiry()
			}
		}

	// Actions
	case instances.ActionResultMsg:
		m.trace.Log("msg=ActionResult err=%v msg=%q region=%q", msg.Err, msg.Msg, msg.Region)
		if msg.Err != nil {
			m.toast = utils.NewToast([]string{msg.Err.Error()})
		} else {
			m.toast = utils.NewToast([]string{msg.Msg})
			m.pollRegion = msg.Region
		}
		m.refreshGen++
		return m, tea.Batch(
			instances.FetchRegionOnly(m.ctx, m.provider(), msg.Region),
			instances.ScheduleRefresh(m.refreshGen, m.pollRegion),
			utils.ScheduleToastExpiry(),
		)

	// SSH
	case instances.SSHCredentialsMsg:
		m.trace.Log("msg=SSHCredentials err=%v user=%q ip=%q", msg.Err, msg.Username, msg.IP)
		if msg.Err != nil {
			m.toast = utils.NewToast([]string{msg.Err.Error()})
			return m, utils.ScheduleToastExpiry()
		}
		return m, instances.SSHExecCmd(msg)

	case instances.SSHExitMsg:
		m.trace.Log("msg=SSHExit err=%v", msg.Err)
		if msg.Err != nil {
			m.toast = utils.NewToast([]string{msg.Err.Error()})
			return m, utils.ScheduleToastExpiry()
		}

	// Refresh tick
	case instances.RefreshTickMsg:
		m.trace.Log("msg=RefreshTick gen=%d current=%d refreshing=%v pollRegion=%q", msg.Gen, m.refreshGen, m.refreshing, m.pollRegion)
		return m.handleRefreshTick(msg)

	// Toast / errors
	case utils.ToastExpireMsg:
		m.toast = utils.Toast{}

	case instances.ErrMsg:
		m.trace.Log("msg=ErrMsg err=%v", msg.Error())
		m.toast = utils.NewToast([]string{msg.Error()})
		m.loading, m.progress, m.refreshing = false, "", false
		return m, utils.ScheduleToastExpiry()
	}
	return m, nil
}

func (m Model) handleRefreshTick(msg instances.RefreshTickMsg) (tea.Model, tea.Cmd) {
	if msg.Gen != m.refreshGen {
		return m, nil
	}
	if m.refreshing {
		m.refreshGen++
		return m, instances.ScheduleRefresh(m.refreshGen, m.pollRegion)
	}
	if since := time.Since(m.lastRefreshDone); since < instances.RefreshCooldown {
		m.refreshGen++
		gen := m.refreshGen
		return m, tea.Tick(instances.RefreshCooldown-since, func(time.Time) tea.Msg {
			return instances.RefreshTickMsg{Gen: gen}
		})
	}
	if m.view == viewCreate {
		m.refreshGen++
		return m, instances.ScheduleRefresh(m.refreshGen, m.pollRegion)
	}
	m.refreshing = true
	if m.pollRegion != "" {
		return m, instances.FetchRegionOnly(m.ctx, m.provider(), m.pollRegion)
	}
	return m, instances.FetchResources(m.ctx, m.client, m.provider(), m.region)
}

// --- Key handling ---

func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Delegate to active sub-screen
	if m.view == viewCreate && m.createScreen != nil {
		var cmd tea.Cmd
		m.createScreen, cmd = m.createScreen.Update(msg)
		if m.createScreen.IsCancelled() || m.createScreen.IsComplete() {
			m.view = viewResources
			m.createScreen = nil
			m.loading = true
			m.resources = nil
			return m, instances.FetchResources(m.ctx, m.client, m.provider(), m.region)
		}
		return m, cmd
	}

	if m.view == viewConfirm && m.confirm != nil {
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("y"))):
			action := *m.confirm
			m.confirm = nil
			m.view = viewResources
			return m, instances.ExecuteAction(m.ctx, m.client, action)
		case key.Matches(msg, key.NewBinding(key.WithKeys("n", "esc"))):
			m.confirm = nil
			m.view = viewResources
		}
		return m, nil
	}

	if m.view == viewFilter {
		return m.handleFilterKey(msg)
	}

	// Global + resource-view keys
	switch {
	case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
		m.cancel()
		return m, tea.Quit

	case key.Matches(msg, key.NewBinding(key.WithKeys("/"))):
		if m.view == viewResources {
			m.view = viewFilter
			m.filter = ""
		}

	case key.Matches(msg, key.NewBinding(key.WithKeys(":"))):
		if m.view == viewResources {
			m.view = viewProviders
		}

	case key.Matches(msg, key.NewBinding(key.WithKeys("r"))):
		if m.view == viewResources {
			m.view = viewRegions
		}

	case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
		m.view = viewResources
		m.filter = ""
		m.applyFilter()

	case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
		return m.handleEnterKey()

	case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
		m.moveCursor(1)

	case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
		m.moveCursor(-1)

	case key.Matches(msg, key.NewBinding(key.WithKeys("R"))):
		m.cancel()
		m.ctx, m.cancel = context.WithCancel(context.Background())
		m.loading, m.refreshing = true, true
		m.resources = nil
		m.filter = ""
		return m, instances.FetchResources(m.ctx, m.client, m.provider(), m.region)

	// Instance-specific keys
	case key.Matches(msg, key.NewBinding(key.WithKeys("c"))):
		return m.handleCreateKey()

	case key.Matches(msg, key.NewBinding(key.WithKeys("s"))):
		return m.handleStopStartKey()

	case key.Matches(msg, key.NewBinding(key.WithKeys("d"))):
		return m.handleDeleteKey()

	case key.Matches(msg, key.NewBinding(key.WithKeys("x"))):
		return m.handleShellKey()
	}
	return m, nil
}

func (m Model) handleFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEscape:
		m.view = viewResources
		m.filter = ""
		m.applyFilter()
	case tea.KeyEnter:
		m.view = viewResources
	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			m.filter = m.filter[:len(m.filter)-1]
			m.applyFilter()
		}
	default:
		if len(msg.Text) > 0 {
			m.filter += msg.Text
			m.applyFilter()
		}
	}
	return m, nil
}

func (m Model) handleEnterKey() (tea.Model, tea.Cmd) {
	if m.view == viewRegions {
		m.cancel()
		m.ctx, m.cancel = context.WithCancel(context.Background())
		m.region = m.regions[m.regCursor]
		m.view = viewResources
		m.loading = true
		m.resources = nil
		m.filter = ""
		return m, instances.FetchResources(m.ctx, m.client, m.provider(), m.region)
	}
	if m.view == viewProviders {
		m.cancel()
		m.ctx, m.cancel = context.WithCancel(context.Background())
		m.providerIdx = m.provCursor
		m.cursor = 0
		m.view = viewResources
		m.loading = true
		m.resources = nil
		m.filter = ""
		return m, instances.FetchResources(m.ctx, m.client, m.provider(), m.region)
	}
	return m, nil
}

func (m *Model) moveCursor(dir int) {
	switch m.view {
	case viewResources:
		m.cursor += dir
		if m.cursor < 0 {
			m.cursor = 0
		}
		if m.cursor >= len(m.filtered) {
			m.cursor = max(0, len(m.filtered)-1)
		}
	case viewRegions:
		m.regCursor += dir
		if m.regCursor < 0 {
			m.regCursor = 0
		}
		if m.regCursor >= len(m.regions) {
			m.regCursor = len(m.regions) - 1
		}
	case viewProviders:
		m.provCursor += dir
		if m.provCursor < 0 {
			m.provCursor = 0
		}
		if m.provCursor >= len(m.providers) {
			m.provCursor = len(m.providers) - 1
		}
	}
}

func (m Model) handleCreateKey() (tea.Model, tea.Cmd) {
	if m.view != viewResources || m.provider().Kind() != "lightsail/instances" {
		m.trace.Log("handleCreateKey: skipped view=%s provider=%s", m.view, m.provider().Kind())
		return m, nil
	}
	region := m.region
	if region == "global" {
		region = m.client.DefaultRegion()
	}
	m.trace.Log("handleCreateKey: creating screen region=%s", region)
	m.createScreen = instances.NewCreateScreen(m.client, region, context.Background())
	m.createScreen.SetSize(m.width, m.height)
	m.view = viewCreate
	return m, m.createScreen.Init()
}

func (m Model) handleStopStartKey() (tea.Model, tea.Cmd) {
	if m.view != viewResources || m.provider().Kind() != "lightsail/instances" || len(m.filtered) == 0 {
		return m, nil
	}
	r := m.filtered[m.cursor]
	kind := instances.ActionStop
	if r.Status() == "stopped" {
		kind = instances.ActionStart
	}
	m.confirm = &instances.ConfirmAction{Kind: kind, Name: r.Name(), Region: r.Region()}
	m.view = viewConfirm
	return m, nil
}

func (m Model) handleDeleteKey() (tea.Model, tea.Cmd) {
	if m.view != viewResources || m.provider().Kind() != "lightsail/instances" || len(m.filtered) == 0 {
		return m, nil
	}
	r := m.filtered[m.cursor]
	m.confirm = &instances.ConfirmAction{Kind: instances.ActionDelete, Name: r.Name(), Region: r.Region()}
	m.view = viewConfirm
	return m, nil
}

func (m Model) handleShellKey() (tea.Model, tea.Cmd) {
	if m.view != viewResources || m.provider().Kind() != "lightsail/instances" || len(m.filtered) == 0 {
		return m, nil
	}
	r := m.filtered[m.cursor]
	if r.Status() == "running" {
		return m, instances.FetchSSHCredentials(m.ctx, m.client, r.Name(), r.Region())
	}
	return m, nil
}

// --- Helpers ---

func (m *Model) applyFilter() {
	m.filtered = instances.ApplyFilter(m.resources, m.filter)
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
}

// --- View ---

func (m Model) View() tea.View {
	var screen string
	if m.view == viewCreate && m.createScreen != nil {
		content := m.createScreen.View()
		m.trace.Log("View: create screen len(content)=%d width=%d height=%d", len(content), m.width, m.height)
		modal := utils.ModalStyle.Width(70).Render(content)
		m.trace.Log("View: modal len=%d lines=%d", len(modal), strings.Count(modal, "\n")+1)
		base := instances.RenderResources(m.provider(), m.filtered, m.cursor, m.region, m.filter, m.progress, m.spinner.View(), m.width, m.height, m.loading, m.view == viewFilter)
		screen = utils.Overlay(base, content, m.width, m.height)
		screen = utils.CenterModal(modal, m.width, m.height)
	} else {
		base := instances.RenderResources(m.provider(), m.filtered, m.cursor, m.region, m.filter, m.progress, m.spinner.View(), m.width, m.height, m.loading, m.view == viewFilter)
		switch m.view {
		case viewRegions:
			screen = utils.Overlay(base, instances.RenderRegionModal(m.regions, m.regCursor), m.width, m.height)
		case viewProviders:
			screen = utils.Overlay(base, instances.RenderProviderModal(m.providers, m.provCursor), m.width, m.height)
		case viewConfirm:
			screen = utils.Overlay(base, instances.RenderConfirmModal(m.confirm), m.width, m.height)
		default:
			screen = base
		}
	}
	if m.toast.Active() {
		lines := strings.Split(screen, "\n")
		toast := m.toast.View(m.width)
		if len(lines) > 0 {
			lines[0] = toast
		}
		screen = strings.Join(lines, "\n")
	}
	m.trace.Log("View: output lines=%d len=%d", strings.Count(screen, "\n")+1, len(screen))

	// Clamp output to terminal dimensions
	if m.height > 0 {
		lines := strings.Split(screen, "\n")
		if len(lines) > m.height {
			m.trace.Log("View: WARN clamped %d lines to %d", len(lines), m.height)
			lines = lines[:m.height]
			screen = strings.Join(lines, "\n")
		}
	}

	v := tea.NewView(screen)
	v.AltScreen = true
	return v
}
