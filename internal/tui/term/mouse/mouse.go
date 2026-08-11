// Package mouse is a pure SGR mouse-report decoder plus the
// (mode, zone, button, action) → intent table. Terminal bytes in, intents out.
// Legacy X10 encoding is deliberately unsupported.
//
// Go port of Ruby's lib/tui/mouse.rb and lib/tui/mouse_router.rb.
//
// Under Bubble Tea the framework already decodes mouse reports and delivers
// them as messages, so Decode is not on the hot path there: build an Event with
// New from the framework's fields and hand it to Route. Decode remains for
// callers reading a terminal directly, and it is what pins the wire format in
// tests.
package mouse

import (
	"regexp"
	"strconv"

	"github.com/marcus/tasks/internal/tui/term/hitmap"
)

const (
	// Enable and Disable are the terminal sequences that turn SGR mouse
	// reporting on and off.
	Enable  = "\x1b[?1000h\x1b[?1006h"
	Disable = "\x1b[?1006l\x1b[?1000l"
	// WheelDelta is how many rows one wheel notch moves.
	WheelDelta = 3
)

// Sequence matches the start of an SGR mouse report, for input demultiplexing.
var Sequence = regexp.MustCompile(`^\x1b\[<[0-9;]*[Mm]`)

var report = regexp.MustCompile(`^\x1b\[<(\d+);(\d+);(\d+)([Mm])$`)

// Button names a mouse button or wheel axis.
type Button string

const (
	Left       Button = "left"
	Middle     Button = "middle"
	Right      Button = "right"
	WheelUp    Button = "wheel_up"
	WheelDown  Button = "wheel_down"
	WheelLeft  Button = "wheel_left"
	WheelRight Button = "wheel_right"
	Button8    Button = "button8"
	Button9    Button = "button9"
	Button10   Button = "button10"
	Button11   Button = "button11"
)

// Action is what the button did.
type Action string

const (
	Press   Action = "press"
	Release Action = "release"
	Motion  Action = "motion"
)

// Event is one decoded report. Coordinates are 0-based screen cells.
type Event struct {
	Button           Button
	Action           Action
	Col, Row         int
	Shift, Alt, Ctrl bool
}

func (e Event) IsWheel() bool {
	switch e.Button {
	case WheelUp, WheelDown, WheelLeft, WheelRight:
		return true
	}
	return false
}

func (e Event) IsPress() bool   { return e.Action == Press }
func (e Event) IsRelease() bool { return e.Action == Release }
func (e Event) IsMotion() bool  { return e.Action == Motion }

// IsExtra reports one of the button8..11 side buttons.
func (e Event) IsExtra() bool {
	switch e.Button {
	case Button8, Button9, Button10, Button11:
		return true
	}
	return false
}

var buttons = map[int]Button{0: Left, 1: Middle, 2: Right}
var wheelButtons = map[int]Button{0: WheelUp, 1: WheelDown, 2: WheelLeft, 3: WheelRight}
var extraButtons = map[int]Button{0: Button8, 1: Button9, 2: Button10, 3: Button11}

// New builds an Event from already-decoded fields — the path for a framework
// (Bubble Tea, say) that has parsed the report itself. Coordinates are 0-based
// screen cells, as everywhere else in this package.
func New(button Button, action Action, col, row int) Event {
	return Event{Button: button, Action: action, Col: col, Row: row}
}

// Decode parses one complete SGR report. It reports ok=false for malformed
// input rather than failing — a garbled report is discarded, never fatal.
func Decode(seq string) (Event, bool) {
	m := report.FindStringSubmatch(seq)
	if m == nil {
		return Event{}, false
	}
	cb, err := strconv.Atoi(m[1])
	if err != nil {
		return Event{}, false
	}
	// Terminal reports are 1-based; screen coordinates are 0-based.
	col, err1 := strconv.Atoi(m[2])
	row, err2 := strconv.Atoi(m[3])
	if err1 != nil || err2 != nil {
		return Event{}, false
	}
	col--
	row--
	if col < 0 || row < 0 {
		return Event{}, false
	}

	action := Press
	switch {
	case cb&32 != 0:
		action = Motion
	case m[4] == "M":
		action = Press
	default:
		action = Release
	}

	code := cb &^ (4 | 8 | 16 | 32)
	var button Button
	var ok bool
	switch {
	case code&64 != 0:
		button, ok = wheelButtons[code&3]
	case code&128 != 0:
		button, ok = extraButtons[code&3]
	default:
		button, ok = buttons[code&3]
	}
	if !ok {
		return Event{}, false
	}

	return Event{
		Button: button, Action: action, Col: col, Row: row,
		Shift: cb&4 != 0, Alt: cb&8 != 0, Ctrl: cb&16 != 0,
	}, true
}

// Mode is the input mode the application is in when the event arrives.
type Mode string

const (
	ModeList           Mode = "list"
	ModeModal          Mode = "modal"
	ModeModalFilter    Mode = "modal_filter"
	ModePalette        Mode = "palette"
	ModeContextPalette Mode = "context_palette"
	ModeLinkPicker     Mode = "link_picker"
	ModeForm           Mode = "form"
	ModeTaskEdit       Mode = "task_edit"
	ModePrompt         Mode = "prompt"
)

// overlayModes are the modes where an overlay owns input. A click or wheel that
// reached the list underneath would move the selection — or open a detail panel
// — behind a blocking box, so only the overlay's own zones route while one is
// open.
var overlayModes = map[Mode]bool{
	ModeModal: true, ModeModalFilter: true, ModePalette: true,
	ModeContextPalette: true, ModeLinkPicker: true, ModeForm: true,
}

var overlayZones = map[hitmap.Zone]bool{
	hitmap.ZoneModalRow: true, hitmap.ZonePopupRow: true,
}

// Action names the intent a mouse event resolves to. Every intent maps to an
// existing keyboard handler.
type IntentKind string

const (
	Ignored        IntentKind = "ignored"
	ScrollList     IntentKind = "scroll_list"
	ScrollPanel    IntentKind = "scroll_panel"
	ScrollModal    IntentKind = "scroll_modal"
	ScrollPopup    IntentKind = "scroll_popup"
	ScrollResponse IntentKind = "scroll_response"
	SelectRow      IntentKind = "select_row"
	ActivateRow    IntentKind = "activate_row"
	ToggleCollapse IntentKind = "toggle_collapse"
	SwitchView     IntentKind = "switch_view"
	FocusPrompt    IntentKind = "focus_prompt"
	PickerHit      IntentKind = "picker_hit"
)

// Intent is a resolved mouse intent. Delta carries the scroll amount for the
// scroll intents; Index carries the row for the row intents; View carries the
// tab key for SwitchView.
type Intent struct {
	Kind  IntentKind
	Delta int
	Index int
	View  string
}

// RouteOptions carries the application state the routing table consults.
type RouteOptions struct {
	Mode Mode
	// Panel reports whether a right panel is currently drawn.
	Panel bool
	// Selected is the currently selected absolute row index, or nil.
	Selected *int
}

// Route maps a decoded event and the zone it landed in to an intent.
func Route(event Event, ok bool, hit hitmap.Hit, opts RouteOptions) Intent {
	if !ok {
		return Intent{Kind: Ignored}
	}
	if event.IsRelease() || event.IsMotion() {
		return Intent{Kind: Ignored}
	}
	if event.Button == Middle || event.Button == Right || event.IsExtra() {
		return Intent{Kind: Ignored}
	}
	mode := opts.Mode
	if mode == "" {
		mode = ModeList
	}
	if overlayModes[mode] && !overlayZones[hit.Zone] {
		return Intent{Kind: Ignored}
	}
	if event.IsWheel() {
		return wheelIntent(event, hit, mode, opts.Panel)
	}
	if event.Button != Left || !event.IsPress() {
		return Intent{Kind: Ignored}
	}
	return clickIntent(hit, mode, opts.Selected)
}

func wheelIntent(event Event, hit hitmap.Hit, mode Mode, panel bool) Intent {
	// Direction. macOS natural scrolling (the default, and what Apple mice and
	// trackpads ship with) reports a *downward* gesture as wheel-up, so taking
	// the report at face value moved the list cursor the opposite way from the
	// user's hand. Deltas therefore follow the gesture, not the report name:
	// wheel-up advances, wheel-down goes back. Every wheel target — list, panel,
	// modal, response pane — shares this one sign so the panes never disagree
	// about which way a flick goes. Swap the two terms to invert.
	delta := -WheelDelta
	if event.Button == WheelUp {
		delta = WheelDelta
	}
	// Wheel left/right are decoded but unused.
	if event.Button != WheelUp && event.Button != WheelDown {
		return Intent{Kind: Ignored}
	}

	if mode == ModeTaskEdit {
		if hit.Zone == hitmap.ZonePanel && panel {
			return Intent{Kind: ScrollPanel, Delta: delta}
		}
		return Intent{Kind: Ignored}
	}

	switch hit.Zone {
	case hitmap.ZonePanel:
		if panel {
			return Intent{Kind: ScrollPanel, Delta: delta}
		}
		return Intent{Kind: Ignored}
	case hitmap.ZoneModalRow:
		return Intent{Kind: ScrollModal, Delta: delta}
	case hitmap.ZoneListRow, hitmap.ZoneCollapseMarker:
		return Intent{Kind: ScrollList, Delta: delta}
	case hitmap.ZoneFooterRow:
		if hit.Footer.Role == hitmap.RoleResponse {
			return Intent{Kind: ScrollResponse, Delta: delta}
		}
		return Intent{Kind: Ignored}
	case hitmap.ZonePopupRow:
		return Intent{Kind: ScrollPopup, Delta: delta}
	default:
		return Intent{Kind: Ignored}
	}
}

func clickIntent(hit hitmap.Hit, mode Mode, selected *int) Intent {
	// The task editor saves on blur — clicks must not steal focus.
	if mode == ModeTaskEdit {
		return Intent{Kind: Ignored}
	}
	switch hit.Zone {
	case hitmap.ZoneListRow:
		if selected != nil && *selected == hit.Index {
			return Intent{Kind: ActivateRow, Index: hit.Index}
		}
		return Intent{Kind: SelectRow, Index: hit.Index}
	case hitmap.ZoneCollapseMarker:
		return Intent{Kind: ToggleCollapse, Index: hit.Index}
	case hitmap.ZoneTab:
		return Intent{Kind: SwitchView, View: hit.Tab}
	case hitmap.ZoneFooterRow:
		if hit.Footer.Role == hitmap.RolePrompt {
			return Intent{Kind: FocusPrompt}
		}
		return Intent{Kind: Ignored}
	case hitmap.ZonePopupRow:
		return Intent{Kind: PickerHit, Index: hit.Index}
	default:
		// Deliberately ignore modal chrome clicks (no dismiss-on-outside-click).
		return Intent{Kind: Ignored}
	}
}
