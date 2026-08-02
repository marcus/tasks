package query

import (
	"reflect"
	"testing"
)

func TestParseCLIComposesLegacyFilters(t *testing.T) {
	parsed, err := ParseCLI([]string{"--all", "--deferred", "--recurring", "--body", "@computer", "+important", "-A", "/flight", "plans", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.JSON || parsed.Filter.Scope != ScopeAll || !parsed.Filter.DeferredOnly || !parsed.Filter.RecurringOnly || !parsed.Filter.BodySearch {
		t.Fatalf("unexpected parsed filter: %#v", parsed)
	}
	if got := parsed.Filter.TextQuery(); got != "flight plans" {
		t.Fatalf("TextQuery() = %q", got)
	}
	if got := parsed.Filter.States(); len(got) != 7 || got[0] != "PROPOSED" || got[6] != "CANCELLED" {
		t.Fatalf("States() = %#v", got)
	}
}

func TestNewFilterIntersectsStateWithScope(t *testing.T) {
	filter, err := NewFilter(FilterOptions{Scope: ScopeOpen, State: "done"})
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
			filter, err := NewFilter(FilterOptions{Scope: scope, State: state})
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
	filter, err := NewFilter(FilterOptions{Scope: scope})
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
		if !reflect.DeepEqual(first.Filter, second.Filter) {
			t.Fatalf("%s and %s differ: %#v %#v", aliases[0], aliases[1], first.Filter, second.Filter)
		}
	}
}
