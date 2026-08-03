// Command tasks-probe is the Go implementation's self-report for the porting
// conformance runner.
//
//	tasks-probe <copy-root>
//
// It prints ONE JSON object on stdout and exits 0 — the same object
// porting/runners/ruby/probe prints from the same inputs. The object's shape
// is the contract; the language behind it is not. See
// porting/runners/README.md § "The probe".
//
// It exists because two things an observation must record cannot be read off
// the CLI's output:
//
//  1. Revision tokens. No CLI command prints s1. / v1. tokens. Deriving them
//     inside the harness would mean re-implementing the product's revision
//     algorithm there — and then the harness could not detect a port that got
//     that algorithm wrong, which is one of the seeded-mismatch classes the
//     Phase 1 gate tests. So the probe asks the implementation.
//
//  2. Which pins were actually applied. A pin that was set and silently
//     ignored produces a run that looks reproducible and is not. Only the
//     implementation knows what it resolved.
//
// The probe is READ-ONLY with respect to the store's bytes, but it is not
// side-effect free: reading a snapshot takes the store lock, which creates
// .tasks.jsonl.lock when it is absent. The runner therefore runs it only AFTER
// it has captured the post-invocation tree.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"tasks-go/internal/config"
	"tasks-go/internal/determinism"
	"tasks-go/internal/journal"
	"tasks-go/internal/store"
	"tasks-go/internal/timezones"
	"tasks-go/internal/updatestamp"
)

const probeVersion = 1

// localeEncoding is this implementation's default external encoding. Go's
// strings are UTF-8 and there is no per-process override, so the honest
// self-report is a constant — the Ruby oracle reports the resolved encoding
// name for the same reason, not the raw LANG string it was handed.
const localeEncoding = "UTF-8"

type report struct {
	ProbeVersion int         `json:"probe_version"`
	Revisions    revisions   `json:"revisions"`
	Paths        pathsReport `json:"paths"`
	Pins         []pin       `json:"pins"`
	Environment  environment `json:"environment"`
}

type revisions struct {
	Status    string           `json:"status"`
	Store     *string          `json:"store"`
	Resources []store.Resource `json:"resources"`
	Error     string           `json:"error,omitempty"`
}

type pathsReport struct {
	Store            *string `json:"store"`
	StoreCanonical   *string `json:"store_canonical"`
	Archive          *string `json:"archive"`
	ArchiveCanonical *string `json:"archive_canonical"`
	Memory           *string `json:"memory"`
	Config           *string `json:"config"`
	JournalDir       *string `json:"journal_dir"`
}

type pin struct {
	Name    string  `json:"name"`
	Applied bool    `json:"applied"`
	Value   *string `json:"value"`
}

type environment struct {
	TZDBVersion string `json:"tzdb_version"`
	Platform    string `json:"platform"`
	Locale      string `json:"locale"`
	Runtime     string `json:"runtime"`
}

func main() {
	if len(os.Args) < 2 || os.Args[1] == "" {
		fmt.Fprintln(os.Stderr, "usage: probe <copy-root>")
		os.Exit(1)
	}
	copyRoot := os.Args[1]
	env := determinism.OSEnv()

	// Resolve exactly as the CLI does, from the same environment the
	// invocation ran under: anything else would report tokens for a different
	// store than the one under test.
	paths := config.Resolve(copyRoot, env, nil)

	out := report{
		ProbeVersion: probeVersion,
		Revisions:    reportRevisions(paths),
		Paths:        reportPaths(paths, env),
		Pins:         reportPins(paths, env),
		Environment: environment{
			TZDBVersion: timezones.TZDBVersion(),
			Platform:    runtime.GOARCH + "-" + runtime.GOOS,
			Locale:      localeEncoding,
			Runtime:     "go " + runtime.Version(),
		},
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// reportRevisions asks the store for the API-grade read: the content-derived
// store revision for the exact bytes on disk even when those bytes are
// invalid, and per-task revisions only when they are not. A malformed fixture
// therefore still yields a store token, which is the honest answer — the token
// is a digest of bytes, not an assertion that they parse.
func reportRevisions(paths config.Paths) revisions {
	checked, err := store.New(paths.Org, paths.Archive).CheckedReadSnapshot()
	if err != nil {
		// A probe that cannot read the store says so; it never guesses a token.
		return revisions{Status: "probe_error", Resources: []store.Resource{}, Error: err.Error()}
	}
	resources := []store.Resource{}
	if checked.Snapshot != nil {
		resources = checked.Snapshot.Resources()
	}
	out := revisions{Status: string(checked.Status), Resources: resources}
	if checked.StoreRevision != "" {
		out.Store = &checked.StoreRevision
	}
	return out
}

// reportPaths is the paths the implementation ACTUALLY resolved, so the runner
// can assign files[].role from them instead of from a table of hardcoded
// filenames. Both spellings are reported because both can appear in the tree:
// a symlinked store has a `configured` path (the link) and a `canonical` one
// (the bytes), and a harness that knew only the first would record the file
// carrying the store's bytes as role "other".
func reportPaths(paths config.Paths, env determinism.Env) pathsReport {
	out := pathsReport{
		Store:            optional(paths.Org),
		StoreCanonical:   canonical(paths.Org),
		Archive:          optional(paths.Archive),
		ArchiveCanonical: canonical(paths.Archive),
		Memory:           optional(paths.Memory),
		Config:           optional(paths.ConfigFile),
	}
	if paths.Org != "" {
		out.JournalDir = optional(journal.DirFor(paths.Org, env))
	}
	return out
}

func canonical(path string) *string {
	if path == "" {
		return nil
	}
	return optional(journal.Canonical(path))
}

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// reportPins answers two questions per input: did the implementation USE this,
// and what did it RESOLVE it to. Reporting the resolved value is what catches
// two implementations parsing one pin differently, and `applied: false`
// alongside a non-null request is what catches a pin that was accepted and
// dropped. The runner treats that combination as a hard failure.
func reportPins(paths config.Paths, env determinism.Env) []pin {
	pins := []pin{}
	add := func(name string, applied bool, value *string) {
		pins = append(pins, pin{Name: name, Applied: applied, Value: value})
	}

	nowRequest, nowRequested := env.Requested(determinism.NameNow)
	if instant, ok, err := determinism.Now(env); err != nil {
		// Malformed and therefore NOT applied — reported rather than raised, so
		// the runner can fail the case with the requested value in evidence.
		add(determinism.NameNow, false, requestValue(nowRequest, nowRequested))
	} else if ok {
		add(determinism.NameNow, true, optional(instant.Format("2006-01-02T15:04:05Z")))
	} else {
		add(determinism.NameNow, false, nil)
	}

	idsRequest, idsRequested := env.Requested(determinism.NameIDs)
	sequence, idsErr := determinism.IDSource(env)
	add(determinism.NameIDs, idsErr == nil && sequence != nil, requestValue(idsRequest, idsRequested))

	scope, scoped := determinism.CoalesceScope(env)
	add(determinism.NameCoalesceScope, scoped, requestValue(scope, scoped))

	delegationRequest, delegationRequested := env.Requested(determinism.NameDelegationKeys)
	delegationSource, delegationErr := determinism.DelegationKeySource(env)
	add(determinism.NameDelegationKeys, delegationErr == nil && delegationSource != nil,
		requestValue(delegationRequest, delegationRequested))

	// `applied` for the hostname pin is a CALL-SITE check, not a module check.
	// There are two hostname consumers — Config's host-context selection and
	// the device half of the update stamp — and a pin that reaches only one of
	// them is the exact failure `applied` exists to catch. So each consumer is
	// asked what it resolved, and the pin counts as applied only when both
	// agree with it. TASKS_DEVICE legitimately out-ranks the hostname for the
	// device slug, so that consumer is only interrogated when no override is set.
	hostnameRequest, hostnameRequested := env.Requested(determinism.NameHostname)
	resolvedHostname := determinism.Hostname(env)()
	_, deviceOverridden := env.Requested(determinism.NameDevice)
	deviceSlug := updatestamp.Device(env)
	hostnameAgrees := hostnameRequested &&
		paths.Hostname == hostnameRequest &&
		resolvedHostname == hostnameRequest &&
		(deviceOverridden || deviceSlug == updatestamp.Slug(hostnameRequest))
	add(determinism.NameHostname, hostnameAgrees, requestValue(resolvedHostname, hostnameRequested))

	// The test-only clock seam, reported because it is a clock input. The
	// reported value is the instant the whole clock precedence produced, so an
	// implementation that got the precedence backwards reports a different one.
	//
	// Being out-ranked by TASKS_PIN_NOW counts as APPLIED, not as a dropped
	// pin — the same rule TASKS_DEVICE gets against the hostname pin above.
	_, sequenceRequested := env.Requested(determinism.NameTestTodaySequence)
	var sequenceResolved *string
	if sequenceRequested {
		if instant, err := determinism.NowForAdapter(env); err == nil {
			sequenceResolved = optional(instant.UTC().Format("2006-01-02T15:04:05Z"))
		}
	}
	add(determinism.NameTestTodaySequence, sequenceRequested && sequenceResolved != nil, sequenceResolved)

	rows, columns, sized := determinism.Winsize(env)
	add(determinism.NameLines, sized, sizeValue(rows, sized))
	add(determinism.NameColumns, sized, sizeValue(columns, sized))

	_, deviceRequested := env.Requested(determinism.NameDevice)
	add(determinism.NameDevice, deviceRequested, requestValue(deviceSlug, deviceRequested))

	// TZ resolves through Config, so `applied` is the strict test: the pin
	// counts as applied only when the zone the implementation ended up with is
	// the zone that was asked for. A silent fallback reports applied: false.
	tzRequest, tzRequested := env.Requested(determinism.NameTZ)
	add(determinism.NameTZ, tzRequested && paths.Timezone == tzRequest,
		requestValue(paths.Timezone, tzRequested))

	// TASKS_TIMEZONE is a product setting rather than one of Determinism's
	// keys, but the harness pins it (it out-ranks TZ), so it is reported here
	// too: a pin the runner sets and nobody reports is exactly the blind spot
	// `applied` exists to close.
	timezoneRequest, timezoneRequested := env.Requested("TASKS_TIMEZONE")
	add("TASKS_TIMEZONE", timezoneRequested && paths.Timezone == timezoneRequest,
		requestValue(paths.Timezone, timezoneRequested))

	// LANG/LC_ALL are applied when the process's default external encoding is
	// the encoding they name; that is the only effect they have here.
	for _, name := range []string{determinism.NameLang, determinism.NameLCAll} {
		request, requested := env.Requested(name)
		applied := requested && sameEncoding(request, localeEncoding)
		add(name, applied, requestValue(localeEncoding, requested))
	}

	// Colour. Four environment names and no more — read off the source rather
	// than taken from the conventional list. NO_COLOR and TASKS_THEME reach the
	// theme resolution (TASKS_THEME out-ranks NO_COLOR, and the resolved answer
	// is paths.Theme); COLORTERM and TERM reach the TUI only, which the CLI
	// surface does not run. CLICOLOR / CLICOLOR_FORCE are read NOWHERE and are
	// deliberately absent.
	//
	// `applied` for both theme inputs is answered from the implementation's own
	// precedence report (Config records WHICH source decided the theme), not
	// from a re-derivation here. NO_COLOR is legitimately out-ranked by
	// TASKS_THEME and by a config-file `theme` key, so being overridden counts
	// as applied rather than as a dropped pin.
	themeSource := paths.Sources["theme"]
	_, themeRequested := env.Requested("TASKS_THEME")
	add("TASKS_THEME", themeRequested && themeSource == "TASKS_THEME env",
		requestValue(paths.Theme, themeRequested))

	_, noColorRequested := env.Requested("NO_COLOR")
	overridden := themeSource == "NO_COLOR env" || themeSource == "TASKS_THEME env" || themeSource == "config file"
	add("NO_COLOR", noColorRequested && overridden, requestValue(paths.Theme, noColorRequested))

	for _, name := range []string{"TERM", "COLORTERM"} {
		request, requested := env.Requested(name)
		add(name, requested, requestValue(request, requested))
	}

	sort.SliceStable(pins, func(left, right int) bool { return pins[left].Name < pins[right].Name })
	return pins
}

// requestValue is the Ruby idiom `request && resolved`: null when nothing was
// asked for, and the RESOLVED value when something was.
func requestValue(resolved string, requested bool) *string {
	if !requested {
		return nil
	}
	return &resolved
}

func sizeValue(size int, ok bool) *string {
	if !ok {
		return nil
	}
	return optional(strconv.Itoa(size))
}

// sameEncoding compares a locale's encoding half against an encoding name the
// way Ruby's Encoding comparison does: case-insensitively and ignoring the
// hyphens that distinguish "UTF-8" from "utf8".
func sameEncoding(locale, encoding string) bool {
	_, half, found := strings.Cut(locale, ".")
	if !found {
		half = locale
	}
	normalize := func(value string) string {
		return strings.ToLower(strings.ReplaceAll(value, "-", ""))
	}
	return normalize(half) == normalize(encoding)
}
