package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// Table wraps a lipgloss table with convenience methods for building
// incrementally and optional status-column coloring.
type Table struct {
	headers   []string
	rows      [][]string
	theme     Theme
	tty       bool
	width     int
	statusCol int // column index to apply status styling (-1 = none)
}

// NewTable creates a table builder. Set statusCol to the 0-based column index
// that holds a status string (e.g. "added", "failed") for automatic color
// coding, or -1 to disable status styling.
func (r *Renderer) NewTable(headers []string, statusCol int) *Table {
	return &Table{
		headers:   headers,
		theme:     r.theme,
		tty:       r.tty,
		width:     r.Width(),
		statusCol: statusCol,
	}
}

// AddRow appends a data row.
func (t *Table) AddRow(values ...string) {
	t.rows = append(t.rows, values)
}

// Render returns the fully rendered table string.
func (t *Table) Render() string {
	if !t.tty {
		return t.renderPlain()
	}
	return t.renderStyled()
}

func (t *Table) renderStyled() string {
	if detailCol := t.columnIndex("DETAIL"); detailCol >= 0 {
		return t.renderDetailList(detailCol)
	}

	theme := t.theme
	statusCol := t.statusCol

	tbl := table.New().
		Border(lipgloss.NormalBorder()).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderColumn(false).
		BorderRow(false).
		BorderHeader(true).
		BorderStyle(theme.BorderStyle).
		BaseStyle(theme.CellStyle.PaddingRight(2)).
		Headers(t.headers...).
		Rows(t.rows...).
		Wrap(true).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return theme.HeaderStyle
			}
			// Apply status color to the status column.
			if statusCol >= 0 && col == statusCol && row >= 0 && row < len(t.rows) {
				val := t.rows[row][col]
				return theme.StatusStyle(val)
			}
			if row%2 == 0 {
				return theme.EvenRow
			}
			return theme.OddRow
		})

	if t.width > 0 {
		tbl.Width(t.width)
	}
	return tbl.String()
}

func (t *Table) renderDetailList(detailCol int) string {
	visibleCols := make([]int, 0, len(t.headers)-1)
	for i := range t.headers {
		if i != detailCol {
			visibleCols = append(visibleCols, i)
		}
	}

	widths := t.columnWidths(visibleCols)
	var out strings.Builder

	headers := make([]string, 0, len(visibleCols))
	for i, col := range visibleCols {
		headers = append(headers, t.theme.HeaderStyle.Render(padRight(t.headers[col], widths[i])))
	}
	out.WriteString(strings.Join(headers, "  "))
	out.WriteByte('\n')
	out.WriteString(t.theme.BorderStyle.Render(strings.Repeat("─", t.visibleLineWidth(widths))))
	out.WriteByte('\n')

	for rowIndex, row := range t.rows {
		parts := make([]string, 0, len(visibleCols))
		for i, col := range visibleCols {
			cell := padRight(row[col], widths[i])
			style := t.rowStyle(rowIndex, col, row[col])
			parts = append(parts, style.Render(cell))
		}
		out.WriteString(strings.Join(parts, "  "))

		detail := strings.TrimSpace(row[detailCol])
		if detail != "" {
			prefix := t.theme.Dim.Render("    detail: ")
			continuation := t.theme.Dim.Render("            ")
			lines := wrapDetail(detail, t.detailWidth(len("    detail: ")))
			for lineIndex, line := range lines {
				out.WriteByte('\n')
				if lineIndex == 0 {
					out.WriteString(prefix)
				} else {
					out.WriteString(continuation)
				}
				out.WriteString(t.theme.Dim.Render(line))
			}
		}

		if rowIndex < len(t.rows)-1 {
			out.WriteString("\n\n")
		}
	}

	return out.String()
}

func (t *Table) rowStyle(rowIndex, colIndex int, value string) lipgloss.Style {
	if t.statusCol >= 0 && colIndex == t.statusCol {
		return t.theme.StatusStyle(value).Bold(true)
	}
	if rowIndex%2 == 0 {
		return t.theme.EvenRow
	}
	return t.theme.OddRow
}

func (t *Table) columnIndex(header string) int {
	for i, candidate := range t.headers {
		if candidate == header {
			return i
		}
	}
	return -1
}

func (t *Table) columnWidths(cols []int) []int {
	widths := make([]int, len(cols))
	for i, col := range cols {
		widths[i] = minInt(maxInt(lipgloss.Width(t.headers[col]), 1), columnCap(t.headers[col]))
		for _, row := range t.rows {
			widths[i] = minInt(maxInt(widths[i], lipgloss.Width(row[col])), columnCap(t.headers[col]))
		}
	}
	return widths
}

func (t *Table) detailWidth(prefixWidth int) int {
	if t.width <= 0 {
		return 72
	}
	width := t.width - prefixWidth - 2
	if width < 36 {
		return 36
	}
	return width
}

func (t *Table) visibleLineWidth(widths []int) int {
	lineWidth := 0
	for i, width := range widths {
		lineWidth += width
		if i < len(widths)-1 {
			lineWidth += 2
		}
	}
	return lineWidth
}

// renderPlain produces tab-aligned output similar to the original tabwriter
// format, ensuring machine-readable output when stdout is not a terminal.
func (t *Table) renderPlain() string {
	if detailCol := t.columnIndex("DETAIL"); detailCol >= 0 {
		return t.renderDetailList(detailCol)
	}

	// Use lipgloss table with no border and no colors for clean plain text.
	tbl := table.New().
		Border(lipgloss.HiddenBorder()).
		Wrap(true).
		Headers(t.headers...).
		Rows(t.rows...)

	if t.width > 0 {
		tbl.Width(t.width)
	}

	return tbl.String()
}

// FormatInt converts an int64 to a string for table cells.
func FormatInt(v int64) string {
	return fmt.Sprintf("%d", v)
}

func columnCap(header string) int {
	switch header {
	case "#":
		return 3
	case "TMDB", "TVDB":
		return 8
	case "STATUS":
		return 12
	case "MATCH", "TYPE":
		return 10
	case "SEASON":
		return 14
	case "MONITOR":
		return 16
	case "RATING KEY":
		return 12
	case "TITLE", "SHOW":
		return 28
	default:
		return 24
	}
}

func padRight(value string, width int) string {
	padding := width - lipgloss.Width(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func wrapDetail(value string, width int) []string {
	segments := strings.Split(strings.ReplaceAll(value, "; ", ";\n"), "\n")
	lines := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		lines = append(lines, wrapWords(segment, width)...)
	}
	return lines
}

func wrapWords(value string, width int) []string {
	if width <= 0 || lipgloss.Width(value) <= width {
		return []string{value}
	}

	words := strings.Fields(value)
	if len(words) == 0 {
		return []string{""}
	}

	lines := make([]string, 0, 4)
	current := words[0]
	for _, word := range words[1:] {
		candidate := current + " " + word
		if lipgloss.Width(candidate) <= width {
			current = candidate
			continue
		}
		lines = append(lines, current)
		current = word
	}
	lines = append(lines, current)
	return lines
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
