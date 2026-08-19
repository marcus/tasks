package shortcuts

import (
	"strings"
	"testing"
)

// Mirrors test/test_shortcuts.rb. The Ruby tests that validate the registry by
// reflecting over Tui::App are represented here by the explicit handler and
// predicate sets Validate accepts — the application half is another package's.

func handlerFor(t *testing.T, sequence string, context Context) string {
	t.Helper()
	e, ok := Match(sequence, context, nil)
	if !ok {
		t.Fatalf("no binding for %q in %s", sequence, context)
	}
	return e.Handler
}

func assertUnbound(t *testing.T, sequence string, context Context) {
	t.Helper()
	if e, ok := Match(sequence, context, nil); ok {
		t.Fatalf("%q is bound to %s in %s, want unbound", sequence, e.Handler, context)
	}
}

func TestRegistryValidates(t *testing.T) {
	if err := Validate(ValidateOptions{}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestEveryEntryDeclaresContextHandlerAvailabilityAndMetadata(t *testing.T) {
	for _, e := range Registry {
		if len(e.Sequences) == 0 {
			if !e.Palette.Active() && !e.DocOnly {
				t.Fatalf("keyless action %q is neither palette-enabled nor doc-only", e.Description)
			}
		} else if e.DocOnly {
			t.Fatalf("doc-only entry %q declares key sequences", e.Description)
		}
		if e.DocOnly && e.Handler != "" {
			t.Fatalf("doc-only entry %q declares a handler", e.Description)
		}
		if !e.DocOnly && e.Handler == "" {
			t.Fatalf("entry %q declares no handler", e.Description)
		}
		if e.DisplayKey == "" || e.Description == "" || len(e.Contexts) == 0 {
			t.Fatalf("entry %#v is missing display metadata", e)
		}
		if e.FooterLabel == "" || e.FooterPriority <= 0 {
			t.Fatalf("entry %#v is missing footer metadata", e)
		}
		if !e.DocOnly && e.CommandID == "" {
			t.Fatalf("entry %#v is missing a command id", e)
		}
		if !e.DocOnly && e.Availability == "" {
			t.Fatalf("entry %q declares no availability", e.Description)
		}
	}
}

func TestContextLookupKeepsTaskActionsInListAndDetailOnly(t *testing.T) {
	ordinary := func(name string) bool { return name == DefaultAvailability }
	if e, _ := Match("c", List, ordinary); e.Handler != "complete_selected" {
		t.Fatalf("c in list = %s", e.Handler)
	}
	if e, _ := Match("c", Detail, ordinary); e.Handler != "complete_selected" {
		t.Fatalf("c in detail = %s", e.Handler)
	}
	if got := handlerFor(t, "#", List); got != "delete_selected" {
		t.Fatalf("# in list = %s", got)
	}
	if got := handlerFor(t, "#", Detail); got != "delete_selected" {
		t.Fatalf("# in detail = %s", got)
	}
	if got := handlerFor(t, "\x1b[3~", List); got != "delete_selected" {
		t.Fatalf("Delete key in list = %s", got)
	}
	assertUnbound(t, "c", Modal)
	assertUnbound(t, "x", Detail) // list-only archive must not leak into details
	assertUnbound(t, "\x1b[C", Detail)
}

func TestModalNavigationResolvesIndependently(t *testing.T) {
	cases := map[string]string{
		"\x04":    "modal_half_down",
		"\x15":    "modal_half_up",
		"\x06":    "modal_page_down",
		"\x1b[6~": "modal_page_down",
		"\x02":    "modal_page_up",
		"/":       "modal_start_filter",
		"\x1b":    "modal_confirmation",
		"q":       "modal_confirmation",
	}
	for sequence, want := range cases {
		if got := handlerFor(t, sequence, Modal); got != want {
			t.Fatalf("%q in modal = %s, want %s", sequence, got, want)
		}
	}
}

func TestAtOpensContextPaletteFromList(t *testing.T) {
	if got := handlerFor(t, "@", List); got != "open_context_palette" {
		t.Fatalf("@ = %s", got)
	}
	assertUnbound(t, "@", Detail)
	assertUnbound(t, "@", Modal)
}

func TestOrderingBindingsCoverCSIAndEscapePrefixedAltVariants(t *testing.T) {
	for _, sequence := range []string{"\x1b[1;3A", "\x1b\x1b[A", "\x1bk"} {
		if got := handlerFor(t, sequence, List); got != "move_subtree_up" {
			t.Fatalf("%q = %s", sequence, got)
		}
	}
	for _, sequence := range []string{"\x1b[1;3B", "\x1b\x1b[B", "\x1bj"} {
		if got := handlerFor(t, sequence, List); got != "move_subtree_down" {
			t.Fatalf("%q = %s", sequence, got)
		}
	}
	if got := handlerFor(t, ">", List); got != "indent_subtree" {
		t.Fatalf("> = %s", got)
	}
	if got := handlerFor(t, "<", List); got != "outdent_subtree" {
		t.Fatalf("< = %s", got)
	}
}

func TestSixViewsHaveDirectJumpKeysAndSevenIsUnbound(t *testing.T) {
	for index, id := range []string{"view-agenda", "view-next", "view-quadrants", "view-projects", "view-outline", "view-inbox"} {
		key := string(rune('1' + index))
		e, ok := Match(key, List, nil)
		if !ok || e.Handler != "jump_view" || e.CommandID != id {
			t.Fatalf("%s = %#v", key, e)
		}
	}
	assertUnbound(t, "7", List)
}

// D and W are the uppercase variants of the lowercase concepts they extend
// (d = date, D = delegate). They must resolve identically on the list and in
// the task detail panel, and must not leak elsewhere.
func TestDelegationBindingsCoverListAndDetailOnly(t *testing.T) {
	cases := map[string]string{"D": "delegate_selected", "W": "set_work_ref_selected"}
	for key, handler := range cases {
		for _, context := range []Context{List, Detail} {
			e, ok := Match(key, context, nil)
			if !ok || e.Handler != handler {
				t.Fatalf("%s in %s = %#v", key, context, e)
			}
			if e.Availability != "delegation_action_available?" || e.Palette.Predicate != "delegation_action_available?" {
				t.Fatalf("%s availability = %#v", key, e)
			}
			if e.DisplayKey != key {
				t.Fatalf("%s display key = %q", key, e.DisplayKey)
			}
		}
		assertUnbound(t, key, Modal)
		assertUnbound(t, key, TaskEdit)
	}
	if e, _ := Match("D", List, nil); e.Form != "delegate" {
		t.Fatalf("D form = %q", e.Form)
	}
	if e, _ := Match("W", List, nil); e.Form != "work_ref" {
		t.Fatalf("W form = %q", e.Form)
	}
}

func TestDelegationBindingsDoNotCollideWithExistingUppercaseActions(t *testing.T) {
	if err := Validate(ValidateOptions{}); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, context := range []Context{List, Detail} {
		entries, err := Entries(context, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"D", "W"} {
			count := 0
			for _, e := range entries {
				for _, s := range e.Sequences {
					if s == key {
						count++
					}
				}
			}
			if count != 1 {
				t.Fatalf("%s has %d bindings in %s, want exactly one", key, count, context)
			}
		}
	}
}

func TestUnknownLookupContextIsRejected(t *testing.T) {
	_, err := Entries("bogus", true)
	if err == nil || !strings.Contains(err.Error(), "unknown shortcut context") {
		t.Fatalf("err = %v", err)
	}
}

func TestGlobalBindingResolvesInEveryContext(t *testing.T) {
	for _, context := range Contexts {
		if got := handlerFor(t, "\x03", context); got != "quit" {
			t.Fatalf("ctrl-c in %s = %s", context, got)
		}
	}
}

func TestTaskEditBindingsAreCompleteAndIsolated(t *testing.T) {
	cases := []struct {
		sequence string
		context  Context
		handler  string
	}{
		{"e", Detail, "start_task_edit"},
		{"\t", Detail, "focus_prompt"},
		{"\x1b[Z", Detail, "start_task_edit_last"},
		{"\t", TaskEdit, "task_edit_input"},
		{"\x13", TaskEdit, "task_edit_input"},
		{"\x0f", TaskEdit, "task_edit_input"},
		{"\x0b", TaskEdit, "grow_task_panel"},
		{"\x0c", TaskEdit, "shrink_task_panel"},
	}
	for _, c := range cases {
		if got := handlerFor(t, c.sequence, c.context); got != c.handler {
			t.Fatalf("%q in %s = %s, want %s", c.sequence, c.context, got, c.handler)
		}
	}
	assertUnbound(t, "j", TaskEdit)
}

func TestValidationRejectsDuplicateKeyInSameContext(t *testing.T) {
	first := Registry[0]
	duplicate := first
	duplicate.Handler = "select_next"
	err := Validate(ValidateOptions{Entries: []Entry{first, duplicate}})
	if err == nil || !strings.Contains(err.Error(), "duplicate shortcut") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidationRejectsDuplicateSequencesInsideOneEntry(t *testing.T) {
	e := Registry[0]
	e.Sequences = []string{"k", "k"}
	err := Validate(ValidateOptions{Entries: []Entry{e}})
	if err == nil || !strings.Contains(err.Error(), "sequences must be unique") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidationAllowsPaletteOnlyAndDocsOnlyActions(t *testing.T) {
	paletteOnly := Registry[0]
	paletteOnly.Sequences = nil
	paletteOnly.DisplayKey = "palette"
	paletteOnly.Palette = PaletteAlways
	if err := Validate(ValidateOptions{Entries: []Entry{paletteOnly}}); err != nil {
		t.Fatalf("palette-only: %v", err)
	}

	docsOnly := Registry[0]
	docsOnly.Sequences = nil
	docsOnly.DisplayKey = "click"
	docsOnly.Palette = PaletteNever
	docsOnly.DocOnly = true
	docsOnly.Handler = ""
	if err := Validate(ValidateOptions{Entries: []Entry{docsOnly}}); err != nil {
		t.Fatalf("docs-only: %v", err)
	}

	unreachable := Registry[0]
	unreachable.Sequences = nil
	unreachable.Palette = PaletteNever
	err := Validate(ValidateOptions{Entries: []Entry{unreachable}})
	if err == nil || !strings.Contains(err.Error(), "must be palette-enabled") {
		t.Fatalf("unreachable: %v", err)
	}

	docsWithHandler := Registry[0]
	docsWithHandler.Sequences = nil
	docsWithHandler.DocOnly = true
	docsWithHandler.Handler = "open_detail"
	err = Validate(ValidateOptions{Entries: []Entry{docsWithHandler}})
	if err == nil || !strings.Contains(err.Error(), "doc_only shortcuts must not declare a handler") {
		t.Fatalf("docs with handler: %v", err)
	}
}

func TestValidationAllowsSameKeyInDifferentContexts(t *testing.T) {
	modal := Registry[0]
	modal.Contexts = []Context{Modal}
	detail := Registry[0]
	detail.Contexts = []Context{Detail}
	detail.Handler = "select_next"
	if err := Validate(ValidateOptions{Entries: []Entry{modal, detail}}); err != nil {
		t.Fatalf("modal + detail: %v", err)
	}

	list := Registry[0]
	list.Contexts = []Context{List}
	other := Registry[0]
	other.Contexts = []Context{Modal}
	other.Handler = "modal_up"
	if err := Validate(ValidateOptions{Entries: []Entry{list, other}}); err != nil {
		t.Fatalf("list + modal: %v", err)
	}
}

func TestValidationRejectsMissingHandlerAndAvailabilityHook(t *testing.T) {
	known := ValidateOptions{
		Handlers:   map[string]bool{"select_prev": true},
		Predicates: map[string]bool{DefaultAvailability: true},
	}

	missingHandler := Registry[0]
	missingHandler.Handler = "not_an_app_handler"
	opts := known
	opts.Entries = []Entry{missingHandler}
	if err := Validate(opts); err == nil || !strings.Contains(err.Error(), "missing shortcut handler") {
		t.Fatalf("err = %v", err)
	}

	missingAvailability := Registry[0]
	missingAvailability.Availability = "not_an_app_predicate"
	opts = known
	opts.Entries = []Entry{missingAvailability}
	if err := Validate(opts); err == nil || !strings.Contains(err.Error(), "missing shortcut availability") {
		t.Fatalf("err = %v", err)
	}

	missingPalette := Registry[0]
	missingPalette.Palette = PaletteWhen("not_an_app_predicate")
	opts = known
	opts.Entries = []Entry{missingPalette}
	if err := Validate(opts); err == nil || !strings.Contains(err.Error(), "missing shortcut palette availability") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidationRejectsPaletteHandlerThatRequiresTheOriginalKey(t *testing.T) {
	var jump Entry
	for _, e := range Registry {
		if e.Handler == "jump_view" {
			jump = e
		}
	}
	jump.Palette = PaletteAlways
	err := Validate(ValidateOptions{
		Entries:       []Entry{jump},
		KeyedHandlers: map[string]bool{"jump_view": true},
	})
	if err == nil || !strings.Contains(err.Error(), "must not require a key") {
		t.Fatalf("err = %v", err)
	}
}

// The `r` hint is the only place the grammar is advertised before the popup
// opens, so it names one example per shape the parser accepts.
func TestRecurHintAdvertisesTheCalendarGrammar(t *testing.T) {
	for _, e := range Registry {
		if e.Handler == "open_recur_popup" {
			if e.Description != "recur — weekly · every mon · m:15 · off" {
				t.Fatalf("description = %q", e.Description)
			}
			return
		}
	}
	t.Fatal("no recur binding")
}

func TestOpenLinkShortcutAdvertisesSingleAndMultipleLinks(t *testing.T) {
	for _, e := range Registry {
		if e.Handler == "open_link" {
			if e.Description != "open task link(s) in browser" {
				t.Fatalf("description = %q", e.Description)
			}
			return
		}
	}
	t.Fatal("no open-link binding")
}

func TestPaletteEntriesAreContextualAndAvailable(t *testing.T) {
	resolve := func(name string) bool {
		switch name {
		case "selected_action_available?", "completion_action_available?", DefaultAvailability:
			return true
		default:
			return false
		}
	}
	handlers := map[string]bool{}
	for _, e := range PaletteEntries(Detail, resolve) {
		handlers[e.Handler] = true
	}
	if !handlers["complete_selected"] {
		t.Fatal("complete_selected must be offered")
	}
	if !handlers["delete_selected"] {
		t.Fatal("delete_selected must be offered")
	}
	for _, unwanted := range []string{
		"open_recur_popup", "open_link", "delegate_selected",
		"set_work_ref_selected", "open_action_palette",
	} {
		if handlers[unwanted] {
			t.Fatalf("%s must not be offered", unwanted)
		}
	}
}

// An unavailable binding is still returned so dispatch consumes the key rather
// than falling through to another context.
func TestMatchReturnsAnUnavailableBindingRatherThanNothing(t *testing.T) {
	e, ok := Match("D", List, func(string) bool { return false })
	if !ok || e.Handler != "delegate_selected" {
		t.Fatalf("match = %#v, %v", e, ok)
	}
}

// Two entries share `r` and `a` in list/detail; the available one must win.
func TestMatchPrefersTheAvailableBindingWhenSequencesOverlap(t *testing.T) {
	proposal := func(name string) bool { return name == "proposal_action_available?" }
	recurrence := func(name string) bool { return name == DefaultAvailability }

	if e, _ := Match("r", List, proposal); e.Handler != "reject_proposal" {
		t.Fatalf("r with a proposal = %s", e.Handler)
	}
	if e, _ := Match("r", List, recurrence); e.Handler != "open_recur_popup" {
		t.Fatalf("r without a proposal = %s", e.Handler)
	}
	if e, _ := Match("a", List, proposal); e.Handler != "approve_proposal" {
		t.Fatalf("a with a proposal = %s", e.Handler)
	}
	// `c` is the same shape: on a proposal it approves AND completes, because
	// the store refuses to complete a PROPOSED task at all.
	if e, _ := Match("c", List, proposal); e.Handler != "approve_and_complete_proposal" {
		t.Fatalf("c with a proposal = %s", e.Handler)
	}
	if e, _ := Match("c", Detail, proposal); e.Handler != "approve_and_complete_proposal" {
		t.Fatalf("c on a proposal detail = %s", e.Handler)
	}
	if e, _ := Match("c", List, recurrence); e.Handler != "complete_selected" {
		t.Fatalf("c without a proposal = %s", e.Handler)
	}
}

func TestEntriesCanExcludeGlobals(t *testing.T) {
	withGlobal, err := Entries(List, true)
	if err != nil {
		t.Fatal(err)
	}
	withoutGlobal, err := Entries(List, false)
	if err != nil {
		t.Fatal(err)
	}
	globalCount := 0
	for _, entry := range Registry {
		if entry.hasContext(Global) {
			globalCount++
		}
	}
	if len(withGlobal) != len(withoutGlobal)+globalCount {
		t.Fatalf("global inclusion changed nothing: %d vs %d", len(withGlobal), len(withoutGlobal))
	}
}

// Help is generated from the registry, so every entry must carry the text the
// help modal shows.
func TestEveryEntryCanBeRenderedInHelp(t *testing.T) {
	for _, context := range Contexts {
		entries, err := Entries(context, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if strings.TrimSpace(e.DisplayKey) == "" || strings.TrimSpace(e.Description) == "" {
				t.Fatalf("entry %#v cannot be rendered in help", e)
			}
		}
	}
}
