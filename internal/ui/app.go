package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/resources"
	"github.com/wagnerbm/nimbusv2/internal/trace"
	appsdk "github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/ui/applications"
	"github.com/wagnerbm/nimbusv2/internal/ui/instances"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

type appCreateDoneMsg struct{}
type deleteProgressDoneMsg struct{}
type view int

const (
	viewResources view = iota
	viewRegions
	viewProviders
	viewFilter
	viewCreate
	viewConfirm
	viewDetail
	viewAddTarget
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
	detail          *types.Instance
	metrics         *instances.MetricsData
	metricsLoading  bool
	metricRange     int // index into MetricRanges
	detailName      string
	detailRegion    string
	detailVP        viewport.Model
	appCreateScreen *applications.CreateScreen
	appDetail       *appsdk.Detail
	appDetailCursor int
	appDetailFrom   bool // true when viewing instance detail navigated from app detail
	appConfirmName  string
	appConfirmReg   string
	disassocConfirm *applications.TargetEntry // pending disassociate confirmation
	addTargetEnv     string                   // env name for add-target modal
	addTargetInsts   []appsdk.Target          // instances available in add-target modal
	addTargetCursor  int
	addTargetLoading bool
	addTargetStatus  string
	accountID        string                   // cached AWS account ID
	deleteProgress   *utils.StepProgress      // step progress for app deletion
	deleteAppName    string
	deleteAppReg     string
	deletedApps     map[string]time.Time      // name -> expiry
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
	case viewDetail:
		return "detail"
	case viewAddTarget:
		return "addTarget"
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
			resources.NewLightsailApplicationProvider(client),
		},
		region:     "global",
		loading:    true,
		refreshing: true,
		ctx:        ctx,
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
		if m.view == viewDetail {
			m.detailVP.SetWidth(m.width)
			m.detailVP.SetHeight(m.height - 1)
			m.updateDetailContent()
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
			m.pollRegion = m.createScreen.Region()
			m.view = viewResources
			m.createScreen = nil
			m.refreshGen++
			return m, tea.Batch(
				instances.FetchRegionOnly(m.ctx, m.provider(), m.pollRegion),
				instances.ScheduleRefresh(m.refreshGen, m.pollRegion),
			)
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

	case instances.EditorResultMsg:
		m.trace.Log("msg=EditorResult err=%v", msg.Err)
		if m.createScreen != nil {
			m.createScreen, _ = m.createScreen.Update(msg)
		}

	// Detail
	case instances.InstanceDetailMsg:
		m.trace.Log("msg=InstanceDetail err=%v", msg.Err)
		if msg.Err != nil {
			m.toast = utils.NewToast([]string{msg.Err.Error()})
			m.view = viewResources
			return m, utils.ScheduleToastExpiry()
		}
		m.detail = msg.Instance
		m.updateDetailContent()

	case instances.MetricsMsg:
		m.trace.Log("msg=MetricsMsg err=%v", msg.Err)
		if msg.Err == nil {
			m.metrics = msg.Data
			m.metricsLoading = false
			m.updateDetailContent()
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

	// Application messages
	case applications.AccountIDMsg:
		if msg.Err == nil {
			m.accountID = msg.AccountID
		}
		if m.appCreateScreen != nil {
			m.appCreateScreen, _ = m.appCreateScreen.Update(msg)
			if errs := m.appCreateScreen.Errors(); len(errs) > 0 {
				m.toast = utils.NewToast(errs)
				m.appCreateScreen.ClearErrors()
				return m, utils.ScheduleToastExpiry()
			}
		}

	case applications.InstancesMsg:
		if m.view == viewAddTarget {
			m.addTargetLoading = false
			if msg.Err != nil {
				m.toast = utils.NewToast([]string{msg.Err.Error()})
				m.view = viewDetail
				return m, utils.ScheduleToastExpiry()
			}
			m.addTargetInsts = msg.Instances
			return m, nil
		}
		if m.appCreateScreen != nil {
			m.appCreateScreen, _ = m.appCreateScreen.Update(msg)
			if errs := m.appCreateScreen.Errors(); len(errs) > 0 {
				m.toast = utils.NewToast(errs)
				m.appCreateScreen.ClearErrors()
				return m, utils.ScheduleToastExpiry()
			}
		}

	case applications.CreateAppMsg:
		m.trace.Log("msg=CreateAppMsg err=%v", msg.Err)
		if m.appCreateScreen != nil {
			m.appCreateScreen, _ = m.appCreateScreen.Update(msg)
			if m.appCreateScreen.IsComplete() {
				return m, tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return appCreateDoneMsg{} })
			}
			if msg.Err != nil {
				m.toast = utils.NewToast([]string{msg.Err.Error()})
				return m, utils.ScheduleToastExpiry()
			}
		}

	case appCreateDoneMsg:
		if m.view == viewCreate && m.appCreateScreen != nil && m.appCreateScreen.IsComplete() {
			m.view = viewResources
			m.appCreateScreen = nil
			m.refreshGen++
			return m, tea.Batch(
				instances.FetchResources(m.ctx, m.client, m.provider(), m.region),
				instances.ScheduleRefresh(m.refreshGen, m.pollRegion),
			)
		}

	case deleteProgressDoneMsg:
		m.deleteProgress = nil

	case applications.AppDetailMsg:
		m.trace.Log("msg=AppDetail err=%v", msg.Err)
		if msg.Err != nil {
			m.toast = utils.NewToast([]string{msg.Err.Error()})
			m.view = viewResources
			return m, utils.ScheduleToastExpiry()
		}
		m.appDetail = msg.Detail
		targets := applications.AppDetailTargets(m.appDetail)
		if m.appDetailCursor >= len(targets) {
			m.appDetailCursor = max(0, len(targets)-1)
		}
		m.updateDetailContent()

	case applications.CleanupInstancesDoneMsg:
		if m.deleteProgress != nil {
			if msg.Err != nil {
				m.deleteProgress.Fail(0, msg.Err)
				return m, nil
			}
			m.deleteProgress.Complete(0)
			m.deleteProgress.Start(1)
			return m, applications.DeleteAppTags(m.ctx, m.client, m.deleteAppName, m.deleteAppReg)
		}

	case applications.DeleteTagsDoneMsg:
		if m.deleteProgress != nil {
			if msg.Err != nil {
				m.deleteProgress.Fail(1, msg.Err)
				return m, nil
			}
			m.deleteProgress.Complete(1)
			m.deleteProgress.Start(2)
			return m, applications.DeleteAppBuckets(m.ctx, m.client, m.deleteAppName, m.deleteAppReg)
		}

	case applications.DeleteAppMsg:
		if m.deleteProgress != nil {
			if msg.Err != nil {
				m.deleteProgress.Fail(2, msg.Err)
				return m, nil
			}
			m.deleteProgress.Complete(2)
		}
		m.progress = ""
		if msg.Err != nil {
			m.toast = utils.NewToast([]string{msg.Err.Error()})
			return m, utils.ScheduleToastExpiry()
		}
		if m.deletedApps == nil {
			m.deletedApps = make(map[string]time.Time)
		}
		m.deletedApps[msg.Name] = time.Now().Add(60 * time.Second)
		m.applyFilter()
		m.refreshGen++
		// Auto-dismiss after a short delay
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return deleteProgressDoneMsg{} })

	case applications.DisassociateTargetMsg:
		if msg.Err != nil {
			m.toast = utils.NewToast([]string{msg.Err.Error()})
			return m, utils.ScheduleToastExpiry()
		}
		m.toast = utils.NewToast([]string{fmt.Sprintf("Disassociated '%s'", msg.InstanceName)})
		if m.appDetail != nil {
			return m, tea.Batch(
				applications.FetchAppDetail(m.ctx, m.client, m.appDetail.Name, m.appDetail.Bucket, m.appDetail.Region),
				utils.ScheduleToastExpiry(),
			)
		}
		return m, utils.ScheduleToastExpiry()

	case applications.AddTargetMsg:
		m.addTargetLoading = false
		if msg.Err != nil {
			m.toast = utils.NewToast([]string{msg.Err.Error()})
			return m, utils.ScheduleToastExpiry()
		}
		m.toast = utils.NewToast([]string{"Target added"})
		if m.appDetail != nil {
			return m, tea.Batch(
				applications.FetchAppDetail(m.ctx, m.client, m.appDetail.Name, m.appDetail.Bucket, m.appDetail.Region),
				utils.ScheduleToastExpiry(),
			)
		}
		return m, utils.ScheduleToastExpiry()

	// Toast / errors
	case utils.ToastExpireMsg:
		m.toast = utils.Toast{}

	case instances.ErrMsg:
		m.trace.Log("msg=ErrMsg err=%v", msg.Error())
		m.toast = utils.NewToast([]string{msg.Error()})
		m.loading, m.progress, m.refreshing = false, "", false
		return m, utils.ScheduleToastExpiry()
	}
	// Forward unhandled messages to active create screen (e.g. filepicker internal msgs)
	if m.view == viewCreate {
		if m.createScreen != nil {
			var cmd tea.Cmd
			m.createScreen, cmd = m.createScreen.Update(msg)
			return m, cmd
		}
		if m.appCreateScreen != nil {
			m.trace.Log("msg=appCreateForward type=%T", msg)
			bucketWasCreated := m.appCreateScreen.BucketCreated()
			var cmd tea.Cmd
			m.appCreateScreen, cmd = m.appCreateScreen.Update(msg)
			if m.appCreateScreen.IsComplete() {
				m.trace.Log("msg=appCreateComplete")
				return m, tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg { return appCreateDoneMsg{} })
			}
			if m.appCreateScreen.Progress() != nil && m.appCreateScreen.Progress().Failed() {
				m.trace.Log("msg=appCreateFailed err=%v", m.appCreateScreen.Progress().Error())
			}
			// Trigger a background refresh the moment the bucket is created
			if !bucketWasCreated && m.appCreateScreen.BucketCreated() {
				m.refreshGen++
				return m, tea.Batch(cmd, instances.FetchResources(m.ctx, m.client, m.provider(), m.region))
			}
			return m, cmd
		}
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
	if m.view == viewDetail && m.detailName != "" {
		// Refresh metrics periodically while viewing detail
		m.refreshGen++
		mr := instances.MetricRanges[m.metricRange]
		return m, tea.Batch(
			instances.FetchMetrics(m.ctx, m.client, m.detailName, m.detailRegion, mr),
			instances.ScheduleRefresh(m.refreshGen, m.pollRegion),
		)
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
			if m.createScreen.IsComplete() {
				m.pollRegion = m.createScreen.Region()
			}
			m.view = viewResources
			m.createScreen = nil
			if m.pollRegion != "" {
				m.refreshGen++
				return m, tea.Batch(
					instances.FetchRegionOnly(m.ctx, m.provider(), m.pollRegion),
					instances.ScheduleRefresh(m.refreshGen, m.pollRegion),
				)
			}
			return m, nil
		}
		return m, cmd
	}

	if m.view == viewCreate && m.appCreateScreen != nil {
		var cmd tea.Cmd
		m.appCreateScreen, cmd = m.appCreateScreen.Update(msg)
		if m.appCreateScreen.IsCancelled() || m.appCreateScreen.IsComplete() {
			bucketCreated := m.appCreateScreen.BucketCreated()
			m.view = viewResources
			m.appCreateScreen = nil
			if bucketCreated {
				m.refreshGen++
				return m, tea.Batch(
					instances.FetchResources(m.ctx, m.client, m.provider(), m.region),
					instances.ScheduleRefresh(m.refreshGen, m.pollRegion),
				)
			}
			return m, nil
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

	if m.view == viewConfirm && m.appConfirmName != "" {
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("y"))):
			name, region := m.appConfirmName, m.appConfirmReg
			m.appConfirmName = ""
			m.deleteAppName = name
			m.deleteAppReg = region
			m.deleteProgress = utils.NewStepProgress(
				fmt.Sprintf("Deleting %s", name),
				appsdk.DeleteSteps(name)...,
			)
			m.deleteProgress.Start(0)
			m.refreshGen++
			m.view = viewResources
			return m, applications.CleanupAppInstances(m.ctx, m.client, name, region)
		case key.Matches(msg, key.NewBinding(key.WithKeys("n", "esc"))):
			m.appConfirmName = ""
			m.view = viewResources
		}
		return m, nil
	}

	if m.view == viewConfirm && m.disassocConfirm != nil {
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("y"))):
			t := *m.disassocConfirm
			m.disassocConfirm = nil
			m.view = viewDetail
			return m, applications.DisassociateTarget(m.ctx, m.client, t.Target.Name, m.appDetail.Name, t.EnvName, m.appDetail.Region)
		case key.Matches(msg, key.NewBinding(key.WithKeys("n", "esc"))):
			m.disassocConfirm = nil
			m.view = viewDetail
		}
		return m, nil
	}

	if m.view == viewAddTarget {
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			m.view = viewDetail
		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			if m.addTargetCursor < len(m.addTargetInsts)-1 {
				m.addTargetCursor++
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			if m.addTargetCursor > 0 {
				m.addTargetCursor--
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
			if len(m.addTargetInsts) > 0 {
				inst := m.addTargetInsts[m.addTargetCursor]
				m.view = viewDetail
				m.addTargetLoading = true
				m.addTargetStatus = fmt.Sprintf("Preparing %s...", inst.Name)
				return m, applications.AddTargetFromDetail(m.ctx, m.client, inst.Name, m.appDetail.Name, m.addTargetEnv, m.accountID, m.appDetail.Region)
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			m.cancel()
			return m, tea.Quit
		}
		return m, nil
	}

	if m.view == viewFilter {
		return m.handleFilterKey(msg)
	}

	// Detail view keys
	if m.view == viewDetail {
		name := m.detailName
		region := m.detailRegion

		// App detail view: target selection with j/k, enter, x, d
		if m.appDetail != nil && !m.appDetailFrom {
			targets := applications.AppDetailTargets(m.appDetail)
			cur := targets[m.appDetailCursor]
			switch {
			case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
				m.view = viewResources
				m.appDetail = nil
				return m, nil
			case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
				if m.appDetailCursor < len(targets)-1 {
					m.appDetailCursor++
				}
				m.updateDetailContent()
				return m, nil
			case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
				if m.appDetailCursor > 0 {
					m.appDetailCursor--
				}
				m.updateDetailContent()
				return m, nil
			case key.Matches(msg, key.NewBinding(key.WithKeys("enter"))):
				if cur.IsAddTarget {
					// Open add-target instance selection modal
					m.addTargetEnv = cur.EnvName
					m.addTargetCursor = 0
					m.addTargetInsts = nil
					m.addTargetLoading = true
					m.addTargetStatus = "Loading instances..."
					m.view = viewAddTarget
					cmds := []tea.Cmd{applications.FetchInstances(m.ctx, m.client, m.appDetail.Region)}
					if m.accountID == "" {
						cmds = append(cmds, applications.FetchAccountID(m.ctx, m.client))
					}
					return m, tea.Batch(cmds...)
				}
				// Navigate to instance detail
				m.appDetailFrom = true
				m.detail = nil
				m.metrics = nil
				m.detailName = cur.Target.Name
				m.detailRegion = cur.Target.Region
				m.detailVP = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(m.height-1))
				m.detailVP.KeyMap.HalfPageDown.SetEnabled(false)
				mr := instances.MetricRanges[m.metricRange]
				return m, tea.Batch(
					instances.FetchInstanceDetail(m.ctx, m.client, cur.Target.Name, cur.Target.Region),
					instances.FetchMetrics(m.ctx, m.client, cur.Target.Name, cur.Target.Region, mr),
				)
			case key.Matches(msg, key.NewBinding(key.WithKeys("x"))):
				if !cur.IsAddTarget && cur.Target.State == "running" {
					return m, instances.FetchSSHCredentials(m.ctx, m.client, cur.Target.Name, cur.Target.Region)
				}
			case key.Matches(msg, key.NewBinding(key.WithKeys("d"))):
				if !cur.IsAddTarget {
					m.disassocConfirm = &cur
					m.view = viewConfirm
					return m, nil
				}
			case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
				m.cancel()
				return m, tea.Quit
			default:
				var cmd tea.Cmd
				m.detailVP, cmd = m.detailVP.Update(msg)
				return m, cmd
			}
			return m, nil
		}

		// Instance detail view (possibly navigated from app detail)
		state := ""
		if m.detail != nil && m.detail.State != nil && m.detail.State.Name != nil {
			state = *m.detail.State.Name
		}
		switch {
		case key.Matches(msg, key.NewBinding(key.WithKeys("esc"))):
			if m.appDetailFrom {
				// Go back to app detail
				m.appDetailFrom = false
				m.detail = nil
				m.metrics = nil
				if m.appDetail == nil {
					m.view = viewResources
					return m, nil
				}
				m.detailName = m.appDetail.Name
				m.detailRegion = m.appDetail.Region
				m.detailVP = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(m.height-1))
				bucketName := m.appDetail.Bucket
				return m, applications.FetchAppDetail(m.ctx, m.client, m.appDetail.Name, bucketName, m.appDetail.Region)
			}
			m.view = viewResources
			m.detail = nil
			m.metrics = nil
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("["))):
			if m.metricRange > 0 {
				m.metricRange--
				m.metricsLoading = true
				m.updateDetailContent()
				mr := instances.MetricRanges[m.metricRange]
				return m, instances.FetchMetrics(m.ctx, m.client, name, region, mr)
			}
			m.toast = utils.NewToast([]string{"Already at shortest range"})
			return m, utils.ScheduleToastExpiry()
		case key.Matches(msg, key.NewBinding(key.WithKeys("]"))):
			if m.metricRange < len(instances.MetricRanges)-1 {
				m.metricRange++
				m.metricsLoading = true
				m.updateDetailContent()
				mr := instances.MetricRanges[m.metricRange]
				return m, instances.FetchMetrics(m.ctx, m.client, name, region, mr)
			}
			m.toast = utils.NewToast([]string{"Already at longest range"})
			return m, utils.ScheduleToastExpiry()
		case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
			var cmd tea.Cmd
			m.detailVP, cmd = m.detailVP.Update(msg)
			return m, cmd
		case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
			var cmd tea.Cmd
			m.detailVP, cmd = m.detailVP.Update(msg)
			return m, cmd
		case key.Matches(msg, key.NewBinding(key.WithKeys("s"))):
			if m.detail == nil {
				return m, nil
			}
			kind := instances.ActionStop
			if state == "stopped" {
				kind = instances.ActionStart
			}
			m.confirm = &instances.ConfirmAction{Kind: kind, Name: name, Region: region}
			m.view = viewConfirm
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("d"))):
			if m.detail == nil {
				return m, nil
			}
			m.confirm = &instances.ConfirmAction{Kind: instances.ActionDelete, Name: name, Region: region}
			m.view = viewConfirm
			return m, nil
		case key.Matches(msg, key.NewBinding(key.WithKeys("x"))):
			if state == "running" {
				return m, instances.FetchSSHCredentials(m.ctx, m.client, name, region)
			}
		case key.Matches(msg, key.NewBinding(key.WithKeys("q", "ctrl+c"))):
			m.cancel()
			return m, tea.Quit
		default:
			// Forward to viewport for scrolling (j/k/up/down/pgup/pgdn)
			m.trace.Log("detail: forwarding key=%q to viewport, yOffset=%d totalLines=%d height=%d",
				msg.String(), m.detailVP.YOffset(), m.detailVP.TotalLineCount(), m.detailVP.Height())
			var cmd tea.Cmd
			m.detailVP, cmd = m.detailVP.Update(msg)
			m.trace.Log("detail: after viewport update yOffset=%d", m.detailVP.YOffset())
			return m, cmd
		}
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
		if m.deleteProgress != nil && m.deleteProgress.Failed() {
			m.deleteProgress = nil
			return m, nil
		}
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
	if m.view == viewResources && len(m.filtered) > 0 {
		kind := m.provider().Kind()
		r := m.filtered[m.cursor]
		if kind == "lightsail/instances" {
			m.view = viewDetail
			m.detail = nil
			m.metrics = nil
			m.detailName = r.Name()
			m.detailRegion = r.Region()
			m.detailVP = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(m.height-1))
			m.detailVP.KeyMap.HalfPageDown.SetEnabled(false)
			mr := instances.MetricRanges[m.metricRange]
			return m, tea.Batch(
				instances.FetchInstanceDetail(m.ctx, m.client, r.Name(), r.Region()),
				instances.FetchMetrics(m.ctx, m.client, r.Name(), r.Region(), mr),
			)
		}
		if kind == "lightsail/applications" {
			m.view = viewDetail
			m.appDetail = nil
			m.appDetailCursor = 0
			m.detailName = r.Name()
			m.detailRegion = r.Region()
			m.detailVP = viewport.New(viewport.WithWidth(m.width), viewport.WithHeight(m.height-1))
			// Get bucket name from the resource
			bucketName := ""
			if app, ok := r.(resources.LightsailApplication); ok {
				bucketName = app.Bucket()
			}
			return m, applications.FetchAppDetail(m.ctx, m.client, r.Name(), bucketName, r.Region())
		}
	}
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
	if m.view != viewResources {
		return m, nil
	}
	kind := m.provider().Kind()
	region := m.region
	if region == "global" {
		region = m.client.DefaultRegion()
	}
	m.trace.Log("handleCreateKey: provider=%s region=%s", kind, region)
	switch kind {
	case "lightsail/instances":
		m.createScreen = instances.NewCreateScreen(m.client, region, context.Background())
		m.createScreen.SetSize(m.width, m.height)
		m.view = viewCreate
		return m, m.createScreen.Init()
	case "lightsail/applications":
		m.appCreateScreen = applications.NewCreateScreen(m.client, region, context.Background())
		m.appCreateScreen.SetSize(m.width, m.height)
		m.view = viewCreate
		return m, m.appCreateScreen.Init()
	}
	return m, nil
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
	if m.view != viewResources || len(m.filtered) == 0 {
		return m, nil
	}
	r := m.filtered[m.cursor]
	kind := m.provider().Kind()
	if kind == "lightsail/instances" {
		m.confirm = &instances.ConfirmAction{Kind: instances.ActionDelete, Name: r.Name(), Region: r.Region()}
		m.view = viewConfirm
	} else if kind == "lightsail/applications" {
		m.appConfirmName = r.Name()
		m.appConfirmReg = r.Region()
		m.view = viewConfirm
	}
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

func (m *Model) updateDetailContent() {
	var content string
	if m.appDetailFrom || (m.detail != nil && m.appDetail != nil) {
		// Viewing instance detail (possibly navigated from app detail)
		content = instances.RenderInstanceDetail(m.detail, m.metrics, m.metricsLoading, m.metricRange, m.width)
	} else if m.provider().Kind() == "lightsail/applications" && m.appDetail != nil {
		content = applications.RenderAppDetail(m.appDetail, m.appDetailCursor, m.width)
	} else if m.provider().Kind() == "lightsail/applications" {
		content = applications.RenderAppDetail(nil, 0, m.width)
	} else {
		content = instances.RenderInstanceDetail(m.detail, m.metrics, m.metricsLoading, m.metricRange, m.width)
	}
	m.detailVP.SetContent(content)
	m.trace.Log("updateDetailContent: contentLines=%d vpHeight=%d vpWidth=%d totalLines=%d",
		strings.Count(content, "\n")+1, m.detailVP.Height(), m.detailVP.Width(), m.detailVP.TotalLineCount())
}

func (m *Model) applyFilter() {
	m.filtered = instances.ApplyFilter(m.resources, m.filter)
	// Hide recently-deleted apps until the eventual consistency window passes
	if len(m.deletedApps) > 0 {
		now := time.Now()
		for name, expiry := range m.deletedApps {
			if now.After(expiry) {
				delete(m.deletedApps, name)
			}
		}
		if len(m.deletedApps) > 0 {
			var kept []resources.Resource
			for _, r := range m.filtered {
				if _, hidden := m.deletedApps[r.Name()]; !hidden {
					kept = append(kept, r)
				}
			}
			m.filtered = kept
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
}

// --- View ---

func (m Model) View() tea.View {
	var screen string
	kind := m.provider().Kind()

	// Render base list based on provider type
	renderBase := func() string {
		if kind == "lightsail/applications" {
			return applications.RenderApplications(m.provider(), m.filtered, m.cursor, m.region, m.filter, m.progress, m.spinner.View(), m.width, m.height, m.loading, m.view == viewFilter)
		}
		return instances.RenderResources(m.provider(), m.filtered, m.cursor, m.region, m.filter, m.progress, m.spinner.View(), m.width, m.height, m.loading, m.view == viewFilter)
	}

	if m.view == viewCreate {
		base := renderBase()
		if m.createScreen != nil {
			screen = utils.Overlay(base, m.createScreen.View(), m.width, m.height)
		} else if m.appCreateScreen != nil {
			screen = utils.Overlay(base, m.appCreateScreen.ViewWithSpinner(m.spinner.View()), m.width, m.height)
		} else {
			screen = base
		}
	} else if m.view == viewDetail || m.view == viewAddTarget {
		help := " esc:back  ↑↓:scroll  pgup/pgdn "
		if kind == "lightsail/instances" {
			help = " esc:back  s:stop/start  d:delete  x:shell  [/]:range  ↑↓:scroll  pgup/pgdn "
		} else if kind == "lightsail/applications" {
			if m.appDetail != nil && m.detail == nil {
				targets := applications.AppDetailTargets(m.appDetail)
				if len(targets) > 0 && !targets[m.appDetailCursor].IsAddTarget {
					help = " esc:back  j/k:select  enter:detail  x:ssh  d:disassociate "
				} else {
					help = " esc:back  j/k:select  enter:add/detail  x:ssh "
				}
			} else if m.appDetailFrom {
				help = " esc:app detail  s:stop/start  d:delete  x:shell  [/]:range  ↑↓:scroll "
			}
		}
		detailScreen := utils.RenderWithStatusBar(m.detailVP.View(), help, m.width, m.height)
		if m.view == viewAddTarget {
			modal := applications.RenderInstanceSelectModal(m.addTargetInsts, m.addTargetCursor)
			if m.addTargetLoading {
				modal = utils.TitleStyle.Render(fmt.Sprintf(" %s %s ", m.spinner.View(), m.addTargetStatus))
			}
			screen = utils.Overlay(detailScreen, modal, m.width, m.height)
		} else if m.addTargetLoading {
			// Show progress overlay while adding target
			modal := utils.TitleStyle.Render(fmt.Sprintf(" %s %s ", m.spinner.View(), m.addTargetStatus))
			screen = utils.Overlay(detailScreen, modal, m.width, m.height)
		} else {
			screen = detailScreen
		}
	} else {
		base := renderBase()
		switch m.view {
		case viewRegions:
			screen = utils.Overlay(base, instances.RenderRegionModal(m.regions, m.regCursor), m.width, m.height)
		case viewProviders:
			screen = utils.Overlay(base, instances.RenderProviderModal(m.providers, m.provCursor), m.width, m.height)
		case viewConfirm:
			if m.disassocConfirm != nil {
				base := utils.RenderWithStatusBar(m.detailVP.View(), "", m.width, m.height)
				screen = utils.Overlay(base, applications.RenderDisassociateConfirmModal(m.disassocConfirm.Target.Name, m.disassocConfirm.EnvName), m.width, m.height)
			} else if m.appConfirmName != "" {
				screen = utils.Overlay(renderBase(), applications.RenderAppConfirmModal(m.appConfirmName), m.width, m.height)
			} else {
				screen = utils.Overlay(renderBase(), instances.RenderConfirmModal(m.confirm), m.width, m.height)
			}
		default:
			if m.deleteProgress != nil {
				screen = utils.Overlay(base, m.deleteProgress.View(m.spinner.View(), m.width), m.width, m.height)
			} else {
				screen = base
			}
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
