package instances

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// ActionKind represents the type of instance action.
type ActionKind int

const (
	ActionStop ActionKind = iota
	ActionStart
	ActionDelete
)

// ConfirmAction holds the details of a pending action.
type ConfirmAction struct {
	Kind   ActionKind
	Name   string
	Region string
}

// ActionResultMsg is returned when an instance action completes.
type ActionResultMsg struct {
	Err    error
	Msg    string
	Region string
}

// SSHExitMsg is returned when an SSH session ends.
type SSHExitMsg struct{ Err error }

// SSHCredentialsMsg carries temporary SSH credentials.
type SSHCredentialsMsg struct {
	KeyPath  string
	Username string
	IP       string
	Err      error
}

// ExecuteAction runs a stop/start/delete action against the Lightsail API.
func ExecuteAction(ctx context.Context, client *aws.Client, a ConfirmAction) tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(client.WithRegion(a.Region).Config())
		switch a.Kind {
		case ActionStop:
			_, err := svc.StopInstance(ctx, &lightsail.StopInstanceInput{InstanceName: &a.Name})
			if err != nil {
				return ActionResultMsg{Err: fmt.Errorf("stop %s: %w", a.Name, err), Region: a.Region}
			}
			return ActionResultMsg{Msg: fmt.Sprintf("Instance '%s' stopping", a.Name), Region: a.Region}
		case ActionStart:
			_, err := svc.StartInstance(ctx, &lightsail.StartInstanceInput{InstanceName: &a.Name})
			if err != nil {
				return ActionResultMsg{Err: fmt.Errorf("start %s: %w", a.Name, err), Region: a.Region}
			}
			return ActionResultMsg{Msg: fmt.Sprintf("Instance '%s' starting", a.Name), Region: a.Region}
		case ActionDelete:
			_, err := svc.DeleteInstance(ctx, &lightsail.DeleteInstanceInput{InstanceName: &a.Name, ForceDeleteAddOns: utils.BoolPtr(true)})
			if err != nil {
				return ActionResultMsg{Err: fmt.Errorf("delete %s: %w", a.Name, err), Region: a.Region}
			}
			return ActionResultMsg{Msg: fmt.Sprintf("Instance '%s' deleted", a.Name), Region: a.Region}
		}
		return nil
	}
}

// RenderConfirmModal renders the confirmation modal content.
func RenderConfirmModal(a *ConfirmAction) string {
	action := "Stop"
	if a.Kind == ActionDelete {
		action = "Delete"
	} else if a.Kind == ActionStart {
		action = "Start"
	}
	return utils.ErrorStyle.Render(fmt.Sprintf(" %s instance '%s'? ", action, a.Name)) + "\n\n" +
		utils.HelpStyle.Render(" y:confirm  n/esc:cancel ")
}

// FetchSSHCredentials fetches temporary SSH credentials for an instance.
func FetchSSHCredentials(ctx context.Context, client *aws.Client, name, region string) tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(client.WithRegion(region).Config())
		out, err := svc.GetInstanceAccessDetails(ctx, &lightsail.GetInstanceAccessDetailsInput{
			InstanceName: &name,
			Protocol:     types.InstanceAccessProtocolSsh,
		})
		if err != nil {
			return SSHCredentialsMsg{Err: fmt.Errorf("get access details: %w", err)}
		}
		d := out.AccessDetails

		keyFile, err := os.CreateTemp("", "nimbus-ssh-*")
		if err != nil {
			return SSHCredentialsMsg{Err: err}
		}
		keyPath := keyFile.Name()
		keyFile.Chmod(0600)
		keyFile.WriteString(*d.PrivateKey)
		keyFile.Close()

		if d.CertKey != nil && *d.CertKey != "" {
			os.WriteFile(keyPath+"-cert.pub", []byte(*d.CertKey), 0600)
		}

		return SSHCredentialsMsg{KeyPath: keyPath, Username: *d.Username, IP: *d.IpAddress}
	}
}

// SSHExecCmd returns a tea.Cmd that execs an SSH process and cleans up key files on exit.
func SSHExecCmd(creds SSHCredentialsMsg) tea.Cmd {
	c := exec.Command("ssh",
		"-i", creds.KeyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		fmt.Sprintf("%s@%s", creds.Username, creds.IP),
	)
	keyPath := creds.KeyPath
	return tea.ExecProcess(c, func(err error) tea.Msg {
		os.Remove(keyPath)
		os.Remove(keyPath + "-cert.pub")
		return SSHExitMsg{Err: err}
	})
}
