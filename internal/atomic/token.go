package atomic

import "sync/atomic"

// tempCounter stands in for Ruby's Thread.current.object_id in the temp-file
// name. Its only job is uniqueness between concurrent writers to different
// files within one process, which a monotonic counter satisfies exactly; the
// value is never observed once the rename completes.
var tempCounter atomic.Uint64

func nextTempToken() uint64 { return tempCounter.Add(1) }
