package theme

import (
	"strings"
	"testing"
)

// Ruby has no test_theme.rb: the theme layer is exercised indirectly through
// test_frame.rb, test_views.rb, test_modals.rb and test_config.rb. These tests
// characterize the resolution rules that lib/tui/theme.rb documents, so the Go
// port has them directly.

func TestParseAcceptsAttributesNamedColorsIndexesAndHex(t *testing.T) {
	cases := []struct {
		spec string
		want []string
	}{
		{"bold", []string{"1"}},
		{"bold reverse", []string{"1", "7"}},
		{"dim italic underline", []string{"2", "3", "4"}},
		{"red", []string{"31"}},
		{"gray", []string{"90"}},
		{"grey", []string{"90"}},
		{"bright-magenta", []string{"95"}},
		{"on-blue", []string{"44"}},
		{"bold black on-cyan", []string{"1", "30", "46"}},
		{"#1e2030", []string{"38;2;30;32;48"}},
		{"on-#1e2030", []string{"48;2;30;32;48"}},
		{"0", []string{"38;5;0"}},
		{"255", []string{"38;5;255"}},
		{"on-231", []string{"48;5;231"}},
		{"  BOLD   Red  ", []string{"1", "31"}},
	}
	for _, c := range cases {
		got, ok := Parse(c.spec)
		if !ok {
			t.Fatalf("Parse(%q) rejected", c.spec)
		}
		if strings.Join(got, ";") != strings.Join(c.want, ";") {
			t.Fatalf("Parse(%q) = %v, want %v", c.spec, got, c.want)
		}
	}
}

func TestParseNoneIsValidAndUnstyled(t *testing.T) {
	for _, spec := range []string{"none", "plain", "NONE"} {
		codes, ok := Parse(spec)
		if !ok || len(codes) != 0 {
			t.Fatalf("Parse(%q) = %v, %v", spec, codes, ok)
		}
	}
}

func TestParseRejectsTheWholeSpecWhenAnyTokenIsInvalid(t *testing.T) {
	for _, spec := range []string{"", "chartreuse", "bold chartreuse", "#12345", "256", "on-bold", "#xyzxyz"} {
		if _, ok := Parse(spec); ok {
			t.Fatalf("Parse(%q) accepted, want rejection", spec)
		}
	}
}

func TestPaintAndSGRAgreeAndNoneIsAPassthrough(t *testing.T) {
	th := Configure("default", nil)
	if got := th.Paint("accent", "hi"); got != "\x1b[36mhi\x1b[0m" {
		t.Fatalf("Paint = %q", got)
	}
	if got := th.SGR("accent"); got != "\x1b[36m" {
		t.Fatalf("SGR = %q", got)
	}
	// "border" defaults to none.
	if got := th.Paint("border", "hi"); got != "hi" {
		t.Fatalf("none slot painted: %q", got)
	}
	if got := th.SGR("border"); got != "" {
		t.Fatalf("none slot SGR = %q", got)
	}
	if got := th.Paint("not_a_slot", "hi"); got != "hi" {
		t.Fatalf("unknown slot painted: %q", got)
	}
}

func TestNamedThemeOverlaysDefaultsAndKeepsOmittedSlots(t *testing.T) {
	mono := Configure("mono", nil)
	if got := mono.SGR("tab_active"); got != "\x1b[7m" {
		t.Fatalf("mono tab_active = %q", got)
	}
	// "section" is not overridden by mono, so it keeps the stock bold.
	if got := mono.SGR("section"); got != "\x1b[1m" {
		t.Fatalf("mono section = %q", got)
	}
}

// The NO_COLOR contract: the configuration layer resolves NO_COLOR to "mono",
// and "mono" must be attribute-only — never a color code — and must disable the
// border gradient.
func TestMonoIsAttributeOnlyAndDisablesTheGradient(t *testing.T) {
	mono := Configure("mono", nil)
	attributeCodes := map[string]bool{"1": true, "2": true, "3": true, "4": true, "7": true}
	for slot := range Defaults {
		for _, code := range mono.codes[slot] {
			if !attributeCodes[code] {
				t.Fatalf("mono slot %q emits color code %q", slot, code)
			}
		}
	}
	if mono.Gradient("border") != nil {
		t.Fatal("mono must not sweep the border gradient")
	}
}

func TestDefaultThemeCarriesTheStockBorderGradient(t *testing.T) {
	g := Configure("default", nil).Gradient("border")
	if g == nil || len(g.Stops) != 2 || g.Angle != 45 {
		t.Fatalf("gradient = %#v", g)
	}
}

func TestUnknownThemeNameFallsBackToDefaults(t *testing.T) {
	th := Configure("no-such-theme", nil)
	if got := th.SGR("accent"); got != "\x1b[36m" {
		t.Fatalf("accent = %q, want the stock default", got)
	}
}

func TestOverridesWinOverTheNamedThemeButOnlyWhenValid(t *testing.T) {
	th := Configure("mono", map[string]string{
		"accent":       "magenta",
		"section":      "chartreuse", // invalid: the theme value survives
		"not_a_slot":   "bold",       // unknown slot: dropped
		"tab_inactive": "none",
	})
	if got := th.SGR("accent"); got != "\x1b[35m" {
		t.Fatalf("accent = %q", got)
	}
	if got := th.SGR("section"); got != "\x1b[1m" {
		t.Fatalf("invalid override changed the slot: %q", got)
	}
	if got := th.SGR("tab_inactive"); got != "" {
		t.Fatalf("tab_inactive = %q", got)
	}
	if HasSlot("not_a_slot") {
		t.Fatal("unknown slot leaked into the slot table")
	}
}

func TestBorderGradientOverrideIsNotAnSGRSlot(t *testing.T) {
	th := Configure("default", map[string]string{"border_gradient": "#000000 #ffffff @90"})
	g := th.Gradient("border")
	if g == nil || g.Angle != 90 {
		t.Fatalf("gradient = %#v", g)
	}
	off := Configure("default", map[string]string{"border_gradient": "none"})
	if off.Gradient("border") != nil {
		t.Fatal("an unparseable gradient must disable the sweep, not fail")
	}
}

func TestSelectedSlotPrefersTheSelectedVariant(t *testing.T) {
	if got := SelectedSlot("title"); got != "title_selected" {
		t.Fatalf("SelectedSlot(title) = %q", got)
	}
	if got := SelectedSlot("accent"); got != "accent" {
		t.Fatalf("SelectedSlot(accent) = %q", got)
	}
}

func TestPaintOverCombinesTwoSlotsOnPlainText(t *testing.T) {
	th := Configure("default", nil)
	if got := th.PaintOver("section", "accent", "x"); got != "\x1b[1;36mx\x1b[0m" {
		t.Fatalf("PaintOver = %q", got)
	}
}

func TestCompositeOverPreservesExistingFieldColors(t *testing.T) {
	th := Configure("default", map[string]string{"outline_container": "bold"})
	line := "a\x1b[31mb\x1b[0mc"
	want := "\x1b[1ma\x1b[31mb\x1b[0m\x1b[1mc\x1b[0m"
	if got := th.CompositeOver("outline_container", line); got != want {
		t.Fatalf("CompositeOver = %q, want %q", got, want)
	}
	if got := th.CompositeOver("border", line); got != line {
		t.Fatalf("none slot must pass through: %q", got)
	}
}

func TestGeneratedThemesAreCompleteAndParseable(t *testing.T) {
	if len(generatedThemes) != 36 {
		t.Fatalf("%d generated themes, want the 36 the Ruby generator emits", len(generatedThemes))
	}
	for name, slots := range generatedThemes {
		for slot, spec := range slots {
			if slot == SlotBorderGradient {
				continue
			}
			if !HasSlot(slot) {
				t.Fatalf("theme %q names unknown slot %q", name, slot)
			}
			if _, ok := Parse(spec); !ok {
				t.Fatalf("theme %q slot %q has unparseable spec %q", name, slot, spec)
			}
		}
	}
	names := Names()
	if len(names) != 38 {
		t.Fatalf("%d themes, want 36 generated + default + mono", len(names))
	}
	for _, want := range []string{"default", "mono", "dracula", "tokyonight", "gruvbox-dark"} {
		if _, ok := Themes()[want]; !ok {
			t.Fatalf("theme %q missing", want)
		}
	}
}

func TestGeneratedThemeResolvesTruecolorSlots(t *testing.T) {
	th := Configure("dracula", nil)
	// Selection is darkened out of the imported palette — see selectionBand —
	// so it carries a truecolor foreground AND background rather than the
	// scheme's own single colour.
	selection := th.SGR("selection")
	if !strings.Contains(selection, ";38;2;") || !strings.Contains(selection, ";48;2;") {
		t.Fatalf("dracula selection = %q, want truecolor fg and bg", selection)
	}
	if th.Gradient("border") == nil {
		t.Fatal("dracula should carry its own border gradient")
	}
}

// The modal chrome components (button.go, keychips.go, scrollregion.go,
// chrome.go in package tui) paint only these slots, so the whole surface is
// restyleable from config and projectable by a host. Every slot needs a stock
// default AND a mono entry: attribute-only output is the NO_COLOR contract,
// and a modal that loses its buttons' look under mono would be unreadable.
func TestModalChromeSlotsHaveDefaultsAndMonoCoverage(t *testing.T) {
	chromeSlots := []Slot{
		"button_primary", "button_danger", "button_danger_armed", "button_muted",
		"chip_key", "chip_label", "field_border", "field_border_focused",
		"scrollbar_thumb", "scrollbar_track", "modal_backdrop",
		"modal_border_accent", "modal_border_warning",
	}
	for _, slot := range chromeSlots {
		if spec, ok := Defaults[slot]; !ok {
			t.Fatalf("chrome slot %q has no default", slot)
		} else if _, ok := Parse(spec); !ok {
			t.Fatalf("chrome slot %q default %q unparseable", slot, spec)
		}
		spec, ok := builtinThemes["mono"][slot]
		if !ok {
			t.Fatalf("mono does not cover chrome slot %q", slot)
		}
		if _, ok := Parse(spec); !ok {
			t.Fatalf("mono slot %q has unparseable spec %q", slot, spec)
		}
	}

	def := Configure("default", nil)
	for _, slot := range chromeSlots {
		if !HasSlot(slot) {
			t.Fatalf("HasSlot(%q) = false", slot)
		}
		if def.Paint(slot, "x") == "" {
			t.Fatalf("Paint(%q) dropped its text", slot)
		}
	}

	// A host overlay can restyle any of them — this is the seam the Sidecar
	// embed will use for the new surfaces.
	restyle := Configure("default", map[string]string{"button_primary": "bold black on-#ff8800"})
	if restyle.SGR("button_primary") == def.SGR("button_primary") {
		t.Fatal("button_primary override had no effect")
	}
}
