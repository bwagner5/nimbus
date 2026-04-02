package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/resources"
)

type view int

const (
	viewResources view = iota
	viewRegions
	viewProviders
	viewFilter
	viewCreate
)

type Model struct {
	client         *aws.Client
	providers      []resources.Provider
	providerIdx    int
	resources      []resources.Resource
	filtered       []resources.Resource
	cursor         int
	region         string
	regions        []string
	regCursor      int
	provCursor     int
	view           view
	width          int
	height         int
	err            error
	loading        bool
	progress       string
	filter         string
	ctx            context.Context
	cancel         context.CancelFunc
	createScreen   *CreateInstanceScreen
	toast          Toast
}

type resourcesMsg []resources.Resource
type regionsMsg []string
type partialMsg struct {
	resources []resources.Resource
	done      bool
	total     int
	completed int
}
type errMsg error

func NewModel() Model {
	ctx, cancel := context.WithCancel(context.Background())
	client, _ := aws.NewClient(ctx, "us-east-1")
	providers := []resources.Provider{
		resources.NewLightsailProvider(client),
		resources.NewLightsailDatabaseProvider(client),
		resources.NewLightsailLoadBalancerProvider(client),
		resources.NewLightsailBucketProvider(client),
		resources.NewLightsailContainerProvider(client),
		resources.NewLightsailDistributionProvider(client),
	}
	return Model{
		client:    client,
		providers: providers,
		region:    "global",
		loading:   true,
		ctx:       ctx,
		cancel:    cancel,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchResources(), m.fetchRegions())
}

func (m Model) fetchRegions() tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		regions, err := m.client.FetchRegionsSorted(ctx)
		if err != nil {
			return errMsg(err)
		}
		return regionsMsg(append([]string{"global"}, regions...))
	}
}

func (m Model) fetchResources() tea.Cmd {
	provider := m.providers[m.providerIdx]
	ctx := m.ctx
	if m.region == "global" {
		return m.fetchAllRegionsStreaming(provider)
	}
	return func() tea.Msg {
		res, err := provider.Fetch(ctx, m.region)
		if err != nil {
			return errMsg(err)
		}
		return resourcesMsg(res)
	}
}

func (m Model) fetchAllRegionsStreaming(provider resources.Provider) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		regions, err := m.client.FetchRegionsSorted(ctx)
		if err != nil {
			return errMsg(err)
		}
		return streamRegionsCmd{regions: regions, provider: provider, idx: 0, ctx: ctx}
	}
}

type streamRegionsCmd struct {
	regions  []string
	provider resources.Provider
	idx      int
	ctx      context.Context
}

func (s streamRegionsCmd) fetch() tea.Msg {
	if s.ctx.Err() != nil {
		return partialMsg{done: true, total: len(s.regions), completed: s.idx}
	}
	if s.idx >= len(s.regions) {
		return partialMsg{done: true, total: len(s.regions), completed: len(s.regions)}
	}
	res, _ := s.provider.Fetch(s.ctx, s.regions[s.idx])
	return partialMsg{resources: res, done: false, total: len(s.regions), completed: s.idx + 1}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.createScreen != nil {
			m.createScreen.SetSize(m.width, m.height)
		}
	case tea.KeyMsg:
		return m.handleKey(msg)
	case resourcesMsg:
		m.resources, m.loading, m.err, m.progress = msg, false, nil, ""
		m.applyFilter()
	case regionsMsg:
		m.regions = msg
	case streamRegionsCmd:
		return m, msg.fetch
	case partialMsg:
		if m.ctx.Err() != nil {
			m.loading, m.progress = false, ""
			return m, nil
		}
		if !msg.done {
			m.resources = append(m.resources, msg.resources...)
			m.applyFilter()
			m.progress = fmt.Sprintf("Loading... %d/%d regions", msg.completed, msg.total)
			// Continue to next region
			regions, _ := m.client.FetchRegionsSorted(m.ctx)
			if msg.completed < len(regions) {
				return m, streamRegionsCmd{regions: regions, provider: m.providers[m.providerIdx], idx: msg.completed, ctx: m.ctx}.fetch
			}
		}
		if msg.done || msg.completed >= msg.total {
			m.loading, m.progress = false, ""
		}
	case CreateResult:
		if m.createScreen != nil {
			m.createScreen, _ = m.createScreen.Update(msg)
		}
	case bundlesMsg, blueprintsMsg:
		if m.createScreen != nil {
			m.createScreen, _ = m.createScreen.Update(msg)
			if errs := m.createScreen.Errors(); len(errs) > 0 {
				m.toast = NewToast(errs)
				m.createScreen.ClearErrors()
				return m, ScheduleToastExpiry()
			}
		}
	case toastExpireMsg:
		m.toast = Toast{}
	case errMsg:
		m.toast = NewToast([]string{msg.Error()})
		m.loading, m.progress = false, ""
		return m, ScheduleToastExpiry()
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Handle create screen
	if m.view == viewCreate && m.createScreen != nil {
		var cmd tea.Cmd
		m.createScreen, cmd = m.createScreen.Update(msg)

		// Check if wizard was cancelled or completed
		if m.createScreen.IsCancelled() || m.createScreen.IsComplete() {
			m.view = viewResources
			m.createScreen = nil
			m.loading = true
			m.resources = nil
			return m, m.fetchResources()
		}
		return m, cmd
	}

	// Handle filter input mode
	if m.view == viewFilter {
		switch msg.Type {
		case tea.KeyEsc:
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
			if msg.Type == tea.KeyRunes {
				m.filter += string(msg.Runes)
				m.applyFilter()
			}
		}
		return m, nil
	}

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
		if m.view == viewRegions {
			m.cancel()
			m.ctx, m.cancel = context.WithCancel(context.Background())
			m.region = m.regions[m.regCursor]
			m.view = viewResources
			m.loading = true
			m.resources = nil
			m.filter = ""
			return m, m.fetchResources()
		} else if m.view == viewProviders {
			m.cancel()
			m.ctx, m.cancel = context.WithCancel(context.Background())
			m.providerIdx = m.provCursor
			m.cursor = 0
			m.view = viewResources
			m.loading = true
			m.resources = nil
			m.filter = ""
			return m, m.fetchResources()
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("j", "down"))):
		if m.view == viewResources && m.cursor < len(m.filtered)-1 {
			m.cursor++
		} else if m.view == viewRegions && m.regCursor < len(m.regions)-1 {
			m.regCursor++
		} else if m.view == viewProviders && m.provCursor < len(m.providers)-1 {
			m.provCursor++
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("k", "up"))):
		if m.view == viewResources && m.cursor > 0 {
			m.cursor--
		} else if m.view == viewRegions && m.regCursor > 0 {
			m.regCursor--
		} else if m.view == viewProviders && m.provCursor > 0 {
			m.provCursor--
		}
	case key.Matches(msg, key.NewBinding(key.WithKeys("R"))):
		m.cancel()
		m.ctx, m.cancel = context.WithCancel(context.Background())
		m.loading = true
		m.resources = nil
		m.filter = ""
		return m, m.fetchResources()
	case key.Matches(msg, key.NewBinding(key.WithKeys("c"))):
		if m.view == viewResources && m.providers[m.providerIdx].Kind() == "lightsail/instances" {
			region := m.region
			if region == "global" {
				region = m.client.DefaultRegion()
			}
			m.createScreen = NewCreateInstanceScreen(m.client, region, m.ctx)
			m.createScreen.SetSize(m.width, m.height)
			m.view = viewCreate
			return m, m.createScreen.Init()
		}
	}
	return m, nil
}

func (m *Model) applyFilter() {
	if m.filter == "" {
		m.filtered = m.resources
		return
	}
	f := strings.ToLower(m.filter)
	m.filtered = nil
	for _, r := range m.resources {
		for _, v := range r.Values() {
			if strings.Contains(strings.ToLower(v), f) {
				m.filtered = append(m.filtered, r)
				break
			}
		}
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = max(0, len(m.filtered)-1)
	}
}

func (m Model) View() string {
	var screen string
	if m.view == viewCreate && m.createScreen != nil {
		screen = m.renderCreateModal()
	} else {
		base := m.renderResources()
		switch m.view {
		case viewRegions:
			screen = m.overlay(base, m.renderRegionModal())
		case viewProviders:
			screen = m.overlay(base, m.renderProviderModal())
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
	return screen
}

func (m Model) renderCreateModal() string {
	content := m.createScreen.View()
	modal := ModalStyle.Width(70).Render(content)
	return m.centerModal(modal)
}

func (m Model) centerModal(modal string) string {
	modalLines := strings.Split(modal, "\n")
	modalH := len(modalLines)
	modalW := lipgloss.Width(modal)
	startY := (m.height - modalH) / 2
	startX := (m.width - modalW) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	var result []string
	bg := DimStyle.Width(m.width).Render(strings.Repeat(" ", m.width))
	for y := 0; y < m.height; y++ {
		if y >= startY && y < startY+modalH {
			mIdx := y - startY
			left := strings.Repeat(" ", startX)
			result = append(result, left+modalLines[mIdx])
		} else {
			result = append(result, bg)
		}
	}
	return strings.Join(result, "\n")
}

func (m Model) overlay(base, modal string) string {
	// Dim the background
	dimmed := DimStyle.Width(m.width).Height(m.height).Render(base)
	dimLines := strings.Split(dimmed, "\n")

	// Render modal with consistent background
	modalStyled := ModalStyle.Render(modal)
	modalLines := strings.Split(modalStyled, "\n")
	modalH := len(modalLines)
	modalW := lipgloss.Width(modalStyled)

	startY := (m.height - modalH) / 2
	startX := (m.width - modalW) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	// Pad background lines
	for len(dimLines) < m.height {
		dimLines = append(dimLines, DimStyle.Width(m.width).Render(""))
	}

	// Place modal lines over background
	var result []string
	for y := 0; y < m.height; y++ {
		if y >= startY && y < startY+modalH {
			mIdx := y - startY
			left := ""
			if startX > 0 && y < len(dimLines) {
				left = truncateToWidth(dimLines[y], startX)
			}
			right := ""
			rightStart := startX + modalW
			if rightStart < m.width && y < len(dimLines) {
				right = padOrTruncate(dimLines[y], rightStart, m.width)
			}
			result = append(result, left+modalLines[mIdx]+right)
		} else if y < len(dimLines) {
			result = append(result, dimLines[y])
		}
	}
	return strings.Join(result, "\n")
}

func truncateToWidth(s string, w int) string {
	// Return first w visible characters worth of string
	var result strings.Builder
	visible := 0
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
		}
		if inEscape {
			result.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if visible >= w {
			break
		}
		result.WriteRune(r)
		visible++
	}
	for visible < w {
		result.WriteRune(' ')
		visible++
	}
	return result.String()
}

func padOrTruncate(s string, start, end int) string {
	// Skip first 'start' visible chars, return next (end-start) chars
	var result strings.Builder
	visible := 0
	inEscape := false
	started := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
		}
		if inEscape {
			if started {
				result.WriteRune(r)
			}
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if visible >= end {
			break
		}
		if visible >= start {
			started = true
			result.WriteRune(r)
		}
		visible++
	}
	for visible < end {
		if visible >= start {
			result.WriteRune(' ')
		}
		visible++
	}
	return result.String()
}

func placeOverlay(width, height int, bg, modal string) string {
	bgLines := strings.Split(bg, "\n")
	modalLines := strings.Split(modal, "\n")
	modalW := lipgloss.Width(modal)
	modalH := len(modalLines)
	startX := (width - modalW) / 2
	startY := (height - modalH) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}
	for len(bgLines) < height {
		bgLines = append(bgLines, strings.Repeat(" ", width))
	}
	for i, mLine := range modalLines {
		y := startY + i
		if y >= len(bgLines) {
			break
		}
		bgRunes := []rune(bgLines[y])
		for len(bgRunes) < width {
			bgRunes = append(bgRunes, ' ')
		}
		mRunes := []rune(mLine)
		for j, r := range mRunes {
			x := startX + j
			if x < len(bgRunes) {
				bgRunes[x] = r
			}
		}
		bgLines[y] = string(bgRunes)
	}
	return strings.Join(bgLines[:height], "\n")
}

func (m Model) renderResources() string {
	var b strings.Builder
	provider := m.providers[m.providerIdx]
	title := fmt.Sprintf(" ☁ nimbus │ %s │ %s ", provider.Kind(), m.region)
	b.WriteString(TitleStyle.Render(title) + "\n")

	if m.view == viewFilter || m.filter != "" {
		b.WriteString(FilterStyle.Render(fmt.Sprintf(" /%s", m.filter)))
		if m.view == viewFilter {
			b.WriteString("█")
		}
	}
	b.WriteString("\n")

	headers := provider.Headers()
	b.WriteString(HeaderStyle.Render(formatRow(headers, m.width)) + "\n")
	for i, r := range m.filtered {
		row := formatRow(r.Values(), m.width)
		if i == m.cursor {
			row = SelectedStyle.Render(row)
		} else if r.Status() == "running" || r.Status() == "available" {
			row = RunningStyle.Render(row)
		} else if r.Status() == "stopped" {
			row = StoppedStyle.Render(row)
		}
		b.WriteString(row + "\n")
	}
	if len(m.filtered) == 0 && !m.loading {
		b.WriteString(HelpStyle.Render("  No resources found\n"))
	}

	help := " q:quit  /:filter  ::resources  r:regions  c:create  R:refresh  j/k:navigate "
	if m.progress != "" {
		help = " " + m.progress + " │" + help
	}
	status := StatusBarStyle.Width(m.width).Render(help)
	return lipgloss.JoinVertical(lipgloss.Left, b.String(), status)
}

func (m Model) renderRegionModal() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render(" Select Region ") + "\n\n")
	visible := 10
	start := m.regCursor - visible/2
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > len(m.regions) {
		end = len(m.regions)
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	for i := start; i < end; i++ {
		r := m.regions[i]
		if i == m.regCursor {
			b.WriteString(SelectedStyle.Render(fmt.Sprintf(" > %s ", r)) + "\n")
		} else {
			b.WriteString(fmt.Sprintf("   %s\n", r))
		}
	}
	b.WriteString("\n" + HelpStyle.Render(" enter:select  esc:cancel "))
	return b.String()
}

func (m Model) renderProviderModal() string {
	var b strings.Builder
	b.WriteString(TitleStyle.Render(" Select Resource ") + "\n\n")
	maxLen := 0
	for _, p := range m.providers {
		if len(p.Kind()) > maxLen {
			maxLen = len(p.Kind())
		}
	}
	for i, p := range m.providers {
		if i == m.provCursor {
			b.WriteString(SelectedStyle.Render(fmt.Sprintf(" > %-*s ", maxLen, p.Kind())) + "\n")
		} else {
			b.WriteString(fmt.Sprintf("   %-*s\n", maxLen, p.Kind()))
		}
	}
	b.WriteString("\n" + HelpStyle.Render(" enter:select  esc:cancel "))
	return b.String()
}

func formatRow(cols []string, width int) string {
	colWidth := width / len(cols)
	if colWidth < 10 {
		colWidth = 10
	}
	var parts []string
	for _, c := range cols {
		if len(c) > colWidth-2 {
			c = c[:colWidth-2]
		}
		parts = append(parts, fmt.Sprintf("%-*s", colWidth, c))
	}
	return strings.Join(parts, "")
}
