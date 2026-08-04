package store

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

// RepairKind is the machine-readable discriminator for a defect repair knows.
type RepairKind string

// The two known members of the class.
const (
	// RepairMintedID is a record with no id. The targeted repair mints one, but
	// only for the record it was asked about.
	RepairMintedID RepairKind = "minted_id"
	// RepairDroppedTemporalKeys is an unknown key inside `scheduled_time` or
	// `deadline_time` — the drop the format calls "the repair path, not data
	// loss", made explicit here so the in-memory re-check sees it and the
	// report can enumerate it.
	RepairDroppedTemporalKeys RepairKind = "dropped_temporal_keys"
)

// RepairFix is one defect repair knows how to converge, named by the file and
// the physical line it sits on.
//
// Message deliberately restates the DEFECT in Check's own wording rather than
// the action taken: the report then reads as a line-for-line answer to what
// `tasks check` just printed, and it means the same thing whether the pass
// wrote or refused. ID carries the minted id, and only a written pass should
// show it — a plan that is never written mints an id nothing will ever hold.
type RepairFix struct {
	File    string
	Line    int
	Kind    RepairKind
	Message string
	ID      string
}

// RepairBlocker is a defect repair does NOT know how to converge. Reported so
// the caller learns what still blocks the store rather than only that repair
// refused.
type RepairBlocker struct {
	File    string
	Line    int
	Message string
}

// RepairStatus is the outcome vocabulary of one pass.
type RepairStatus string

// The statuses.
const (
	// RepairOK means the store validates now, or would under a dry run.
	RepairOK RepairStatus = "ok"
	// RepairUnrepairable means at least one blocker was left and NOTHING was
	// written.
	RepairUnrepairable RepairStatus = "unrepairable"
	// RepairUnsupportedSchema is the version gate.
	RepairUnsupportedSchema RepairStatus = "unsupported_schema"
)

// RepairResult is the outcome of one Repair pass. Written distinguishes a pass
// that changed the files from a clean store and from a dry run, so a caller
// never has to infer it from Fixes.
type RepairResult struct {
	Status   RepairStatus
	Fixes    []RepairFix
	Blockers []RepairBlocker
	Written  bool
	DryRun   bool
}

// OK reports a pass that left the store valid.
func (r RepairResult) OK() bool { return r.Status == RepairOK }

// temporalRepairKeys is the set a temporal object may carry. Deliberately the
// same set Check subtracts and the format's nested key order declares: a repair
// that dropped a different set than Check refuses would either fail to converge
// or destroy a field Check was happy with.
var temporalRepairKeys = map[string]bool{"local": true, "timezone": true, "fold": true}

var temporalRepairFields = []string{"scheduled_time", "deadline_time"}

// Repair converges a readable-but-unwritable store in ONE pass, and writes once.
//
// The targeted repairs — minting a missing id, dropping an unknown key inside a
// temporal object — are each a RECORD repair, while every mutation pre- or
// post-flights Check over the WHOLE file. So a record repair lands only when its
// record is the file's last remaining error; with two or more instances the file
// never validates, every attempt refuses or rolls back, and the store is
// readable but unrepairable except by hand. This is the command that closes that
// loop: it fixes every instance it knows about across the store, then writes, so
// the file Check sees is already converged.
//
// It repairs the ARCHIVE as well as the live file, because post-write validation
// covers both: a live-only repair would leave every mutation still refusing on
// account of the archive, which is the same dead end one file over.
//
// Two invariants, both load-bearing:
//
//   - It never writes a partially repaired file. The repaired records are
//     re-checked in memory first, and any remaining error refuses the whole pass
//     with nothing written. An unparseable line therefore always refuses — Check
//     folds the parser's errors in — so a write can never silently drop a line
//     this binary could not read.
//   - It never touches `updated`. A repair asserts nothing about a task's
//     content; stamping would falsify when the task last changed and, in the
//     last-write-wins merge, hand the repairing device a win it did not earn.
func (s *Store) Repair(dryRun bool) RepairResult {
	var result RepairResult
	err := s.withLock(func() error {
		if source, declared := s.unsupportedSchemaSource(); source != "" {
			name := filepath.Base(s.org)
			if source == SourceArchive {
				name = filepath.Base(s.archive)
			}
			result = RepairResult{
				Status: RepairUnsupportedSchema, Fixes: []RepairFix{}, Written: false, DryRun: dryRun,
				Blockers: []RepairBlocker{{
					File: name, Line: 1, Message: check.UnsupportedVersionMessage(declared),
				}},
			}
			return nil
		}

		plans := s.repairPlans()
		fixes := []RepairFix{}
		blockers := []RepairBlocker{}
		for _, plan := range plans {
			fixes = append(fixes, plan.fixes...)
			blockers = append(blockers, plan.blockers...)
		}
		if len(blockers) > 0 {
			result = RepairResult{Status: RepairUnrepairable, Fixes: fixes, Blockers: blockers,
				Written: false, DryRun: dryRun}
			return nil
		}
		if len(fixes) == 0 || dryRun {
			result = RepairResult{Status: RepairOK, Fixes: fixes, Blockers: []RepairBlocker{},
				Written: false, DryRun: dryRun}
			return nil
		}

		s.clearRollback()
		before := s.fileSnapshot()
		for _, plan := range plans {
			if len(plan.fixes) == 0 {
				continue
			}
			if err := s.writeRecordsUnstamped(plan.path, plan.records); err != nil {
				s.recordRollback(err.Error(), RollbackWrite)
				_ = s.restore(before)
				result = repairFailed(fixes, s, dryRun)
				return nil
			}
		}
		if reason := s.postWriteFailure(); reason != "" {
			s.recordRollback(reason, RollbackValidation)
			_ = s.restore(before)
			result = repairFailed(fixes, s, dryRun)
			return nil
		}
		// `repair: true` marks the journal step so `undo` restores the malformed
		// bytes on request instead of refusing to write a file that fails
		// today's invariants — the one case where reverting to a Check-invalid
		// state is the user's intent rather than a hazard to gate.
		s.journal().Record("repair store", before, s.fileSnapshot(), "", true)

		result = RepairResult{Status: RepairOK, Fixes: fixes, Blockers: []RepairBlocker{},
			Written: true, DryRun: dryRun}
		return nil
	})
	if err != nil {
		return RepairResult{
			Status: RepairUnrepairable, Fixes: []RepairFix{}, DryRun: dryRun,
			Blockers: []RepairBlocker{{Line: 0, Message: "task store unavailable"}},
		}
	}
	return result
}

func repairFailed(fixes []RepairFix, s *Store, dryRun bool) RepairResult {
	reason, _ := s.LastRollback()
	if reason == "" {
		reason = "validation failed after the repair"
	}
	return RepairResult{
		Status: RepairUnrepairable, Fixes: fixes, Written: false, DryRun: dryRun,
		Blockers: []RepairBlocker{{Line: 0, Message: reason}},
	}
}

type filePlan struct {
	path     string
	file     string
	records  []record.Record
	fixes    []RepairFix
	blockers []RepairBlocker
}

// repairPlans plans the repair of every file in the store. Ids are minted from
// ONE pool spanning both files, so a repair can never invent an id that collides
// with a swept task — which the cross-file duplicate check would then refuse.
func (s *Store) repairPlans() []filePlan {
	taken := map[string]bool{}
	type target struct {
		path, name string
		parsed     record.Result
	}
	targets := []target{}

	paths := []string{s.org}
	if _, err := os.Stat(s.archive); err == nil {
		paths = append(paths, s.archive)
	}
	plans := []filePlan{}
	for _, path := range paths {
		name := filepath.Base(path)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if !utf8.Valid(raw) {
			plans = append(plans, filePlan{
				path: path, file: name, records: []record.Record{}, fixes: []RepairFix{},
				blockers: []RepairBlocker{{File: name, Line: 0, Message: "file is not valid UTF-8"}},
			})
			continue
		}
		parsed := record.Parse(raw)
		for _, parsedRecord := range parsed.Records {
			if id := parsedRecord.String("id"); id != "" {
				taken[id] = true
			}
		}
		targets = append(targets, target{path: path, name: name, parsed: parsed})
	}

	for _, entry := range targets {
		plans = append(plans, s.repairPlan(entry.path, entry.name, entry.parsed, taken))
	}
	return plans
}

// repairPlan applies the known repairs to one file's records, then re-checks the
// result IN MEMORY. Whatever Check still reports is a blocker: a defect this
// command does not know how to converge, reported rather than written over.
func (s *Store) repairPlan(path, name string, parsed record.Result, taken map[string]bool) filePlan {
	records := record.CloneAll(parsed.Records)
	fixes := s.applyKnownRepairs(records, name, taken)
	after := check.CheckParsed(record.Result{Records: records, Errors: parsed.Errors})
	blockers := []RepairBlocker{}
	for _, entry := range after.Errors {
		blockers = append(blockers, RepairBlocker{File: name, Line: entry.Line, Message: entry.Message})
	}
	return filePlan{path: path, file: name, records: records, fixes: fixes, blockers: blockers}
}

// applyKnownRepairs is the whole vocabulary of repairs this command performs.
//
// A MALFORMED id is deliberately left alone: children may point at it, and
// reminting would orphan them. An id-less record can never be a parent — Check
// resolves `parent` against ids it has already seen — so minting one cannot
// invalidate a reference.
func (s *Store) applyKnownRepairs(records []record.Record, file string, taken map[string]bool) []RepairFix {
	fixes := []RepairFix{}
	for index := range records {
		if records[index].String("type") == "meta" {
			continue
		}
		line := records[index].Line
		// `id.nil? || (id.is_a?(String) && id.empty?)`: an absent, null, or
		// empty id is minted. A present but MALFORMED id is left alone —
		// children may point at it, and reminting would orphan them — so Check
		// reports it as a blocker instead.
		raw, present := records[index].Get("id")
		if !present || isNullOrEmptyString(raw) {
			minted := s.genID(sortedKeys(taken))
			taken[minted] = true
			records[index].SetString("id", minted)
			fixes = append(fixes, RepairFix{
				File: file, Line: line, Kind: RepairMintedID,
				Message: "record missing id", ID: minted,
			})
		}
		for _, key := range temporalRepairFields {
			value, present := records[index].Get(key)
			if !present {
				continue
			}
			repaired, dropped, ok := dropUnknownTemporalKeys(value)
			if !ok || len(dropped) == 0 {
				continue
			}
			records[index].Set(key, repaired)
			fixes = append(fixes, RepairFix{
				File: file, Line: line, Kind: RepairDroppedTemporalKeys,
				Message: key + " has unknown keys: " + strings.Join(dropped, ", "),
			})
		}
	}
	return fixes
}

func isNullOrEmptyString(raw json.RawMessage) bool {
	trimmed := string(bytes.TrimSpace(raw))
	return trimmed == "null" || trimmed == `""`
}

// dropUnknownTemporalKeys rebuilds a temporal object without the keys the
// schema does not define, retaining the source order of the ones it keeps. It
// reports the dropped keys in source order, which is the order Check names them
// in the error this fix answers.
func dropUnknownTemporalKeys(raw json.RawMessage) (json.RawMessage, []string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, nil, false
	}
	fields, err := record.Fields(trimmed)
	if err != nil {
		return nil, nil, false
	}
	dropped := []string{}
	kept := []record.Field{}
	for _, field := range fields {
		if temporalRepairKeys[field.Key] {
			kept = append(kept, field)
			continue
		}
		dropped = append(dropped, field.Key)
	}
	if len(dropped) == 0 {
		return raw, nil, true
	}
	var out bytes.Buffer
	out.WriteByte('{')
	for index, field := range kept {
		if index > 0 {
			out.WriteByte(',')
		}
		record.EncodeString(&out, field.Key)
		out.WriteByte(':')
		if err := record.EncodeJSON(&out, field.Value); err != nil {
			return nil, nil, false
		}
	}
	out.WriteByte('}')
	return json.RawMessage(out.Bytes()), dropped, true
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return sortedIDs(keys)
}
