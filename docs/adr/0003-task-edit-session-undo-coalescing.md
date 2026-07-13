# ADR-0003: Coalesce one task-edit session into a usable undo step

Status: Accepted and implemented

Date: 2026-07-13

## Context

Save-on-blur makes every completed field durable immediately. If every blur also
creates an independent journal entry, editing five fields requires five undo
commands and exposes implementation mechanics to the user. Debouncing writes
or grouping by elapsed time would weaken durability and make slow note editing
behave differently from quick edits.

The Store already surrounds mutations with locked before/after snapshots and a
file-backed Journal shared by CLI and TUI operations. That gives coalescing a
stronger correctness test than timestamps alone.

## Decision drivers

- Every successful blur remains immediately durable and recoverable after a
  crash.
- One ordinary visit to the task editor should normally be one undo step.
- A CLI command, external write, undo/redo, or another edit session must retain
  an independent history boundary.
- CLI commands keep their current one-operation/one-entry behavior.

## Considered options

1. One history entry per blur. Simple and exact, but poor editing ergonomics.
2. Delay persistence or group writes within a timer. This makes durability and
   undo behavior depend on typing speed.
3. Write each blur immediately and allow Journal to replace only a rigorously
   contiguous entry carrying the same edit-session key.

## Decision

Choose option 3.

`TaskEditorSession` creates an unguessable coalesce key on entry and includes it
with each semantic field patch. `Store#with_history` accepts the key as an
optional argument; existing CLI calls omit it.

After a successful write, Journal may coalesce with its current tip only when:

1. both entries carry the same non-nil key; and
2. the new mutation's exact `before` bytes equal the tip's exact `after` bytes;
   and
3. the journal cursor is at that tip with no intervening undo/redo branch.

When those conditions hold, the entry keeps the earliest `before` and newest
`after`, updates its label/metadata, and remains one undo step. Any mismatch,
external/CLI mutation, undo/redo, reopened editor, or history branch starts a
new entry or segment. Coalescing never crosses Store instances merely because
a key or time happens to match.

## Consequences

The user gets immediate save-on-blur durability and useful session-level undo.
A crash after any completed blur leaves the latest data on disk and the current
coalesced journal entry able to restore the session's starting bytes.

Journal format and tests become more sophisticated. Exact byte continuity is a
required safety condition, not an optimization. If implementation cannot prove
that condition with the shared on-disk journal, it must fall back to separate
undo entries rather than merge optimistically.
