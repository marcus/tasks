# ADR-0001: Keep terminal forms as an embedded, task-agnostic package

Status: Accepted and implemented

Date: 2026-07-08; updated 2026-08-04

The reactive form engine lives in `internal/tui/termform`. It owns fields,
validation, focus, event handling, and rendering, but imports no task domain
packages. Tasks-specific form construction stays in `internal/tui`.

The package remains embedded until a second real consumer proves that a public
module and compatibility contract are worthwhile. This keeps the seam clear
without creating a premature standalone project.
