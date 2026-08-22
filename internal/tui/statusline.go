package tui

// The status line is a modal's one fixed row for news: a host's refusal, an
// armed destructive action, or nothing. It is fixed so that posting a message
// never moves the buttons under the pointer — the same zero-row-cost rule the
// per-field hint row follows.

// StatusLevel says what kind of news a status line carries.
type StatusLevel int

const (
	StatusNone StatusLevel = iota
	StatusError
	// StatusWarning is the armed destructive-action state: loud enough to
	// demand attention, but not claiming something already failed.
	StatusWarning
)

// PaintStatusLine renders one status message, or "" when there is none.
func PaintStatusLine(styler Styler, level StatusLevel, message string) string {
	if message == "" {
		return ""
	}
	switch level {
	case StatusError:
		return styler.Paint("form_error", "  ! "+inlineText(message, " "))
	case StatusWarning:
		return styler.Paint("warning", "  ! "+inlineText(message, " "))
	default:
		return ""
	}
}
