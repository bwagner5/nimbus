package instances

import (
	"fmt"
	"strings"

	"github.com/wagnerbm/nimbusv2/internal/resources"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

// RenderResources renders the main resource list view with help pinned to the bottom.
func RenderResources(provider resources.Provider, filtered []resources.Resource, cursor int, region, filter, progress, spinnerView string, width, height int, loading bool, viewFilter bool) string {
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
		} else if r.Status() == "running" || r.Status() == "available" {
			row = utils.RunningStyle.Render(row)
		} else if r.Status() == "stopped" {
			row = utils.StoppedStyle.Render(row)
		}
		b.WriteString(row + "\n")
	}
	if len(filtered) == 0 && !loading {
		b.WriteString(utils.HelpStyle.Render("  No resources found\n"))
	}

	content := b.String()

	sLabel := "s:stop"
	if len(filtered) > 0 && cursor < len(filtered) && filtered[cursor].Status() == "stopped" {
		sLabel = "s:start"
	}
	help := fmt.Sprintf(" q:quit  /:filter  ::resources  r:regions  enter:details  c:create  %s  d:delete  x:shell  R:refresh  j/k:navigate ", sLabel)

	if progress != "" {
		progressLine := utils.HelpStyle.Width(width).Render(" " + spinnerView + " " + progress)
		// Reserve 2 lines at bottom: progress + help
		lines := strings.Split(content, "\n")
		ph := height - 2
		for len(lines) < ph {
			lines = append(lines, "")
		}
		if len(lines) > ph {
			lines = lines[:ph]
		}
		content = strings.Join(lines, "\n") + "\n" + progressLine
	}

	return utils.RenderWithStatusBar(content, help, width, height)
}

// RenderRegionModal renders the region selection modal content.
func RenderRegionModal(regions []string, regCursor int) string {
	var b strings.Builder
	b.WriteString(utils.TitleStyle.Render(" Select Region ") + "\n\n")
	visible := 10
	start := regCursor - visible/2
	if start < 0 {
		start = 0
	}
	end := start + visible
	if end > len(regions) {
		end = len(regions)
		start = end - visible
		if start < 0 {
			start = 0
		}
	}
	for i := start; i < end; i++ {
		r := regions[i]
		if i == regCursor {
			b.WriteString(utils.SelectedStyle.Render(fmt.Sprintf(" > %s ", r)) + "\n")
		} else {
			b.WriteString(fmt.Sprintf("   %s\n", r))
		}
	}
	b.WriteString("\n" + utils.HelpStyle.Render(" enter:select  esc:cancel "))
	return b.String()
}

// RenderProviderModal renders the resource type selection modal content.
func RenderProviderModal(providers []resources.Provider, provCursor int) string {
	var b strings.Builder
	b.WriteString(utils.TitleStyle.Render(" Select Resource ") + "\n\n")
	maxLen := 0
	for _, p := range providers {
		if len(p.Kind()) > maxLen {
			maxLen = len(p.Kind())
		}
	}
	for i, p := range providers {
		if i == provCursor {
			b.WriteString(utils.SelectedStyle.Render(fmt.Sprintf(" > %-*s ", maxLen, p.Kind())) + "\n")
		} else {
			b.WriteString(fmt.Sprintf("   %-*s\n", maxLen, p.Kind()))
		}
	}
	b.WriteString("\n" + utils.HelpStyle.Render(" enter:select  esc:cancel "))
	return b.String()
}

// ApplyFilter filters resources by the given filter string.
func ApplyFilter(all []resources.Resource, filter string) []resources.Resource {
	if filter == "" {
		return all
	}
	f := strings.ToLower(filter)
	var filtered []resources.Resource
	for _, r := range all {
		for _, v := range r.Values() {
			if strings.Contains(strings.ToLower(v), f) {
				filtered = append(filtered, r)
				break
			}
		}
	}
	return filtered
}

// MergeResources merges incoming resources into existing ones.
// If region is set, stale entries from that region are removed.
func MergeResources(existing, incoming []resources.Resource, region string) []resources.Resource {
	if region != "" {
		incomingIDs := make(map[string]bool, len(incoming))
		for _, r := range incoming {
			incomingIDs[r.ID()] = true
		}
		var kept []resources.Resource
		for _, r := range existing {
			if r.Region() != region || incomingIDs[r.ID()] {
				kept = append(kept, r)
			}
		}
		idx := make(map[string]int, len(kept))
		for i, r := range kept {
			idx[r.ID()] = i
		}
		for _, r := range incoming {
			if i, ok := idx[r.ID()]; ok {
				kept[i] = r
			} else {
				kept = append(kept, r)
			}
		}
		return kept
	}
	idx := make(map[string]int, len(existing))
	merged := make([]resources.Resource, len(existing))
	copy(merged, existing)
	for i, r := range merged {
		idx[r.ID()] = i
	}
	for _, r := range incoming {
		if i, ok := idx[r.ID()]; ok {
			merged[i] = r
		} else {
			idx[r.ID()] = len(merged)
			merged = append(merged, r)
		}
	}
	return merged
}
