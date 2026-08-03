package journal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"tasks-go/internal/atomic"
	"tasks-go/internal/record"
)

// Limit is Store::UNDO_LIMIT: the deepest undo history the journal retains.
const Limit = 50

// Writable turns the read view into the full journal by supplying the two
// values only a writer needs: how deep the history may get, and the coalescing
// scope this process owns.
//
// The scope is deliberately local to one owner. Persisting it on a keyed tip is
// what stops an unrelated process from extending another process's coalesced
// step — the tip records who was allowed to keep appending to it.
func (j *Journal) Writable(limit int, coalesceScope string) *Journal {
	j.limit = limit
	j.coalesceScope = coalesceScope
	return j
}

// Record persists a completed mutation. `before` and `after` are the bytes of
// both files at each moment, a nil half meaning that file is absent. It drops
// any redo tail, appends or safely replaces the keyed tip, and caps history at
// `limit` undo steps.
//
// If `before` does not match the recorded tip, an out-of-band edit slipped in
// since the last record: the stale chain can no longer be safely replayed —
// undoing across it would clobber that edit — so it is discarded and a fresh
// baseline starts at `before`.
//
// It reports success, and a failure is not an error the caller propagates.
// History is convenience state: an unreadable or non-repairable journal must
// never roll back a task mutation that was already durably written.
func (j *Journal) Record(label string, before, after Snapshot, coalesceKey string, repair bool) bool {
	if err := ensureDirectory(j.blobsDir()); err != nil {
		return false
	}
	index := j.Load()

	tipMatches := len(index.States) > 0 && j.stateMatchesSnapshot(index.States[index.Cursor], before)
	beforeState, ok := j.intern(before)
	if !ok {
		return false
	}
	atTip := tipMatches && index.Cursor == len(index.States)-1

	coalesce := false
	if coalesceKey != "" && atTip && index.Cursor > 0 {
		tip := index.States[index.Cursor]
		coalesce = tip.CoalesceKey != nil && *tip.CoalesceKey == coalesceKey &&
			tip.CoalesceScope != nil && *tip.CoalesceScope == j.coalesceScope &&
			j.stateBlobsValid(index.States[index.Cursor-1])
	}

	var states []State
	cursor := 0
	if tipMatches {
		states = append(states, index.States[:index.Cursor+1]...)
		cursor = index.Cursor
	} else {
		states = []State{beforeState}
	}

	state, ok := j.intern(after)
	if !ok {
		return false
	}
	state.Label = &label
	if repair {
		yes := true
		state.Repair = &yes
	}
	if coalesceKey != "" {
		key, scope := coalesceKey, j.coalesceScope
		state.CoalesceKey = &key
		state.CoalesceScope = &scope
	}

	if coalesce {
		// A coalesced follow-up replaces the step's content but keeps the same
		// before-state, so a repair exemption must survive the overwrite —
		// otherwise undoing the coalesced step wrongly refuses to restore the
		// malformed bytes the user deliberately asked to fix.
		if states[cursor].Repair != nil && *states[cursor].Repair {
			yes := true
			state.Repair = &yes
		}
		states[cursor] = state
	} else {
		states = append(states, state)
		cursor++
	}

	if len(states) > j.limit+1 {
		drop := len(states) - (j.limit + 1)
		states = states[drop:]
		cursor -= drop
	}
	return j.persist(cursor, states, true)
}

// Barrier establishes a schema barrier: the current files become the only
// journal baseline, so an ordinary undo can never restore an older schema.
func (j *Journal) Barrier(snapshot Snapshot) bool {
	if err := ensureDirectory(j.blobsDir()); err != nil {
		return false
	}
	state, ok := j.intern(snapshot)
	if !ok {
		return false
	}
	return j.persist(0, []State{state}, true)
}

// Commit moves the cursor after the caller has rewritten the files for an undo
// or a redo. Undo and redo are explicit history boundaries, so all segment
// metadata is stripped: even a redo back to the exact former tip must not
// resume coalescing with the editor session that preceded the history move.
func (j *Journal) Commit(to int) bool {
	index := j.Load()
	states := make([]State, len(index.States))
	for position, state := range index.States {
		state.CoalesceKey = nil
		state.CoalesceScope = nil
		states[position] = state
	}
	return j.persist(to, states, false)
}

// persist replaces index.json atomically. `gc` is asked for only when the state
// set may have SHRUNK — a Record that capped the history or dropped a redo tail
// — because an undo or redo commit leaves `states` untouched and scanning the
// blob directory for it would be pure waste on the hot path.
func (j *Journal) persist(cursor int, states []State, gc bool) bool {
	if err := ensureDirectory(j.blobsDir()); err != nil {
		return false
	}
	discardNonRegular(j.indexPath())
	if err := atomic.Write(j.indexPath(), j.renderIndex(cursor, states)); err != nil {
		return false
	}
	if gc {
		j.collect(states)
	}
	return true
}

// renderIndex is JSON.pretty_generate over the index hash: two-space indent, a
// space after each colon, one member per line. These bytes are digested by the
// conformance harness, so the whitespace is contract and encoding/json — which
// spells its own indentation and escapes — cannot produce it.
func (j *Journal) renderIndex(cursor int, states []State) string {
	var out bytes.Buffer
	out.WriteString("{\n  \"version\": ")
	out.WriteString(itoa(Version))
	out.WriteString(",\n  \"org\": ")
	record.EncodeString(&out, j.org)
	out.WriteString(",\n  \"cursor\": ")
	out.WriteString(itoa(cursor))
	out.WriteString(",\n  \"states\": ")
	// JSON.pretty_generate emits no trailing newline, and these bytes are
	// digested: one extra byte is a mismatch.
	if len(states) == 0 {
		out.WriteString("[]\n}")
		return out.String()
	}
	out.WriteString("[\n")
	for index, state := range states {
		if index > 0 {
			out.WriteString(",\n")
		}
		out.WriteString("    {\n")
		members := [][2]string{}
		members = append(members, [2]string{"org_sha", jsonStringOrNull(state.OrgSHA)})
		members = append(members, [2]string{"archive_sha", jsonStringOrNull(state.ArchiveSHA)})
		// `compact`'s rule: an absent label, a false repair and an unkeyed step
		// are omitted, not written as null.
		if state.Label != nil && *state.Label != "" {
			members = append(members, [2]string{"label", quote(*state.Label)})
		}
		if state.Repair != nil && *state.Repair {
			members = append(members, [2]string{"repair", "true"})
		}
		if state.CoalesceKey != nil && *state.CoalesceKey != "" {
			members = append(members, [2]string{"coalesce_key", quote(*state.CoalesceKey)})
		}
		if state.CoalesceScope != nil && *state.CoalesceScope != "" {
			members = append(members, [2]string{"coalesce_scope", quote(*state.CoalesceScope)})
		}
		for position, member := range members {
			if position > 0 {
				out.WriteString(",\n")
			}
			out.WriteString("      ")
			out.WriteString(quote(member[0]))
			out.WriteString(": ")
			out.WriteString(member[1])
		}
		out.WriteString("\n    }")
	}
	out.WriteString("\n  ]\n}")
	return out.String()
}

// intern stores both files' contents as content-addressed blobs and returns
// their digests. An ordinary edit therefore writes one new blob: the untouched
// archive deduplicates to the digest already on disk.
func (j *Journal) intern(snapshot Snapshot) (State, bool) {
	org, ok := j.put(snapshot.Org)
	if !ok {
		return State{}, false
	}
	archive, ok := j.put(snapshot.Archive)
	if !ok {
		return State{}, false
	}
	return State{OrgSHA: org, ArchiveSHA: archive}, true
}

func (j *Journal) put(text *string) (*string, bool) {
	if text == nil {
		return nil, true
	}
	digest := sha256.Sum256([]byte(*text))
	sha := hex.EncodeToString(digest[:])
	path := j.blobPath(sha)
	// Content-addressed: identical content already on disk needs no rewrite.
	if regularBlobMatches(path, sha, *text) {
		return &sha, true
	}
	// Repair a tampered or truncated blob before a new baseline references it.
	// Symlinks and other special files are unlinked, never followed.
	discardNonRegular(path)
	if err := atomic.Write(path, *text); err != nil {
		return nil, false
	}
	return &sha, true
}

func (j *Journal) stateMatchesSnapshot(state State, snapshot Snapshot) bool {
	for _, pair := range []struct {
		expected *string
		sha      *string
	}{{snapshot.Org, state.OrgSHA}, {snapshot.Archive, state.ArchiveSHA}} {
		if pair.expected == nil {
			if pair.sha != nil {
				return false
			}
			continue
		}
		if pair.sha == nil {
			return false
		}
		bytes, ok := j.blobBytes(*pair.sha)
		if !ok || string(bytes) != *pair.expected {
			return false
		}
	}
	return true
}

func (j *Journal) stateBlobsValid(state State) bool {
	for _, sha := range []*string{state.OrgSHA, state.ArchiveSHA} {
		if sha == nil {
			continue
		}
		bytes, ok := j.blobBytes(*sha)
		if !ok {
			return false
		}
		digest := sha256.Sum256(bytes)
		if hex.EncodeToString(digest[:]) != *sha {
			return false
		}
	}
	return true
}

func (j *Journal) blobBytes(sha string) ([]byte, bool) {
	path := j.blobPath(sha)
	if !regularFile(path) {
		return nil, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// collect deletes blobs no live state references — the ones freed by capping
// the history or dropping a redo tail. Best-effort: a leaked blob wastes a
// little disk, never breaks undo, and must not affect task durability.
func (j *Journal) collect(states []State) {
	keep := map[string]bool{}
	for _, state := range states {
		for _, sha := range []*string{state.OrgSHA, state.ArchiveSHA} {
			if sha != nil {
				keep[*sha] = true
			}
		}
	}
	entries, err := os.ReadDir(j.blobsDir())
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		if !keep[name] {
			_ = os.Remove(j.blobPath(name))
		}
	}
}

func (j *Journal) blobsDir() string           { return filepath.Join(j.dir, "blobs") }
func (j *Journal) blobPath(sha string) string { return filepath.Join(j.blobsDir(), sha) }

func regularBlobMatches(path, sha, text string) bool {
	if !regularFile(path) {
		return false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if string(raw) != text {
		return false
	}
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:]) == sha
}

// ensureDirectory is Journal#ensure_directory: a non-directory sitting at the
// path is removed rather than worked around, because the alternative is a
// journal that silently stops recording.
func ensureDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return os.MkdirAll(path, 0o755)
	}
	if info.IsDir() {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return os.Mkdir(path, 0o755)
}

// discardNonRegular removes a symlink, directory or special file squatting
// where a journal file belongs. It never follows the link.
func discardNonRegular(path string) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode().IsRegular() {
		return
	}
	if info.IsDir() {
		_ = os.Remove(path)
		return
	}
	_ = os.Remove(path)
}

func jsonStringOrNull(value *string) string {
	if value == nil {
		return "null"
	}
	return quote(*value)
}

func quote(value string) string {
	var out bytes.Buffer
	record.EncodeString(&out, value)
	return out.String()
}

func itoa(value int) string { return strconv.Itoa(value) }
