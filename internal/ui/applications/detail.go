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
	EnvIdx      int // index in the environment order
	Target      applications.Target
	IsEnvHeader bool // env header row (selectable for reorder/promote)
	IsAddTarget bool // sentinel: "add deployment target" action row
	IsAddEnv    bool // sentinel: "add environment" action row
}

// AppDetailTargets returns a flat list of all targets across environments,
// with env headers, "add target" per env, and "add environment" at the end.
func AppDetailTargets(detail *applications.Detail) []TargetEntry {
	if detail == nil {
		return nil
	}
	var entries []TargetEntry
	for i, env := range detail.Environments {
		entries = append(entries, TargetEntry{EnvName: env.Name, EnvIdx: i, IsEnvHeader: true})
		for _, t := range env.Targets {
			entries = append(entries, TargetEntry{EnvName: env.Name, EnvIdx: i, Target: t})
		}
		entries = append(entries, TargetEntry{EnvName: env.Name, EnvIdx: i, IsAddTarget: true})
	}
	entries = append(entries, TargetEntry{IsAddEnv: true})
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
	for i, env := range detail.Environments {
		// Env header row (selectable)
		selected := targetIdx == cursor
		targetIdx++
		envHeader := fmt.Sprintf("─── Environment: %s ", env.Name)
		if env.Bucket != "" {
			envHeader += dim.Render("(" + env.Bucket + ") ")
		}
		if selected {
			envHeader = fmt.Sprintf("▸ ─── Environment: %s ", env.Name)
			if env.Bucket != "" {
				envHeader += dim.Render("(" + env.Bucket + ") ")
			}
			b.WriteString(highlight.Render("  "+envHeader) + "\n")
		} else {
			b.WriteString(section.Render("  "+envHeader) + "\n")
		}
		_ = i

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
				deployInfo := formatDeployInfo(s.LastDeploy.ObjectURL, s.LastDeploy.Timestamp)
				b.WriteString(fmt.Sprintf("      Last deploy: %s\n", dim.Render(deployInfo)))
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
		selected = targetIdx == cursor
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

	// "Add environment" action row
	{
		selected := targetIdx == cursor
		targetIdx++
		prefix := "    "
		addStyle := dim
		if selected {
			prefix = "  ▸ "
			addStyle = highlight
		}
		b.WriteString(prefix + addStyle.Render("+ Add environment...") + "\n")
	}
	_ = targetIdx

	return b.String()
}

// formatDeployInfo parses a deploy object URL or key like
// "deploy/<unix>-<commit>.tar.gz" and returns "<commit> at <RFC3339 local time>".
func formatDeployInfo(objectURL string, fallbackTS time.Time) string {
	// Extract the key portion after the last "deploy/"
	key := objectURL
	if idx := strings.LastIndex(key, "deploy/"); idx >= 0 {
		key = key[idx+len("deploy/"):]
	}
	// key is now like "1712345678-abc1234.tar.gz" or "1712345678-abc1234-rollback.tar.gz"
	key = strings.TrimSuffix(key, ".tar.gz")

	parts := strings.SplitN(key, "-", 2)
	if len(parts) != 2 {
		if !fallbackTS.IsZero() {
			return fallbackTS.Local().Format(time.RFC3339)
		}
		return objectURL
	}

	commit := parts[1]
	var ts time.Time
	var epoch int64
	if _, err := fmt.Sscanf(parts[0], "%d", &epoch); err == nil && epoch > 0 {
		ts = time.Unix(epoch, 0)
	} else {
		ts = fallbackTS
	}

	if ts.IsZero() {
		return commit
	}
	return fmt.Sprintf("%s at %s", commit, ts.Local().Format(time.RFC3339))
}

// RenderPromoteModal renders the promote destination selection modal.
func RenderPromoteModal(srcEnv string, destEnvs []string, cursor int) string {
	var b strings.Builder
	b.WriteString(utils.TitleStyle.Render(fmt.Sprintf(" Promote from %s ", srcEnv)) + "\n\n")
	for i, e := range destEnvs {
		if i == cursor {
			b.WriteString(utils.SelectedStyle.Render(fmt.Sprintf(" > %s ", e)) + "\n")
		} else {
			b.WriteString(fmt.Sprintf("   %s\n", e))
		}
	}
	b.WriteString("\n" + utils.HelpStyle.Render(" enter:promote  esc:cancel "))
	return b.String()
}
