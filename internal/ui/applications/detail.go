package applications

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/wagnerbm/nimbusv2/internal/applications"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// RenderAppDetail renders the application detail view.
func RenderAppDetail(detail *applications.Detail, width int) string {
	if detail == nil {
		return utils.TitleStyle.Render(" Loading application details... ")
	}

	title := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	label := lipgloss.NewStyle().Width(22).Foreground(lipgloss.Color("245"))
	val := lipgloss.NewStyle().Foreground(lipgloss.Color("255"))
	section := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	green := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	var b strings.Builder

	stateStyled := green.Render("● " + detail.State)
	b.WriteString(title.Render("  "+detail.Name) + "  " + stateStyled + "\n")
	b.WriteString(dim.Render("  bucket: "+detail.Bucket) + "\n\n")

	row := func(l, v string) { b.WriteString("  " + label.Render(l) + val.Render(v) + "\n") }

	b.WriteString(section.Render("  ─── General ") + "\n")
	row("Region:", detail.Region)
	row("Bucket:", detail.Bucket)
	row("Environments:", fmt.Sprintf("%d", len(detail.Environments)))
	totalTargets := 0
	for _, e := range detail.Environments {
		totalTargets += len(e.Targets)
	}
	row("Targets:", fmt.Sprintf("%d", totalTargets))
	b.WriteString("\n")

	if len(detail.Environments) > 0 {
		for _, env := range detail.Environments {
			b.WriteString(section.Render(fmt.Sprintf("  ─── Environment: %s ", env.Name)) + "\n")
			if len(env.Targets) == 0 {
				b.WriteString(dim.Render("    No deployment targets") + "\n")
			} else {
				targetLabel := lipgloss.NewStyle().Width(24).Foreground(lipgloss.Color("255"))
				for _, t := range env.Targets {
					stateStr := dim.Render(t.State)
					if t.State == "running" {
						stateStr = green.Render("● " + t.State)
					}
					ip := dim.Render(t.IP)
					b.WriteString("    " + targetLabel.Render(t.Name) + stateStr + "  " + ip + "\n")
				}
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString(section.Render("  ─── Environments ") + "\n")
		b.WriteString(dim.Render("    No environments configured") + "\n\n")
	}

	return b.String()
}
