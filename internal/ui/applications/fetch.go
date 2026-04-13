package applications

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/deploy"
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

// CreateApp creates the app config bucket and the first env bucket.
func CreateApp(ctx context.Context, client *aws.Client, accountID, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		appClient := applications.NewClient(client)
		if err := appClient.CreateAppBucket(ctx, accountID, appName, region); err != nil {
			return CreateAppMsg{Err: err, Name: appName}
		}
		err := appClient.Create(ctx, accountID, appName, envName, region)
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

// DeleteAppBuckets deletes env buckets (step 4 of delete).
func DeleteAppBuckets(ctx context.Context, client *aws.Client, appName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).DeleteBuckets(ctx, appName, region)
		if err != nil {
			return DeleteAppMsg{Err: err, Name: appName}
		}
		return DeleteAppMsg{Name: appName}
	}
}

// CleanupFirewallDoneMsg signals firewall cleanup step completed.
type CleanupFirewallDoneMsg struct {
	Err  error
	Name string
}

// CleanupAppFirewall cleans up firewall rules (step 3 of delete).
func CleanupAppFirewall(ctx context.Context, client *aws.Client, appName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).CleanupFirewall(ctx, appName, region)
		return CleanupFirewallDoneMsg{Err: err, Name: appName}
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

// AddEnvMsg signals environment addition completed.
type AddEnvMsg struct {
	Err     error
	AppName string
	EnvName string
}

// AddEnvBucketMsg signals the env bucket was created.
type AddEnvBucketMsg struct {
	Err     error
	AppName string
	EnvName string
}

// AddEnvSteps returns the step labels for adding an environment.
func AddEnvSteps(envName string) []string {
	return []string{
		fmt.Sprintf("Create bucket for %s", envName),
		"Update environment order",
	}
}

// AddEnvCreateBucket creates the env bucket (step 1).
func AddEnvCreateBucket(ctx context.Context, client *aws.Client, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		appClient := applications.NewClient(client)
		accountID, err := appClient.AccountID(ctx)
		if err != nil {
			return AddEnvBucketMsg{Err: err, AppName: appName, EnvName: envName}
		}
		err = appClient.Create(ctx, accountID, appName, envName, region)
		return AddEnvBucketMsg{Err: err, AppName: appName, EnvName: envName}
	}
}

// AddEnvUpdateOrder updates the env order config (step 2).
func AddEnvUpdateOrder(ctx context.Context, client *aws.Client, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		appClient := applications.NewClient(client)
		order, _ := appClient.GetEnvOrder(ctx, appName, region)
		found := false
		for _, e := range order {
			if e == envName {
				found = true
				break
			}
		}
		if !found {
			order = append(order, envName)
		}
		err := appClient.SetEnvOrder(ctx, appName, region, order)
		return AddEnvMsg{Err: err, AppName: appName, EnvName: envName}
	}
}

// DeleteEnvSteps returns the step labels for deleting an environment.
func DeleteEnvSteps(envName string) []string {
	return applications.DeleteEnvSteps(envName)
}

// DeleteEnvDisassocMsg signals targets have been disassociated (step 1).
type DeleteEnvDisassocMsg struct {
	EnvName string
}

// DeleteEnvBucketMsg signals the env bucket has been deleted (step 2).
type DeleteEnvBucketMsg struct {
	Err     error
	EnvName string
}

// DeleteEnvDoneMsg signals the env deletion is complete (step 3).
type DeleteEnvDoneMsg struct {
	Err     error
	EnvName string
}

// DeleteEnvDisassocTargets disassociates all targets for an env (step 1).
func DeleteEnvDisassocTargets(ctx context.Context, client *aws.Client, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		applications.NewClient(client).DisassociateEnvTargets(ctx, appName, envName, region)
		return DeleteEnvDisassocMsg{EnvName: envName}
	}
}

// DeleteEnvBucket deletes the env bucket (step 2).
func DeleteEnvBucket(ctx context.Context, client *aws.Client, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).DeleteEnvBucket(ctx, appName, envName, region)
		return DeleteEnvBucketMsg{Err: err, EnvName: envName}
	}
}

// DeleteEnvUpdateOrder updates the env order after deletion (step 3).
func DeleteEnvUpdateOrder(ctx context.Context, client *aws.Client, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		appClient := applications.NewClient(client)
		order, _ := appClient.GetEnvOrder(ctx, appName, region)
		var newOrder []string
		for _, e := range order {
			if e != envName {
				newOrder = append(newOrder, e)
			}
		}
		var err error
		if len(newOrder) > 0 {
			err = appClient.SetEnvOrder(ctx, appName, region, newOrder)
		}
		return DeleteEnvDoneMsg{Err: err, EnvName: envName}
	}
}

// RenderDeleteEnvConfirmModal renders a delete environment confirmation.
func RenderDeleteEnvConfirmModal(envName string) string {
	return utils.ErrorStyle.Render(fmt.Sprintf(" Delete environment '%s'? This will remove all targets and data. ", envName)) + "\n\n" +
		utils.HelpStyle.Render(" y:confirm  n/esc:cancel ")
}

// ReorderEnvMsg signals environment reorder completed.
type ReorderEnvMsg struct {
	Err   error
	Order []string
}

// ReorderEnv sets the environment order for an app.
func ReorderEnv(ctx context.Context, client *aws.Client, appName, region string, order []string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).SetEnvOrder(ctx, appName, region, order)
		return ReorderEnvMsg{Err: err, Order: order}
	}
}

// EnvOrderMsg carries the current environment order.
type EnvOrderMsg struct {
	Err   error
	Order []string
}

// FetchEnvOrder fetches the environment order for an app.
func FetchEnvOrder(ctx context.Context, client *aws.Client, appName, region string) tea.Cmd {
	return func() tea.Msg {
		order, err := applications.NewClient(client).GetEnvOrder(ctx, appName, region)
		return EnvOrderMsg{Err: err, Order: order}
	}
}

// PromoteMsg signals promotion completed (step 2).
type PromoteMsg struct {
	Err     error
	SrcEnv  string
	DestEnv string
}

// PromoteDownloadMsg signals the download step completed (step 1).
type PromoteDownloadMsg struct {
	Err    error
	SrcEnv string
	Result *deploy.PromoteResult
}

// PromoteSteps returns the step labels for promoting between environments.
func PromoteSteps(srcEnv, destEnv string) []string {
	return applications.PromoteSteps(srcEnv, destEnv)
}

// PromoteDownload downloads the latest deploy from srcEnv (step 1).
func PromoteDownload(ctx context.Context, client *aws.Client, appName, srcEnv, region string) tea.Cmd {
	return func() tea.Msg {
		r, err := deploy.PromoteDownload(ctx, client, appName, srcEnv, region)
		return PromoteDownloadMsg{Err: err, SrcEnv: srcEnv, Result: r}
	}
}

// PromoteUpload uploads the deploy to destEnv (step 2).
func PromoteUpload(ctx context.Context, client *aws.Client, appName, destEnv, region string, r *deploy.PromoteResult) tea.Cmd {
	return func() tea.Msg {
		err := deploy.PromoteUpload(ctx, client, appName, destEnv, region, r)
		return PromoteMsg{Err: err, SrcEnv: "", DestEnv: destEnv}
	}
}

// LogsExitMsg signals the logs process exited.
type LogsExitMsg struct{ Err error }

// LogsExecCmd returns a tea.Cmd that SSHes to the target and streams docker compose logs.
func LogsExecCmd(ctx context.Context, client *aws.Client, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		appClient := applications.NewClient(client)
		cmd, creds, err := appClient.RemoteLogsCmd(ctx, appName, envName, region)
		if err != nil {
			return LogsExitMsg{Err: err}
		}
		return ExecLogsMsg{Cmd: cmd, KeyPath: creds.KeyPath}
	}
}

// ExecLogsMsg carries the prepared command for tea.ExecProcess.
type ExecLogsMsg struct {
	Cmd     *exec.Cmd
	KeyPath string
}
