package ui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
)

// Progress renders an in-place progress bar to a writer.
type Progress struct {
	w     io.Writer
	label string
	total int
	model progress.Model
	tty   bool
}

// NewProgress creates a progress indicator that writes to w.
func (r *Renderer) NewProgress(w io.Writer, label string, total int) *Progress {
	opts := []progress.Option{
		progress.WithWidth(40),
		progress.WithColors(lipgloss.Color("99"), lipgloss.Color("42")),
	}
	if !r.tty {
		opts = []progress.Option{
			progress.WithWidth(40),
			progress.WithoutPercentage(),
		}
	}
	m := progress.New(opts...)
	return &Progress{
		w:     w,
		label: label,
		total: total,
		model: m,
		tty:   r.tty,
	}
}

// Update renders the progress bar at current/total. TTY output uses carriage
// return to update in place and writes a final newline on completion; non-TTY
// output is emitted as newline-delimited progress updates.
func (p *Progress) Update(current int) {
	if p.total == 0 {
		return
	}
	pct := float64(current) / float64(p.total)
	if p.tty {
		bar := p.model.ViewAs(pct)
		// Use carriage return for in-place update, clear the line first.
		fmt.Fprintf(p.w, "\r%s %s %d/%d", p.label, bar, current, p.total)
		if current == p.total {
			fmt.Fprintln(p.w)
		}
		return
	}

	fmt.Fprintf(p.w, "%s: %d/%d\n", p.label, current, p.total)
}

// StatusSummary renders a list of status counts using themed colors.
func StatusSummary(w io.Writer, theme Theme, tty bool, statusCounts map[string]int, orderedStatuses []string) {
	var parts []string
	for _, status := range orderedStatuses {
		count := statusCounts[status]
		if count == 0 {
			continue
		}
		label := fmt.Sprintf("%s: %d", status, count)
		if tty {
			label = theme.StatusStyle(status).Render(label)
		}
		parts = append(parts, label)
	}
	if len(parts) > 0 {
		fmt.Fprintln(w, strings.Join(parts, "  "))
	}
}
