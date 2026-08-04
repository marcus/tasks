package llm

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The Hermes agent (Nous Research) — an agentic CLI installed locally. It runs
// tools and edits files autonomously, so it fits the Agent contract as just
// another spawn: a different binary and flags, the same protocol.
//
// Hermes reads its model and endpoint from ~/.hermes/config.yaml. We pass -m and
// --provider per invocation so the selected model wins without mutating the
// user's global Hermes config. Point it at a local Ollama model via Hermes' own
// config (an "ollama-launch"/custom provider whose base_url is
// http://127.0.0.1:11434/v1); this adapter only names the model and provider.
//
// Verified against Hermes v0.17.0 (2026-06). The CLI is an external contract,
// not a stable API — re-check `hermes --help` when upgrading. Notably:
//
//	-z / --oneshot PROMPT     one-shot, prints ONLY the final answer (sync CLI)
//	chat -q / --query PROMPT  single non-interactive query, streams the
//	                          transcript including tool previews (TUI); add -Q
//	                          to silence it, which we deliberately do NOT.
//	-m / --model, --provider  model and inference-provider overrides
//	--yolo                    bypass approval prompts — required headless, the
//	                          analogue of claude's --dangerously-skip-permissions
//	--accept-hooks            auto-approve config.yaml shell hooks (no TTY)
type Hermes struct {
	// OllamaURL is the endpoint the availability probe pings.
	OllamaURL string
	// InferenceProvider is Hermes' `--provider`. Empty omits the flag, which
	// falls back to Hermes' own default provider.
	InferenceProvider string
}

const (
	DefaultOllamaURL = "http://127.0.0.1:11434"
	// DefaultInferenceProvider is Hermes' conventional local-Ollama provider
	// name; overridable via config, or set empty to fall back to Hermes' default.
	DefaultInferenceProvider = "ollama-launch"
)

// NewHermes applies the two defaults. A nil inferenceProvider means "unset", and
// takes the conventional name; a non-nil empty string means "deliberately none",
// and drops the --provider flag.
func NewHermes(ollamaURL string, inferenceProvider *string) Hermes {
	if ollamaURL == "" {
		ollamaURL = DefaultOllamaURL
	}
	provider := DefaultInferenceProvider
	if inferenceProvider != nil {
		provider = strings.TrimSpace(*inferenceProvider)
	}
	return Hermes{OllamaURL: ollamaURL, InferenceProvider: provider}
}

func (Hermes) DefaultBinary() string { return "hermes" }

// Argv: Hermes has no --append-system-prompt, so the context (TASK_AGENT.md plus
// the file locations) is prepended to the prompt text. Hermes may also
// auto-inject any AGENTS.md it finds in cwd; the data directory should not carry
// one — our injected copy is authoritative about the contract and the absolute
// file locations.
func (h Hermes) Argv(binary, system, prompt, model string, stream bool) []string {
	full := prependSystem(system, prompt)
	var argv []string
	if stream {
		argv = []string{binary, "chat", "-q", full}
	} else {
		argv = []string{binary, "-z", full}
	}
	if model != "" {
		argv = append(argv, "--model", model)
	}
	if h.InferenceProvider != "" {
		argv = append(argv, "--provider", h.InferenceProvider)
	}
	return append(argv, "--yolo", "--accept-hooks")
}

// Reachable pings the model endpoint: an installed Hermes pointed at a dead
// Ollama is still a dead end, so it surfaces as unavailable rather than as a run
// that fails after the user has waited for it.
//
// Short timeouts, because this runs synchronously from a submit path — a dead
// endpoint must fail fast rather than stall the event loop. /api/tags answers
// instantly when Ollama is up; it only lists local models.
func (h Hermes) Reachable() bool {
	endpoint, err := url.Parse(h.OllamaURL)
	if err != nil || endpoint.Host == "" {
		return false
	}
	endpoint.Path = "/api/tags"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""

	client := &http.Client{
		Transport: &http.Transport{
			DialContext:           (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext,
			ResponseHeaderTimeout: 500 * time.Millisecond,
			DisableKeepAlives:     true,
		},
		Timeout: time.Second,
	}
	response, err := client.Get(endpoint.String())
	if err != nil {
		return false
	}
	defer response.Body.Close()
	// Net::HTTPSuccess is the 2xx family, and nothing else.
	return response.StatusCode >= 200 && response.StatusCode < 300
}
