package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/marcus/tasks/internal/config"
	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/timezones"
)

// config reports where the task files resolved and why. It is the one command
// the conformance runner calls before any case runs — not to point anything at
// the live store, but to know which directory to stay away from — so it has to
// answer even when nothing else in this binary would.
func (s *surfaceContext) config(args []string) int {
	if slices.Contains(args, "--json") {
		out(s.configJSON())
		return 0
	}
	s.configHuman()
	return 0
}

func (s *surfaceContext) configJSON() string {
	paths := s.paths
	w := jsonout.New()
	w.BeginObject()
	w.KeyBool("configured", paths.Configured)
	w.KeyStr("org", paths.Org)
	w.KeyStr("archive", paths.Archive)
	w.KeyStr("memory", paths.Memory)
	w.KeyInt("urgent_days", paths.UrgentDays)
	w.KeyInt("max_depth", paths.MaxDepth)
	w.KeyStr("theme", paths.Theme)
	w.Key("colors")
	writeStringMap(w, paths.Colors)
	w.KeyBool("mouse", paths.Mouse)
	w.KeyStr("timezone", paths.Timezone)
	w.KeyInt("time_format", paths.TimeFormat)
	w.KeyStr("date_order", paths.DateOrder)
	w.Key("delegation_modes")
	w.Strings(paths.DelegationModes)
	w.KeyStr("tzdb_version", timezones.TZDBVersion())
	w.KeyBool("timezone_fallback_warning", paths.TimezoneFallbackWarning)
	w.Key("links")
	writeStringMap(w, paths.Links)
	w.Key("link_systems")
	writeStringMap(w, paths.LinkSystems)
	w.Key("prompt_facts")
	writeBoolMap(w, paths.PromptFacts)
	w.KeyStrOrNull("hostname", paths.Hostname)
	w.KeyStrOrNull("host_context", paths.HostContext)
	w.KeyStrOrNull("host_context_source", paths.HostContextSource)
	w.Key("host_contexts")
	writeStringMap(w, paths.HostContexts)
	w.Key("sources")
	writeSources(w, paths.Sources)
	w.KeyBool("memory_exists", isFile(paths.Memory))
	w.KeyStr("config_file", paths.ConfigFile)
	w.KeyBool("config_file_exists", isFile(paths.ConfigFile))
	w.EndObject()
	return w.String()
}

func (s *surfaceContext) configHuman() {
	paths := s.paths
	if !paths.Configured {
		fmt.Fprintln(os.Stderr, config.ConfigurationRequiredMessage(paths))
	}
	source := func(key string) string { return dim("(" + paths.Sources[key] + ")") }
	out(fmt.Sprintf("org:         %s  %s", paths.Org, source("org")))
	out(fmt.Sprintf("archive:     %s  %s", paths.Archive, source("archive")))
	memoryNote := paths.Sources["memory"]
	if !isFile(paths.Memory) {
		memoryNote += ", not present"
	}
	out(fmt.Sprintf("memory:      %s  %s", paths.Memory, dim("("+memoryNote+")")))
	out(fmt.Sprintf("urgent_days: %d  %s", paths.UrgentDays, source("urgent_days")))
	out(fmt.Sprintf("max_depth:   %d  %s", paths.MaxDepth, source("max_depth")))
	out(fmt.Sprintf("theme:       %s  %s", paths.Theme, source("theme")))
	mouse := "off"
	if paths.Mouse {
		mouse = "on"
	}
	out(fmt.Sprintf("mouse:       %s  %s", mouse, source("mouse")))
	out(fmt.Sprintf("timezone:    %s  %s", paths.Timezone, source("timezone")))
	out(fmt.Sprintf("time_format: %d  %s", paths.TimeFormat, source("time_format")))
	out(fmt.Sprintf("date_order:  %s  %s", paths.DateOrder, source("date_order")))
	out(fmt.Sprintf("delegation_modes: %s  %s",
		strings.Join(paths.DelegationModes, ", "), source("delegation_modes")))
	hostname := paths.Hostname
	if hostname == "" {
		hostname = "unavailable"
	}
	out("hostname:    " + hostname)
	hostContext := paths.HostContext
	if hostContext == "" {
		hostContext = "none"
	}
	contextSource := ""
	if paths.HostContextSource != "" {
		contextSource = "  " + dim("("+paths.HostContextSource+")")
	}
	out("host_context: " + hostContext + contextSource)
	out("tzdb:        " + timezones.TZDBVersion())
	if paths.TimezoneFallbackWarning {
		fmt.Fprintln(os.Stderr, "warning: host time zone could not be detected; using Etc/UTC")
	}
	if len(paths.Colors) > 0 {
		pairs := ""
		for index, key := range sortedKeys(paths.Colors) {
			if index > 0 {
				pairs += " · "
			}
			pairs += key + "=" + paths.Colors[key]
		}
		out("colors:      " + pairs)
	}
	for _, name := range sortedKeys(paths.Links) {
		out(fmt.Sprintf("link.%s:   %s", name, paths.Links[name]))
	}
	for _, name := range sortedKeys(paths.LinkSystems) {
		out(fmt.Sprintf("system.%s: %s", name, paths.LinkSystems[name]))
	}
	for _, name := range sortedKeys(paths.PromptFacts) {
		state := "off"
		if paths.PromptFacts[name] {
			state = "on"
		}
		out(fmt.Sprintf("prompt.%s: %s", name, state))
	}
	suffix := ""
	if !isFile(paths.ConfigFile) {
		suffix = dim(" (not present)")
	}
	out("config:      " + paths.ConfigFile + suffix)
}

// sourceOrder is the order Tasks::Config builds its sources hash in. Ruby
// Hash preserves insertion order and JSON.generate honours it, so a sorted
// emission here would produce different bytes for identical settings.
var sourceOrder = []string{
	"org", "archive", "memory", "urgent_days", "max_depth",
	"theme", "mouse", "timezone", "time_format", "date_order", "delegation_modes",
}

func writeSources(w *jsonout.Writer, sources map[string]string) {
	w.BeginObject()
	for _, key := range sourceOrder {
		if value, ok := sources[key]; ok {
			w.KeyStr(key, value)
		}
	}
	w.EndObject()
}

// writeStringMap emits a config-file-derived map. Ruby preserves the file's own
// line order; this port cannot recover it from a Go map, so it sorts — the one
// place `config --json` is knowingly not byte-identical, and only for stores
// that configure link/colour/host-context keys at all. No conformance case
// does.
func writeStringMap(w *jsonout.Writer, values map[string]string) {
	w.BeginObject()
	for _, key := range sortedKeys(values) {
		w.KeyStr(key, values[key])
	}
	w.EndObject()
}

func writeBoolMap(w *jsonout.Writer, values map[string]bool) {
	w.BeginObject()
	for _, key := range sortedKeys(values) {
		w.KeyBool(key, values[key])
	}
	w.EndObject()
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// config is dispatched here so main.go never lists the commands.
func init() {
	register("config", (*surfaceContext).config)
}
