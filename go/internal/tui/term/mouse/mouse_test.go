package mouse

import (
	"testing"

	"tasks-go/internal/tui/term/hitmap"
)

// Mirrors test/test_mouse.rb and test/test_mouse_router.rb.

func decode(t *testing.T, seq string) Event {
	t.Helper()
	ev, ok := Decode(seq)
	if !ok {
		t.Fatalf("Decode(%q) refused", seq)
	}
	return ev
}

func TestLeftPressAndRelease(t *testing.T) {
	press := decode(t, "\x1b[<0;5;7M")
	if press.Button != Left || press.Action != Press || press.Col != 4 || press.Row != 6 {
		t.Fatalf("press = %#v", press)
	}
	if press.Shift || press.Alt || press.Ctrl {
		t.Fatalf("unexpected modifiers: %#v", press)
	}
	release := decode(t, "\x1b[<0;5;7m")
	if release.Button != Left || release.Action != Release {
		t.Fatalf("release = %#v", release)
	}
}

func TestMiddleAndRightButtons(t *testing.T) {
	if got := decode(t, "\x1b[<1;1;1M").Button; got != Middle {
		t.Fatalf("button = %q", got)
	}
	if got := decode(t, "\x1b[<2;1;1M").Button; got != Right {
		t.Fatalf("button = %q", got)
	}
}

func TestWheelDirections(t *testing.T) {
	cases := map[string]Button{
		"\x1b[<64;10;10M": WheelUp,
		"\x1b[<65;10;10M": WheelDown,
		"\x1b[<66;10;10M": WheelLeft,
		"\x1b[<67;10;10M": WheelRight,
	}
	for seq, want := range cases {
		if got := decode(t, seq).Button; got != want {
			t.Fatalf("%q = %q, want %q", seq, got, want)
		}
	}
	if !decode(t, "\x1b[<64;10;10M").IsWheel() {
		t.Fatal("wheel_up must report as a wheel")
	}
}

func TestModifierBits(t *testing.T) {
	ev := decode(t, "\x1b[<28;3;4M") // left + shift(4) + alt(8) + ctrl(16)
	if ev.Button != Left || !ev.Shift || !ev.Alt || !ev.Ctrl {
		t.Fatalf("event = %#v", ev)
	}
}

func TestMotionBit(t *testing.T) {
	ev := decode(t, "\x1b[<32;3;4M")
	if ev.Action != Motion || ev.Button != Left {
		t.Fatalf("event = %#v", ev)
	}
}

func TestExtraButtons(t *testing.T) {
	if got := decode(t, "\x1b[<128;1;1M").Button; got != Button8 {
		t.Fatalf("button = %q", got)
	}
	if got := decode(t, "\x1b[<129;1;1M").Button; got != Button9 {
		t.Fatalf("button = %q", got)
	}
}

func TestCoordinatesPast223RoundTrip(t *testing.T) {
	ev := decode(t, "\x1b[<0;300;400M")
	if ev.Col != 299 || ev.Row != 399 {
		t.Fatalf("event = %#v", ev)
	}
}

func TestOneBasedToZeroBased(t *testing.T) {
	ev := decode(t, "\x1b[<0;1;1M")
	if ev.Col != 0 || ev.Row != 0 {
		t.Fatalf("event = %#v", ev)
	}
}

func TestMalformedReturnsNil(t *testing.T) {
	for _, seq := range []string{
		"\x1b[<0;1M", "\x1b[<0;1;2X", "", "\x1b[<abc;1;2M",
		"\x1b[<0;0;1M", // col 0 → -1 after conversion
		"\x1b[<0;1;0M", // row 0 → -1 after conversion
	} {
		if _, ok := Decode(seq); ok {
			t.Fatalf("Decode(%q) accepted, want refusal", seq)
		}
	}
}

func TestEnableDisableSequences(t *testing.T) {
	if Enable != "\x1b[?1000h\x1b[?1006h" {
		t.Fatalf("Enable = %q", Enable)
	}
	if Disable != "\x1b[?1006l\x1b[?1000l" {
		t.Fatalf("Disable = %q", Disable)
	}
}

func TestSequenceRegexMatchesSGRReports(t *testing.T) {
	for _, seq := range []string{"\x1b[<0;12;34M", "\x1b[<65;1;1m"} {
		if !Sequence.MatchString(seq) {
			t.Fatalf("%q did not match", seq)
		}
	}
	if Sequence.MatchString("\x1b[A") {
		t.Fatal("an arrow key must not look like a mouse report")
	}
}

// -- routing -----------------------------------------------------------------

func event(button Button, action Action) Event {
	return Event{Button: button, Action: action}
}

func hit(zone hitmap.Zone, index int) hitmap.Hit {
	return hitmap.Hit{Zone: zone, Index: index}
}

func footerHit(role hitmap.FooterRole) hitmap.Hit {
	return hitmap.Hit{Zone: hitmap.ZoneFooterRow, Footer: hitmap.FooterPayload{Index: 0, Role: role}}
}

func route(ev Event, h hitmap.Hit, opts RouteOptions) Intent {
	return Route(ev, true, h, opts)
}

func sel(n int) *int { return &n }

// A downward gesture reports as wheel-up under macOS natural scrolling, so
// wheel-up advances and wheel-down goes back — every target shares the sign.
func TestWheelOverListScrollsList(t *testing.T) {
	up := route(event(WheelUp, Press), hit(hitmap.ZoneListRow, 5), RouteOptions{Mode: ModeList})
	if up.Kind != ScrollList || up.Delta != 3 {
		t.Fatalf("up = %#v", up)
	}
	down := route(event(WheelDown, Press), hit(hitmap.ZoneListRow, 5), RouteOptions{Mode: ModeList})
	if down.Kind != ScrollList || down.Delta != -3 {
		t.Fatalf("down = %#v", down)
	}
}

func TestEveryWheelTargetSharesOneDirection(t *testing.T) {
	cases := []struct {
		hit  hitmap.Hit
		kind IntentKind
		opts RouteOptions
	}{
		{hit(hitmap.ZonePanel, 0), ScrollPanel, RouteOptions{Panel: true}},
		{hit(hitmap.ZoneModalRow, 0), ScrollModal, RouteOptions{Mode: ModeModal}},
		{footerHit(hitmap.RoleResponse), ScrollResponse, RouteOptions{}},
		{hit(hitmap.ZonePopupRow, 0), ScrollPopup, RouteOptions{Mode: ModePalette}},
	}
	for _, c := range cases {
		if got := route(event(WheelUp, Press), c.hit, c.opts); got.Kind != c.kind || got.Delta != 3 {
			t.Fatalf("%s up = %#v", c.hit.Zone, got)
		}
		if got := route(event(WheelDown, Press), c.hit, c.opts); got.Kind != c.kind || got.Delta != -3 {
			t.Fatalf("%s down = %#v", c.hit.Zone, got)
		}
	}
}

func TestWheelOverPanel(t *testing.T) {
	ev := event(WheelDown, Press)
	with := route(ev, hit(hitmap.ZonePanel, 0), RouteOptions{Mode: ModeList, Panel: true})
	if with.Kind != ScrollPanel || with.Delta != -3 {
		t.Fatalf("with panel = %#v", with)
	}
	without := route(ev, hit(hitmap.ZonePanel, 0), RouteOptions{Mode: ModeList})
	if without.Kind != Ignored {
		t.Fatalf("without panel = %#v", without)
	}
}

func TestWheelOverResponseFooter(t *testing.T) {
	ev := event(WheelDown, Press)
	if got := route(ev, footerHit(hitmap.RoleResponse), RouteOptions{Mode: ModeList}); got.Kind != ScrollResponse || got.Delta != -3 {
		t.Fatalf("response = %#v", got)
	}
	if got := route(ev, footerHit(hitmap.RolePrompt), RouteOptions{Mode: ModeList}); got.Kind != Ignored {
		t.Fatalf("prompt = %#v", got)
	}
}

// An open modal, palette, or form owns the pointer: nothing may reach the list,
// tabs, or panel underneath it.
func TestOverlayModesIgnoreEverythingOutsideTheOverlay(t *testing.T) {
	click := event(Left, Press)
	wheel := event(WheelDown, Press)
	hits := []hitmap.Hit{
		hit(hitmap.ZoneListRow, 5), hit(hitmap.ZoneCollapseMarker, 5),
		{Zone: hitmap.ZoneTab, Tab: "next"}, hit(hitmap.ZonePanel, 0),
		footerHit(hitmap.RolePrompt), footerHit(hitmap.RoleResponse),
	}
	for _, mode := range []Mode{ModeModal, ModeModalFilter, ModePalette, ModeContextPalette, ModeForm} {
		for _, h := range hits {
			if got := route(click, h, RouteOptions{Mode: mode, Panel: true, Selected: sel(5)}); got.Kind != Ignored {
				t.Fatalf("click on %s reached past a %s overlay: %#v", h.Zone, mode, got)
			}
			if got := route(wheel, h, RouteOptions{Mode: mode, Panel: true}); got.Kind != Ignored {
				t.Fatalf("wheel on %s reached past a %s overlay: %#v", h.Zone, mode, got)
			}
		}
	}
}

func TestOverlayModesStillRouteTheirOwnZones(t *testing.T) {
	wheel := event(WheelUp, Press)
	for _, mode := range []Mode{ModeModal, ModeModalFilter} {
		got := route(wheel, hit(hitmap.ZoneModalRow, 0), RouteOptions{Mode: mode})
		if got.Kind != ScrollModal || got.Delta != 3 {
			t.Fatalf("%s = %#v", mode, got)
		}
	}
	got := route(event(Left, Press), hit(hitmap.ZonePopupRow, 2), RouteOptions{Mode: ModeContextPalette})
	if got.Kind != PickerHit || got.Index != 2 {
		t.Fatalf("picker = %#v", got)
	}
}

func TestLeftClickSelectAndActivate(t *testing.T) {
	ev := event(Left, Press)
	if got := route(ev, hit(hitmap.ZoneListRow, 5), RouteOptions{Selected: sel(2)}); got.Kind != SelectRow || got.Index != 5 {
		t.Fatalf("select = %#v", got)
	}
	if got := route(ev, hit(hitmap.ZoneListRow, 5), RouteOptions{Selected: sel(5)}); got.Kind != ActivateRow || got.Index != 5 {
		t.Fatalf("activate = %#v", got)
	}
}

func TestLeftClickTabAndPrompt(t *testing.T) {
	ev := event(Left, Press)
	got := route(ev, hitmap.Hit{Zone: hitmap.ZoneTab, Tab: "next"}, RouteOptions{})
	if got.Kind != SwitchView || got.View != "next" {
		t.Fatalf("tab = %#v", got)
	}
	if got := route(ev, footerHit(hitmap.RolePrompt), RouteOptions{}); got.Kind != FocusPrompt {
		t.Fatalf("prompt = %#v", got)
	}
}

func TestReleaseAndRightIgnored(t *testing.T) {
	cases := []Event{
		event(Left, Release), event(Right, Press), event(Middle, Press),
		{Button: Button8, Action: Press},
	}
	for _, ev := range cases {
		if got := route(ev, hit(hitmap.ZoneListRow, 0), RouteOptions{}); got.Kind != Ignored {
			t.Fatalf("%v = %#v", ev.Button, got)
		}
	}
}

func TestTaskEditIgnoresClicksAllowsPanelWheel(t *testing.T) {
	click := event(Left, Press)
	opts := RouteOptions{Mode: ModeTaskEdit, Panel: true}
	if got := route(click, hit(hitmap.ZoneListRow, 0), opts); got.Kind != Ignored {
		t.Fatalf("click on list = %#v", got)
	}
	if got := route(click, hit(hitmap.ZonePanel, 0), opts); got.Kind != Ignored {
		t.Fatalf("click on panel = %#v", got)
	}
	wheel := event(WheelDown, Press)
	if got := route(wheel, hit(hitmap.ZonePanel, 0), opts); got.Kind != ScrollPanel || got.Delta != -3 {
		t.Fatalf("wheel on panel = %#v", got)
	}
	if got := route(wheel, hit(hitmap.ZoneListRow, 0), opts); got.Kind != Ignored {
		t.Fatalf("wheel on list = %#v", got)
	}
}

func TestCollapseMarkerAndPicker(t *testing.T) {
	ev := event(Left, Press)
	if got := route(ev, hit(hitmap.ZoneCollapseMarker, 3), RouteOptions{}); got.Kind != ToggleCollapse || got.Index != 3 {
		t.Fatalf("marker = %#v", got)
	}
	if got := route(ev, hit(hitmap.ZonePopupRow, 2), RouteOptions{Mode: ModePalette}); got.Kind != PickerHit || got.Index != 2 {
		t.Fatalf("picker = %#v", got)
	}
}

func TestModalChromeClickIgnored(t *testing.T) {
	ev := event(Left, Press)
	for _, zone := range []hitmap.Zone{hitmap.ZoneBorder, hitmap.ZoneHeader, hitmap.ZoneModalRow} {
		if got := route(ev, hit(zone, 0), RouteOptions{}); got.Kind != Ignored {
			t.Fatalf("%s = %#v", zone, got)
		}
	}
}

func TestUndecodedEventIsIgnored(t *testing.T) {
	if got := Route(Event{}, false, hit(hitmap.ZoneListRow, 0), RouteOptions{}); got.Kind != Ignored {
		t.Fatalf("garbled report = %#v", got)
	}
}

func TestWheelLeftAndRightAreDecodedButUnused(t *testing.T) {
	for _, b := range []Button{WheelLeft, WheelRight} {
		if got := route(event(b, Press), hit(hitmap.ZoneListRow, 0), RouteOptions{}); got.Kind != Ignored {
			t.Fatalf("%s = %#v", b, got)
		}
	}
}
