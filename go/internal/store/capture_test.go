package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCaptureReadsBothSourcesInOneLockAndKeepsHeldSnapshot(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, "tasks.jsonl")
	archivePath := filepath.Join(dir, "archive.jsonl")
	copyFixture(t, "valid/full-field-matrix/store/tasks.jsonl", livePath)
	copyFixture(t, "valid/archive-pair/store/archive.jsonl", archivePath)

	locker := &recordingLocker{}
	snapshot, err := Capture(Paths{Live: livePath, Archive: archivePath}, locker)
	if err != nil {
		t.Fatal(err)
	}
	if locker.calls != 1 {
		t.Fatalf("lock acquisitions = %d, want 1", locker.calls)
	}
	if _, ok := snapshot.ItemByID(Live, "f0000026"); !ok {
		t.Fatal("live record missing from capture")
	}
	if _, ok := snapshot.ItemByID(Archive, "a0000102"); !ok {
		t.Fatal("archive record missing from capture")
	}

	if err := os.WriteFile(livePath, []byte(`{"type":"meta","version":2}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.ItemByID(Live, "f0000026"); !ok {
		t.Fatal("held snapshot changed after a later live-file write")
	}
}

func TestCaptureTreatsMissingArchiveAsEmptyAndLeavesMalformedRecordsReadable(t *testing.T) {
	dir := t.TempDir()
	livePath := filepath.Join(dir, "tasks.jsonl")
	copyFixture(t, "adversarial/mid-write-torn-file/store/tasks.jsonl", livePath)

	snapshot, err := Capture(Paths{Live: livePath, Archive: filepath.Join(dir, "missing.jsonl")}, Unlocked{})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items()) == 0 {
		t.Fatal("sound records before malformed tail were not retained")
	}
	if got := snapshot.ArchiveItems(); len(got) != 0 {
		t.Fatalf("missing archive = %#v, want empty", got)
	}
}

func TestCaptureRequiresLivePathAndReadLocker(t *testing.T) {
	if _, err := Capture(Paths{}, Unlocked{}); err == nil {
		t.Fatal("missing live path did not fail")
	}
	if _, err := Capture(Paths{Live: "tasks.jsonl"}, nil); err == nil {
		t.Fatal("missing read locker did not fail")
	}
}

type recordingLocker struct {
	calls int
	err   error
}

func (locker *recordingLocker) WithReadLock(fn func() error) error {
	locker.calls++
	if locker.err != nil {
		return locker.err
	}
	return fn()
}

func copyFixture(t *testing.T, fixture, target string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "porting", "fixtures", fixture))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCapturePropagatesLockFailureWithoutReading(t *testing.T) {
	dir := t.TempDir()
	locker := &recordingLocker{err: errors.New("lock unavailable")}
	_, err := Capture(Paths{Live: filepath.Join(dir, "missing.jsonl")}, locker)
	if got, want := err.Error(), "lock unavailable"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}
