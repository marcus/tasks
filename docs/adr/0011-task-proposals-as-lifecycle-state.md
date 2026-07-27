# ADR-0011: Task proposals are a distinct lifecycle state

Status: accepted

Date: 2026-07-27

## Context

Agents often identify useful work that the owner may want on the task list, but
creating an ordinary task silently turns a suggestion into an accepted
commitment. Asking for permission before every capture adds enough friction that
agents hesitate or interrupt the owner. The owner primarily reviews work in the
TUI and wants a fast queue with single-key acceptance and rejection.

The existing dimensions do not express this boundary:

- contexts say where or how accepted work can be done;
- tags describe properties of accepted work;
- projects group accepted work under an outcome;
- `INBOX` contains accepted captures that still need processing; and
- `WAITING` contains accepted work delegated to or blocked on another party.

Delegation is related because it also involves another actor, but it answers who
owns accepted work rather than whether the owner accepts the work at all.

## Decision drivers

- Agents need explicit permission to record inert suggestions without
  interrupting the owner.
- Proposed work must not appear actionable or affect ordinary open-task and
  project counts.
- The TUI must make review visible and keyboard-cheap.
- Rejection should remain auditable and undoable.
- The design must not conflate approval with future assignment/delegation.

## Considered options

1. **Use an `approval` tag on `INBOX` or `WAITING`.** This needs no schema
   change, but overloads accepted-work states, leaks suggestions into ordinary
   views and counts, and makes approval a multi-field convention.
2. **Use an `Approvals` project or `@approval` context.** This gives proposals
   a place to appear but misstates project/context semantics and loses the
   distinction as soon as the task is moved or re-contextualized.
3. **Persist a separate proposal record type or proposal store.** This isolates
   unaccepted work but duplicates task fields, references, editing, storage,
   sync, merge, and TUI machinery for a small personal workflow.
4. **Add `PROPOSED` as a task lifecycle state.** This reuses task identity,
   notes, editing, atomic mutation, undo, sync, and conflict handling while
   allowing proposal-specific queries and transitions.

## Decision

Add `PROPOSED` as a third task-state category:

- proposed: `PROPOSED`;
- open: `INBOX`, `TODO`, `NEXT`, `WAITING`;
- closed: `DONE`, `CANCELLED`.

`PROPOSED` is persisted as an ordinary task with a stable id, but it is not
accepted work. It is excluded from ordinary active views, open-task counts,
availability calculations, project health, and completion/archive flows unless
a command explicitly includes proposals.

Agents create proposals through `tasks propose`. The TUI exposes a dedicated
Approvals view with a pending count. Approval transitions a proposal to
`INBOX`; rejection transitions it to `CANCELLED` and stamps `closed`. Both are
ordinary checked, atomic, undoable state transitions. Direct transition through
the general state command remains available, but `approve` and `reject` are the
documented intent-revealing paths.

Initial proposal rationale is stored in the existing task body. This decision
does not add persisted creator, reviewer, approval timestamp, assignee, or
delegation fields. `OperationContext` remains the future audit seam.

Delegation remains orthogonal. A future delegation design may add structured
assignment metadata and use `WAITING` for accepted delegated work; it must not
reuse `PROPOSED` to mean assigned.

## Consequences

- Agents can propose work without creating commitments or prompting first.
- The owner gets a focused, low-friction review inbox in the primary TUI.
- State classification can no longer assume every non-terminal state is open;
  the code must name proposed, open, and closed categories explicitly.
- Older binaries will reject a store containing `PROPOSED`, so the feature must
  land across validation, reads, mutations, merge behavior, CLI, API, TUI, and
  agent guidance as one coherent release.
- Rejected proposals remain in closed history and may be archived normally.
- Structured provenance and delegation remain possible follow-ons rather than
  prerequisites.
