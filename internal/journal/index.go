package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"unicode/utf8"
)

// Version is the journal's own format version. A bump invalidates the whole
// history rather than replaying states this build cannot interpret.
const Version = 1

// State is one point in the timeline: the content addresses of both files at
// that moment, plus the label of the mutation that produced it. states[0] is
// the baseline and carries no label.
type State struct {
	OrgSHA        *string `json:"org_sha"`
	ArchiveSHA    *string `json:"archive_sha"`
	Label         *string `json:"label,omitempty"`
	Repair        *bool   `json:"repair,omitempty"`
	CoalesceKey   *string `json:"coalesce_key,omitempty"`
	CoalesceScope *string `json:"coalesce_scope,omitempty"`
}

// Index is the whole timeline plus the cursor into it — the state that matches
// the live files right now.
type Index struct {
	Cursor int
	States []State
}

// Snapshot is the bytes of both files at one moment. A nil half means that file
// is absent, which is a real state: an archive that does not exist yet is not
// the same as an empty one.
type Snapshot struct {
	Org     *string
	Archive *string
}

// Equal compares two snapshots including absence, which is what the undo
// conflict gate turns on.
func (s Snapshot) Equal(other Snapshot) bool {
	return equalOptional(s.Org, other.Org) && equalOptional(s.Archive, other.Archive)
}

func equalOptional(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

// Step is a planned undo or redo: the label of the mutation being reverted or
// replayed, the snapshot the live files must currently match, and the snapshot
// to move to.
type Step struct {
	Label  string
	Repair bool
	Expect Snapshot
	Target Snapshot
	To     int

	// from and states are the index EXACTLY as Plan read it: the cursor the
	// step moves away from, and the timeline that cursor indexes. Rollback
	// restores them, and it can only do that from the values captured at plan
	// time — re-reading would read back whatever the failed commit left.
	from   int
	states []State
}

// Journal is the durable undo history: the index, the blobs, the plan a caller
// would apply, and — once Writable has supplied the two values a writer needs —
// the record, commit and interning half in write.go.
//
// The journal is convenience state, not the source of truth: tasks.jsonl is.
// Every method runs under the store's file lock and index.json is replaced
// atomically, so concurrent processes never corrupt it. A crash in the narrow
// window between rewriting the files and committing the cursor can leave the
// cursor one step stale; that degrades to a refused undo, never to a mangled
// task file.
type Journal struct {
	dir           string
	org           string
	limit         int
	coalesceScope string
}

// Open builds the read view over one store's history. `org` is canonicalized so
// two spellings of the same file resolve to one history rather than silently
// diverging.
func Open(dir, org string) *Journal {
	return &Journal{dir: dir, org: Canonical(org)}
}

var shaPattern = regexp.MustCompile(`\A[0-9a-f]{64}\z`)

// Load reads the index, degrading to an EMPTY history for every kind of
// trouble: missing, unreadable, wrong version, someone else's org, a cursor out
// of range. That is the contract the whole journal is built on — history is
// convenience state, and journal trouble degrades to "nothing to undo", never
// to a crash or to replaying a history that is not yours.
func (j *Journal) Load() Index {
	blank := Index{Cursor: 0, States: []State{}}
	if !regularFile(j.indexPath()) {
		return blank
	}
	raw, err := os.ReadFile(j.indexPath())
	if err != nil || !utf8.Valid(raw) {
		return blank
	}
	var document struct {
		Version *int    `json:"version"`
		Org     *string `json:"org"`
		Cursor  *int    `json:"cursor"`
		States  []State `json:"states"`
	}
	if json.Unmarshal(raw, &document) != nil {
		return blank
	}
	// A key collision (a different org sharing a 16-hex prefix) or a format bump
	// invalidates the whole history rather than replaying someone else's.
	if document.Version == nil || *document.Version != Version {
		return blank
	}
	if document.Org == nil || *document.Org != j.org {
		return blank
	}
	// Never trust an out-of-range cursor from a corrupted or hand-edited index:
	// indexing states with it would crash every undo AND every later mutation.
	if document.States == nil || document.Cursor == nil ||
		*document.Cursor < 0 || *document.Cursor > len(document.States)-1 {
		return blank
	}
	for _, state := range document.States {
		if !validState(state) {
			return blank
		}
	}
	return Index{Cursor: *document.Cursor, States: document.States}
}

func validState(state State) bool {
	for _, sha := range []*string{state.OrgSHA, state.ArchiveSHA} {
		if sha != nil && !shaPattern.MatchString(*sha) {
			return false
		}
	}
	return true
}

// Plan describes an undo (delta -1) or redo (delta +1) without performing it.
// A missing step, an unreadable blob, or a blob whose bytes no longer hash to
// its name all degrade to ok=false — nothing to undo — upholding the contract
// that journal trouble never crashes a command.
func (j *Journal) Plan(delta int) (Step, bool) {
	index := j.Load()
	from := index.Cursor
	to := from + delta
	if to < 0 || to > len(index.States)-1 {
		return Step{}, false
	}
	// The label lives on the higher-indexed of the two states — the mutation
	// that sits BETWEEN them, which undo reverts and redo replays.
	tipIndex := from
	if to > from {
		tipIndex = to
	}
	tip := index.States[tipIndex]
	expect, expectOK := j.content(index.States[from])
	target, targetOK := j.content(index.States[to])
	if !expectOK || !targetOK {
		return Step{}, false
	}
	label := ""
	if tip.Label != nil {
		label = *tip.Label
	}
	return Step{
		Label:  label,
		Repair: tip.Repair != nil && *tip.Repair,
		Expect: expect,
		Target: target,
		To:     to,
		from:   from,
		states: index.States,
	}, true
}

// equalIndex compares two loaded indexes by value, which is what Rollback's
// "has anything actually landed?" question needs. States hold pointers, so a
// shallow == would compare addresses and answer "changed" every time.
func equalIndex(left, right Index) bool {
	if left.Cursor != right.Cursor || len(left.States) != len(right.States) {
		return false
	}
	for position := range left.States {
		if !equalState(left.States[position], right.States[position]) {
			return false
		}
	}
	return true
}

func equalState(left, right State) bool {
	if !equalOptional(left.OrgSHA, right.OrgSHA) ||
		!equalOptional(left.ArchiveSHA, right.ArchiveSHA) ||
		!equalOptional(left.Label, right.Label) ||
		!equalOptional(left.CoalesceKey, right.CoalesceKey) ||
		!equalOptional(left.CoalesceScope, right.CoalesceScope) {
		return false
	}
	if (left.Repair == nil) != (right.Repair == nil) {
		return false
	}
	return left.Repair == nil || *left.Repair == *right.Repair
}

func (j *Journal) content(state State) (Snapshot, bool) {
	org, orgOK := j.read(state.OrgSHA)
	archive, archiveOK := j.read(state.ArchiveSHA)
	if !orgOK || !archiveOK {
		return Snapshot{}, false
	}
	return Snapshot{Org: org, Archive: archive}, true
}

// read fetches one blob and verifies it. A blob whose bytes do not hash to its
// own name is corrupt, and a corrupt blob invalidates the plan rather than
// being restored: writing unverified bytes over a task file is the one outcome
// worse than refusing to undo.
func (j *Journal) read(sha *string) (*string, bool) {
	if sha == nil {
		return nil, true
	}
	path := filepath.Join(j.dir, "blobs", *sha)
	if !regularFile(path) {
		return nil, false
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	digest := sha256.Sum256(bytes)
	if hex.EncodeToString(digest[:]) != *sha {
		return nil, false
	}
	if !utf8.Valid(bytes) {
		return nil, false
	}
	text := string(bytes)
	return &text, true
}

func (j *Journal) indexPath() string { return filepath.Join(j.dir, "index.json") }

// regularFile is File.lstat(path).file?: a symlink or a directory at the path
// is not a journal file, and is never followed.
func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}
