package applications

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// AppDetailMsg carries fetched app detail.
type AppDetailMsg struct {
	Detail *applications.Detail
	Err    error
}

// CreateAppMsg is returned when app creation completes.
type CreateAppMsg struct {
	Err  error
	Name string
}

// AppProgressMsg carries intermediate progress updates during async operations.
type AppProgressMsg struct {
	Status string
}

// AddTargetStepMsg reports sub-step progress during AddTarget.
type AddTargetStepMsg struct {
	Step int // 0=tagging, 1=creating key, 2=writing creds
}

// DeleteAppMsg is returned when app deletion completes.
type DeleteAppMsg struct {
	Err  error
	Name string
}

// AccountIDMsg carries the AWS account ID.
type AccountIDMsg struct {
	AccountID string
	Err       error
}

// FetchAccountID gets the AWS account ID via STS.
func FetchAccountID(ctx context.Context, client *aws.Client) tea.Cmd {
	return func() tea.Msg {
		id, err := applications.NewClient(client).AccountID(ctx)
		if err != nil {
			return AccountIDMsg{Err: err}
		}
		return AccountIDMsg{AccountID: id}
	}
}

// FetchAppDetail fetches full app detail: bucket info + instance tags for targets.
func FetchAppDetail(ctx context.Context, client *aws.Client, appName, bucketName, region string) tea.Cmd {
	return func() tea.Msg {
		detail, err := applications.NewClient(client).GetDetail(ctx, appName, bucketName, region)
		if err != nil {
			return AppDetailMsg{Err: err}
		}
		return AppDetailMsg{Detail: detail}
	}
}

// CreateApp creates a Lightsail bucket for the application.
func CreateApp(ctx context.Context, client *aws.Client, accountID, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).Create(ctx, accountID, appName, envName, region)
		if err != nil {
			return CreateAppMsg{Err: err, Name: appName}
		}
		return CreateAppMsg{Name: appName}
	}
}

// AddTarget tags an instance as a deployment target for an app/env.
func AddTarget(ctx context.Context, client *aws.Client, instanceName, appName, envName, accountID, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).AddTarget(ctx, instanceName, appName, envName, accountID, region)
		if err != nil {
			return CreateAppMsg{Err: err, Name: appName}
		}
		return CreateAppMsg{Name: appName}
	}
}

// DeleteApp deletes an application via the SDK (single-shot, used by CLI).
func DeleteApp(ctx context.Context, client *aws.Client, appName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).Delete(ctx, appName, region)
		if err != nil {
			return DeleteAppMsg{Err: err, Name: appName}
		}
		return DeleteAppMsg{Name: appName}
	}
}

// DeleteTagsDoneMsg signals tag removal step completed.
type DeleteTagsDoneMsg struct {
	Err  error
	Name string
}

// CleanupInstancesDoneMsg signals instance cleanup step completed.
type CleanupInstancesDoneMsg struct {
	Err  error
	Name string
}

// CleanupAppInstances cleans up instances (step 1 of delete).
func CleanupAppInstances(ctx context.Context, client *aws.Client, appName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).CleanupInstances(ctx, appName, region)
		return CleanupInstancesDoneMsg{Err: err, Name: appName}
	}
}

// DeleteAppTags removes instance tags (step 2 of delete).
func DeleteAppTags(ctx context.Context, client *aws.Client, appName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).DeleteTags(ctx, appName, region)
		return DeleteTagsDoneMsg{Err: err, Name: appName}
	}
}

// DeleteAppBuckets deletes env buckets (step 2 of delete).
func DeleteAppBuckets(ctx context.Context, client *aws.Client, appName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).DeleteBuckets(ctx, appName, region)
		if err != nil {
			return DeleteAppMsg{Err: err, Name: appName}
		}
		return DeleteAppMsg{Name: appName}
	}
}

// DisassociateTargetMsg is returned when a target is disassociated.
type DisassociateTargetMsg struct {
	Err          error
	InstanceName string
	AppName      string
}

// DisassociateTarget removes an instance's association with an app/env and cleans up the instance.
func DisassociateTarget(ctx context.Context, client *aws.Client, instanceName, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).RemoveTarget(ctx, instanceName, appName, envName, region, true)
		return DisassociateTargetMsg{Err: err, InstanceName: instanceName, AppName: appName}
	}
}

// RenderAppConfirmModal renders a delete confirmation for an application.
func RenderAppConfirmModal(name string) string {
	return utils.ErrorStyle.Render(fmt.Sprintf(" Delete application '%s'? ", name)) + "\n\n" +
		utils.HelpStyle.Render(" y:confirm  n/esc:cancel ")
}

// RenderDisassociateConfirmModal renders a disassociate confirmation.
func RenderDisassociateConfirmModal(instanceName, envName string) string {
	return utils.ErrorStyle.Render(fmt.Sprintf(" Disassociate '%s' from env '%s'? ", instanceName, envName)) + "\n\n" +
		utils.HelpStyle.Render(" y:confirm  n/esc:cancel ")
}

// AddTargetMsg is returned when add-target completes from the detail view.
type AddTargetMsg struct {
	Err error
}

// AddTargetFromDetail adds a target from the app detail view, sending progress updates.
func AddTargetFromDetail(ctx context.Context, client *aws.Client, instanceName, appName, envName, accountID, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).AddTarget(ctx, instanceName, appName, envName, accountID, region)
		return AddTargetMsg{Err: err}
	}
}

// FetchInstances fetches available instances for the add-target modal.
func FetchInstances(ctx context.Context, client *aws.Client, region string) tea.Cmd {
	return func() tea.Msg {
		targets, err := applications.NewClient(client).ListInstances(ctx, region)
		if err != nil {
			return InstancesMsg{Err: err}
		}
		return InstancesMsg{Instances: targets}
	}
}

// RenderInstanceSelectModal renders a modal for selecting an instance to add as a target.
func RenderInstanceSelectModal(instances []applications.Target, cursor int) string {
	var b strings.Builder
	b.WriteString(utils.TitleStyle.Render(" Add Deployment Target ") + "\n\n")
	if len(instances) == 0 {
		b.WriteString("  No instances available\n")
		b.WriteString("\n" + utils.HelpStyle.Render(" esc:cancel "))
		return b.String()
	}
	visible := 10
	start := cursor - visible/2
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > len(instances) {
		end = len(instances)
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	for i := start; i < end; i++ {
		inst := instances[i]
		label := fmt.Sprintf("%-24s %-10s %s", inst.Name, inst.State, inst.IP)
		if i == cursor {
			b.WriteString(utils.SelectedStyle.Render(" > "+label+" ") + "\n")
		} else {
			b.WriteString(fmt.Sprintf("   %s\n", label))
		}
	}
	b.WriteString("\n" + utils.HelpStyle.Render(" enter:select  esc:cancel "))
	return b.String()
}
