package applications

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// TargetEntry is a flattened target with its env context, for cursor indexing.
type TargetEntry struct {
	EnvName     string
	Target      applications.Target
	IsAddTarget bool // sentinel: "add deployment target" action row
}

// AppDetailTargets returns a flat list of all targets across environments,
// with an "add target" entry appended to each env.
func AppDetailTargets(detail *applications.Detail) []TargetEntry {
	if detail == nil {
		return nil
	}
	var entries []TargetEntry
	for _, env := range detail.Environments {
		for _, t := range env.Targets {
			entries = append(entries, TargetEntry{EnvName: env.Name, Target: t})
		}
		entries = append(entries, TargetEntry{EnvName: env.Name, IsAddTarget: true})
	}
	return entries
}

// RenderAppDetail renders the application detail view with an optional cursor highlight.
func RenderAppDetail(detail *applications.Detail, cursor int, width int) string {
	if detail == nil {
		return utils.TitleStyle.Render(" Loading application details... ")
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	label := lipgloss.NewStyle().Width(22).Foreground(lipgloss.Color("245"))
	val := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	section := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	yellow := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	red := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	cyan := lipgloss.NewStyle().Foreground(lipgloss.Color("87"))
	highlight := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))

	var b strings.Builder

	stateStyled := green.Render("● " + detail.State)
	b.WriteString(title.Render("  "+detail.Name) + "  " + stateStyled + "\n")
	b.WriteString(dim.Render("  bucket: "+detail.Bucket) + "\n\n")

	row := func(l, v string) { b.WriteString("  " + label.Render(l) + val.Render(v) + "\n") }

	b.WriteString(section.Render("  ─── General ") + "\n")
	row("Region:", detail.Region)
	row("Environments:", fmt.Sprintf("%d", len(detail.Environments)))
	totalTargets := 0
	for _, e := range detail.Environments {
		totalTargets += len(e.Targets)
	}
	row("Targets:", fmt.Sprintf("%d", totalTargets))
	b.WriteString("\n")

	if len(detail.Environments) == 0 {
		b.WriteString(section.Render("  ─── Environments ") + "\n")
		b.WriteString(dim.Render("    No environments configured") + "\n\n")
		return b.String()
	}

	targetIdx := 0
	for _, env := range detail.Environments {
		envHeader := fmt.Sprintf("  ─── Environment: %s ", env.Name)
		if env.Bucket != "" {
			envHeader += dim.Render("(" + env.Bucket + ") ")
		}
		b.WriteString(section.Render(envHeader) + "\n")

		for _, t := range env.Targets {
			selected := targetIdx == cursor
			targetIdx++

			stateStr := dim.Render(t.State)
			if t.State == "running" {
				stateStr = green.Render("● " + t.State)
			}

			prefix := "    "
			nameStyle := lipgloss.NewStyle().Width(24).Foreground(lipgloss.Color("255"))
			if selected {
				prefix = "  ▸ "
				nameStyle = highlight.Width(24)
			}
			b.WriteString(prefix + nameStyle.Render(t.Name) + stateStr + "  " + dim.Render(t.IP) + "\n")

			s := t.InstanceStatus
			if s == nil {
				b.WriteString(dim.Render("      No status reported") + "\n")
				continue
			}

			var statusStyled string
			switch s.Status {
			case "healthy":
				statusStyled = green.Render("● healthy")
			case "degraded":
				statusStyled = yellow.Render("◐ degraded")
			case "down":
				statusStyled = red.Render("○ down")
			default:
				statusStyled = dim.Render("◌ " + s.Status)
			}

			age := time.Since(s.Timestamp).Truncate(time.Second)
			b.WriteString(fmt.Sprintf("      Status: %s  %s\n", statusStyled, dim.Render(fmt.Sprintf("(last seen %s ago)", age))))

			if s.LastDeploy != nil {
				deployAge := ""
				if !s.LastDeploy.Timestamp.IsZero() {
					deployAge = fmt.Sprintf(" (%s ago)", time.Since(s.LastDeploy.Timestamp).Truncate(time.Second))
				}
				b.WriteString(fmt.Sprintf("      Last deploy: %s%s\n", dim.Render(s.LastDeploy.ObjectURL), dim.Render(deployAge)))
			}

			if len(s.Containers) > 0 {
				b.WriteString("      Containers:\n")
				for _, c := range s.Containers {
					cStatus := dim.Render(c.Status)
					if c.Status == "running" {
						cStatus = green.Render(c.Status)
					} else if c.Status == "exited" || c.Status == "dead" {
						cStatus = red.Render(c.Status)
					}
					uptime := ""
					if !c.StartedAt.IsZero() {
						uptime = dim.Render(fmt.Sprintf(" (up %s)", time.Since(c.StartedAt).Truncate(time.Second)))
					}
					b.WriteString(fmt.Sprintf("        %-20s %s  %s%s\n", c.Name, dim.Render(c.Image), cStatus, uptime))
				}
			}

			if len(s.Endpoints) > 0 {
				b.WriteString("      Endpoints:\n")
				for _, ep := range s.Endpoints {
					b.WriteString("        " + cyan.Render(ep) + "\n")
				}
			}
		}

		// "Add deployment target" action row
		selected := targetIdx == cursor
		targetIdx++
		prefix := "    "
		addStyle := dim
		if selected {
			prefix = "  ▸ "
			addStyle = highlight
		}
		b.WriteString(prefix + addStyle.Render("+ Add deployment target...") + "\n")
		b.WriteString("\n")
	}

	return b.String()
}
