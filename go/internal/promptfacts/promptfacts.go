// Package promptfacts is the registry of short, labeled facts injected into an
// agent's system prompt under a "Current environment" heading. It is the Go
// counterpart of lib/tasks/prompt_facts.rb.
//
// Each fact is toggled with a `prompt.<name> = on|off` config key; presentation
// order follows Registry, not map iteration, because the block is a rendered
// document and two runs that listed the facts in different orders would be two
// different system prompts.
//
// A provider that fails or returns blank is omitted silently, so a flaky future
// source (weather, a calendar, …) can never abort an agent run.
package promptfacts

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// FactName is PromptFacts::FACT_NAME — what a `prompt.<name>` key may spell.
var FactName = regexp.MustCompile(`\A[a-z][a-z0-9_-]*\z`)

// Sources are the injectable providers a fact renders from. Production uses
// time.Now and os.Hostname, which is what lib/tasks/prompt_facts.rb defaults to
// (Time.now / Socket.gethostname).
//
// Deliberately NOT wired to internal/determinism: Ruby's `tasks -p` builds the
// context with the defaults and never passes a harness pin, so pinning here
// would make the Go system prompt differ from the Ruby one on exactly the runs
// a conformance harness performs. The block never reaches stdout, so there is
// nothing to gain and a difference to lose.
type Sources struct {
	Clock    func() time.Time
	Hostname func() (string, error)
}

func (s Sources) clock() time.Time {
	if s.Clock != nil {
		return s.Clock()
	}
	return time.Now()
}

func (s Sources) hostname() (string, error) {
	if s.Hostname != nil {
		return s.Hostname()
	}
	return os.Hostname()
}

// Fact is one registry entry: its config name, whether it is on when nothing
// says otherwise, and how it renders.
type Fact struct {
	Name    string
	Default bool
	// Render answers the fact's value. An error omits this line only — never
	// the block, and never the run.
	Render func(Sources) (string, error)
}

// Registry is PromptFacts::REGISTRY, in its order.
var Registry = []Fact{
	{
		Name:    "datetime",
		Default: true,
		Render:  func(s Sources) (string, error) { return FormatDatetime(s.clock()), nil },
	},
	{
		Name:    "hostname",
		Default: true,
		Render:  func(s Sources) (string, error) { return s.hostname() },
	},
}

// Defaults is the name → default-state map, derived from Registry so the two
// cannot drift. internal/config carries its own copy for the resolution it
// owns; TestConfigAndRegistryAgreeOnTheFactSet pins them together.
func Defaults() map[string]bool {
	defaults := make(map[string]bool, len(Registry))
	for _, fact := range Registry {
		defaults[fact.Name] = fact.Default
	}
	return defaults
}

// Resolve is PromptFacts.resolve: the effective on/off map for every REGISTERED
// fact. `overrides` comes from config (`prompt.*`); an absent key keeps the
// registry default and an unknown key is dropped, because only a fact this
// binary can render may claim to be on.
func Resolve(overrides map[string]bool) map[string]bool {
	resolved := make(map[string]bool, len(Registry))
	for _, fact := range Registry {
		if override, ok := overrides[fact.Name]; ok {
			resolved[fact.Name] = override
			continue
		}
		resolved[fact.Name] = fact.Default
	}
	return resolved
}

// Render builds the "Current environment" block, or "" when nothing is enabled
// or every provider failed. `enabled` is the resolved name → bool map; a nil
// map means "nothing was resolved", which resolves to the registry defaults the
// way `paths.prompt_facts || PromptFacts.resolve` does in Ruby.
func Render(enabled map[string]bool, sources Sources) string {
	if enabled == nil {
		enabled = Resolve(nil)
	}
	var lines []string
	for _, fact := range Registry {
		if !enabled[fact.Name] {
			continue
		}
		value, err := fact.Render(sources)
		if err != nil {
			continue
		}
		value = rubyStrip(value)
		if value == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", fact.Name, value))
	}
	if len(lines) == 0 {
		return ""
	}
	return "Current environment:\n" + strings.Join(lines, "\n")
}

// FormatDatetime is `time.strftime("%Y-%m-%d %a %H:%M %Z")`. The zone
// abbreviation is the last field, so an agent reading "09:00" knows which 09:00
// it is without a second question.
func FormatDatetime(when time.Time) string {
	return when.Format("2006-01-02 Mon 15:04 MST")
}

// ParseToggle is PromptFacts.parse_toggle: a strict three-way answer. The
// second result is false for a value that names neither state, which the config
// parser treats as "say nothing" rather than as off — a typo must not silently
// disable a fact the user meant to enable.
func ParseToggle(value string) (bool, bool) {
	switch strings.ToLower(rubyStrip(value)) {
	case "on", "true", "1":
		return true, true
	case "off", "false", "0":
		return false, true
	default:
		return false, false
	}
}

// rubyStrip is String#strip: ASCII whitespace and NUL, and nothing else.
// strings.TrimSpace would also take U+00A0 and the other Unicode spaces, so a
// sidecar or a hostname containing one would be classified blank here and
// non-blank by the oracle.
func rubyStrip(text string) string {
	return strings.Trim(text, " \t\n\v\f\r\x00")
}
