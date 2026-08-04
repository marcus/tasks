// Package theme is the semantic color layer. Rendering code never names a
// color — it paints a *slot* (SlotAccent, SlotSelection, SlotLink, …) and the
// theme resolves the slot to SGR codes. Slots come from three layers, later
// wins:
//
//  1. defaults — the stock look
//  2. a named theme (config `theme = mono`, TASKS_THEME env, or NO_COLOR)
//  3. per-slot overrides from the config file (`color.accent = magenta`)
//
// A slot spec is space-separated tokens: attributes (bold, dim, italic,
// underline, reverse), a named color (red, bright-red, gray, …), a 256-color
// index (0–255), or a hex color (#rrggbb). Prefix a color with `on-` for the
// background (on-blue, on-#1e2030). `none` means unstyled. An invalid spec is
// dropped and the slot falls back to its theme value, so a typo degrades the
// look rather than crashing the TUI.
//
// Go port of Ruby's lib/tui/theme.rb. Unlike Ruby, a Theme is a value rather
// than process-global module state: callers hold the *Theme they configured.
//
// Which theme NAME is in force (including the NO_COLOR convention, which
// selects "mono" when nothing explicit is set) is resolved by the
// configuration layer, exactly as Ruby resolves it in lib/tasks/config.rb.
// This package consumes the resolved name.
package theme

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/tui/term/ansi"
	"github.com/marcus/tasks/internal/tui/term/border"
)

// Slot names a semantic style. Values match the Ruby symbol names exactly, so
// a config file's `color.<slot>` key maps across unchanged.
type Slot = string

// Defaults is the stock look, keyed by slot.
var Defaults = map[Slot]string{
	"tab_active":            "bold reverse",
	"tab_inactive":          "gray",
	"tab_agenda":            "cyan",
	"tab_next":              "green",
	"tab_quadrants":         "yellow",
	"tab_inbox":             "magenta",
	"tab_projects":          "blue",
	"tab_approvals":         "yellow",
	"tab_agenda_active":     "bold reverse cyan",
	"tab_next_active":       "bold reverse green",
	"tab_quadrants_active":  "bold reverse yellow",
	"tab_inbox_active":      "bold reverse magenta",
	"tab_projects_active":   "bold reverse blue",
	"tab_approvals_active":  "bold reverse yellow",
	"selection":             "reverse",
	"accent":                "cyan",
	"prompt":                "bold cyan",
	"section":               "bold",
	"approval_section":      "bold yellow",
	"inbox_section":         "bold magenta",
	"modal_title":           "bold",
	"panel_title":           "bold",
	"border":                "none",
	"context":               "bold cyan",
	"context_selected":      "bold cyan",
	"context_filter_active": "bold green",
	"project":               "magenta",
	"project_selected":      "bold magenta",
	"title":                 "none",
	"title_selected":        "bold",
	"priority":              "bold",
	"priority_selected":     "bold",
	"muted":                 "gray",
	"muted_selected":        "gray",
	"outline_thread":        "gray",
	"outline_container":     "bold",
	"note":                  "gray",
	"description":           "gray",
	"link":                  "underline cyan",
	"detail_label":          "bold gray",
	"link_system":           "cyan",
	"error":                 "red",
	"warning":               "yellow",
	"form_group":            "bold",
	"form_group_label":      "bold black on-cyan",
	"form_label":            "bold",
	"form_value":            "none",
	"form_focus":            "bold cyan",
	"form_cursor":           "reverse",
	"form_error":            "bold red",
	"form_unsaved":          "bold yellow",
	"form_hint":             "gray",
	"form_disabled":         "dim",
	"form_choice_cursor":    "bold cyan",
	"form_choice_selected":  "bold",
	"due_overdue":           "red",
	"due_soon":              "yellow",
	"due_week":              "cyan",
	"due_far":               "gray",
	"due_overdue_selected":  "bold red",
	"due_soon_selected":     "bold yellow",
	"due_week_selected":     "bold cyan",
	"due_far_selected":      "gray",
	"state_next":            "cyan",
	"state_waiting":         "yellow",
	"state_done":            "gray",
}

// SlotBorderGradient is not an SGR slot (it is per-cell truecolor, not one code
// list), so it lives outside Defaults. A theme supplies its own via this key;
// "none" (or any unparseable value) disables it and the border falls back to
// the solid "border" slot.
const SlotBorderGradient = "border_gradient"

// DefaultBorderGradient is a blue→violet diagonal sweep, contrasty enough to
// read on both light and dark terminals.
const DefaultBorderGradient = "#5aa2f7 #c678dd @45"

// builtinThemes overlay Defaults; slots they omit keep the stock value.
// "mono" is attribute-only (also the NO_COLOR fallback).
var builtinThemes = map[string]map[Slot]string{
	"default": {},
	"mono": {
		"tab_active": "reverse", "tab_inactive": "dim", "accent": "bold",
		"tab_agenda": "none", "tab_next": "none", "tab_quadrants": "none",
		"tab_inbox": "none", "tab_projects": "none", "tab_approvals": "none",
		"tab_agenda_active": "reverse", "tab_next_active": "reverse",
		"tab_quadrants_active": "reverse", "tab_inbox_active": "reverse",
		"tab_projects_active": "reverse", "tab_approvals_active": "reverse",
		"border": "dim", "border_gradient": "none",
		"prompt": "bold", "modal_title": "bold", "panel_title": "bold", "context": "bold",
		"approval_section": "bold", "inbox_section": "bold underline",
		"context_selected": "bold", "context_filter_active": "bold",
		"project": "none", "project_selected": "bold",
		"title": "none", "title_selected": "bold", "muted": "dim", "muted_selected": "dim",
		"outline_thread": "dim",
		"note":           "dim", "description": "dim", "detail_label": "bold", "link_system": "none",
		"link": "underline", "error": "bold", "warning": "bold",
		"form_group": "bold", "form_group_label": "bold reverse",
		"form_label": "bold", "form_value": "none",
		"form_focus": "bold", "form_cursor": "reverse", "form_error": "bold",
		"form_unsaved": "bold", "form_hint": "dim", "form_disabled": "dim",
		"form_choice_cursor": "bold", "form_choice_selected": "bold",
		"due_overdue": "bold", "due_soon": "none", "due_week": "none",
		"due_overdue_selected": "bold", "due_soon_selected": "bold",
		"due_week_selected": "bold", "due_far_selected": "dim",
		"due_far": "dim", "state_next": "bold", "state_waiting": "none",
		"state_done": "dim", "priority_selected": "bold",
	},
}

// Themes returns the full theme table: the two builtins plus the generated
// color schemes.
func Themes() map[string]map[Slot]string {
	all := make(map[string]map[Slot]string, len(builtinThemes)+len(generatedThemes))
	for name, slots := range builtinThemes {
		all[name] = slots
	}
	for name, slots := range generatedThemes {
		all[name] = slots
	}
	return all
}

// Names lists every theme name in sorted order.
func Names() []string {
	all := Themes()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

var namedColors = map[string]int{
	"black": 30, "red": 31, "green": 32, "yellow": 33,
	"blue": 34, "magenta": 35, "cyan": 36, "white": 37,
	"gray": 90, "grey": 90,
	"bright-black": 90, "bright-red": 91, "bright-green": 92,
	"bright-yellow": 93, "bright-blue": 94, "bright-magenta": 95,
	"bright-cyan": 96, "bright-white": 97,
}

var attributes = map[string]int{
	"bold": 1, "dim": 2, "italic": 3, "underline": 4, "reverse": 7,
}

var (
	hexRe   = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	indexRe = regexp.MustCompile(`^\d{1,3}$`)
)

// Theme is a resolved slot table.
type Theme struct {
	codes     map[Slot][]string
	gradients map[string]*border.Gradient
}

// Configure installs a theme: a named theme plus per-slot overrides. Unknown
// theme names, unknown slots, and invalid specs all fall back rather than
// fail — the config file must never be able to break the TUI.
func Configure(name string, overrides map[string]string) *Theme {
	named := Themes()[name]
	merged := make(map[Slot]string, len(Defaults))
	for slot, spec := range Defaults {
		merged[slot] = spec
	}
	for slot, spec := range named {
		if slot == SlotBorderGradient {
			continue
		}
		merged[slot] = spec
	}

	gradSpec := DefaultBorderGradient
	if spec, ok := named[SlotBorderGradient]; ok {
		gradSpec = spec
	}

	for slot, spec := range overrides {
		if slot == SlotBorderGradient {
			gradSpec = spec
			continue
		}
		if _, known := Defaults[slot]; !known {
			continue
		}
		if _, ok := Parse(spec); ok {
			merged[slot] = spec
		}
	}

	codes := make(map[Slot][]string, len(merged))
	for slot, spec := range merged {
		parsed, ok := Parse(spec)
		if !ok {
			parsed, _ = Parse(Defaults[slot])
		}
		codes[slot] = parsed
	}

	return &Theme{
		codes:     codes,
		gradients: map[string]*border.Gradient{"border": border.ParseGradient(gradSpec)},
	}
}

// Default is the stock theme with no overrides.
func Default() *Theme { return Configure("default", nil) }

// Gradient returns the parsed border gradient for slot, or nil when the theme
// disables it. The border package decides whether the terminal can render it.
func (t *Theme) Gradient(slot string) *border.Gradient { return t.gradients[slot] }

// Paint styles str for slot. Unknown slots and "none" slots pass the string
// through untouched.
func (t *Theme) Paint(slot Slot, str string) string {
	codes := t.codes[slot]
	if len(codes) == 0 {
		return str
	}
	return ansi.Color(str, codes...)
}

// SGR is the raw opening sequence for a slot (e.g. "\e[7m"), or "" when the
// slot is unset or "none". Frame uses this to composite the selection
// background UNDER a row's own field styling instead of repainting the row.
func (t *Theme) SGR(slot Slot) string {
	codes := t.codes[slot]
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

// HasSlot reports whether slot is a known slot name.
func HasSlot(slot Slot) bool {
	_, ok := Defaults[slot]
	return ok
}

// SelectedSlot returns "<slot>_selected" when that variant exists, else slot.
func SelectedSlot(slot Slot) Slot {
	candidate := slot + "_selected"
	if HasSlot(candidate) {
		return candidate
	}
	return slot
}

// PaintOver combines two slots' codes on fresh plain text.
func (t *Theme) PaintOver(baseSlot, slot Slot, str string) string {
	codes := append(append([]string{}, t.codes[baseSlot]...), t.codes[slot]...)
	if len(codes) == 0 {
		return str
	}
	return ansi.Color(str, codes...)
}

// CompositeOver lays slot's style OVER a line that already carries its own
// field SGRs (each closed with a reset). A "none"/unset slot is a passthrough.
// Closes with one trailing reset.
func (t *Theme) CompositeOver(slot Slot, str string) string {
	seq := t.SGR(slot)
	if seq == "" {
		return str
	}
	return ansi.Composite(seq, str) + "\x1b[0m"
}

// Parse turns a spec string into SGR codes. It reports ok=false if any token is
// invalid — the whole spec is rejected so a half-styled slot cannot happen.
// "none"/"plain" parse to an empty, valid code list.
func Parse(spec string) ([]string, bool) {
	spec = strings.ToLower(strings.TrimSpace(spec))
	if spec == "" {
		return nil, false
	}
	if spec == "none" || spec == "plain" {
		return []string{}, true
	}
	var codes []string
	for _, tok := range strings.Fields(spec) {
		code, ok := tokenCode(tok)
		if !ok {
			return nil, false
		}
		codes = append(codes, code)
	}
	return codes, true
}

func tokenCode(tok string) (string, bool) {
	bg := strings.HasPrefix(tok, "on-")
	name := tok
	if bg {
		name = strings.TrimPrefix(tok, "on-")
	}
	if !bg {
		if a, ok := attributes[tok]; ok {
			return strconv.Itoa(a), true
		}
	}
	if c, ok := namedColors[name]; ok {
		if bg {
			c += 10
		}
		return strconv.Itoa(c), true
	}
	if hexRe.MatchString(name) {
		r, _ := strconv.ParseInt(name[1:3], 16, 32)
		g, _ := strconv.ParseInt(name[3:5], 16, 32)
		b, _ := strconv.ParseInt(name[5:7], 16, 32)
		lead := "38"
		if bg {
			lead = "48"
		}
		return lead + ";2;" + strconv.FormatInt(r, 10) + ";" + strconv.FormatInt(g, 10) + ";" + strconv.FormatInt(b, 10), true
	}
	if indexRe.MatchString(name) {
		n, _ := strconv.Atoi(name)
		if n <= 255 {
			lead := "38"
			if bg {
				lead = "48"
			}
			return lead + ";5;" + strconv.Itoa(n), true
		}
	}
	return "", false
}
