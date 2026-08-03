package term

import (
	"strings"
	"testing"

	"tasks-go/internal/tui/term/theme"
)

// styler is the four-method contract the TUI shell depends on. Declaring it
// here proves the shape without importing the shell package (which imports
// this one).
type styler interface {
	Paint(slot, text string) string
	Width(text string) int
	Truncate(text string, width int) string
	Wrap(text string, width int) []string
}

var _ styler = (*Styler)(nil)

// shellSlots is the slot vocabulary the shell paints with. Every one must
// resolve to a real theme slot, or the shell silently loses color.
var shellSlots = []string{
	"title", "section", "muted", "accent", "warning", "error", "context",
	"project", "priority", "selection", "tab", "tab_active", "detail_label",
	"description", "link", "link_system", "state_next", "state_waiting",
	"state_done", "outline_container", "outline_thread", "approval_section",
	"inbox_section", "due_overdue", "due_soon", "due_week", "due_far",
}

func TestEveryShellSlotResolvesToARealThemeSlot(t *testing.T) {
	for _, slot := range shellSlots {
		if !theme.HasSlot(resolveSlot(slot)) {
			t.Fatalf("shell slot %q resolves to %q, which is not a theme slot", slot, resolveSlot(slot))
		}
	}
}

func TestDefaultThemePaintsTheShellSlots(t *testing.T) {
	s := NewStyler("default", nil)
	unpainted := 0
	for _, slot := range shellSlots {
		got := s.Paint(slot, "x")
		if got == "x" {
			// "title" legitimately defaults to none.
			unpainted++
			continue
		}
		if !strings.HasPrefix(got, "\x1b[") || !strings.HasSuffix(got, "\x1b[0m") {
			t.Fatalf("slot %q painted %q", slot, got)
		}
	}
	if unpainted > 1 {
		t.Fatalf("%d shell slots resolved to no styling at all", unpainted)
	}
}

func TestUnknownSlotDegradesToUnpaintedText(t *testing.T) {
	s := NewStyler("default", nil)
	if got := s.Paint("no_such_slot", "hello"); got != "hello" {
		t.Fatalf("unknown slot = %q", got)
	}
	if got := s.SGR("no_such_slot"); got != "" {
		t.Fatalf("unknown slot SGR = %q", got)
	}
}

func TestTabAliasPaintsTheInactiveTabSlot(t *testing.T) {
	s := NewStyler("default", nil)
	if got, want := s.Paint("tab", "x"), s.Theme().Paint("tab_inactive", "x"); got != want {
		t.Fatalf("tab = %q, want %q", got, want)
	}
	if s.Paint("tab", "x") == s.Paint("tab_active", "x") {
		t.Fatal("the active and inactive tabs must not paint identically")
	}
}

// The whole point of handing the shell a real styler: layout decisions made
// through Width become wide-character correct with no call-site change.
func TestWidthTruncateAndWrapUseTerminalCells(t *testing.T) {
	s := NewStyler("default", nil)
	if got := s.Width("界界界"); got != 6 {
		t.Fatalf("Width = %d, want 6", got)
	}
	if got := s.Width(s.Paint("accent", "hello")); got != 5 {
		t.Fatalf("styled Width = %d, want 5", got)
	}
	if got := s.Width("Inbox empty. ✨"); got != 15 {
		t.Fatalf("Width = %d, want 15", got)
	}
	if got := s.Truncate("ab✨cd", 4); strings.Contains(got, "✨") || s.Width(got) > 4 {
		t.Fatalf("Truncate = %q", got)
	}
	for _, line := range s.Wrap("界界界界界", 4) {
		if s.Width(line) > 4 {
			t.Fatalf("wrapped line over budget: %q", line)
		}
	}
	if got := s.Pad("界", 5); s.Width(got) != 5 {
		t.Fatalf("Pad width = %d", s.Width(got))
	}
	if got := s.Slice("a界b", 1, 2); got != "界" {
		t.Fatalf("Slice = %q", got)
	}
}

// NO_COLOR resolves to the mono theme upstream; a mono styler must emit no
// color codes at all, only attributes.
func TestMonoStylerEmitsNoColorCodes(t *testing.T) {
	s := NewStyler("mono", nil)
	attributes := map[string]bool{"1": true, "2": true, "3": true, "4": true, "7": true}
	for _, slot := range shellSlots {
		seq := s.SGR(slot)
		if seq == "" {
			continue
		}
		body := strings.TrimSuffix(strings.TrimPrefix(seq, "\x1b["), "m")
		for _, code := range strings.Split(body, ";") {
			if !attributes[code] {
				t.Fatalf("mono slot %q emits color code %q", slot, code)
			}
		}
	}
}

func TestNewStylerFromThemeAcceptsNil(t *testing.T) {
	if got := NewStylerFromTheme(nil).Paint("accent", "x"); got != "\x1b[36mx\x1b[0m" {
		t.Fatalf("nil theme = %q", got)
	}
}
