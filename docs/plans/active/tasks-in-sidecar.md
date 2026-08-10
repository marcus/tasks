# Tasks in Sidecar implementation plan

- **Plan date:** 2026-08-04
- **Copied into tasks repo:** 2026-08-05 from
  `~/code/clara-home/projects/tasks-sidecar/implementation-plan.md`
  (companion overview: `~/code/clara-home/projects/tasks-sidecar/README.md`)
- **Tracking epic:** `td-29dcdb` (Tasks in Sidecar)
- **Architecture outcome:** approved with conditions
- **Primary condition:** align Tasks and Sidecar on Bubble Tea v2 before
  attempting to embed the Tasks model
- **Delivery shape:** five reviewable packets across the Tasks and Sidecar repos
- **Repos:** `~/code/tasks` (Packet 0) and `~/code/sidecar` (Packets 1–4)

### td task map

| Packet | Issue | Title |
|--------|-------|-------|
| 0.1 | `td-9c553b` | Upgrade Tasks TUI Bubble Tea v1 → v2 |
| 0.2 | `td-452d19` | Export public `pkg/tui` embeddable host package |
| 0.3 | `td-595a3b` | Export shortcut binding and command metadata |
| 0 ADR | `td-5aa68b` | ADRs for embeddable TUI API and Bubble Tea v2 |
| 1.1 | `td-a7894c` | Sidecar Tasks plugin lifecycle adapter |
| 1.2 | `td-bf37ae` | Sidecar Tasks config and deterministic tab order |
| 1.3–1.4 | `td-98ced9` | Keys, help, footer, modals, and key-conflict routing |
| 1 ADR | `td-1ef9fe` | Sidecar ADR for contextual plugin key precedence |
| 2 | `td-7b95b9` | Per-project Tasks context following |
| 3 | `td-3b67d9` | Provider-based td + Tasks work search |
| 4.1 | `td-a6bc69` | Open Tasks work-search result in Tasks tab |
| 4.2 | `td-8ba13a` | Create Sidecar workspace from a Tasks item |
| 4.3 | `td-96f94b` | Record workspace on task via `work_ref` |
| 4.4 | `td-346194` | Context-aware Tasks capture from mapped project |
| 4.5 | `td-07f0d3` | Distinct td and Tasks signals on project overview |

Dependency chain (high level): `0.1 → 0.2 → 0.3 → 1.1 → 1.2 → 1.3–1.4 → 2 → 3 → 4.x`.
Only `td-9c553b` is unblocked at creation time.

## The intended result

The first useful version is deliberately simple: Sidecar has a Tasks tab that
behaves like the standalone Tasks TUI. It can sit after Workspaces or after
Notes, selected through configuration. Its keys appear in Sidecar's command
palette and footer, its text-entry and modal modes receive keystrokes correctly,
and its agent provider/model controls still work.

After that is stable, Sidecar can apply an explicit set of Tasks `@contexts`
when the active software project changes. A later packet can turn Sidecar's
current project-local `td` issue search into a provider-based work search that
also returns Tasks items. Workspace linking and other cross-feature behavior
comes after those seams have proved useful.

## Current facts that constrain the design

- Tasks is Go-only on `main` and released as `v1.0.0`. Its CLI, TUI, API,
  journal, merge driver, and copied-real-data behavior share one application
  and store implementation.
- The Tasks TUI is a client of the application facade. It already owns the
  interaction model Sidecar needs: six views, selection, detail and edit
  panels, context filtering, help, action palettes, task-specific modals,
  undo/redo, agent prompts, and provider/model cycling.
- Tasks has one declarative shortcut registry. Help is generated from it; the
  direct keys and action palette dispatch through it. Duplicating that table in
  Sidecar would guarantee drift.
- Sidecar's plugin contract already covers lifecycle, Bubble Tea updates,
  rendering, commands, focus contexts, text-input ownership, diagnostics, and
  project-switch epochs.
- Sidecar's `td` plugin is the reference integration. It constructs the
  embedded model asynchronously, exports bindings and command metadata into
  Sidecar, maps the model's current focus context, suppresses nested quit, and
  constrains rendering to the plugin's allotted height.
- Sidecar registers tabs in a hard-coded order. Workspaces is always registered;
  Notes is currently feature-gated. A Tasks position setting therefore needs a
  small registration-order seam.
- Sidecar's global issue search is currently a `td`-specific subprocess and
  data model in `internal/app/issue_preview.go`. It searches the active repo
  only.
- Tasks uses Bubble Tea v1 today. Sidecar uses Bubble Tea v2. Their message,
  command, and model types are not interchangeable.

## Ownership and boundaries

| Concern | Owner | Boundary |
|---|---|---|
| Task data, locking, validation, mutations, undo, recurrence | Tasks | Tasks application/store only |
| Task list/detail/edit UI and task-specific overlays | Tasks | Public embedded TUI package |
| Task shortcuts, descriptions, contexts, availability | Tasks | Exported binding and command metadata |
| Tab lifecycle, order, header, footer, command palette | Sidecar | Sidecar plugin interface |
| Active software project and worktree identity | Sidecar | `plugin.Context` and project config |
| Mapping a software project to GTD contexts | Sidecar | Explicit per-project configuration |
| Combined `td` + Tasks result presentation | Sidecar | Search-provider interface |
| `td` issue retrieval | `td` | Existing CLI/monitor adapter |
| Tasks retrieval for aggregate search | Tasks | CLI JSON first; replaceable provider seam |

Two rules follow from this table:

1. Sidecar never reads or writes Tasks JSONL itself.
2. Tasks never imports Sidecar types. Its embedding API stays useful to another
   Go host.

## Target runtime shape

```text
Sidecar app
  ├─ tab/header/footer/help and project lifecycle
  ├─ Tasks plugin
  │    └─ github.com/marcus/tasks/pkg/tui
  │         ├─ Tasks shortcut registry and interaction model
  │         ├─ Tasks application facade
  │         └─ Tasks store + journal ──▶ configured tasks data
  ├─ td plugin ──▶ td monitor
  └─ work search
       ├─ td provider ──▶ current repo
       └─ Tasks provider ──▶ configured Tasks store
```

The Tasks tab and aggregate search are separate consumers. They share no
Sidecar-owned cache or task model. The task store remains the source of truth,
and Tasks' existing file-watch/read semantics keep the tab current when the CLI
or another process writes.

## Proposed configuration

The first packet should support tab enablement and placement. Project following
is parsed only when that phase is implemented.

```json
{
  "plugins": {
    "tasks": {
      "enabled": true,
      "position": "after-workspaces",
      "followProjectContexts": false,
      "defaultContexts": ["@home"]
    }
  },
  "projects": {
    "list": [
      {
        "name": "sidecar",
        "path": "/Users/marcus/code/sidecar",
        "tasks": {
          "contexts": ["@sidecar"],
          "view": "next"
        }
      }
    ]
  }
}
```

Semantics:

- `enabled` is false by default for the public Sidecar release. Marcus's local
  config enables it. This avoids giving every Sidecar user a tab for a product
  they may not have configured.
- `position` accepts `after-workspaces` or `after-notes`; the default is
  `after-workspaces`. If Notes is disabled, `after-notes` appends Tasks after
  Workspaces rather than hiding the tab.
- Sidecar does not accept a JSONL path. The embedded Tasks package resolves the
  normal Tasks configuration, including its refusal when no store is configured.
- `defaultContexts` is a Sidecar view preference. It does not change task data.
- Per-project `tasks.contexts` are explicit. Sidecar never guesses a GTD context
  from a repository basename.
- Worktree changes inside one configured project use the main project binding.
- The Sidecar-hosted Tasks model uses its own session namespace. It must not
  overwrite `$XDG_STATE_HOME/tasks/tui.json`, which belongs to the standalone
  TUI.

## Packet 0: make the Tasks TUI embeddable

This work belongs in `~/code/tasks` and must land before the Sidecar plugin.

### 0.1 Align the terminal stack

- Upgrade Tasks from Bubble Tea v1 to the same Bubble Tea v2 import family
  Sidecar uses.
- Update Bubbles/Lip Gloss dependencies only where Tasks actually consumes
  them; do not make this a general visual redesign.
- Preserve the standalone `tasks-tui` screen and interaction contract through
  fixture-driven model tests and fixed-size terminal captures.
- Audit printable input using the v2 event's printable text, while shortcut
  matching uses the canonical key string. Space, Unicode, multi-rune input,
  paste, and CSI navigation need regression coverage.

The compatibility check is binary: an external v2 host must be able to pass
`tea.Msg` values into the Tasks model without translation.

### 0.2 Export a narrow host package

Create a public package such as `github.com/marcus/tasks/pkg/tui`. It may wrap
the existing internal implementation; callers must not import `internal/tui`.
The public surface should provide:

- `NewEmbedded(EmbeddedOptions)` with normal Tasks configuration resolution;
- `Init`, `Update`, and a size-aware `View` suitable for composition;
- `Close`, which saves the host-specific session and shuts down any agent queue;
- exported command and binding metadata derived from the existing shortcut
  registry;
- the current focus context and whether that context consumes text input;
- a programmatic way to set initial view/context filters without changing task
  records; and
- theme/render options expressed as Tasks-owned semantic values, not Sidecar
  types.

Suggested options:

```go
type EmbeddedOptions struct {
    SessionNamespace string
    InitialView      string
    InitialContexts  []string
    SuppressFooter   bool
    SuppressQuit     bool
    Theme            ThemeOptions
}
```

The actual names can follow Tasks conventions. The behaviors cannot be left
implicit:

- embedded quit requests are surfaced to the host or suppressed; they never
  terminate Sidecar;
- embedded mode can omit Tasks' own footer because Sidecar will render one;
- the standalone TUI continues to render its current footer and help;
- provider/model cycling (`M`), agent activity (`A`), and prompt submission use
  the same Tasks-owned queue as the standalone TUI; and
- host shutdown terminates provider processes and preserves no orphan queue.

### 0.3 Export shortcut state, not another shortcut table

Model this on `td/pkg/monitor/keymap.ExportBindings` and `ExportCommands`.
Tasks needs stable host contexts for at least list, detail, task edit, modal,
modal filter, form/picker, context picker, prompt, and response/activity views.
Each exported command carries:

- stable command ID;
- short footer label;
- full help description;
- current context;
- priority for footer truncation; and
- its default binding or bindings.

Help in Sidecar is then a projection of Tasks' registry. A key change in Tasks
updates the standalone help, Sidecar command palette, and Sidecar footer in one
commit.

### Packet 0 acceptance

- `tasks-tui` retains its existing views, shortcuts, modals, agent/model
  controls, session behavior, and mouse behavior.
- A tiny out-of-module fixture imports `pkg/tui`, constructs an embedded model
  against copied fixture data, drives keys, renders, saves, closes, and exits
  without a process leak.
- Exported help/bindings contain no hand-maintained duplicate of the shortcut
  registry.
- Go tests, race tests for the model/store boundary, vet, fixed-size render
  comparison, and an independent code review pass.

## Packet 1: add the configurable Sidecar tab

This work belongs in `~/code/sidecar` after a released or pinned Tasks embedding
API exists.

### 1.1 Add the plugin and lifecycle adapter

Create `internal/plugins/tasks`. Follow the `tdmonitor` lifecycle shape:

- `Init` stores context and clears state; it does not open files or spawn a
  process before Sidecar's first frame.
- `Start` builds the Tasks model in a `tea.Cmd` and returns an epoch-tagged ready
  message.
- stale ready messages from a project switch are closed and discarded.
- `Update` forwards window, key, mouse, tick, paste, and Tasks queue messages.
- nested `tea.Quit` is suppressed or translated into Sidecar's quit flow.
- `Stop` closes the Tasks model, saves its Sidecar session, and shuts its queue.
- `View` constrains width and height so the Sidecar header/footer cannot scroll
  away.
- missing/unconfigured Tasks state produces a clear diagnostic and setup hint,
  never an empty list that looks authoritative.

Use a generic Tasks theme adapter. Tasks-owned overlays remain Tasks-owned; only
Sidecar-specific setup/error dialogs should use Sidecar's modal library. This is
an intentional boundary, not an exception that lets new Sidecar business logic
grow inside foreign modals.

### 1.2 Add configuration and deterministic tab order

- Add `TasksPluginConfig` to config, raw loader, merge, validation, saver, and
  tests.
- Replace scattered conditional registration with one small ordered plugin
  assembly function.
- Insert Tasks after the configured anchor with deterministic fallback when
  Notes is absent.
- Persist/restore the active Tasks tab through Sidecar's existing per-project
  active-plugin state.
- Document the config and tab shortcut number as derived state; do not promise
  one fixed number when preceding plugins are disabled.

### 1.3 Integrate keys, help, footer, modals, and models immediately

This is part of the first tab packet, not polish for later.

- Register every exported Tasks binding with Sidecar's keymap.
- Convert every exported command to `plugin.Command`; the Sidecar command
  palette (`?`) becomes the merged contextual help surface.
- Return the embedded model's current focus context from `FocusContext()`.
- Implement `ConsumesTextInput()` for prompt, filter, edit, form, picker, and
  modal-filter modes so Sidecar never steals typed characters.
- Let Sidecar's unified footer select the highest-priority Tasks commands. The
  embedded model suppresses its own ordinary footer to avoid duplication.
- Keep Tasks' task-specific overlays inside the plugin bounds and change focus
  contexts while they are open, so their own keys and footer hints remain true.
- Preserve Tasks' provider/model selector and agent activity model. The `M` and
  `A` commands must appear in the command palette and footer when available.

### 1.4 Resolve key conflicts deliberately

Sidecar currently handles several global keys before a plugin can see them.
Tasks uses some of the same keys. Add a table-driven routing test and adopt this
precedence:

1. an open Sidecar application modal;
2. the active plugin's text-input or blocking-overlay context;
3. an active plugin contextual binding;
4. Sidecar global bindings; then
5. unbound input forwarded to the plugin.

Initial conflict decisions:

| Key | Tasks meaning | Sidecar meaning | In Tasks list/detail |
|---|---|---|---|
| `?` | Tasks shortcuts | command palette/help | Open Sidecar's merged contextual help |
| `@` | Tasks context picker | project switcher | Tasks context picker wins; project switch remains in help/palette and user-overridable |
| `1`-`6` | Tasks views | Sidecar tabs | **Revised after live use: Sidecar tabs win.** Tasks views keep `←`/`→` and stay in the palette and merged help |
| `[` / `]` | unbound | unbound globally | Previous / next Sidecar tab, bound in the Tasks root contexts only; a bracket typed into a Tasks prompt, filter, or form is still a bracket |
| `q` | quit Tasks | quit Sidecar | Sidecar quit flow wins; embedded Tasks never exits the app |
| `tab` | Tasks prompt/edit traversal | no root Sidecar action | Tasks wins |
| `M` / `A` | model/activity | unbound globally | Tasks wins |

The numeric row is the revision this section anticipated: "If live use shows
that local numeric view keys are less valuable than tab switching, change the
mapping." Live use showed exactly that — switching tabs by number is muscle
memory across every other Sidecar tab, and one tab where `3` means something
else is a key you have to think about.

It was changed in the host's claim set (Sidecar's `shadowableGlobals`, which is
now `@` alone) rather than through a user keymap override. The override is the
right tool for one user's preference; this is the default every user gets, and
defaults belong in the shipped table. The Tasks registry was not forked either —
Tasks still binds `1`-`6` to its views, and Sidecar simply declines to let those
bindings shadow its own.

### Packet 1 acceptance

- With Tasks enabled, the tab appears after Workspaces by default and after
  Notes when configured.
- Every Tasks mode renders inside the allocated frame at narrow, ordinary, and
  wide terminal sizes.
- Direct keys, command-palette actions, footer hints, help descriptions, modal
  focus, text entry, mouse, provider/model switching, and quit behavior agree.
- External CLI writes refresh the embedded view without restarting Sidecar.
- Mutation tests use a temporary copied store. A read-only smoke may use the
  configured real store; tests never mutate it.
- Startup tracing shows no Tasks file walk, store open, or subprocess spawn
  before Sidecar's first ready frame.
- `go test ./...`, focused race tests, `go vet ./...`, Sidecar headless tmux
  captures, and independent review pass in both affected repos.

## Packet 2: follow project context

Add this only after the standalone tab has been used long enough to establish
that embedding and shortcut routing are sound.

### 2.1 Add explicit project bindings

Extend `ProjectConfig` with a nested Tasks binding containing `contexts` and an
optional initial `view`. Add plugin defaults and `followProjectContexts`.
Loader, saver, add-project flow, docs, and round-trip tests all need the new
fields.

Resolution uses Sidecar's configured main project, not the current worktree
directory:

```text
current WorkDir → main ProjectRoot → matching projects.list row → tasks binding
```

No match means use `plugins.tasks.defaultContexts`. An explicit empty context
list means show the unfiltered Tasks view.

### 2.2 Keep automatic and manual state separate

Do not rewrite the standalone Tasks session or change any task's contexts.
Sidecar should create one Tasks session namespace per configured software
project. The project mapping supplies the initial filter; manual changes through
`@` remain Sidecar-local for that project.

Recommended precedence on each project switch:

1. explicit project binding;
2. saved Sidecar-local filter for that project, when no explicit binding exists;
3. plugin `defaultContexts`;
4. no filter.

An explicit binding therefore stays deterministic. A user who wants a manually
remembered filter omits it.

### 2.3 Reinitialize safely

- Increment and capture Sidecar's plugin epoch before asynchronous model work.
- Close the old embedded model and queue before adopting the new project
  session.
- Discard late refresh/search/model messages from the previous epoch.
- Preserve selection only when the same stable task remains visible.
- A project switch while a dirty Tasks edit is open must use Tasks' existing
  explicit save/discard confirmation; Sidecar must not silently throw the edit
  away or apply it under the new context.

### Packet 2 acceptance

- Switching Sidecar projects changes the Tasks filter exactly when configured.
- Switching worktrees inside one project does not change the Tasks filter.
- Unmapped projects follow the documented fallback without leaking the previous
  project's explicit context.
- Standalone `tasks-tui` session state is byte-identical before and after a
  Sidecar session.
- Dirty edit, queued agent work, rapid project switching, stale async messages,
  and missing-context cases have tests and headless runtime proof.

## Packet 3: turn global `td` search into work search

Sidecar's current `i` modal can become provider-based without changing its
first-stage behavior.

### 3.1 Extract a provider interface

Replace `issueSearchCmd`'s `td`-specific result with a typed result:

```go
type WorkSearchResult struct {
    Provider string
    ID       string
    Title    string
    State    string
    Kind     string
    Updated  time.Time
}

type WorkSearchProvider interface {
    Search(ctx context.Context, query string, opts SearchOptions) ([]WorkSearchResult, error)
    Preview(ctx context.Context, id string) (WorkPreview, error)
    Open(id string) tea.Cmd
}
```

Keep the current `td` subprocess behavior behind the first provider. Add a Tasks
provider behind a feature flag. Provider errors degrade independently: a
missing Tasks binary cannot erase healthy `td` results, and a broken `td` repo
cannot make Tasks disappear.

### 3.2 Use the Tasks CLI JSON contract first

For this user-triggered search path, the CLI is a good adapter and keeps
Sidecar out of Tasks internals:

- open items: `tasks list --open --body /<query> --json`;
- include closed: `tasks list --all --body /<query> --json`;
- preview: `tasks show <id> --json`.

Pass every token as a separate process argument. Do not build a shell command.
If project contexts are active, filter the returned JSON with OR semantics to
match the Tasks TUI; the CLI's repeated `@context` filters use AND semantics.
If subprocess cost becomes visible, replace only the provider adapter with a
Tasks public read facade. The search UI and result contract should not change.

### 3.3 Make mixed results legible

- Rename the modal and docs from “issue search” to “work search”; keep `i` as
  the default shortcut unless live use finds a conflict.
- Prefix or badge each result as `td` or `tasks`. IDs are namespaced; a matching
  short ID can never open the wrong product.
- Preserve `ctrl+x` as “include closed” for both providers.
- Default Tasks results to the active project context mapping when one exists.
  Add a discoverable command to widen Tasks to all contexts for this search.
- Opening a `td` result continues to focus the `td` plugin. Opening a Tasks
  result focuses the Tasks tab and selects the stable task ID.
- Merge provider results deterministically. Recency can break ties inside a
  provider, but Sidecar should not pretend incomparable `td` and GTD priorities
  share one numeric score.

### Packet 3 acceptance

- Existing `td` search behavior and result opening remain intact when the Tasks
  provider is disabled.
- Mixed search returns, previews, and opens both record types by stable ID.
- Open/closed and project-context scopes are visible in the modal and help.
- One failed, slow, or missing provider cannot block the other; stale queries
  and project switches discard late results.
- Tests cover escaping, Unicode, empty results, duplicate IDs across providers,
  provider timeouts, and result ordering.

## Packet 4: deeper workflow integration

Do these as separate small features after context following and mixed search
have real use. The likely order is:

1. **Open a Tasks result in the Tasks tab.** This is already needed by Packet 3
   and establishes a generic cross-plugin selection message.
2. **Create a Sidecar workspace from a Tasks item.** Generalize the current
   `td`-shaped workspace link to a typed `WorkItemRef{provider, id, title}`.
   Never store a Tasks ID in a field whose contract says `td` issue.
3. **Record the workspace on the task.** Use Tasks' `work_ref` command/application
   capability after explicit confirmation. Tasks owns that mutation and undo.
4. **Context-aware capture.** When invoked from a mapped project, offer the
   mapped context and project as defaults in the Tasks-owned capture form. Do
   not attach them silently.
5. **Project overview signals.** Add separate `td` and Tasks counts or badges to
   a future cross-project overview. Keep engineering issue state and personal
   GTD state visually distinct.

Avoid a generic “unified task” model. `td` issues and Tasks items overlap in the
UI but have different lifecycles, ownership, persistence, and meaning. A typed
work-item reference and provider interface are enough.

## Verification strategy

Each packet needs proof at the layer it changes.

### Tasks repository

- unit/model tests for every exported context, binding, and lifecycle method;
- standalone TUI regression suite;
- external-module compile and drive fixture;
- temporary-store mutation and undo/redo tests;
- queue shutdown/process leak test;
- fixed-size render captures before and after the v2 migration; and
- full Go test, race, vet, and independent review.

### Sidecar repository

- config default, parse, validation, save, and round-trip tests;
- deterministic registration order for every enabled/disabled anchor case;
- plugin lifecycle, epoch, stale-message, close, and restart tests;
- table-driven shortcut precedence covering every conflict above;
- footer truncation and command-palette metadata tests;
- narrow/wide/short rendering and modal containment tests;
- text input tests for Space, Unicode, paste, and multi-rune key text;
- global search provider isolation and typed-open tests;
- startup trace proving asynchronous initialization;
- `scripts/tmux-drive.sh` captures of the real Sidecar tab; and
- full Go test, focused race, vet, lint policy, and independent review.

### Live consumer proof

Use the installed binaries, not only repo entry points:

1. Install a reviewed Tasks build that exports the embedding package and verify
   standalone `tasks`, `tasks-tui`, and the configured store.
2. Build/install Sidecar against that exact Tasks version.
3. Launch Sidecar from a foreign working directory and open the Tasks tab.
4. Verify help, footer, context picker, task detail/edit, undo, one safe fixture
   mutation, provider/model switching, and project switching.
5. Make a read-safe CLI change against a copied store and prove the tab refreshes.
6. Capture the screen and startup trace, then inspect logs for leaked provider
   processes or repeated poll chains.

All code changes require an independent review before their packet is complete.
Tests alone do not close a packet.

## Risks and controls

| Risk | Control |
|---|---|
| Bubble Tea v1/v2 incompatibility | Packet 0 blocks embedding until one v2 message model is proven externally |
| Shortcut drift | Export Tasks' registry; no Sidecar duplicate |
| Sidecar steals text or modal keys | Explicit focus contexts, `ConsumesTextInput`, and routing-precedence tests |
| Two footers/help systems disagree | Sidecar renders the embedded footer/help projection; Tasks remains the metadata owner |
| Tasks and standalone sessions overwrite each other | Sidecar-specific session namespace and byte-level regression test |
| Project mapping leaks across worktrees/projects | Resolve by main `ProjectRoot`, epoch async work, explicit fallbacks |
| Sidecar bypasses task validation | No direct JSONL; every write stays in the Tasks application/CLI |
| Startup becomes slow | Build embedded model in `Start()` command; measure first ready frame |
| Agent processes survive tab/project shutdown | Public `Close()` owns queue shutdown and is tested |
| Go module version skew | Pin a released Tasks version in Sidecar and test the public API externally before upgrade |
| Combined search conflates unlike records | Typed providers, namespaced IDs, visible source badges, separate lifecycle labels |

## Delivery order and stop gates

| Order | Packet | Stop gate |
|---:|---|---|
| 0 | Tasks v2 + public embedded TUI | Standalone parity and external host proof |
| 1 | Sidecar Tasks tab + shortcut/help/footer/modal/model integration | Headless visual proof and real read-only consumer smoke |
| 2 | Per-project context following | No session leakage; rapid-switch and dirty-edit proof |
| 3 | Provider-based `td` + Tasks work search | Existing td search unchanged; provider isolation proven |
| 4 | Workspace/capture/overview links | One typed integration at a time, each reviewed separately |

Do not start Packet 3 or 4 to make Packet 1 feel complete. The tab is valuable
on its own. Let the context mapping earn its way into daily use before turning
Sidecar into a broader work aggregator.

## Decisions to record in the owning repositories

When implementation begins, add short ADRs in the repo that owns each choice:

- Tasks: public embeddable TUI API and host lifecycle contract.
- Tasks: Bubble Tea v2 migration and standalone compatibility promise.
- Sidecar: contextual plugin keys take precedence over global keys.
- Sidecar: explicit software-project to Tasks-context mapping.
- Sidecar: provider-based work search with typed `td` and Tasks results.

Those ADRs should link back to this plan and record the final API names and
tradeoffs. This packet remains the cross-repo sequence; it should not become a
second implementation source of truth.
