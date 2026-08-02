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
	scope           string
	deferredOnly    bool
	unavailableOnly bool
	somedayOnly     bool
	recurringOnly   bool
	bodySearch      bool
	delegatedOnly   bool
	agentReadyOnly  bool
	contexts        []string
	tags            []string
	priority        string
	state           string
	text            []string
}

// FilterOptions supplies Filter's constructor fields.
type FilterOptions struct {
	Scope           *string  `json:"scope"`
	DeferredOnly    bool     `json:"deferred_only"`
	UnavailableOnly bool     `json:"unavailable_only"`
	SomedayOnly     bool     `json:"someday_only"`
	RecurringOnly   bool     `json:"recurring_only"`
	BodySearch      bool     `json:"body_search"`
	DelegatedOnly   bool     `json:"delegated_only"`
	AgentReadyOnly  bool     `json:"agent_ready_only"`
	Contexts        []string `json:"contexts"`
	Tags            []string `json:"tags"`
	Priority        *string  `json:"priority"`
	State           *string  `json:"state"`
	Text            []string `json:"text"`
}

// NewFilter validates and normalizes one filter value.
func NewFilter(options FilterOptions) (Filter, error) {
	scope := ScopeOpen
	if options.Scope != nil {
		scope = *options.Scope
	}
	if !knownScope(scope) {
		return Filter{}, fmt.Errorf("unknown task scope: %s", scope)
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
	priority := ""
	if options.Priority != nil {
		priority = strings.ToUpper(*options.Priority)
	}
	if options.Priority != nil && priority != "A" && priority != "B" && priority != "C" {
		return Filter{}, fmt.Errorf("priority must be A, B, C, or none")
	}
	state := ""
	if options.State != nil {
		state = strings.ToUpper(*options.State)
	}
	if options.State != nil && !knownState(state) {
		return Filter{}, fmt.Errorf("state must be one of %s", strings.Join(stateOrder, ", "))
	}
	return Filter{
		scope: scope, deferredOnly: options.DeferredOnly, unavailableOnly: options.UnavailableOnly,
		somedayOnly: options.SomedayOnly, recurringOnly: options.RecurringOnly, bodySearch: options.BodySearch,
		delegatedOnly: options.DelegatedOnly, agentReadyOnly: options.AgentReadyOnly,
		contexts: copyStrings(options.Contexts), tags: copyStrings(options.Tags), priority: priority,
		state: state, text: copyStrings(options.Text),
	}, nil
}

// ParsedFilter is the legacy list syntax plus its JSON rendering switch.
type ParsedFilter struct {
	filter Filter
	json   bool
}

// ParseCLI translates the legacy list arguments into a Filter.
func ParseCLI(args []string) (ParsedFilter, error) {
	options := FilterOptions{Scope: stringPointer(ScopeOpen)}
	json := false
	scopes := make(map[string]struct{})
	for _, arg := range args {
		switch arg {
		case "--open", "-o":
			options.Scope = stringPointer(ScopeOpen)
			scopes[ScopeOpen] = struct{}{}
		case "--proposed":
			options.Scope = stringPointer(ScopeProposed)
			scopes[ScopeProposed] = struct{}{}
		case "--done", "-d":
			options.Scope = stringPointer(ScopeDone)
			scopes[ScopeDone] = struct{}{}
		case "--archived", "-x":
			options.Scope = stringPointer(ScopeArchived)
			scopes[ScopeArchived] = struct{}{}
		case "--all", "-a":
			options.Scope = stringPointer(ScopeAll)
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
				options.Priority = stringPointer(arg[1:])
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
	return ParsedFilter{filter: filter, json: json}, nil
}

func (parsed ParsedFilter) Filter() Filter { return parsed.filter }

func (parsed ParsedFilter) JSON() bool { return parsed.json }

func (filter Filter) Scope() string { return filter.scope }

func (filter Filter) DeferredOnly() bool { return filter.deferredOnly }

func (filter Filter) UnavailableOnly() bool { return filter.unavailableOnly }

func (filter Filter) SomedayOnly() bool { return filter.somedayOnly }

func (filter Filter) RecurringOnly() bool { return filter.recurringOnly }

func (filter Filter) BodySearch() bool { return filter.bodySearch }

func (filter Filter) DelegatedOnly() bool { return filter.delegatedOnly }

func (filter Filter) AgentReadyOnly() bool { return filter.agentReadyOnly }

func (filter Filter) Contexts() []string { return copyStrings(filter.contexts) }

func (filter Filter) Tags() []string { return copyStrings(filter.tags) }

func (filter Filter) Priority() string { return filter.priority }

func (filter Filter) State() string { return filter.state }

func (filter Filter) Text() []string { return copyStrings(filter.text) }

func (filter Filter) IncludeArchive() bool {
	return filter.scope == ScopeArchived || filter.scope == ScopeAll
}

func (filter Filter) States() []string {
	var scoped []string
	switch filter.scope {
	case ScopeOpen:
		scoped = []string{"INBOX", "TODO", "NEXT", "WAITING"}
	case ScopeProposed:
		scoped = []string{"PROPOSED"}
	case ScopeDone:
		scoped = []string{"DONE", "CANCELLED"}
	default:
		scoped = stateOrder
	}
	if filter.state == "" {
		return copyStrings(scoped)
	}
	for _, state := range scoped {
		if state == filter.state {
			return []string{state}
		}
	}
	return []string{}
}

func (filter Filter) TextQuery() string { return strings.ToLower(strings.Join(filter.text, " ")) }

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
func copyStrings(values []string) []string {
	valuesCopy := make([]string, len(values))
	copy(valuesCopy, values)
	return valuesCopy
}

func stringPointer(value string) *string { return &value }
