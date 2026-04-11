package ui

import (
	"io"
	"os"

	"github.com/charmbracelet/colorprofile"
	term "github.com/charmbracelet/x/term"
)

// Renderer wraps a colorprofile writer with TTY detection and a theme.
type Renderer struct {
	out    io.Writer
	writer *colorprofile.Writer
	theme  Theme
	tty    bool
}

// New creates a Renderer that writes to out. It auto-detects whether out
// is a terminal and picks colors/styles accordingly.
func New(out io.Writer) *Renderer {
	w := colorprofile.NewWriter(out, os.Environ())
	tty := w.Profile != colorprofile.NoTTY && w.Profile != colorprofile.Ascii
	theme := DefaultTheme()
	if !tty {
		theme = PlainTheme()
	}
	return &Renderer{out: out, writer: w, theme: theme, tty: tty}
}

// IsTTY reports whether the output is an interactive terminal.
func (r *Renderer) IsTTY() bool {
	return r.tty
}

// Theme returns the renderer's color theme.
func (r *Renderer) Theme() Theme {
	return r.theme
}

// Width returns the current terminal width when available.
func (r *Renderer) Width() int {
	if r == nil {
		return 0
	}
	file, ok := r.out.(*os.File)
	if !ok || !term.IsTerminal(file.Fd()) {
		return 0
	}
	width, _, err := term.GetSize(file.Fd())
	if err != nil || width <= 0 {
		return 0
	}
	return width
}

// Writer returns the underlying colorprofile writer.
func (r *Renderer) Writer() *colorprofile.Writer {
	return r.writer
}

// Stdout is a convenience constructor that builds a Renderer for os.Stdout.
func Stdout() *Renderer {
	return New(os.Stdout)
}
