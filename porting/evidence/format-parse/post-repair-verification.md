# `format-parse` post-repair verification

Date: 2026-08-02

The handoff after `b76ff2d` still described five Ruby diagnostic defects, but
their repair (`51bb514`) is already reachable from `port/format-parse`.
This iteration re-ran the Ruby oracle comparison rather than treating the
stale handoff as a reason to repeat the implementation.

The five review-discovered cases match Ruby exactly, and the full direct
parser comparison is 28/28. Ruby remains the oracle; no Go output was used to
change an expected result.

```console
$ cd go && go test ./...
ok   tasks-go/internal/record
$ go test -race ./...
ok   tasks-go/internal/record
$ go vet ./...
$ ruby ../porting/evidence/format-parse/conformance
format-parse direct conformance: 28/28 cases matched
$ ruby ../porting/manifest-issues validate
ok: 44 slices, 4 campaigns, every source path and oracle test resolves
$ ruby ../test/test_format.rb
26 runs, 58 assertions, 0 failures, 0 errors, 0 skips
```

The slice is ready only for fresh independent medium-risk source-fidelity and
Go-idiom reviews. Approval is not claimed by this verification.
