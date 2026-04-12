package ui

import "strings"

// Field represents a label/value pair in terminal feedback output.
type Field struct {
	Label string
	Value string
}

// Section renders a section heading.
func (r *Renderer) Section(title string) string {
	if r == nil {
		return title
	}
	return r.theme.Title.Render(title)
}

// Notice renders a short single-line notice.
func (r *Renderer) Notice(label, message string) string {
	if strings.TrimSpace(label) == "" {
		return message
	}
	renderedLabel := label
	if r != nil {
		renderedLabel = r.theme.Bold.Render(label)
	}
	if strings.TrimSpace(message) == "" {
		return renderedLabel
	}
	return renderedLabel + " " + message
}

// Fields renders a section title and a list of label/value rows.
func (r *Renderer) Fields(title string, fields []Field) string {
	var out strings.Builder
	if strings.TrimSpace(title) != "" {
		out.WriteString(r.Section(title))
		if len(fields) > 0 {
			out.WriteByte('\n')
		}
	}
	for index, field := range fields {
		out.WriteString(r.renderField(field))
		if index < len(fields)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

func (r *Renderer) renderField(field Field) string {
	label := field.Label
	if r != nil {
		label = r.theme.Subtitle.Render(field.Label)
	}
	if strings.TrimSpace(field.Value) == "" {
		return label
	}
	return label + ": " + field.Value
}
