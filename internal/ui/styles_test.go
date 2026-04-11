package ui

import "testing"

func TestDefaultThemeStatusStyleMappings(t *testing.T) {
	theme := DefaultTheme()
	cases := []struct {
		status string
		want   string
	}{
		{status: "added", want: "added"},
		{status: "updated", want: "updated"},
		{status: "existing", want: "existing"},
		{status: "present", want: "present"},
		{status: "failed", want: "failed"},
		{status: "missing-movie", want: "missing-movie"},
		{status: "missing-series", want: "missing-series"},
		{status: "missing-season", want: "missing-season"},
		{status: "skipped", want: "skipped"},
		{status: "unmonitored", want: "unmonitored"},
		{status: "would-add", want: "would-add"},
		{status: "would-update", want: "would-update"},
	}

	for _, tc := range cases {
		if theme.StatusStyle(tc.status).Render(tc.want) == "" {
			t.Fatalf("StatusStyle(%q) rendered empty string", tc.status)
		}
	}

	if got := theme.StatusStyle("unknown").Render("value"); got != "value" {
		t.Fatalf("StatusStyle(unknown) = %q, want unchanged text", got)
	}
}

func TestPlainThemeLeavesTextUnchanged(t *testing.T) {
	theme := PlainTheme()
	if got := theme.StatusStyle("added").Render("added"); got != "added" {
		t.Fatalf("plain StatusStyle() = %q, want unchanged text", got)
	}
	if got := theme.Title.Render("title"); got != "title" {
		t.Fatalf("plain Title.Render() = %q, want unchanged text", got)
	}
}
