package applications

import (
	"fmt"
	"strings"

	"github.com/wagnerbm/nimbusv2/internal/resources"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// RenderApplications renders the application list view.
func RenderApplications(provider resources.Provider, filtered []resources.Resource, cursor int, region, filter, progress, spinnerView string, width, height int, loading bool, viewFilter bool) string {
	var b strings.Builder
	title := fmt.Sprintf(" ☁ nimbus │ %s │ %s ", provider.Kind(), region)
	b.WriteString(utils.TitleStyle.Render(title) + "\n")

	if viewFilter || filter != "" {
		b.WriteString(utils.FilterStyle.Render(fmt.Sprintf(" /%s", filter)))
		if viewFilter {
			b.WriteString("█")
		}
	}
	b.WriteString("\n")

	headers := provider.Headers()
	b.WriteString(utils.HeaderStyle.Render(utils.FormatRow(headers, width)) + "\n")
	for i, r := range filtered {
		row := utils.FormatRow(r.Values(), width)
		if i == cursor {
			row = utils.SelectedStyle.Render(row)
		} else if r.Status() == "OK" || r.Status() == "active" {
			row = utils.RunningStyle.Render(row)
		}
		b.WriteString(row + "\n")
	}
	if len(filtered) == 0 && !loading {
		b.WriteString(utils.HelpStyle.Render("  No applications found\n"))
	}

	content := b.String()
	help := " q:quit  /:filter  ::resources  r:regions  enter:details  c:create  d:delete  R:refresh  j/k:navigate "

	return utils.RenderWithStatusBar(content, help, width, height)
}
