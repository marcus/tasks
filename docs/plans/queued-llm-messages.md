# Plan: queued LLM requests and result history

Status: implemented; independent adversarial review pending

Date: 2026-07-13

Implementation note: delivered as a TUI-layer `AgentQueue` above the existing
single-run adapters, with a pure Agent activity renderer, live modal refresh,
process outcome metadata, FIFO App integration, session-bounded history,
queue-aware cancellation/quit guards, and focused integration tests. The
headless `tasks -p` path remains unchanged.

## Outcome

The TUI accepts another agent request while one is already running. Requests
run one at a time in submission order, so the user can enter a batch without
waiting and the autonomous harnesses do not race while mutating the same task
list. Every request retains its prompt, selected provider/model, status, and
captured transcript for the rest of the TUI session. The footer shows current
progress; an Agent activity view shows the full request/result history.

The headless `tasks -p` command remains a synchronous one-shot interface. This
plan changes the interactive TUI only.

## Recommended product direction

These are recommendations to settle in discussion before implementation:

- Run requests **sequentially in FIFO order**. “Queued” must not mean parallel
  agent processes: every harness can write `tasks.jsonl`, and parallel runs
  would create avoidable semantic races even though individual CLI writes are
  locked and validated.
- Keep the queue and result history **in memory for the current TUI session**.
  Quitting with active or queued work requires confirmation; this first version
  does not promise background execution or restart recovery.
- Snapshot the selected `provider:model` **when each prompt is submitted**.
  Pressing `M` later affects only prompts submitted afterward.
- Preserve the harness's exact captured transcript. Do not parse model output,
  infer task changes, or introduce a second completion protocol.
- Retain the latest 50 finished requests, plus all active and pending requests.
  This bounds a long-running TUI session without truncating an individual
  response beyond the behavior the adapter already has.
- Cap the waiting queue at 100 requests. Reaching the cap leaves the next
  prompt intact and asks the user to let or cancel queued work before adding
  more.
- Keep the current footer as the quick status surface and add an **Agent
  activity** modal for the durable per-request view. The modal is available
  from the action palette and a direct `A` shortcut.

## Current behavior and seams

Today `Tui::App` owns one `@agent`, one prompt buffer, and one wrapped response:

- `submit_prompt` clears the prompt and rejects the submission when
  `@agent.running?` is true.
- `loop_once` adds only `@agent.io` to `IO.select`, then `pump_agent` drains it.
- The footer shows either the active stream or the single latest response.
- `Escape` cancels the active harness or dismisses that one response.
- `M` may change the selected entry while a request runs, but the new entry is
  applied only to the next request.
- `LLM::Agent` is already the correct single-run transport boundary:
  `start`/`io`/`pump`/`cancel` for the TUI and `run_sync` for `tasks -p`.

The queue belongs above `LLM::Agent`, in the TUI layer. The adapters should
remain unaware of batching, history UI, task storage, and subsequent requests.

## Interaction contract

### Submitting prompts

| Situation | Return in the prompt does |
|---|---|
| No request is active | Accepts the prompt, starts it immediately, clears the input, and returns to the list. |
| A request is active | Accepts the prompt into the FIFO queue, clears the input, and flashes its queue position. |
| Prompt is blank | Does nothing, as today. |
| Selected harness is unavailable or the queue is full | Keeps the typed prompt and prompt focus, and shows an actionable error. |

`Tab` continues to focus the prompt even while the footer is streaming an
active request. The inactive prompt hint changes from the current ambiguous
ellipsis to a useful status such as `tab to ask · 2 queued`.

Each accepted request captures:

- a session-local monotonic request number;
- the exact prompt text;
- the selected `LLM::Entry` (`provider:model`);
- queued, started, and finished monotonic timestamps;
- status: `queued`, `running`, `succeeded`, `failed`, or `cancelled`;
- the exact output transcript and process exit status when finished.

Requests are independent one-shot agent invocations. They do not share chat
history. Their shared context is the same system prompt plus the task files as
updated by earlier requests.

### Queue progress

While work remains, the footer shows:

- the active request number and its captured provider/model;
- the spinner and the last few streamed lines, preserving current behavior;
- the number of requests waiting;
- a short hint that `A` opens Agent activity and `Escape` cancels the active
  request.

Example:

```text
⠸ #2 claude-cli:sonnet is working · 3 queued · A activity · esc cancels
   ...latest transcript line...
```

When the queue drains, the footer opens the most recently completed response as
it does today, but labels it `result #N of N` and points to `A` for all results.
Dismissing the footer does not delete history.

### Agent activity view

`A` opens a scrollable, filterable modal built on the existing modal
infrastructure. It groups every retained request clearly:

```text
✓ #1 · claude-cli:sonnet · succeeded · 8s
  request  capture milk tomorrow
  result   Captured TODO milk ...

⠸ #2 · hermes:qwen3.6:35b-a3b · running · 42s
  request  move the flight follow-up to Waiting
  result   ...live transcript...

○ #3 · claude-cli:opus · queued #1
  request  show me what is overdue and capture the follow-ups
```

The view uses the current modal scroll keys and `/` filtering. It refreshes on
queue events without resetting the user's filter or scroll position. Prompt and
response boundaries remain visible even for empty output, failures, and
cancellations.

### Cancellation and quitting

- `Escape` while a request is running cancels **only the active request**, marks
  it cancelled, records any transcript already received, and advances to the
  next queued request.
- A palette action, `Cancel queued agent requests`, discards all waiting
  requests after confirmation but leaves the active request alone. This gives
  the user a fast recovery path after submitting a mistaken batch without
  changing the meaning of `Escape`.
- `q`/`Ctrl-C` with active or queued requests shows a confirmation with exact
  counts. Confirming cancels the active process group and discards pending
  requests before restoring the terminal. This must compose with the existing
  unsaved-task-editor quit guard rather than bypass it.
- A failed or unavailable request is recorded as failed and does not block later
  requests. The next request starts automatically.

## Architecture

### 1. Add `Tui::AgentQueue`

Create `lib/tui/agent_queue.rb` as the single owner of request lifecycle. It is
initialized with an agent factory so tests can supply deterministic fake
adapters. Its public surface should be small:

```ruby
queue.enqueue(prompt:, entry:)       # accepted item or rejection
queue.start_next                     # one transition, never parallel
queue.io                             # active adapter IO for IO.select
queue.pump                           # zero or one lifecycle event
queue.cancel_active
queue.cancel_pending
queue.active? / queue.pending_count
queue.requests                       # ordered read-only snapshots for rendering
```

The queue owns at most one live `LLM::Agent`. It builds an adapter from the
request's captured entry only when accepting/starting that request, and releases
the adapter after recording the final transcript. The renderer receives
read-only snapshots rather than mutable queue internals.

State transitions are explicit and tested:

```text
queued -> running -> succeeded
                  -> failed
                  -> cancelled
```

No queue method writes task data. Agents continue to act through `bin/tasks`,
and the existing Store/check/atomic-write/journal path remains authoritative.

### 2. Preserve single-run adapter semantics

Keep `LLM::Agent#start`, `#pump`, and `#cancel` single-run. Add only the result
metadata the coordinator needs:

- capture the child process exit status when `finish` reaps it;
- distinguish an explicit cancellation from a normal/non-zero exit;
- keep partial output available after cancellation;
- reset result metadata at the next `start`.

Do not add queue arrays, request IDs, UI strings, or task-store knowledge to
`lib/llm/`.

### 3. Integrate one queue into `Tui::App`

Replace direct app ownership of `@agent`/`@resp` with an `@agent_queue` and a
small selected-result/footer projection. The event loop selects
`@agent_queue.io` when present.

Completion ordering is important:

1. Drain and finalize the active request.
2. Record its transcript/status in history.
3. Reload the TUI Store if the task file changed.
4. Start the next queued request.

This gives the visible task list a coherent checkpoint between requests and
ensures only one harness is live. Spawn errors, availability failures, and
non-zero exits become per-request failure events; they must not crash the TUI
or strand the queue.

The app header continues to show the entry selected for newly submitted
requests. The active footer and activity view show the captured entry belonging
to that particular request.

### 4. Add a pure activity renderer

Create `lib/tui/agent_activity.rb` to turn queue snapshots into themed lines.
Keep wrapping, elapsed-time labels, status glyphs, prompt/result indentation,
and empty-output messages out of `App`.

Use the existing `Tui::Modal` for box placement, filtering, and scrolling. Add
a narrow content-replacement API only if needed so a live activity modal can
refresh its lines while preserving its filter and clamped scroll position.
Avoid introducing a second general modal system or another geometry authority.

### 5. Register actions and update the interaction state

- Add `A` / `Open agent activity` to `Tui::Shortcuts`, available when at least
  one request exists, and expose the same handler in the action palette.
- Add the palette-only `Cancel queued agent requests` action, available when
  pending work exists and guarded by a confirmation.
- Reuse `:modal`/`:modal_filter` if the activity view can obey those contracts;
  add a new `UiState` mode only if the implementation proves the existing modal
  lifecycle cannot represent live content safely.
- Keep input precedence explicit: prompt editing owns ordinary text; global
  quit still wins; an open activity modal owns its navigation/filter keys.

## Failure and edge-case semantics

- **Provider/model changes:** each queued item uses its submission-time entry.
- **Unavailable provider:** reject immediately when detected before acceptance;
  if availability changes later or spawn fails, record a failure and continue.
- **Non-zero exit:** keep the transcript, label the request failed, continue.
- **No output:** store and render `(no output)` for that request.
- **Cancellation:** preserve partial output and label it cancelled; do not report
  success merely because the process was reaped.
- **External task writes:** retain the existing mtime reload behavior while an
  agent runs. Queue serialization does not prevent normal CLI/TUI writes.
- **Agent changes selection/order:** stable task IDs continue to protect TUI
  selection during each Store reload.
- **Short terminals:** queue status yields to the active prompt and uses the
  existing footer fitting rules; it must not make the main list disappear.
- **Unicode and partial chunks:** continue scrubbing only at rendering
  boundaries; retain raw accumulated UTF-8 output behavior in the adapter.
- **History eviction:** evict the oldest finished entry only after the retention
  limit is exceeded; never evict active or queued requests.
- **TUI exit:** no orphan process and no silent loss of queued work.

## Test plan

### Queue unit tests (`test/test_agent_queue.rb`)

- idle enqueue starts exactly one adapter;
- enqueue while active remains pending and never starts a second adapter;
- FIFO order across three or more requests;
- provider/model is snapshotted per request;
- completion records exact output and exit status;
- Store-reload/start-next event ordering is observable at the App boundary;
- non-zero exit, spawn error, and later unavailability fail one item and
  continue;
- cancelling active preserves partial output and advances;
- cancelling pending never touches the active adapter;
- history retention evicts only oldest finished items;
- queue-full/unavailable rejection does not consume the prompt.

### App and interaction tests

- submitting while busy queues instead of flashing “still working”;
- accepted prompts clear, rejected prompts remain editable;
- `Tab` can enter another prompt while output streams;
- footer reports active request, captured entry, and pending count;
- final completion exposes the latest result and retains older results;
- `A` and the palette open the same activity view;
- activity lines distinguish queued/running/succeeded/failed/cancelled items and
  preserve prompt/result boundaries;
- activity refresh preserves modal filter and scroll;
- `Escape` cancels active only, then starts the next request;
- cancel-pending confirmation leaves active work intact;
- quit confirmation composes with the dirty-editor confirmation path;
- tiny-terminal footer fitting and Unicode prompts/transcripts remain valid;
- shortcut registry/help generation remains collision-free.

### Adapter tests

- normal exit status, non-zero status, cancellation status, and output reset;
- process-group cancellation remains intact;
- `run_sync` and `tasks -p` behavior remain unchanged.

### Verification gates

```sh
ruby test/all.rb
bin/tasks check
git diff --check
```

After automated tests, do an adversarial TUI review focused on orphaned
processes, lost prompts, skipped/duplicated queue entries, stale modal state,
quit-confirmation precedence, and footer behavior on short terminals.

## Documentation updates during implementation

- Update `docs/cli-spec.md` first with the queue, result-history, cancellation,
  model-snapshot, and session-lifetime contracts.
- Update the README TUI key table and agent paragraph (`A`, queued submissions,
  and results).
- Keep `docs/plans/llm-adapter-pattern.md` as the adapter history; add a short
  cross-link rather than rewriting its shipped architecture record.
- Update generated help through `Tui::Shortcuts`; do not maintain a second
  handwritten shortcut list in code.

## Delivery phases

1. **Agree on product semantics.** Settle session persistence, result UI, and
   cancellation behavior; mark this plan approved.
2. **Specify and build the queue core.** Update `docs/cli-spec.md`, add
   `Tui::AgentQueue`, result metadata in `LLM::Agent`, and focused unit tests.
3. **Wire the event loop.** Accept busy submissions, serialize execution,
   reload between runs, and harden failure/cancel/quit behavior.
4. **Add result UX.** Footer progress, activity renderer/modal, shortcuts,
   filtering, and short-terminal behavior.
5. **Finish proof and docs.** README/help sync, full suite, `tasks check`, manual
   TUI exercise with at least three queued requests and two different captured
   model selections, then adversarial review and remediation.

## Non-goals

- Parallel agent execution against one task list.
- A multi-turn conversational thread or shared chat context between requests.
- Queue persistence, background service execution, or resuming work after the
  TUI exits.
- Batching or daemonizing the synchronous `tasks -p` command.
- Reordering queued requests, per-request retry policy, or editing a submitted
  prompt in the first version.
- Parsing transcripts into structured task diffs or bypassing the CLI-only
  writer rule.

## Discussion points before approval

1. Is session-only queue/history sufficient, or must queued work and results
   survive closing and reopening the TUI?
2. Is the recommended `A` activity modal plus latest-result footer the right
   presentation, or should results be navigated only in the footer?
3. Should `Escape` cancel only the active request and continue the queue, as
   proposed, or pause the queue after cancellation?
