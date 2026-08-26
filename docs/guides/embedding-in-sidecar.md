# Embedding Tasks in Sidecar

You are changing the **embedded application**. Sidecar embeds the Tasks TUI as a
tab through `pkg/tui`, pinned to a released Tasks version. Everything in
`pkg/tui` is a compatibility surface, and several changes that compile cleanly
here break the host at runtime.

The mirror of this document, written for someone changing the host, is
`docs/guides/active/embedding-tasks.md` in the sidecar repo. The cross-repo plan
of record is [`docs/plans/active/tasks-in-sidecar.md`](../plans/active/tasks-in-sidecar.md);
the decisions are [ADR-0013](../adr/0013-public-embeddable-tui-api.md),
[ADR-0015](../adr/0015-embeddable-tui-fixes-from-the-first-host.md),
[ADR-0016](../adr/0016-footer-suppression-granularity.md), and
[ADR-0017](../adr/0017-view-key-hint-suppression.md).

## Your obligations, up front

1. **Never import Sidecar types.** `pkg/tui` contains Tasks-owned types and
   Bubble Tea v2 types only. The moment it imports a host, it stops being useful
   to the next one.
2. **Treat `pkg/tui` names and values as frozen.** Renaming a `FocusContext`,
   changing a command ID, or adding a context changes host routing. See
   [What "public" costs](#what-public-costs).
3. **Check every new key binding against Sidecar's global set** before you land
   it. A colliding key silently loses to Sidecar and the user never sees your
   command on that key. See [Adding a binding](#adding-a-binding-tasks-side).
4. **Tag and push before Sidecar can consume the change.** `go.work` will lie to
   you about this. See [Release timing](#release-and-version-timing).
5. **Drive the real app.** Unit tests did not catch a single one of the problems
   the integration actually produced. See [Verifying end to end](#verifying-a-change-end-to-end).

## The shape of the integration

```text
Sidecar app
  ├─ tab/header/footer/help and project lifecycle
  └─ Tasks plugin (internal/plugins/tasks)
       └─ github.com/marcus/tasks/pkg/tui
            ├─ Tasks shortcut registry and interaction model
            ├─ Tasks application facade
            └─ Tasks store + journal ──▶ configured tasks data
```

Two rules hold the seam together:

- **Sidecar never reads or writes `tasks.jsonl`.** It does not accept a data
  path in its config; `NewEmbedded` runs normal Tasks configuration resolution
  and refuses an unconfigured store. Every mutation goes through the Tasks
  application, so validation, canonical key order, DFS pre-order, the journal,
  and undo all still apply.
- **Tasks never imports Sidecar.** The host exchanges only `tea.Msg` values and
  strings with `pkg/tui`.

What the boundary buys: one shortcut registry (Sidecar's palette, merged help,
and footer are projections of `ExportCommands`, not a second table that drifts);
one store writer; and a Tasks release that another Go host could adopt unchanged.
`internal/plugins/tasks/plugin_test.go` in sidecar pins the no-direct-I/O claim.

The cautionary tale is in the same repo. Sidecar's older `td` integration owns
`plugins.td-monitor.dbPath` — a host config key naming a path into another
tool's storage. It is filed as `td-a3867c`, alongside `td-9fb430` (that default
now points at nothing since td moved off a single SQLite DB, so the setting
silently does nothing). That is exactly the coupling this boundary exists to
prevent, and it is why "Sidecar does not name a Tasks data path" is a rule and
not a preference.

## What "public" costs

Every exported name in `pkg/tui` is part of the contract:

| Surface | Notes |
| --- | --- |
| `NewEmbedded(EmbeddedOptions) (*Model, error)` | Resolves normal Tasks config; refuses an unconfigured store |
| `EmbeddedOptions.SessionNamespace` | Required, validated; isolates host state at `$XDG_STATE_HOME/tasks/hosts/<ns>/tui.json` |
| `.InitialView` / `.InitialContexts` | Presentation only; must never mutate records |
| `.SuppressFooter` | Blunt: removes the whole stack including the prompt input |
| `.SuppressKeyHints` | Removes only the key-hint row (ADR-0016). This is what Sidecar sets |
| `.SuppressViewKeyHints` | Removes only the `1`..`6` prefixes in the view bar (ADR-0017) |
| `.SuppressQuit` | Host owns quit; Tasks latches instead of returning `tea.Quit` |
| `.Theme` (`ThemeOptions{Name, Colors, ReplaceColors}`) | `Colors` is an overlay by default |
| `.Environment` | Determinism/test seam; nil snapshots `os.Environ` |
| `Model`: `Init`, `Update`, `View(w,h)`, `Close`, `Discard`, `Invoke`, `CommandAvailable`, `FocusContext`, `ConsumesTextInput`, `CurrentView`, `Contexts`, `QuitRequested`, `ClearQuitRequest`, `LoadError`, `Warnings` | |
| `ExportBindings` / `ExportCommands` / `ExportContexts` | Projections of the one shortcut registry: today 373 bindings, 328 commands, 15 contexts |
| `Binding`, `Command`, `ContextMetadata`, `FocusContext`, `View` | Field names and values are the contract, not just the types |

`Close` and `Discard` share one `sync.Once`: the first to run wins, the other is
a no-op returning that error. `Close` saves the namespaced session; `Discard`
does not. A host that builds models speculatively depends on that distinction —
do not collapse them.

### Changes that break the host while compiling fine

- **Renaming a `FocusContext` value.** `rootContexts` in sidecar's
  `internal/plugins/tasks/routing.go` names four contexts by constant, and
  root-ness is the allow-list intersected with what Tasks still exports. A
  renamed context is silently demoted to "overlay", so Sidecar's globals stop
  firing and `q` stops reaching the quit flow there.
- **Changing a command ID.** Sidecar filters `hostOwnedCommands` (`quit`,
  `quit-confirmation-reminder`, `open-help`) by ID, and its palette handlers
  call `Model.Invoke(id)`. A renamed ID either leaks a host-owned command into
  the palette or produces a palette entry that does nothing.
- **Changing a default binding.** Sidecar registers bindings for the footer and
  merged help. A key it will not route is withheld; a key it will route is
  advertised. Move a binding onto a Sidecar global and the command disappears
  from the footer without any test in this repo noticing.
- **Adding a context.** New contexts are unknown to the host's allow-list, so
  they are treated as blocking overlays (safe, but Sidecar globals will not fire
  there and `q` will not quit). If a new context should be root, it needs a
  matching sidecar change.
- **Changing what `ConsumesTextInput` reports.** It decides precedence level 2.
  Flip it wrongly and either Sidecar steals typed characters out of a Tasks
  editor, or a Tasks browsing context swallows every Sidecar global.

The routing table is **derived at runtime** from these exports rather than
hardcoded, so most of this surfaces as a test failure on the sidecar side rather
than as silent misrouting. The test is
`TestRoutingTableIsDerivedFromTheTasksRegistry` in
`internal/plugins/tasks/routing_test.go`: it asserts every exported context is
recognised, that text-input classification matches Tasks' own metadata, that no
text-input context is root, and that the root set is exactly `tasks-list`,
`tasks-detail`, `tasks-response`, `tasks-response-detail`. A rename fails there.
It fails **in the sidecar repo**, which is why the release order in this document
matters: you find out at the re-pin, not at your own `go test`.

## Shortcuts and the key contract

### The precedence ladder

Sidecar resolves a keypress in five levels; the first that handles it wins
(sidecar `docs/adr/0001-contextual-plugin-keys-take-precedence.md`):

1. an open Sidecar application modal;
2. the active plugin's text-input or blocking-overlay context;
3. an active plugin **contextual** binding (`ClaimsKey`);
4. Sidecar global bindings;
5. unbound input forwarded to the plugin.

Level 3 above level 4 is the substance of that ADR: a plugin showing a list of
tasks knows better than the shell what `j` means. Level 5 is why most Tasks keys
need no negotiation at all — Sidecar does not bind them, so they arrive.

### Host-reserved keys

`ctrl+c`, `q`, and `?` are never offered to a plugin from a non-overlay context,
whatever the plugin claims. The single definition is
`keymap.HostReservedKeys` in sidecar's `internal/keymap/hostkeys.go`; the Tasks
plugin aliases the same variable as defence in depth. Do not bind a new Tasks
root-context command to any of them and expect it to fire: `q` would swallow the
only way out of Sidecar, and `?` is the merged help that lists your command.
(Inside a Tasks overlay or text-input context, level 2 forwards everything except
`ctrl+c`, so `q` genuinely cancels a Tasks modal.)

### The conflict table

| Key | Who wins inside the Tasks tab | Why |
| --- | --- | --- |
| `@` | **Tasks** (`open-context-palette`) | The only Sidecar global a Tasks binding may shadow. Sidecar's project switcher stays in `?`/palette |
| `1`-`6` | Sidecar tab switching | Revised after live use; see below |
| `[` / `]` | Sidecar tab cycling from Tasks **root** contexts; **Tasks** in text-input/overlay contexts | Opt-in per context via `prev-plugin`/`next-plugin` bindings + `keymap.BracketTabCycleKeys` |
| `←` / `→` | **Tasks** (`prev-view` / `next-view`) | Not Sidecar globals; how views are stepped now that `1`-`6` are gone |
| `tab` | **Tasks** (`focus-prompt`) | No root Sidecar action |
| `M` / `A` | **Tasks** (`toggle-model`, `open-agent-activity`) | Not Sidecar globals |
| `K`, `W`, `#` | Sidecar | Accidental collisions, never in the conflict table |
| `ctrl+c`, `q`, `?` | Sidecar, always | Host-reserved |

`1`-`6` originally went to Tasks views. That shipped and lost: switching tabs by
number is muscle memory across every other Sidecar tab, and one tab where `3`
means something else is a key you have to think about. The Tasks registry was
**not** forked — Tasks still binds `1`-`6` to `view-agenda`…`view-inbox`; Sidecar
simply declines to let those bindings shadow its own. `SuppressViewKeyHints`
(ADR-0017) exists so Tasks can stop advertising them in its own view bar, but
Sidecar has **not yet adopted it**: the option landed after the newest published
tag, and Sidecar cannot pin what is not pushed. Publishing a tag that contains it
is what unblocks that one-line change.

`K`, `W`, `#` are the object lesson for why shadowing is opt-in. Claims were
originally availability-aware for every key, so in the same `tasks-list` context
`#` opened Sidecar's theme switcher with nothing selected and ran Tasks'
`delete-selected` with a task selected. A destructive command hiding behind a key
whose meaning depends on the selection is not a mapping anyone chose. Claims on a
Sidecar global are now unconditional per context.

### Advertise only what fires

Sidecar registers a Tasks binding **only if it will actually honour it**
(`registerableKey` in `routing.go`). The footer and merged help are both built
from registered bindings, so registering a key the host keeps would put a lie on
the most visible line in the app. A withheld binding does not withhold the
command: `Commands()` still exports it and `palette.BuildEntries` turns a
command with no binding into a **keyless palette entry**, invocable through
`Model.Invoke`. That is the escape hatch that keeps `view-quadrants`,
`raise-priority`, and friends reachable.

For this to work from your side, keep `ExportCommands` metadata honest —
`FooterLabel`, `Description`, and `FooterPriority` are what the host has to
render with when the key is gone.

### Adding a binding (Tasks side)

A new binding in a Tasks **root** context (`tasks-list`, `tasks-detail`,
`tasks-response`, `tasks-response-detail`) can collide. In an overlay or
text-input context it cannot — level 2 forwards everything but `ctrl+c`.

The Sidecar global set (`keymap.GlobalKeys`, `internal/keymap/hostkeys.go`) is:

```
` ~ 1 2 3 4 5 6 7 8 9 ? ! @ K W # ^ i q ctrl+c
```

Note `r` is deliberately **absent**: Sidecar's refresh yields to the plugin in
any context that binds `r` itself.

**If your new key is in that set and is not `@`, you lose it.** Not with an
error — the key just does something else, and Sidecar will not even advertise
your command on it. Your command stays reachable through `?` and the palette,
keyless.

To check:

```sh
# 1. The host's global set, verbatim.
sed -n '/var GlobalKeys/,/^}/p' ~/code/sidecar/internal/keymap/hostkeys.go

# 2. What the registry now exports for your key, in every context.
cd ~/code/tasks
cat > pkg/tui/zz_scratch_test.go <<'EOF'
package tui

import (
	"fmt"
	"testing"
)

func TestScratchBindings(t *testing.T) {
	for _, b := range ExportBindings() {
		if b.Key == "#" { // <- your key
			fmt.Printf("%-28s %s\n", b.Context, b.CommandID)
		}
	}
}
EOF
go test ./pkg/tui/ -run TestScratchBindings -v
rm pkg/tui/zz_scratch_test.go

# 3. Prove the host still routes as intended (after re-pinning; see below).
cd ~/code/sidecar
go test ./internal/plugins/tasks/ ./internal/app/
```

If the collision is one you genuinely want Tasks to win, it is a **host** change:
add the key to `shadowableGlobals` in sidecar's
`internal/plugins/tasks/routing.go` and add a row to
`TestClaimsKeyFollowsTheConflictTable`. Do not work around it here.

## Release and version timing

Sidecar consumes `github.com/marcus/tasks` as a pinned module version in its
`go.mod`, and resolves it through `go.work` (`use . ../tasks ../td`) for local
development.

### The trap

`go.work` masks an unpublished dependency. Your local Sidecar build resolves
`../tasks` from your working tree and passes; CI, a fresh clone, and every other
developer resolve the pinned tag and fail. **Before claiming a cross-repo change
works:**

```sh
cd ~/code/sidecar && GOWORK=off go build ./...
```

### Order of operations

1. Land the Tasks change on `main` in this repo, with a `CHANGELOG.md` entry.
2. Tag and push it: `RELEASE_VERSION=v1.X.0 make release`
   (see [`docs/releasing.md`](../releasing.md); the target fails closed unless
   the tree is clean, `HEAD` is live `origin/main`, and the changelog entry
   exists).
3. Confirm the tag is on the remote: `git ls-remote --tags origin | grep v1.X.0`
4. Re-pin sidecar and refresh `go.sum`:
   ```sh
   cd ~/code/sidecar
   GOWORK=off go get github.com/marcus/tasks@v1.X.0
   GOWORK=off go mod download github.com/marcus/tasks
   GOWORK=off go build ./...
   ```
   `github.com/marcus/tasks` is a public repo, so no `GOPRIVATE` is needed; if
   it ever becomes private, prefix these with
   `GOPRIVATE=github.com/marcus/tasks`.
5. Land the sidecar change.

**Never re-pin sidecar to a tag that is not pushed.** A local-only tag resolves
for you and for nobody else, and the failure appears as a CI break in the other
repo.

### Tag history

`v1.0.0` predates the integration. `v1.1.0` (initial `pkg/tui`), `v1.2.0`
(ADR-0015 fixes), `v1.3.0` (`SuppressKeyHints`), and `v1.4.0` (published release
superseding the three; their release workflows failed before publishing
artifacts) were all minted for this work. Sidecar's `go.mod` currently pins
`v1.3.0`; `SuppressViewKeyHints` landed after `v1.4.0` and is unpinned as of
this writing.

### Go directive coupling

Sidecar's `go` directive must be **>=** the tasks module's. Both are `go 1.26.0`
today. Raising it here forces a matching bump in sidecar's `go.mod` in the same
re-pin step.

## Verifying a change end to end

### Gates

```sh
# tasks
cd ~/code/tasks
go test ./...
go test -race ./...
go vet ./...
gofmt -l .
go build ./cmd/...
make install-local    # from the canonical main checkout only

# sidecar, after re-pinning
cd ~/code/sidecar
GOWORK=off go build ./...
go test ./...
go vet ./...
```

`pkg/tui/external_consumer_test.go` (`TestOutOfModuleConsumer`) is the one that
proves the module boundary: it runs `testdata/external-tui-consumer` with
`GOWORK=off`, drives keys, renders, saves, and closes, then asserts the session
landed under `hosts/external-proof/` and that `tasks.jsonl` is byte-identical.
`pkg/tui/external_host_contract_test.go` holds the host-facing behaviour
contracts (discard vs close, broken store vs empty store, each suppression
switch independently).

### Drive the real app

Unit tests are not sufficient, and this integration proved it repeatedly. Build
and drive the binaries.

**tmux discipline: use an isolated socket.** The machine's default tmux server
holds live Sidecar and agent sessions and must never be stopped, restarted, or
killed. Every proof runs on `tmux -L <name>`.

Sidecar ships a driver that isolates both axes — the outer socket *and* the
sessions Sidecar itself creates, plus its state tree
(`docs/guides/active/headless-testing.md` in that repo):

```sh
cd ~/code/sidecar
go build -o /tmp/sidecar-proof ./cmd/sidecar
./scripts/tmux-drive.sh start 200 50
./scripts/tmux-drive.sh keys 6      # tab number is derived from the plugin order — check the header
./scripts/tmux-drive.sh snap tasks-tab
./scripts/tmux-drive.sh stop
```

For standalone `tasks-tui` there is no driver; do it by hand on a private socket:

```sh
go build -o /tmp/tasks-tui ./cmd/tasks-tui
tmux -L tasks-proof new-session -d -x 200 -y 50 \
  "TASKS_DIR=/tmp/tasks-proof-store /tmp/tasks-tui"
tmux -L tasks-proof send-keys -t 0 '3'
tmux -L tasks-proof capture-pane -p -t 0
tmux -L tasks-proof kill-server        # this server only
```

### Use a fixture store

Anything that can mutate runs against an isolated `TASKS_DIR`, and you diff the
store before and after:

```sh
mkdir -p /tmp/tasks-proof-store
cp testdata/fixtures/valid/small-gtd/store/tasks.jsonl /tmp/tasks-proof-store/
cp /tmp/tasks-proof-store/tasks.jsonl /tmp/before.jsonl
# ...drive the app...
diff /tmp/before.jsonl /tmp/tasks-proof-store/tasks.jsonl
TASKS_DIR=/tmp/tasks-proof-store tasks check --all-files
```

Never point a proof at the configured real store except for a read-only smoke.

### What only showed up in the real app

- **Keys stolen by the host.** `1`-`6` reached Sidecar, not Tasks views. No Tasks
  test could see this; the registry still bound them correctly.
- **The footer advertising the wrong keys.** Sidecar's footer and merged help are
  built from registered bindings, so bindings the host wins were being advertised
  by the plugin that lost them — `#` labelled as delete, `1`-`6` as views. That is
  what produced `registerableKey` and the keyless palette entry.
- **A duplicated footer row.** Sidecar renders a unified key-hint bar; Tasks
  painted its own underneath. `SuppressFooter` was the only switch available and
  it also removed the prompt input, the agent transcript, the store-read banner,
  and the filter lines — `tab` focused an invisible caret. ADR-0016 exists because
  of that live observation.

## Change checklist (Tasks side)

- [ ] No Sidecar import crept into `pkg/tui`.
- [ ] Did you rename a `FocusContext`, a command ID, or a default binding? If so,
      a matching sidecar change lands in the same sequence.
- [ ] New root-context binding? Checked against `keymap.GlobalKeys`; not
      `ctrl+c`/`q`/`?`.
- [ ] New context? It will be treated as an overlay by the host until sidecar
      adds it to `rootContexts` — is that what you want?
- [ ] `ExportCommands` metadata (label, description, `FooterPriority`) is good
      enough to render without a key.
- [ ] `go test ./... && go test -race ./... && go vet ./... && gofmt -l .`
- [ ] `TestOutOfModuleConsumer` and the contract tests pass.
- [ ] Standalone `tasks-tui` driven on an isolated tmux socket against a fixture
      store; store diffed.
- [ ] Tagged and pushed **before** sidecar is re-pinned.
- [ ] `cd ~/code/sidecar && GOWORK=off go build ./... && go test ./...` after the
      re-pin.

## Known gaps and traps

- **A bare `[` can cycle Sidecar tabs.** Split SGR mouse escape sequences
  sometimes leak a lone `[` as a rune. `internal/tty/tty.go` and
  `internal/plugins/workspace/mouse.go` in sidecar carry mouse-proximity
  workarounds (drop a bare `[` within ~10 ms of a mouse event), but the
  app-level `isMouseEscapeSequence` in `internal/app/update.go` only matches
  sequences containing `[<` or digits+`;` ending in `M`/`m` — it does not filter
  a lone `[`. In a Tasks root context that leaked bracket now switches tabs.
- **`Commands()` costs about 0.8 ms** with a live model (measured: 248 commands,
  ~840 µs per call), and Sidecar calls it per render from `internal/app/view.go`,
  `internal/app/update.go`, and `internal/palette/entries.go`. Each call runs
  `ExportCommands()` plus a `CommandAvailable` check per current-context command.
  Do not make the export path more expensive without measuring.
- **`cancel-queued-agent-requests` has no default binding.** It is registered
  with `Sequences: nil` and `DisplayKey: "palette"`, so it exists only as a
  palette entry — in standalone Tasks and in Sidecar's merged palette alike.
- **Four boundary issues filed against sidecar during this work:**
  `td-a3867c` (Sidecar config owns a td store path, `plugins.td-monitor.dbPath`),
  `td-9fb430` (that path's default is dead config since td moved off a single
  SQLite DB), `td-0b7210` (two plugin enablement mechanisms: `plugins.X.enabled`
  booleans vs. `features.flags`, with Tasks on the latter as `tasks_plugin`), and
  `td-e3c390` (plugin config validation is inconsistent across plugins). The
  first is the same boundary violation this integration is drawn to avoid.
- **Root-ness is not derivable.** `ContextMetadata` carries only `Name` and
  `ConsumesTextInput`; there is no "is an overlay" bit, so sidecar keeps a
  hand-maintained allow-list that fails safe. If `pkg/tui` ever gains that bit,
  the allow-list can go.
