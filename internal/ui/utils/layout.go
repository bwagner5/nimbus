package utils

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// Overlay renders a modal string centered over a base string.
// The base is kept visible (not dimmed) and the modal is placed on top.
func Overlay(base, modal string, width, height int) string {
	baseLines := strings.Split(base, "\n")
	for len(baseLines) < height {
		baseLines = append(baseLines, "")
	}
	baseLines = baseLines[:height]

	modalStyled := ModalStyle.Render(modal)
	modalLines := strings.Split(modalStyled, "\n")
	modalH := len(modalLines)
	modalW := lipgloss.Width(modalStyled)

	startY := (height - modalH) / 2
	startX := (width - modalW) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	result := make([]string, height)
	for y := 0; y < height; y++ {
		if y >= startY && y < startY+modalH {
			mIdx := y - startY
			pad := strings.Repeat(" ", startX)
			result[y] = pad + modalLines[mIdx]
		} else {
			result[y] = baseLines[y]
		}
	}
	return strings.Join(result, "\n")
}

// CenterModal renders a modal string centered on a dim background.
func CenterModal(modal string, width, height int) string {
	modalLines := strings.Split(modal, "\n")
	modalH := len(modalLines)
	modalW := lipgloss.Width(modal)
	startY := (height - modalH) / 2
	startX := (width - modalW) / 2
	if startY < 0 {
		startY = 0
	}
	if startX < 0 {
		startX = 0
	}

	var result []string
	bg := strings.Repeat(" ", width)
	for y := 0; y < height; y++ {
		if y >= startY && y < startY+modalH {
			mIdx := y - startY
			left := strings.Repeat(" ", startX)
			result = append(result, left+modalLines[mIdx])
		} else {
			result = append(result, bg)
		}
	}
	return strings.Join(result, "\n")
}

// FormatRow formats columns to fit within the given width.
func FormatRow(cols []string, width int) string {
	colWidth := width / len(cols)
	if colWidth < 10 {
		colWidth = 10
	}
	var parts []string
	for _, c := range cols {
		if len(c) > colWidth-2 {
			c = c[:colWidth-2]
		}
		parts = append(parts, fmt.Sprintf("%-*s", colWidth, c))
	}
	return strings.Join(parts, "")
}

func truncateToWidth(s string, w int) string {
	var result strings.Builder
	visible := 0
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
		}
		if inEscape {
			result.WriteRune(r)
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if visible >= w {
			break
		}
		result.WriteRune(r)
		visible++
	}
	for visible < w {
		result.WriteRune(' ')
		visible++
	}
	return result.String()
}

func padOrTruncate(s string, start, end int) string {
	var result strings.Builder
	visible := 0
	inEscape := false
	started := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
		}
		if inEscape {
			if started {
				result.WriteRune(r)
			}
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		if visible >= end {
			break
		}
		if visible >= start {
			started = true
			result.WriteRune(r)
		}
		visible++
	}
	for visible < end {
		if visible >= start {
			result.WriteRune(' ')
		}
		visible++
	}
	return result.String()
}
