// Package query contains the shared read-query inputs and operations.
package query

import (
	"fmt"
	"strings"
	"unicode/utf8"
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
		// Every `when` below is either a literal String — which an argument
		// with invalid bytes can never equal — or a Regexp, and matching a
		// Regexp against such a String raises. Ruby therefore raises here, in
		// argument order: after an earlier unknown flag, before the post-loop
		// mutually-exclusive-scopes check.
		if !utf8.ValidString(arg) {
			return ParsedFilter{}, fmt.Errorf("invalid byte sequence in UTF-8")
		}
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
			tag, isTag := tagArgument(arg)
			switch {
			case len(arg) == 2 && arg[0] == '-' && strings.ContainsRune("ABC", rune(arg[1])):
				options.Priority = stringPointer(arg[1:])
			case strings.HasPrefix(arg, "@"):
				options.Contexts = append(options.Contexts, arg)
			case isTag:
				options.Tags = append(options.Tags, tag)
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

// tagArgument reproduces `/\A\+(.+)/`. Ruby's `.` never matches "\n", so the
// capture is the run of characters between the "+" and the first newline, and
// an argument whose "+" is followed immediately by a newline does not take the
// branch at all — it falls through and keeps its leading "+".
func tagArgument(arg string) (string, bool) {
	if !strings.HasPrefix(arg, "+") {
		return "", false
	}
	rest := arg[1:]
	if index := strings.IndexByte(rest, '\n'); index >= 0 {
		rest = rest[:index]
	}
	return rest, rest != ""
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

// dottedCapitalI is the only character with an unconditional multi-character
// lowercase mapping, which is one of the two differences between Ruby's
// String#downcase (full Unicode case mapping) and strings.ToLower (simple 1:1).
const dottedCapitalI, dottedCapitalIFolded = "İ", "i̇"

// newerUnicodeLower is the other difference: Ruby 4.0.6 ships Unicode 17.0.0
// tables while Go 1.26.5's unicode package is still Unicode 15.0.0, so
// strings.ToLower leaves the case pairs added in Unicode 16.0/17.0 unchanged.
// These are exactly the 55 codepoints whose downcase mapping diverges over the
// whole non-surrogate range U+0000-U+10FFFF, enumerated in
// porting/evidence/query-filter-parse/downcase-divergence-2026-08-02.jsonl.
// The overrides map uppercase to lowercase, so they remain correct once a
// future Go release picks the tables up: ToLower of the lowered form is
// identity, which TestNewerUnicodeLowerOverrides asserts.
var newerUnicodeLower = map[rune]rune{
	0x1C89:  0x1C8A,  // U+1C89 → U+1C8A
	0xA7CB:  0x0264,  // U+A7CB → U+0264
	0xA7CC:  0xA7CD,  // U+A7CC → U+A7CD
	0xA7CE:  0xA7CF,  // U+A7CE → U+A7CF
	0xA7D2:  0xA7D3,  // U+A7D2 → U+A7D3
	0xA7D4:  0xA7D5,  // U+A7D4 → U+A7D5
	0xA7DA:  0xA7DB,  // U+A7DA → U+A7DB
	0xA7DC:  0x019B,  // U+A7DC → U+019B
	0x10D50: 0x10D70, // U+10D50 → U+10D70
	0x10D51: 0x10D71, // U+10D51 → U+10D71
	0x10D52: 0x10D72, // U+10D52 → U+10D72
	0x10D53: 0x10D73, // U+10D53 → U+10D73
	0x10D54: 0x10D74, // U+10D54 → U+10D74
	0x10D55: 0x10D75, // U+10D55 → U+10D75
	0x10D56: 0x10D76, // U+10D56 → U+10D76
	0x10D57: 0x10D77, // U+10D57 → U+10D77
	0x10D58: 0x10D78, // U+10D58 → U+10D78
	0x10D59: 0x10D79, // U+10D59 → U+10D79
	0x10D5A: 0x10D7A, // U+10D5A → U+10D7A
	0x10D5B: 0x10D7B, // U+10D5B → U+10D7B
	0x10D5C: 0x10D7C, // U+10D5C → U+10D7C
	0x10D5D: 0x10D7D, // U+10D5D → U+10D7D
	0x10D5E: 0x10D7E, // U+10D5E → U+10D7E
	0x10D5F: 0x10D7F, // U+10D5F → U+10D7F
	0x10D60: 0x10D80, // U+10D60 → U+10D80
	0x10D61: 0x10D81, // U+10D61 → U+10D81
	0x10D62: 0x10D82, // U+10D62 → U+10D82
	0x10D63: 0x10D83, // U+10D63 → U+10D83
	0x10D64: 0x10D84, // U+10D64 → U+10D84
	0x10D65: 0x10D85, // U+10D65 → U+10D85
	0x16EA0: 0x16EBB, // U+16EA0 → U+16EBB
	0x16EA1: 0x16EBC, // U+16EA1 → U+16EBC
	0x16EA2: 0x16EBD, // U+16EA2 → U+16EBD
	0x16EA3: 0x16EBE, // U+16EA3 → U+16EBE
	0x16EA4: 0x16EBF, // U+16EA4 → U+16EBF
	0x16EA5: 0x16EC0, // U+16EA5 → U+16EC0
	0x16EA6: 0x16EC1, // U+16EA6 → U+16EC1
	0x16EA7: 0x16EC2, // U+16EA7 → U+16EC2
	0x16EA8: 0x16EC3, // U+16EA8 → U+16EC3
	0x16EA9: 0x16EC4, // U+16EA9 → U+16EC4
	0x16EAA: 0x16EC5, // U+16EAA → U+16EC5
	0x16EAB: 0x16EC6, // U+16EAB → U+16EC6
	0x16EAC: 0x16EC7, // U+16EAC → U+16EC7
	0x16EAD: 0x16EC8, // U+16EAD → U+16EC8
	0x16EAE: 0x16EC9, // U+16EAE → U+16EC9
	0x16EAF: 0x16ECA, // U+16EAF → U+16ECA
	0x16EB0: 0x16ECB, // U+16EB0 → U+16ECB
	0x16EB1: 0x16ECC, // U+16EB1 → U+16ECC
	0x16EB2: 0x16ECD, // U+16EB2 → U+16ECD
	0x16EB3: 0x16ECE, // U+16EB3 → U+16ECE
	0x16EB4: 0x16ECF, // U+16EB4 → U+16ECF
	0x16EB5: 0x16ED0, // U+16EB5 → U+16ED0
	0x16EB6: 0x16ED1, // U+16EB6 → U+16ED1
	0x16EB7: 0x16ED2, // U+16EB7 → U+16ED2
	0x16EB8: 0x16ED3, // U+16EB8 → U+16ED3
}

func (filter Filter) TextQuery() string { return Downcase(strings.Join(filter.text, " ")) }

// Downcase is Ruby's String#downcase, which strings.ToLower alone is not: it
// applies the one unconditional multi-character mapping and the case pairs
// added in Unicode versions newer than Go's tables. Every surface that folds
// case for comparison — the text filter, the title index, ref resolution — goes
// through here, so there is one place for that difference to live.
func Downcase(value string) string {
	folded := strings.ReplaceAll(value, dottedCapitalI, dottedCapitalIFolded)
	folded = strings.Map(func(candidate rune) rune {
		if lowered, ok := newerUnicodeLower[candidate]; ok {
			return lowered
		}
		return candidate
	}, folded)
	return strings.ToLower(folded)
}

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
