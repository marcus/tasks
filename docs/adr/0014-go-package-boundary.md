# ADR-0014: Ports and adapters as the Go package boundary

Status: Proposed — not accepted. Conditional on Phase 1 evidence (epic
`td-27fbf5`).

Date: 2026-08-01

## Context

ADR-0005 already established the shape a port would preserve: a typed
`Tasks::Application` facade over a request-scoped Store, with CLI, TUI, and
HTTP as thin adapters that map one result vocabulary to their own surface. That
boundary is enforced in Ruby by convention and review, because Ruby has no
compiler-checked package graph.

Go does have one. Import cycles are errors, `internal/` is enforced by the
toolchain, and a package that imports `net/http` drags it into every binary
that imports the package. That turns ADR-0005's convention into something a
build can check — but only if the boundary is drawn before the code, because
retrofitting a layering violation out of a Go module means moving types across
packages and breaking every caller.

The port also adds consumers Ruby never had. A mobile binding (ADR-0018) links
the core into an application process with no filesystem policy, no processes to
spawn, and no terminal. A Windows build needs different locking and replacement
primitives (ADR-0015). An embedded TUI (ADR-0017) is a library, not a program.
Each of those is an adapter or it is a fork.

## Decision drivers

- Domain rules must be identical across CLI, HTTP, TUI, and native bindings —
  the parity property ADR-0005 exists to protect.
- Platform difference must live in one named place per concern, not in
  `runtime.GOOS` branches scattered through domain code.
- A mobile build must be able to omit desktop-only capability entirely rather
  than carry unreachable APIs.
- The layering must be checkable by the build and by a reviewer reading imports,
  not only by taste.

## Considered options

1. **A flat package with everything in it.** Fastest to write, and it makes
   every subsequent decision in this ADR unavailable. Platform branching
   spreads, and the mobile binding gets the whole desktop surface.
2. **Mirror the Ruby file layout one-to-one.** Comfortable during translation,
   but Ruby's layout encodes Ruby's constraints; it has no `internal/`, no
   distinction between what a binding may see and what it may not, and its
   `bin/tasks` entrypoint holds 2,900 lines of CLI grammar that must not become
   a package the TUI imports.
3. **Ports and adapters with dependencies pointing inward**, domain and
   application in `internal/`, the TUI in `pkg/`, and platform behavior behind
   named interfaces.

## Decision

Choose option 3.

Dependencies point inward. Domain code imports no terminal, HTTP, filesystem,
mobile, or process package. The application layer declares the interfaces it
needs; adapters own serialization and operating-system policy.

The layout the port targets:

```text
cmd/tasks · cmd/tasks-api · cmd/tasks-tui   entrypoints
internal/application                        commands, queries, read models
internal/domain                             task/tree/time/placement rules
internal/store/jsonl · journal · temporal · merge
internal/cli · httpapi · platform
pkg/tui                                     importable TUI component
mobile/tasksbind                            native facade
```

The named ports, one per platform concern, are:

- `Locker` — process-level writer exclusion;
- `AtomicWriter` — durable replacement;
- `FileIdentity` — external-change detection;
- `DeviceIdentity` — update stamps;
- `Repository` — the domain-facing record operations; and
- `AgentRunner` — model-harness process execution.

Each gets two boring implementations where two platforms genuinely differ
(desktop and mobile for the store; Unix and Windows for locking, replacement,
and process trees) rather than one implementation with branches inside it.
Mobile builds omit `AgentRunner` entirely.

`pkg/tui` is public because sidecar must import it (ADR-0017). Everything else
stays `internal/` so the module can be restructured without breaking outside
consumers, and so the mobile facade's small surface (ADR-0018) is the only way
in from outside.

Application results carry what their adapter needs to answer from the committed
transaction — touched IDs, rollback status, the relevant resource revision, and
the committed global store revision — so no adapter performs a fresh read after
a write and accidentally reports a neighboring writer's state.

Keep HTTP dependencies out of the CLI and TUI package graphs even though
`net/http` is in the standard library. The isolation is architectural, not a
dependency-count exercise.

## Consequences

- The port cannot proceed slice-by-slice in Ruby file order. Slices are
  proof-sized capabilities that cross files (`porting/manifest.jsonl`), which is
  more up-front slicing work than translating file by file.
- Two implementations of each platform port must be written and tested even
  where only macOS is exercised daily. Windows locking has to be tested while
  another process holds the file open, on Windows — not asserted.
- Desktop safety is never weakened to simplify the mobile adapter. That is an
  escalation condition in `porting/PORTING.md`, not a trade to be made locally.
- The boundary is checkable but not free: a translation agent that reaches for
  a convenient import across a layer produces a compile error rather than a
  silent violation, which costs a repair round. That is the intended cost.
- ADR-0005's `MutationResult` vocabulary and ADR-0007's revision scopes must be
  carried across as distinct types. A single generic `Version` would compile and
  hide real mistakes.

## Related

- [ADR-0005](0005-application-boundary.md) — the boundary being preserved
- [ADR-0007](0007-concurrency-and-revisions.md) — the revision scopes that must
  stay distinct
- Plan: [`docs/plans/deprecated/tasks-go-port-plan.md`](../plans/deprecated/tasks-go-port-plan.md),
  "Target shape"
