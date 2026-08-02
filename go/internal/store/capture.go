package store

import (
	"errors"
	"io"
	"os"

	"tasks-go/internal/record"
)

// ReadLocker keeps a multi-file capture coherent with mutations. Locking is a
// later persistence slice: this read-model package only states the boundary a
// platform-specific lock implementation must satisfy.
type ReadLocker interface {
	WithReadLock(func() error) error
}

// Unlocked is the fixture and single-reader adapter. Production callers must
// supply the same lock implementation used by mutations before concurrent
// writes are ported.
type Unlocked struct{}

func (Unlocked) WithReadLock(fn func() error) error { return fn() }

// Paths identifies the live store and its optional archive companion.
type Paths struct {
	Live    string
	Archive string
}

// Capture reads the live and archive files through one caller-supplied lock
// acquisition, then returns an immutable read projection. Parsing remains
// lenient: malformed lines do not prevent sound task records from appearing in
// the snapshot. A missing archive is an empty history; a missing live file is
// an error, matching Store's read boundary.
func Capture(paths Paths, locker ReadLocker) (Snapshot, error) {
	if paths.Live == "" {
		return Snapshot{}, errors.New("live store path is required")
	}
	if locker == nil {
		return Snapshot{}, errors.New("read locker is required")
	}

	var snapshot Snapshot
	err := locker.WithReadLock(func() error {
		live, err := readRecords(paths.Live, false)
		if err != nil {
			return err
		}
		archive, err := readRecords(paths.Archive, true)
		if err != nil {
			return err
		}
		snapshot = NewSnapshot(live, archive)
		return nil
	})
	return snapshot, err
}

func readRecords(path string, optional bool) ([]record.Record, error) {
	if path == "" && optional {
		return nil, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if optional && errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	// Keep the descriptor open while reading. The future lock implementation
	// protects the live/archive pair; an atomic replacement after this open
	// leaves this capture internally coherent rather than mixing old metadata
	// with new bytes.
	if _, err := file.Stat(); err != nil {
		return nil, err
	}
	contents, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	return record.Parse(contents).Records, nil
}
