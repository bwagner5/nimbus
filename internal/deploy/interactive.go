package deploy

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

// phase tracks the deploy model's current state.
type phase int

const (
	phaseSelectApp phase = iota
	phaseSelectEnv
	phaseResolve
	phasePackage
	phaseCreateKey
	phaseUpload
	phaseCleanupKey
	phaseFirewall
	phasePollStatus
	phaseDone
	phaseFailed
)

var (
	stepStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	failStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	selStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	greenStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	yellowStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	redStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
)

// --- messages ---

type appsLoadedMsg struct {
	apps []applications.App
	err  error
}
type resolvedMsg struct {
	bucket string
	err    error
}
type packagedMsg struct {
	path string
	size int64
	err  error
}
type keyCreatedMsg struct {
	keyID, secret string
	err           error
}
type uploadedMsg struct{ err error }
type keyCleanedMsg struct{}
type firewallMsg struct {
	ports []int
	err   error
}
type statusPollMsg struct {
	statuses []applications.InstanceStatus
	err      error
}
type pollTickMsg struct{}

// deployModel is an inline (non-fullscreen) bubbletea model for deploy.
type deployModel struct {
	ctx       context.Context
	client    *aws.Client
	appClient *applications.Client
	region    string

	// selection
	apps      []applications.App
	appCursor int
	envs      []string
	envCursor int

	// state
	phase     phase
	appName   string
	envName   string
	accountID string
	bucket    string
	assetName string
	tmpPath   string
	keyID     string
	secret    string
	spinner   spinner.Model
	err       error

	// completed steps for display
	steps []string

	// poll
	pollStart  time.Time
	ports      []int
	endpoints  []string
	containers []applications.ContainerStatus
}

// RunInteractiveDeploy runs the inline deploy TUI.
func RunInteractiveDeploy(ctx context.Context, client *aws.Client, appName, envName, region string) error {
	s := spinner.New()
	s.Spinner = spinner.Dot

	m := &deployModel{
		ctx:       ctx,
		client:    client,
		appClient: applications.NewClient(client),
		region:    region,
		appName:   appName,
		envName:   envName,
		spinner:   s,
	}

	p := tea.NewProgram(m, tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr))
	result, err := p.Run()
	if err != nil {
		return err
	}
	if dm, ok := result.(*deployModel); ok && dm.err != nil {
		return dm.err
	}
	return nil
}

func (m *deployModel) Init() tea.Cmd {
	if m.appName == "" {
		m.phase = phaseSelectApp
		return tea.Batch(m.spinner.Tick, m.loadApps())
	}
	if m.envName == "" {
		m.phase = phaseSelectApp // load apps to get envs
		return tea.Batch(m.spinner.Tick, m.loadApps())
	}
	m.phase = phaseResolve
	return tea.Batch(m.spinner.Tick, m.doResolve())
}

func (m *deployModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.err = fmt.Errorf("cancelled")
			return m, tea.Quit
		case "up", "k":
			if m.phase == phaseSelectApp && m.appCursor > 0 {
				m.appCursor--
			}
			if m.phase == phaseSelectEnv && m.envCursor > 0 {
				m.envCursor--
			}
		case "down", "j":
			if m.phase == phaseSelectApp && m.appCursor < len(m.apps)-1 {
				m.appCursor++
			}
			if m.phase == phaseSelectEnv && m.envCursor < len(m.envs)-1 {
				m.envCursor++
			}
		case "enter":
			if m.phase == phaseSelectApp && len(m.apps) > 0 {
				m.appName = m.apps[m.appCursor].Name
				if m.envName != "" {
					m.phase = phaseResolve
					return m, m.doResolve()
				}
				m.envs = m.apps[m.appCursor].Envs
				if len(m.envs) == 1 {
					m.envName = m.envs[0]
					m.phase = phaseResolve
					return m, m.doResolve()
				}
				m.envCursor = 0
				m.phase = phaseSelectEnv
				return m, nil
			}
			if m.phase == phaseSelectEnv && len(m.envs) > 0 {
				m.envName = m.envs[m.envCursor]
				m.phase = phaseResolve
				return m, m.doResolve()
			}
		}
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case appsLoadedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.phase = phaseFailed
			return m, tea.Quit
		}
		m.apps = msg.apps
		if len(m.apps) == 0 {
			m.err = fmt.Errorf("no applications found")
			m.phase = phaseFailed
			return m, tea.Quit
		}
		if m.appName != "" {
			// App given, need env selection
			for _, a := range m.apps {
				if a.Name == m.appName {
					m.envs = a.Envs
					break
				}
			}
			if len(m.envs) == 0 {
				m.err = fmt.Errorf("application %q not found", m.appName)
				m.phase = phaseFailed
				return m, tea.Quit
			}
			if len(m.envs) == 1 {
				m.envName = m.envs[0]
				m.phase = phaseResolve
				return m, m.doResolve()
			}
			m.envCursor = 0
			m.phase = phaseSelectEnv
			return m, nil
		}
		return m, nil

	case resolvedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.phase = phaseFailed
			return m, tea.Quit
		}
		m.bucket = msg.bucket
		m.addStep("Resolved bucket")
		m.phase = phasePackage
		return m, m.doPackage()

	case packagedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.phase = phaseFailed
			return m, tea.Quit
		}
		m.tmpPath = msg.path
		m.addStep(fmt.Sprintf("Packaged (%d bytes)", msg.size))
		m.phase = phaseCreateKey
		return m, m.doCreateKey()

	case keyCreatedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.phase = phaseFailed
			return m, tea.Quit
		}
		m.keyID = msg.keyID
		m.secret = msg.secret
		m.addStep("Created access key")
		m.phase = phaseUpload
		return m, m.doUpload()

	case uploadedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.phase = phaseFailed
			// Still clean up key
			m.appClient.DeleteBucketAccessKey(m.ctx, m.bucket, m.keyID, m.region)
			return m, tea.Quit
		}
		m.addStep(fmt.Sprintf("Uploaded %s", m.assetName))
		m.phase = phaseCleanupKey
		return m, m.doCleanupKey()

	case keyCleanedMsg:
		m.addStep("Cleaned up access key")
		m.phase = phaseFirewall
		return m, m.doFirewall()

	case firewallMsg:
		if msg.err != nil {
			m.addStep(fmt.Sprintf("Firewall update skipped: %v", msg.err))
		} else if len(msg.ports) > 0 {
			m.ports = msg.ports
			m.addStep(fmt.Sprintf("Opened firewall ports %v", msg.ports))
		}
		m.phase = phasePollStatus
		m.pollStart = time.Now()
		return m, m.doPollStatus()

	case statusPollMsg:
		if msg.err == nil {
			for _, s := range msg.statuses {
				if len(s.Containers) > 0 {
					m.containers = s.Containers
					m.endpoints = s.Endpoints
					m.addStep("Containers running")
					m.phase = phaseDone
					return m, tea.Quit
				}
			}
		}
		if time.Since(m.pollStart) > 3*time.Minute {
			m.addStep("Timed out waiting for containers (deploy was uploaded successfully)")
			m.phase = phaseDone
			return m, tea.Quit
		}
		return m, tea.Tick(5*time.Second, func(time.Time) tea.Msg { return pollTickMsg{} })

	case pollTickMsg:
		return m, m.doPollStatus()
	}
	return m, nil
}

func (m *deployModel) View() tea.View {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("  Deploying %s/%s\n\n", m.appName, m.envName))

	// Selection UI
	if m.phase == phaseSelectApp {
		if len(m.apps) == 0 {
			b.WriteString(fmt.Sprintf("  %s Loading applications...\n", m.spinner.View()))
			return tea.NewView(b.String())
		}
		b.WriteString("  Select application:\n")
		for i, a := range m.apps {
			cursor := "  "
			style := dimStyle
			if i == m.appCursor {
				cursor = "▸ "
				style = selStyle
			}
			b.WriteString(fmt.Sprintf("  %s%s\n", cursor, style.Render(a.Name)))
		}
		return tea.NewView(b.String())
	}
	if m.phase == phaseSelectEnv {
		b.WriteString("  Select environment:\n")
		for i, e := range m.envs {
			cursor := "  "
			style := dimStyle
			if i == m.envCursor {
				cursor = "▸ "
				style = selStyle
			}
			b.WriteString(fmt.Sprintf("  %s%s\n", cursor, style.Render(e)))
		}
		return tea.NewView(b.String())
	}

	// Completed steps
	for _, s := range m.steps {
		b.WriteString(fmt.Sprintf("  %s %s\n", stepStyle.Render("✓"), s))
	}

	// Current step
	switch m.phase {
	case phaseResolve:
		b.WriteString(fmt.Sprintf("  %s Resolving bucket...\n", m.spinner.View()))
	case phasePackage:
		b.WriteString(fmt.Sprintf("  %s Packaging...\n", m.spinner.View()))
	case phaseCreateKey:
		b.WriteString(fmt.Sprintf("  %s Creating access key...\n", m.spinner.View()))
	case phaseUpload:
		b.WriteString(fmt.Sprintf("  %s Uploading %s...\n", m.spinner.View(), m.assetName))
	case phaseCleanupKey:
		b.WriteString(fmt.Sprintf("  %s Cleaning up access key...\n", m.spinner.View()))
	case phaseFirewall:
		b.WriteString(fmt.Sprintf("  %s Opening firewall ports...\n", m.spinner.View()))
	case phasePollStatus:
		elapsed := time.Since(m.pollStart).Truncate(time.Second)
		b.WriteString(fmt.Sprintf("  %s Waiting for containers... (%s)\n", m.spinner.View(), elapsed))
	case phaseFailed:
		b.WriteString(fmt.Sprintf("  %s %s\n", failStyle.Render("✗"), m.err))
	case phaseDone:
		if len(m.endpoints) > 0 {
			b.WriteString("\n")
			for _, ep := range m.endpoints {
				b.WriteString(fmt.Sprintf("  %s %s\n", stepStyle.Render("→"), ep))
			}
		}
		if len(m.containers) > 0 {
			b.WriteString("\n")
			for _, c := range m.containers {
				style := dimStyle
				switch c.Status {
				case "running":
					style = greenStyle
				case "restarting", "paused":
					style = yellowStyle
				case "exited", "dead", "removing":
					style = redStyle
				}
				b.WriteString(fmt.Sprintf("  %s %s (%s)\n", style.Render("▪"), c.Name, style.Render(c.Status)))
			}
		}
		b.WriteString("\n")
	}

	return tea.NewView(b.String())
}

func (m *deployModel) addStep(label string) {
	m.steps = append(m.steps, label)
}

// --- commands ---

func (m *deployModel) loadApps() tea.Cmd {
	client, region, ctx := m.appClient, m.region, m.ctx
	return func() tea.Msg {
		apps, err := client.List(ctx, region)
		return appsLoadedMsg{apps: apps, err: err}
	}
}

func (m *deployModel) doResolve() tea.Cmd {
	client, ctx := m.appClient, m.ctx
	appName, envName := m.appName, m.envName
	return func() tea.Msg {
		accountID, err := client.AccountID(ctx)
		if err != nil {
			return resolvedMsg{err: err}
		}
		return resolvedMsg{bucket: applications.BucketName(accountID, appName, envName)}
	}
}

func (m *deployModel) doPackage() tea.Cmd {
	return func() tea.Msg {
		if findComposeFile() == "" {
			return packagedMsg{err: fmt.Errorf("no docker-compose.yml or compose.yaml found")}
		}
		commitID := getGitCommit()
		m.assetName = fmt.Sprintf("deploy/%d-%s.tar.gz", time.Now().Unix(), commitID)

		tmpFile, err := os.CreateTemp("", "nimbus-deploy-*.tar.gz")
		if err != nil {
			return packagedMsg{err: err}
		}
		if err := tarDir(".", tmpFile); err != nil {
			tmpFile.Close()
			os.Remove(tmpFile.Name())
			return packagedMsg{err: err}
		}
		fi, _ := tmpFile.Stat()
		size := fi.Size()
		tmpFile.Close()
		return packagedMsg{path: tmpFile.Name(), size: size}
	}
}

func (m *deployModel) doCreateKey() tea.Cmd {
	client, bucket, region, ctx := m.appClient, m.bucket, m.region, m.ctx
	return func() tea.Msg {
		keyID, secret, err := client.CreateBucketAccessKey(ctx, bucket, region)
		return keyCreatedMsg{keyID: keyID, secret: secret, err: err}
	}
}

func (m *deployModel) doUpload() tea.Cmd {
	bucket, assetName, tmpPath, keyID, secret, region, ctx := m.bucket, m.assetName, m.tmpPath, m.keyID, m.secret, m.region, m.ctx
	return func() tea.Msg {
		defer os.Remove(tmpPath)
		s3svc := s3.New(s3.Options{
			Region:      region,
			Credentials: credentials.NewStaticCredentialsProvider(keyID, secret, ""),
		})
		f, err := os.Open(tmpPath)
		if err != nil {
			return uploadedMsg{err: err}
		}
		defer f.Close()
		fi, _ := f.Stat()

		var uploadErr error
		for attempt := 0; attempt < 5; attempt++ {
			if attempt > 0 {
				time.Sleep(2 * time.Second)
				f.Seek(0, 0)
			}
			_, uploadErr = s3svc.PutObject(ctx, &s3.PutObjectInput{
				Bucket:        &bucket,
				Key:           &assetName,
				Body:          f,
				ContentLength: awssdk.Int64(fi.Size()),
			})
			if uploadErr == nil {
				break
			}
		}
		return uploadedMsg{err: uploadErr}
	}
}

func (m *deployModel) doCleanupKey() tea.Cmd {
	client, bucket, keyID, region, ctx := m.appClient, m.bucket, m.keyID, m.region, m.ctx
	return func() tea.Msg {
		client.DeleteBucketAccessKey(ctx, bucket, keyID, region)
		return keyCleanedMsg{}
	}
}

func (m *deployModel) doFirewall() tea.Cmd {
	composeFile := findComposeFile()
	client, appName, envName, region, ctx := m.appClient, m.appName, m.envName, m.region, m.ctx
	return func() tea.Msg {
		ports, err := ParseComposePorts(composeFile)
		if err != nil || len(ports) == 0 {
			return firewallMsg{}
		}
		target, err := client.FindTarget(ctx, appName, envName, region)
		if err != nil {
			return firewallMsg{err: err}
		}
		err = client.OpenFirewallPorts(ctx, target.Name, region, ports)
		return firewallMsg{ports: ports, err: err}
	}
}

func (m *deployModel) doPollStatus() tea.Cmd {
	client, bucket, region, ctx := m.appClient, m.bucket, m.region, m.ctx
	return func() tea.Msg {
		statuses, err := client.FetchBucketStatuses(ctx, bucket, region)
		return statusPollMsg{statuses: statuses, err: err}
	}
}
