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
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"tasks-go/internal/determinism"
	"tasks-go/internal/timezones"
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
	Links                   map[string]string
	LinkSystems             map[string]string
	Hostname                string
	HostContext             string
	HostContextSource       string
	HostContexts            map[string]string
	PromptFacts             map[string]bool
	Sources                 map[string]string
	ConfigFile              string
}

// Resolve answers where the task files live for this process. `hostname` may be
// nil, in which case it defaults through Determinism so every adapter honours
// the harness's host pin without each one remembering to.
func Resolve(defaultDir string, env determinism.Env, hostname func() string) Paths {
	if hostname == nil {
		hostname = determinism.Hostname(env)
	}
	file := ConfigFile(env)
	conf := parseFile(file)

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
	timezone, timezoneSource, timezoneWarning := pickTimezone(conf, env)
	timeFormat, timeFormatSource := pickTimeFormat(conf, env)
	dateOrder, dateOrderSource := pickDateOrder(conf, env)
	detectedHostname, hostContext, hostContextSource := pickHostContext(conf.hostContexts, hostname)

	return Paths{
		Org: org, Archive: archive, Memory: memory,
		UrgentDays: urgentDays, MaxDepth: maxDepth,
		Theme: theme, Colors: conf.colors, Mouse: mouse,
		Timezone: timezone, TimeFormat: timeFormat, TimezoneFallbackWarning: timezoneWarning,
		DateOrder: dateOrder,
		Links:     conf.links, LinkSystems: conf.linkSystems,
		Hostname: detectedHostname, HostContext: hostContext,
		HostContextSource: hostContextSource, HostContexts: conf.hostContexts,
		PromptFacts: conf.promptFacts,
		Sources: map[string]string{
			"org": orgSource, "archive": archiveSource, "memory": memorySource,
			"urgent_days": urgentSource, "max_depth": maxDepthSource, "theme": themeSource,
			"mouse": mouseSource, "timezone": timezoneSource, "time_format": timeFormatSource,
			"date_order": dateOrderSource,
		},
		ConfigFile: file,
	}
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
// TASKS_TIMEZONE must not silently skip past a valid config-file zone.
func pickTimezone(conf parsedConfig, env determinism.Env) (string, string, bool) {
	candidates := []struct{ value, source string }{
		{env.Get("TASKS_TIMEZONE"), "TASKS_TIMEZONE env"},
		{conf.strings["timezone"], "config file"},
	}
	for _, candidate := range candidates {
		if candidate.value == "" {
			continue
		}
		if id, err := timezones.Get(candidate.value); err == nil {
			return id, candidate.source, false
		}
	}
	return timezones.Detect(env.Get(determinism.NameTZ), "/etc/localtime")
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
func parseFile(path string) parsedConfig {
	conf := newParsedConfig()
	content, err := os.ReadFile(path)
	if err != nil {
		return conf
	}
	env := determinism.OSEnv()
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
