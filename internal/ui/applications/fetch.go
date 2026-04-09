package applications

import (
	"context"
	"fmt"

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
func CreateApp(ctx context.Context, client *aws.Client, accountID, appName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).Create(ctx, accountID, appName, region)
		if err != nil {
			return CreateAppMsg{Err: err, Name: appName}
		}
		return CreateAppMsg{Name: appName}
	}
}

// AddTarget tags an instance as a deployment target for an app/env.
func AddTarget(ctx context.Context, client *aws.Client, instanceName, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).AddTarget(ctx, instanceName, appName, envName, region)
		if err != nil {
			return CreateAppMsg{Err: err, Name: appName}
		}
		return CreateAppMsg{Name: appName}
	}
}

// DeleteApp deletes an application via the SDK.
func DeleteApp(ctx context.Context, client *aws.Client, appName, region string) tea.Cmd {
	return func() tea.Msg {
		err := applications.NewClient(client).Delete(ctx, appName, region)
		if err != nil {
			return DeleteAppMsg{Err: err, Name: appName}
		}
		return DeleteAppMsg{Name: appName}
	}
}

// RenderAppConfirmModal renders a delete confirmation for an application.
func RenderAppConfirmModal(name string) string {
	return utils.ErrorStyle.Render(fmt.Sprintf(" Delete application '%s'? ", name)) + "\n\n" +
		utils.HelpStyle.Render(" y:confirm  n/esc:cancel ")
}
