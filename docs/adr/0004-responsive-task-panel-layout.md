# ADR-0004: Responsive task-panel layout from pure geometry

Status: Accepted and implemented

Date: 2026-07-11; updated 2026-08-04

TUI layout is computed by state-free functions under `internal/tui`. Wide
terminals show list and detail/edit panels side by side; narrow terminals use a
single focused panel. Rendering and hit testing consume the same geometry so
mouse and keyboard behavior cannot disagree about ownership of a cell.

Breakpoints are presentation policy only. They do not affect task selection,
mutation semantics, or data storage.
