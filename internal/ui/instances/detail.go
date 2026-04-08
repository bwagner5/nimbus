package instances

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// InstanceDetailMsg carries the full instance detail from GetInstance.
type InstanceDetailMsg struct {
	Instance *types.Instance
	Err      error
}

// FetchInstanceDetail calls GetInstance for a single instance.
func FetchInstanceDetail(ctx context.Context, client *aws.Client, name, region string) tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(client.WithRegion(region).Config())
		out, err := svc.GetInstance(ctx, &lightsail.GetInstanceInput{InstanceName: &name})
		if err != nil {
			return InstanceDetailMsg{Err: err}
		}
		return InstanceDetailMsg{Instance: out.Instance}
	}
}

// RenderInstanceDetail renders a pretty detail view for an instance.
func RenderInstanceDetail(inst *types.Instance, width, height int) string {
	if inst == nil {
		return utils.RenderWithStatusBar(
			utils.TitleStyle.Render(" Loading instance details... "),
			" esc:back ",
			width, height,
		)
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	label := lipgloss.NewStyle().Width(22).Foreground(lipgloss.Color("245"))
	val := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	section := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	var b strings.Builder

	// Header
	name := deref(inst.Name)
	state := ""
	if inst.State != nil {
		state = deref(inst.State.Name)
	}
	stateStyled := dim.Render(state)
	if state == "running" {
		stateStyled = green.Render("● " + state)
	} else if state == "stopped" {
		stateStyled = red.Render("○ " + state)
	}
	b.WriteString(title.Render("  "+name) + "  " + stateStyled + "\n")
	b.WriteString(dim.Render("  "+deref(inst.Arn)) + "\n\n")

	// General
	b.WriteString(section.Render("  ─── General ") + "\n")
	row := func(l, v string) { b.WriteString("  " + label.Render(l) + val.Render(v) + "\n") }

	row("Blueprint:", deref(inst.BlueprintName)+" ("+deref(inst.BlueprintId)+")")
	row("Bundle:", deref(inst.BundleId))
	if inst.CreatedAt != nil {
		age := formatAge(*inst.CreatedAt)
		row("Created:", inst.CreatedAt.Format("2006-01-02 15:04 MST")+" ("+age+")")
	}
	row("Region:", derefRegion(inst.Location))
	row("AZ:", derefAZ(inst.Location))
	row("SSH User:", deref(inst.Username))
	row("SSH Key:", deref(inst.SshKeyName))
	b.WriteString("\n")

	// Hardware
	b.WriteString(section.Render("  ─── Hardware ") + "\n")
	if inst.Hardware != nil {
		cpu := int32(0)
		if inst.Hardware.CpuCount != nil {
			cpu = *inst.Hardware.CpuCount
		}
		ram := float32(0)
		if inst.Hardware.RamSizeInGb != nil {
			ram = *inst.Hardware.RamSizeInGb
		}
		row("CPU:", fmt.Sprintf("%d vCPU", cpu))
		row("RAM:", fmt.Sprintf("%.1f GB", ram))
		for _, d := range inst.Hardware.Disks {
			if d.IsSystemDisk != nil && *d.IsSystemDisk {
				size := int32(0)
				if d.SizeInGb != nil {
					size = *d.SizeInGb
				}
				iops := int32(0)
				if d.Iops != nil {
					iops = *d.Iops
				}
				row("Disk:", fmt.Sprintf("%d GB SSD (%d IOPS)", size, iops))
			}
		}
	}
	b.WriteString("\n")

	// Networking
	b.WriteString(section.Render("  ─── Networking ") + "\n")
	row("Public IP:", deref(inst.PublicIpAddress))
	row("Private IP:", deref(inst.PrivateIpAddress))
	if len(inst.Ipv6Addresses) > 0 {
		row("IPv6:", inst.Ipv6Addresses[0])
	}
	row("Static IP:", fmt.Sprintf("%v", inst.IsStaticIp != nil && *inst.IsStaticIp))
	row("IP Type:", string(inst.IpAddressType))
	if inst.Networking != nil && inst.Networking.MonthlyTransfer != nil && inst.Networking.MonthlyTransfer.GbPerMonthAllocated != nil {
		row("Transfer:", fmt.Sprintf("%d GB/mo", *inst.Networking.MonthlyTransfer.GbPerMonthAllocated))
	}
	b.WriteString("\n")

	// Firewall
	if inst.Networking != nil && len(inst.Networking.Ports) > 0 {
		b.WriteString(section.Render("  ─── Firewall ") + "\n")
		for _, p := range inst.Networking.Ports {
			proto := string(p.Protocol)
			port := ""
			from := p.FromPort
			to := p.ToPort
			if from == to {
				port = fmt.Sprintf("%d", from)
			} else {
				port = fmt.Sprintf("%d-%d", from, to)
			}
			source := deref(p.AccessFrom)
			b.WriteString("  " + label.Render(proto+"/"+port) + dim.Render(source) + "\n")
		}
		b.WriteString("\n")
	}

	// Add-ons
	if len(inst.AddOns) > 0 {
		b.WriteString(section.Render("  ─── Add-ons ") + "\n")
		for _, a := range inst.AddOns {
			status := deref(a.Status)
			statusStyled := dim.Render(status)
			if status == "Enabled" || status == "enabled" {
				statusStyled = green.Render(status)
			}
			row(deref(a.Name)+":", statusStyled)
		}
		b.WriteString("\n")
	}

	// Tags
	if len(inst.Tags) > 0 {
		b.WriteString(section.Render("  ─── Tags ") + "\n")
		for _, t := range inst.Tags {
			k := deref(t.Key)
			v := deref(t.Value)
			if v == "" {
				row(k, "(no value)")
			} else {
				row(k+":", v)
			}
		}
		b.WriteString("\n")
	}

	help := " esc:back  s:stop/start  d:delete  x:shell "
	return utils.RenderWithStatusBar(b.String(), help, width, height)
}

func deref(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

func derefRegion(loc *types.ResourceLocation) string {
	if loc == nil || loc.RegionName == "" {
		return "-"
	}
	return string(loc.RegionName)
}

func derefAZ(loc *types.ResourceLocation) string {
	if loc == nil {
		return "-"
	}
	return deref(loc.AvailabilityZone)
}

func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		return fmt.Sprintf("%dh%dm", h, m)
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		y := int(d.Hours() / (24 * 365))
		mo := int(d.Hours()/(24*30)) % 12
		return fmt.Sprintf("%dy%dmo", y, mo)
	}
}
