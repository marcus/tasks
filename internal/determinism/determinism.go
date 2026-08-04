// Package determinism turns harness pins into the injectable values the read
// path already accepts. It is the Go counterpart of lib/tasks/determinism.rb
// and exists for the porting conformance harness and for nothing else.
//
// The design rules are the Ruby module's, unchanged: every accessor reports
// "unset" rather than substituting a value of its own, and it configures
// nothing that is not already a seam.
package determinism

import (
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The pinned names, spelled exactly as lib/tasks/determinism.rb spells them.
const (
	NameNow               = "TASKS_PIN_NOW"
	NameIDs               = "TASKS_PIN_IDS"
	NameCoalesceScope     = "TASKS_PIN_COALESCE_SCOPE"
	NameDelegationKeys    = "TASKS_PIN_DELEGATION_KEYS"
	NameHostname          = "TASKS_PIN_HOSTNAME"
	NameLines             = "LINES"
	NameColumns           = "COLUMNS"
	NameTestTodaySequence = "TASKS_TEST_TODAY_SEQUENCE"
	NameDevice            = "TASKS_DEVICE"
	NameTZ                = "TZ"
	NameLang              = "LANG"
	NameLCAll             = "LC_ALL"
)

// Keys is Determinism::KEYS: every name `report` accounts for, in the Ruby
// module's own order.
var Keys = []string{
	NameNow, NameIDs, NameCoalesceScope, NameDelegationKeys, NameHostname,
	NameLines, NameColumns, NameTestTodaySequence, NameDevice, NameTZ,
	NameLang, NameLCAll,
}

// Env is a process environment. A name absent from the map is unset; a name
// present with an empty value is set-and-empty, which several pins treat
// differently from unset — TASKS_TEST_TODAY_SEQUENCE keys off presence alone,
// exactly as `if ENV[name]` does under Ruby's truthiness.
type Env map[string]string

// OSEnv snapshots the real environment.
func OSEnv() Env {
	env := Env{}
	for _, entry := range os.Environ() {
		if index := strings.IndexByte(entry, '='); index > 0 {
			env[entry[:index]] = entry[index+1:]
		}
	}
	return env
}

// Lookup reports the value and whether the name is set at all.
func (e Env) Lookup(name string) (string, bool) {
	value, present := e[name]
	return value, present
}

// Get is the `ENV[name].to_s` spelling: unset reads as the empty string.
func (e Env) Get(name string) string { return e[name] }

// Requested is the probe's `requested` helper: the raw value, or "" when the
// name is unset or blank. A pin nobody asked for is not a pin that was dropped.
func (e Env) Requested(name string) (string, bool) {
	raw, present := e[name]
	if !present || strings.TrimSpace(raw) == "" {
		return "", false
	}
	return raw, true
}

// isoInstant is Ruby's Time.xmlschema pattern (Time.iso8601 is its alias),
// including the case-insensitivity and the surrounding-whitespace tolerance.
var isoInstant = regexp.MustCompile(`\A\s*(-?\d+)-(\d\d)-(\d\d)[Tt](\d\d):(\d\d):(\d\d)(\.\d+)?([Zz]|[+-]\d\d(?::?\d\d)?)?\s*\z`)

// Now is the frozen UTC instant TASKS_PIN_NOW names, or ok=false when the pin
// is unset. An unparseable pin is an error rather than a silent fall back to
// the wall clock: a harness that fell back would produce a green run that
// proves nothing.
func Now(env Env) (time.Time, bool, error) {
	raw := strings.TrimSpace(env.Get(NameNow))
	if raw == "" {
		return time.Time{}, false, nil
	}
	instant, err := parseISO8601(raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("%s must be an ISO8601 instant with an offset, got %q", NameNow, raw)
	}
	return instant.UTC(), true, nil
}

// NowForAdapter resolves the whole clock precedence in one place:
// TASKS_PIN_NOW dominates, then TASKS_TEST_TODAY_SEQUENCE (noon UTC of the
// current date), then the wall clock. Presence, not non-emptiness, triggers
// step 2 — the Ruby seam it mirrors reads `if ENV[name]`.
func NowForAdapter(env Env) (time.Time, error) {
	pinned, ok, err := Now(env)
	if err != nil {
		return time.Time{}, err
	}
	if ok {
		return pinned, nil
	}
	if _, present := env.Lookup(NameTestTodaySequence); !present {
		return time.Now().UTC(), nil
	}
	today := time.Now()
	return time.Date(today.Year(), today.Month(), today.Day(), 12, 0, 0, 0, time.UTC), nil
}

// Clock is the pinned instant in the shape the store takes, or nil when unset.
func Clock(env Env) func() time.Time {
	instant, ok, err := Now(env)
	if err != nil || !ok {
		return nil
	}
	return func() time.Time { return instant }
}

// CoalesceScope is the journal's per-process coalescing scope, or ok=false.
func CoalesceScope(env Env) (string, bool) {
	value := strings.TrimSpace(env.Get(NameCoalesceScope))
	return value, value != ""
}

// Hostname is Config's host provider: never nil, and the unpinned provider is
// the one Config defaulted to before.
func Hostname(env Env) func() string {
	pinned := strings.TrimSpace(env.Get(NameHostname))
	if pinned == "" {
		return func() string {
			name, err := os.Hostname()
			if err != nil {
				return ""
			}
			return name
		}
	}
	return func() string { return pinned }
}

// Winsize is [rows, columns] when both are pinned to positive integers, so the
// caller falls back to querying a real terminal only when they are not.
func Winsize(env Env) (rows, columns int, ok bool) {
	rows, rowsOK := positiveInteger(env.Get(NameLines))
	columns, columnsOK := positiveInteger(env.Get(NameColumns))
	if rowsOK && columnsOK {
		return rows, columns, true
	}
	return 0, 0, false
}

func positiveInteger(value string) (int, bool) {
	// Ruby's Integer(value, 10) accepts a leading sign and underscores and
	// rejects everything else, including an empty string.
	number, err := strconv.Atoi(strings.ReplaceAll(strings.TrimSpace(value), "_", ""))
	if err != nil || number <= 0 {
		return 0, false
	}
	return number, true
}

// shared holds the process-wide mints. Ruby memoizes these inside the module
// for a reason that is not cosmetic: ONE invocation can build several stores
// (a CLI store and an application factory), and they must draw from one
// sequence rather than each restarting it at the first pinned token — two
// records would otherwise be minted the same id and the store's collision loop
// would silently renumber the second.
var shared struct {
	sync.Mutex
	idSpec         string
	idSource       *IDSequence
	delegationSpec string
	delegationKeys *IDSequence
}

// SharedIDSource is IDSource memoized per spec: the same env yields the same
// sequence, and changing the pin rebuilds it. Adapters that mint ids more than
// once in a process should use this rather than IDSource.
func SharedIDSource(env Env) (*IDSequence, error) {
	spec := strings.TrimSpace(env.Get(NameIDs))
	if spec == "" {
		return nil, nil
	}
	shared.Lock()
	defer shared.Unlock()
	if shared.idSource != nil && shared.idSpec == spec {
		return shared.idSource, nil
	}
	sequence, err := NewIDSequence(spec, 8, NameIDs)
	if err != nil {
		return nil, err
	}
	shared.idSpec, shared.idSource = spec, sequence
	return sequence, nil
}

// SharedDelegationKeySource is DelegationKeySource memoized per spec, for the
// same reason: one invocation may build more than one application.
func SharedDelegationKeySource(env Env) (*IDSequence, error) {
	spec := strings.TrimSpace(env.Get(NameDelegationKeys))
	if spec == "" {
		return nil, nil
	}
	shared.Lock()
	defer shared.Unlock()
	if shared.delegationKeys != nil && shared.delegationSpec == spec {
		return shared.delegationKeys, nil
	}
	sequence, err := NewIDSequence(spec, 16, NameDelegationKeys)
	if err != nil {
		return nil, err
	}
	shared.delegationSpec, shared.delegationKeys = spec, sequence
	return sequence, nil
}

// Reset drops the memoized sequences. Test-only, exactly as Determinism.reset!
// is: a test that pinned one spec must not leak its position into the next.
func Reset() {
	shared.Lock()
	defer shared.Unlock()
	shared.idSpec, shared.idSource = "", nil
	shared.delegationSpec, shared.delegationKeys = "", nil
}

// IDSource is a fresh id mint, or nil when TASKS_PIN_IDS is unset.
func IDSource(env Env) (*IDSequence, error) {
	spec := strings.TrimSpace(env.Get(NameIDs))
	if spec == "" {
		return nil, nil
	}
	return NewIDSequence(spec, 8, NameIDs)
}

// DelegationKeySource is the process-wide mint for delegation coalescing keys,
// or nil when TASKS_PIN_DELEGATION_KEYS is unset.
func DelegationKeySource(env Env) (*IDSequence, error) {
	spec := strings.TrimSpace(env.Get(NameDelegationKeys))
	if spec == "" {
		return nil, nil
	}
	return NewIDSequence(spec, 16, NameDelegationKeys)
}

// IDSequence is a monotonic, reproducible hex mint. Stateful on purpose: one
// invocation can perform several mutations and each must draw the next token.
type IDSequence struct {
	// A shared sequence can be drawn from more than one goroutine (the store
	// and the journal both mint), and the draw mutates the queue and counter.
	mu      sync.Mutex
	width   int
	name    string
	queue   []string
	counter *big.Int
	span    *big.Int
}

var hexToken = regexp.MustCompile(`\A[0-9a-f]+\z`)

// NewIDSequence parses a pin spec: comma-separated tokens, each `width` hex
// characters or the literal "seq".
func NewIDSequence(spec string, width int, name string) (*IDSequence, error) {
	queue := []string{}
	// Ruby's String#split drops TRAILING empty fields, so ",," yields no tokens
	// while "a,,b" yields three. Mirrored so an empty spec reports "is empty"
	// rather than a token diagnostic about a field Ruby never produced.
	fields := strings.Split(spec, ",")
	for len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	for _, raw := range fields {
		token := strings.ToLower(strings.TrimSpace(raw))
		if token == "seq" {
			token = strings.Repeat("0", width)
		}
		if len(token) != width || !hexToken.MatchString(token) {
			return nil, fmt.Errorf("%s token must be %d hex characters or \"seq\", got %q", name, width, raw)
		}
		queue = append(queue, token)
	}

	if len(queue) == 0 {
		return nil, fmt.Errorf("%s is empty", name)
	}
	span := new(big.Int).Exp(big.NewInt(16), big.NewInt(int64(width)), nil)
	return &IDSequence{width: width, name: name, queue: queue, span: span}, nil
}

// Width is the token length in hex characters.
func (s *IDSequence) Width() int { return s.width }

// Call draws the next token: the queued ones in order, then the last one
// incremented as a counter of the same width.
func (s *IDSequence) Call() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) > 0 {
		token := s.queue[0]
		s.queue = s.queue[1:]
		counter, _ := new(big.Int).SetString(token, 16)
		s.counter = counter
		return token
	}
	s.counter = new(big.Int).Mod(new(big.Int).Add(s.counter, big.NewInt(1)), s.span)
	return fmt.Sprintf("%0*s", s.width, s.counter.Text(16))
}

func parseISO8601(raw string) (time.Time, error) {
	match := isoInstant.FindStringSubmatch(raw)
	if match == nil {
		return time.Time{}, fmt.Errorf("not an ISO8601 instant")
	}
	year, err := strconv.Atoi(match[1])
	if err != nil {
		return time.Time{}, err
	}
	month, _ := strconv.Atoi(match[2])
	day, _ := strconv.Atoi(match[3])
	hour, _ := strconv.Atoi(match[4])
	minute, _ := strconv.Atoi(match[5])
	second, _ := strconv.Atoi(match[6])
	nanos := 0
	if match[7] != "" {
		fraction := match[7][1:]
		for len(fraction) < 9 {
			fraction += "0"
		}
		nanos, _ = strconv.Atoi(fraction[:9])
	}
	location := time.Local
	switch offset := match[8]; {
	case offset == "":
		// No offset: Ruby's Time.xmlschema falls back to local time.
	case offset == "Z" || offset == "z":
		location = time.UTC
	default:
		sign := 1
		if offset[0] == '-' {
			sign = -1
		}
		digits := strings.ReplaceAll(offset[1:], ":", "")
		hours, _ := strconv.Atoi(digits[:2])
		minutes := 0
		if len(digits) > 2 {
			minutes, _ = strconv.Atoi(digits[2:])
		}
		location = time.FixedZone("", sign*(hours*3600+minutes*60))
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, nanos, location), nil
}
