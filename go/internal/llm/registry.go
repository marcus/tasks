package llm

import (
	"fmt"
	"strings"

	"tasks-go/internal/determinism"
)

// Entry is a single (provider, model) choice — what the TUI switcher cycles and
// what the CLI resolves a default for. Because every backend is an agent, there
// is no kind/transport branch here: an Entry only names which harness and model.
type Entry struct {
	Provider string
	Model    string
}

func (e Entry) String() string { return e.Provider + ":" + e.Model }

var providerLabels = map[string]string{
	"claude-cli": "claude",
	"cursor-cli": "cursor",
}

var modelLabels = map[Entry]string{
	{"cursor-cli", "cursor-grok-4.5-low-fast"}: "grok",
	{"cursor-cli", "composer-2.5-fast"}:        "composer",
	{"hermes", "qwen3.6:35b-a3b"}:              "qwen",
	{"hermes", "gemma4:e4b"}:                   "gemma",
}

// UILabel is the short spelling a switcher shows. It never becomes the canonical
// identity: an unknown provider or model passes through verbatim, so a harness
// added tomorrow is displayable today.
func (e Entry) UILabel() string {
	provider, ok := providerLabels[e.Provider]
	if !ok {
		provider = e.Provider
	}
	model, ok := modelLabels[e]
	if !ok {
		model = e.Model
	}
	return provider + ":" + model
}

// Spec maps a provider name to its adapter and model list, assembled from the
// built-in defaults overlaid with the user's config.
//
// Transport is informational (for optional-dependency handling), never a
// call-site branch.
type Spec struct {
	Provider  string
	Models    []string
	Transport string
	Settings  Settings

	// harness builds the adapter for this provider from the resolved settings.
	// Adding a harness is a two-line change: an adapter type plus a defaults
	// entry. Config can then tune a provider's model list or binary with no code
	// change at all.
	harness func(Settings) Harness
}

// Registry is the provider table in DECLARATION order. Order is contract, not
// convenience: `reg.keys.first` is the overall default provider, the switcher
// cycles in this order, and the unknown-provider refusal lists the known names
// in it. A Go map would permute all three between runs.
type Registry struct {
	order []string
	specs map[string]Spec
}

func (r Registry) Keys() []string { return append([]string(nil), r.order...) }

func (r Registry) Get(name string) (Spec, bool) {
	spec, ok := r.specs[name]
	return spec, ok
}

func (r Registry) Has(name string) bool {
	_, ok := r.specs[name]
	return ok
}

// defaultSpec is one built-in provider before config is overlaid.
type defaultSpec struct {
	provider  string
	transport string
	models    []string
	harness   func(Settings) Harness
}

// defaults is Registry::DEFAULTS, in its order.
//
// The overall default is the first provider's first model — claude-cli:sonnet —
// because no local model is fast or reliable enough to default to (see
// eval/llm/results-2026-07-02.md). Within Hermes the default model is
// qwen3.6:35b-a3b: the one local model that reliably drove the CLI in the eval
// (0 corruptions, all 8 task dimensions), replacing gemma4:e4b, which derailed.
// gemma4:e4b is kept as a lighter, faster fallback in the switcher.
var defaults = []defaultSpec{
	{
		provider: "claude-cli", transport: "cli",
		models:  []string{"sonnet", "opus", "haiku"},
		harness: func(Settings) Harness { return ClaudeCLI{} },
	},
	{
		provider: "hermes", transport: "cli",
		models: []string{"qwen3.6:35b-a3b", "gemma4:e4b"},
		harness: func(s Settings) Harness {
			return NewHermes(s.OllamaURL, s.InferenceProvider)
		},
	},
	{
		provider: "cursor-cli", transport: "cli",
		models:  []string{"composer-2.5-fast"},
		harness: func(Settings) Harness { return CursorCLI{} },
	},
}

// BuildRegistry overlays the user's config onto the built-in defaults.
func BuildRegistry(conf Config) Registry {
	registry := Registry{specs: make(map[string]Spec, len(defaults))}
	for _, base := range defaults {
		over := conf.ProviderSettings(base.provider)
		models := base.models
		if len(over.Models) > 0 {
			models = over.Models
		}
		registry.order = append(registry.order, base.provider)
		registry.specs[base.provider] = Spec{
			Provider:  base.provider,
			Models:    models,
			Transport: base.transport,
			Settings:  over,
			harness:   base.harness,
		}
	}
	return registry
}

// Entries is the flat, ordered (provider, model) list for the switcher. The
// resolved default is moved to the front so cycling starts there — out of the
// box that is claude-cli:sonnet, so nothing changes for current users.
func Entries(conf Config) []Entry {
	registry := BuildRegistry(conf)
	var all []Entry
	// DefaultEntry only errors on an EXPLICIT unknown provider, and there is no
	// explicit one here, so this cannot fail.
	if entry, err := defaultEntryIn(registry, conf, "", ""); err == nil {
		all = append(all, entry)
	}
	for _, name := range registry.order {
		spec, _ := registry.Get(name)
		for _, model := range spec.Models {
			all = append(all, Entry{Provider: name, Model: model})
		}
	}

	seen := make(map[Entry]bool, len(all))
	unique := all[:0]
	for _, entry := range all {
		if seen[entry] {
			continue
		}
		seen[entry] = true
		unique = append(unique, entry)
	}
	return unique
}

// DefaultEntry is the starting (provider, model).
//
// Explicit arguments (a CLI --provider/--model) win, then config, then the first
// registered provider and its first model. A model given explicitly is honoured
// even when it is not in the provider's list, so a user can run any model their
// harness supports without editing config.
//
// config.Model only applies when the provider was NOT explicitly overridden: it
// is paired with config.Provider, so `--provider hermes` alone resolves to
// hermes's own default model rather than to a claude tier left in config.
func DefaultEntry(provider, model string, conf Config) (Entry, error) {
	return defaultEntryIn(BuildRegistry(conf), conf, provider, model)
}

func defaultEntryIn(registry Registry, conf Config, provider, model string) (Entry, error) {
	explicit := nonblank(provider)
	// An explicit provider (a CLI --provider flag) that is not registered is a
	// USER error — reject it rather than silently running the default backend. A
	// stale config provider falls back quietly, so a typo cannot brick the tool.
	if explicit != "" && !registry.Has(explicit) {
		return Entry{}, fmt.Errorf("unknown LLM provider: %s (known: %s)",
			rubyInspect(explicit), strings.Join(registry.order, ", "))
	}

	name := explicit
	if name == "" {
		name = validKey(conf.Provider, registry)
	}
	if name == "" && len(registry.order) > 0 {
		name = registry.order[0]
	}
	spec, ok := registry.Get(name)
	if !ok {
		return Entry{}, fmt.Errorf("unknown LLM provider: %s", rubyInspect(name))
	}

	resolved := nonblank(model)
	if resolved == "" && explicit == "" {
		resolved = nonblank(conf.Model)
	}
	if resolved == "" && len(spec.Models) > 0 {
		resolved = spec.Models[0]
	}
	return Entry{Provider: name, Model: resolved}, nil
}

// BuildOptions is what instantiating an adapter needs beyond the entry. The
// model is absent on purpose: it rides along at call time.
type BuildOptions struct {
	// Root is the working directory the harness runs in.
	Root string
	// System is the resolved system-context string, or "".
	System string
	// Path is the PATH the availability probe searches; see Options.Path.
	Path string
}

// Build instantiates the adapter for an entry, ready to RunSync or Start.
func Build(entry Entry, options BuildOptions, conf Config) (*Agent, error) {
	spec, ok := BuildRegistry(conf).Get(entry.Provider)
	if !ok {
		return nil, fmt.Errorf("unknown LLM provider: %s", rubyInspect(entry.Provider))
	}
	return New(spec.harness(spec.Settings), Options{
		Root:              options.Root,
		System:            options.System,
		Binary:            spec.Settings.Command,
		Path:              options.Path,
		OllamaURL:         spec.Settings.OllamaURL,
		InferenceProvider: spec.Settings.InferenceProvider,
	}), nil
}

// PathFrom is the PATH an availability probe should search, taken from the
// environment the adapter boundary resolved rather than from the process.
func PathFrom(env determinism.Env) string { return env.Get("PATH") }

func validKey(name string, registry Registry) string {
	if key := nonblank(name); key != "" && registry.Has(key) {
		return key
	}
	return ""
}

func nonblank(value string) string { return strings.TrimSpace(value) }

// rubyInspect is String#inspect for the names that reach these two refusals: a
// provider name a user typed. The message is stderr contract, and the quotes are
// part of it.
func rubyInspect(value string) string {
	var quoted strings.Builder
	quoted.WriteByte('"')
	for _, char := range value {
		switch char {
		case '"':
			quoted.WriteString(`\"`)
		case '\\':
			quoted.WriteString(`\\`)
		case '\n':
			quoted.WriteString(`\n`)
		case '\t':
			quoted.WriteString(`\t`)
		default:
			quoted.WriteRune(char)
		}
	}
	quoted.WriteByte('"')
	return quoted.String()
}
