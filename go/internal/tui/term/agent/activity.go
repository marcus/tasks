package agent

import (
	"fmt"
	"strings"

	"tasks-go/internal/tui/term/ansi"
	"tasks-go/internal/tui/term/theme"
)

// Glyphs is the status marker shown against each request.
var Glyphs = map[Status]string{
	Queued:    "○",
	Running:   "⠸",
	Succeeded: "✓",
	Failed:    "✗",
	Cancelled: "–",
}

// Slots is the theme slot each status paints with.
var Slots = map[Status]theme.Slot{
	Queued:    "muted",
	Running:   "accent",
	Succeeded: "accent",
	Failed:    "error",
	Cancelled: "warning",
}

// Content is the rendered agent-activity modal body. FilterGroups is parallel
// to Lines and names the request each line belongs to, so filtering a line
// keeps its whole request block together.
type Content struct {
	Title        string
	Lines        []string
	FilterGroups []int
}

// Activity renders AgentQueue snapshots. It deliberately shows the exact
// captured transcript rather than interpreting agent output or task mutations.
//
// Go port of Ruby's lib/tui/agent_activity.rb.
func Activity(th *theme.Theme, requests []Snapshot, now float64, width int) Content {
	if th == nil {
		th = theme.Default()
	}
	running, queued, finished := 0, 0, 0
	for _, r := range requests {
		switch {
		case r.Status == Running:
			running++
		case r.Status == Queued:
			queued++
		}
		if r.Finished() {
			finished++
		}
	}
	title := fmt.Sprintf("Agent activity · %d running · %d queued · %d finished", running, queued, finished)
	if len(requests) == 0 {
		return Content{Title: title, Lines: []string{th.Paint("muted", "No agent requests this session.")}}
	}

	contentWidth := width - 16
	if contentWidth < 20 {
		contentWidth = 20
	}
	if contentWidth > 120 {
		contentWidth = 120
	}

	queuedPosition := 0
	var lines []string
	var groups []int
	for index, request := range requests {
		if request.Status == Queued {
			queuedPosition++
		}
		var block []string
		if index != 0 {
			block = append(block, "")
		}
		block = append(block, header(th, request, now, queuedPosition))
		block = append(block, labeledLines(th, "request", request.Prompt, contentWidth, false)...)
		block = append(block, resultLines(th, request, contentWidth)...)
		lines = append(lines, block...)
		for range block {
			groups = append(groups, request.ID)
		}
	}
	return Content{Title: title, Lines: lines, FilterGroups: groups}
}

func header(th *theme.Theme, request Snapshot, now float64, queuedPosition int) string {
	status := string(request.Status)
	if request.Status == Queued {
		status += fmt.Sprintf(" #%d", queuedPosition)
	}
	elapsed := ""
	if request.StartedAt != nil {
		elapsed = " · " + formatElapsed(request.Elapsed(now))
	}
	label := fmt.Sprintf("%s #%d · %s · %s%s",
		Glyphs[request.Status], request.ID, request.Entry.UILabel(), status, elapsed)
	return th.Paint(Slots[request.Status], label)
}

func resultLines(th *theme.Theme, request Snapshot, width int) []string {
	switch request.Status {
	case Queued:
		return []string{th.Paint("muted", "  result   (waiting)")}
	case Running:
		return labeledLines(th, "result", presentOutput(request.Output, true), width, request.Output == "")
	default:
		lines := labeledLines(th, "result", presentOutput(request.Output, false), width, request.Output == "")
		if request.Error != "" && request.Status != Cancelled {
			lines = append(lines, labeledLines(th, "error", request.Error, width, false)...)
		}
		return lines
	}
}

func presentOutput(output string, running bool) string {
	text := strings.TrimSpace(ansi.Normalize(output))
	if text == "" {
		if running {
			return "(working; no output yet)"
		}
		return "(no output)"
	}
	return text
}

func labeledLines(th *theme.Theme, label, text string, width int, muted bool) []string {
	wrapped := ansi.Wrap(text, width)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	out := make([]string, 0, len(wrapped))
	for index, line := range wrapped {
		prefix := strings.Repeat(" ", 11)
		if index == 0 {
			prefix = fmt.Sprintf("  %-8s ", label)
		}
		rendered := prefix + line
		if muted {
			rendered = th.Paint("muted", rendered)
		}
		out = append(out, rendered)
	}
	return out
}

func formatElapsed(seconds float64) string {
	total := int(seconds + 0.5)
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	return fmt.Sprintf("%dm%02ds", total/60, total%60)
}
