package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestKeySequence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		// Printable Text path
		{name: "letter a", msg: tea.KeyPressMsg{Code: 'a', Text: "a"}, want: "a"},
		{name: "letter z", msg: tea.KeyPressMsg{Code: 'z', Text: "z"}, want: "z"},
		{name: "shifted A via Text", msg: tea.KeyPressMsg{Code: 'a', Text: "A", Mod: tea.ModShift}, want: "A"},
		{name: "space via Text", msg: tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, want: " "},
		{name: "unicode via Text", msg: tea.KeyPressMsg{Code: 'é', Text: "é"}, want: "é"},
		{name: "multi-rune Text", msg: tea.KeyPressMsg{Code: 0, Text: "你好"}, want: "你好"},
		{name: "alt+j via Text", msg: tea.KeyPressMsg{Code: 'j', Text: "j", Mod: tea.ModAlt}, want: "\x1bj"},
		{name: "alt+k via Text", msg: tea.KeyPressMsg{Code: 'k', Text: "k", Mod: tea.ModAlt}, want: "\x1bk"},

		// Bound Ctrl letters → C0
		{name: "ctrl-a", msg: tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, want: "\x01"},
		{name: "ctrl-c", msg: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, want: "\x03"},
		{name: "ctrl-k", msg: tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}, want: "\x0b"},
		{name: "ctrl-w", msg: tea.KeyPressMsg{Code: 'w', Mod: tea.ModCtrl}, want: "\x17"},
		{name: "ctrl-A uppercase code", msg: tea.KeyPressMsg{Code: 'A', Mod: tea.ModCtrl}, want: "\x01"},
		// ctrl-x is the delegate modal's Undelegate button.
		{name: "ctrl-x", msg: tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl}, want: "\x18"},

		// Unbound Ctrl must NOT fall through to letter bindings (td-65d0a8)
		{name: "ctrl-z unbound", msg: tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, want: ""},
		{name: "ctrl-q unbound", msg: tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}, want: ""},
		{name: "ctrl-g unbound", msg: tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}, want: ""},
		{name: "ctrl-y unbound", msg: tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl}, want: ""},
		{name: "ctrl-enter unbound", msg: tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl}, want: ""},
		{name: "ctrl-space unbound", msg: tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl}, want: ""},

		// Specials
		{name: "enter", msg: tea.KeyPressMsg{Code: tea.KeyEnter}, want: "\r"},
		{name: "esc", msg: tea.KeyPressMsg{Code: tea.KeyEsc}, want: "\x1b"},
		{name: "tab", msg: tea.KeyPressMsg{Code: tea.KeyTab}, want: "\t"},
		{name: "shift-tab", msg: tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, want: "\x1b[Z"},
		{name: "space via Code", msg: tea.KeyPressMsg{Code: tea.KeySpace}, want: " "},
		{name: "backspace", msg: tea.KeyPressMsg{Code: tea.KeyBackspace}, want: "\x7f"},
		{name: "delete", msg: tea.KeyPressMsg{Code: tea.KeyDelete}, want: "\x1b[3~"},
		{name: "up", msg: tea.KeyPressMsg{Code: tea.KeyUp}, want: "\x1b[A"},
		{name: "down", msg: tea.KeyPressMsg{Code: tea.KeyDown}, want: "\x1b[B"},
		{name: "right", msg: tea.KeyPressMsg{Code: tea.KeyRight}, want: "\x1b[C"},
		{name: "left", msg: tea.KeyPressMsg{Code: tea.KeyLeft}, want: "\x1b[D"},
		{name: "home", msg: tea.KeyPressMsg{Code: tea.KeyHome}, want: "\x1b[H"},
		{name: "end", msg: tea.KeyPressMsg{Code: tea.KeyEnd}, want: "\x1b[F"},
		{name: "pgup", msg: tea.KeyPressMsg{Code: tea.KeyPgUp}, want: "\x1b[5~"},
		{name: "pgdown", msg: tea.KeyPressMsg{Code: tea.KeyPgDown}, want: "\x1b[6~"},
		{name: "alt-up", msg: tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}, want: "\x1b[1;3A"},
		{name: "alt-down", msg: tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt}, want: "\x1b[1;3B"},
		{name: "alt-left", msg: tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}, want: "\x1b[1;3D"},
		{name: "alt-right", msg: tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModAlt}, want: "\x1b[1;3C"},
		{name: "alt-backspace", msg: tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModAlt}, want: "\x1b\x7f"},
		{name: "alt-delete", msg: tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModAlt}, want: "\x1b[3;3~"},

		// Shifted specials must stay unbound (v1 KeyShiftUp etc. were ignored)
		{name: "shift-up unbound", msg: tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}, want: ""},
		{name: "shift-down unbound", msg: tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}, want: ""},
		{name: "shift-left unbound", msg: tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModShift}, want: ""},
		{name: "shift-right unbound", msg: tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift}, want: ""},
		{name: "shift-delete unbound", msg: tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModShift}, want: ""},
		{name: "shift-enter unbound", msg: tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, want: ""},
		{name: "shift-space unbound", msg: tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModShift}, want: ""},
		{name: "shift-home unbound", msg: tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModShift}, want: ""},
		{name: "shift-pgup unbound", msg: tea.KeyPressMsg{Code: tea.KeyPgUp, Mod: tea.ModShift}, want: ""},

		// Mixed modifiers on specials must not collapse to a simpler binding
		{name: "shift-alt-up unbound", msg: tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift | tea.ModAlt}, want: ""},
		{name: "shift-alt-down unbound", msg: tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift | tea.ModAlt}, want: ""},
		{name: "shift-alt-tab unbound", msg: tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift | tea.ModAlt}, want: ""},
		{name: "meta-up unbound", msg: tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModMeta}, want: ""},

		// Code-only printable fallback
		{name: "code-only letter", msg: tea.KeyPressMsg{Code: 'm'}, want: "m"},
		{name: "code-only shift+letter unbound", msg: tea.KeyPressMsg{Code: 'm', Mod: tea.ModShift}, want: ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := KeySequence(tc.msg)
			if got != tc.want {
				t.Fatalf("KeySequence(%+v) = %q (%v), want %q (%v)",
					tc.msg, got, []byte(got), tc.want, []byte(tc.want))
			}
		})
	}
}

func TestKeySequenceIgnoresKeyReleaseInUpdate(t *testing.T) {
	// KeyReleaseMsg must not be treated as a press. KeySequence is only
	// called from handleKey(KeyPressMsg); Update ignores releases. This
	// documents the contract for hosts that deliver both event types.
	h := newModelHarness(t, harnessOptions{})
	before := h.model.View().Content
	_, _ = h.model.Update(tea.KeyReleaseMsg{Code: 'q'})
	after := h.model.View().Content
	if before != after {
		t.Fatal("KeyReleaseMsg changed the view; releases must be ignored")
	}
	if h.model.quitting {
		t.Fatal("KeyReleaseMsg of q must not quit")
	}
}
