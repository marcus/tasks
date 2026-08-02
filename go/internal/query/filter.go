// Package query contains the shared read-query inputs and operations.
package query

import (
	"fmt"
	"strings"
)

const (
	ScopeOpen     = "open"
	ScopeProposed = "proposed"
	ScopeDone     = "done"
	ScopeArchived = "archived"
	ScopeAll      = "all"
)

var stateOrder = []string{"PROPOSED", "INBOX", "TODO", "NEXT", "WAITING", "DONE", "CANCELLED"}

// Filter is the immutable selection input shared by read adapters.
type Filter struct {
	Scope           string
	DeferredOnly    bool
	UnavailableOnly bool
	SomedayOnly     bool
	RecurringOnly   bool
	BodySearch      bool
	DelegatedOnly   bool
	AgentReadyOnly  bool
	Contexts        []string
	Tags            []string
	Priority        string
	State           string
	Text            []string
}

// FilterOptions supplies Filter's constructor fields.
type FilterOptions struct {
	Scope           string   `json:"scope"`
	DeferredOnly    bool     `json:"deferred_only"`
	UnavailableOnly bool     `json:"unavailable_only"`
	SomedayOnly     bool     `json:"someday_only"`
	RecurringOnly   bool     `json:"recurring_only"`
	BodySearch      bool     `json:"body_search"`
	DelegatedOnly   bool     `json:"delegated_only"`
	AgentReadyOnly  bool     `json:"agent_ready_only"`
	Contexts        []string `json:"contexts"`
	Tags            []string `json:"tags"`
	Priority        string   `json:"priority"`
	State           string   `json:"state"`
	Text            []string `json:"text"`
}

// NewFilter validates and normalizes one filter value.
func NewFilter(options FilterOptions) (Filter, error) {
	scope := strings.ToLower(options.Scope)
	if scope == "" {
		scope = ScopeOpen
	}
	if !knownScope(scope) {
		return Filter{}, fmt.Errorf("unknown task scope: %s", options.Scope)
	}
	if options.DeferredOnly && options.SomedayOnly {
		return Filter{}, fmt.Errorf("--deferred and --someday are mutually exclusive")
	}
	if options.UnavailableOnly && scope != ScopeOpen {
		return Filter{}, fmt.Errorf("--unavailable is only valid with --open")
	}
	if options.DelegatedOnly && options.AgentReadyOnly {
		return Filter{}, fmt.Errorf("--delegated and --agent-ready are mutually exclusive")
	}
	if options.AgentReadyOnly && scope != ScopeOpen {
		return Filter{}, fmt.Errorf("--agent-ready is only valid with --open")
	}
	priority := strings.ToUpper(options.Priority)
	if priority != "" && priority != "A" && priority != "B" && priority != "C" {
		return Filter{}, fmt.Errorf("priority must be A, B, C, or none")
	}
	state := strings.ToUpper(options.State)
	if state != "" && !knownState(state) {
		return Filter{}, fmt.Errorf("state must be one of %s", strings.Join(stateOrder, ", "))
	}
	return Filter{
		Scope: scope, DeferredOnly: options.DeferredOnly, UnavailableOnly: options.UnavailableOnly,
		SomedayOnly: options.SomedayOnly, RecurringOnly: options.RecurringOnly, BodySearch: options.BodySearch,
		DelegatedOnly: options.DelegatedOnly, AgentReadyOnly: options.AgentReadyOnly,
		Contexts: copyStrings(options.Contexts), Tags: copyStrings(options.Tags), Priority: priority,
		State: state, Text: copyStrings(options.Text),
	}, nil
}

// ParsedFilter is the legacy list syntax plus its JSON rendering switch.
type ParsedFilter struct {
	Filter Filter
	JSON   bool
}

// ParseCLI translates the legacy list arguments into a Filter.
func ParseCLI(args []string) (ParsedFilter, error) {
	options := FilterOptions{Scope: ScopeOpen}
	json := false
	scopes := make(map[string]struct{})
	for _, arg := range args {
		switch arg {
		case "--open", "-o":
			options.Scope = ScopeOpen
			scopes[ScopeOpen] = struct{}{}
		case "--proposed":
			options.Scope = ScopeProposed
			scopes[ScopeProposed] = struct{}{}
		case "--done", "-d":
			options.Scope = ScopeDone
			scopes[ScopeDone] = struct{}{}
		case "--archived", "-x":
			options.Scope = ScopeArchived
			scopes[ScopeArchived] = struct{}{}
		case "--all", "-a":
			options.Scope = ScopeAll
			scopes[ScopeAll] = struct{}{}
		case "--deferred", "-D":
			options.DeferredOnly = true
		case "--unavailable":
			options.UnavailableOnly = true
		case "--someday", "--on-hold":
			options.SomedayOnly = true
		case "--recurring", "-R":
			options.RecurringOnly = true
		case "--delegated":
			options.DelegatedOnly = true
		case "--agent-ready":
			options.AgentReadyOnly = true
		case "--body", "-b":
			options.BodySearch = true
		case "--json":
			json = true
		default:
			switch {
			case len(arg) == 2 && arg[0] == '-' && strings.ContainsRune("ABC", rune(arg[1])):
				options.Priority = arg[1:]
			case strings.HasPrefix(arg, "@"):
				options.Contexts = append(options.Contexts, arg)
			case len(arg) > 1 && strings.HasPrefix(arg, "+"):
				options.Tags = append(options.Tags, arg[1:])
			case strings.HasPrefix(arg, "/"):
				options.Text = append(options.Text, arg[1:])
			case strings.HasPrefix(arg, "-"):
				return ParsedFilter{}, fmt.Errorf("unknown flag: %s", arg)
			default:
				options.Text = append(options.Text, arg)
			}
		}
	}
	if len(scopes) > 1 {
		return ParsedFilter{}, fmt.Errorf("task lifecycle scopes are mutually exclusive")
	}
	filter, err := NewFilter(options)
	if err != nil {
		return ParsedFilter{}, err
	}
	return ParsedFilter{Filter: filter, JSON: json}, nil
}

func (filter Filter) IncludeArchive() bool {
	return filter.Scope == ScopeArchived || filter.Scope == ScopeAll
}

func (filter Filter) States() []string {
	var scoped []string
	switch filter.Scope {
	case ScopeOpen:
		scoped = []string{"INBOX", "TODO", "NEXT", "WAITING"}
	case ScopeProposed:
		scoped = []string{"PROPOSED"}
	case ScopeDone:
		scoped = []string{"DONE", "CANCELLED"}
	default:
		scoped = stateOrder
	}
	if filter.State == "" {
		return copyStrings(scoped)
	}
	for _, state := range scoped {
		if state == filter.State {
			return []string{state}
		}
	}
	return []string{}
}

func (filter Filter) TextQuery() string { return strings.ToLower(strings.Join(filter.Text, " ")) }

func knownScope(scope string) bool {
	return scope == ScopeOpen || scope == ScopeProposed || scope == ScopeDone || scope == ScopeArchived || scope == ScopeAll
}

func knownState(state string) bool {
	for _, candidate := range stateOrder {
		if candidate == state {
			return true
		}
	}
	return false
}
func copyStrings(values []string) []string { return append([]string(nil), values...) }
