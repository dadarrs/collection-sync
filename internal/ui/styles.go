package ui

import (
	"charm.land/lipgloss/v2"
)

// Theme holds all named styles used across the CLI output.
type Theme struct {
	// Table styles.
	HeaderStyle lipgloss.Style
	CellStyle   lipgloss.Style
	OddRow      lipgloss.Style
	EvenRow     lipgloss.Style
	BorderStyle lipgloss.Style

	// Status colors used for check/sync result labels.
	StatusAdded       lipgloss.Style
	StatusUpdated     lipgloss.Style
	StatusExisting    lipgloss.Style
	StatusPresent     lipgloss.Style
	StatusFailed      lipgloss.Style
	StatusMissing     lipgloss.Style
	StatusSkipped     lipgloss.Style
	StatusUnmonitored lipgloss.Style
	StatusDryRun      lipgloss.Style

	// General-purpose styles.
	Title    lipgloss.Style
	Subtitle lipgloss.Style
	Dim      lipgloss.Style
	Bold     lipgloss.Style
}

// statusStyle maps a status string constant to its themed style.
func (t Theme) StatusStyle(status string) lipgloss.Style {
	switch status {
	case "added":
		return t.StatusAdded
	case "updated":
		return t.StatusUpdated
	case "existing":
		return t.StatusExisting
	case "present":
		return t.StatusPresent
	case "failed":
		return t.StatusFailed
	case "missing-movie", "missing-series", "missing-season":
		return t.StatusMissing
	case "skipped":
		return t.StatusSkipped
	case "unmonitored":
		return t.StatusUnmonitored
	case "would-add", "would-update":
		return t.StatusDryRun
	default:
		return lipgloss.NewStyle()
	}
}

// DefaultTheme returns a color theme suited for TTY output.
func DefaultTheme() Theme {
	purple := lipgloss.Color("99")
	gray := lipgloss.Color("245")
	lightGray := lipgloss.Color("241")

	return Theme{
		HeaderStyle: lipgloss.NewStyle().Bold(true).Foreground(purple),
		CellStyle:   lipgloss.NewStyle().PaddingRight(1),
		OddRow:      lipgloss.NewStyle().Foreground(gray),
		EvenRow:     lipgloss.NewStyle().Foreground(lightGray),
		BorderStyle: lipgloss.NewStyle().Foreground(purple),

		StatusAdded:       lipgloss.NewStyle().Foreground(lipgloss.Color("42")),  // green
		StatusUpdated:     lipgloss.NewStyle().Foreground(lipgloss.Color("42")),  // green
		StatusExisting:    lipgloss.NewStyle().Foreground(lipgloss.Color("245")), // dim grey
		StatusPresent:     lipgloss.NewStyle().Foreground(lipgloss.Color("42")),  // green
		StatusFailed:      lipgloss.NewStyle().Foreground(lipgloss.Color("196")), // red
		StatusMissing:     lipgloss.NewStyle().Foreground(lipgloss.Color("196")), // red
		StatusSkipped:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")), // dim grey
		StatusUnmonitored: lipgloss.NewStyle().Foreground(lipgloss.Color("214")), // yellow/orange
		StatusDryRun:      lipgloss.NewStyle().Foreground(lipgloss.Color("214")), // yellow/orange

		Title:    lipgloss.NewStyle().Bold(true),
		Subtitle: lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		Dim:      lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
		Bold:     lipgloss.NewStyle().Bold(true),
	}
}

// PlainTheme returns a no-op theme for non-TTY output. All styles are empty
// so Render() returns the input string unchanged.
func PlainTheme() Theme {
	return Theme{}
}
