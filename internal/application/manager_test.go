package application

import (
	"testing"

	"github.com/marcus/tasks/internal/store"
)

func TestArchiveSweepMatchingPinsTheFingerprint(t *testing.T) {
	h := newHarness(t, harnessOptions{})
	preview, supported := h.app.ArchivePreview(nil)
	if !supported || preview.Roots != 1 || preview.Fingerprint == "" {
		t.Fatalf("preview = %+v", preview)
	}
	before := h.read()
	changed, _ := h.app.ArchiveSweepMatching("not-"+preview.Fingerprint, nil)
	if changed.Refusal != store.ArchivePreviewChanged || changed.Preview.Fingerprint != preview.Fingerprint {
		t.Fatalf("mismatched fingerprint = %+v", changed.ArchiveResult)
	}
	if h.read() != before {
		t.Fatal("a refused sweep wrote")
	}
	swept, _ := h.app.ArchiveSweepMatching(preview.Fingerprint, nil)
	if !swept.OK() || swept.Roots != 1 || swept.StoreRevision == "" {
		t.Fatalf("matching sweep = %+v", swept.ArchiveResult)
	}
	revision, err := h.app.StoreRevision()
	if err != nil || revision != swept.StoreRevision {
		t.Fatalf("store revision %q (%v), sweep reported %q", revision, err, swept.StoreRevision)
	}
}

func TestManagerOperationsRefuseAStoreWithoutTheCapability(t *testing.T) {
	h := newHarness(t, harnessOptions{wrap: bareFactory()})
	if _, supported := h.app.HistoryPeek(); supported {
		t.Error("HistoryPeek claimed support")
	}
	if _, supported := h.app.GuardedHistoryStep(-1, "s1.x"); supported {
		t.Error("GuardedHistoryStep claimed support")
	}
	if _, supported := h.app.ArchiveSweepMatching("x", nil); supported {
		t.Error("ArchiveSweepMatching claimed support")
	}
	// The revision is still answered, through the checked read every store has.
	revision, err := h.app.StoreRevision()
	checked, _ := store.NewReader(h.org, h.archive, nil).CheckedReadSnapshot()
	if err != nil || revision != checked.StoreRevision {
		t.Fatalf("fallback revision %q (%v), checked read %q", revision, err, checked.StoreRevision)
	}
}
