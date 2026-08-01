# Hypothetical Go port plan for `tasks`

- **Status:** exploratory; no port is approved
- **Research snapshot:** 2026-07-31
- **Related issue:** `td-2b768f`; Phase 1 epic in the tasks repo:
  `td-27fbf5`
- **Premise:** the final product has a Go core, Go CLI, Go HTTP adapter, Go
  native bindings, and a Bubble Tea TUI; Ruby remains only as the migration
  oracle and is removed after cutover

The companion [language-porting playbook](language-porting-playbook.md)
develops the conformance harness and implementation loop in more detail,
based on recent large language migrations.

## Recommendation

A full Go port is plausible. The existing architecture already points in the
right direction: CLI, TUI, and HTTP are adapters over one application layer and
one JSONL store. Go can express that shape more directly and compile it for
macOS, Windows, iOS, and Android.

The port should proceed only as a behavior-preserving replacement. Do not mix
it with a new data format, a sync protocol, a redesigned CLI, or a new task
model. Any one of those may be worthwhile later. Combining them would remove
the only trustworthy oracle for whether the port is correct.

The difficult work is not exposing commands or routes. It is preserving the
small semantics that now sit behind them. The current repository contains
roughly 12,000 lines under `lib/tasks`, another 12,000 in the TUI, a 2,900-line
CLI entrypoint, a 1,500-line HTTP adapter, a 3,500-line OpenAPI contract, and
more than 2,000 named tests. That is mature product behavior, even though the
storage remains pleasingly simple.

## Target shape

Keep the application a modular monolith with ports and adapters:

```text
                         ┌──────────────┐
                         │ cmd/tasks    │ CLI
                         ├──────────────┤
                         │ cmd/tasks-api│ net/http + OpenAPI
                         ├──────────────┤
                         │ cmd/tasks-tui│ Bubble Tea
                         ├──────────────┤
                         │ mobile       │ Obj-C/Java or C bindings
                         └──────┬───────┘
                                │
                    ┌───────────▼───────────┐
                    │ application           │
                    │ commands, queries,    │
                    │ immutable read models │
                    └───────────┬───────────┘
                                │
                ┌───────────────▼───────────────┐
                │ domain                         │
                │ task/tree/time/placement rules │
                └───────────────┬───────────────┘
                                │ ports
              ┌─────────────────┼──────────────────┐
              ▼                 ▼                  ▼
       JSONL repository    undo journal      clock/identity
       desktop/mobile      desktop/mobile    platform adapters
```

Dependencies point inward. Domain code imports no terminal, HTTP, filesystem,
mobile, or process package. The application layer defines the interfaces it
needs. Adapters own serialization and operating-system policy.

A possible repository shape after cutover:

```text
cmd/
  tasks/
  tasks-api/
  tasks-tui/
internal/
  domain/
  application/
  store/jsonl/
  journal/
  temporal/
  merge/
  cli/
  httpapi/
  platform/
pkg/
  tui/
mobile/
  tasksbind/
docs/
  api/openapi.yaml
  cli-spec.md
  adr/
testdata/
  conformance/
```

Keep the port in the existing repository. The Ruby and Go implementations need
to share specifications, fixtures, and black-box tests until cutover. During
migration, build the new executables as `tasks-go`, `tasks-api-go`, and
`tasks-tui-go`; preserve the public names only when each adapter is ready to
replace its Ruby counterpart.

## The hard parts

### Behavioral parity needs an executable oracle

The Ruby tests are valuable, but they cannot simply be translated into Go.
Translated tests tend to encode the new implementation's interpretation of the
old behavior. Build a language-neutral conformance harness that runs both
implementations from identical fixture directories and compares observable
results.

For CLI operations, capture arguments, environment, stdin, stdout, stderr,
exit status, and resulting store and journal bytes. For HTTP, compare status,
headers, JSON, store bytes, and subsequent reads. Pin clocks, time zones,
hostnames, device IDs, and random ID generation. Normalize only values that are
explicitly nondeterministic, such as request IDs.

The harness must cover rejection and recovery, not just success. An invalid
changeset is rejected before lookup; a stale field revision differs from a
missing task; a failed post-write check restores prior bytes; a same-owner
worker retry differs from a conflicting claim. Those distinctions are part of
the product.

### JSONL compatibility is stricter than parsing JSON

The store carries several coupled contracts:

- a metadata record first;
- fixed key order and omitted defaults;
- stable eight-hex IDs;
- explicit parents and strict depth-first pre-order;
- live and archive files treated as one consistency boundary;
- semantic update stamps that exclude non-semantic rewrites; and
- validation after every mutation with rollback on failure.

The first Go writer should emit byte-identical canonical JSONL for every
supported Ruby fixture. Byte compatibility keeps rollback simple: either
binary can open data written by the other. It also keeps Git diffs calm while
the implementation underneath changes.

Byte identity rules out `encoding/json` as the writer: it will not reproduce
Ruby's key order, float and escape formatting, or omitted defaults. A small
hand-written canonical emitter, fuzzed against parse–emit round trips, is
part of the port's cost from the first phase that writes — put it in the
estimate rather than discovering it in the first persistence slice.

The persistence port needs failure injection at each step: lock acquisition,
temp creation, write, file flush, validation, rename, directory flush, journal
append, and cleanup. A green happy-path suite does not prove crash safety.

### Locking and atomic replacement differ by platform

Today's `flock`, rename, symlink, mode, and directory-`fsync` behavior is
macOS/Unix-shaped. A Go port intended for Windows and mobile needs explicit
ports instead of scattered `runtime.GOOS` branches:

- `Locker` for process-level writer exclusion;
- `AtomicWriter` for durable replacement;
- `FileIdentity` for change detection;
- `DeviceIdentity` for update stamps; and
- `Repository` for the domain-facing record operations.

macOS and Linux can retain advisory file locks. Windows needs a real Windows
lock and replacement implementation, tested while another process has the file
open. iOS and Android normally have one app process and an app-container path;
they still need crash-safe writes, but not desktop Git and symlink behavior.

Do not weaken desktop safety to make the mobile adapter simpler. Implement two
boring stores behind one narrow interface.

### Revisions and post-write reads are concurrency behavior

Task revisions, global store revisions, `ETag`, `If-Match`, editor snapshots,
and undo preconditions all observe different scopes. Preserve those scopes
deliberately. A single generic hash called `Version` will hide mistakes.

Every mutation should return enough information for its adapter to answer from
the committed transaction. If an HTTP handler performs an unrelated fresh read
after the write, it can accidentally return a neighboring writer's state. The
Go application result should carry touched IDs, rollback status, the relevant
resource revision, and the committed global store revision.

### Undo and redo are shared durable state

Undo is not a TUI convenience. CLI and TUI share a content-addressed journal,
editor saves coalesce across fresh store instances, redo branches truncate,
and corruption is detected from hashes. The Go journal must read all existing
Ruby journal data or ship an explicit migration with a rehearsed rollback.

Preserve exact-byte restoration. Reconstructing a logically equivalent task
file would alter formatting, update stamps, and Git diffs. Test concurrent
writers, coalescing lifetime, missing blobs, truncated indexes, stale history
preconditions, and a crash between each journal boundary.

### Dates and recurrence contain years of edge cases

`tasks` distinguishes scheduled availability from deadlines, floating local
times from fixed instants, IANA zones, daylight-saving folds, inherited
availability, all-day boundaries, friendly input, recurrence cookies, and
calendar recurrence. The Go standard library is capable, but that does not
make a new implementation equivalent.

Build the temporal package from a table-driven corpus extracted from the Ruby
tests. Include DST gaps and folds, leap days, month ends, multiple weekdays,
time-zone fallback, 12/24-hour presentation, date-order configuration,
out-of-range years, and recurrence on parent tasks. Pin the time-zone database
used in CI and expose its version through the existing config/meta surfaces.

### Tree mutations and merge rules are algorithms, not CRUD

Moving a task moves a subtree while preserving DFS order and depth limits.
Completing a parent may close descendants. Project creation may bootstrap its
root and must remain atomic. Archive previews refuse open descendants. Proposal
approval and delegation carry their own state rules.

The Git merge driver adds another set of properties. Stable IDs align records;
tags union; state progression and timestamps resolve selected fields; a
delegation marker merges as one atomic value; output remains canonical. Keep
property tests for determinism, commutativity, associativity where promised,
idempotence, and refusal without overwriting malformed input.

### Native bindings need a smaller API than HTTP

Do not expose internal Go structs through `gomobile`. Its generated bridge
supports a restricted set of types, and panics must never cross the language
boundary. Create a small `mobile/tasksbind` facade using strings, byte slices,
integers, booleans, and errors. Structured commands and results can cross as
versioned JSON bytes until a typed binding proves more useful.

Native calls should invoke `application` directly. Do not start a loopback
server inside the phone. Run blocking reads and writes away from the UI thread,
make cancellation explicit, translate every error into a stable code, and test
object lifetimes across Swift/Objective-C and Kotlin/Java.

The mobile repository writes inside the application container. Notifications,
background refresh, keychain credentials, document sharing, and app lifecycle
remain UI/platform adapter concerns.

### Mobile sync remains a separate product

A Go core gives the phone an offline engine. It does not decide how Mac,
Windows, iPhone, and Android converge. Keep sync out of the port's acceptance
criteria; the local Go core must reach parity first.

The recommended direction, decided now so the port leaves room for it: the
field-aware merge is the hard part of sync, it already exists, and it is
transport-independent. Port it as a library and put the transport behind a
`SyncTransport` adapter. Git then becomes one transport rather than the
architecture, and today's desktop reconciliation keeps working unchanged.

For the phone, the first transport candidate is the same Git hub, embedded:
a pure-Go client (go-git) that fetches, three-way merges the live and archive
files with the ported merge rules, commits, and pushes at foreground and
background-refresh boundaries. Git is normally a poor invisible phone
protocol because conflicts need a human; a deterministic, self-resolving
merge removes exactly that objection, and a two-file store measured in
kilobytes removes the size one. The real risks are credential storage in the
keychain, iOS background-execution limits, go-git's partial merge support
(the three-way merge is application code — Git supplies only the merge base),
and unbounded archive growth: shallow-clone the phone and treat archive
compaction as a desktop job.

If embedded Git proves fragile, the fallback is the same merge library behind
a dumber transport — iCloud file coordination on Apple platforms, or later a
small sync service that replays the same merge server-side. Identity,
authentication, deletion semantics, ordering conflicts, retries, encryption,
and recovery from long-offline devices remain unsolved in every variant;
that is why sync stays a separate product from the port.

### Agent execution is operating-system work

`tasks -p` and the TUI agent queue spawn model-harness CLIs, stream output,
inject task paths and memory, report diffs, and cancel process trees. That
behavior is desktop-only and varies sharply between Unix and Windows.

Put it behind an `AgentRunner` interface. Preserve synchronous CLI execution
and FIFO TUI queue semantics as separate adapters over the same runner. Unix
can keep process-group TERM-to-KILL escalation; Windows needs Job Objects or an
equivalent tree-termination implementation. Mobile builds should omit the
runner entirely rather than carry unreachable process APIs.

### Cutover and rollback must remain boring

Ruby should stay authoritative until the Go core passes conformance. Do not
dual-write a real task store. Shadow reads are safe; parallel writers are not.

The ideal cutover changes only the executable behind `tasks`. Because the Go
writer and journal remain byte-compatible, rollback means restoring the Ruby
binary or PATH entry, not migrating user data backward. Back up the live store
and journal, rehearse the swap on a copy, then run both versions' `check`
commands before and after the production switch.

## Rebuilding the TUI with Bubble Tea

Use the current Charm v2 stack:

- Bubble Tea v2 for the Elm-style update loop and terminal lifecycle;
- Bubbles v2 for text inputs, text areas, viewports, lists, and help;
- Lip Gloss v2 for layout and semantic styles; and
- Huh v2 selectively for self-contained forms or its screen-reader-friendly
  prompt mode, not as a second application architecture.

Bubble Tea v2 is MIT-licensed and currently provides a cell-based renderer,
keyboard and mouse input, clipboard support, color downsampling, and
declarative view configuration. These remove a large amount of custom terminal
plumbing. They do not replace the current interaction design.

Sources: [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Bubbles](https://github.com/charmbracelet/bubbles),
[Lip Gloss](https://github.com/charmbracelet/lipgloss), and
[Huh](https://github.com/charmbracelet/huh).

Model the TUI as explicit child models rather than one enormous `Update`:

```text
Root
├── navigation and current view
├── task list and stable selection
├── right-side details/editor
├── modal and action-palette stack
├── agent prompt and FIFO activity queue
├── file-change watcher
└── theme, dimensions, and mouse hit regions
```

Every asynchronous command should return a message carrying the stable task ID
and the store revision it started from. The receiving model can then discard,
merge, or surface stale results instead of applying them to whatever row is
currently selected.

Preserve the interaction contracts that users already know:

- selection follows stable identity across refreshes;
- the right panel preserves scroll for the same task and resets for another;
- save-on-blur commits before focus moves;
- contextual Tab, Shift-Tab, double-Escape, and finish-edit behavior remain
  coherent;
- clipboard and paste messages reach the focused field or modal;
- queued agent requests stay FIFO, preserve per-request history, and cancel
  their whole process tree;
- mouse hit testing uses terminal cells, including wide characters; and
- `NO_COLOR`, 16-color, 256-color, truecolor, narrow-terminal, and accessible
  modes remain deliberate outputs.

Do not port the old renderer line by line. Port its user-visible behavior and
reuse the current stable-ID, editor-session, action-registry, and layout ideas.
Bubble Tea's `Model`, `Update`, and `View` boundary should replace manual
`IO.select` and frame painting, not wrap them.

## Design the TUI to be embeddable in sidecar

Sidecar already embeds td's monitor as a plugin: `sidecar` imports
`github.com/marcus/td/pkg/monitor`, wraps it in a ~600-line adapter
(`internal/plugins/tdmonitor`), and gets the full monitor UI inside its own
layout, theme, keymap, and command palette. A `tasks` panel in sidecar is one
of the reasons to do this port at all — a Ruby TUI can never be embedded this
way, a Bubble Tea one can be almost for free, but only if Phase 7 adopts the
embedding contract from the start. The first release of the port should not
include the sidecar plugin itself; it should include the architecture that
makes the plugin a small adapter later rather than a second TUI rewrite.

The contract td's monitor satisfies today, which the `tasks` TUI should match:

**An importable component that does not own the terminal.** The TUI lives in a
public package — `pkg/tui` in the layout above, not `internal/` — with the
standalone `cmd/tasks-tui` as a thin shell over it. An
`EmbeddedOptions`-style constructor plus `Close()` is the whole entry surface.
In embedded mode the model never touches terminal-level features: no
alt-screen, no mouse-enable, no cursor control, no `tea.Quit` that could tear
down the host (sidecar intercepts quit defensively, but the component should
not emit it in the first place). `View()` returns composable content that the
host constrains, places, and themes; the host's own `tea.View` owns the
terminal.

**Injection points instead of global style.** td's `EmbeddedOptions` accepts a
base directory, refresh interval, panel-border renderer, modal-border
renderer, markdown theme palette, and a swappable clipboard function. This is
how sidecar makes the embedded UI match its active theme (and fix WSL
clipboard) without patching td. The `tasks` TUI needs the same seams, which
means no package-level singleton styles: every style derives from an injected
theme with a default.

**Shortcuts exported as data.** Sidecar's keymap is a registry of
`(context, key, command-id)` bindings with multi-key sequence support; on
adoption it calls the embedded component's `ExportBindings()` and
`ExportCommands()` and registers everything under host-prefixed context names
(`td-monitor`, `td-modal`, …). The export feeds sidecar's footer hints,
command palette (with 1–5 priorities deciding footer versus palette-only),
and conflict awareness; raw key messages still flow to the embedded `Update`
for actual handling. Two live queries complete the contract:
`CurrentContextString()` tells the host which context is active so its
palette and footer follow the embedded UI's state, and `ConsumesTextInput()`
tells it when to stop intercepting printable keys because the user is typing
in a search box or form. All of this presupposes what the TUI section already
requires: one keymap registry as the single source of truth for every
binding, with contexts as first-class values. That registry is also what
generates standalone help and enables user rebinding, so it is not
embedding-only cost.

**td's modal model and mouse system, adopted rather than reinvented — but
refactored as part of the extraction.** td factored both into small,
domain-free packages: `pkg/monitor/modal` is a declarative modal library
(sections that report measured focusables; a layout pass that registers hit
regions only for what the scroll viewport shows; built-in Tab/Enter/Esc
navigation and hover state), and `pkg/monitor/mouse` is a rectangular
hit-region map with double-click and drag tracking. The core design is right
and battle-tested — the two-pass focus discovery and viewport-clipped hit
regions each encode a real fixed bug, and the mouse package needs nothing.
The `tasks` TUI should adopt both rather than growing a third implementation.

The clean way is to extract them — modal, mouse, and the keymap
registry/export shape — into a small shared module that `tasks` uses from
the first Phase 7 slice and td migrates to opportunistically; they depend
only on the Charm stack, so extraction is cheap. The fallback of importing
`github.com/marcus/td/pkg/monitor/...` directly works but makes `tasks`
depend on td's whole module; a private copy works but guarantees divergence.

The extraction is also the moment to fix five known flaws in the modal
library, because each sits on a seam `tasks` needs and an API break is free
while the module has one consumer:

- Styling is package-level hardcoded ANSI-256 variables with no setter, and
  the `ModalRenderer` sidecar injects belongs to td's older hand-rolled modal
  path — the library renders its own chrome and never sees it, so
  library-based modals ignore the host theme even in sidecar today. The
  module takes an injected theme (current values as default) and its chrome
  renderer becomes the injectable one, not a parallel system.
- Hit-region offsets for the modal chrome are hand-synced constants
  (`border(1) + padding(2)` as a comment) rather than measured; a themed
  border would silently shift every region. Measure the chrome the way the
  sections already are.
- The modal assumes it is composited centered on screen and registers
  absolute hit regions on that assumption, which the overlay code must match
  by convention. Position becomes explicit — passed in or returned.
- Input routing offers each message to every section until one responds,
  trusting sections to check focus themselves. The layout pass knows which
  section owns the focused element; route only to it.
- Esc-means-cancel and the English hint line are baked in. Key policy
  becomes overridable and hints derive from the keymap registry, because the
  `tasks` interaction contracts (double-Escape, save-on-blur, finish-edit)
  must be expressible inside the engine, not by bypassing it — conformance
  is against `tasks`, not against td's habits.

One caution from td's own history: the library migration inside td stalled
halfway — its hardest modals (the form editor, issue details) never moved
off the legacy path, so the section model is unproven against a really
complex modal. Validate it against the `tasks` editor equivalent in an early
Phase 7 slice, not the last one. Finishing td's internal migration is td's
backlog, not a precondition here.

**Mouse events in component coordinates.** Sidecar enables terminal mouse
reporting, subtracts its own header offset from each event's Y coordinate,
and forwards the adjusted `tea.MouseMsg` to the focused plugin. The embedded
component therefore computes every hit region from the width and height of
its last `WindowSizeMsg` and never assumes it is rendered at the terminal
origin — the same discipline the standalone TUI needs anyway once panels can
be resized. Hit regions are rebuilt on every size change; sidecar re-sends
`WindowSizeMsg` on project switches precisely because td's bounds are only
recomputed on receipt.

**Message discipline a host can live with.** The td plugin's history shows
what the host forces on the component: construction opens the store and is
slow enough to keep off the first frame, so the constructor must be cheap and
heavy work must arrive later via a ready message; project switching means
`Init`/`Start`/`Stop` are re-entrant, `Stop` releases everything, and async
results carry an epoch so stale ones are dropped rather than adopted;
`StatusMessage` is exposed so the host can mirror it as toasts; and
cross-plugin actions cross the boundary as exported typed messages in both
directions (td emits `SendTaskToWorktreeMsg`, accepts `OpenIssueByIDMsg`).
The `tasks` equivalents — at minimum an inbound open-task-by-ID and an
exposed status/error signal — should be part of the component's public API.
The JSONL store makes the lifecycle side easier than td's: there is no
database pool to reference-count, the write lock is held only during writes,
and the file watcher the TUI already needs is what keeps an embedded panel
current alongside CLI writes from other processes.

What this adds to the port, honestly: the injection seams, the exported
keymap metadata, and (if extraction is chosen) a small shared UI module to
coordinate, including the modal-library refactor above — modest, since the
section interface and mouse package barely change, but real work. Most of the rest — keymap as data, hit regions, no terminal
globals, cheap construction — is architecture the standalone TUI should have
regardless, and the existing action-registry and cell-based hit testing in
the Ruby TUI show the shape is already proven in this product. The retrofit
cost, by contrast, is high: sidecar's contract touches styling, input
routing, focus, and lifecycle, which is most of a TUI's connective tissue.
Verify the contract cheaply: a smoke test that mounts the root model inside a
trivial host program — fixed viewport, offset mouse events, injected
renderers, exported bindings consumed — proves embeddability without
building the real sidecar plugin.

## Migration phases

### Phase 0: record the decisions

Before code, add proposed ADRs in the `tasks` repository for:

- Go as the authoritative core and final Ruby retirement;
- the ports-and-adapters package boundary;
- byte-compatible JSONL and journal compatibility;
- Bubble Tea v2 as the TUI foundation;
- the TUI as an embeddable component (sidecar's contract, plugin deferred);
- the native binding contract; and
- sync explicitly deferred from the language port.

Define measurable stop conditions. If the Go implementation requires a data
format break, cannot match temporal behavior, or forces permanent dual domain
implementations, stop and reconsider rather than pushing through sunk cost.

### Phase 1: build the conformance harness

Create sanitized fixture stores covering valid, legacy, malformed, and
concurrent cases. Add a runner protocol that can execute Ruby or Go commands
and emit a machine-readable observation containing outputs, errors, files, and
journal state. Capture the current Ruby result set before Go code exists.

Gate: the harness detects intentionally introduced mismatches in exit status,
JSON output, write bytes, revision tokens, and rollback behavior.

### Phase 2: port read-only domain behavior

Implement records, canonical parsing, validation, trees, immutable snapshots,
queries, links, availability, quadrants, projects, and config resolution. Ship
`tasks-go check`, `list`, `show`, `agenda`, `next`, `inbox`, `projects`, and
JSON output against fixture copies only.

Gate: all read-only conformance cases match Ruby, including malformed-store
diagnostics and time-zone-pinned output.

### Phase 3: port mutations and transaction safety

Implement creation, changesets, placement, lifecycle transitions, proposals,
delegation, recurrence, archive operations, locking, atomic replacement,
validation, rollback, update stamps, and revisions. Add failure injection and
multi-process tests for every write boundary.

Gate: Ruby and Go produce identical store bytes and equivalent typed outcomes
for every mutation corpus; real competing Go processes cannot lose writes or
deadlock.

### Phase 4: port journal and merge behavior

Read existing Ruby journal state, implement undo/redo and coalescing, then port
the JSONL merge driver and installer. Add algebraic property tests and
cross-version tests in which Ruby writes and Go undoes or merges, then the
reverse.

Gate: byte-exact cross-version history and merge proof passes on macOS and
Windows filesystem adapters.

### Phase 5: replace the CLI and HTTP adapters

Implement the full CLI specification, including aliases, fuzzy refs, friendly
dates, dry-run, human output, JSON envelopes, exit codes, config, links, and
agent invocation. Implement the OpenAPI 3.1 server using `net/http`, preserving
the 25 path templates, ETags, header defenses, body limits, errors, logging,
health checks, and server-sent events.

Run both black-box suites in isolated processes. Keep HTTP dependencies out of
the CLI and TUI package graph even though `net/http` is in the standard
library; entrypoint isolation remains useful architecture.

Gate: the Go binaries pass the existing CLI and OpenAPI contracts plus
cross-process store/revision tests. The Ruby binaries remain the production
default.

### Phase 6: add native bindings and a device proof

Expose a deliberately small application facade through `gomobile bind` or a C
ABI. Build an XCFramework for device and simulator plus an Android AAR. In a
throwaway host app, exercise list, create, edit, complete, reorder, recurrence,
undo, invalid-store refusal, and app restart against an app-container store.

Gate: release builds pass on a physical iPhone and Android device, stay off the
UI thread during blocking work, leak no bridge objects under repeated use, and
produce the same canonical data as desktop Go.

### Phase 7: rebuild the TUI

Construct the Bubble Tea shell and migrate one vertical interaction at a time:
read-only navigation, detail panel, mutations, editor, palettes and modals,
mouse, themes, archive/history, agent prompt, and activity queue. Compare
rendered frames at fixed terminal sizes and exercise real key, paste, mouse,
resize, file-change, and process-completion messages.

Build the shell inside `pkg/tui` against the embedding contract from the
first slice: injected theme and renderers, keymap registry with export
surface, hit regions computed from the delivered size, no terminal-level
commands outside the standalone entrypoint.

Gate: every documented shortcut and edit flow works against the Go application
layer; 72-column, narrow, `NO_COLOR`, themed, and wide-character proof is
captured; long-running agent work never blocks input or painting; the
embedding smoke test mounts the root model in a trivial host with offset
mouse events and consumes its exported bindings.

### Phase 8: certify and cut over

Produce clean-install builds for macOS and Windows, mobile library archives,
checksums, SBOM and license reports, shell completions, and operator docs.
The Go binaries become an opt-in preview under their `-go` names once all
application and persistence phases are green and the remaining gaps are named
adapter features; opting in and reverting must each be one step, with Ruby
still authoritative.

Promote Go to the public command names only after:

- every manifest entry needed by the chosen release surface is green;
- the mainline-parity queue is empty;
- Ruby and Go pass the same clean fixture corpus;
- the preview has survived normal daily use with mismatch reporting enabled;
- macOS and Windows artifact and install tests pass;
- a copy of the live store has completed write, undo, archive, merge, and
  recovery rehearsals; and
- rollback to Ruby has been executed against data last written by Go.

Back up the real store and journal, rehearse rollback, switch one desktop to
Go, and verify the CLI, TUI, API health, external file refresh, undo, and Git
merge path before changing the other installation.

Gate: an independent review clears core correctness, adapters, packaging, and
rollback evidence. Retire Ruby only after the compatibility window closes and
no rollback path still depends on it; removing the Ruby files is the last
housekeeping step, not the definition of success.

## Verification stack

The Go port needs several layers of proof:

- `go test ./...`, race-enabled tests on desktop, formatting, vet, and lint;
- property and fuzz tests for parser/dumper, recurrence, trees, revisions, and
  merge rules;
- fault-injection tests for store and journal transaction boundaries;
- Ruby-versus-Go conformance for CLI, HTTP, files, and history;
- real-process contention and stale-write tests;
- deterministic Bubble Tea model tests plus rendered terminal fixtures;
- macOS and Windows clean-install smoke tests;
- iOS simulator, physical iPhone, Android emulator, and physical Android proof;
- dependency license and vulnerability checks; and
- fresh independent review at the end of each risky phase, not only after the
  final cutover.

Not every slice runs every layer. The playbook's minimum-loop table maps
slice risk to required proof: read-only slices stop at differential
conformance plus one combined review, and the full stack is reserved for
anything that writes. Ceremony beyond a slice's risk tier is cost without
evidence.

The migration is complete when Ruby can be removed without losing a contract,
not when the Go binaries look feature-complete.

## Decisions that can wait

The port does not need to choose the graphical toolkit, remote hosting model,
account system, cloud database, or sync protocol. Native bindings keep Flutter,
Tauri, React Native, or a platform-specific UI available. The existing OpenAPI
surface remains useful for desktop clients and automation.

The sidecar plugin itself also waits. The port ships an embeddable component
and the smoke test that proves it; writing the actual `internal/plugins/tasks`
adapter in sidecar is a separate, small project once the standalone TUI has
passed parity — and a natural first exercise of the contract.

The first decision after this document is smaller: whether preserving `tasks`
across more platforms is valuable enough to justify a behavior-preserving core
port. If yes, Phase 1 is the honest estimate. A working conformance harness will
show how large the product really is before the rewrite begins.
