package ui

import (
	"bytes"
	"os"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

func TestNewUsesPlainThemeForNonTerminalWriters(t *testing.T) {
	var buf bytes.Buffer
	r := New(&buf)

	if r == nil {
		t.Fatal("New() = nil")
	}
	if r.IsTTY() {
		t.Fatal("IsTTY() = true, want false for bytes.Buffer")
	}
	if r.Writer() == nil {
		t.Fatal("Writer() = nil")
	}
	if r.Writer().Profile != colorprofile.NoTTY {
		t.Fatalf("Writer().Profile = %v, want %v", r.Writer().Profile, colorprofile.NoTTY)
	}
	if got := r.Theme().StatusStyle("added").Render("added"); got != "added" {
		t.Fatalf("plain theme rendered %q, want unchanged text", got)
	}
	if got := r.Width(); got != 0 {
		t.Fatalf("Width() = %d, want 0 for non-file writer", got)
	}
}

func TestRendererHelpers(t *testing.T) {
	if got := (*Renderer)(nil).Width(); got != 0 {
		t.Fatalf("nil Width() = %d, want 0", got)
	}

	r := Stdout()
	if r == nil {
		t.Fatal("Stdout() = nil")
	}
	if r.out != os.Stdout {
		t.Fatalf("Stdout().out = %v, want os.Stdout", r.out)
	}
	if r.Writer() == nil {
		t.Fatal("Stdout().Writer() = nil")
	}
	if got := r.Theme().StatusStyle("unknown").Render("value"); got != "value" {
		t.Fatalf("Theme() returned unexpected theme value: %q", got)
	}
}
