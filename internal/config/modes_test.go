package config

import (
	"reflect"
	"strings"
	"testing"
)

// delegation_modes resolves like every other setting: env beats the config
// file, the config file beats the built-in vocabulary, and the SOURCE is
// reported so `tasks config` can answer "why is that mode refused?" without the
// user reading this code.
func TestDelegationModesPrecedenceAndSource(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		overrides map[string]string
		want      []string
		source    string
	}{
		{"default", "", nil, []string{"refine", "research", "implement"}, "default"},
		{"config file", "delegation_modes = triage, ship\n", nil, []string{"triage", "ship"}, "config file"},
		{"whitespace is not syntax", "delegation_modes =    triage ,ship   \n", nil, []string{"triage", "ship"}, "config file"},
		{"env wins", "delegation_modes = triage, ship\n",
			map[string]string{"TASKS_DELEGATION_MODES": "review"}, []string{"review"}, "TASKS_DELEGATION_MODES env"},
		{"one mode is a list", "delegation_modes = implement\n", nil, []string{"implement"}, "config file"},
		{"digits and underscores", "delegation_modes = deep_research, tier2\n", nil,
			[]string{"deep_research", "tier2"}, "config file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := resolve(t, tc.body, tc.overrides)
			if !reflect.DeepEqual(paths.DelegationModes, tc.want) {
				t.Fatalf("modes = %#v, want %#v", paths.DelegationModes, tc.want)
			}
			if got := paths.Sources["delegation_modes"]; got != tc.source {
				t.Fatalf("source = %q, want %q", got, tc.source)
			}
			if got := paths.Modes().Modes(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("vocabulary = %#v, want %#v", got, tc.want)
			}
			if len(paths.Warnings) != 0 {
				t.Fatalf("warnings = %v, want none", paths.Warnings)
			}
		})
	}
}

// A list this binary cannot understand falls back WHOLE. Keeping the readable
// half would run the user against a set they never wrote, and a mode that
// quietly vanished is how a delegation gets refused with no explanation.
func TestMalformedDelegationModesFallBackWithAWarning(t *testing.T) {
	for _, tc := range []struct{ name, body, problem string }{
		{"uppercase", "delegation_modes = Triage\n", `"Triage" is not a mode name`},
		{"punctuation", "delegation_modes = triage, ship!\n", `"ship!" is not a mode name`},
		{"space inside a mode", "delegation_modes = deep research\n", `"deep research" is not a mode name`},
		{"duplicate", "delegation_modes = triage, ship, triage\n", `"triage" is listed twice`},
		{"empty", "delegation_modes = , ,\n", "the list is empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			paths := resolve(t, tc.body, nil)
			want := []string{"refine", "research", "implement"}
			if !reflect.DeepEqual(paths.DelegationModes, want) {
				t.Fatalf("modes = %#v, want the built-in set", paths.DelegationModes)
			}
			if paths.Sources["delegation_modes"] != "default" {
				t.Fatalf("source = %q", paths.Sources["delegation_modes"])
			}
			if len(paths.Warnings) != 1 || !strings.Contains(paths.Warnings[0], tc.problem) {
				t.Fatalf("warnings = %v, want one naming %q", paths.Warnings, tc.problem)
			}
			if !strings.Contains(paths.Warnings[0], "refine/research/implement") {
				t.Fatalf("warning does not say which set is in use: %v", paths.Warnings)
			}
		})
	}
}

// An invalid env value must not skip past a VALID config file list, for the
// same reason a typo'd TASKS_TIMEZONE must not skip past a configured zone.
func TestAnInvalidDelegationModesEnvFallsThroughToTheConfigFile(t *testing.T) {
	paths := resolve(t, "delegation_modes = triage, ship\n",
		map[string]string{"TASKS_DELEGATION_MODES": "Nope!"})
	if got := paths.Modes().Quoted(); got != "triage/ship" {
		t.Fatalf("modes = %q", got)
	}
	if paths.Sources["delegation_modes"] != "config file" {
		t.Fatalf("source = %q", paths.Sources["delegation_modes"])
	}
	if len(paths.Warnings) != 1 {
		t.Fatalf("warnings = %v, want one", paths.Warnings)
	}
}

// ForDir is what a sandbox pins itself with, so it must answer the vocabulary
// question too rather than leave a store to guess.
func TestForDirReportsTheBuiltInVocabulary(t *testing.T) {
	env, _ := testEnv(t, map[string]string{"TZ": "Etc/UTC"})
	paths := ForDir(t.TempDir(), env)
	if got := paths.Modes().Quoted(); got != "refine/research/implement" {
		t.Fatalf("modes = %q", got)
	}
	if paths.Sources["delegation_modes"] != "default" {
		t.Fatalf("source = %q", paths.Sources["delegation_modes"])
	}
}
