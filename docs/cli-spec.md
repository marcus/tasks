# tasks CLI — agent interface specification

The `tasks` CLI is the API for `tasks.jsonl`. Humans use it too, but the primary
audience is LLM agents. The CLI is the **only** writer: `tasks.jsonl` is a JSONL
store with per-record ids, a strict DFS pre-order, fixed key order, and a `meta`
line 1, so a hand-edit is error-prone by construction — every mutation has a
command. Commands go through the shared model layer (`lib/tasks/store.rb`), which
enforces conventions (e.g. dating an INBOX item promotes it to TODO) and validates
the file after every write.

Status legend: ✅ implemented · 🚧 planned (spec is authoritative for behavior
when it lands). `tasks check` is the escape hatch if the file is ever edited
out-of-band.

## Global conventions

**Invocation.** `bin/tasks <command> [args] [flags]` from the repo root (or the
`tasks` alias). Every command has a short alias. Synonyms are accepted where
an agent would plausibly reach for them (`done`/`complete`/`close` are the
same command); the canonical name is listed first. Unknown `--flags` are an
error (exit 1), never silently treated as positional args.

**File locations.** The task files don't have to live in this repo — the code
and your data are separable (so the project can be shared without the tasks).
Both the CLI and the TUI resolve `tasks.jsonl`/`archive.jsonl` through
`lib/tasks/config.rb`, highest precedence first:

1. `TASKS_FILE` / `TASKS_ARCHIVE` env vars (per-file; used by the test suite
   and for safe manual experiments).
2. `TASKS_DIR` env var — a directory containing `tasks.jsonl` and `archive.jsonl`.
3. Config file `~/.config/tasks/config` (or `$XDG_CONFIG_HOME/tasks/config`),
   `key = value` lines: `dir = ~/tasks`, or per-file `file = …` / `archive = …`.
   `~` expands; `#` comments (full-line, or inline after whitespace) and blank
   lines ignored — so a value can't contain ` #`; a bare `#` inside a value
   (e.g. a URL anchor) is fine.
4. Default: the repo root (current behavior).

The config file also carries non-path settings: `urgent_days = N` sets the
quadrants urgency window (see `quadrants`), overridable by the `TASKS_URGENT_DAYS`
env var, default 3. `max_depth = N` caps how deeply tasks may nest (integer ≥ 1),
overridable by the `TASKS_MAX_DEPTH` env var, default 4.

`timezone = Area/Location` sets the evaluation/display zone for floating times
and all-day boundaries. Resolution is `TASKS_TIMEZONE`, config, a valid IANA
`TZ`, the host `/etc/localtime` zoneinfo link, then `Etc/UTC` with a fallback
warning. `time_format = 12|24` controls human output; JSON always uses `HH:MM`
and RFC 3339. Full IANA identifiers are accepted for stored fixed values;
abbreviations such as `PST` are rejected.

A dotted `prompt.<name>` namespace toggles short facts injected into every agent
system prompt under a **Current environment** heading (see
[`prompt-context-injection.md`](plans/implemented/prompt-context-injection.md)):

```
prompt.datetime = on     # default: on — local `2026-07-15 Wed 08:41 PDT`
prompt.hostname = on     # default: on — Socket.gethostname
# prompt.weather = on    # future providers default off until registered as default-on
```

Truthy: `on` / `true` / `1` (case-insensitive). Falsy: `off` / `false` / `0`.
An invalid value is ignored (falls through to the registry default). Unknown
`prompt.*` names are ignored at resolve time (forward compatibility). A provider
that errors or returns blank is omitted silently; the rest of the block still
injects. Both `tasks -p` and the TUI queue assemble this through
`Tasks::AgentContext`.

Host-specific creation contexts use another dotted namespace:

```ini
host_context.marcus-home.local = @home
host_context.work-mbp = @work
```

Matching against `Socket.gethostname` is case-insensitive and tries the full
hostname before its first DNS label. Values without `@` are normalized. A
matched context is added to every task created through the CLI, TUI, or API,
alongside any explicit contexts. `capture --no-host-context` suppresses it for
one creation. `tasks config` reports the detected hostname, resolved context,
and matching config key.

Two dotted namespaces configure links (see `links`/`open`):

```
link.jira   = https://acme.atlassian.net/browse/%s   # shorthand: notes can say jira:OPS-1234
link.gh     = https://github.com/%s                  # gh:acme/app/pull/412
system.gitlab = gitlab.acme.io                       # classify this host as "gitlab"
```

`link.<name>` makes `<name>:<value>` in a task body expand through the URL
template (`%s`, or appended if the template has none) — descriptions stay
terse and one config edit re-points every link if a host changes. Names are
`[a-z][a-z0-9_-]*`; only configured names match, so ordinary prose ("note:
this") can't false-positive. `system.<name>` classifies a custom host (and its
subdomains) for self-hosted systems the built-in registry can't know; user
rows win over built-ins.

**TUI colors.** The TUI paints semantic *slots* (`lib/tui/theme.rb` lists them
all: `accent`, `selection`, per-view tabs like `tab_agenda` /
`tab_agenda_active`, intake headers `approval_section` / `inbox_section`,
task-row fields like `project`, `context`, `title`, the
`due_*` ladder plus selected-row variants such as `due_soon_selected`,
detail-panel slots like `panel_title`, `detail_label`, `description`, `link`, `link_system`,
`state_*`, …). Appearance keys in the same config file:

- `theme = <name>` — a named base theme: `default`, `mono` (attribute-only),
  or a generated popular scheme such as `dracula`, `nord`,
  `catppuccin-mocha`, `gruvbox-dark`, `tokyonight-night`, or
  `solarized-dark`. The generated names come from
  `scripts/generate-tui-themes`, which converts iTerm2-Color-Schemes
  Window Terminal JSON into tasks semantic slots. Overridable by `TASKS_THEME`;
  a non-empty `NO_COLOR` env var selects `mono` when nothing explicit is set.
- `mouse = on|off` — enable SGR mouse tracking in the TUI (default `on`).
  Overridable by `TASKS_MOUSE`. While tracking is on, unmodified terminal
  text selection is unavailable (use the terminal's bypass modifier, or turn
  mouse off). Wheel over the list moves the selection; a detached list
  viewport is not implemented yet.
- `color.<slot> = <spec>` — restyle one slot on top of the theme. A spec is
  space-separated tokens: attributes (`bold`, `dim`, `italic`, `underline`,
  `reverse`), a named color (`red`, `bright-red`, `gray`, …), a 256-color index
  (`208`), or hex (`#ff8800`); prefix a color with `on-` for the background
  (`on-blue`, `on-#1e2030`); `none` = unstyled. Example:
  `color.selection = black on-cyan`. Invalid values fall back to the theme
  default rather than erroring. Because a hex token follows a space, `color.*`
  lines are exempt from inline `#` comments.
- `color.border = <spec>` — the container chrome (the window frame, modals, the
  form box, and the palettes all share it). This is the solid fallback used when
  the terminal lacks truecolor, `NO_COLOR` is set, or the gradient is disabled.
  `none` (the stock default) leaves the border the terminal's own foreground.
- `color.border_gradient = <stop> <stop> [<stop>…] @<angle>` — an angled
  truecolor gradient swept across the whole chrome, e.g.
  `color.border_gradient = #7aa2f7 #bb9af7 @60`. Two or more `#rrggbb` stops set
  the sweep; `@<angle>` is the direction in degrees (0 = left→right, 90 =
  top→bottom). The outer corners are drawn rounded (`╭ ╮ ╰ ╯`). Set it to `none`
  to disable the sweep and fall back to `color.border`. A malformed value
  degrades to the solid border rather than erroring. Only rendered on truecolor
  terminals; `mono`/`NO_COLOR` never sweep it.

`tasks config` prints the resolved paths, `urgent_days`, `max_depth`, `theme`,
`mouse`, the effective IANA `timezone`, `time_format` (12 or 24), `date_order`
(`mdy` or `dmy` — see Dates and times), and tzdb version (+ any `color.*`,
link, and `prompt.*` overrides), and where each came from.
`--json` includes `prompt_facts` (the effective name→boolean map).

**Multi-device Git merge plumbing.** Every Store write stamps only task records
whose semantic fields changed with `updated=<RFC3339 UTC second>#<device>`, for
example `2026-07-16T14:03:11Z#home`. The device is the first alphanumeric token
of `TASKS_DEVICE` or the hostname's first DNS label. Existing records without a
stamp remain valid and are treated as oldest during a merge. `updated` is not
part of task revision/ETag fingerprints, and undo/redo restores exact journal
bytes without re-stamping.

**Determinism pins (conformance harness only).** A small set of `TASKS_PIN_*`
environment variables fixes the values that are otherwise nondeterministic —
the clock, minted ids, the journal's coalescing scope, and the hostname used for
host-context selection — so two runs of one command produce byte-identical
output. They exist for the Go-port conformance harness; with none of them set,
behavior is exactly as documented everywhere else in this spec. The complete
list, defaults, and rules live in
[`porting/specs/determinism.md`](../porting/specs/determinism.md).

`tasks merge-driver <base> <ours> <theirs> <pathname>` is an internal,
Git-invoked CLI-only adapter. It performs a deterministic field-level 3-way
merge by stable id and writes valid canonical JSONL to `<ours>`; hard failure
leaves `<ours>` untouched and exits 1. `bin/install-merge-driver [data-repo]`
registers the absolute command in that repository's local Git config after
verifying `.gitattributes` selects `merge=tasksjsonl`. This is intentionally
not an HTTP capability: it is local Git transport plumbing, not user-visible
task behavior. See the root README for setup and audit-log details.

The `delegation` marker is merged as **one atomic value** — the merged record
takes exactly one side's whole object, never a mix, which is what makes a
spliced two-owner claim impossible. The winner is chosen by a single total
order over the two values, *not* by "which side changed it" or by
last-write-wins; the base is consulted only to detect a removal. In order:

1. **removal absorbs** — if either side dropped a marker the base carried, the
   merge drops it. Owner `undelegate` is the always-wins override and the
   escape hatch for every rule below;
2. a **`claimed`** marker beats any non-claimed one, so a live claim is never
   silently downgraded to `ready` by a concurrent edit that did not go through
   revocation;
3. **two claims** — earlier `at`, then the lexicographically smaller
   `assignee`, then canonical bytes: the first claim wins, and the loser
   discovers it at its next worker-matched operation;
4. **two non-claims** — later `at`, then canonical bytes: the most recent owner
   intent.

Being a maximum over one total order, this is associative and commutative,
which is what makes "exactly one worker holds a claim" survive multi-device
merges. (The earlier rule — one-sided change wins, then last-write-wins — was
not: two devices syncing in different orders could converge on different claim
holders.) A merge event names the reason: `removal_wins`, `claim_holds`,
`earlier_claim`, or `later_intent`. Afterwards the marker is reconciled with
the rest of the merged record, which can additionally clear it with
`cleared_on_non_task`, `cleared_on_proposal`, or `cleared_on_close` (a merely
`ready` marker on a task the other side closed — the same normalization a local
close performs). `last_write` no longer appears for this field.

**Known residual.** A *third* device that concurrently closes the task can
still diverge, because the `state` merge's own rule (one-sided change wins →
terminal state wins → last-write-wins) is not associative, and the
`cleared_on_close` reconciliation reads the merged state. That predates
delegation and is a property of state merging, not of the delegation order.

**TUI interaction.** `Tab` always focuses the agent prompt, including while a
read-only task panel is open. `p` inserts the selected task's stable id into
that prompt, and `y` copies the same stable id to the clipboard.
`:` opens the searchable, context-aware action palette; typing filters the
available actions, the arrow keys choose one, Return runs it, and Escape
cancels. `@` opens a searchable, fixed-size context selector for GTD `@` tags
(for example `@work` or `@home`). Typing filters and relevance-ranks the stable
list; arrow keys move the `❯` cursor without reordering choices. `Space` toggles
the cursor context (`●` marks every staged selection), Return applies the staged
set, and Escape cancels staged changes. Typing a context and pressing Return
still replaces the active set in one compact interaction. A leading **Clear all
contexts** row clears the staged set with Space or clears-and-applies with
Return. Multiple selected contexts match any selected context (OR within the
context facet); the `/` text filter composes with that group using AND.
Selected contexts are persisted in the TUI session and restored on the next
launch; contexts that no longer appear in the task set are pruned individually
on save. In list mode Escape clears `/` first, then the context group.
Direct shortcuts and palette entries invoke the same registered
actions. Return opens the read-only task-detail panel on the right in every
view; list navigation stays active and refreshes the panel for each newly
selected task. Return or Escape closes it. The existing `d` date and `r`
recurrence quick actions remain available. The sixth and final **Inbox** tab
composes two visibly separate intake sections: **Approvals** first, containing
only inert PROPOSED tasks with an `a approve · r reject` hint, then accepted
**Inbox** captures with their existing tree and inline `@` contexts. `a`
approves the selected proposal and `r` rejects it; registered shortcut
availability keeps project capture and recurrence behavior on their eligible
rows. Repeated decisions advance through the visible proposal queue. Once it
is empty, approval follows the accepted task when that task is visible, while
rejection uses the nearest selectable Inbox row. Both decisions participate in
undo/redo. A session saved on the former `approvals` view restores to this
combined Inbox tab.

The final tab label always shows both scoped counts, including zeroes, with the
Inbox count first and approval count second: `Inbox 4 | 2`. Both counts use the
active `@` context group and `/` text filter. The Inbox count additionally uses
the current unavailable-shown setting and the same INBOX-and-available rule the
`inbox` command applies;
proposals remain visible regardless of availability because they await an
owner decision. So the label never advertises work the current filters hide,
and the Inbox number is what `tasks inbox` would list. It is deliberately *not*
a count
of painted rows, and on the list views the two differ in both directions: tree
mode rides non-matching descendants along under a matching anchor for context
(they are on screen but are not that tab's work), and collapsing an anchor hides
its descendant rows without emptying the inbox (the fold's own `(n)` marker
accounts for those). A zero count renders no badge at all.

**Editable task-panel behavior.** With a read-only task panel open, `e` enters
editing at the first editable field and `Shift-Tab` enters at the last. `Tab`
continues to focus the agent prompt. In edit mode, `Tab` and `Shift-Tab`
traverse in their respective directions. Leaving a changed field validates and
immediately saves that semantic field before focus moves; an unchanged field
moves without IO.
Validation errors and conflicts retain focus and the pending, copyable buffer.
Opening a picker, scrolling, resizing the terminal, or resizing the panel is
not blur and never saves.

Edit-mode keys are fixed as follows:

| Key | Contract |
|---|---|
| `Tab` / `Shift-Tab` | Validate and save on blur, then move forward/backward only after success. |
| `Ctrl-S` | Save the focused field in place. |
| `Ctrl-O` | Save the focused field if needed and finish editing, returning to the read panel. |
| `Ctrl-K` / `Ctrl-L` | Grow/shrink through compact → standard → wide → focus without blur; in task-edit text fields `Ctrl-K` intentionally shadows kill-to-end, while the agent prompt keeps its current `Ctrl-K`. |
| `Escape` | Close an inner picker first. A dirty field requires a confirming second Escape before only that buffer is reverted; a clean field leaves edit mode. |

Return on a scheduled or deadline row opens one structured temporal control.
It combines a calendar, 15-minute time adjustment, all-day/floating/fixed mode,
searchable full IANA zones, and an earlier/later fold choice that appears only
for an ambiguous civil time. Closing the control changes only the field buffer;
the complete temporal value is validated and saved atomically by the normal
save-on-blur rules. Direct text entry remains available.

The key reader treats `Shift-Tab` (`\e[Z`), CSI keys, and ESC-prefixed Alt
bindings as complete sequences, including when input arrives across reads, so
a partial sequence cannot become a destructive lone Escape. The editor is
bound to the selected task's stable ID. External changes to the same owned semantic slice conflict;
unrelated task or same-task field changes may be adopted without overwriting an
active buffer. Missing targets are never rebound to a neighboring row.

Quitting with `Ctrl-C`, or `q` while a resize-suspended editor exists, requires
an explicit visible confirmation before any unsaved field buffer is discarded.
Repeated quit keys do not confirm that prompt; `y`/Return confirms and
`n`/Escape cancels while retaining the draft.

Field ownership and order are contractual: Title owns `title`; Priority owns
`priority`; Available from owns `scheduled`; On hold owns only the indefinite
`defer` marker; Deadline owns `deadline`; each date owns its documented
INBOX/recurrence side effects; Recurrence owns
`recur`; Lead time owns `lead` (and nothing else — the anchor stays the two date
fields'); Contexts owns `@` tags; Tags owns other non-`defer` tags; Notes owns
exact raw `body`; and State owns `state`, `closed`, recurrence completion, and
documented lifecycle effects. State is last, keeping high-impact changes out of
ordinary traversal. Parent/subtree placement is not an editor field: nesting is
handled at the store/move level. Manual ordering will use dedicated structure
actions in the unfiltered Outline tab described under [Nesting](#nesting), not
an editable form field.

Panel sizing uses named modes with content-cell breakpoints: 48 or more cells
may render short labels and controls inline; 32–47 cells stack them; 32 cells is
the editable minimum. Below that, the layout promotes to focus mode when it can
supply the minimum, otherwise it stays read-only and reports the required
width. Resize preserves task identity, focus, buffer, cursor, errors, picker
state, scroll, and edit-session identity.

Every successful blur is durable immediately. Consecutive writes in one edit
session may coalesce into one undo entry only when their non-nil session key
matches and the new exact `before` bytes equal the journal tip's exact `after`
bytes. CLI/external writes, undo/redo, reopening the editor, or any byte mismatch
breaks the group. If a successful Location or State patch removes the task from
the current view, the app immediately exits editing, selects a deterministic
nearby row, returns to the read panel or list, and explains where the task went.

The generated `?` help comes from `lib/tui/shortcuts.rb` and includes the
detail-panel entry keys, every editor-owned key, and the panel resize actions.
The embedded `TermForm` boundary can be exercised independently with
`ruby examples/term_form_demo.rb`; that plain renderer is extraction proof, not
a stable public or gem API.

`x` previews the number of completed roots and descendants that would move to
`archive.jsonl`; `y` confirms, while `n` or Escape cancels without writing.

**Queued TUI agent requests.** Return in the agent prompt accepts the request
even while another request is running. The TUI executes accepted requests one
at a time in FIFO order; at most one autonomous harness may mutate the task
files at once. Each request snapshots the selected `provider:model` at submit
time, so `M` affects only subsequently submitted requests. The waiting queue is
capped at 100; a full queue or an unavailable selected harness leaves the
prompt intact and focused.

The footer streams the active request and reports the pending count. `A` opens
a scrollable, filterable Agent activity modal containing every retained prompt,
status, provider/model, and transcript. Results are session-only: the latest 50
finished requests plus every active/pending request remain available until the
TUI exits. Escape cancels only the active request and continues with the next
queued request. The action palette can cancel all waiting requests without
touching the active one. Quit with active or pending work requires explicit
confirmation, then cancels the live process group and discards the queue.

### LLM agent settings

`-p` and the TUI hand your request to an **agent** — an autonomous harness
(the local `claude` CLI, the Hermes agent, …) that acts on `tasks.jsonl` itself
through this CLI. Which harness and model are chosen from the same config file;
all keys optional, unknown keys ignored:

```
llm_provider = hermes            # selected harness (default: claude-cli)
llm_model    = qwen3.6:35b-a3b   # selected model (default: provider's first model)
claude-cli_models = sonnet,opus,haiku   # override a provider's model list
hermes_models     = qwen3.6:35b-a3b      # override Hermes' model list
cursor-cli_models = composer-2.5-fast    # override Cursor CLI's model list
hermes_command    = hermes       # override the binary a provider spawns
cursor-cli_command = agent       # override the Cursor CLI binary
hermes_provider   = ollama-launch # Hermes inference provider (passed as --provider)
ollama_url        = http://127.0.0.1:11434  # endpoint Hermes' availability probe hits
```

Built-in providers are `claude-cli` (models `sonnet/opus/haiku`), `hermes`
(default model `qwen3.6:35b-a3b`, driving a local Ollama model via Hermes' own
config), and `cursor-cli` (default model `composer-2.5-fast`). Cursor CLI uses
the local `agent` binary in non-interactive force mode; authenticate first with
`agent login` or `CURSOR_API_KEY`, and run `agent --list-models` to discover
model ids available to the current account. Its text output contains the final
assistant message rather than structured tool progress. The overall default
stays `claude-cli:sonnet`. The TUI's `M` key cycles the flattened
`(provider, model)` list. The header and agent activity use concise display
aliases for known entries (for example `claude:sonnet`, `cursor:grok`,
`cursor:composer`, and `hermes:qwen`) while configuration and CLI flags retain
the exact provider/model ids; unknown ids fall back to their full names. Adding
a new harness is one adapter class in `lib/llm/` plus a `Registry::DEFAULTS`
entry — see `docs/plans/llm-adapter-pattern.md`.

**Local models:** a pre-JSONL eval of models behind Hermes
(`eval/llm/results-2026-07-02.md`) selected `qwen3.6:35b-a3b` as the default
Hermes model. It was the only candidate that handled every tested task type
without corrupting the Org store, but it took roughly 2–4 minutes per request.
The overall default remains `claude-cli:sonnet`. Treat those scores as
historical until the harness is rerun against `tasks.jsonl`.

**Task refs.** Mutations take a `<ref>` — a case-insensitive substring of the
task title. Resolution rules:

- Exactly one open task matches → act on it.
- Zero matches → exit 2, message `no match: <ref>`.
- Multiple matches → exit 2, listing each candidate as `L<line>: <headline>`.
  The agent retries with a longer substring or an exact `L<line>` ref.
- `L<line>` (e.g. `L42`) targets the record on that 1-based file line — precise,
  but only valid until the file changes. Prefer titles.
- An exact `id` (e.g. `7f3a9c2e`) resolves unambiguously and is stable across
  edits — it wins over fuzzy title matching. Get one with `tasks id <ref>`.
- By default refs match **open** tasks only; `--include-done` widens.

**Task IDs.** Every record carries a stable 8-hex `id` field — the durable handle
for that task no matter how lines shift or the title changes. Migration and
`capture` mint them; `tasks id <ref>` is the repair path for a record somehow
missing one. Mutations locate their target by id (falling back to line + title
otherwise), so an out-of-band reflow or retitle can't misfire an edit onto the
wrong task. IDs must be unique — `check` reports a collision as an error.

**Dates and times.** Anywhere a date is accepted: `2026-07-15`, `2026/07/15`,
`07-15`, `7/15`, `7/15/2026`, `7/15/26`, `aug 1`, `august 1st`, `1 aug 2026`,
`aug 1, 2026`, `fri`/`friday`, `next fri`, `today`, `tomorrow`, `+3` (days from
today), `in 3 days`/`in 2 weeks`/`in 6 months`/`in a year`, `next week`, `next
month` (same day next month, clamped to the last day if the target month is
shorter), `next year`. Same parser as the TUI (`lib/tasks/dates.rb`). Bare
month-day (numeric or by name) in the past rolls forward a year; an explicit
year is always respected as-is.

Bare numeric dates with no 4-digit year (`7/15`, `7/15/26`) are ambiguous
between month-first and day-first — `date_order = mdy` (the default, US
month/day/year) or `date_order = dmy` in the config file, or `TASKS_DATE_ORDER`
env, picks the reading. `tasks config` reports the resolved value.

`due`, `schedule`, and timed `defer` also accept `today 5pm`, `tomorrow at
09:30`, `fri noon`, `2026-07-20 17:00`, and `2026-07-20T17:00`. A time without
a zone is floating in the configured evaluation zone. `--timezone
Europe/London` makes it fixed; `--floating` explicitly selects floating mode;
`--fold later` selects the later instant during an ambiguous DST fold. A bare
time is rejected, as are seconds, abbreviations, numeric offsets, unknown IANA
zones, and nonexistent local times. `TASKS_TIMEZONE` overrides the config's
`timezone`; `time_format = 12|24` affects human output only.
If a later configuration-zone change makes a stored floating civil time
nonexistent, CLI/API reads fail safely with a corrective error instead of a
trace or partial result. The TUI reports the same error and temporarily
projects in UTC so the value can be edited.

**Availability and deferral.** `scheduled` is the task's single
available-from/start/defer-until value; `deadline` is its independent due value.
An open task is available on and after the exact `scheduled` boundary, and a future value filters it
out of `agenda`, `next`, `quadrants`, `inbox`, and the default `list`. The
semantic `defer` marker now means an indefinite **On Hold** state
(Someday/Maybe), not another date. A task retains its lifecycle state while
unavailable.

Availability is ancestor-aware. A task is available only when neither it nor
any task ancestor has a future available-from date or an On Hold marker. Closed
ancestor rows are skipped for lifecycle rendering and their open descendants
are hoisted, but those ancestors remain in the ancestry chain for availability:
their timed and On Hold constraints still propagate. When several timed
ancestors block a task, the latest boundary wins; an On Hold marker wins over every
date or time. `defer <ref> <date-or-date-time>` sets availability without moving `deadline`;
`someday <ref>` holds indefinitely; `activate <ref>` clears the task's own hold
and any own future available-from date. `list --unavailable` (`--deferred/-D`
compatibility alias) reviews all effective blockers, while
`list --someday`/`--on-hold` matches only an own indefinite marker. In the TUI,
`Z` reveals unavailable rows and `z` accepts a date/time, `someday`, or `now`.

**Proposals and approval.** `PROPOSED` is a lifecycle category separate from
accepted open work. `propose` creates an inert task, while `approve` transitions
it to INBOX and `reject` transitions it to CANCELLED with a `closed` date.
Repeatable `--note` on `reject` (and on `cancel`) records withdrawal rationale
on the body, visible in `show` — the same join semantics as `propose --note`.
Proposals appear only through the explicit `list --proposed` scope, direct
`show`, and the approval section of the final TUI Inbox tab; they stay out of agenda, next, quadrants,
inbox, project rollups, Outline, and the default open list. They cannot recur
or transition directly to DONE. Approval/rejection are checked atomic Store
mutations with revision support in the application/API, undo history, and
leaves-first handling for proposal trees. A proposal is retained live until it
is approved or rejected; archive sweep treats it as a non-closed descendant,
so it cannot disappear inside a closed ancestor. HTTP clients use
`scope=proposed` and explicit `POST /api/v1/tasks/{id}/approve` or `/reject`
intent routes with `If-Match`; both return the transitioned task plus its new
ETag. Reject may optionally carry a `notes` body field.

**Delegation and pickup.** An accepted live task can carry one optional
`delegation` object naming who holds the next action: a person (`kind: human`,
an email `assignee`, status `delegated`) or the agent pool (`kind: agent`, an
authority `mode` of `refine`/`research`/`implement`, status `ready` until a
worker claims it and `claimed` after). `PROPOSED`, closed, and archived tasks
refuse delegation with an error naming the state; approval and delegation stay
two independent owner decisions. Human delegation sets WAITING by default
(`--keep-state` opts out) because that is exactly what WAITING encodes, and
replacing that person with the agent pool leaves WAITING again — agent-ready
work is actionable. Agent delegation otherwise never changes lifecycle state,
and a WAITING the owner set on an undelegated task is left alone. Closing a
task clears an
unclaimed marker (nothing happened yet) and retains a claimed or human one
verbatim as provenance into the archive, `work_ref` included.

**Recurring tasks roll the standing intent forward.** Completing a delegated
recurring task does not close it, so there is nothing to keep as provenance:
the next occurrence inherits the *intent* — the agent `mode`, or the person the
task is delegated to — always **unclaimed**, with a fresh `at` and **no**
`work_ref`. Anything saying the work already started belongs to the cycle that
just finished; carrying a claim over would hand the new occurrence to a worker
who never picked it up (invisible to `--agent-ready`, unpickupable by anyone
else). To stop a standing delegation, the owner `undelegate`s — completing a
cycle will not.

**Identity and reference validation.** `assignee`, the worker id, and
`work_ref` all refuse C0 controls, `DEL`, the C1 block, and Unicode whitespace
(NBSP, U+2028/U+2029, the ideographic space — Ruby's `\s` is ASCII-only, so a
plain "no whitespace" rule let all of those through). These strings are
rendered raw by `show`, `list --delegated`, the TUI detail panel, and the
conflict line that names a holder, so a worker id carrying `\e[2K\e[1A` would
rewrite the terminal of the agent that *lost* a claim race; the bytes are
refused at the schema boundary instead of sanitized at four surfaces. A human
`assignee` must additionally be address-*shaped* — non-empty local part,
exactly one `@`, dotted domain — so `@work` (muscle memory from the TUI's
context filter, one keystroke from silently moving a task to WAITING) and
`pat@localhost` are refused. `assignee`/worker ids are bounded at 200
characters, `work_ref` at 500. Nested keys inside `delegation` that this binary
does not know are **preserved** across a rewrite (a claim from an older binary
cannot silently drop a newer one's field) and reported by `check` as a WARNING,
not an error — the same forward-compatible posture the top-level schema has.

The one hard guarantee is **single pickup**: `claim` is a compare-and-set from
`ready` to `claimed` performed under the store mutation lock, so two workers
can never both believe they own a task; a list read never grants ownership, and
the loser gets a conflict naming the holder. There are deliberately no leases,
renewals, or liveness tracking — a crashed agent leaves a visibly claimed task
until the owner clears it with `undelegate` or `release --force`. `release`
requires the matching worker id unless the owner forces it, and revocation
wins: after `undelegate` a stale worker's `release`/`workref` fail their
worker-match precondition.

That last guarantee is about *this* device. Across devices the whole marker is
merged as one atomic value under a single total order (see Multi-device Git
merge plumbing above), and only `undelegate` — a removal — always wins there. In particular an owner's
`release --force` racing a worker's concurrent write on another device **loses**
the merge, because a `claimed` marker outranks a non-claimed one; a one-sided
release can be overturned when the devices meet. Use `undelegate` when you mean
"this is no longer theirs, whatever else happened".

All five operations are revision-aware typed
`Tasks::Application` commands (`delegate_task`, `undelegate_task`,
`claim_task`, `release_task`, `set_work_ref`), journaled and undoable; the two
composed ones (the WAITING default, a release note) fold their second write
into the same undo step. Heartbeat agents discover work through
`list --agent-ready --json` and read their authority from `claim --json`.

Normal create, move, and state operations cannot strand accepted descendants
beneath a proposed task. In particular, transitioning an accepted parent to
PROPOSED refuses while any accepted descendant remains. The explicit
leaf-first approval command is the narrow exception: it may temporarily accept
a proposed leaf beneath a proposed parent so the parent can be decided next.

Date-only deadlines remain on time for their whole calendar date. Timed
deadlines become overdue strictly after their resolved instant and sort by that
instant; same-day all-day deadlines sort after timed ones. Times affect task
semantics only. They do not schedule reminders or notifications.

**Recurrence.** A task *recurs* when it carries a `recur` schedule alongside a
`scheduled`/`deadline` date. The stamp on the task **is** the next occurrence;
the schedule only says how it advances on completion — nothing is materialized
ahead of time. One scalar field holds two stored shapes:

*Interval cookies* — `.+1w`, `++1m`, `+2d`. The prefix sets what the interval is
measured from on completion: `+` fixed (stored date + interval, one hop), `++`
catch-up (repeated until strictly future), `.+` from-completion (today +
interval). The suffix is a count plus a unit (`d`/`w`/`m`/`y`; months/years step
by calendar with day-clamp, so Jan 31 `+1m` → Feb 28).

*Calendar schedules* — advance to the next matching calendar date:
`w:mon,wed` (weekly on a day set), `2w:mon` (every Nth week, parity anchored on
the stamp's ISO week when the schedule is set and preserved by every roll),
`m:15` / `m:last` / `m:2tue` / `m:lastfri` (monthly by day-of-month, last day, or
ordinal weekday, comma-separable), `y:07-04` / `y:02:2tue` (yearly). Only two
prefixes exist, because "from completion" and "catch-up" coincide when the dates
are calendar-fixed: bare (the default) advances to the next match strictly after
**today**; `+` (one-hop) advances to the next match strictly after the **stored
date**, which may stay in the past when every missed occurrence must be
processed. `.+`/`++` on a calendar schedule are rejected with that explanation.
Edge rules: a numeric day the month lacks clamps to the month's last day (`m:31`
in April = April 30); an ordinal weekday the month lacks skips to the next month
that has one (`m:5fri`); `y:02-29` clamps to Feb 28 in non-leap years.

Input never has to be canonical. `recur`, `capture --recur`, and the API take
natural phrases (`every monday`, `every mon wed fri`, `weekdays`, `weekends`,
`the 15th`, `last day of the month`, `2nd tuesday`, `every 2 weeks on monday`,
`every july 4`; case-insensitive, filler words ignored) as well as the canonical
grammar, and one parser normalizes both to the single stored spelling. Bare
intervals (`weekly`, `2w`, `every 3 days`) default to `.+`; bare calendar input
defaults to the prefixless catch-up form. `off`/`none`/`never` clear the
schedule. Unparsable stored values degrade to non-recurring on read and are
reported by `check`, never auto-repaired.

Timed `++` catch-up compares the exact release/due boundary, and `.+` uses the
completion date in the value's effective zone. A nonexistent recurring wall time
(DST gap) skips to the next occurrence without changing its clock time or fixed
zone. When both dates are present the deadline carries the schedule and the
scheduled date shifts by the same offset, preserving lead time.

Completing a recurring task (`done`, or `state … DONE`) rolls its date forward
and **leaves it open** instead of setting `closed`; it logs a `- Did [date]` line
to the body since the task never closes. `cancel` still truly closes it
(stopping the recurrence). `recur <ref> <schedule>` sets/replaces the schedule
(`--from schedule` switches a bare *interval* to `+`; it does not apply to
calendar schedules); `recur <ref> off` clears it; `recur <ref>` with no schedule
previews the stamp and the occurrences after it; `recur --explain "<schedule>"` parses and projects
any schedule without touching the store; `list --recurring` reviews them. A
schedule that could never fire from the task's stamp — or would roll past the
storable year range — is refused at write time with the engine's reason, so
nothing lands that `done` could not roll. Dating commands
(`due`/`schedule`/`reschedule`) preserve an existing schedule. In the TUI, `r`
opens a recurrence popup on the selected task, a `↻` badge marks recurring
tasks, and completing one rolls it forward in place.

**Lead time.** A dated task can carry a `lead` span — a positive count and a
calendar unit (`3w`, `2d`, `1m`, `10y`), or a clock duration in hours (`5h`) —
saying how long *before* its occurrence date it becomes visible. The **anchor**
is the task's `deadline` if it has one, otherwise its `scheduled` date (the same
precedence a recurrence roll uses), and the window opens at midnight of
`anchor - lead` **in the anchor's own effective zone** — a deadline that lives in
Tokyo has a window that starts in Tokyo, whoever is reading; an all-day or
floating anchor has no zone of its own and opens at the reader's local midnight.
(The *date* a surface prints for a zoned gate is that anchor-zone date; a reader
far enough west sees it release during their previous evening. `available_at` is
always the exact instant.)
Month and year spans clamp exactly as recurrence intervals do (`1m` before Mar 31
= Feb 28 in a common year), and a calendar lead keeps its wall date across a DST
change.

The lead **owns the task's own timed gate**: it replaces the available-from date
rather than joining it. On a deadline-anchored task that hides work which would
otherwise be available today; on a scheduled-anchored task it *releases* the task
early, before its available-from date. One rule — `anchor - lead` — produces both.
Nothing else about availability changes: the task is timed-unavailable like any
deferred one (`availability_reason: "scheduled"`, `available_at` at the derived
gate), `list --unavailable`/`--deferred` and the TUI reveal toggle show it, an
ancestor's lead gates its whole subtree, and an own or inherited indefinite hold
still outranks it.

Input never has to be canonical: `lead`, `capture --lead`, and the API take
phrases (`3 weeks`, `a week`, `10 days`, `a quarter`, `5 hours`) as well as the
stored spelling, and `off`/`none`/`never` clear the window. Five rules are
refused at write time on every surface, each naming the fix:

1. **A lead needs an anchor** — a task with neither date is refused.
2. **A lead may not sit beside a two-date window.** A task carrying *both* a
   deadline and an available-from date already expresses that window with its
   two dates; a lead there would be a second spelling of one gate. Refused from
   either direction — setting the lead, or adding the second date to a lead
   task.
3. **While a lead is set, the available-from date is not a manual defer gate.**
   `defer <ref> <date>` is refused on any lead task, and `schedule` is refused
   on a deadline-anchored one; on a scheduled-anchored lead task `schedule`
   still means "move the occurrence", which is a legitimate anchor edit.
4. **A lead longer than the recurrence period is allowed** with no warning: it
   simply means the task is always visible. No validation invents a policy the
   user did not ask for.
5. **Clearing the anchor clears the lead**, in the same changeset, exactly as it
   already retires a `recur` cookie — a lead is an intent about a date and
   cannot outlive the last one.

There is deliberately no *state* rule: a lead rides with the date fields, so a
proposal takes one on the same terms it takes `--due`. One further guard, not
one of the five: a span whose derived date would fall outside the storable
four-digit year range is refused, the same range check recurrence has.

*Clock leads.* `h` is the one clock unit, and `m` always means months — never
minutes — because the lead grammar shares its unit letters with the recurrence
grammar. A clock lead measures a real duration back from the anchor's own
**instant**, so it opens partway through a day: an all-day anchor resolves to the
first instant of its date, making `5h` before June 1 **19:00 on May 31** local.
Because it is a duration, a DST change inside the window moves the wall time (the
opposite of a calendar lead, which holds its wall date). The gate stays a raw
instant — never rebuilt from a local time, which a fall-back hour would make
ambiguous — so `available_at` is exact; `lead <ref>`'s `opens_at` and the API's
`lead_opens_at` carry it. An idle TUI picks up a passing gate instant on its next
minute boundary, exactly as it already does for a timed available-from date.

`activate` on a lead-gated task releases **this occurrence only**, stamping the
internal `lead_skip` with the current anchor date and keeping every date the task
has; the next `done` roll (or any anchor edit) retires the stamp and the window
re-arms. This is the one path that behaves differently: for every other task,
including a recurring one with no lead, `activate` keeps its long-standing
meaning of clearing a future available-from date.

A **recurring capture with a lead and no date** seeds the schedule's first
occurrence rather than today (`tasks capture "clean gutters" --recur y:06-01
--lead 17d` anchors on June 1 and hides until May 15). Anchoring on today would
put the window in the past and show the task immediately, which is the opposite
of what a lead asks for.

An older binary round-trips `lead`/`lead_skip` untouched and simply does not
apply the gate — the task reads as available. Nothing is lost, and a current
binary re-applies the window on the next read.

**Cascading completion.** Completing a parent completes its whole open subtree.
`done` (or `state … DONE`) on a task closes every open descendant
(INBOX/TODO/NEXT/WAITING) with the same `closed` date and drops any `defer`
tag. A recurring descendant closes **outright** — its `recur` cookie is retired,
not rolled forward: finishing the project finishes the sub-item (no date hop, no
`- Did` log). Already-closed descendants (DONE/CANCELLED) keep their existing
`closed`. The whole cascade is a single journal entry, so one `undo` restores
the subtree exactly. Completing a **recurring parent** is the exception: it rolls
its own date forward, stays open, and does **not** cascade (an occurrence, not
the project). `cancel` (and `state … CANCELLED`) never cascades — it closes only
the target. Reopening a cascaded parent (e.g. `state … TODO`) does **not**
reopen its descendants; reopen those individually. (Pre-existing caveat:
`archive` refuses the whole sweep when a DONE/CANCELLED root still has an open
descendant. Complete, cancel, move, or unnest the open descendant first; a
closed subtree only moves as one unit once every descendant is closed.)

**Nesting.** Tasks form a tree via their `parent` ids; the CLI both reads that
hierarchy (`list` groups it, `show` reports each task's `project` — unchanged
here) and edits it. Two depth terms govern the mutations:

- **task-depth** — the number of TASK records on a task's parent chain,
  counting itself. A task filed directly under a section is depth 1; sections
  don't count.
- **subtree height** — over the span `records[ri...subtree_end)` of a subtree,
  `max(task_depth) − task_depth(root) + 1` (a lone task has height 1).

The `max_depth` config (default 4; see [Global conventions](#global-conventions),
env `TASKS_MAX_DEPTH`) caps how deep tasks may nest, enforced only at these mutation
points (never in `check`, so a deeper legacy file still validates and rolls
back cleanly):

- `capture --under P` requires `task_depth(P) + 1 ≤ max_depth`.
- `move <ref> --under P` of subtree S requires `task_depth(P) + height(S) ≤
  max_depth`.
- A move to a section (positional `move <ref> "Section"`) or `move <ref> --top`
  is **never** depth-checked — it can only reduce depth, so it's the escape
  hatch for a legacy file already deeper than the cap.

`capture --under <ref>` files the new task as the last child of an existing task
(mutually exclusive with `--project`, which files under a section). `move`'s
destination is exactly one of a positional section, `--under <ref>` (nest the
whole subtree below another task), or `--top` (unnest to the section level).
Over-cap moves/captures exit 1 with a depth message and write nothing; nesting a
task under itself or a descendant exits 1 (a cycle); `move --top` on an
already-top-level task is a no-op (prints "already at top level", exit 0, burns
no undo slot). Completion still cascades over the whole subtree regardless of
depth (see Cascading completion).

In the TUI tree views, an open task under a *closed* (DONE/CANCELLED) ancestor
is **hoisted** to top level rather than dropped with its pruned parent — so a
reopened child, or a task captured under a since-completed project, still shows.
An *unavailable* ancestor is different: it still hides its whole subtree (unless
`Z` reveals unavailable tasks), and availability hiding wins over hoisting — a
closed node below an unavailable parent stays hidden with it. Conversely, a
closed ancestor that itself owns a future date or On Hold marker remains an
availability blocker even though its row is transparent and its open descendants
would otherwise be hoisted.

`h`/`l` collapse/expand the selected subtree (a collapsed node shows `▸` and a
dim count of hidden descendants; a second `h` on a leaf or already-collapsed
node climbs to the parent), and `H`/`L` collapse/expand every subtree at once.
The collapsed set persists across restarts alongside the active view (pruned to
tasks that still exist), in `$XDG_STATE_HOME/tasks/tui.json`.

**Manual sibling placement.** The exact CLI forms are:

```text
tasks move <ref> --before <anchor-ref>
tasks move <ref> --under <parent-ref> --before <anchor-ref>
tasks move <ref> "Section" --before <anchor-ref>
```

`--before` alone infers the anchor's current direct parent. With `--under` or a
positional section, the anchor must be a direct child of that explicit
destination. `--before` cannot be combined with `--top`; at most one explicit
destination is allowed. Existing `move <ref> --under <ref>`, `move <ref>
--top`, and positional section moves remain append operations.

Source, parent, and anchor task refs use normal exact-id/line/fuzzy resolution:
no match or ambiguity exits 2. Missing flag values, contradictory destinations,
a missing section, a self-anchor, an anchor outside the requested parent,
cycles, and excessive depth exit 1 and write nothing. A placement that already
describes the exact slot succeeds with exit 0, writes nothing, and creates no
undo entry.

Every new `--before` form has a non-null anchor. Its successful human output
prints a summary followed by the moved task's standard post-write headline; the
summary names the task and destination and ends with `before "<anchor>"`.
`--dry-run` prefixes that summary with `would`, prints the current headline,
and writes nothing; it takes precedence over `--json` and remains
human-readable. Non-dry-run `--json` keeps the standard `touched` array and adds
`placement` with `parent_id`, `parent_type` (`task`/`section`), `parent_title`,
and non-null `before_id` and `before_title`.

The legacy positional section, `--under`, and `--top` forms continue to build
their existing append/unnest location values and keep their current human,
JSON, and dry-run output. They do not emit the new placement summary or
`placement` JSON member. Appending through `TaskPlacement` remains available to
the API/TUI via an omitted/null `before_id`; no CLI grammar for that conversion
is added in this slice.

Agenda, Next, Quadrants, Inbox, and Projects are not eligible for ordering:
they filter, regroup, or sort away live siblings. The fifth **Outline** tab
renders every live section and task in canonical DFS order, including closed
and unavailable tasks. Only collapse may hide
descendants. `Alt+↑`/`Alt+k`, `Alt+↓`/`Alt+j`, `>`, and `<` reorder, indent,
and outdent in that unfiltered tab. In another tab, or while `/` text or `@`
context filtering is active, those keys are consumed and the footer directs
the user to the unfiltered Outline tab. Up/down stay within the current direct
sibling list; indent appends under the preceding sibling; outdent places the
subtree immediately after its old parent. Each action is one checked placement
changeset and one undo entry, while boundary/refusal cases write nothing.

**Output.** Human-readable by default. Read commands and mutations accept
`--json`; shapes below. Mutations always print (or return in JSON) the full
new headline of every task they touched — a single mutation may touch several
(a completion cascade closes the whole open subtree; every touched headline
prints, in file order) — so the agent can verify the result without a
follow-up read.

**Exit codes.** `0` success · `1` error (bad args, validation failure,
file corrupt) · `2` ref resolution failure (no match / ambiguous). Code 2 is
distinct so agents can branch: refine the ref rather than abort.

**Safety.** Every mutation validates the file afterward and rolls back if it
would introduce a structural error. `--dry-run` on any mutation prints what
would change and writes nothing.

### Task-set agent memory

A task set may carry an optional Markdown sidecar, `agent-memory.md`, holding
durable, user-approved defaults for managing that list ("garden tasks use
`@home`"). It travels with the task data, not this checkout, so a private task
repo can commit `tasks.jsonl`, `archive.jsonl`, and `agent-memory.md` together
and cloning brings the right defaults along. The file is human-authored
Markdown — read and diffable in Git; it is *not* a structured store and does not
relax the CLI-only rule for `tasks.jsonl`/`archive.jsonl`.

When present and non-empty, its contents are appended to the agent's system
context (below `TASK_AGENT.md` and the Current environment / file-locations
blocks, inside a delimited block) on **every** `-p` run and
every TUI request — read fresh each time, never cached, so a default saved by
one request is visible to the next and an out-of-band edit or `git pull` is
picked up without a restart. An absent file simply means "no saved defaults";
the CLI never creates it as a side effect of running an agent. Agents create or
edit it only when a user explicitly asks to remember, change, or forget a
default (the policy lives in [`TASK_AGENT.md`](../TASK_AGENT.md)).

Resolution adds `memory` to the config paths, highest precedence first:

| Precedence | Location | Purpose |
| --- | --- | --- |
| 1 | `TASKS_MEMORY` env var | One-run or test override. |
| 2 | `memory = /path/to/agent-memory.md` in the config file | Intentional nonstandard location (`~`/relative expanded). |
| 3 | `agent-memory.md` beside the resolved `tasks.jsonl` | Normal per-task-set default. |

The default is derived from the **final** `tasks.jsonl` path, so a `TASKS_FILE`
override selects its sibling `agent-memory.md` even when the base dir or archive
resolve elsewhere. An empty `TASKS_MEMORY` is ignored (falls through to the next
level).

**Size budget and errors.** Before injection the sidecar is capped at 16 KiB
UTF-8. If it exists but is oversize, unreadable, invalid UTF-8, or contains one
of the reserved `----- BEGIN/END AGENT MEMORY -----` delimiter lines (which
could escape the fence that marks the block as data), the run fails loudly with
the path and reason rather than silently dropping the user's defaults: `-p`
aborts before starting the agent (exit 1), and in the TUI the request surfaces
as a failed queue event carrying the same message — never a crash and never a
run without the defaults.

**Diff.** After a `-p` run in a git-backed task set, the change summary includes
`agent-memory.md` alongside the task files, so a saved default shows up in the
same diff as the captured task. A sidecar relocated by `TASKS_MEMORY`/`memory`
to *outside* the task-data repo can't be diffed there; instead a one-line notice
points at its path.

**`tasks config`.** Reports the resolved memory path, its source, and whether
the file exists. `--json` adds `memory` (the path), `memory_exists` (boolean),
and `sources.memory` (`"TASKS_MEMORY env"` / `"config file"` /
`"beside tasks.jsonl"`, or `"pinned"` under a hermetic sandbox).

## Structured output (`--json`) coverage

Every command in the dispatch table either emits a machine-readable result under
`--json` or is an explicit opt-out with a stated reason. There is no third
category: a command that accepted `--json` and printed human text would be
indistinguishable, to a caller, from a command that produced no result.

This table is the contract, and it is enforced rather than maintained by hand.
`lib/tasks/cli_commands.rb` is the registry `bin/tasks` dispatches from;
`tasks help --json` emits it; `test/test_cli_json_coverage.rb` runs every
command listed here and fails if its stdout is not exactly one JSON document, if
this table and the registry disagree, or if a new command arrives without a
decision recorded in both.

**Refusals are only partly structured, and the honest rule is: branch on the
exit code, not on stdout.** A nonzero exit means the command refused; stdout may
be empty. Some refusals additionally print one error object on stdout —
`{"error": "<code>", "action": "<command>", …, "message": "<the same sentence
stderr got>"}` — but most still print the human sentence to stderr only. The
commands that emit the error object today:

| Command | Error codes |
|---|---|
| **every `--json` command** | `unsupported_schema_version` (see the schema version gate below) |
| `claim`, `release`, `delegate`, `undelegate`, `workref` | `conflict` (lost race / worker mismatch) |
| `archive` | `conflict` (with `reason`: `open_descendants`, `archive_conflict`, `preview_changed`, `write_failed`) |
| `undo`, `redo` | `empty`, `conflict` |
| `open` | `not_found`, `ambiguous`, `unavailable` |

`unsupported_schema_version` is the first row because it is the one refusal
every `--json` command answers structurally, and `test_cli_json_coverage.rb`
enumerates all of them against an unsupported store to prove it. It used to be
three commands: the rest printed prose with empty stdout, so `tasks done
--json` handed a caller an unparseable empty result on exactly the path it most
needed to branch on.

`recur --explain` is older and different: an unreadable schedule comes back as
`{"input": …, "error": "<prose reason>"}`, with no `action`/`message`.
Everything else — an unknown state, an unparseable date, a depth or cycle
refusal, a blank title, a `lead` with no anchor date, a `delete` needing
`--cascade` — exits nonzero with prose on stderr and nothing on stdout.
Ref resolution is the same for every command alike: no-match/ambiguous exits 2
with the candidate list on stderr (see Global conventions).

Extending the envelope to the remaining refusal paths is a deliberate follow-up,
not part of this contract: doing it needs a decision about error codes for the
whole domain, and the enumeration test below asserts what is true today rather
than what would be nicer.

| Command | `--json` | Result on success |
|---|---|---|
| `agenda` | ✅ | array of task objects |
| `next` | ✅ | array of task objects |
| `quadrants` | ✅ | array of task objects, each with `quadrant` |
| `inbox` | ✅ | array of task objects |
| `projects` | ✅ | array of project objects |
| `list` | ✅ | array of task objects |
| `show` | ✅ | one canonical task resource |
| `links` | ✅ | `{links: [{url, label, system, task, id, line, source}]}` |
| `open` | ✅ | `{id, line, title, url, label, system, opened}` — `opened` is false under `--print`. Errors: `not_found` (no links / no such index), `ambiguous` (several links, carrying `links`), `unavailable` (no browser launcher). |
| `id` | ✅ | `{id, touched: [task]}` |
| `check` | ✅ | the check result object (`errors`, `warnings`, …) |
| `project create` | ✅ | the project object |
| `project show` | ✅ | the project object |
| `project complete` | ✅ | `{touched: [task]}` |
| `project archive` | ✅ | `{archived, moved_ids}` |
| `project rename` | ✅ | the project object |
| `capture` | ✅ | `{touched: [task]}` |
| `propose` | ✅ | `{touched: [task]}` |
| `approve` | ✅ | `{touched: [task]}` |
| `reject` | ✅ | `{touched: [task]}` |
| `delegate` | ✅ | `{touched: [task]}`; conflict error object on a lost race |
| `undelegate` | ✅ | `{touched: [task]}` |
| `workref` | ✅ | `{touched: [task]}` |
| `claim` | ✅ | the full canonical task resource; `conflict` error object on a lost race |
| `release` | ✅ | `{touched: [task]}`; `conflict` error object on a worker mismatch |
| `done` | ✅ | `{touched: [task]}` |
| `due` | ✅ | `{touched: [task]}` |
| `schedule` | ✅ | `{touched: [task]}` |
| `undate` | ✅ | `{touched: [task]}` |
| `state` | ✅ | `{touched: [task]}` |
| `cancel` | ✅ | `{touched: [task]}` |
| `priority` | ✅ | `{touched: [task]}` |
| `retitle` | ✅ | `{touched: [task]}` |
| `tag` | ✅ | `{touched: [task]}` |
| `note` | ✅ | `{touched: [task]}` |
| `move` | ✅ | `{touched: [task]}`; the `--before` form adds `placement: {…}` |
| `delete` | ✅ | `{deleted: [task]}` (pre-delete headlines) |
| `recur` | ✅ | setting: `{touched: [task], next}`; reading: the preview payload; `--explain`: the engine payload |
| `lead` | ✅ | setting: `{touched: [task]}`; reading: the window preview payload |
| `defer` | ✅ | `{touched: [task]}` |
| `someday` | ✅ | `{touched: [task]}` |
| `activate` | ✅ | `{touched: [task]}` |
| `archive` | ✅ | `{roots, records, moved_ids}` — `roots` is what the human line counts, `records` the whole swept subtree (what `moved_ids` lists). Deliberately not named `archived`: the sibling `project archive --json` uses that word for its record count. Refusals: `conflict` with `reason` = `open_descendants` (carrying `blocked` + `open_descendants`), `archive_conflict` (carrying `conflicting_ids`), `preview_changed` (the store changed while the sweep was being prepared — retry), or `write_failed`; `unsupported_schema_version` on a store whose declared schema version this build does not implement. |
| `undo` | ✅ | `{action: "undo", label}`; errors `empty`, `conflict`, `unsupported_schema_version` |
| `redo` | ✅ | `{action: "redo", label}`; errors `empty`, `conflict`, `unsupported_schema_version` |
| `config` | ✅ | the resolved settings object |
| `help` | ✅ | `{commands: [{name, aliases, json, json_reason}]}` — this table, as data |
| `-p` | ❌ | Opt-out: the result is an LLM harness's free-form transcript, not a value this CLI computes; the mutations it makes are readable through the commands that do emit JSON. A leading `--json` is **rejected** (exit 1) rather than folded into the prompt. |
| `merge-driver` | ❌ | Opt-out: Git plumbing. Git supplies the three merge-stage paths and reads the merged file and the exit code; stdout is not a result surface. |

`--json` reporting the ids that moved is why `archive --json` pins the sweep to
the preview it just took: a store that changed in between refuses (`conflict`
with `reason: preview_changed`) rather than reporting a stale list. The human
form takes no preview and is unaffected. Retrying is always the right response,
including for the one benign case — a sweep prepared either side of local
midnight, whose day stamp is part of the fingerprint.

`archive`, `undo`, and `redo` reject stray positional arguments (`tasks archive x`
is now `usage:` + exit 1, where it used to ignore the extra word). `help` is the
deliberate exception: it accepts anything and prints the reference, because it is
the command you reach for when you are already unsure.

**API parity.** The HTTP adapter is JSON-only, so structured output is not a
capability that can drift there — what can drift is which capabilities it routes
at all. `GET /api/v1/meta` advertises that honestly (`capabilities.undo`,
`.redo`, `.archive_sweep` are `false` until the manager endpoints exist), and
`test/api/test_app.rb` holds those flags to what the adapter actually dispatches.
The CLI gaining structured `undo`/`redo`/`archive` results does not change what
the API routes, and must not silently flip those flags.

**Reconcile these names when the manager endpoints land.**
[`docs/api/openapi.yaml`](api/openapi.yaml) already describes the unimplemented
`/history/undo`, `/history/redo`, and `/archive-sweeps` endpoints, and it chose
different words for the same things. Whichever adapter is written second must
adopt the other's vocabulary deliberately rather than by accident:

| Concept | CLI `--json` | `openapi.yaml` |
|---|---|---|
| sweep result | `{roots, records, moved_ids}` | `data.swept` (records moved) |
| blocked sweep | `error: conflict`, `reason: open_descendants` | `code: conflict`, `details.open_descendants` |
| unreadable schema version | `error: unsupported_schema_version` | `code: unsupported_schema_version` (503) |
| undo/redo result | `{action, label}` | `HistoryStepResponse` → `data.label` |
| partial archive overlap | `reason: archive_conflict` | (no analogue yet) |

The sweep's preview pinning matches the endpoint's documented `fingerprint` →
`409 conflict` design, which is the one place the two already agree.

## Read commands

| Command | Alias | Status | Description |
|---|---|---|---|
| `list [filters]` | `l` | ✅ | All tasks grouped by state. Filters compose: `@context`, `+tag`, `/text` or bare word, `-A/-B/-C`, lifecycle scope `--open/-o` (default), `--proposed`, `--done/-d`, `--archived/-x`, or `--all/-a` (mutually exclusive). Effectively unavailable tasks are hidden from the default open scope; `--unavailable` (compatibility alias `--deferred/-D`) lists timed, inherited, and indefinite blockers; `--someday/--on-hold` selects tasks carrying their own indefinite marker. Those two filters are mutually exclusive. With a closed/archive scope, legacy `--deferred` and `--someday` filter the own marker; explicit `--unavailable` is rejected because every closed task is unavailable for lifecycle reasons. `--recurring/-R` lists tasks with a schedule; every list row with one shows `↻ <humanized schedule>`. `--delegated` selects tasks carrying a delegation (human or agent, ready or claimed) in file order and composes with any lifecycle scope — but only *within* that scope, so it is not "every delegated task": under the default `--open` it still inherits the open scope's availability rule, and a delegated task with a future `scheduled` date or a blocked/on-hold ancestor is hidden until you ask for it (`--all --delegated`, which also reviews closed provenance, or `--unavailable --delegated`, which lists exactly the hidden ones). `--agent-ready` lists the claimable queue — agent kind, unclaimed, accepted live state, and available under the ordinary prerequisite/ancestor rules — ranked by priority, then soonest deadline-or-scheduled boundary, then file order; it is only valid with `--open` and is mutually exclusive with `--delegated`. Both print one flat line per task (`agent-ready (<mode>): …`, `delegated → <email> (<STATE>): …`, `claimed by <id>: …`) instead of the state-grouped view. `--body/-b` widens text matching into notes. `--json` |
| `agenda` | `a` | ✅ | Available dated items, soonest first. `--json` |
| `next` | `n` | ✅ | NEXT actions by context. `--json` |
| `quadrants` | `q` | ✅ | Covey 2×2 from priority (A/B ⇒ important) + a `DEADLINE` within `urgent_days` (default 3, overdue counts) ⇒ urgent, with `important`/`urgent` tags as overrides. `--json` adds `quadrant`. |
| `inbox` | `i` | ✅ | Unprocessed INBOX items. `--json` |
| `projects` | `pj` | ✅ | Projects and areas rolled up over their open, non-deferred tasks (at any depth). Projects are the section children of the top-level "Projects" heading (listed even when empty); areas are the other top-level sections that currently hold open work (Inbox excluded). Each carries an open count, a NEXT count, the soonest deadline-or-scheduled value, and a `stuck` flag (no open NEXT — including an empty project). Ordered projects-before-areas, then by soonest boundary (nil last), then title. `--json` adds `next_time` and `next_at` beside the compatibility `next_date`. |
| `show <ref>` | `s` | ✅ | One live task in full, including PROPOSED without an extra flag: rendered headline + body/notes + links. Human output labels `scheduled` as `available from` and reports exact effective availability. `--json` keeps nullable ISO `scheduled`/`deadline` and adds nullable `scheduled_time`/`deadline_time` plus `available_at`; time objects carry `local`, stored `timezone`, `fold`, `effective_timezone`, and derived UTC `instant`. Reasons remain `available`, `proposed`, `scheduled`, `on_hold`, `ancestor_scheduled`, `ancestor_on_hold`, or `closed`. |
| `id <ref> [--json]` | | ✅ | Print a task's stable `id`, minting one if absent (post-migration every record already has one — this is the repair path). Idempotent. Resolves refs regardless of state. |
| `links [<ref>]` | `urls` | ✅ | Links found in task titles/notes, classified by system (`slack`, `jira`, `github`, …; unknown hosts fall back to the host name; Confluence-on-Atlassian is told apart from Jira by its `/wiki` path). One task's links with `<ref>`; every open task's otherwise. `--system <name>` filters (case-insensitive), `--all` widens the listing to done + archived (`<ref>` resolution itself stays live-file only), `--json` emits `{links: [{url, label, system, task, id, line, source}]}`. Recognizes org links `[[url][label]]`, bare URLs, and configured shorthands (below), in file order; org-internal targets (`[[id:…]]`, `[[file:…]]`, headline links) are org navigation, not links. |
| `open <ref> [n]` | `o` | ✅ | Open a task's link in the browser (macOS `open` / `xdg-open`; `TASKS_OPENER` overrides). One link opens directly; several are listed numbered (exit 1) unless picked by 1-based `n` or `--system <name>`. `--print` prints the URL instead of launching. `--json` reports which link it resolved to (`{id, line, title, url, label, system, opened}`) and, on the branches that refuse, a `not_found`/`ambiguous`/`unavailable` error object; the ambiguous one carries the numbered `links` so a caller can pick without re-running. Resolves refs regardless of state (live file). |
| `check [--json] [--all-files]` | `k` | ✅ | Validate `tasks.jsonl` structure (records, ids, DFS order, dates). `--all-files` also validates `archive.jsonl` and rejects any stable id present in both files; sync automation uses this after a merge. Exit 1 if errors — including an unreadable schema version: this build reads schema v2 only, and any other declared `meta` version (an older v1 store, or one written by a newer binary) fails with `unsupported meta version <n> (expected 2)`. The **version** header of `archive.jsonl` is consulted even without `--all-files` (its structure still is not), because a foreign archive makes every other command refuse the whole store — see The schema version gate. There is no conversion command in either direction; every read and every mutation refuses such a store without writing, on the CLI, the TUI, and the API (`503 unsupported_schema_version`). `check` itself is one of four commands exempt from that refusal, since it is where the refusal sends you. The escape hatch after any out-of-band edit — and see Repairing an invalid record below for how a mutation can fix the broken record it names. |

JSON list shape (`--json` on list/agenda/next/quadrants/inbox) — a flat array,
already sorted the way the text view sorts:
`[{"state": "NEXT", "priority": "A", "title": "…", "tags": [..], "contexts": [..], "deferred": false, "scheduled": null, "scheduled_time": null, "deadline": "2026-07-02", "deadline_time": null, "available": true, "available_at": null, "availability_reason": "available", "availability_blocker_id": null, "recur": null, "recur_human": null, "lead": null, "lead_human": null, "lead_opens": null, "lead_opens_at": null, "line": 17, "source": "live", "headline": "NEXT [#A] …"}]`
(`headline` is the star-less summary rendered from the record's fields; `source`
is `"live"` or `"archive"`; `recur` is the stored canonical schedule, e.g.
`".+1w"` or `"w:mon,wed"`, or `null`, and `recur_human` is that same value
rendered once for display (`"every Mon, Wed"`, `null` when `recur` is) so no
consumer re-implements the grammar; proposals report
`availability_reason: "proposed"`.)
Every row also carries `"project"` and `"delegation"` — the latter is the
delegation object verbatim (`{kind, mode?, status, assignee?, at, work_ref?}`)
or `null`. That is what makes `list --agent-ready --json` a complete heartbeat
entrypoint: id, title, mode, priority, dates, project, and marker, with no
display text to parse.
`quadrants --json` adds `"quadrant": "Q1".."Q4"` per item. Empty result → `[]`.

## Create

| Command | Alias | Status | Description |
|---|---|---|---|
| `capture "text"` | `add`, `c` | ✅ | New accepted INBOX item. `--due` and `--scheduled` accept complete date/time expressions. Each has independent `--due-timezone`/`--scheduled-timezone`, `--due-floating`/`--scheduled-floating`, and `--due-fold`/`--scheduled-fold` modifiers; a modifier without its matching value is rejected. Other flags remain `--priority`, repeatable tags/contexts/notes, `--no-host-context`, state, project/under, recurrence, dry-run, and JSON. `--lead <span>` sets a lead-time window on the new task and needs one of the two dates (`off` is rejected here — a new task has no window to clear); a lead beside BOTH dates is refused. `propose` accepts `--lead` on the same terms. `--recur` takes every input form `recur` does (intervals, natural calendar phrases, canonical grammar — see Recurrence) and stores the canonical value; `off` is rejected here since a new task has no schedule to clear, and a recurring capture with no date is scheduled today so it has something to repeat from. A configured host context is additive with explicit contexts unless suppressed. A capture with either temporal value lands as TODO unless state is explicit. |
| `propose "text"` | | ✅ | New inert PROPOSED task for owner review. Shares capture's dates, priority, repeatable tags/contexts/notes, host-context, project/under, dry-run, and JSON behavior, but rejects explicit state and recurrence. Agent-authored proposals should use `--note` for concise rationale/evidence. |

## Update (all take `<ref>`, all support `--dry-run` and `--json`)

| Command | Alias/synonyms | Status | Description |
|---|---|---|---|
| `done <ref>` | `complete`, `close`, `d` | ✅ | Mark DONE + `closed` date, cascading to every open descendant (see Cascading completion); recurring descendants close outright and their recur cookie is retired. A recurring task (recur cookie on its date) rolls forward and stays open instead — output shows `↻ <title> → next <date>` — and does **not** cascade. `--dry-run` also previews how many open descendants would close. |
| `cancel <ref> [--note "text"]` | `drop` | ✅ | Mark CANCELLED + `closed` date. Repeatable `--note` appends withdrawal rationale to the body (same join semantics as `propose --note`), visible in `show`. |
| `approve <ref>` | | ✅ | Accept exactly one PROPOSED task into INBOX. A live non-proposal resolves normally and reports its current state as the semantic error; a proposal with proposed descendants refuses so decisions proceed leaves first. Undoable. |
| `reject <ref> [--note "text"]` | | ✅ | Decline exactly one PROPOSED task into CANCELLED and stamp `closed`. Repeatable `--note` records withdrawal rationale on the body in the same write (mirrors `propose --note`). Uses the same broad live-ref/current-state/refusal/undo contract as `approve`. |
| `state <ref> <STATE>` | `mv` | ✅ | Any state transition (PROPOSED/INBOX/TODO/NEXT/WAITING/DONE/CANCELLED). Enforces: entering DONE/CANCELLED sets `closed`; leaving them clears it. A proposal cannot transition directly to DONE or carry recurrence; use `approve`/`reject` for review intent. Entering DONE cascades to accepted open descendants (see Cascading completion); entering CANCELLED does not. Resolves refs across proposed, open, and closed live tasks so you can repair state explicitly. |
| `due <ref> <date-or-date-time>` | `deadline`, `reschedule` | ✅ | Atomically replace `deadline`; accepts `--timezone ZONE` or `--floating`, plus `--fold earlier\|later`. Omitting time creates an all-day value and clears old time metadata. INBOX items promote to TODO. |
| `schedule <ref> <date-or-date-time>` | | ✅ | Atomically replace `scheduled` with the same temporal flags. A future exact boundary hides the task, but this command does not clear an On Hold marker; callers that mean deferral use `defer`. Same INBOX promotion. |
| `undate <ref>` | | ✅ | Remove `scheduled` and/or `deadline` (`--kind deadline\|scheduled` to pick one). |
| `priority <ref> <A\|B\|C\|none>` | `pri` | ✅ | Set or clear the `priority` field. Resolves accepted open tasks and PROPOSED tasks so a proposal's presentation can be corrected before its lifecycle decision. |
| `retitle <ref> "new title"` | `rename` | ✅ | Replace the `title`; tags/priority/state untouched. Resolves accepted open tasks and PROPOSED tasks. |
| `tag <ref> +foo -bar @ctx -@old` | | ✅ | Add/remove tags and contexts in one call. `+t`/`@ctx` add, `-t`/`-@ctx` remove. Resolves accepted open tasks and PROPOSED tasks. |
| `note <ref> "text"` | | ✅ | Append a line to the task's `body`. Resolves accepted open tasks and PROPOSED tasks. |
| `delegate <ref> <refine\|research\|implement>` | | ✅ | Mark the task agent-ready at that authority mode (`delegation: {kind: agent, status: ready, mode}`). Repeating it on an already-ready task updates the mode and keeps any `work_ref`; re-stating the mode it already has is a clean no-op (exit 0, no undo slot, no new `at`); a claimed task refuses with a conflict naming the holder (`undelegate` first). Replacing a *human* delegation is a different delegation, so its `work_ref` is dropped. Lifecycle state is untouched except when this replaces a human delegation on a WAITING task: the WAITING that delegating to a person set is undone (to `TODO`) in the same undo step, because agent-ready work is actionable again. `--keep-state` opts out (it applies to both kinds of `delegate`). Prints `agent-ready (<mode>): <title>`, or `agent-ready (<mode>) \u2192 <STATE>: <title>` when the state moved. |
| `delegate <ref> --to <email> [--keep-state]` | | ✅ | Hand the task to a person (`delegation: {kind: human, status: delegated, assignee}`) and move it to WAITING — the next action is outside the owner's control. `--keep-state` opts out. `<email>` must be a real address shape (a non-empty local part, exactly one `@`, and a dotted domain), so `@work` — one keystroke from the TUI's context filter — and `pat@localhost` are refused rather than silently parking the task in WAITING. Replaces an agent delegation (and vice versa): one delegation per task, and a change of kind drops the old `work_ref`. The state change is folded into the same undo step. Prints `delegated → <email> (<STATE>): <title>`. |
| `undelegate <ref>` | | ✅ | Clear the marker, revoking any live claim; afterwards the stale worker's `release`/`workref` fail their worker match. Lifecycle state is left alone — undelegating does not leave WAITING. An undelegated task is a clean no-op (exit 0, no undo slot). It is also the delegation **repair** route: alone among these verbs it may strip a marker some other writer left malformed even while `tasks check` calls the file invalid — provided that record is the only thing wrong and no `expected_revision`/`If-Match` was supplied (see Repairing an invalid record). Being a repair, `undo` deliberately restores the malformed bytes. |
| `workref <ref> <url-or-id\|off\|none>` | `work-ref` | ✅ | Record the single reference to where the work lives (ticket, PR, brief, session); setting overwrites, and `off` or `none` (either word, case-insensitive, on every surface) clears it. At most 500 characters, one line, no control characters. The owner may always write it; an agent adds `--worker <id>` to prove its claim still matches (deliberately **not** defaulted from `TASKS_WORKER_ID`, so an exported worker id cannot silently change who is writing). Survives completion and archival. |
| `claim <ref> --worker <id> [--json]` | | ✅ | Atomic compare-and-set from `ready` to `claimed` under the store mutation lock — exactly one worker can ever hold a task. `--worker` defaults from `TASKS_WORKER_ID`; the flag always wins, and missing both is a usage error (exit 1). A lost race exits 1 with `conflict: already claimed by <holder> at <ts>` on stderr, plus a `{"error":"conflict","action":"claim","id","holder","at","message"}` object on stdout under `--json`. Success prints `claimed by <id>: <title>`; `--json` re-emits the **full canonical task resource** (the `show --json` shape) so an agent claims and reads its authority in one step. |
| `release <ref> --worker <id> [--note "text"] [--force]` | | ✅ | Hand a claim back (`claimed → ready`, assignee dropped, `work_ref` kept). Requires the worker id matching the live claim unless `--force` (the owner override, which needs no worker). `--note` appends a blocker line to the body through the ordinary note seam, folded into the **same undo step** as the release. A worker mismatch exits 1 with `conflict: claim is held by <holder>, not "<id>"`. Prints `released → agent-ready (<mode>): <title>`. |
| `move <ref> ("Section" \| --under <ref> \| --top)` | | ✅ | Relocate a task's whole subtree by re-pointing its `parent`. Exactly one destination: a positional **section** name (out of `Inbox` into `Work`), `--under <ref>` to **nest** below another task, or `--top` to **unnest** to the section level. A section name resolves in the same widening tiers as `capture --project` (exact top-level, exact any-level, substring top-level, substring any-level; case-insensitive), so a **nested project sub-section** — e.g. a project under the "Projects" root — is a valid destination, not just a top-level heading. Section and `--top` moves are never depth-checked; `--under` is capped at `max_depth` (over-cap exits 1 with a depth message). Nesting under itself or a descendant exits 1 (cycle). `--top` on an already-top-level task prints "already at top level" (exit 0, no-op). See Nesting. |
| `move <ref> ["Section" \| --under <ref>] --before <ref>` | | ✅ | Place the whole subtree before a stable sibling. Without an explicit destination, infer the anchor's current parent; otherwise require the anchor to be a direct child of the named task/section. Not combinable with `--top`. Exact errors and human/JSON/dry-run output are frozen under Manual sibling placement above. |
| `recur <ref> <schedule>` | `repeat`, `every` | ✅ | Attach/replace the `recur` value on the task's date. `<schedule>` is any input form the one parser takes (see Recurrence): an interval cookie (`.+1w`/`+2d`/`++1m`) or friendly interval (`weekly`/`2w`/`every 3 days`), a calendar phrase (`every mon,wed`/`weekdays`/`the 15th`/`2nd tuesday`/`last day of the month`/`every july 4`), or the canonical calendar grammar (`w:mon,wed`/`2w:mon`/`m:15`/`m:last`/`m:2tue`/`y:07-04`, optional `+` one-hop prefix); `off`/`none` clears it. Input is stored canonical, so two spellings of one schedule store identically. `--from schedule\|completion` picks `+`/`.+` for a bare **interval** (default `completion` → `.+`); with a calendar schedule it exits 1 naming the prefix the input lacks — bare input is told to write `+w:mon` for one-hop, `+`-prefixed input is told to drop the `+` for catch-up. `--on <date>` seeds a `deadline` when the task has no date yet (else it errors); the seed and the schedule land in **one checked transaction**, so a refused schedule leaves the file byte-unchanged and a successful one is a single `undo`. Unreadable input exits 1 with the parser's reason (quoting the input verbatim) plus an example line; a schedule that could never fire from the task's stamp is refused by the store with its reason. Success prints `↻ <humanized> (<canonical>) → next <date> (<Dow>)` above the touched headline, where `<date>` is the task's stamp after the write — the stamp *is* the next occurrence, the same convention `done` prints; `--json` carries that date as `"next"` beside `touched`. `--dry-run`/`--json`/`--include-done`. |
| `recur <ref>` | | ✅ | Read-only preview — no schedule argument, no write. Prints the headline, `↻ <humanized> (<canonical>)`, then `--count N` occurrence dates (default 5, max 50). The list **starts with the task's stamp** — that is its next occurrence — and projects forward from there, so `--count 5` is the stamp plus four. A task with no recurrence says so and exits 0. `--json` emits `{"id","line","title","recur","recur_human","anchor","next":[…]}` with `next` starting at the stamp likewise (`recur`/`recur_human` null when absent, `"error"` added when nothing can be projected past the stamp — the stamp itself still lists). Rejects `--from`/`--on`/`--dry-run`, which only make sense when setting. |
| `recur --explain "<schedule>"` | | ✅ | Taskless parse/preview: no ref, no store access. Prints `<canonical> — <humanized>` and the next `--count N` dates (default 5) from today. Three outcomes: understood and projected (exit 0); understood but never firing from today's anchor (dates empty, reason on stderr, exit 1); unreadable (parser reason plus the example line on stderr, exit 1). `off` reports that it clears the schedule (exit 0). `--json` emits the engine payload verbatim — `{"input","canonical","human","next":[ISO dates]}`, with `"error"` present on either failure and dates as ISO strings — on stdout, with the same exit codes. The agent-facing contract: propose a schedule, explain it, verify the dates, then commit. |
| `lead <ref> <span>` | `leadtime`, `lead-time` | ✅ | Attach/replace the `lead` window on the task's date: hide it until `<span>` before its anchor (deadline if it has one, else available-from). `<span>` is a count and a unit, canonical (`3w`/`2d`/`1m`/`10y`/`5h`) or phrased (`3 weeks`/`a week`/`10 days`/`a quarter`/`5 hours`); `off`/`none`/`never` clears it. Input stores canonical. Unreadable input exits 1 with the parser's reason plus an example line. The five rules in Lead time are refused at write time, each naming the fix. Success prints the mutation with its window (`lead time 3w on "…" (3w before 2026-11-01)`) followed by the effective availability. `--dry-run`/`--json`/`--include-done`. |
| `lead <ref>` | | ✅ | Read-only preview — no span argument, no write. Prints the headline, `⏳ <humanized> before (<canonical>)`, and `opens <date> (<Dow>) — <span> before <anchor>`, plus a note when `activate` already released the current occurrence. A task with no lead says so and exits 0. `--json` emits `{"id","line","title","lead","lead_human","anchor","opens","opens_at","lead_skip"}` — `opens` is the gate's local date and `opens_at` the exact instant, which is the only precise answer for a clock span. |
| `defer <ref> [date-or-date-time]` | `snooze` | ✅ | With a value, atomically set `scheduled` and clear the task's own indefinite marker, preserving `deadline`; accepts the same temporal flags as `schedule`. Without a value, put it On Hold indefinitely. Output and `--dry-run` report exact ancestor-aware availability. |
| `someday <ref>` | | ✅ | Canonical spelling for an indefinite Someday/Maybe / On Hold task. Adds the own `defer` marker without changing either date. Idempotent. |
| `activate <ref>` | `undefer`, `resume` | ✅ | Make the task available now: clear its own indefinite marker and clear its own `scheduled` only when that date is in the future. On a task with a `lead` — or a **recurring** task, whose future date is its next occurrence rather than a defer — it instead releases exactly that occurrence (stamping the internal `lead_skip`) and keeps every date, so the series still has an anchor to roll from and the window re-arms on the next roll. A blocker inherited from an ancestor remains effective and is reported. Resolves unavailable open tasks. |

### The schema version gate

This build reads schema **v2** and nothing else. A store whose `meta` record
declares any other Integer version — an older v1 file, or one written by a
newer binary — is **refused, on read exactly as on write**, on every surface:
the CLI, the TUI, and the API (`503 unsupported_schema_version`). There is no
conversion command in either direction; a store at another version needs the
binary that matches it, not a rewrite by this one.

On the CLI the gate runs once, at dispatch, before the command's handler:

- **Every command is gated** except four, each of which states why in
  `lib/tasks/cli_commands.rb`: `check` (it is the diagnostic every refusal
  sends you to), `config` (it reports where the store *is*, never what it
  holds), `help` (it reads the registry, not the store), and `merge-driver`
  (Git hands it three explicit paths and never the configured store).
- The refusal is exit **1**, with one sentence on stderr leading with `check`'s
  own wording: `unsupported meta version 3 (expected 2) — this build cannot
  read this task file (nothing was written)`, prefixed `archive: ` when the
  skew is in `archive.jsonl`.
- With `--json` it is additionally one error object on stdout:
  `{"error": "unsupported_schema_version", "action": "<command>", "message": …}`.

Reads were the last surface to comply, and the case that forced it is a
structurally different store, not a v1 one. v1 and v2 differ only by optional
`*_time` keys, so a v1 store used to print plausible output. A v3 store that
renamed fields made `tasks list` print `No matching tasks.` and `list --json`
print `[]`, exit 0 — every record silently dropped, byte-identical to the
answer for an empty store. An unreadable store and an empty one must not be
indistinguishable to a caller that cannot see the file.

**Default `check` consults the archive's version header.** `tasks check`
without `--all-files` lints `tasks.jsonl` only — archive *structure* is what
`--all-files` is for. The version gate is the deliberate exception, because it
is store-wide rather than file-scoped: a v1 `archive.jsonl` under a current
`tasks.jsonl` makes every read and every mutation refuse the whole store. While
the default check could not see it, the refusal said "run `tasks check`" and
`tasks check` answered `ok — no structural errors`: a closed diagnostic loop
with the answer sitting in a file the command declined to open.

**The merge driver gates the two sides, not the base.** `merge-driver` refuses
any *side* at another version — reconciling records field by field across a
schema boundary is the silent corruption a version header exists to prevent.
The **base** is allowed to be *older*, because a base is never merged: it is
consulted only to tell "changed" from "unchanged". An ancestor predating a
schema upgrade is the ordinary shape of a `merge`, `rebase`, `cherry-pick`, or
`revert` that reaches back far enough, and refusing it aborted the whole merge
with a `CONFLICT`. A base *newer* than this build is still refused — that means
this binary is the stale one and cannot know what the ancestor's records meant.

### Repairing an invalid record

Every mutation preflights the whole file and normally refuses with a "task file
is already invalid — run `tasks check` (nothing was written)" hint when it finds
breakage, since editing on top of a broken file isn't trustworthy. The one
exception is a **targeted repair**: an update command (`schedule`, `undate`,
`due`, `retitle`, …) whose `<ref>` resolves to the *only* broken record may fix
it. Because hand-editing is forbidden and `check` only reports, this is the
supported way to clear a malformed field (e.g. `schedule <ref> 2026-08-01` over
a record with `"scheduled":"not-a-date"`, or `undate <ref>` to drop a bad
stamp).

The contract is narrow:

- Repair engages only when **every** `check` error is on the record the command
  targets. If any other record is also broken, the command still refuses with
  the "already invalid" hint and writes nothing — fix the others first (each via
  its own targeted mutation).
- Raw-safety comes first: a file that isn't valid UTF-8, or that has a line which
  isn't parseable JSON, always refuses — even when that line is the target.
- The target must not be on **line 1**. `check` reports a file's `meta` problems
  against line 1, so in a file with no meta record the first task inherits them
  and "every error is on my record" becomes true of an error the patch cannot
  fix. Such a file needs `tasks check`, not a field patch.
- After the write the file must validate **completely**, or the change rolls back
  (exit 1). A repair can't leave the file partially broken.
- `undo` of a repair faithfully restores the prior (invalid) bytes, so you can
  retry a different fix.
- Among the delegation verbs, only `undelegate` repairs, because it is the only
  one that owns the whole `delegation` field: it may strip a marker a version
  skew or a foreign writer left malformed (a `claimed` marker with no
  `assignee`, say). `delegate`, `claim`, `release`, and `workref` all keep
  refusing on an invalid file, since each of them would have to *read* the
  broken marker to decide what to write. A repair is never granted under an
  `expected_revision`/`If-Match`, whose baseline was computed over the
  malformed bytes.

## Projects

`projects` (alias `pj`) lists projects and areas; the `project <verb>` command
group reads and mutates a single project or area. A fixed verb set avoids the
title ambiguity a bare `project "<ref>"` would create.

| Command | Synonyms | Status | Description |
|---|---|---|---|
| `project create <title> [--json] [--dry-run]` | `project new` | ✅ | Create a new empty project — a section filed under the top-level "Projects" root (created first if the store has none yet, so an empty/rootless file still works). A blank title, or one that duplicates an existing project or area (case-insensitive; the project-ref candidate set, so a duplicate would make later refs ambiguous), exits 1 with the reason. Success prints the new project row (`--json` emits the project object). `--dry-run` writes nothing. Then `move <ref> "<title>"` files tasks into it. |
| `project show <ref> [--json]` | | ✅ | One project/area in full: title, kind, rolled-up open/NEXT counts, soonest date, and body note. `--json` is the project object (same shape as a `projects` element). |
| `project complete <ref>` | `project done` | ✅ | Close every open descendant task of the project — the same cascade as `done`: DONE + today's `closed` date, `defer` dropped, and a recurring descendant retired (its cookie removed, no roll-forward). Prints every touched task's new headline (identified by line). |
| `project archive <ref> [--force]` | | ✅ | Sweep the project's whole section subtree to `archive.jsonl` (the root section drops its `parent` and gains today's `archived` stamp). Refuses with exit 1 while the project still has open work unless `--force`; deferred/held tasks (`held_count`) count as open work too. PROPOSED descendants always refuse, even with `--force`, until approved or rejected. |
| `project rename <ref> <new title>` | | ✅ | Replace the section title (leading/trailing space trimmed). |

**Project refs.** A `<ref>` resolves against the `projects` listing: an exact
8-hex section id (case-insensitive) wins, then an `L<line>` section line, then a
case-insensitive title substring across both projects and areas. Zero matches or
an ambiguous substring exits 2, listing candidates as `L<line>: <title>` — the
same contract as task refs. All four commands accept `--json`; the three
mutations accept `--dry-run` and write nothing in that mode.

Over the HTTP API the same capability is `GET /api/v1/projects`,
`POST /api/v1/projects` (create — `{"title": …}` → 201 with the project; 422 on
a blank/duplicate title), `GET/PATCH /api/v1/projects/{id}`, and
`POST /api/v1/projects/{id}/complete` and `…/archive` — strict 8-hex ids only,
no fuzzy refs (a transport difference per design rule 7). See
`docs/api/openapi.yaml`.

## Lifecycle / meta

| Command | Alias | Status | Description |
|---|---|---|---|
| `archive` | `x` | ✅ | Sweep each DONE/CANCELLED subtree to `archive.jsonl` (root drops `parent`, gains `archived`). Refuses with exit 1 when any candidate root has a non-closed descendant, including PROPOSED, and explains how to resolve it. Persistence is retry-safe across interruption: the archive is installed first, and live records are removed only when the archive contains exactly one canonical copy of every moved ID; partial or conflicting overlap refuses without deleting live data. In the TUI, `x` previews root and descendant counts and requires `y` confirmation; the Store validates that exact candidate-ID/content fingerprint under the sweep lock, while `n`/`esc` cancels without writing. `--json` emits `{roots, records, moved_ids}` (`roots` matches the human count; `records` is the whole swept subtree); because only a pre-sweep preview knows which records move, the JSON form pins the sweep to that preview and refuses if the store changed in between. Every refusal is an error object on stdout: `conflict` with `reason` = `open_descendants`, `archive_conflict`, `preview_changed`, or `write_failed`, or `unsupported_schema_version` on a store whose declared schema version this build does not implement. Stray positional arguments are now a usage error (exit 1). |
| `delete <ref>` | | ✅ | Undoable **hard delete** of a task's subtree from the live file — not an alias for `CANCELLED`, and it never touches `archive.jsonl`. A leaf deletes directly; a task that still has descendants is refused (exit 1) unless `--cascade` removes the whole contiguous subtree as one journal entry. Deleting never hoists or reparents children. PROPOSED and accepted open tasks resolve directly; `--include-done` additionally widens to closed live tasks. Archived-only ids are not found (exit 2 via ref resolution / `not_found`); a section id is rejected (delete targets tasks). Reports every removed task's pre-delete headline (`--json` → `{deleted: [..]}`); `--dry-run` prints what would be deleted, including the descendant count when cascading, and writes nothing. Undoable via `tasks undo` (restores the exact prior bytes). Cancellation/archival is usually the right call — `delete` is for genuine mistakes. |
| `undo [--json]` | | ✅ | Revert the last mutation via the on-disk journal (`Tasks::Journal`, under `$XDG_STATE_HOME/tasks/journal/`), shared with the TUI and across CLI runs. Refuses (exit 1) if `tasks.jsonl` changed out-of-band since that edit — resolve with `git diff` / `git checkout -- tasks.jsonl`. `--json` emits `{action: "undo", label}` naming the mutation it reverted, or an `empty`/`conflict` error object. |
| `redo [--json]` | | ✅ | Replay the last undone mutation; same shared journal and conflict guard as `undo`, including the `{action: "redo", label}` result and its `empty`/`conflict` error objects. |
| `-p [--provider N] [--model N] "prompt"` | | ✅ | Natural-language request via a headless LLM agent (Claude CLI by default, or any configured harness). Leading `--provider`/`--model` override the config default for one run; see [LLM agent settings](#llm-agent-settings). Deliberately has no `--json` — see the opt-out in Structured output (`--json`) coverage. |
| `config [--json]` | | ✅ | Print resolved file paths, `urgent_days`, `max_depth`, theme/colors, effective `timezone`, `time_format`, tzdb version, fallback warning, prompt facts, and each setting's source. |
| `help [--json]` | `-h`, `--help` | ✅ | Grouped command reference. Also printed (to stderr, exit 1) on an unknown/absent command. `--json` emits the dispatch registry instead — `{commands: [{name, aliases, json, json_reason}]}` — which is the machine-readable form of Structured output (`--json`) coverage above. |

Ideas beyond this spec live in `docs/ideas.md`.

## Design rules for new commands

1. **Spec first**: add/adjust the row here before implementing.
2. Thin dispatch in `bin/tasks`; logic in `lib/tasks/` (usually a `Store` method).
3. Mutations go through `Store#with_history` — never `File.write` directly.
   That buys the file lock, the post-write `check` rollback, the persistent
   undo journal, and crash-safe atomic writes (`Tasks::Atomic.write`).
4. Accept synonyms liberally, print the canonical name in output.
5. Every mutation's output includes the resulting headline(s).
6. Tests required: happy path, ref-not-found, ref-ambiguous, and
   `Tasks::Check.check` clean after every mutating test (the test helper's
   fixture makes this a one-liner).
7. **CLI/API parity by default**: user-visible task semantics belong behind
   `Tasks::Application` and should be exposed consistently by `bin/tasks` and
   the loopback HTTP API. Keep this spec and `docs/api/openapi.yaml` synchronized
   whenever both adapters expose the capability. CLI-only or API-only behavior
   requires an explicitly discussed product/security reason documented in the
   relevant spec (and an ADR or plan when architectural). Adapter mechanics may
   differ — fuzzy refs and friendly input on CLI, stable ids/JSON/ETags over
   HTTP — but the resulting domain behavior must not drift.
