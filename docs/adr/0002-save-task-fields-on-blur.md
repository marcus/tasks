# ADR-0002: Commit validated task fields on blur through semantic Store patches

Status: Accepted and implemented

Date: 2026-07-13

## Context

The task panel needs fast keyboard editing without making every keystroke a file
write. The user prefers save-on-blur: after changing a field, Tab or Shift-Tab
should make that change durable before focus moves. A whole-form draft would
make ordinary traversal feel like a separate editing workflow, while calling
existing Store methods directly from field callbacks would couple generic form
code to task persistence.

The TUI also polls for external file changes. Stable task IDs prevent writes to
the wrong row, but a stale field must not overwrite a newer value. A full-record
digest is too broad for ordinary fields because an unrelated same-task change
would cause a false conflict.

## Decision drivers

- A changed field becomes durable when it loses focus, not on each key.
- Focus moves only after validation and persistence succeed.
- Generic `TermForm` fields remain persistence-neutral.
- One control cannot erase data semantically owned by another control.
- Same-field and affected-subtree conflicts cannot be overwritten silently.
- Existing lock, atomic-write, validation, rollback, stable-ID, and lifecycle
  behavior remains in `Tasks::Store`.

## Considered options

1. Save on each key. This is reactive, but creates invalid intermediate writes,
   excessive IO, and unusable history.
2. Keep a whole-form draft and save explicitly. This is atomic as a batch, but
   it does not match the preferred traversal workflow and makes Escape/Cancel
   apply to changes made many fields ago.
3. Commit one semantic field patch on blur, using a two-phase host protocol and
   Store-side conflict checks.

## Decision

Choose option 3.

`TermForm` treats focus departure as two phases. It validates and emits a
`commit_requested` transition containing the field key, normalized proposed
value, expected baseline, and intended next focus. The host persists the value,
then accepts or rejects the transition. Acceptance updates the baseline and
moves focus. Rejection retains focus, pending text, cursor, and an actionable
error. Resize, picker entry, and terminal reflow are not blur.

`Tui::TaskEditorSession` translates the request into a `Tasks::TaskPatch` and
calls `Store#patch_task!`. The patch names a semantic field. Its expectation is
the slice that field owns rather than an ordinary whole-record digest:

- Contexts owns only `@` tags.
- Tags owns non-context tags other than `defer`.
- Deferred owns only `defer`.
- A date owns its date and documented INBOX/recurrence side effects.
- State owns lifecycle state, closed date, recurrence completion effects, and
  any affected descendants.
- Location owns the parent and subtree order.
- Notes owns the exact raw body.

Under the existing Store lock, `patch_task!` re-reads records, finds the target
by stable ID, compares the owned slice or affected-subtree fingerprint, applies
the patch through fresh-record domain helpers, validates the resulting records,
writes once through the normal atomic path, runs `Tasks::Check`, journals the
change, reloads, and returns a typed result plus a fresh edit snapshot.

The result distinguishes at least `ok`, `no_change`, `conflict`, `missing`,
`invalid`, `cycle`, and `too_deep`. Successful results refresh clean fields and
reactive context. A conflict keeps the local field copyable and offers reload,
revert, or keep-for-copy; the first version does not offer overwrite or an
automatic merge. A missing target never retargets the neighboring row.

High-impact state or location changes show their exact consequence and require
confirmation before the pending blur reaches the Store.

## Consequences

Traversal and persistence feel like one operation, while no write occurs until
a field is complete enough to leave. Failed validation and conflicts keep focus
where the user can fix the problem.

The Store gains a first-class semantic patch path and exact edit snapshots,
including raw body and parent data that `Tasks::Item` does not expose today.
Pure mutation helpers must be extracted so the TUI and CLI share task behavior.

Save-on-blur creates multiple durable mutations during one editing pass. ADR-0003
groups rigorously contiguous commits into a usable undo step without weakening
the durability or conflict behavior chosen here.
