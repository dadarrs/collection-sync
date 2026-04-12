package ui

import (
	"strings"
	"testing"
)

func TestTextHelpersPlain(t *testing.T) {
	r := &Renderer{tty: false, theme: PlainTheme()}

	if got := r.Section("TV Sync"); got != "TV Sync" {
		t.Fatalf("Section() = %q", got)
	}
	if got := r.Notice("[dry-run]", "previewing changes only"); got != "[dry-run] previewing changes only" {
		t.Fatalf("Notice() = %q", got)
	}
	got := r.Fields("waiting for next run", []Field{{Label: "last run took", Value: "1.5s"}, {Label: "current time", Value: "2026-04-12 04:11:28 +00:00 UTC"}})
	want := "waiting for next run\nlast run took: 1.5s\ncurrent time: 2026-04-12 04:11:28 +00:00 UTC"
	if got != want {
		t.Fatalf("Fields() = %q, want %q", got, want)
	}
}

func TestTextHelpersTTY(t *testing.T) {
	r := &Renderer{tty: true, theme: DefaultTheme()}

	if got := r.Section("Movie Sync"); !strings.Contains(got, "Movie Sync") {
		t.Fatalf("Section() = %q", got)
	}
	if got := r.Notice("sync error:", "boom"); !strings.Contains(got, "sync error:") || !strings.Contains(got, "boom") {
		t.Fatalf("Notice() = %q", got)
	}
	if got := r.Fields("batch summary", []Field{{Label: "eligible", Value: "3"}}); !strings.Contains(got, "batch summary") || !strings.Contains(got, "eligible") || !strings.Contains(got, "3") {
		t.Fatalf("Fields() = %q", got)
	}
}
