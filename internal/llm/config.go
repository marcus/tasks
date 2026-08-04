package llm

import (
	"os"
	"regexp"
	"strings"

	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/determinism"
)

// Config is the LLM half of the same flat `key = value` file the task paths use
// (~/.config/tasks/config). Every key is optional and unknown keys are ignored
// — forward-compatible, so old configs keep working and a headless CLI that
// never touches the LLM pays nothing. Recognised keys:
//
//	llm_provider = hermes                 default harness for `-p` and the TUI
//	llm_model    = gemma4:e4b             default model within that provider
//	<provider>_models  = a, b, c          override a provider's model list
//	<provider>_command = /path/to/binary  override the binary a provider spawns
//	hermes_provider = ollama-launch       Hermes inference provider (--provider)
//	ollama_url  = http://127.0.0.1:11434  endpoint for Hermes' availability probe
//
// `<provider>` is any registry key, e.g. `claude-cli_models`, `hermes_models`,
// or `cursor-cli_models`.
type Config struct {
	Provider  string
	Model     string
	Providers map[string]Settings
}

// Settings are one provider's overrides. They become the adapter's constructor
// arguments, which is what lets config tune a harness without any code change —
// the growth path as the harness/model landscape shifts.
type Settings struct {
	Models  []string
	Command string
	// InferenceProvider is a pointer because "unset" and "set to empty" are
	// different answers: unset takes Hermes' conventional local-Ollama provider,
	// and empty deliberately drops the --provider flag.
	InferenceProvider *string
	OllamaURL         string
}

// ProviderSettings is the overrides for one provider, or the zero value.
func (c Config) ProviderSettings(name string) Settings {
	return c.Providers[name]
}

var (
	modelsKey  = regexp.MustCompile(`\A(.+)_models\z`)
	commandKey = regexp.MustCompile(`\A(.+)_command\z`)
)

// LoadConfig reads the config file. `path` empty resolves it the way every other
// surface does, through internal/config, so the LLM settings can never end up
// reading a different file than the task paths did.
func LoadConfig(env determinism.Env, path string) Config {
	if path == "" {
		path = config.ConfigFile(env)
	}
	raw, order := readRaw(path)

	providers := map[string]Settings{}
	for _, key := range order {
		value := raw[key]
		if match := modelsKey.FindStringSubmatch(key); match != nil {
			settings := providers[match[1]]
			settings.Models = splitCSV(value)
			providers[match[1]] = settings
		} else if match := commandKey.FindStringSubmatch(key); match != nil {
			settings := providers[match[1]]
			settings.Command = value
			providers[match[1]] = settings
		}
	}
	// `hermes_provider` and `ollama_url` are spelled outside the `<provider>_`
	// convention — the first because `_provider` would collide with the naming
	// of every other key, the second because it names an endpoint rather than a
	// harness setting.
	if value, ok := raw["hermes_provider"]; ok {
		settings := providers["hermes"]
		settings.InferenceProvider = &value
		providers["hermes"] = settings
	}
	if value, ok := raw["ollama_url"]; ok {
		settings := providers["hermes"]
		settings.OllamaURL = value
		providers["hermes"] = settings
	}

	return Config{
		Provider:  presence(raw["llm_provider"]),
		Model:     presence(raw["llm_model"]),
		Providers: providers,
	}
}

// readRaw parses the flat file, returning the values and the order the keys
// appeared in. A missing file is an empty config, never an error: the LLM
// settings are entirely optional.
//
// Note what this does NOT do: it does not strip inline comments, because
// LLM::Config.read_raw does not either. `llm_model = sonnet # fast` therefore
// names a model called "sonnet # fast", which is a difference from
// Tasks::Config's parser and is the oracle's behavior.
func readRaw(path string) (map[string]string, []string) {
	values := map[string]string{}
	var order []string

	data, err := os.ReadFile(path)
	if err != nil {
		return values, order
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			// String#partition with no separator yields an empty value, which
			// the `unless val.empty?` guard then drops.
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		values[key] = value
	}
	return values, order
}

func splitCSV(value string) []string {
	var parts []string
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// presence is `str.to_s.strip.empty? ? nil : str.strip`.
func presence(value string) string { return strings.TrimSpace(value) }
