package ui

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewProgressAndUpdatePlain(t *testing.T) {
	r := &Renderer{tty: false, theme: PlainTheme()}
	var buf bytes.Buffer
	p := r.NewProgress(&buf, "Processing", 3)
	if p == nil {
		t.Fatal("NewProgress() = nil")
	}

	p.Update(1)
	if got := buf.String(); got != "\rProcessing: 1/3" {
		t.Fatalf("Update(1) = %q", got)
	}

	buf.Reset()
	p.Update(3)
	if got := buf.String(); got != "\rProcessing: 3/3\n" {
		t.Fatalf("Update(3) = %q", got)
	}
}

func TestProgressUpdateTTYAndZeroTotal(t *testing.T) {
	r := &Renderer{tty: true, theme: DefaultTheme()}
	var buf bytes.Buffer
	p := r.NewProgress(&buf, "Processing", 4)
	p.Update(2)
	if got := buf.String(); !strings.Contains(got, "Processing") || !strings.Contains(got, "2/4") || !strings.HasPrefix(got, "\r") {
		t.Fatalf("TTY Update() = %q", got)
	}

	buf.Reset()
	zero := &Progress{w: &buf, label: "noop", total: 0, tty: false}
	zero.Update(0)
	if got := buf.String(); got != "" {
		t.Fatalf("zero-total Update() = %q, want empty", got)
	}
}

func TestStatusSummary(t *testing.T) {
	var buf bytes.Buffer
	StatusSummary(&buf, PlainTheme(), false, map[string]int{"added": 2, "failed": 0, "skipped": 1}, []string{"added", "failed", "skipped"})
	if got := buf.String(); got != "added: 2  skipped: 1\n" {
		t.Fatalf("StatusSummary() = %q", got)
	}

	buf.Reset()
	StatusSummary(&buf, PlainTheme(), false, map[string]int{}, []string{"added"})
	if got := buf.String(); got != "" {
		t.Fatalf("StatusSummary(empty) = %q, want empty", got)
	}
}
