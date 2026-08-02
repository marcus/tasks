# `recur-interval-cookies` Ruby oracle capture

This directory records the Ruby oracle for manifest slice
`recur-interval-cookies`, characterized against source revision
`28424b77950f59f2d78bf089be316c38d9f54aec`. The source closure has no drift
from that revision on this branch.

The focused interval-only oracle passes with **20 runs, 66 assertions, 0
failures, 0 errors, and 0 skips**:

```sh
ruby test/test_recur.rb
```

Representative direct observations confirm the boundaries the Go translation
must preserve: `weekly` normalizes to `.+1w`; a bare `2w` obeys the supplied
default prefix; `++0d` is rejected; month arithmetic clamps 2026-01-31 +1m to
2026-02-28; and catch-up may land on today.

No Go code or Go output was used to produce this capture. The next step is a
**mid-tier translation** in a different context: implement only the interval
cookie parser/projection in `internal/recur`, add the medium-tier property
coverage, then compile and run differential conformance without changing this
Ruby baseline.
