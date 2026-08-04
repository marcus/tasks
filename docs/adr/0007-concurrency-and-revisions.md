# ADR-0007: File locks plus scoped optimistic revisions

Status: Accepted and implemented

Date: 2026-07-14; updated 2026-08-04

Writers serialize on the task store's sidecar lock, re-read while holding it,
apply one command, validate the result, and atomically replace the file.
Optimistic revisions protect decisions made before that lock was acquired.

Revision scope follows the operation: task edits use the relevant task or
field expectation; structural placement may include anchors and parents; HTTP
exposes opaque quoted ETags through `If-Match`. Revisions are equality tokens,
not public digest recipes. A mismatch refuses with fresh state rather than
merging silently.
