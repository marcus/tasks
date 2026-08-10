# ADR-0015: Four embeddable-TUI contracts the first external host forced

Status: Accepted and implemented

Date: 2026-08-10

## Context

[ADR-0013](0013-public-embeddable-tui-api.md) declared `pkg/tui` the supported
host boundary and froze its names. Building the first real host against it — the
Sidecar Tasks plugin, Packet 1.1 of the
[Tasks-in-Sidecar plan](../plans/active/tasks-in-sidecar.md) — found four places
where the contract could not express what a host actually does, or where Tasks
told a host something untrue. Each belongs on the Tasks side of the seam: the
host exchanges only messages and strings, so a workaround in the host would be a
second implementation of Tasks behavior.

1. `Close` is the only way to release a model, and it always saves. Every model
   a host builds for one namespace shares one session file, so a host that
   builds speculatively — a project switch, an epoch race, a build that lands
   after the user moved on — had to choose between leaking an agent queue and
   overwriting the live model's session with the discarded model's state.
2. A store that is missing, corrupt, unreadable, or a directory built a model
   that rendered a fully-chromed frame reading `0 open`. Tasks has a read-error
   banner, and none of the four set it. This is a Tasks bug in its own right:
   standalone `tasks-tui` painted the same lie.
3. `SuppressQuit` refused the quit action with a flash instead of recording it,
   which made `QuitRequested` and `ClearQuitRequest` unreachable in exactly the
   configuration a quit-owning host runs in.
4. `ThemeOptions.Colors` replaced the user's configured colors wholesale, so
   running Tasks inside a host silently discarded the palette its user chose.

## Decision

### Discard is the call for a model the host throws away

`Discard` releases a model's resources without persisting its session: it shuts
the agent queue and provider processes exactly as `Close` does, and skips the
session save. `Close` remains the call for the model the host actually
presented.

The two share one `sync.Once`. Whichever runs first wins; the other is a no-op
returning the first call's error; each is idempotent. A host must never rely on
calling both to mean anything, and a `Close` after a `Discard` does not
resurrect the save.

### A broken store is a read error, a first run is not

The TUI's post-read assessment now sets the sticky `readErr` as well as its
flash, so the condition outlives the flash and appears in the footer banner
until the store is readable again. It reports when the tasks directory does not
exist, when the store path is a directory or cannot be opened, and when the
store's bytes are not valid UTF-8 or not valid Tasks JSONL.

It deliberately does NOT report the first-run state: no store file yet inside an
existing tasks directory, or a zero-length file. Tasks writes the store on the
first mutation, so a brand-new install legitimately has nothing to read, and
calling that an error would greet every new user with a broken banner. A store
holding only its meta record is likewise healthy and empty. The distinction a
caller needs — "no tasks" versus "no read" — survives, because every genuinely
unreadable case above reports.

`Model.LoadError` exposes that assessment across the seam so a host can render
its own diagnostic. It is meaningful only after a read, so after `Init`'s
command or any `Update`.

### Embedded quit always latches

Embedded quit records a request the host observes through `QuitRequested`,
whether or not `SuppressQuit` is set, and never returns `tea.Quit`.
`ClearQuitRequest` un-latches, and a second quit re-latches. The refusal flash
is gone.

`SuppressQuit` now means what its name says: the HOST owns the quit affordance.
Tasks drops quit from its own key hint and never treats quit as terminating,
while still acting on the key by latching. Tasks' own safety confirmations — an
unsaved task draft, pending agent work — still run first in both modes, because
they protect Tasks data the host cannot see.

### Host colors overlay the user's colors

`ThemeOptions.Colors` is an overlay: each host-supplied slot wins, unspecified
slots keep the user's configured value, and the rest falls back to the named
base theme. `ThemeOptions.ReplaceColors` is an explicit opt-in for a host that
must guarantee an exact palette. Replacement is never the default, because
discarding user configuration is not a reasonable default.

## Consequences

- `Discard`, `LoadError`, and `ThemeOptions.ReplaceColors` join the public
  compatibility surface named by ADR-0013. Nothing was removed.
- The behavior changes are visible to standalone `tasks-tui` too: a broken store
  now says so. That is the point — the host only made an existing lie visible.
- A host that suppresses quit must handle the latched request. Ignoring it now
  means an unhandled request rather than a refusal the user can see.
- Host-facing contract tests live beside the external-consumer proof in
  `pkg/tui`, so the API keeps being exercised the way a real host uses it.
