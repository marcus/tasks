package llm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/determinism"
)

func emptyConfig() Config { return Config{Providers: map[string]Settings{}} }

func indexOf(argv []string, needle string) int {
	for index, arg := range argv {
		if arg == needle {
			return index
		}
	}
	return -1
}

func after(t *testing.T, argv []string, flag string) string {
	t.Helper()
	index := indexOf(argv, flag)
	if index < 0 || index+1 >= len(argv) {
		t.Fatalf("%s is missing a value in %v", flag, argv)
	}
	return argv[index+1]
}

// -- ClaudeCLI adapter --------------------------------------------------------

func TestClaudeCommandIncludesModelAndPermissionsFlag(t *testing.T) {
	argv := New(ClaudeCLI{}, Options{Root: "/tmp"}).Command("do the thing", "opus", true)

	if !reflect.DeepEqual(argv[0:2], []string{"claude", "-p"}) {
		t.Fatalf("argv = %v", argv)
	}
	if indexOf(argv, "do the thing") < 0 {
		t.Fatalf("the prompt is not in %v", argv)
	}
	if got := after(t, argv, "--model"); got != "opus" {
		t.Fatalf("--model = %q", got)
	}
	if indexOf(argv, "--dangerously-skip-permissions") < 0 {
		t.Fatalf("a headless run cannot answer permission prompts: %v", argv)
	}
	if indexOf(argv, "--append-system-prompt") >= 0 {
		t.Fatalf("no system context was given, so no flag: %v", argv)
	}
}

func TestClaudeCommandAppendsSystemPromptWhenPresent(t *testing.T) {
	argv := New(ClaudeCLI{}, Options{Root: "/tmp", System: "conventions here"}).Command("x", "sonnet", true)
	if got := after(t, argv, "--append-system-prompt"); got != "conventions here" {
		t.Fatalf("--append-system-prompt = %q", got)
	}
}

func TestCommandOverrideChangesBinary(t *testing.T) {
	argv := New(ClaudeCLI{}, Options{Root: "/tmp", Binary: "/opt/claude"}).Command("x", "sonnet", true)
	if argv[0] != "/opt/claude" {
		t.Fatalf("argv[0] = %q", argv[0])
	}
}

// `claude -p --output-format text` streams the same way either way, so the
// stream hint is a no-op here — it only matters for harnesses with distinct
// one-shot and transcript modes.
func TestClaudeCommandIsTheSameForSyncAndStreamingRuns(t *testing.T) {
	agent := New(ClaudeCLI{}, Options{Root: "/tmp", System: "SYS"})
	streaming := agent.Command("hello", "sonnet", true)
	sync := agent.Command("hello", "sonnet", false)
	if !reflect.DeepEqual(streaming, sync) {
		t.Fatalf("%v != %v", streaming, sync)
	}
}

// The context goes in a FLAG, so the user's prompt reaches the harness verbatim.
func TestClaudeKeepsThePromptSeparateFromTheContext(t *testing.T) {
	argv := New(ClaudeCLI{}, Options{Root: "/tmp", System: "SYS"}).Command("hello", "sonnet", true)
	if argv[2] != "hello" {
		t.Fatalf("prompt = %q", argv[2])
	}
}

// -- CursorCLI adapter --------------------------------------------------------

func TestCursorCommandUsesHeadlessForceTextModeAndModel(t *testing.T) {
	argv := New(CursorCLI{}, Options{Root: "/tmp"}).Command("do the thing", "cursor-grok-4.5-low-fast", true)

	if !reflect.DeepEqual(argv[0:5], []string{"agent", "-p", "--force", "--output-format", "text"}) {
		t.Fatalf("argv = %v", argv)
	}
	if got := after(t, argv, "--model"); got != "cursor-grok-4.5-low-fast" {
		t.Fatalf("--model = %q", got)
	}
	if argv[len(argv)-1] != "do the thing" {
		t.Fatalf("the prompt must be last: %v", argv)
	}
}

func TestCursorCommandPrependsSystemPrompt(t *testing.T) {
	argv := New(CursorCLI{}, Options{Root: "/tmp", System: "SYS"}).Command("hello", "composer-2.5-fast", true)
	if argv[len(argv)-1] != "SYS\n\nhello" {
		t.Fatalf("last = %q", argv[len(argv)-1])
	}
}

func TestCursorCommandIsTheSameForSyncAndStreamingRuns(t *testing.T) {
	agent := New(CursorCLI{}, Options{Root: "/tmp"})
	streaming := agent.Command("hello", "composer-2.5-fast", true)
	sync := agent.Command("hello", "composer-2.5-fast", false)
	if !reflect.DeepEqual(streaming, sync) {
		t.Fatalf("%v != %v", streaming, sync)
	}
}

func TestCursorCommandOverrideChangesBinary(t *testing.T) {
	argv := New(CursorCLI{}, Options{Root: "/tmp", Binary: "/opt/cursor-agent"}).Command("x", "composer-2.5-fast", true)
	if argv[0] != "/opt/cursor-agent" {
		t.Fatalf("argv[0] = %q", argv[0])
	}
}

func TestCursorOmitsTheModelFlagWhenNoModelIsNamed(t *testing.T) {
	argv := New(CursorCLI{}, Options{Root: "/tmp"}).Command("x", "", true)
	if indexOf(argv, "--model") >= 0 {
		t.Fatalf("an empty model must not become an empty flag value: %v", argv)
	}
}

// -- Hermes adapter -----------------------------------------------------------

func TestHermesStreamUsesChatQueryAndPrependsSystem(t *testing.T) {
	argv := New(NewHermes("", nil), Options{Root: "/tmp", System: "SYS"}).Command("hello", "gemma4:e4b", true)

	if !reflect.DeepEqual(argv[0:3], []string{"hermes", "chat", "-q"}) {
		t.Fatalf("argv = %v", argv)
	}
	if argv[3] != "SYS\n\nhello" {
		t.Fatalf("no --append-system-prompt flag exists, so the context is prepended: %q", argv[3])
	}
	if got := after(t, argv, "--model"); got != "gemma4:e4b" {
		t.Fatalf("--model = %q", got)
	}
	if got := after(t, argv, "--provider"); got != "ollama-launch" {
		t.Fatalf("--provider = %q", got)
	}
	if indexOf(argv, "--yolo") < 0 {
		t.Fatalf("--yolo is required for headless edits: %v", argv)
	}
	if indexOf(argv, "--accept-hooks") < 0 {
		t.Fatalf("--accept-hooks is required without a TTY: %v", argv)
	}
}

func TestHermesSyncUsesOneshot(t *testing.T) {
	argv := New(NewHermes("", nil), Options{Root: "/tmp"}).Command("hello", "gemma4:e4b", false)
	if !reflect.DeepEqual(argv[0:2], []string{"hermes", "-z"}) {
		t.Fatalf("argv = %v", argv)
	}
	if argv[2] != "hello" {
		t.Fatalf("argv[2] = %q", argv[2])
	}
}

// The one adapter whose sync and streaming invocations genuinely differ.
func TestHermesSyncAndStreamingDiffer(t *testing.T) {
	agent := New(NewHermes("", nil), Options{Root: "/tmp"})
	if reflect.DeepEqual(agent.Command("hi", "m", true), agent.Command("hi", "m", false)) {
		t.Fatal("hermes has distinct one-shot and transcript modes")
	}
}

func TestHermesInferenceProviderOmittedWhenBlank(t *testing.T) {
	blank := ""
	argv := New(NewHermes("", &blank), Options{Root: "/tmp"}).Command("x", "m", true)
	if indexOf(argv, "--provider") >= 0 {
		t.Fatalf("a deliberately empty inference provider drops the flag: %v", argv)
	}
}

// Unset and set-to-empty are different answers, and only one of them keeps the
// conventional local-Ollama provider.
func TestHermesUnsetInferenceProviderTakesTheDefault(t *testing.T) {
	if got := NewHermes("", nil).InferenceProvider; got != DefaultInferenceProvider {
		t.Fatalf("InferenceProvider = %q", got)
	}
	blank := "   "
	if got := NewHermes("", &blank).InferenceProvider; got != "" {
		t.Fatalf("a whitespace-only setting is empty, not a provider named %q", got)
	}
}

func TestHermesOmitsTheModelFlagWhenNoModelIsNamed(t *testing.T) {
	argv := New(NewHermes("", nil), Options{Root: "/tmp"}).Command("x", "", true)
	if indexOf(argv, "--model") >= 0 {
		t.Fatalf("argv = %v", argv)
	}
}

// An installed Hermes pointed at a dead Ollama is still a dead end.
func TestHermesAvailableFalseWhenModelEndpointDown(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "hermes"))
	agent := New(NewHermes("http://127.0.0.1:1", nil), Options{Path: dir})
	if agent.Available() {
		t.Fatal("an unreachable endpoint must report unavailable")
	}
}

// The live-server half of the same probe: installed AND the endpoint answers.
func TestHermesAvailableWhenTheEndpointAnswers(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"models":[]}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "hermes"))
	if !New(NewHermes(server.URL, nil), Options{Path: dir}).Available() {
		t.Fatal("an installed hermes with a live endpoint must be available")
	}
	if path != "/api/tags" {
		t.Fatalf("the probe asked for %q; /api/tags is the endpoint that answers instantly", path)
	}
}

// A non-2xx answer is not "up": Net::HTTPSuccess is the 2xx family and nothing
// else, so a proxy returning 500 must not read as a working model server.
func TestHermesAvailableFalseWhenTheEndpointRefuses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "hermes"))
	if New(NewHermes(server.URL, nil), Options{Path: dir}).Available() {
		t.Fatal("a 500 is not an available model server")
	}
}

// The endpoint is derived from the configured base URL, replacing any path it
// carries — a Hermes config pointed at .../v1 still probes /api/tags.
func TestHermesProbeReplacesTheConfiguredPath(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "hermes"))
	New(NewHermes(server.URL+"/v1", nil), Options{Path: dir}).Available()
	if path != "/api/tags" {
		t.Fatalf("probe path = %q", path)
	}
}

func TestHermesUnparseableURLIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "hermes"))
	if New(NewHermes("not a url", nil), Options{Path: dir}).Available() {
		t.Fatal("a URL with no host cannot be probed, so it is not available")
	}
}

func TestHermesDefaultOllamaURL(t *testing.T) {
	if got := NewHermes("", nil).OllamaURL; got != DefaultOllamaURL {
		t.Fatalf("OllamaURL = %q", got)
	}
}

// -- Config parsing -----------------------------------------------------------

func withConfig(t *testing.T, body string) Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return LoadConfig(determinism.Env{}, path)
}

func TestConfigReadsDefaultProviderAndModel(t *testing.T) {
	conf := withConfig(t, "llm_provider = hermes\nllm_model = gemma4:e4b\n")
	if conf.Provider != "hermes" || conf.Model != "gemma4:e4b" {
		t.Fatalf("conf = %+v", conf)
	}
}

func TestConfigReadsProviderModelListsAndSettings(t *testing.T) {
	conf := withConfig(t, `hermes_models = gemma4:e4b, gemma4:12b-mlx , qwen3:4b
hermes_command = /opt/hermes
hermes_provider = my-ollama
ollama_url = http://127.0.0.1:9999
cursor-cli_models = composer-2.5-fast, cursor-grok-4.5-low-fast
cursor-cli_command = /opt/agent
`)

	hermes := conf.ProviderSettings("hermes")
	if !reflect.DeepEqual(hermes.Models, []string{"gemma4:e4b", "gemma4:12b-mlx", "qwen3:4b"}) {
		t.Fatalf("models = %v", hermes.Models)
	}
	if hermes.Command != "/opt/hermes" {
		t.Fatalf("command = %q", hermes.Command)
	}
	if hermes.InferenceProvider == nil || *hermes.InferenceProvider != "my-ollama" {
		t.Fatalf("inference provider = %v", hermes.InferenceProvider)
	}
	if hermes.OllamaURL != "http://127.0.0.1:9999" {
		t.Fatalf("ollama url = %q", hermes.OllamaURL)
	}

	cursor := conf.ProviderSettings("cursor-cli")
	if !reflect.DeepEqual(cursor.Models, []string{"composer-2.5-fast", "cursor-grok-4.5-low-fast"}) {
		t.Fatalf("models = %v", cursor.Models)
	}
	if cursor.Command != "/opt/agent" {
		t.Fatalf("command = %q", cursor.Command)
	}
}

func TestConfigIgnoresUnknownKeysAndMissingFile(t *testing.T) {
	conf := withConfig(t, "nonsense = 1\ndir = /whatever\n")
	if conf.Provider != "" || conf.Model != "" {
		t.Fatalf("conf = %+v", conf)
	}
	if missing := LoadConfig(determinism.Env{}, "/no/such/file"); missing.Provider != "" {
		t.Fatalf("a missing file must be an empty config, not an error: %+v", missing)
	}
}

func TestConfigSkipsCommentsBlankLinesAndEmptyValues(t *testing.T) {
	conf := withConfig(t, "# llm_provider = commented-out\n\n  \nllm_model =\nllm_provider = hermes\n")
	if conf.Provider != "hermes" {
		t.Fatalf("provider = %q", conf.Provider)
	}
	if conf.Model != "" {
		t.Fatalf("an empty value is no value: %q", conf.Model)
	}
}

// The value is everything after the FIRST `=`, so a model name containing one
// survives.
func TestConfigValueKeepsLaterEqualsSigns(t *testing.T) {
	conf := withConfig(t, "llm_model = a=b\n")
	if conf.Model != "a=b" {
		t.Fatalf("model = %q", conf.Model)
	}
}

// A file with no trailing newline still yields its last line.
func TestConfigReadsAFinalLineWithoutANewline(t *testing.T) {
	if conf := withConfig(t, "llm_provider = hermes"); conf.Provider != "hermes" {
		t.Fatalf("provider = %q", conf.Provider)
	}
}

// The LLM parser does not strip inline comments, unlike Tasks::Config's. That is
// the oracle's behavior, and it is asserted rather than left to chance so a
// later "tidy-up" cannot silently change what a config file means.
func TestConfigDoesNotStripInlineComments(t *testing.T) {
	if conf := withConfig(t, "llm_model = sonnet # fast\n"); conf.Model != "sonnet # fast" {
		t.Fatalf("model = %q", conf.Model)
	}
}

// Resolution goes through internal/config when no path is given, so the LLM
// settings can never read a different file than the task paths did.
func TestConfigResolvesTheSameFileTheTaskPathsUse(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "tasks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte("llm_provider = hermes\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	conf := LoadConfig(determinism.Env{"XDG_CONFIG_HOME": home}, "")
	if conf.Provider != "hermes" {
		t.Fatalf("provider = %q", conf.Provider)
	}
}

func TestConfigProviderSettingsForAnUnknownProviderIsEmpty(t *testing.T) {
	conf := withConfig(t, "llm_provider = hermes\n")
	if settings := conf.ProviderSettings("nope"); settings.Models != nil || settings.Command != "" {
		t.Fatalf("settings = %+v", settings)
	}
}

// -- Entry labels -------------------------------------------------------------

func TestEntryUILabelsAreConciseWithoutChangingCanonicalIdentity(t *testing.T) {
	cursor := Entry{"cursor-cli", "cursor-grok-4.5-low-fast"}
	if cursor.String() != "cursor-cli:cursor-grok-4.5-low-fast" {
		t.Fatalf("String() = %q", cursor.String())
	}
	for entry, want := range map[Entry]string{
		{"cursor-cli", "cursor-grok-4.5-low-fast"}: "cursor:grok",
		{"claude-cli", "sonnet"}:                   "claude:sonnet",
		{"hermes", "qwen3.6:35b-a3b"}:              "hermes:qwen",
		{"cursor-cli", "composer-2.5-fast"}:        "cursor:composer",
		{"hermes", "gemma4:e4b"}:                   "hermes:gemma",
	} {
		if got := entry.UILabel(); got != want {
			t.Fatalf("%v.UILabel() = %q, want %q", entry, got, want)
		}
	}
}

func TestEntryUILabelPreservesUnknownProviderAndModel(t *testing.T) {
	entry := Entry{"future-cli", "future-model-v2"}
	if got := entry.UILabel(); got != "future-cli:future-model-v2" {
		t.Fatalf("UILabel() = %q", got)
	}
}

// A short label for one provider's model must not leak onto another's: the map
// is keyed on the PAIR.
func TestModelLabelsAreKeyedOnTheProviderToo(t *testing.T) {
	entry := Entry{"claude-cli", "gemma4:e4b"}
	if got := entry.UILabel(); got != "claude:gemma4:e4b" {
		t.Fatalf("UILabel() = %q", got)
	}
}

// -- Registry + resolution ----------------------------------------------------

func TestRegistryDefaults(t *testing.T) {
	registry := BuildRegistry(emptyConfig())
	if !reflect.DeepEqual(registry.Keys(), []string{"claude-cli", "hermes", "cursor-cli"}) {
		t.Fatalf("keys = %v", registry.Keys())
	}
	claude, _ := registry.Get("claude-cli")
	if !reflect.DeepEqual(claude.Models, []string{"sonnet", "opus", "haiku"}) {
		t.Fatalf("claude models = %v", claude.Models)
	}
	cursor, _ := registry.Get("cursor-cli")
	if cursor.Transport != "cli" {
		t.Fatalf("transport = %q", cursor.Transport)
	}
	if !reflect.DeepEqual(cursor.Models, []string{"composer-2.5-fast"}) {
		t.Fatalf("cursor models = %v", cursor.Models)
	}
	if _, ok := registry.Get("nope"); ok {
		t.Fatal("an unregistered provider must not resolve")
	}
}

// Registry order is contract: the first provider is the overall default, the
// switcher cycles in it, and the unknown-provider refusal lists it. A Go map
// would permute all three between runs, so the order is asserted repeatedly.
func TestRegistryOrderIsStableAcrossBuilds(t *testing.T) {
	for range 20 {
		if !reflect.DeepEqual(BuildRegistry(emptyConfig()).Keys(),
			[]string{"claude-cli", "hermes", "cursor-cli"}) {
			t.Fatal("registry order is not stable")
		}
	}
}

func TestRegistryKeysAreACopy(t *testing.T) {
	registry := BuildRegistry(emptyConfig())
	keys := registry.Keys()
	keys[0] = "mutated"
	if registry.Keys()[0] != "claude-cli" {
		t.Fatal("Keys() handed out its own backing array")
	}
}

func TestEntriesPutDefaultFirstAndDedupe(t *testing.T) {
	entries := Entries(emptyConfig())
	if entries[0].String() != "claude-cli:sonnet" {
		t.Fatalf("first = %q", entries[0].String())
	}
	seen := map[string]bool{}
	var labels []string
	for _, entry := range entries {
		if seen[entry.String()] {
			t.Fatalf("duplicate entry %q in %v", entry.String(), labels)
		}
		seen[entry.String()] = true
		labels = append(labels, entry.String())
	}
	// qwen3.6:35b-a3b is the default Hermes model (best local model per the
	// eval); gemma4:e4b remains available as a lighter fallback.
	for _, want := range []string{"hermes:qwen3.6:35b-a3b", "hermes:gemma4:e4b"} {
		if !seen[want] {
			t.Fatalf("%q is missing from %v", want, labels)
		}
	}
}

func TestHermesDefaultModelIsTheEvaluatedBest(t *testing.T) {
	entry, err := DefaultEntry("hermes", "", emptyConfig())
	if err != nil {
		t.Fatalf("DefaultEntry: %v", err)
	}
	if entry.Model != "qwen3.6:35b-a3b" {
		t.Fatalf("model = %q", entry.Model)
	}
}

func TestConfigMovesDefaultEntryToFront(t *testing.T) {
	conf := Config{Provider: "hermes", Model: "gemma4:e4b", Providers: map[string]Settings{}}
	if got := Entries(conf)[0].String(); got != "hermes:gemma4:e4b" {
		t.Fatalf("first = %q", got)
	}
}

func TestConfigModelListOverrideFlowsIntoEntries(t *testing.T) {
	conf := Config{Providers: map[string]Settings{"hermes": {Models: []string{"qwen3:4b"}}}}
	var hermes []string
	for _, entry := range Entries(conf) {
		if strings.HasPrefix(entry.String(), "hermes:") {
			hermes = append(hermes, entry.String())
		}
	}
	if !reflect.DeepEqual(hermes, []string{"hermes:qwen3:4b"}) {
		t.Fatalf("hermes entries = %v", hermes)
	}
}

func TestCursorConfigModelListOverrideFlowsIntoEntries(t *testing.T) {
	conf := Config{
		Provider: "cursor-cli", Model: "cursor-grok-4.5-low-fast",
		Providers: map[string]Settings{"cursor-cli": {Models: []string{"composer-2.5-fast"}}},
	}
	entries := Entries(conf)
	if entries[0].String() != "cursor-cli:cursor-grok-4.5-low-fast" {
		t.Fatalf("first = %q", entries[0].String())
	}
	found := false
	for _, entry := range entries {
		if entry.String() == "cursor-cli:composer-2.5-fast" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the configured list is missing from %v", entries)
	}
}

// An EMPTY configured model list falls back to the built-ins rather than leaving
// a provider with nothing to run.
func TestEmptyConfiguredModelListFallsBackToTheBuiltIns(t *testing.T) {
	conf := Config{Providers: map[string]Settings{"hermes": {Models: []string{}}}}
	spec, _ := BuildRegistry(conf).Get("hermes")
	if !reflect.DeepEqual(spec.Models, []string{"qwen3.6:35b-a3b", "gemma4:e4b"}) {
		t.Fatalf("models = %v", spec.Models)
	}
}

func TestDefaultEntryPrecedenceExplicitOverConfig(t *testing.T) {
	conf := Config{Provider: "claude-cli", Model: "haiku", Providers: map[string]Settings{}}

	// An explicit provider wins, and hermes resolves to its OWN default model
	// rather than to a claude tier left in config.
	entry, err := DefaultEntry("hermes", "", conf)
	if err != nil || entry.String() != "hermes:qwen3.6:35b-a3b" {
		t.Fatalf("entry = %q, err = %v", entry.String(), err)
	}
	// A model not in the list is still honoured — a user can run any model their
	// harness supports without editing config.
	entry, err = DefaultEntry("", "sonnet-5", conf)
	if err != nil || entry.String() != "claude-cli:sonnet-5" {
		t.Fatalf("entry = %q, err = %v", entry.String(), err)
	}
	// With neither explicit, config supplies both halves.
	entry, err = DefaultEntry("", "", conf)
	if err != nil || entry.String() != "claude-cli:haiku" {
		t.Fatalf("entry = %q, err = %v", entry.String(), err)
	}
}

// Both explicit at once, and a blank flag value is no flag at all.
func TestDefaultEntryHonorsBothExplicitFlagsAndIgnoresBlankOnes(t *testing.T) {
	conf := Config{Provider: "claude-cli", Model: "haiku", Providers: map[string]Settings{}}
	entry, err := DefaultEntry("cursor-cli", "composer-2.5-fast", conf)
	if err != nil || entry.String() != "cursor-cli:composer-2.5-fast" {
		t.Fatalf("entry = %q, err = %v", entry.String(), err)
	}
	entry, err = DefaultEntry("   ", "  ", conf)
	if err != nil || entry.String() != "claude-cli:haiku" {
		t.Fatalf("a blank flag must not override config: %q, %v", entry.String(), err)
	}
}

func TestDefaultEntryFallsBackToFirstProviderAndModel(t *testing.T) {
	entry, err := DefaultEntry("", "", emptyConfig())
	if err != nil || entry.String() != "claude-cli:sonnet" {
		t.Fatalf("entry = %q, err = %v", entry.String(), err)
	}
}

func TestDefaultEntryRefusesAnExplicitUnknownProvider(t *testing.T) {
	_, err := DefaultEntry("hremes", "", emptyConfig()) // typo
	if err == nil {
		t.Fatal("an explicit unknown provider is a user error, not a silent fallback")
	}
	if !strings.Contains(err.Error(), "unknown LLM provider") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), `"hremes"`) {
		t.Fatalf("the refusal must quote what was typed: %v", err)
	}
	if !strings.Contains(err.Error(), "claude-cli, hermes, cursor-cli") {
		t.Fatalf("the refusal must list what is known, in registry order: %v", err)
	}
}

// A stale or typo'd CONFIG provider must not brick the tool — fall back quietly.
// The asymmetry is deliberate: a flag is this invocation's mistake, a config file
// is a mistake the user may not be able to see from here.
func TestDefaultEntryToleratesUnknownConfigProvider(t *testing.T) {
	conf := Config{Provider: "gone", Providers: map[string]Settings{}}
	entry, err := DefaultEntry("", "", conf)
	if err != nil || entry.String() != "claude-cli:sonnet" {
		t.Fatalf("entry = %q, err = %v", entry.String(), err)
	}
}

// A config model paired with an unknown config provider still applies, because
// the pair falls back together to the first provider.
func TestConfigModelSurvivesAnUnknownConfigProvider(t *testing.T) {
	conf := Config{Provider: "gone", Model: "opus", Providers: map[string]Settings{}}
	entry, _ := DefaultEntry("", "", conf)
	if entry.String() != "claude-cli:opus" {
		t.Fatalf("entry = %q", entry.String())
	}
}

// -- Build --------------------------------------------------------------------

func TestBuildReturnsConfiguredAdapterWithSettings(t *testing.T) {
	provider := "x"
	conf := Config{Providers: map[string]Settings{
		"hermes": {Command: "/opt/hermes", InferenceProvider: &provider},
	}}
	agent, err := Build(Entry{"hermes", "gemma4:e4b"}, BuildOptions{Root: "/tmp"}, conf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	argv := agent.Command("hi", "m", true)
	if argv[0] != "/opt/hermes" {
		t.Fatalf("argv[0] = %q", argv[0])
	}
	if got := after(t, argv, "--provider"); got != "x" {
		t.Fatalf("--provider = %q", got)
	}
}

func TestBuildReturnsConfiguredCursorAdapter(t *testing.T) {
	conf := Config{Providers: map[string]Settings{"cursor-cli": {Command: "/opt/agent"}}}
	agent, err := Build(Entry{"cursor-cli", "composer-2.5-fast"}, BuildOptions{Root: "/tmp"}, conf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got := agent.Command("hi", "composer-2.5-fast", true)[0]; got != "/opt/agent" {
		t.Fatalf("argv[0] = %q", got)
	}
}

func TestBuildRefusesUnknownProvider(t *testing.T) {
	_, err := Build(Entry{"nope", "m"}, BuildOptions{Root: "/tmp"}, emptyConfig())
	if err == nil || !strings.Contains(err.Error(), "unknown LLM provider") {
		t.Fatalf("err = %v", err)
	}
}

func TestBuildCarriesRootSystemAndPathIntoTheAgent(t *testing.T) {
	agent, err := Build(Entry{"claude-cli", "sonnet"},
		BuildOptions{Root: "/tmp/data", System: "SYS", Path: "/nowhere"}, emptyConfig())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if agent.System() != "SYS" {
		t.Fatalf("System() = %q", agent.System())
	}
	if agent.root != "/tmp/data" {
		t.Fatalf("root = %q", agent.root)
	}
	if agent.Available() {
		t.Fatal("an empty PATH directory must find nothing")
	}
}

// The Hermes settings survive the whole config → registry → adapter path.
func TestBuildCarriesTheOllamaURLThrough(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "hermes"))
	conf := Config{Providers: map[string]Settings{"hermes": {OllamaURL: server.URL}}}
	agent, err := Build(Entry{"hermes", "gemma4:e4b"}, BuildOptions{Root: "/tmp", Path: dir}, conf)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !agent.Available() {
		t.Fatal("the configured endpoint did not reach the probe")
	}
}

func TestPathFromReadsTheEnvironment(t *testing.T) {
	if got := PathFrom(determinism.Env{"PATH": "/a:/b"}); got != "/a:/b" {
		t.Fatalf("PathFrom = %q", got)
	}
	if got := PathFrom(determinism.Env{}); got != "" {
		t.Fatalf("an unset PATH is empty, which finds nothing: %q", got)
	}
}
