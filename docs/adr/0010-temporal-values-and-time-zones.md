# ADR-0010: Explicit civil-time values and time-zone context

Status: Accepted and implemented

Date: 2026-07-18; updated 2026-08-04

Task dates and optional minute-precision times are domain values handled by
`internal/temporal` and `internal/timezones`. Floating times are interpreted in
the configured evaluation zone; fixed times retain their IANA zone and fold.
JSON uses ISO dates, `HH:MM`, and RFC 3339 where applicable.

Time-zone context is passed explicitly through application operations. Domain
code does not mutate process-global time-zone state, and all surfaces share the
same parsing, comparison, recurrence, and display rules.
