// Package config resolves where the task files live and which settings the
// process runs under. It is the Go counterpart of lib/tasks/config.rb, ported
// as far as the read-only surfaces and the probe need it.
//
// Precedence, highest first, exactly as the Ruby module states it:
//
//  1. TASKS_FILE / TASKS_ARCHIVE / TASKS_MEMORY (per-file overrides)
//  2. TASKS_DIR (a directory holding tasks.jsonl + archive.jsonl)
//  3. the config file at $XDG_CONFIG_HOME/tasks/config
//  4. default_dir
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/determinism"
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/timezones"
)

// The built-in defaults, spelled where Ruby spells them.
const (
	DefaultUrgentDays = 3
	DefaultMaxDepth   = 4
	DefaultDateOrder  = "mdy"
	DefaultTheme      = "default"
	DefaultTimeFormat = 12
)

// PathKeys are the config-file keys whose value is expanded as a path.
var PathKeys = []string{"dir", "file", "archive", "memory"}

var (
	linkName     = regexp.MustCompile(`\A[a-z][a-z0-9_-]*\z`)
	hostSelector = regexp.MustCompile(`(?i)\A[a-z0-9][a-z0-9._-]*\z`)
	factName     = regexp.MustCompile(`\A[a-z][a-z0-9_-]*\z`)
	orderValues  = map[string]bool{"mdy": true, "dmy": true}
)

// Paths is the resolved answer: every path and setting a surface needs, plus
// `Sources`, which records WHICH input decided each one. The probe reads
// Sources["theme"] rather than re-deriving the theme precedence, so a port
// that got the precedence wrong reports it rather than hides it.
type Paths struct {
	Configured              bool
	Org                     string
	Archive                 string
	Memory                  string
	UrgentDays              int
	MaxDepth                int
	Theme                   string
	Colors                  map[string]string
	Mouse                   bool
	Timezone                string
	TimeFormat              int
	TimezoneFallbackWarning bool
	DateOrder               string
	// DelegationModes is the resolved delegation mode vocabulary, in the order
	// a human should read it. It is a plain []string rather than a
	// record.ModeVocabulary because config resolves SETTINGS; Modes turns it
	// into the value the store and the checker carry.
	DelegationModes   []string
	Links             map[string]string
	LinkSystems       map[string]string
	Hostname          string
	HostContext       string
	HostContextSource string
	HostContexts      map[string]string
	PromptFacts       map[string]bool
	Sources           map[string]string
	ConfigFile        string
	// Warnings are the resolution notes a surface should show the user, in the
	// order resolution produced them — today, one per time zone that was
	// configured and could not be loaded. Ruby writes these straight to stderr
	// from inside Config.resolve; a library that printed would be untestable and
	// would decide for every surface, so they are returned instead and the
	// adapter chooses where they go.
	Warnings []string
}

// Resolve answers where the task files live for this process. `hostname` may be
// nil, in which case it defaults through Determinism so every adapter honours
// the harness's host pin without each one remembering to.
func Resolve(defaultDir string, env determinism.Env, hostname func() string) Paths {
	if hostname == nil {
		hostname = determinism.Hostname(env)
	}
	file := ConfigFile(env)
	conf := parseFile(file, env)
	hasDirectory := defaultDir != "" || env.Get("TASKS_DIR") != "" || hasPath(conf.paths, "dir")
	hasOrg := env.Get("TASKS_FILE") != "" || hasPath(conf.paths, "file")
	hasArchive := env.Get("TASKS_ARCHIVE") != "" || hasPath(conf.paths, "archive")
	configured := hasDirectory || (hasOrg && hasArchive)
	if defaultDir == "" {
		defaultDir = "."
	}

	dir, dirSource := defaultDir, "default"
	if value := env.Get("TASKS_DIR"); value != "" {
		dir, dirSource = value, "TASKS_DIR env"
	} else if value, ok := conf.paths["dir"]; ok {
		dir, dirSource = value, "config file"
	}

	org, orgSource := pick("tasks.jsonl", "TASKS_FILE", dir, dirSource, conf.paths, "file", env)
	archive, archiveSource := pick("archive.jsonl", "TASKS_ARCHIVE", dir, dirSource, conf.paths, "archive", env)
	// Memory derives from the FINAL org path, not the base dir: a TASKS_FILE
	// override must select its sibling agent-memory.md even when the dir and
	// archive come from elsewhere.
	memory, memorySource := pickMemory(org, conf.paths, env)
	urgentDays, urgentSource := pickUrgentDays(conf, env)
	maxDepth, maxDepthSource := pickMaxDepth(conf, env)
	theme, themeSource := pickTheme(conf, env)
	mouse, mouseSource := pickMouse(conf, env)
	timezone, timezoneSource, timezoneWarning, warnings := pickTimezone(conf, env)
	timeFormat, timeFormatSource := pickTimeFormat(conf, env)
	dateOrder, dateOrderSource := pickDateOrder(conf, env)
	delegationModes, delegationModesSource, modeWarnings := pickDelegationModes(conf, env)
	warnings = append(warnings, modeWarnings...)
	detectedHostname, hostContext, hostContextSource := pickHostContext(conf.hostContexts, hostname)

	return Paths{
		Configured: configured,
		Org:        org, Archive: archive, Memory: memory,
		UrgentDays: urgentDays, MaxDepth: maxDepth,
		Theme: theme, Colors: conf.colors, Mouse: mouse,
		Timezone: timezone, TimeFormat: timeFormat, TimezoneFallbackWarning: timezoneWarning,
		DateOrder: dateOrder, DelegationModes: delegationModes,
		Links: conf.links, LinkSystems: conf.linkSystems,
		Hostname: detectedHostname, HostContext: hostContext,
		HostContextSource: hostContextSource, HostContexts: conf.hostContexts,
		PromptFacts: ResolvePromptFacts(conf.promptFacts),
		Warnings:    warnings,
		Sources: map[string]string{
			"org": orgSource, "archive": archiveSource, "memory": memorySource,
			"urgent_days": urgentSource, "max_depth": maxDepthSource, "theme": themeSource,
			"mouse": mouseSource, "timezone": timezoneSource, "time_format": timeFormatSource,
			"date_order": dateOrderSource, "delegation_modes": delegationModesSource,
		},
		ConfigFile: file,
	}
}

// Modes is the resolved vocabulary as the value the store and the checker
// carry. It is the ONE conversion from settings to vocabulary, so a surface
// wires the configured modes in with a single field.
func (p Paths) Modes() record.ModeVocabulary {
	if len(p.DelegationModes) == 0 {
		return record.BuiltinModes()
	}
	return record.ModeSet(p.DelegationModes)
}

// PromptFactDefaults is the registry lib/tasks/prompt_facts.rb owns: the facts
// an agent's "Current environment" block can carry, and whether each is on when
// nothing says otherwise. Rendering the block belongs to the agent surface;
// what config owns is the effective on/off map, because `tasks config` reports
// it and a `prompt.<name>` line is what changes it.
var PromptFactDefaults = map[string]bool{"datetime": true, "hostname": true}

// ResolvePromptFacts is PromptFacts.resolve: every REGISTERED fact, with the
// config file's override where there is one. A name the registry does not know
// is dropped rather than reported — parseFile keeps it for forward
// compatibility, but only a fact this binary can render may claim to be on.
func ResolvePromptFacts(overrides map[string]bool) map[string]bool {
	resolved := make(map[string]bool, len(PromptFactDefaults))
	for name, fallback := range PromptFactDefaults {
		if override, ok := overrides[name]; ok {
			resolved[name] = override
			continue
		}
		resolved[name] = fallback
	}
	return resolved
}

// ForDir pins every path to one directory, ignoring the environment and the
// config file. It is what a sandbox uses so a test can never reach the user's
// real task files — the settings are the built-in defaults, and the hostname
// provider is never called, so nothing about the host leaks into the answer.
func ForDir(dir string, env determinism.Env) Paths {
	return Paths{
		Configured: true,
		Org:        filepath.Join(dir, "tasks.jsonl"),
		Archive:    filepath.Join(dir, "archive.jsonl"),
		Memory:     filepath.Join(dir, "agent-memory.md"),
		UrgentDays: DefaultUrgentDays, MaxDepth: DefaultMaxDepth,
		Theme: DefaultTheme, Colors: map[string]string{}, Mouse: true,
		Timezone: timezones.Fallback, TimeFormat: DefaultTimeFormat,
		TimezoneFallbackWarning: false, DateOrder: DefaultDateOrder,
		DelegationModes: record.BuiltinModes().Modes(),
		Links:           map[string]string{}, LinkSystems: map[string]string{},
		HostContexts: map[string]string{},
		PromptFacts:  ResolvePromptFacts(nil),
		Warnings:     []string{},
		Sources: map[string]string{
			"org": "pinned", "archive": "pinned", "memory": "pinned",
			"urgent_days": "default", "max_depth": "default", "theme": "default",
			"mouse": "default", "timezone": "pinned", "time_format": "default",
			"date_order": "default", "delegation_modes": "default",
		},
		ConfigFile: ConfigFile(env),
	}
}

func hasPath(paths map[string]string, keys ...string) bool {
	for _, key := range keys {
		if value, ok := paths[key]; ok && value != "" {
			return true
		}
	}
	return false
}

func ConfigurationRequiredMessage(paths Paths) string {
	return fmt.Sprintf("tasks is not configured; refusing to choose a task-data directory\n"+
		"create %s containing: dir = /absolute/path/to/task-data\n"+
		"or set TASKS_DIR; per-file configuration requires both TASKS_FILE and TASKS_ARCHIVE", paths.ConfigFile)
}

// ConfigFile is $XDG_CONFIG_HOME/tasks/config, with the XDG fallback applied.
func ConfigFile(env determinism.Env) string {
	return filepath.Join(XDGBase(env, "XDG_CONFIG_HOME", ".config"), "tasks", "config")
}

// XDGBase resolves an XDG base directory, falling back to ~/<default...> when
// the variable is unset or empty and expanding either form to an absolute
// path — a relative XDG value would otherwise resolve against each process's
// cwd, so two invocations from different directories would disagree about
// where state lives.
func XDGBase(env determinism.Env, key string, defaults ...string) string {
	base := env.Get(key)
	if base == "" {
		base = filepath.Join(append([]string{home(env)}, defaults...)...)
	}
	return ExpandPath(base, env)
}

// ExpandPath is Ruby's File.expand_path: `~` expansion, then resolution
// against the process's working directory, then lexical cleaning. It never
// touches the filesystem, so a missing path expands exactly like a present one.
func ExpandPath(path string, env determinism.Env) string {
	if path == "~" {
		path = home(env)
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home(env), path[2:])
	}
	if !filepath.IsAbs(path) {
		cwd, err := os.Getwd()
		if err == nil {
			path = filepath.Join(cwd, path)
		}
	}
	return filepath.Clean(path)
}

func home(env determinism.Env) string {
	if value := env.Get("HOME"); value != "" {
		return value
	}
	dir, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return dir
}

func pick(basename, envKey, dir, dirSource string, paths map[string]string, confKey string, env determinism.Env) (string, string) {
	if value := env.Get(envKey); value != "" {
		return ExpandPath(value, env), envKey + " env"
	}
	if value, ok := paths[confKey]; ok {
		return value, "config file"
	}
	return ExpandPath(filepath.Join(dir, basename), env), dirSource
}

func pickMemory(org string, paths map[string]string, env determinism.Env) (string, string) {
	if value := env.Get("TASKS_MEMORY"); value != "" {
		return ExpandPath(value, env), "TASKS_MEMORY env"
	}
	if value, ok := paths["memory"]; ok {
		return value, "config file"
	}
	return ExpandPath(filepath.Join(filepath.Dir(org), "agent-memory.md"), env), "beside tasks.jsonl"
}

// pickTheme: env beats config file; NO_COLOR (when nothing explicit is set)
// selects the attribute-only theme. The probe reads the source this returns
// rather than re-deriving the order.
func pickTheme(conf parsedConfig, env determinism.Env) (string, string) {
	if value := env.Get("TASKS_THEME"); value != "" {
		return value, "TASKS_THEME env"
	}
	if value, ok := conf.strings["theme"]; ok {
		return value, "config file"
	}
	if value := env.Get("NO_COLOR"); value != "" {
		return "mono", "NO_COLOR env"
	}
	return DefaultTheme, "default"
}

func pickMouse(conf parsedConfig, env determinism.Env) (bool, string) {
	if value := env.Get("TASKS_MOUSE"); value != "" {
		if toggle, ok := parseOnOff(value); ok {
			return toggle, "TASKS_MOUSE env"
		}
	}
	if value, ok := conf.bools["mouse"]; ok {
		return value, "config file"
	}
	return true, "default"
}

func parseOnOff(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "yes", "1":
		return true, true
	case "off", "false", "no", "0":
		return false, true
	}
	return false, false
}

// pickTimezone falls through independently on an invalid zone: a typo'd
// TASKS_TIMEZONE must not silently skip past a valid config-file zone. Each
// source that is set and unusable earns a warning, because a zone that was
// configured and ignored changes every rendered time — silence would leave the
// user reading yesterday's agenda in the wrong offset with nothing to go on.
func pickTimezone(conf parsedConfig, env determinism.Env) (string, string, bool, []string) {
	candidates := []struct{ value, source string }{
		{env.Get("TASKS_TIMEZONE"), "TASKS_TIMEZONE env"},
		{conf.strings["timezone"], "config file"},
	}
	warnings := []string{}
	for _, candidate := range candidates {
		if candidate.value == "" {
			continue
		}
		if id, err := timezones.Get(candidate.value); err == nil {
			return id, candidate.source, false, warnings
		}
		warnings = append(warnings,
			fmt.Sprintf("tasks: ignoring invalid time zone %q from %s", candidate.value, candidate.source))
	}
	zone, source, fallback := timezones.Detect(env.Get(determinism.NameTZ), "/etc/localtime")
	return zone, source, fallback, warnings
}

func pickTimeFormat(conf parsedConfig, env determinism.Env) (int, string) {
	switch env.Get("TASKS_TIME_FORMAT") {
	case "12":
		return 12, "TASKS_TIME_FORMAT env"
	case "24":
		return 24, "TASKS_TIME_FORMAT env"
	}
	if value, ok := conf.numbers["time_format"]; ok {
		return value, "config file"
	}
	return DefaultTimeFormat, "default"
}

func pickDateOrder(conf parsedConfig, env determinism.Env) (string, string) {
	raw := strings.ToLower(env.Get("TASKS_DATE_ORDER"))
	if orderValues[raw] {
		return raw, "TASKS_DATE_ORDER env"
	}
	if value, ok := conf.strings["date_order"]; ok {
		return value, "config file"
	}
	return DefaultDateOrder, "default"
}

// pickDelegationModes resolves the delegation mode vocabulary. The list is
// validated HERE rather than at parse time so a malformed list can report what
// was wrong with it and fall through to the next source, exactly as a typo'd
// time zone does.
//
// Degradation is total, not partial: one bad entry drops the whole list back to
// the built-in vocabulary. Silently keeping the good half would leave the user
// running against a set they never wrote, and a mode that quietly vanished is
// how a delegation gets refused with no explanation. A warning says which list
// was ignored and why; nothing crashes, and the store stays writable, because
// the vocabulary only ever decides which modes may be WRITTEN.
func pickDelegationModes(conf parsedConfig, env determinism.Env) ([]string, string, []string) {
	candidates := []struct{ value, source string }{
		{env.Get("TASKS_DELEGATION_MODES"), "TASKS_DELEGATION_MODES env"},
		{conf.strings["delegation_modes"], "config file"},
	}
	warnings := []string{}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.value) == "" {
			continue
		}
		modes, problem := record.ParseModeList(candidate.value)
		if problem == "" {
			return modes.Modes(), candidate.source, warnings
		}
		warnings = append(warnings, fmt.Sprintf(
			"tasks: ignoring delegation_modes from %s (%s); using %s",
			candidate.source, problem, record.BuiltinModes().Quoted()))
	}
	return record.BuiltinModes().Modes(), "default", warnings
}

func pickUrgentDays(conf parsedConfig, env determinism.Env) (int, string) {
	if value := env.Get("TASKS_URGENT_DAYS"); value != "" {
		if days, ok := parseDays(value); ok {
			return days, "TASKS_URGENT_DAYS env"
		}
	}
	if value, ok := conf.numbers["urgent_days"]; ok {
		return value, "config file"
	}
	return DefaultUrgentDays, "default"
}

func pickMaxDepth(conf parsedConfig, env determinism.Env) (int, string) {
	if value := env.Get("TASKS_MAX_DEPTH"); value != "" {
		if depth, ok := parseDepth(value); ok {
			return depth, "TASKS_MAX_DEPTH env"
		}
	}
	if value, ok := conf.numbers["max_depth"]; ok {
		return value, "config file"
	}
	return DefaultMaxDepth, "default"
}

func parseInteger(value string) (int, bool) {
	number, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(value), "_", ""))
	if err != nil {
		return 0, false
	}
	return number, true
}

func parseDays(value string) (int, bool) {
	number, ok := parseInteger(value)
	return number, ok && number >= 0
}

func parseDepth(value string) (int, bool) {
	number, ok := parseInteger(value)
	return number, ok && number >= 1
}

// pickHostContext selects the context a hostname maps to, trying the full name
// then its first DNS label. The detected hostname is returned even when no
// context matches, because the probe compares it against the hostname pin.
func pickHostContext(contexts map[string]string, hostname func() string) (string, string, string) {
	detected := strings.TrimSpace(hostname())
	if detected == "" {
		return "", "", ""
	}
	full := strings.ToLower(detected)
	short, _, _ := strings.Cut(full, ".")
	for _, selector := range dedupe(full, short) {
		if context, ok := contexts[selector]; ok {
			return detected, context, "host_context." + selector
		}
	}
	return detected, "", ""
}

func dedupe(values ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	return out
}

// parsedConfig keeps the config file's typed buckets apart the way the Ruby
// hash's string-vs-symbol keys do, so a `link.x` line can never collide with a
// flat key.
type parsedConfig struct {
	paths        map[string]string
	strings      map[string]string
	numbers      map[string]int
	bools        map[string]bool
	colors       map[string]string
	links        map[string]string
	linkSystems  map[string]string
	promptFacts  map[string]bool
	hostContexts map[string]string
}

func newParsedConfig() parsedConfig {
	return parsedConfig{
		paths: map[string]string{}, strings: map[string]string{}, numbers: map[string]int{},
		bools: map[string]bool{}, colors: map[string]string{}, links: map[string]string{},
		linkSystems: map[string]string{}, promptFacts: map[string]bool{},
		hostContexts: map[string]string{},
	}
}

// parseFile reads `key = value` lines. `#` comments and blanks are ignored and
// unknown keys are dropped, so a newer binary's setting cannot break this one.
// parseFile takes the caller's env rather than reading the process
// environment. A `~` or relative path in the config file expands through the
// same HOME the rest of resolution uses, which is what keeps a harness pin —
// or a test sandbox — from being silently overridden by the real environment.
// Ruby expands these with File.expand_path against ENV, so reading OSEnv here
// made Go disagree with the oracle whenever the two differed.
func parseFile(path string, env determinism.Env) parsedConfig {
	conf := newParsedConfig()
	content, err := os.ReadFile(path)
	if err != nil {
		return conf
	}
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			// Ruby's partition yields an empty value, which the next guard drops.
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		// An inline comment is whitespace + "#". Requiring the whitespace keeps
		// a "#" INSIDE a value intact (a URL anchor has no space before its #).
		// color.* specs are exempt: a hex token legitimately follows a space.
		if !strings.HasPrefix(key, "color.") {
			value = strings.TrimSpace(inlineComment.ReplaceAllString(value, ""))
		}
		if value == "" {
			continue
		}
		conf.assign(key, value, env)
	}
	return conf
}

var inlineComment = regexp.MustCompile(`\s#.*\z`)

func (c parsedConfig) assign(key, value string, env determinism.Env) {
	for _, pathKey := range PathKeys {
		if key == pathKey {
			c.paths[key] = ExpandPath(value, env)
			return
		}
	}
	switch {
	case key == "urgent_days":
		if number, ok := parseDays(value); ok {
			c.numbers[key] = number
		}
	case key == "max_depth":
		if number, ok := parseDepth(value); ok {
			c.numbers[key] = number
		}
	case key == "theme":
		c.strings[key] = value
	case key == "mouse":
		if toggle, ok := parseOnOff(value); ok {
			c.bools[key] = toggle
		}
	case key == "timezone":
		if id, err := timezones.Get(value); err == nil {
			c.strings[key] = id
		}
	case key == "time_format":
		if value == "12" || value == "24" {
			number, _ := strconv.Atoi(value)
			c.numbers[key] = number
		}
	case key == "delegation_modes":
		// Stored raw: pickDelegationModes owns the validation, so a malformed
		// list can WARN about itself instead of disappearing here.
		c.strings[key] = value
	case key == "date_order":
		if orderValues[strings.ToLower(value)] {
			c.strings[key] = strings.ToLower(value)
		}
	case strings.HasPrefix(key, "color.") && len(key) > 6:
		c.colors[strings.TrimPrefix(key, "color.")] = value
	case strings.HasPrefix(key, "link.") && linkName.MatchString(strings.TrimPrefix(key, "link.")):
		c.links[strings.TrimPrefix(key, "link.")] = value
	case strings.HasPrefix(key, "system.") && linkName.MatchString(strings.TrimPrefix(key, "system.")):
		c.linkSystems[strings.TrimPrefix(key, "system.")] = value
	case strings.HasPrefix(key, "prompt.") && factName.MatchString(strings.TrimPrefix(key, "prompt.")):
		if toggle, ok := parsePromptToggle(value); ok {
			c.promptFacts[strings.TrimPrefix(key, "prompt.")] = toggle
		}
	case strings.HasPrefix(key, "host_context.") && hostSelector.MatchString(strings.TrimPrefix(key, "host_context.")):
		if context, ok := normalizeContext(value); ok {
			c.hostContexts[strings.ToLower(strings.TrimPrefix(key, "host_context."))] = context
		}
	}
}

// parsePromptToggle is PromptFacts.parse_toggle, whose vocabulary is a strict
// subset of parseOnOff's: "yes"/"no" are not prompt-fact spellings.
func parsePromptToggle(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "1":
		return true, true
	case "off", "false", "0":
		return false, true
	}
	return false, false
}

func normalizeContext(value string) (string, bool) {
	context := strings.TrimSpace(value)
	if !strings.HasPrefix(context, "@") {
		context = "@" + context
	}
	if context == "@" || strings.ContainsAny(context, " \t\n\v\f\r") {
		return "", false
	}
	return context, true
}
