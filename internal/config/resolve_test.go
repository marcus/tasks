package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/marcus/tasks/internal/determinism"
)

// The rest of test/test_config.rb: urgent days, max depth, timezone resolution
// and its warning, date order, theme and colors, mouse, time format, prompt
// facts, and host contexts. The path-precedence ladder lives in config_test.go.
//
// Every test passes an explicit environment; none may read the real one, which
// is the rule the Ruby file states about itself. TZ is pinned so the host's own
// zone can never decide an assertion.

// resolve is the Ruby helper of the same name: a sandboxed HOME and XDG dir, a
// pinned TZ, a fixed hostname, and an optional config file.
func resolve(t *testing.T, body string, overrides map[string]string) Paths {
	t.Helper()
	merged := map[string]string{"TZ": "Etc/UTC"}
	for key, value := range overrides {
		merged[key] = value
	}
	env, _ := testEnv(t, merged)
	if body != "" {
		writeConfig(t, env, body)
	}
	return Resolve("/repo", env, func() string { return "test-host.local" })
}

// test_defaults_to_default_dir — the whole sources map, because an attribution
// that drifts is how a surface starts explaining the wrong reason for a path.
func TestDefaultsReportEverySource(t *testing.T) {
	paths := resolve(t, "", nil)
	want := map[string]string{
		"org": "default", "archive": "default", "memory": "beside tasks.jsonl",
		"urgent_days": "default", "max_depth": "default", "theme": "default",
		"mouse": "default", "timezone": "TZ env", "time_format": "default",
		"date_order": "default",
	}
	if !reflect.DeepEqual(paths.Sources, want) {
		t.Fatalf("sources = %#v, want %#v", paths.Sources, want)
	}
	if paths.Memory != "/repo/agent-memory.md" {
		t.Fatalf("memory = %q", paths.Memory)
	}
}

// -- urgent_days (the quadrants urgency window) ------------------------------

func TestUrgentDaysPrecedenceAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string
		overrides map[string]string
		want      int
		source    string
	}{
		{"defaults to three", "", nil, 3, "default"},
		{"from config file", "urgent_days = 7\n", nil, 7, "config file"},
		{"env beats config file", "urgent_days = 7\n", map[string]string{"TASKS_URGENT_DAYS": "14"}, 14, "TASKS_URGENT_DAYS env"},
		{"invalid config value falls back to default", "urgent_days = soon\n", nil, 3, "default"},
		// A negative or unparseable env value is ignored, not fatal.
		{"invalid env falls back to config", "urgent_days = 9\n", map[string]string{"TASKS_URGENT_DAYS": "-2"}, 9, "config file"},
		{"empty env is ignored", "urgent_days = 9\n", map[string]string{"TASKS_URGENT_DAYS": ""}, 9, "config file"},
		{"zero is a legal window", "urgent_days = 0\n", nil, 0, "config file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := resolve(t, tc.config, tc.overrides)
			if paths.UrgentDays != tc.want || paths.Sources["urgent_days"] != tc.source {
				t.Fatalf("urgent days = %d (%q), want %d (%q)", paths.UrgentDays, paths.Sources["urgent_days"], tc.want, tc.source)
			}
		})
	}
}

// -- max_depth (the task-nesting depth cap) ----------------------------------

func TestMaxDepthPrecedenceAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string
		overrides map[string]string
		want      int
		source    string
	}{
		{"defaults to four", "", nil, 4, "default"},
		{"from config file", "max_depth = 6\n", nil, 6, "config file"},
		{"env beats config file", "max_depth = 6\n", map[string]string{"TASKS_MAX_DEPTH": "2"}, 2, "TASKS_MAX_DEPTH env"},
		// Zero would forbid every task, so it is a typo rather than a setting.
		{"zero falls back to default", "max_depth = 0\n", nil, 4, "default"},
		{"negative falls back to default", "max_depth = -1\n", nil, 4, "default"},
		{"non numeric falls back to default", "max_depth = deep\n", nil, 4, "default"},
		{"invalid env falls back to config", "max_depth = 5\n", map[string]string{"TASKS_MAX_DEPTH": "0"}, 5, "config file"},
		{"empty env is ignored", "max_depth = 5\n", map[string]string{"TASKS_MAX_DEPTH": ""}, 5, "config file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := resolve(t, tc.config, tc.overrides)
			if paths.MaxDepth != tc.want || paths.Sources["max_depth"] != tc.source {
				t.Fatalf("max depth = %d (%q), want %d (%q)", paths.MaxDepth, paths.Sources["max_depth"], tc.want, tc.source)
			}
		})
	}
}

// -- temporal settings -------------------------------------------------------

// test_timezone_resolution_precedence_and_time_format
func TestTimezoneResolutionPrecedenceAndTimeFormat(t *testing.T) {
	configured := resolve(t, "timezone = Europe/London\ntime_format = 24\n", map[string]string{"TZ": "Asia/Tokyo"})
	if configured.Timezone != "Europe/London" || configured.Sources["timezone"] != "config file" {
		t.Fatalf("timezone = %q (%q), want the config zone to beat TZ", configured.Timezone, configured.Sources["timezone"])
	}
	if configured.TimeFormat != 24 || configured.Sources["time_format"] != "config file" {
		t.Fatalf("time format = %d (%q)", configured.TimeFormat, configured.Sources["time_format"])
	}

	overridden := resolve(t, "timezone = Europe/London\ntime_format = 24\n", map[string]string{
		"TASKS_TIMEZONE": "America/New_York", "TASKS_TIME_FORMAT": "12",
	})
	if overridden.Timezone != "America/New_York" || overridden.Sources["timezone"] != "TASKS_TIMEZONE env" {
		t.Fatalf("timezone = %q (%q)", overridden.Timezone, overridden.Sources["timezone"])
	}
	if overridden.TimeFormat != 12 || overridden.Sources["time_format"] != "TASKS_TIME_FORMAT env" {
		t.Fatalf("time format = %d (%q)", overridden.TimeFormat, overridden.Sources["time_format"])
	}
}

// test_invalid_timezone_env_falls_through_to_config_zone_with_a_warning. Each
// source falls through INDEPENDENTLY: a typo'd variable must not silently skip
// past a valid config-file zone, and it must say so — a zone that was set and
// ignored changes every rendered time.
func TestInvalidTimezoneEnvFallsThroughToTheConfigZoneWithAWarning(t *testing.T) {
	paths := resolve(t, "timezone = Europe/London\n", map[string]string{"TASKS_TIMEZONE": "Bogus/NotAZone"})
	if paths.Timezone != "Europe/London" || paths.Sources["timezone"] != "config file" {
		t.Fatalf("timezone = %q (%q), want the config zone", paths.Timezone, paths.Sources["timezone"])
	}
	want := `tasks: ignoring invalid time zone "Bogus/NotAZone" from TASKS_TIMEZONE env`
	if len(paths.Warnings) != 1 || paths.Warnings[0] != want {
		t.Fatalf("warnings = %#v, want [%q]", paths.Warnings, want)
	}
	// A usable zone warns about nothing.
	if got := resolve(t, "", map[string]string{"TASKS_TIMEZONE": "Asia/Tokyo"}); len(got.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", got.Warnings)
	}
}

// An unusable config-file zone is dropped at PARSE time, so resolution falls
// through to TZ rather than reporting a "config file" source for a zone it
// could not load.
func TestInvalidConfigTimezoneIsDroppedAtParse(t *testing.T) {
	paths := resolve(t, "timezone = Bogus/NotAZone\n", map[string]string{"TZ": "Asia/Tokyo"})
	if paths.Timezone != "Asia/Tokyo" || paths.Sources["timezone"] != "TZ env" {
		t.Fatalf("timezone = %q (%q), want the TZ value", paths.Timezone, paths.Sources["timezone"])
	}
}

// test_timezone_uses_tz_and_detector_reports_utc_fallback
func TestTimezoneUsesTZAndReportsTheUTCFallback(t *testing.T) {
	if got := resolve(t, "", map[string]string{"TZ": "Asia/Tokyo"}); got.Timezone != "Asia/Tokyo" {
		t.Fatalf("timezone = %q, want the TZ value", got.Timezone)
	}
	// An unusable TZ lands on the fallback WITH the flag set, so a surface can
	// tell "the user asked for UTC" from "we could not tell what zone this is".
	fallback := resolve(t, "", map[string]string{"TZ": "Bogus/NotAZone"})
	if fallback.Timezone != "Etc/UTC" || fallback.Sources["timezone"] != "UTC fallback" {
		t.Fatalf("timezone = %q (%q)", fallback.Timezone, fallback.Sources["timezone"])
	}
	if !fallback.TimezoneFallbackWarning {
		t.Fatal("falling back to UTC must set the warning flag")
	}
	if pinned := resolve(t, "", map[string]string{"TZ": "Etc/UTC"}); pinned.TimezoneFallbackWarning {
		t.Fatal("an explicit Etc/UTC is not a fallback")
	}
}

func TestTimeFormatVocabularyIsTwelveOrTwentyFour(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string
		overrides map[string]string
		want      int
		source    string
	}{
		{"defaults to twelve", "", nil, 12, "default"},
		{"twenty four from config", "time_format = 24\n", nil, 24, "config file"},
		{"invalid config value is dropped", "time_format = 36\n", nil, 12, "default"},
		{"invalid env falls through to config", "time_format = 24\n", map[string]string{"TASKS_TIME_FORMAT": "36"}, 24, "config file"},
		{"env wins", "time_format = 24\n", map[string]string{"TASKS_TIME_FORMAT": "12"}, 12, "TASKS_TIME_FORMAT env"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := resolve(t, tc.config, tc.overrides)
			if paths.TimeFormat != tc.want || paths.Sources["time_format"] != tc.source {
				t.Fatalf("time format = %d (%q), want %d (%q)", paths.TimeFormat, paths.Sources["time_format"], tc.want, tc.source)
			}
		})
	}
}

// -- date order (how a bare "8/1" is read) -----------------------------------

func TestDateOrderPrecedenceAndCaseFolding(t *testing.T) {
	for _, tc := range []struct {
		name      string
		config    string
		overrides map[string]string
		want      string
		source    string
	}{
		{"defaults to mdy", "", nil, "mdy", "default"},
		{"from config file", "date_order = dmy\n", nil, "dmy", "config file"},
		{"config value is case insensitive", "date_order = DMY\n", nil, "dmy", "config file"},
		{"env overrides config file", "date_order = dmy\n", map[string]string{"TASKS_DATE_ORDER": "mdy"}, "mdy", "TASKS_DATE_ORDER env"},
		{"env is case insensitive", "", map[string]string{"TASKS_DATE_ORDER": "DMY"}, "dmy", "TASKS_DATE_ORDER env"},
		{"invalid config value is dropped", "date_order = backwards\n", nil, "mdy", "default"},
		{"invalid env falls through to config file", "date_order = dmy\n", map[string]string{"TASKS_DATE_ORDER": "nonsense"}, "dmy", "config file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := resolve(t, tc.config, tc.overrides)
			if paths.DateOrder != tc.want || paths.Sources["date_order"] != tc.source {
				t.Fatalf("date order = %q (%q), want %q (%q)", paths.DateOrder, paths.Sources["date_order"], tc.want, tc.source)
			}
		})
	}
}

// -- theme and colors (TUI appearance) ---------------------------------------

func TestThemeDefaultsWithNoColors(t *testing.T) {
	paths := resolve(t, "", nil)
	if paths.Theme != "default" || len(paths.Colors) != 0 {
		t.Fatalf("theme = %q with colors %#v", paths.Theme, paths.Colors)
	}
}

// test_theme_and_colors_from_config_file. The color specs are collected
// VERBATIM — the theme owns the vocabulary, so the config layer must not judge
// a spec it does not recognize.
func TestThemeAndColorsFromConfigFile(t *testing.T) {
	paths := resolve(t, "theme = mono\ncolor.accent = magenta\ncolor.link = underline #88aaff\n", nil)
	if paths.Theme != "mono" || paths.Sources["theme"] != "config file" {
		t.Fatalf("theme = %q (%q)", paths.Theme, paths.Sources["theme"])
	}
	want := map[string]string{"accent": "magenta", "link": "underline #88aaff"}
	if !reflect.DeepEqual(paths.Colors, want) {
		t.Fatalf("colors = %#v, want %#v", paths.Colors, want)
	}
}

// A generated theme name is not validated here either: an unknown name reaches
// the theme layer, which falls back to the default look.
func TestGeneratedThemeNameFromConfigFile(t *testing.T) {
	paths := resolve(t, "theme = dracula\n", nil)
	if paths.Theme != "dracula" || paths.Sources["theme"] != "config file" {
		t.Fatalf("theme = %q (%q)", paths.Theme, paths.Sources["theme"])
	}
}

func TestTasksThemeEnvBeatsConfigFile(t *testing.T) {
	paths := resolve(t, "theme = mono\n", map[string]string{"TASKS_THEME": "default"})
	if paths.Theme != "default" || paths.Sources["theme"] != "TASKS_THEME env" {
		t.Fatalf("theme = %q (%q)", paths.Theme, paths.Sources["theme"])
	}
}

// test_no_color_env_selects_mono_unless_theme_is_explicit
func TestNoColorSelectsMonoUnlessTheThemeIsExplicit(t *testing.T) {
	paths := resolve(t, "", map[string]string{"NO_COLOR": "1"})
	if paths.Theme != "mono" || paths.Sources["theme"] != "NO_COLOR env" {
		t.Fatalf("theme = %q (%q), want mono", paths.Theme, paths.Sources["theme"])
	}
	explicit := resolve(t, "theme = default\n", map[string]string{"NO_COLOR": "1"})
	if explicit.Theme != "default" {
		t.Fatalf("theme = %q, want an explicit theme to win over NO_COLOR", explicit.Theme)
	}
	if empty := resolve(t, "", map[string]string{"NO_COLOR": ""}); empty.Theme != "default" {
		t.Fatalf("theme = %q, want an empty NO_COLOR to be absent", empty.Theme)
	}
}

// test_bare_color_dot_key_is_ignored — a `color.` line names no slot.
func TestBareColorDotKeyIsIgnored(t *testing.T) {
	if paths := resolve(t, "color. = red\n", nil); len(paths.Colors) != 0 {
		t.Fatalf("colors = %#v, want none", paths.Colors)
	}
}

// A color spec legitimately contains a hex token after a space, so color lines
// are the one place an inline comment is NOT stripped — otherwise `bold #ff8800`
// would silently become `bold`.
func TestColorSpecsKeepTheirHexTokenWhileOtherValuesDropInlineComments(t *testing.T) {
	paths := resolve(t, "color.accent = bold #ff8800\ntheme = mono # my theme\n", nil)
	if got := paths.Colors["accent"]; got != "bold #ff8800" {
		t.Fatalf("color.accent = %q, want the hex token kept", got)
	}
	if paths.Theme != "mono" {
		t.Fatalf("theme = %q, want the inline comment stripped", paths.Theme)
	}
}

// -- mouse -------------------------------------------------------------------

func TestMouseTrackingDefaultsOnAndTakesOnOffSpellings(t *testing.T) {
	if paths := resolve(t, "", nil); !paths.Mouse || paths.Sources["mouse"] != "default" {
		t.Fatalf("mouse = %v (%q), want on by default", paths.Mouse, paths.Sources["mouse"])
	}
	off := resolve(t, "mouse = off\n", nil)
	if off.Mouse || off.Sources["mouse"] != "config file" {
		t.Fatalf("mouse = %v (%q), want off from the config file", off.Mouse, off.Sources["mouse"])
	}
	on := resolve(t, "mouse = off\n", map[string]string{"TASKS_MOUSE": "on"})
	if !on.Mouse || on.Sources["mouse"] != "TASKS_MOUSE env" {
		t.Fatalf("mouse = %v (%q), want the environment to win", on.Mouse, on.Sources["mouse"])
	}
	for _, spelling := range []string{"off", "false", "no", "0"} {
		if got := resolve(t, "", map[string]string{"TASKS_MOUSE": spelling}); got.Mouse {
			t.Fatalf("TASKS_MOUSE=%q left mouse on", spelling)
		}
	}
	for _, spelling := range []string{"on", "true", "yes", "1", " ON "} {
		if got := resolve(t, "mouse = off\n", map[string]string{"TASKS_MOUSE": spelling}); !got.Mouse {
			t.Fatalf("TASKS_MOUSE=%q left mouse off", spelling)
		}
	}
	// An unusable value falls through to the next source rather than crashing.
	if got := resolve(t, "mouse = off\n", map[string]string{"TASKS_MOUSE": "maybe"}); got.Mouse {
		t.Fatal("an unparseable TASKS_MOUSE should fall through to the config file")
	}
	if got := resolve(t, "mouse = maybe\n", nil); !got.Mouse || got.Sources["mouse"] != "default" {
		t.Fatalf("mouse = %v (%q), want an unparseable config value dropped", got.Mouse, got.Sources["mouse"])
	}
}

// -- prompt facts (the agent's "Current environment" block) ------------------

// test_prompt_facts_default_datetime_and_hostname_on, and the three ways a
// config line can fail to change them.
func TestPromptFactsResolveAgainstTheRegistry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		config string
		want   map[string]bool
	}{
		{"defaults are both on", "", map[string]bool{"datetime": true, "hostname": true}},
		{"from config file", "prompt.datetime = off\nprompt.hostname = on\n", map[string]bool{"datetime": false, "hostname": true}},
		// An unregistered name is kept by the parser for forward compatibility
		// but cannot claim to be on: nothing would render it.
		{"unknown name ignored at resolve", "prompt.weather = on\nprompt.datetime = off\n", map[string]bool{"datetime": false, "hostname": true}},
		{"invalid toggle falls through to the default", "prompt.datetime = maybe\n", map[string]bool{"datetime": true, "hostname": true}},
		{"bare dot key ignored", "prompt. = on\n", map[string]bool{"datetime": true, "hostname": true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolve(t, tc.config, nil).PromptFacts; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("prompt facts = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// PromptFacts.parse_toggle has a STRICTER vocabulary than the mouse toggle:
// "yes"/"no" are not prompt-fact spellings, so they leave the default in place.
func TestPromptFactToggleVocabularyIsNarrowerThanOnOff(t *testing.T) {
	for _, spelling := range []string{"off", "false", "0", "OFF"} {
		if got := resolve(t, "prompt.datetime = "+spelling+"\n", nil); got.PromptFacts["datetime"] {
			t.Fatalf("prompt.datetime = %q left the fact on", spelling)
		}
	}
	for _, spelling := range []string{"no", "nope", "disabled"} {
		if got := resolve(t, "prompt.datetime = "+spelling+"\n", nil); !got.PromptFacts["datetime"] {
			t.Fatalf("prompt.datetime = %q switched the fact off; only on/true/1/off/false/0 count", spelling)
		}
	}
}

// -- host-specific creation context ------------------------------------------

// test_host_context_prefers_full_hostname_then_short_label
func TestHostContextPrefersTheFullHostnameThenTheShortLabel(t *testing.T) {
	body := "host_context.home-mac = home\nhost_context.home-mac.local = @specific\n"
	env, _ := testEnv(t, map[string]string{"TZ": "Etc/UTC"})
	writeConfig(t, env, body)

	full := Resolve("/repo", env, func() string { return "HOME-MAC.LOCAL" })
	if full.Hostname != "HOME-MAC.LOCAL" {
		t.Fatalf("hostname = %q, want the detected spelling verbatim", full.Hostname)
	}
	if full.HostContext != "@specific" || full.HostContextSource != "host_context.home-mac.local" {
		t.Fatalf("context = %q (%q), want the full-hostname row", full.HostContext, full.HostContextSource)
	}

	short := Resolve("/repo", env, func() string { return "home-mac.example" })
	if short.HostContext != "@home" || short.HostContextSource != "host_context.home-mac" {
		t.Fatalf("context = %q (%q), want the short-label row", short.HostContext, short.HostContextSource)
	}
}

// test_host_context_ignores_unmatched_and_malformed_rows. A bare "@", a value
// with whitespace, and a selector that is not hostname-shaped are all dropped
// at parse; a value that lacks the "@" prefix gets one.
func TestHostContextIgnoresUnmatchedAndMalformedRows(t *testing.T) {
	body := "host_context.bad host = @bad\nhost_context.bare = @\n" +
		"host_context.office = work desk\nhost_context.home = @home\n"
	paths := resolve(t, body, nil)
	if paths.Hostname != "test-host.local" {
		t.Fatalf("hostname = %q", paths.Hostname)
	}
	if paths.HostContext != "" || paths.HostContextSource != "" {
		t.Fatalf("context = %q (%q), want none for an unmatched host", paths.HostContext, paths.HostContextSource)
	}
	if want := map[string]string{"home": "@home"}; !reflect.DeepEqual(paths.HostContexts, want) {
		t.Fatalf("host contexts = %#v, want %#v", paths.HostContexts, want)
	}
	// The "@" is added when the value omits it, so both spellings configure the
	// same context rather than two that differ by one character.
	if bare := resolve(t, "host_context.test-host = home\n", nil); bare.HostContext != "@home" {
		t.Fatalf("context = %q, want the @ prefix added", bare.HostContext)
	}
}

// The selector is matched case-insensitively and the stored key is folded, so a
// config written with the host's display capitalization still matches.
func TestHostContextSelectorIsCaseFolded(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"TZ": "Etc/UTC"})
	writeConfig(t, env, "host_context.HOME-MAC = home\n")
	paths := Resolve("/repo", env, func() string { return "Home-Mac" })
	if paths.HostContext != "@home" || paths.HostContextSource != "host_context.home-mac" {
		t.Fatalf("context = %q (%q)", paths.HostContext, paths.HostContextSource)
	}
}

// An unavailable hostname is not an error: the context is simply absent, and
// nothing else about resolution changes.
func TestUnavailableHostnameYieldsNoContext(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"TZ": "Etc/UTC"})
	writeConfig(t, env, "host_context.home = @home\n")
	paths := Resolve("/repo", env, func() string { return "  " })
	if paths.Hostname != "" || paths.HostContext != "" || paths.HostContextSource != "" {
		t.Fatalf("host facts = %q/%q/%q, want all absent", paths.Hostname, paths.HostContext, paths.HostContextSource)
	}
	if paths.Org != "/repo/tasks.jsonl" {
		t.Fatalf("org = %q, want resolution to continue regardless", paths.Org)
	}
}

// The hostname provider defaults through Determinism, so TASKS_PIN_HOSTNAME
// reaches host-context selection without every adapter remembering to pass it.
// (test_config_resolve_honors_the_hostname_pin, and its explicit-provider twin.)
func TestResolveHonorsTheHostnamePinAndAnExplicitProvider(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"TZ": "Etc/UTC", determinism.NameHostname: "fixture-host.local"})
	if got := Resolve("/repo", env, nil); got.Hostname != "fixture-host.local" {
		t.Fatalf("hostname = %q, want the pin", got.Hostname)
	}
	if got := Resolve("/repo", env, func() string { return "explicit-host" }); got.Hostname != "explicit-host" {
		t.Fatalf("hostname = %q, want the explicit provider to win", got.Hostname)
	}
}

// -- links and link systems --------------------------------------------------

// Both dotted namespaces are name-constrained so a stray `foo.bar = x` line
// cannot inject a shorthand that then matches prose.
func TestLinkAndSystemNamespacesAreNameConstrained(t *testing.T) {
	body := "link.jira = https://jira.example.com/browse/%s\nsystem.gitlab = git.example.com\n" +
		"link.Bad = x\nlink.9bad = x\nlink. = x\nsystem.BAD = y\n"
	paths := resolve(t, body, nil)
	wantLinks := map[string]string{"jira": "https://jira.example.com/browse/%s"}
	if !reflect.DeepEqual(paths.Links, wantLinks) {
		t.Fatalf("links = %#v, want %#v", paths.Links, wantLinks)
	}
	wantSystems := map[string]string{"gitlab": "git.example.com"}
	if !reflect.DeepEqual(paths.LinkSystems, wantSystems) {
		t.Fatalf("link systems = %#v, want %#v", paths.LinkSystems, wantSystems)
	}
}

// A "#" INSIDE a value survives — a URL anchor has no space before it — while
// whitespace followed by "#" ends the value.
func TestInlineCommentsRequireLeadingWhitespace(t *testing.T) {
	paths := resolve(t, "link.doc = https://example.com/page#section\nlink.other = https://example.com/x # trailing\n", nil)
	if got := paths.Links["doc"]; got != "https://example.com/page#section" {
		t.Fatalf("link.doc = %q, want the anchor kept", got)
	}
	if got := paths.Links["other"]; got != "https://example.com/x" {
		t.Fatalf("link.other = %q, want the comment stripped", got)
	}
}

// -- config-file parsing edge cases ------------------------------------------

func TestConfigFileParsingBoundaries(t *testing.T) {
	// A line with no "=" is not a setting; a key with an empty value is dropped
	// rather than setting the value to "" (which would blank a path).
	paths := resolve(t, "dir\nfile =\n   \n# comment = still a comment\ndir = /conf/dir\n", nil)
	if paths.Org != "/conf/dir/tasks.jsonl" {
		t.Fatalf("org = %q, want the one real key to decide", paths.Org)
	}
	// The LAST assignment of a key wins, the way repeated hash assignment does.
	if last := resolve(t, "urgent_days = 5\nurgent_days = 9\n", nil); last.UrgentDays != 9 {
		t.Fatalf("urgent days = %d, want the last line to win", last.UrgentDays)
	}
	// Whitespace around key and value is insignificant.
	if padded := resolve(t, "   max_depth   =   7   \n", nil); padded.MaxDepth != 7 {
		t.Fatalf("max depth = %d, want padding ignored", padded.MaxDepth)
	}
	// A value containing "=" keeps everything after the FIRST separator.
	if link := resolve(t, "link.q = https://example.com/?a=1&b=2\n", nil); link.Links["q"] != "https://example.com/?a=1&b=2" {
		t.Fatalf("link.q = %q", link.Links["q"])
	}
}

// A relative or `~` path in the config file expands against the CALLER's
// environment, not the process's. This is the defect Wave 0 fixed: reading the
// real environment here made a sandboxed HOME disagree with the oracle exactly
// when it mattered.
func TestConfigFilePathsExpandAgainstTheCallersHome(t *testing.T) {
	env, home := testEnv(t, map[string]string{"TZ": "Etc/UTC"})
	writeConfig(t, env, "dir = ~/tasks\nmemory = ~/notes/mem.md\n")
	paths := Resolve("/repo", env, func() string { return "host" })
	if want := filepath.Join(home, "tasks", "tasks.jsonl"); paths.Org != want {
		t.Fatalf("org = %q, want %q", paths.Org, want)
	}
	if want := filepath.Join(home, "notes", "mem.md"); paths.Memory != want {
		t.Fatalf("memory = %q, want %q", paths.Memory, want)
	}
	if paths.Sources["memory"] != "config file" {
		t.Fatalf("memory source = %q", paths.Sources["memory"])
	}
}

// -- for_dir (test sandboxing) -----------------------------------------------

// test_for_dir_pins_both_files_ignoring_env_and_config, plus every default it
// is required to carry. A sandbox that quietly read the user's config file
// would be a test harness pointed at real task data.
func TestForDirPinsEverythingAndIgnoresEnvAndConfig(t *testing.T) {
	env, _ := testEnv(t, map[string]string{
		"TASKS_DIR": "/data", "TASKS_FILE": "/elsewhere/mine.jsonl",
		"TASKS_URGENT_DAYS": "14", "TASKS_MAX_DEPTH": "9", "TASKS_THEME": "mono",
		"TASKS_MOUSE": "off", "TZ": "Asia/Tokyo",
	})
	writeConfig(t, env, "dir = /from-file\nurgent_days = 7\n")

	paths := ForDir("/sandbox", env)
	if paths.Org != "/sandbox/tasks.jsonl" || paths.Archive != "/sandbox/archive.jsonl" {
		t.Fatalf("org/archive = %q/%q, want the pinned pair", paths.Org, paths.Archive)
	}
	if paths.Memory != "/sandbox/agent-memory.md" || paths.Sources["memory"] != "pinned" {
		t.Fatalf("memory = %q (%q)", paths.Memory, paths.Sources["memory"])
	}
	if paths.UrgentDays != DefaultUrgentDays || paths.MaxDepth != DefaultMaxDepth {
		t.Fatalf("urgent/depth = %d/%d, want the built-in defaults", paths.UrgentDays, paths.MaxDepth)
	}
	if paths.Theme != DefaultTheme || !paths.Mouse || paths.TimeFormat != DefaultTimeFormat {
		t.Fatalf("theme/mouse/time format = %q/%v/%d", paths.Theme, paths.Mouse, paths.TimeFormat)
	}
	if paths.Timezone != "Etc/UTC" || paths.DateOrder != DefaultDateOrder {
		t.Fatalf("timezone/date order = %q/%q", paths.Timezone, paths.DateOrder)
	}
	want := map[string]bool{"datetime": true, "hostname": true}
	if !reflect.DeepEqual(paths.PromptFacts, want) {
		t.Fatalf("prompt facts = %#v, want %#v", paths.PromptFacts, want)
	}
	// It never asks for a hostname, so nothing about the host reaches a sandbox.
	if paths.Hostname != "" || paths.HostContext != "" || paths.HostContextSource != "" || len(paths.HostContexts) != 0 {
		t.Fatalf("host facts = %q/%q/%q/%#v, want all absent",
			paths.Hostname, paths.HostContext, paths.HostContextSource, paths.HostContexts)
	}
	// It still reports where the config file WOULD be, which is what makes a
	// sandbox's own `config` output explainable.
	if paths.ConfigFile != ConfigFile(env) {
		t.Fatalf("config file = %q, want %q", paths.ConfigFile, ConfigFile(env))
	}
}
