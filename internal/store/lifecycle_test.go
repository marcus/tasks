package store

import (
	"os"
	"strings"
	"testing"

	"github.com/marcus/tasks/internal/atomic"
	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/record"
)

// A closed root, a closed child, a closed grandchild, and a live sibling.
// test/test_store.rb's ARCHIVE_TREE_RECORDS: the shape that proves a sweep
// moves a whole subtree and preserves its DFS structure inside the archive.
const archiveTreeFixture = `{"type":"meta","version":2}
{"type":"section","id":"accc0001","title":"Projects"}
{"type":"task","id":"accc0002","parent":"accc0001","state":"DONE","title":"Closed project","closed":"2026-07-01"}
{"type":"task","id":"accc0003","parent":"accc0002","state":"CANCELLED","title":"Closed child","closed":"2026-07-02"}
{"type":"task","id":"accc0004","parent":"accc0003","state":"DONE","title":"Closed grandchild","closed":"2026-07-03"}
{"type":"task","id":"accc0005","parent":"accc0001","state":"NEXT","title":"Still live"}
`

const sweepDay = "2026-03-14"

func recordFor(t *testing.T, path, title string) (record.Record, bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return record.Record{}, false
	}
	for _, parsed := range record.Parse(raw).Records {
		if parsed.String("title") == title {
			return parsed, true
		}
	}
	return record.Record{}, false
}

// -- the sweep ------------------------------------------------------------------

func TestArchiveSweepsDoneBlocks(t *testing.T) {
	store, _ := writerFixture(t, journalFixture)
	result := store.ArchiveSweep(sweepDay, nil)
	if !result.OK() || result.Roots != 1 {
		t.Fatalf("sweep = %d roots, refusal %q", result.Roots, result.Refusal)
	}
	if _, present := recordFor(t, store.org, "Old finished thing"); present {
		t.Error("the closed task must be swept out of the live file")
	}
	archived, present := recordFor(t, store.archive, "Old finished thing")
	if !present {
		t.Fatal("the closed task must appear in the archive")
	}
	if archived.String("state") != "DONE" {
		t.Errorf("state = %q", archived.String("state"))
	}
	if archived.String("closed") != "2026-06-20" {
		t.Errorf("the closed date travels with the record: %q", archived.String("closed"))
	}
	if archived.String("archived") != sweepDay {
		t.Errorf("archived = %q, want %q", archived.String("archived"), sweepDay)
	}
	if archived.Has("parent") {
		t.Error("a swept root loses its parent")
	}
}

func TestArchiveWithNothingToDo(t *testing.T) {
	store, _ := writerFixture(t, journalFixture)
	if result := store.ArchiveSweep(sweepDay, nil); result.Roots != 1 {
		t.Fatalf("first sweep = %d", result.Roots)
	}
	result := store.ArchiveSweep(sweepDay, nil)
	if !result.OK() || result.Roots != 0 {
		t.Errorf("second sweep = %d roots, refusal %q — nothing left to move",
			result.Roots, result.Refusal)
	}
}

func TestArchivePreviewCountsRootsAndDescendantsWithoutWriting(t *testing.T) {
	store, _ := writerFixture(t, archiveTreeFixture)
	before := readStore(t, store)

	preview := store.ArchivePreviewFor(sweepDay)
	if preview.Roots != 1 || preview.Descendants != 2 || preview.Total() != 3 {
		t.Errorf("preview = %d roots, %d descendants, %d total", preview.Roots,
			preview.Descendants, preview.Total())
	}
	if preview.Blocked() {
		t.Error("nothing is blocked here")
	}
	if got := readStore(t, store); got != before {
		t.Error("a preview must not write")
	}
	if _, err := os.Stat(store.archive); err == nil {
		t.Error("a preview must not create the archive")
	}
}

func TestArchiveNestedSubtreePreservesDFSStructureAndUndo(t *testing.T) {
	store, _ := writerFixture(t, archiveTreeFixture)
	before := readStore(t, store)

	if result := store.ArchiveSweep(sweepDay, nil); result.Roots != 1 {
		t.Fatalf("sweep = %d roots %q", result.Roots, result.Refusal)
	}
	root, _ := recordFor(t, store.archive, "Closed project")
	child, _ := recordFor(t, store.archive, "Closed child")
	grandchild, _ := recordFor(t, store.archive, "Closed grandchild")
	if root.Has("parent") {
		t.Error("the swept root loses its parent")
	}
	if child.String("parent") != root.String("id") ||
		grandchild.String("parent") != child.String("id") {
		t.Error("the subtree's parent chain must survive the move intact")
	}
	if !check.Check(store.org).OK() || !check.Check(store.archive).OK() {
		t.Fatal("both files must validate after a sweep")
	}

	if outcome, label := store.HistoryStep(-1); outcome != HistoryOK || label != "archive sweep" {
		t.Fatalf("undo = (%q, %q)", outcome, label)
	}
	if got := readStore(t, store); got != before {
		t.Error("undo restores the live file exactly")
	}
	if _, err := os.Stat(store.archive); err == nil {
		t.Error("undo removes the archive the sweep created")
	}

	if outcome, _ := store.HistoryStep(1); outcome != HistoryOK {
		t.Fatal("redo replays the sweep")
	}
	if _, present := recordFor(t, store.org, "Closed project"); present {
		t.Error("redo re-sweeps the root out of the live file")
	}
	if _, present := recordFor(t, store.archive, "Closed grandchild"); !present {
		t.Error("redo restores the whole subtree to the archive")
	}
}

func TestArchiveRefusesClosedRootWithOpenDescendant(t *testing.T) {
	blocked := strings.Replace(archiveTreeFixture,
		`{"type":"task","id":"accc0004","parent":"accc0003","state":"DONE","title":"Closed grandchild","closed":"2026-07-03"}`,
		`{"type":"task","id":"accc0004","parent":"accc0003","state":"NEXT","title":"Closed grandchild"}`, 1)
	store, _ := writerFixture(t, blocked)
	before := readStore(t, store)

	result := store.ArchiveSweep(sweepDay, nil)
	if result.Refusal != ArchiveOpenDescendants {
		t.Fatalf("refusal = %q, want open_descendants", result.Refusal)
	}
	if result.Preview.BlockedRoots() != 1 || result.Preview.OpenDescendants() != 1 {
		t.Errorf("blocked = %d roots, %d open", result.Preview.BlockedRoots(),
			result.Preview.OpenDescendants())
	}
	if got := result.Preview.Blocks[0].OpenTitles; len(got) != 1 || got[0] != "Closed grandchild" {
		t.Errorf("open titles = %v", got)
	}
	if got := readStore(t, store); got != before {
		t.Error("the entire blocked subtree remains live")
	}
	if _, err := os.Stat(store.archive); err == nil {
		t.Error("a refused sweep writes no archive")
	}
	if outcome, _ := store.HistoryStep(-1); outcome != HistoryEmpty {
		t.Error("a refusal does not consume history")
	}
}

func TestArchiveRefusesCompleteIDOverlapWhenCanonicalContentDiffers(t *testing.T) {
	store, _ := writerFixture(t, archiveTreeFixture)
	before := readStore(t, store)
	stale := `{"type":"meta","version":2}
{"type":"task","id":"accc0002","state":"DONE","title":"Stale project title","closed":"2026-07-01","archived":"` + sweepDay + `"}
{"type":"task","id":"accc0003","parent":"accc0002","state":"CANCELLED","title":"Closed child","closed":"2026-07-02"}
{"type":"task","id":"accc0004","parent":"accc0003","state":"DONE","title":"Closed grandchild","closed":"2026-07-03"}
`
	if err := os.WriteFile(store.archive, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	result := store.ArchiveSweep(sweepDay, nil)
	if result.Refusal != ArchiveConflict {
		t.Fatalf("refusal = %q, want archive_conflict", result.Refusal)
	}
	if !contains(result.Details, "accc0002") {
		t.Errorf("details = %v, want the differing id", result.Details)
	}
	if got := readStore(t, store); got != before {
		t.Error("newer live content is never replaced by id alone")
	}
	if _, present := recordFor(t, store.archive, "Stale project title"); !present {
		t.Error("the existing archive is left alone")
	}
}

func TestArchiveRefusesPartialPriorArchiveWithoutDeletingLiveSubtree(t *testing.T) {
	store, _ := writerFixture(t, archiveTreeFixture)
	before := readStore(t, store)
	partial := `{"type":"meta","version":2}
{"type":"task","id":"accc0002","state":"DONE","title":"Closed project","closed":"2026-07-01","archived":"` + sweepDay + `"}
`
	if err := os.WriteFile(store.archive, []byte(partial), 0o644); err != nil {
		t.Fatal(err)
	}

	result := store.ArchiveSweep(sweepDay, nil)
	if result.Refusal != ArchiveConflict {
		t.Fatalf("refusal = %q, want archive_conflict", result.Refusal)
	}
	if !contains(result.Details, "accc0003") || !contains(result.Details, "accc0004") {
		t.Errorf("details = %v, want the ids the archive is missing", result.Details)
	}
	if got := readStore(t, store); got != before {
		t.Error("a partial prior archive must not delete the live subtree")
	}
	if _, present := recordFor(t, store.archive, "Closed child"); present {
		t.Error("nothing new was written to the archive")
	}
}

// An interrupted sweep — the archive written, the live delete lost — converges
// on the retry, and the retry is idempotent.
func TestArchiveInterruptionAfterArchiveWriteIsRetrySafeAndIdempotent(t *testing.T) {
	store, _ := writerFixture(t, archiveTreeFixture)

	// The live write fails after the archive write landed — the interruption
	// the archive-first ordering exists to survive.
	restore := atomic.SetWriteHook(func(candidate, content string) error {
		if candidate == store.org {
			return os.ErrPermission
		}
		return atomic.WriteDirect(candidate, content)
	})
	result := store.ArchiveSweep(sweepDay, nil)
	restore()
	if !result.Failed {
		t.Fatalf("the interrupted sweep should report failure, got %d roots", result.Roots)
	}
	if _, present := recordFor(t, store.org, "Closed project"); !present {
		t.Error("the live copy survives the interruption")
	}
	if _, present := recordFor(t, store.archive, "Closed project"); !present {
		t.Error("the archive copy was made durable first")
	}
	if !check.Check(store.org).OK() || !check.Check(store.archive).OK() {
		t.Fatal("both files stay valid through an interruption")
	}

	if retry := store.ArchiveSweep(sweepDay, nil); retry.Roots != 1 {
		t.Fatalf("retry = %d roots %q, want it to finish the sweep", retry.Roots, retry.Refusal)
	}
	if _, present := recordFor(t, store.org, "Closed project"); present {
		t.Error("the retry completes the destructive half")
	}
	if again := store.ArchiveSweep(sweepDay, nil); again.Roots != 0 {
		t.Errorf("a completed retry is idempotent, got %d roots", again.Roots)
	}
	archived := 0
	raw, _ := os.ReadFile(store.archive)
	for _, parsed := range record.Parse(raw).Records {
		if contains([]string{"accc0002", "accc0003", "accc0004"}, parsed.String("id")) {
			archived++
		}
	}
	if archived != 3 {
		t.Errorf("archive holds %d of the moved records, want exactly 3", archived)
	}
}

// The pinned preview is validated by the whole candidate fingerprint, not by
// the root count: two candidates that swap places leave the count identical.
func TestArchiveExpectedPreviewIsValidatedAtomicallyByCandidateFingerprint(t *testing.T) {
	seed := `{"type":"meta","version":2}
{"type":"section","id":"accc2001","title":"Projects"}
{"type":"task","id":"accc2002","parent":"accc2001","state":"DONE","title":"Candidate A","closed":"2026-07-09"}
{"type":"task","id":"accc2003","parent":"accc2001","state":"NEXT","title":"Candidate B"}
`
	store, _ := writerFixture(t, seed)
	expected := store.ArchivePreviewFor(sweepDay)

	swapped := `{"type":"meta","version":2}
{"type":"section","id":"accc2001","title":"Projects"}
{"type":"task","id":"accc2002","parent":"accc2001","state":"NEXT","title":"Candidate A"}
{"type":"task","id":"accc2003","parent":"accc2001","state":"DONE","title":"Candidate B","closed":"2026-07-10"}
`
	if err := os.WriteFile(store.org, []byte(swapped), 0o644); err != nil {
		t.Fatal(err)
	}
	current := store.ArchivePreviewFor(sweepDay)
	if current.Roots != expected.Roots {
		t.Fatal("the setup depends on the root count being unchanged")
	}
	if current.Fingerprint == expected.Fingerprint {
		t.Fatal("the fingerprint must differ when the candidates do")
	}

	result := store.ArchiveSweep(sweepDay, &expected)
	if result.Refusal != ArchivePreviewChanged {
		t.Fatalf("refusal = %q, want preview_changed", result.Refusal)
	}
	if _, present := recordFor(t, store.org, "Candidate A"); !present {
		t.Error("nothing moved")
	}
	if _, err := os.Stat(store.archive); err == nil {
		t.Error("no archive was written")
	}
}

// -- delete ---------------------------------------------------------------------

func TestDeleteRemovesOneTaskAndIsUndoable(t *testing.T) {
	store, _ := writerFixture(t, journalFixture)
	before := readStore(t, store)

	result := store.DeleteTask("aaaa0005", false, "", "")
	if result.Status != MutationOK {
		t.Fatalf("delete = %q %v", result.Status, result.Errors)
	}
	if len(result.TouchedIDs) != 1 || result.TouchedIDs[0] != "aaaa0005" {
		t.Errorf("touched = %v", result.TouchedIDs)
	}
	if _, present := recordFor(t, store.org, "Review PR backlog"); present {
		t.Error("the task must be gone")
	}

	if outcome, label := store.HistoryStep(-1); outcome != HistoryOK ||
		label != "delete: Review PR backlog" {
		t.Fatalf("undo = (%q, %q)", outcome, label)
	}
	if got := readStore(t, store); got != before {
		t.Error("undo restores the deleted task exactly")
	}
}

func TestDeleteRefusesASubtreeWithoutCascade(t *testing.T) {
	store, _ := writerFixture(t, archiveTreeFixture)
	before := readStore(t, store)

	result := store.DeleteTask("accc0002", false, "", "")
	if result.Status != MutationConflict {
		t.Fatalf("delete = %q, want conflict", result.Status)
	}
	if result.Summary.Descendants != 2 || result.Summary.OpenDescendants != 0 {
		t.Errorf("summary = %d descendants, %d open", result.Summary.Descendants,
			result.Summary.OpenDescendants)
	}
	if got := readStore(t, store); got != before {
		t.Error("a refused delete writes nothing")
	}
}

func TestDeleteCascadeRemovesTheWholeSubtreeAndLabelsTheCount(t *testing.T) {
	store, _ := writerFixture(t, archiveTreeFixture)
	result := store.DeleteTask("accc0002", true, "", "")
	if result.Status != MutationOK {
		t.Fatalf("delete = %q %v", result.Status, result.Errors)
	}
	if len(result.TouchedIDs) != 3 {
		t.Errorf("touched = %v, want the whole subtree", result.TouchedIDs)
	}
	if _, present := recordFor(t, store.org, "Closed grandchild"); present {
		t.Error("cascade removes descendants, it never reparents them")
	}
	if _, present := recordFor(t, store.org, "Still live"); !present {
		t.Error("a sibling outside the subtree is untouched")
	}
	if _, label := store.HistoryStep(-1); label != "delete 3 tasks: Closed project" {
		t.Errorf("history label = %q", label)
	}
}

func TestDeleteRefusesAnArchivedOnlyID(t *testing.T) {
	store, _ := writerFixture(t, journalFixture)
	if result := store.ArchiveSweep(sweepDay, nil); result.Roots != 1 {
		t.Fatalf("sweep = %d", result.Roots)
	}
	// aaaa0008 now lives only in the archive, which delete never consults.
	if result := store.DeleteTask("aaaa0008", false, "", ""); result.Status != MutationNotFound {
		t.Errorf("delete of an archived id = %q, want not_found", result.Status)
	}
}

func TestDeleteRefusesASection(t *testing.T) {
	store, _ := writerFixture(t, journalFixture)
	result := store.DeleteTask("aaaa0003", true, "", "")
	if result.Status != MutationInvalid || result.FirstError() != "delete targets tasks" {
		t.Errorf("delete of a section = %q %v", result.Status, result.Errors)
	}
}

func TestDeleteRefusesAnInvalidStoreWithoutRepairing(t *testing.T) {
	invalid := journalFixture + `{"type":"task","parent":"aaaa0003","state":"TODO","title":"No id"}` + "\n"
	store, _ := writerFixture(t, invalid)
	before := readStore(t, store)

	result := store.DeleteTask("aaaa0005", false, "", "")
	if result.Status != MutationStoreInvalid {
		t.Fatalf("delete = %q, want store_invalid — deletion is never a repair route", result.Status)
	}
	if got := readStore(t, store); got != before {
		t.Error("nothing was written")
	}
}

func TestDeleteRevisionGuardComparesAllThreeComponents(t *testing.T) {
	store, _ := writerFixture(t, archiveTreeFixture)
	revision, ok := store.TaskRevision("accc0002")
	if !ok {
		t.Fatal("no revision")
	}
	// A descendant's lifecycle change invalidates the delete's precondition,
	// even though the root's own fields are untouched. A sibling's scalar edit
	// would not, and that narrowing is the point of a three-part revision.
	expected, _ := store.ExpectedFor("accc0004", FieldState)
	if result := store.PatchTask("accc0004", FieldState, "CANCELLED", expected, "", sweepDay); result.Status != MutationOK {
		t.Fatalf("descendant state change = %q %v", result.Status, result.Errors)
	}
	result := store.DeleteTask("accc0002", true, revision, "")
	if result.Status != MutationStale {
		t.Errorf("delete with a stale revision = %q, want stale", result.Status)
	}

	if result := store.DeleteTask("accc0002", true, "v1.not-a-digest", ""); result.Status != MutationInvalid {
		t.Errorf("malformed revision = %q, want invalid", result.Status)
	}
}

// -- proposals -------------------------------------------------------------------

const proposalFixture = `{"type":"meta","version":2}
{"type":"section","id":"ccdd0001","title":"Inbox"}
{"type":"task","id":"ccdd0002","parent":"ccdd0001","state":"PROPOSED","title":"Parent proposal"}
{"type":"task","id":"ccdd0003","parent":"ccdd0002","state":"PROPOSED","title":"Child proposal"}
{"type":"task","id":"ccdd0004","parent":"ccdd0001","state":"TODO","title":"Accepted work"}
`

func TestApproveMovesAProposalToInbox(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	result := store.DecideProposal("ccdd0003", ProposalApprove, nil, "", sweepDay)
	if result.Status != MutationOK {
		t.Fatalf("approve = %q %v", result.Status, result.Errors)
	}
	if result.Summary.From != "PROPOSED" || result.Summary.To != "INBOX" {
		t.Errorf("summary = %+v", result.Summary)
	}
	child, _ := recordFor(t, store.org, "Child proposal")
	if child.String("state") != "INBOX" {
		t.Errorf("state = %q, want INBOX", child.String("state"))
	}
	if _, label := store.HistoryStep(-1); label != "approve proposal: Child proposal" {
		t.Errorf("history label = %q", label)
	}
}

func TestRejectCancelsAndAppendsTheNoteInTheSameWrite(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	result := store.DecideProposal("ccdd0003", ProposalReject,
		[]string{"out of scope", "revisit in Q3"}, "", sweepDay)
	if result.Status != MutationOK {
		t.Fatalf("reject = %q %v", result.Status, result.Errors)
	}
	child, _ := recordFor(t, store.org, "Child proposal")
	if child.String("state") != "CANCELLED" {
		t.Errorf("state = %q", child.String("state"))
	}
	if child.String("body") != "out of scope\nrevisit in Q3" {
		t.Errorf("body = %q", child.String("body"))
	}
	if child.String("closed") != sweepDay {
		t.Errorf("a rejected proposal is closed today: %q", child.String("closed"))
	}
}

func TestDecideRefusesUndecidedDescendantsFirst(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	before := readStore(t, store)

	result := store.DecideProposal("ccdd0002", ProposalApprove, nil, "", sweepDay)
	if result.Status != MutationConflict {
		t.Fatalf("approve = %q, want conflict", result.Status)
	}
	if got := result.Summary.ProposedDescendantIDs; len(got) != 1 || got[0] != "ccdd0003" {
		t.Errorf("proposed descendants = %v", got)
	}
	if got := readStore(t, store); got != before {
		t.Error("a leaves-first refusal writes nothing")
	}
}

func TestDecideRefusesATaskThatIsNotProposed(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	result := store.DecideProposal("ccdd0004", ProposalApprove, nil, "", sweepDay)
	if result.Status != MutationInvalid || result.FirstError() != "task is TODO, not PROPOSED" {
		t.Errorf("approve = %q %v", result.Status, result.Errors)
	}
}

func TestApproveAndCompleteClosesTheProposalInOneUndoableWrite(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	result := store.ApproveAndCompleteProposal("ccdd0003", "", sweepDay)
	if result.Status != MutationOK {
		t.Fatalf("approve+complete = %q %v", result.Status, result.Errors)
	}
	if result.Summary.From != "PROPOSED" || result.Summary.To != "DONE" ||
		result.Summary.Action != ProposalApproveComplete {
		t.Errorf("summary = %+v", result.Summary)
	}
	child, _ := recordFor(t, store.org, "Child proposal")
	if child.String("state") != "DONE" || child.String("closed") != sweepDay {
		t.Errorf("child = %q closed %q", child.String("state"), child.String("closed"))
	}
	// One undo step, and it restores PROPOSED exactly — not INBOX, which is what
	// two composed writes would leave behind.
	if _, label := store.HistoryStep(-1); label != "approve + complete proposal: Child proposal" {
		t.Errorf("history label = %q", label)
	}
	restored, _ := recordFor(t, store.org, "Child proposal")
	if restored.String("state") != "PROPOSED" || restored.String("closed") != "" {
		t.Errorf("undo left %q closed %q, want PROPOSED", restored.String("state"), restored.String("closed"))
	}
}

func TestApproveAndCompleteRefusesWithoutWritingWhenTheRevisionIsStale(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	before := readStore(t, store)
	result := store.ApproveAndCompleteProposal("ccdd0003", "0-deadbeef", sweepDay)
	if result.Status == MutationOK {
		t.Fatalf("a stale revision was accepted: %+v", result)
	}
	if got := readStore(t, store); got != before {
		t.Error("a refused approve+complete writes nothing")
	}
}

func TestApproveAndCompleteStillDecidesLeavesFirst(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	before := readStore(t, store)
	if result := store.ApproveAndCompleteProposal("ccdd0002", "", sweepDay); result.Status != MutationConflict {
		t.Fatalf("approve+complete of a parent = %q", result.Status)
	}
	if got := readStore(t, store); got != before {
		t.Error("a leaves-first refusal writes nothing")
	}
}

func TestApproveAndCompleteRefusesATaskThatIsNotProposed(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	result := store.ApproveAndCompleteProposal("ccdd0004", "", sweepDay)
	if result.Status != MutationInvalid || result.FirstError() != "task is TODO, not PROPOSED" {
		t.Errorf("approve+complete = %q %v", result.Status, result.Errors)
	}
}

func TestNotesAreOnlyAllowedWhenRejecting(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	result := store.DecideProposal("ccdd0003", ProposalApprove, []string{"why"}, "", sweepDay)
	if result.Status != MutationInvalid ||
		result.FirstError() != "notes are only allowed when rejecting a proposal" {
		t.Errorf("approve with notes = %q %v", result.Status, result.Errors)
	}
}

func TestApproveIsTheOnlyWriteAllowedToLeaveAcceptedWorkUnderAProposal(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	// The child is approved while its parent is still PROPOSED — the whole
	// point of deciding leaves-first.
	if result := store.DecideProposal("ccdd0003", ProposalApprove, nil, "", sweepDay); result.Status != MutationOK {
		t.Fatalf("approve = %q %v", result.Status, result.Errors)
	}
	// A plain state patch to the same shape is still refused.
	store2, _ := writerFixture(t, proposalFixture)
	expected, _ := store2.ExpectedFor("ccdd0003", FieldState)
	result := store2.PatchTask("ccdd0003", FieldState, "TODO", expected, "", sweepDay)
	if result.Status != MutationInvalid ||
		result.FirstError() != "accepted work cannot remain under a proposed task" {
		t.Errorf("state patch = %q %v — the gate must survive for every other write",
			result.Status, result.Errors)
	}
}

func TestDecideRefusesAnUnknownAction(t *testing.T) {
	store, _ := writerFixture(t, proposalFixture)
	result := store.DecideProposal("ccdd0003", "maybe", nil, "", sweepDay)
	if result.Status != MutationInvalid ||
		result.FirstError() != "proposal action must be approve or reject" {
		t.Errorf("decide = %q %v", result.Status, result.Errors)
	}
}
