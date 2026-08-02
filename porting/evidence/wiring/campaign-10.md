# CAMPAIGN 10 — seeding agent execution

Companion to the eight records appended to `porting/manifest.jsonl` and the one
record appended to `porting/campaigns.jsonl`. `slicing.md` holds the previous
pass's reasoning; this file holds this one's, for the same reason: the next
agent to read these records deserves to know why the boundaries are where they
are, and — more than in any earlier campaign — what the conformance method
cannot see.

Trigger: Marcus decided agent execution **is** in the port (td-7fce3d).
`slicing.md` §5c had deferred campaign 10 pending exactly that decision, on the
grounds that "a slice seeded and then marked `not_applicable` costs more than a
decision taken first". The decision was taken; this is the seeding.

Result: **8 new slices, 44 → 52.** One new campaign record (10). One amended
sentence in `config-resolution`'s `oracle_gaps` (§6). 88 previously unclaimed
Ruby tests now have an owner. `validate`, `drift`, `reach`, `plan` and
`progress` are clean and `ruby test/all.rb` is at 0 failures.

---

## 1. The inventory, taken mechanically

Every file under `lib/**` and `bin/`, and every `def test_` under `test/**`,
checked against the `source_paths` and `ruby_tests` of all 44 existing records
and against `porting/manifest-issues closure --json`.

`slicing.md`'s `unclaimed_after` list was the starting point and was *not*
authoritative here, for a concrete reason: it surveyed `lib/tasks/**`, and more
than half of agent execution lives outside that tree, in `lib/llm/` and
`lib/tui/`. Neither directory is in any slice's drift closure, so neither
appeared in the coverage claim `manifest.md` makes ("every file under
`lib/tasks/` sits in some slice's closure except `lib/tasks/api/`") — that claim
is about `lib/tasks/` and is silent about the rest of `lib/`.

Nine source files, none in any slice's `source_paths` before this pass:

| File | Lines | What it is |
|---|---|---|
| `lib/llm/agent.rb` | 189 | the execution contract: spawn, pump, cancel, run_sync, availability |
| `lib/llm/claude_cli.rb` | 26 | Claude CLI argv |
| `lib/llm/cursor_cli.rb` | 29 | Cursor Agent argv |
| `lib/llm/hermes.rb` | 80 | Hermes argv + Ollama reachability probe |
| `lib/llm/registry.rb` | 124 | provider/model registry, entry ordering, default resolution, `build` |
| `lib/llm/config.rb` | 62 | the LLM half of `~/.config/tasks/config` |
| `lib/tasks/agent_context.rb` | 104 | system-context assembly + the memory sidecar's guardrails |
| `lib/tasks/prompt_facts.rb` | 71 | the `Current environment` block and its `prompt.*` toggles |
| `lib/tasks/agent_diff.rb` | 87 | the post-run git diff decision |
| `lib/tui/agent_queue.rb` | 273 | the serial request coordinator |

Plus, in files other slices already name: `bin/tasks`' `cmd_prompt`,
`reject_prompt_json!`, `extract_llm_flags` and `maybe_show_diff`; the `-p`
registry entry in `lib/tasks/cli_commands.rb` (the only entry declared
`json: false` with a `reason:`); `Tasks::Config::Paths#agent_context` and the
`prompt.<name>` parse rule in `lib/tasks/config.rb`.

Unclaimed tests, all 88 of them previously owned by no slice:

| File | Tests | Now |
|---|---|---|
| `test/test_llm.rb` | 35 | split three ways across the registry / protocol / adapters slices |
| `test/test_agent_context.rb` | 15 | `agent-context-assembly` |
| `test/test_agent_queue.rb` | 13 | `agent-request-queue` |
| `test/test_prompt_facts.rb` | 10 | `prompt-facts` |
| `test/test_config.rb` (prompt facts) | 7 | `prompt-facts` |
| `test/test_cli_mutations.rb` (AgentDiff) | 5 | `agent-diff-report` |
| `test/test_cli_mutations.rb` (`-p` guards) | 2 | `cli-prompt-command` |
| `test/test_cli_json_coverage.rb` (`-p --json`) | 1 | `cli-prompt-command` |

Two things the survey found that were not in the brief's description of the
surface:

1. **`prompt_facts.rb` is agent execution, and was filed as campaign 8.**
   `config-resolution`'s single `oracle_gap` says the unclaimed remainder of
   `test/test_config.rb` "belong to campaign 8's CLI and TUI work". That was
   true of the theme, mouse, timezone and date-order keys and false of the seven
   `prompt.*` tests: `prompt.datetime` and `prompt.hostname` decide what an
   *agent* is told about its environment, and nothing else consumes them. Fixed
   in §6.

2. **`test_prompt_rejects_json_instead_of_swallowing_it` was named as
   unclaimable and is now claimable.** `cli-mutation-json-envelopes`'
   first gap lists it among the `test/test_cli_json_coverage.rb` tests no slice
   could claim "until those campaigns land", with `-p` (campaign 10) as one of
   the named blockers. It is per-command rather than registry-wide, campaign 10
   has landed, and `cli-prompt-command` claims it. The genuinely registry-wide
   tests in that file are untouched and still need the final parity slice
   `slicing.md` §5b recommended — `recur`/`lead` (5) and `undo`/`redo` (6) are
   still unseeded, so nothing changed for them.

---

## 2. Where the cuts are

Eight slices, in dependency order:

```
config-resolution ──> llm-provider-registry ──┬──> agent-harness-adapters ──┐
      agent-harness-protocol ─────────────────┘                             │
                                              └──> agent-request-queue      │
config-resolution ──> prompt-facts ──> agent-context-assembly ──────────────┤
config-resolution ──> agent-diff-report ────────────────────────────────────┴──> cli-prompt-command
```

Two of these edges cross a campaign boundary into campaign 2
(`config-resolution`), which is correct and matches campaign 7's edges back into
campaigns 2 and 4: the LLM config keys are read out of the same flat
`key = value` file, through `Tasks::Config.config_file(env)`.

**The registry / protocol / adapters split.** All three could have been one
"LLM layer" slice — 35 tests, six small files. They are three because they fail
in three unrelated ways and want three different reviews. The registry is pure
resolution and its expected defect is a precedence rule (`config.model` applies
only when the provider was *not* explicitly overridden, so `--provider hermes`
alone must not inherit a claude tier from config). The protocol is process
management and its expected defects are invisible to the whole suite —
`pgroup: true`, and a cancel that blocks forever on a TERM-ignoring child. The
adapters are transcriptions of three third-party CLIs' flag sets, and their
expected defect is a category error: Claude takes the system context as a *flag*
(`--append-system-prompt`) while Cursor and Hermes take it *prepended to the
prompt text*, because their CLIs have no system-prompt option. A port that
flags for Cursor silently drops TASK_AGENT.md and the absolute file locations
from every Cursor run, and the agent then works from relative paths in the wrong
directory. Folding these together would produce one slice whose review could not
be done by one person in one sitting and whose risk tier would have to be the
maximum of the three.

The three adapters are, however, deliberately **one** slice. They are one
decision seen three times, two of them are twenty-line classes whose entire
content is one array, and splitting them would hide the flag/prepend asymmetry
that is the only property worth reviewing.

**`prompt-facts` before `agent-context-assembly`.** `AgentContext.build` calls
`PromptFacts.render` for section 2 of five, and
`test_omits_current_environment_when_all_prompt_facts_off` cannot pass without
it. It is a true edge, not a plausible one. It is also the one slice in this
campaign that is *fully* observable through an existing `--json` command
(`tasks config --json` prints the resolved `prompt_facts` map verbatim), which
is why it is low risk and why it is a good first thing to actually port here.

**`agent-context-assembly` is tiered high, and not for code complexity.** It is
104 lines. It is high because it is the only place in the product where
untrusted file content — `agent-memory.md`, which can arrive by `git pull` — is
composed into a string that instructs an autonomous process running with
`--dangerously-skip-permissions`. The defense is a delimiter fence plus a hard
refusal when the body contains `----- BEGIN/END AGENT MEMORY -----`. The
tempting defect is treating a bad sidecar as "skip the memory section and carry
on", which is strictly worse than aborting: the user's saved defaults silently
stop applying and the agent acts on the request without them. Every failure mode
in the Ruby is a hard error carrying the path, and the reason is written in the
Ruby.

**`agent-diff-report` is separate from `cli-prompt-command`** because the Ruby
already separated them, and said why: `AgentDiff` was extracted out of
`bin/tasks` "so the decision … can be exercised against a real sandbox repo
without driving an actual agent". The decision (which targets, notice or no
notice, `nil` or a `Result`) is this slice; the heading, the bold/dim styling
and the trailing-newline fixup in `maybe_show_diff` are campaign 8's, as all
human output is.

**`cli-prompt-command` has three tests and is still a slice.** It is thin
because everything else about `-p` spawns a harness the suite must not require
to be installed. What it owns is nevertheless real and nothing else owns it: the
ordering inside `cmd_prompt` — build the context, resolve the entry and build
the adapter, *then* check availability, *then* run — which is what makes an
oversize `agent-memory.md` abort with the path instead of running the agent
without the user's defaults. Its gap sentence lists the six behaviors with no
oracle at all (`extract_llm_flags`, the unavailability abort, the unknown-provider
abort, the nonzero-exit warning, the empty-prompt usage abort, the `.strip`);
five of them become reachable from a case list the moment the fake-harness
fixture exists, and the empty-prompt abort does not — see §4f.

**`agent-request-queue` is in campaign 10, not campaign 12.** `AgentQueue` lives
under `lib/tui/` and its only caller is the TUI's `App`, which is an argument
for campaign 12. It is here anyway, on the same ownership test `CLAUDE.md`
applies to surfaces: the queue *owns* a capability — serial execution of
autonomous requests with one live adapter, per-request provider pinning, and
error containment across five distinct failure paths — and it owns it in a class
that requires nothing from the TUI (no `ansi`, no `theme`, no terminal, no
`App`). What campaign 12 owns is the *rendering* of its snapshots. A Go port
that grew a non-interactive batch mode would want this class unchanged.

---

## 3. What is deliberately out of scope, and why

**TUI presentation of agent activity — campaign 12.** `lib/tui/agent_activity.rb`
requires `ansi` and `theme` and returns painted lines; it is a widget. The App
integration is the same. That is 29 tests
(`test/test_agent_activity.rb` 4, `test/test_app_agent_queue.rb` 12, plus 13
across `test/test_app.rb` and `test/test_app_modals.rb`).

`slicing.md` §1 item 2 records what happens when an exclusion is stated once and
never picked up — `lib/tasks/opener.rb` sat in no slice for a whole pass because
`links-read` excluded it with a reason. So the exclusion here is not a sentence
in this file only. `agent-request-queue`'s `oracle_gaps` names the two tests
that carry behavior *no other test does*, so campaign 12 inherits a specific
obligation rather than a vague one:

- `test_store_reloads_after_completion_before_next_request_starts` — the visible
  checkpoint between runs. `AgentQueue#pump` deliberately does *not* start the
  next request; App reloads task state first. The queue's own comment says this
  is why, and only that test proves it.
- `test_queued_requests_build_fresh_context_so_a_memory_edit_hits_only_the_second`
  — the `agent_factory` contract that the system context is built at **start**,
  not at enqueue. That is why a memory edit between two submissions affects only
  the second, and it is the reason `agent-context-assembly` says the sidecar is
  read fresh on every call.

**`lib/tasks/operation_context.rb`** is unclaimed and is *not* agent execution
despite the name. It is `{operation_id, source, actor}` metadata for the
application facade, with `SOURCES = %i[cli tui api]`; its own comment says
"commands do not interpret this yet". It belongs with whichever campaign seeds
the application boundary and is left alone here rather than absorbed because the
name looked close.

**`test/test_cli_mutations.rb#test_config_reports_the_sibling_memory_path`** is
unclaimed and stays that way. It asserts `tasks config`'s human output for the
memory line, which is `config-resolution`'s behavior (it already claims the
`test/test_config.rb` equivalent) presented as campaign 8's formatting. Claiming
it in campaign 10 would be claiming a `config` output test because the word
"memory" appears in it.

**`lib/tasks/api/`** remains outside every closure, unchanged by this pass.
`slicing.md` §5's note still stands and is now a slightly larger hole: `lib/llm/`
and `lib/tui/` were also outside every drift closure before this pass, and after
it `lib/llm/**` and `lib/tui/agent_queue.rb` are inside one — the rest of
`lib/tui/` is not, and will not be until campaign 12.

---

## 4. What the conformance method cannot observe here, precisely

This is the section the brief asked for, and it is the reason campaign 10 reads
differently from campaigns 2–4 and 7.

Every earlier campaign's behavior is a pure function from (fixture bytes, argv,
env) to (fixture bytes, stdout, stderr, exit status). That is exactly the shape
`porting/specs/observations.schema.json` records and
`porting/runners/README.md` drives. **Agent execution is the first campaign
whose behavior is `spawn a program we did not write and get out of the way`,**
and the schema has no vocabulary for that at all.

### 4a. The process-spawn gap

The observation schema records `files.before/after/deltas`, `process.stdout`,
`process.stderr`, `process.exit_status`, `process.signal`, `journal`,
`revisions`, `invocation` and `environment`. There is **no field for a child
process** — not that one was spawned, not its argv, not its environment, not its
process group, not the signals it received. Consequently:

- `Agent#command`'s argv — the single thing all three adapter classes exist to
  produce — is invisible.
- Whether the child was put in its own process group (`pgroup: true`), which is
  the difference between cancelling a harness and orphaning its tool
  subprocesses, is invisible.
- The TERM→KILL escalation ladder is invisible twice over: no signal can be sent
  to a child under the protocol, and no field would record it if one were.

**I am not proposing a schema extension**, per the brief. The buildable answer
needs nothing from the schema, and it is written into three slices'
`fixtures_todo` in full: a `valid/fake-harness` fixture that ships an executable
script (git preserves the exec bit — `porting/runners/README.md` says so in the
`perms.json` rationale) which writes its own argv, cwd and stdin size into files
in the copy root, plus a `.config/tasks/config` setting
`claude-cli_command = ./fake-harness`. The spawn then becomes file bytes in
`files.after`, which the comparator already compares. What it still does not buy
is cancellation.

Two mechanics of that fixture were stated wrongly in the first draft of this
file and of the records, both caught by the adversarial review, and both are
corrected here because a corpus built from the wrong version does not work:

- **Why a relative `./fake-harness` resolves.** *Not* because the harness is
  `chdir`'d there. `available?` runs `File.executable?` inside the **`tasks`
  process**, against *that* process's cwd; the `chdir:` on the spawn is not in
  play for the check at all. It works because two independent things coincide:
  the runner's case `cwd` defaults to `"."` (the copy root) and
  `cp -a <fixture>/store/. <copy>/` puts `tasks.jsonl` at the copy root, so
  `data_dir = File.dirname(ORG)` is the same directory. The consequence is a
  constraint the records now carry: **cases against this fixture must not set
  `cwd`.** The protocol permits setting it, and a case that does breaks
  `available?` while leaving the spawn workable — presenting as "the agent isn't
  available", indistinguishable from the no-fixture baseline. It remains true
  that no case needs to set `PATH`, which the protocol forbids.
- **How the fake harness takes its exit status.** Not a normal argv scan.
  `cmd_prompt` does `prompt = words.join(" ")` and every adapter passes the
  result as **one** argv element (for Cursor and Hermes it is buried further,
  inside the element that also carries the prepended system context). So
  `-p "--exit 3"` hands the harness a single argument `"--exit 3"` and a token
  scan finds nothing; the script must search each element for an *embedded*
  `--exit N`.

One further load-bearing dependency, now recorded as a gap on
`agent-harness-protocol`: this whole mechanism rests on `command_on_path?`'s
explicit-path branch (`File.executable?(bin) if bin.include?("/")`), and **that
branch has no test**. All three availability tests pass a bare name, so a port
implementing the method as PATH-search-only passes them all and then silently
disables the entire fake-harness corpus.

### 4b. What *is* observable today, with no new fixture at all

Worth stating because it is easy to miss under all the caveats. The runner pins
`PATH` to `/usr/bin:/bin:/usr/sbin:/sbin`, so `claude`, `agent` and `hermes` are
all absent and `available?` is false for every provider. **Three** of `-p`'s
four abort paths are therefore fully deterministic and fully observable today
against any existing fixture — the review verified all three by running them
under the complete pin set:

```
tasks -p "water the garden"   → agent 'claude-cli' not available …   exit 1
tasks -p --json foo           → -p has no --json: …                  exit 1
tasks -p --provider bogus hi  → unknown LLM provider: "bogus" …      exit 1
```

The fourth — `-p` with no words — is **not** observable, and the first draft of
this file and of `cli-prompt-command`'s `fixtures_todo` both wrongly said it was.
See §4f. `cli-prompt-command` is wired to `valid/small-gtd` and
`valid/empty-store` for the three that work, and those cases belong in the case
list before any new fixture exists.

`prompt-facts` is the other bright spot, with one honest qualification. `tasks
config --json` prints the resolved toggle map verbatim, so the slice's
*resolution* half is conformance-testable the moment a fixture ships a config
file — and today none does: no fixture in `porting/fixtures/` carries
`.config/tasks/config` at all, even though the runner defines a `config` role for
that path and its `mkdir_p` leaves a shipped one intact. What that does not
cover is the *rendered* `Current environment` block, which exists only inside a
system prompt handed to a harness and needs the fake-harness corpus like
everything else here.

The mirror image of that qualification is finding 4 against
`llm-provider-registry`, and it is the sharpest correction in this round. Its
`observable_outputs` claimed "the resolved provider **and model**". The model is
not there: `bin/tasks:2854` interpolates `entry.provider` only, `entry.model`
appears once in `bin/tasks` on the path that does *not* abort, and `tasks config
--json` carries no LLM keys at all. So the `valid/llm-config` fixture — which
genuinely does exercise every branch of `Config.read_raw` and `Config.load` —
**exposes exactly one string**. A port that dropped `<provider>_models` parsing,
dropped `<provider>_command` parsing, or resolved the wrong model entirely emits
a byte-identical observation, which defeats precisely the defect that slice's
own notes name as expected. That is the "near-miss wired into fixtures" failure
one level up: a *fixture description* that reads as adequate to whoever builds
it. The record now says so in `oracle_gaps` and in the todo itself.

### 4c. Three things that are nondeterministic rather than unobservable

Different problem, different answer, so they are recorded separately:

1. **Cancellation latency.** `test_async_agent_cancellation_escalates_when_child_ignores_term`
   asserts a TERM-resistant child dies in under one wall-clock second against a
   0.30s budget. No pin in `porting/specs/determinism.md` can pin that; two
   correct implementations will disagree on elapsed time. A comparison must
   compare the outcome (`signaled?`, `termsig == KILL`) and exclude timing — and
   the schema records neither, so it cannot express the comparison at all.
2. **The queue's monotonic clock.** `queued_at`/`started_at`/`finished_at` come
   from `CLOCK_MONOTONIC`. `TASKS_PIN_NOW` pins wall-clock reads and should not
   be extended to cover this: the value is never persisted. The port keeps the
   clock injectable for the same reason the Ruby does.
3. **`git`'s own output.** `AgentDiff` captures `git diff`'s stdout into the
   product's output. Hunk headers, rename detection and `--color=always` escapes
   belong to a third-party binary whose version the schema records nowhere, so a
   byte comparison of that text can fail for reasons neither implementation
   owns. Compare the *decisions*; treat the diff body as diagnostic.

### 4d. One slice that cannot have a fixture at all, for a structural reason

`agent-diff-report`'s input is a **git work tree**, and a git repository cannot
carry another repository's `.git` directory as committed content.
`porting/fixtures/` is committed to this repo, so no fixture can ship the one
thing this behavior requires — and the copy protocol (copy, perms, roots,
journal, mode) runs no commands, so nothing could create one post-copy. Its
`fixtures_todo` names the two buildable options (ship a `git bundle` or a
renamed `dot-git/` and teach the runner one step; or add a `git_init` case key)
and says plainly that choosing between them is not this slice's decision. Until
then the slice is provable by translated unit tests against a sandbox repo, in
the same shape as the Ruby — an honest method, just not the differential one.

`agent-request-queue` is the other slice no case can drive, for a different
reason: it has no CLI surface whatsoever. `tasks -p` calls the adapter directly
through `run_sync`; the only caller of `AgentQueue` is the TUI. A queue is a
sequence of interleaved calls against a live object, which the case-list format
cannot express and the observation schema cannot record. It is the one slice in
this campaign with neither `fixtures` nor `fixtures_todo` — the third honest
shape `manifest.md` allows, with `notes` saying why, following
`query-filter-parse`'s precedent.

### 4e. Two oracle gaps that are not about the harness at all

Recorded because they are the ones a reader will not expect:

- **`TASK_AGENT.md` is proved by nothing.** `AgentContext` reads it from the
  application checkout and *tolerates its absence*
  (`test_missing_contract_file_still_builds_from_paths_and_pointer`). That file
  is a versioned contract whose content is a large part of what an agent
  actually does. A port that ships without it builds a valid, complete,
  empty-contract system prompt and every test in this campaign stays green.
- **The adapter argv arrays are transcriptions of external CLI contracts.**
  `hermes.rb` says "Verified against Hermes v0.17.0 (2026-06)"; `cursor_cli.rb`
  says "Verified against Cursor Agent 2026.07.09-a3815c0". The suite asserts we
  build the argv we decided to build. Neither it, nor `drift`, nor the
  conformance harness will ever notice a third-party flag rename — only a human
  re-reading `--help` will. The port inherits the flags and the staleness
  together.

### 4f. A Ruby defect that makes one abort path uncapturable

`tasks -p` with **no words** does not simply abort. It prints the usage line and
then a Ruby `NameError` backtrace: `cmd_prompt` aborts on the empty prompt
*before* its own `require_relative "../lib/tasks/agent_context"`, so the
unwinding `SystemExit` is matched against a `rescue ArgumentError,
Tasks::AgentContext::Error` clause naming a constant that does not exist yet.
The review reproduced it under the full pin set and without it.

This is a product bug, not a manifest bug, and it is filed separately — the
records point at it rather than re-describing it. What matters here is the
manifest consequence, because the first draft got it backwards twice:

- `cli-prompt-command`'s `fixtures_todo` listed `-p` with no words among the
  cases "observable today". A case list built from that sentence would pin a
  Ruby baseline whose stderr carries absolute checkout paths, line numbers and a
  Ruby-version-dependent error rendering. No port can match it, and the only two
  ways out are both bad: bless a Ruby bug as an intentional difference, or
  reproduce it. Struck.
- The slice's `notes` identified the hazard class exactly — the Ruby's own
  comment says `reject_prompt_json!` is kept out of `cmd_prompt` because "that
  method's rescue clause names constants its own body requires" — and then
  concluded the Ruby *avoids* it. It does not: `cmd_prompt` contains its own
  pre-require `abort`. Corrected, with the further note that lazy requires are a
  Ruby startup-cost technique with no analogue in a compiled port, so the right
  Go shape makes the failure impossible rather than avoided.

The `oracle_gaps` already said the empty-prompt abort was unproved. What was
missing, and is now there, is that it is currently *unprovable*.

### 4g. A blind spot in `reach` this campaign ran into twice

`reach` reported 0 unexplained reaches before and after this pass, and that
proves less than it looks. `VERB_OWNERS` maps **store mutation verb methods**
only, so an oracle that reaches downstream through anything else — an adapter
method, a CLI verb — is invisible to the tool. Two of this campaign's records hit
it:

- `llm-provider-registry` claimed two `LLM.build` tests whose assertions are
  actually over `Hermes#command` / `CursorCli#command` argv — behavior owned by
  `agent-harness-adapters`, which `depends_on` this slice. The oracle pointed
  *downstream*, so the slice declared first in the campaign could not have gone
  green first, and `reach` said nothing. Both tests are now moved;
  `test_build_raises_on_unknown_provider` stays, needing no adapter behavior.
- `agent-diff-report`'s oracle drives `capture` through the binary and cannot
  pass unless something writes the store. Kept without a dep edge — this slice
  needs *some* writer, not that one — but now with an explaining sentence,
  matching the one `cli-prompt-command` already had for its own `capture` drive.

This is the same signature the td-940935 audit found, arriving through a door
`reach` does not watch. Reading the test body is the only defense, and it should
be part of characterizing every slice in this campaign.

---

## 5. What I recommend deferring, and what I did not defer

**Nothing in this campaign was deferred**, which is a change from the previous
pass. `slicing.md` deferred campaign 10 wholesale because a scope decision was
missing; with the decision taken, seeding partially would recreate exactly the
problem it was avoiding.

Three things I recommend for follow-up work, none of them a slice:

1. **Build `valid/fake-harness` first.** Four of the eight slices' fixture gaps
   are the same fixture, and it is small: an executable script, a config file,
   and an ordinary small store. It converts the campaign from "mostly
   unobservable" to "mostly observable" in one change, and it is the fixture the
   `porting/runners/README.md` `config` role was defined for and never used.
2. **Decide the git-work-tree question before `agent-diff-report` is claimed.**
   It is a protocol decision (a fixture-shipped bundle vs. a `git_init` case
   key), it affects the runner rather than the corpus, and an agent that claims
   the slice first will hit it in the first hour.
3. **`lib/tui/` is still outside every drift closure except the queue.** The
   same observation `slicing.md` made about `lib/tasks/api/` now applies to the
   TUI: a change there is reported by no `drift` run. Worth fixing when campaign
   12 is seeded, not before.

I did **not** seed the final CLI-registry parity slice. Campaign 10 removes `-p`
from the list of blockers `slicing.md` §5b named, but `recur`/`lead` (campaign
5) and `undo`/`redo` (campaign 6) are still unseeded, so the whole-registry
tests in `test/test_cli_json_coverage.rb` remain unclaimable and the
recommendation stands unchanged.

---

## 6. The two existing records this pass amended

Two, not one. The first draft did half of a symmetric pair and the review caught
the other half — which is exactly the half-drift `campaigns.jsonl` exists to
prevent, reproduced inside a gap sentence.

`config-resolution`'s single `oracle_gap` ended: *"The rest belong to campaign
8's CLI and TUI work and are unclaimed on purpose."* That sentence predates
campaign 10 and became wrong for the seven prompt-facts tests the moment these
records landed. It now names campaign 10 for those seven, keeps the substance
for the timezone / date-order / theme / mouse / host-context keys, and records
that `prompt-facts` names `lib/tasks/config.rb` in its own `source_paths`. A
second sentence was added recording that
`test/test_cli_mutations.rb#test_config_reports_the_sibling_memory_path` is
unclaimed **on purpose** — it asserts `tasks config`'s human output for the
memory line, which is that slice's resolution rule shown through campaign 8's
formatting, and it sits inside `test_cli_mutations.rb`'s "agent memory
guardrails" block where a campaign-10 reader will trip over it. Claiming a
`config` output test in the agent campaign because the word "memory" appears in
it would be the misattribution td-940935 removed.

`cli-mutation-json-envelopes`' first `oracle_gap` enumerated ten registry-wide
tests and said *"No slice can honestly claim them until those campaigns land"*,
naming `-p` (campaign 10) as one of the blockers. `cli-prompt-command` now
claims one of the ten. That test is per-command rather than registry-wide — it
runs `-p --json …`, asserts one exit status and one message, and reads nothing
from the RECIPES table — so campaign 10 owning `-p` is precisely what makes it
claimable. It is struck from the enumeration, with a pointer to the slice that
took it and a note that the other nine are unaffected: they still read the whole
table, which still contains `recur`/`lead` and `undo`/`redo` from the unseeded
campaigns 5 and 6, so the final parity slice is still owed.

That last part is the only field-level subtlety in this pass and is worth
stating plainly: **two slices name `lib/tasks/config.rb`, and neither ports all
of it.** `config-resolution` owns store-path resolution; `prompt-facts` owns the
`prompt.<name>` key parse. The overlap is deliberate and follows existing
practice (`bin/tasks` is named by five slices, `lib/tasks/store.rb` by nine).
It has to be named here rather than left to the closure, because
`prompt_facts.rb` is required **by** `config.rb` and not the reverse — a
`prompt_facts.rb`-only closure would not watch the parse rule for drift at all.
No test is claimed by both.

`porting/manifest-issues`' `VERB_OWNERS` needed **no change**: it has no `nil`
entries left after the previous pass, and no campaign 10 slice ports a store
mutation verb. `reach` reports 21 reaches, all pre-existing, all explained, none
in a campaign 10 slice — but see §4g for why that is weaker evidence than it
reads.

One process note for whoever edits this file next. The first draft of this pass
was applied by re-serializing all of `manifest.jsonl`, which rewrote every one of
the 44 pre-existing lines in a different JSON style: `git diff` showed 44
deletions, the two surgical edits above were invisible in it, and per-record
`git blame` — the provenance of every `source_sha` and `ruby_tests` decision the
drift rule depends on — was destroyed. The orchestrator restored the 43
unaffected lines byte-for-byte. **Change only the records you intend to change,
and leave every other line untouched.** The fix round was applied that way: ten
records rewritten (the eight new ones plus the two amended), every other line
copied through verbatim and verified equal to `HEAD`'s bytes.

---

## 7. Verification

Run against the applied files, not a scratch copy:

```
porting/manifest-issues validate → ok: 52 slices, 5 campaigns, every source path
                                   and oracle test resolves          (exit 0)
porting/manifest-issues drift    → no drift: every slice's Ruby source is
                                   unchanged since its source_sha     (exit 0)
porting/manifest-issues reach    → 21 reach(es), 0 unexplained        (exit 0)
porting/manifest-issues plan     → skip=63 create=16 update=1 dep=8 total=88
porting/manifest-issues progress → slices: 0/52 at a terminal status
                                   campaign 10: 0/8
ruby test/all.rb                 → 2189 runs, 32361 assertions,
                                   0 failures, 0 errors, 0 skips
```

Additionally checked by hand:

- **No test is claimed by two slices.** The four pre-existing shared tests are
  unchanged; the eight new slices add none, and no new slice shares a test with
  any other slice.
- **Every `source_paths` and `fixtures` entry resolves.** No fixture path was
  invented: only `valid/small-gtd` and `valid/empty-store` are wired, on the one
  slice with a genuinely reachable behavior today. The other seven carry an
  honest `fixtures_todo`, and `agent-request-queue` carries neither with the
  reason in `notes`.
- **`source_sha` is each closure's own last-touch commit**, computed from
  `closure --json`'s `last_touch` as `manifest.md` prescribes, not pinned to
  HEAD. The eight differ (`e75019a3`, `3d2a618b`, `fa5539d4`, `fee6e9cf`)
  precisely because their closures differ, which is the point of the rule;
  `test/test_manifest_issues.rb` asserts the equality and passes.
