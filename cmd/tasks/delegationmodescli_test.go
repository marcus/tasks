package main

import (
	"strings"
	"testing"
)

// The configured delegation vocabulary is exercised as a BLACK BOX, because the
// whole claim it makes is a cross-layer one: a line in ~/.config/tasks/config
// has to reach the store's refusal, the usage line, `tasks config`, and `tasks
// help` — and every one of those is reached through argv, not through a
// package boundary.

const delegationModesFixture = `{"type":"meta","version":2}
{"type":"section","id":"eeee0001","title":"Work"}
{"type":"task","id":"eeee0002","parent":"eeee0001","state":"NEXT","title":"Ship the thing"}
{"type":"task","id":"eeee0003","parent":"eeee0001","state":"NEXT","title":"Second thing"}
`

// A configured list is the list the CLI enforces: the user's word is accepted,
// and a BUILT-IN word that is not in it is refused — quoting the configured
// set, not the one this binary shipped with.
func TestConfiguredDelegationModesDecideWhatDelegateAccepts(t *testing.T) {
	dir := seedStore(t, delegationModesFixture)
	seedConfig(t, dir, "delegation_modes = triage, ship\n")

	accepted := runCLI(t, dir, "delegate", "eeee0002", "triage")
	if accepted.status != 0 {
		t.Fatalf("configured mode refused: exit %d, stderr %q", accepted.status, accepted.stderr)
	}
	if body := storeBytes(t, dir); !strings.Contains(body, `"mode":"triage"`) {
		t.Fatalf("store does not carry the configured mode: %s", body)
	}

	refused := runCLI(t, dir, "delegate", "eeee0003", "research")
	if refused.status == 0 {
		t.Fatal("a mode outside the configured list was accepted")
	}
	if !strings.Contains(refused.stderr, "triage/ship") {
		t.Fatalf("refusal does not quote the configured list: %q", refused.stderr)
	}
	if strings.Contains(refused.stderr, "refine/research/implement") {
		t.Fatalf("refusal quotes the built-in list: %q", refused.stderr)
	}
}

// No config key means exactly today's behaviour, which is the promise that lets
// this ship without every existing user noticing.
func TestUnconfiguredDelegationModesAreTheBuiltInSet(t *testing.T) {
	dir := seedStore(t, delegationModesFixture)

	accepted := runCLI(t, dir, "delegate", "eeee0002", "research")
	if accepted.status != 0 {
		t.Fatalf("built-in mode refused: exit %d, stderr %q", accepted.status, accepted.stderr)
	}
	refused := runCLI(t, dir, "delegate", "eeee0003", "triage")
	if refused.status == 0 || !strings.Contains(refused.stderr, "refine/research/implement") {
		t.Fatalf("exit %d, stderr %q", refused.status, refused.stderr)
	}
}

// A list this binary cannot understand degrades to the built-in set with a
// warning. The store stays writable throughout: the vocabulary decides which
// modes may be written, never whether writing is possible.
func TestMalformedDelegationModesWarnAndFallBack(t *testing.T) {
	for name, body := range map[string]string{
		"bad shape": "delegation_modes = Triage, ship!\n",
		"duplicate": "delegation_modes = triage, triage\n",
		"empty":     "delegation_modes = ,  ,\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := seedStore(t, delegationModesFixture)
			seedConfig(t, dir, body)

			result := runCLI(t, dir, "delegate", "eeee0002", "research")
			if result.status != 0 {
				t.Fatalf("built-in fallback refused: exit %d, stderr %q", result.status, result.stderr)
			}
			if !strings.Contains(result.stderr, "ignoring delegation_modes") {
				t.Fatalf("no warning: %q", result.stderr)
			}
			if !strings.Contains(result.stderr, "refine/research/implement") {
				t.Fatalf("warning does not name the set in use: %q", result.stderr)
			}
			settings := runCLI(t, dir, "config")
			if !strings.Contains(settings.stdout, "delegation_modes: refine, research, implement  (default)") {
				t.Fatalf("config does not report the fallback: %q", settings.stdout)
			}
		})
	}
}

// `tasks config` reports the resolved vocabulary AND where it came from, in
// both surfaces, because the answer to "why did that mode get refused" is a
// setting the user has to be able to see.
func TestConfigReportsTheDelegationModesAndTheirSource(t *testing.T) {
	dir := seedStore(t, delegationModesFixture)
	seedConfig(t, dir, "delegation_modes = triage, ship\n")

	human := runCLI(t, dir, "config")
	if !strings.Contains(human.stdout, "delegation_modes: triage, ship  (config file)") {
		t.Fatalf("config = %q", human.stdout)
	}
	structured := runCLI(t, dir, "config", "--json")
	if !strings.Contains(structured.stdout, `"delegation_modes":["triage","ship"]`) {
		t.Fatalf("config --json = %q", structured.stdout)
	}
	if !strings.Contains(structured.stdout, `"delegation_modes":"config file"`) {
		t.Fatalf("config --json sources = %q", structured.stdout)
	}
}

// help is the other place a user goes to learn the vocabulary, and it must
// agree with the refusal. It prints WITHOUT a store, so this also proves it
// still prints when the store is unreadable.
func TestHelpQuotesTheConfiguredDelegationModes(t *testing.T) {
	dir := seedStore(t, delegationModesFixture)
	seedConfig(t, dir, "delegation_modes = triage, ship\n")

	help := runCLI(t, dir, "help")
	if help.status != 0 {
		t.Fatalf("help exit %d", help.status)
	}
	if !strings.Contains(help.stdout, "delegate <ref> triage|ship") {
		t.Fatalf("help does not quote the configured modes: %q", help.stdout)
	}
	if strings.Contains(help.stdout, "refine|research|implement") {
		t.Fatalf("help quotes the built-in modes: %q", help.stdout)
	}
}

func TestHelpPrintsWithAnUnreadableStore(t *testing.T) {
	dir := seedStore(t, "{ this is not JSONL\n")
	seedConfig(t, dir, "delegation_modes = triage, ship\n")

	help := runCLI(t, dir, "help")
	if help.status != 0 || !strings.Contains(help.stdout, "delegate <ref> triage|ship") {
		t.Fatalf("exit %d, stdout %q", help.status, help.stdout)
	}
}

// A record carrying a mode the configuration no longer lists still LOADS: show
// renders it, and check reports a warning rather than an error, so the file
// stays valid and the store stays writable.
func TestAnUnconfiguredModeOnAnExistingRecordIsAWarningOnly(t *testing.T) {
	dir := seedStore(t, `{"type":"meta","version":2}
{"type":"section","id":"eeee0001","title":"Work"}
{"type":"task","id":"eeee0002","parent":"eeee0001","state":"NEXT","title":"Ship the thing","delegation":{"kind":"agent","mode":"research","status":"ready","at":"2026-07-20T11:00:00Z"}}
`)
	seedConfig(t, dir, "delegation_modes = triage, ship\n")

	shown := runCLI(t, dir, "show", "eeee0002")
	if shown.status != 0 {
		t.Fatalf("show refused a record with an unconfigured mode: %d %q", shown.status, shown.stderr)
	}
	checked := runCLI(t, dir, "check")
	if checked.status != 0 {
		t.Fatalf("check treated an unconfigured mode as an error: %d\n%s", checked.status, checked.stdout)
	}
	if !strings.Contains(checked.stdout, "is not in the configured vocabulary triage/ship") {
		t.Fatalf("check did not warn: %q", checked.stdout)
	}
	// The store is still writable, which is the part that matters most.
	written := runCLI(t, dir, "delegate", "eeee0002", "ship")
	if written.status != 0 {
		t.Fatalf("store was unwritable: %d %q", written.status, written.stderr)
	}
}

// Two configurations, one process, two live vocabularies. Packet C proved the
// store carries the vocabulary as a value; this proves the CONFIG layer feeds
// two of them without either leaking into the other — which is the property a
// process-wide setter would have destroyed, and the reason there is none.
func TestTwoConfiguredVocabulariesCoexistInOneProcess(t *testing.T) {
	first := seedStore(t, delegationModesFixture)
	seedConfig(t, first, "delegation_modes = triage, ship\n")
	second := seedStore(t, delegationModesFixture)
	seedConfig(t, second, "delegation_modes = review\n")
	plain := seedStore(t, delegationModesFixture)

	for _, tc := range []struct {
		dir     string
		accepts string
		quoted  string
	}{
		{first, "triage", "triage/ship"},
		{second, "review", "review"},
		{plain, "refine", "refine/research/implement"},
	} {
		if accepted := runCLI(t, tc.dir, "delegate", "eeee0002", tc.accepts); accepted.status != 0 {
			t.Fatalf("%s refused %q: %q", tc.dir, tc.accepts, accepted.stderr)
		}
		refused := runCLI(t, tc.dir, "delegate", "eeee0003", "nonesuch")
		if refused.status == 0 || !strings.Contains(refused.stderr, tc.quoted) {
			t.Fatalf("%s: exit %d, stderr %q, want it to quote %q",
				tc.dir, refused.status, refused.stderr, tc.quoted)
		}
	}

	// And back to the first, after the others have run: nothing accumulated.
	if again := runCLI(t, first, "delegate", "eeee0003", "ship"); again.status != 0 {
		t.Fatalf("the first store's vocabulary was clobbered: %q", again.stderr)
	}
}
