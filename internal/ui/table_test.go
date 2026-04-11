package ui

import (
	"reflect"
	"strings"
	"testing"
)

const (
	movie2Title = "Movie 2"
	detailText  = "detail text"
	otherDetail = "other detail"
)

func TestRenderDetailList(t *testing.T) {
	tbl := &Table{
		headers:   []string{"#", "TITLE", "TMDB", "STATUS", "DETAIL"},
		rows:      [][]string{{"1", "Bone Tomahawk", "294963", "would-add", "would add Bone Tomahawk with profile 2160p Remux in /media/movies-dav; would ask Radarr to search for the movie after add"}},
		theme:     DefaultTheme(),
		tty:       true,
		width:     100,
		statusCol: 3,
	}

	got := tbl.renderStyled()
	if strings.Contains(got, "│") || strings.Contains(got, "┆") {
		t.Fatalf("renderStyled() unexpectedly rendered column borders: %q", got)
	}
	if !strings.Contains(got, "detail:") {
		t.Fatalf("renderStyled() missing detail prefix: %q", got)
	}
	if !strings.Contains(got, "would-add") {
		t.Fatalf("renderStyled() missing status: %q", got)
	}
}

func TestWrapDetailBreaksSemicolonClauses(t *testing.T) {
	lines := wrapDetail("already in Radarr as Movie; monitoring already enabled", 24)
	if len(lines) < 2 {
		t.Fatalf("wrapDetail() line count = %d, want at least 2 (%v)", len(lines), lines)
	}
	if lines[0] != "already in Radarr as" && !strings.Contains(lines[0], "already in Radarr") {
		t.Fatalf("wrapDetail() first line = %q", lines[0])
	}
	if !strings.Contains(strings.Join(lines, "\n"), "monitoring already") {
		t.Fatalf("wrapDetail() missing second clause: %v", lines)
	}
}

func TestRenderPlainDetailList(t *testing.T) {
	tbl := &Table{
		headers:   []string{"#", "TITLE", "TMDB", "STATUS", "DETAIL"},
		rows:      [][]string{{"1", "Blade of the Immortal", "426284", "existing", "already in Radarr as Blade of the Immortal; monitoring already enabled"}},
		theme:     PlainTheme(),
		tty:       false,
		width:     90,
		statusCol: 3,
	}

	got := tbl.renderPlain()
	if !strings.Contains(got, "detail:") {
		t.Fatalf("renderPlain() missing detail prefix: %q", got)
	}
	if !strings.Contains(got, "monitoring already enabled") {
		t.Fatalf("renderPlain() missing wrapped detail text: %q", got)
	}
	if strings.Contains(got, "│") {
		t.Fatalf("renderPlain() unexpectedly rendered border glyphs: %q", got)
	}
	if strings.Contains(got, "─") {
		t.Fatalf("renderPlain() unexpectedly rendered Unicode separators: %q", got)
	}
}

func TestTableHelperLookups(t *testing.T) {
	tbl := &Table{
		headers:   []string{"#", "TITLE", "STATUS", "DETAIL"},
		rows:      [][]string{{"1", "Movie", "added", detailText}, {"2", movie2Title, "failed", otherDetail}},
		theme:     DefaultTheme(),
		tty:       true,
		width:     80,
		statusCol: 2,
	}

	if got := tbl.columnIndex("STATUS"); got != 2 {
		t.Fatalf("columnIndex(STATUS) = %d, want 2", got)
	}
	if got := tbl.columnIndex("MISSING"); got != -1 {
		t.Fatalf("columnIndex(MISSING) = %d, want -1", got)
	}

	if got, ok := tbl.rowValue(tbl.rows[0], -1); ok || got != "" {
		t.Fatalf("rowValue(negative) = (%q, %t), want (\"\", false)", got, ok)
	}
	if got, ok := tbl.rowValue(tbl.rows[0], len(tbl.rows[0])); ok || got != "" {
		t.Fatalf("rowValue(out-of-bounds) = (%q, %t), want (\"\", false)", got, ok)
	}
	if got, ok := tbl.rowValue(tbl.rows[0], 2); !ok || got != "added" {
		t.Fatalf("rowValue(valid) = (%q, %t), want (\"added\", true)", got, ok)
	}
}

func TestTableHelperWidths(t *testing.T) {
	tbl := &Table{
		headers:   []string{"#", "TITLE", "STATUS", "DETAIL"},
		rows:      [][]string{{"1", "Movie", "added", detailText}, {"2", movie2Title, "failed", otherDetail}},
		theme:     DefaultTheme(),
		tty:       true,
		width:     80,
		statusCol: 2,
	}

	widths := tbl.columnWidths([]int{0, 1, 2})
	if len(widths) != 3 {
		t.Fatalf("columnWidths() len = %d, want 3", len(widths))
	}
	if widths[0] < 1 || widths[1] < 5 || widths[2] < 5 {
		t.Fatalf("columnWidths() = %v", widths)
	}

	if got := tbl.detailWidth(len("    detail: ")); got < 36 {
		t.Fatalf("detailWidth() = %d, want >= 36", got)
	}
	if got := (&Table{width: 20}).detailWidth(12); got != 36 {
		t.Fatalf("detailWidth(min) = %d, want 36", got)
	}
	if got := tbl.visibleLineWidth([]int{3, 10, 8}); got != 25 {
		t.Fatalf("visibleLineWidth() = %d, want 25", got)
	}
}

func TestTableHelperStylesAndFormatting(t *testing.T) {
	tbl := &Table{
		headers:   []string{"#", "TITLE", "STATUS", "DETAIL"},
		rows:      [][]string{{"1", "Movie", "added", detailText}, {"2", movie2Title, "failed", otherDetail}},
		theme:     DefaultTheme(),
		tty:       true,
		width:     80,
		statusCol: 2,
	}

	if got := tbl.rowStyle(0, 2, "added").Render("added"); got == "" {
		t.Fatal("rowStyle(status) rendered empty string")
	}
	if got := tbl.rowStyle(1, 1, movie2Title).Render(movie2Title); got == "" {
		t.Fatal("rowStyle(odd row) rendered empty string")
	}

	if got := FormatInt(7); got != "7" {
		t.Fatalf("FormatInt(7) = %q", got)
	}
	if got := padRight("abc", 5); got != "abc  " {
		t.Fatalf("padRight() = %q", got)
	}
	if got := padRight("abcdef", 3); got != "abcdef" {
		t.Fatalf("padRight(no pad) = %q", got)
	}
}

func TestWrapWords(t *testing.T) {
	if got := wrapWords("short text", 20); len(got) != 1 || got[0] != "short text" {
		t.Fatalf("wrapWords(short) = %v", got)
	}
	if wrapped := wrapWords("this is a somewhat longer line", 10); len(wrapped) < 2 {
		t.Fatalf("wrapWords(long) = %v, want multiple lines", wrapped)
	}
	if got := wrapWords("", 10); len(got) != 1 || got[0] != "" {
		t.Fatalf("wrapWords(empty) = %v", got)
	}
}

func TestTableNumericHelpers(t *testing.T) {
	if got := minInt(1, 2); got != 1 {
		t.Fatalf("minInt() = %d, want 1", got)
	}
	if maxValue := maxInt(1, 2); maxValue != 2 {
		t.Fatalf("maxInt() = %d, want 2", maxValue)
	}
	if got := columnCap("TITLE"); got != 28 {
		t.Fatalf("columnCap(TITLE) = %d, want 28", got)
	}
	if got := columnCap("OTHER"); got != 24 {
		t.Fatalf("columnCap(OTHER) = %d, want 24", got)
	}
}

func TestRenderAndNewTable(t *testing.T) {
	r := &Renderer{tty: false, theme: PlainTheme()}
	tbl := r.NewTable([]string{"#", "TITLE"}, -1)
	tbl.AddRow("1", "Movie")
	if got := tbl.Render(); !strings.Contains(got, "TITLE") || !strings.Contains(got, "Movie") {
		t.Fatalf("Render() = %q", got)
	}

	styled := &Table{
		headers: []string{"#", "TITLE"},
		rows:    [][]string{{"1", "Movie"}},
		theme:   DefaultTheme(),
		tty:     true,
		width:   60,
	}
	if got := styled.Render(); !strings.Contains(got, "TITLE") || !strings.Contains(got, "Movie") {
		t.Fatalf("styled Render() = %q", got)
	}
}

func TestAddRowNormalizesColumnCount(t *testing.T) {
	r := &Renderer{tty: false, theme: PlainTheme()}
	tbl := r.NewTable([]string{"#", "TITLE", "STATUS"}, 2)

	if got, want := tbl.normalizeRow([]string{"1", "Movie"}), []string{"1", "Movie", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeRow(short) = %#v, want %#v", got, want)
	}
	if got, want := tbl.normalizeRow([]string{"2", "Movie 2", "added", "ignored"}), []string{"2", "Movie 2", "added"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("normalizeRow(long) = %#v, want %#v", got, want)
	}
}

func TestRenderStyledHandlesShortRows(t *testing.T) {
	tbl := &Table{
		headers:   []string{"#", "TITLE", "STATUS"},
		rows:      [][]string{{"1", "Movie"}, {"2", "Movie 2", "failed"}},
		theme:     DefaultTheme(),
		tty:       true,
		width:     60,
		statusCol: 2,
	}

	if got := tbl.Render(); !strings.Contains(got, "Movie") || !strings.Contains(got, "Movie 2") {
		t.Fatalf("Render() = %q", got)
	}
}
