package query

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseCLIComposesLegacyFilters(t *testing.T) {
	parsed, err := ParseCLI([]string{"--all", "--deferred", "--recurring", "--body", "@computer", "+important", "-A", "/flight", "plans", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	filter := parsed.Filter()
	if !parsed.JSON() || filter.Scope() != ScopeAll || !filter.DeferredOnly() || !filter.RecurringOnly() || !filter.BodySearch() {
		t.Fatalf("unexpected parsed filter: %#v", parsed)
	}
	if got := filter.TextQuery(); got != "flight plans" {
		t.Fatalf("TextQuery() = %q", got)
	}
	if got := filter.States(); len(got) != 7 || got[0] != "PROPOSED" || got[6] != "CANCELLED" {
		t.Fatalf("States() = %#v", got)
	}
}

func TestNewFilterIntersectsStateWithScope(t *testing.T) {
	filter, err := NewFilter(FilterOptions{Scope: stringPointer(ScopeOpen), State: stringPointer("done")})
	if err != nil {
		t.Fatal(err)
	}
	if got := filter.States(); len(got) != 0 {
		t.Fatalf("States() = %#v", got)
	}
}

func TestStateIntersectionProperty(t *testing.T) {
	for _, scope := range []string{ScopeOpen, ScopeProposed, ScopeDone, ScopeArchived, ScopeAll} {
		for _, state := range []string{"PROPOSED", "INBOX", "TODO", "NEXT", "WAITING", "DONE", "CANCELLED"} {
			filter, err := NewFilter(FilterOptions{Scope: stringPointer(scope), State: stringPointer(state)})
			if err != nil {
				t.Fatalf("NewFilter(%q, %q): %v", scope, state, err)
			}
			got := filter.States()
			allowed := false
			for _, candidate := range NewScopeFilter(t, scope).States() {
				if candidate == state {
					allowed = true
				}
			}
			if allowed && !reflect.DeepEqual(got, []string{state}) {
				t.Fatalf("%s/%s = %#v, want %q", scope, state, got, state)
			}
			if !allowed && len(got) != 0 {
				t.Fatalf("%s/%s = %#v, want empty", scope, state, got)
			}
		}
	}
}

func NewScopeFilter(t *testing.T, scope string) Filter {
	t.Helper()
	filter, err := NewFilter(FilterOptions{Scope: stringPointer(scope)})
	if err != nil {
		t.Fatal(err)
	}
	return filter
}

func TestFilterRejectionsMatchRuby(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"unknown", []string{"--unknown"}, "unknown flag: --unknown"},
		{"scopes", []string{"--done", "--archived"}, "task lifecycle scopes are mutually exclusive"},
		{"unavailable", []string{"--done", "--unavailable"}, "--unavailable is only valid with --open"},
		{"exclusive", []string{"--deferred", "--someday"}, "--deferred and --someday are mutually exclusive"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseCLI(test.args)
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseCLIScopeAliasesAreEquivalent(t *testing.T) {
	cases := [][]string{{"--open", "-o"}, {"--done", "-d"}, {"--archived", "-x"}, {"--all", "-a"}}
	for _, aliases := range cases {
		first, err := ParseCLI([]string{aliases[0]})
		if err != nil {
			t.Fatal(err)
		}
		second, err := ParseCLI([]string{aliases[1]})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first.Filter(), second.Filter()) {
			t.Fatalf("%s and %s differ: %#v %#v", aliases[0], aliases[1], first.Filter(), second.Filter())
		}
	}
}

func TestNewFilterRejectsExplicitInvalidConstructorValues(t *testing.T) {
	cases := []struct {
		name    string
		options FilterOptions
		want    string
	}{
		{"uppercase scope", FilterOptions{Scope: stringPointer("OPEN")}, "unknown task scope: OPEN"},
		{"empty priority", FilterOptions{Priority: stringPointer("")}, "priority must be A, B, C, or none"},
		{"empty state", FilterOptions{State: stringPointer("")}, "state must be one of PROPOSED, INBOX, TODO, NEXT, WAITING, DONE, CANCELLED"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewFilter(test.options)
			if err == nil || err.Error() != test.want {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestFilterOwnsConstructorAndAccessorSlices(t *testing.T) {
	contexts, tags, text := []string{"@computer"}, []string{"important"}, []string{"plans"}
	filter, err := NewFilter(FilterOptions{Contexts: contexts, Tags: tags, Text: text})
	if err != nil {
		t.Fatal(err)
	}
	contexts[0], tags[0], text[0] = "@phone", "later", "changed"
	gotContexts, gotTags, gotText := filter.Contexts(), filter.Tags(), filter.Text()
	gotContexts[0], gotTags[0], gotText[0] = "@errands", "mutated", "again"
	if got := filter.Contexts(); !reflect.DeepEqual(got, []string{"@computer"}) {
		t.Fatalf("Contexts() = %#v", got)
	}
	if got := filter.Tags(); !reflect.DeepEqual(got, []string{"important"}) {
		t.Fatalf("Tags() = %#v", got)
	}
	if got := filter.Text(); !reflect.DeepEqual(got, []string{"plans"}) {
		t.Fatalf("Text() = %#v", got)
	}
	defaultFilter, err := NewFilter(FilterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if defaultFilter.Contexts() == nil || defaultFilter.Tags() == nil || defaultFilter.Text() == nil {
		t.Fatal("constructed empty collections must be non-nil")
	}
}

// TestNewerUnicodeLowerOverrides pins the Unicode 16.0/17.0 downcase overrides
// against the sweep in
// the Unicode compatibility evidence archived at ruby-final-2026-08-04:
// TextQuery must produce Ruby's lowered form for each of the 55 codepoints, and
// every lowered form must already be lowercase so the table stays correct when
// Go's own tables catch up and ToLower starts doing the work itself.
func TestNewerUnicodeLowerOverrides(t *testing.T) {
	if len(newerUnicodeLower) != 55 {
		t.Fatalf("override table has %d entries, want the 55 swept divergences", len(newerUnicodeLower))
	}
	for upper, lower := range newerUnicodeLower {
		if upper == lower {
			t.Fatalf("U+%04X maps to itself", upper)
		}
		if got := strings.ToLower(string(lower)); got != string(lower) {
			t.Fatalf("lowered form of U+%04X is not lowercase: ToLower(%q) = %q", upper, string(lower), got)
		}
		filter, err := NewFilter(FilterOptions{Text: []string{string(upper)}})
		if err != nil {
			t.Fatal(err)
		}
		if got := filter.TextQuery(); got != string(lower) {
			t.Fatalf("TextQuery() for U+%04X = %q, want %q", upper, got, string(lower))
		}
	}
}
