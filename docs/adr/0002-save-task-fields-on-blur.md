# ADR-0002: Save task fields on blur through semantic patches

Status: Accepted and implemented

Date: 2026-07-09; updated 2026-08-04

The TUI's explicit edit mode saves a changed field when focus leaves it. The
edit session converts form values to typed `application.Patch` requests and
uses the shared application/store path. Validation errors remain attached to
the field; stale expectations refuse instead of overwriting another writer.

Escape abandons the current unsaved field. Already saved fields are ordinary
journaled mutations and remain undoable. The view layer does not write JSONL or
implement field semantics.
