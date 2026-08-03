# Adversarial review — campaign 10 (agent execution) seeding

Reviewer: independent agent, 2026-08-01. Scope: the uncommitted diff to
`porting/manifest.jsonl` and `porting/campaigns.jsonl` (44 → 52 slices), read
against `porting/manifest.md`, `porting/evidence/wiring/slicing.md`, and the
Ruby it claims to describe. Nothing was modified except this file.

**Verdict: LAND WITH FIXES.** The slicing is good work — the boundaries are
right, the inventory is genuinely complete, and several oracle_gaps are the most
useful prose in the manifest. But two findings are the exact failure mode this
project fears (a slice that reads provable and is not), one existing record was
left saying something the pass made false, and one wired-fixture claim is
verifiably wrong about the Ruby.

---

## Tooling, run rather than trusted

| Check | Result |
|---|---|
| `porting/manifest-issues validate` | `ok: 52 slices, 5 campaigns, every source path and oracle test resolves`, exit 0 |
| `porting/manifest-issues drift` | `no drift`, exit 0 |
| `porting/manifest-issues reach` | 21 reaches, 0 unexplained (all pre-existing; see finding 1 for why this proves less than it looks) |
| `porting/manifest-issues plan` | `skip=80 total=80 — nothing to do` |
| `ruby test/all.rb` | 2189 runs, 32361 assertions, **0 failures, 0 errors, 0 skips** |
| `source_sha` vs `closure --json` `last_touch` | all 52 equal, mechanically compared |

Note on `plan`: `sync` has evidently already been run against this uncommitted
manifest — td carries fixture-gate issues for 7 of the 8 new slices. Any fix
below that edits `fixtures_todo` text needs a re-sync.

---

# Must fix before this lands

## 1. `llm-provider-registry` claims three tests it cannot pass at its position — HIGH

`llm-provider-registry` `depends_on: ["config-resolution"]` and its `notes` say
*"This is the only slice in campaign 10 whose behavior is pure resolution, which
is why it is first."* It claims:

- `test_build_returns_configured_adapter_with_settings`
- `test_build_returns_configured_cursor_adapter`

Their bodies (test/test_llm.rb:341-360) are not resolution assertions:

```ruby
assert_instance_of A::Hermes, agent
assert_equal "/opt/hermes", agent.command("hi", model: "m").first
assert_equal "x", agent.command("hi", model: "m")[agent.command("hi", model: "m").index("--provider") + 1]
```

`Hermes#command` and `CursorCli#command` are **`agent-harness-adapters`'**
behavior, and `agent-harness-adapters` `depends_on` `llm-provider-registry` —
i.e. the oracle points *downstream*. This is precisely the signature the
td-940935 audit named ("the test drives behavior owned by a slice that is not
upstream"), and `reach` cannot see it: `VERB_OWNERS` maps *store mutation verb
methods* only, so an adapter-argv reach is invisible to the tool.

**Consequence.** The slice declared "first" in the campaign cannot go green
first. The agent who claims it either quietly ports two adapters inside a slice
scoped to resolution — widening a slice without a stated reason — or lands it
with a partial oracle and calls it proved. Noticed the first day anyone works
campaign 10; the *silent* version (a widened slice) is noticed never.

**Fix.** Move both tests to `agent-harness-adapters` (where the argv assertion
belongs and where the dep edge is right), keeping
`test_build_raises_on_unknown_provider` here — it needs no adapter behavior.
Alternatively keep them and add an `oracle_gaps` sentence naming both tests in
the explained-reach style manifest.md § `reach` describes. Moving is better:
here it is genuinely a misplacement, not a "this is the only oracle we have".

## 2. `tasks -p` with no words emits a Ruby backtrace — and a case list is about to pin it — HIGH

`cli-prompt-command` is the one campaign-10 slice with wired fixtures
(`valid/small-gtd`, `valid/empty-store`), and its `fixtures_todo` states: *"under
the protocol's pinned PATH, `-p "..."`, `-p --json ...` and `-p` with no words
all abort observably against an ordinary store."* I ran all three under the full
pinned environment against a real `small-gtd` copy. Two are clean. The third is
not:

```console
$ tasks -p            # stderr, 763 bytes, exit 1
usage: tasks -p [--provider NAME] [--model NAME] "do something with my tasks"
/Users/marcus/code/tasks/bin/tasks:2850:in 'Object#cmd_prompt': uninitialized constant Tasks::AgentContext (NameError)

rescue ArgumentError, Tasks::AgentContext::Error => e
                           ^^^^^^^^^^^^^^
	from /Users/marcus/code/tasks/bin/tasks:3358:in 'block in <main>'
	from /Users/marcus/code/tasks/bin/tasks:3434:in '<main>'
/Users/marcus/code/tasks/bin/tasks:2838:in 'Kernel#abort': usage: tasks -p … (SystemExit)
	from …
```

This is a genuine Ruby defect, environment-independent (reproduced without the
pin set too). `cmd_prompt` aborts on an empty prompt at line 2838, *before* the
`require_relative "../lib/tasks/agent_context"` at line 2841; unwinding
`SystemExit` then evaluates the `rescue ArgumentError, Tasks::AgentContext::Error`
clause at line 2850, whose constant does not exist yet.

The slice's `notes` identify the hazard class exactly — *"that method's rescue
clause names constants its own body requires"* — and then conclude the Ruby
"deliberately" avoids it by keeping `reject_prompt_json!` outside `cmd_prompt`.
It does not avoid it: `cmd_prompt` contains its own pre-require `abort`.

**Consequence.** A case list built from this `fixtures_todo` records a Ruby
baseline whose stderr contains absolute checkout paths, line numbers and a Ruby
version-dependent NameError rendering. No Go port can ever match it. The
comparison fails permanently, and the two available "fixes" are both bad: bless
it as an intentional difference (laundering a Ruby bug into the contract), or
reproduce it. Noticed as soon as the first campaign-10 case list is captured —
which the slice explicitly recommends doing *before* any fixture work.

**Fix.** Two parts. (a) Amend the slice: record the defect in `notes` and add an
`oracle_gaps` sentence saying the empty-prompt abort is **not** comparable
today; strike "`-p` with no words" from the `fixtures_todo`'s list of
observable-today aborts. (b) File the Ruby fix (hoist the two
`require_relative`s above the usage `abort`, or move the empty-prompt check
beside `reject_prompt_json!`) — it is a one-line change and it makes the case
list honest. The `oracle_gaps` already note the empty-prompt abort is unproved;
what is missing is that it is currently *unprovable*.

## 3. `cli-mutation-json-envelopes`'s gap sentence was left false — MEDIUM-HIGH

The pass performed the symmetric edit on `config-resolution` (moving the seven
prompt-facts tests out of "campaign 8") — that edit is correct, precise, and
verified: those seven tests are now claimed by `prompt-facts`, exactly once
each, and the remaining host-context/timezone/theme/mouse tests are still
unclaimed as the amended sentence says.

The other half was not done. `cli-mutation-json-envelopes`'s first oracle_gap
still reads:

> the registry-wide coverage tests — … and `#test_prompt_rejects_json_instead_of_swallowing_it` — assert over the whole RECIPES table … **No slice can honestly claim them until those campaigns land.**

`cli-prompt-command` now claims that test. The correction exists only inside the
*new* record; a reader of the old one is told the opposite. That is the
half-drift `campaigns.jsonl` exists to prevent, reproduced inside a gap
sentence.

I checked the underlying claim and `cli-prompt-command` is right to take it:
`test_prompt_rejects_json_instead_of_swallowing_it` (test/test_cli_json_coverage.rb:398)
runs `["-p","--json","water the garden"]` and asserts exit 1 plus the message —
per-command, not registry-wide. The other nine listed tests genuinely do read
the RECIPES table and remain unclaimable.

**Fix.** Strike that one test from the enumeration in
`cli-mutation-json-envelopes` and add the same one-line pointer the pass gave
`config-resolution`.

## 4. `llm-provider-registry`'s observable-output claim is wrong about the model — MEDIUM

`observable_outputs[0]`: *"the resolved provider and model named in `-p`'s
availability abort message"*. The abort message is bin/tasks:2854:

```ruby
abort "agent '#{entry.provider}' not available — check the CLI is installed and any local model server is running"
```

The model is not in it. `entry.model` appears exactly once in `bin/tasks`, at
line 2856, passed to `run_sync` — i.e. only on the path that *does not* abort.
`tasks config --json` (bin/tasks:2900-2921) carries no LLM keys at all: no
provider, no model, no per-provider settings.

The `fixtures_todo` then claims the `valid/llm-config` fixture puts *"every
branch of `Config.read_raw` and `Config.load` in a single observable input."*
It puts every branch in a single **input**; the **output** exposes one string —
the provider name. A port that dropped `<provider>_models` parsing, dropped
`<provider>_command` parsing, or resolved the wrong model entirely produces a
byte-identical observation. That is the "near-miss wired into fixtures" failure
mode one level up: a *fixture description* that will read as adequate to the
person who builds it.

Note this defeats exactly the defect the slice's own `notes` names as expected
(the `config.model` pairing rule). It is caught by the unit oracle
(`test_default_entry_precedence_explicit_over_config`) and by nothing the
fixture makes observable.

**Fix.** Correct `observable_outputs`. Restate the `fixtures_todo` as: the
config is *exercised* by this fixture but only the provider name is
*observable*; the model and per-provider settings become observable only via the
fake-harness argv, i.e. through `agent-harness-adapters`' corpus.

## 5. Every line of `manifest.jsonl` was reformatted — MEDIUM (process)

`git diff --stat` reports 96 changed lines on a 52-line file: 44 deletions
(every pre-existing record) and 53 insertions. The cause is a style change, not
content — old records are `{"id":"format-parse","campaign":2,…}` and every line
is now re-emitted as `{"id": "format-parse", "campaign": 2, …}` (Python
`json.dumps` default separators). `campaigns.jsonl` now mixes the two styles
within one file.

**Consequence.** The two surgical edits this review was asked to verify are
invisible in `git diff` — I could only find them by parsing both revisions and
comparing per-key. Every slice record's `git blame` now points at this commit,
destroying the provenance of `source_sha`, `ruby_tests` and `oracle_gaps`
decisions the drift rule depends on being attributable. The 384 KB diff also
means the *next* manifest reviewer has no cheap way to see what moved.

**Fix.** Re-emit with compact separators (`separators=(",", ":")`) so the diff
is 8 added lines plus 1 modified line, and match `campaigns.jsonl`'s existing
style for the new campaign record.

---

# Worth doing, not blocking

## 6. The one mechanism the whole fixture story rests on has no test and no gap

The fake-harness corpus depends entirely on `LLM::Agent#command_on_path?`'s
explicit-path branch (lib/llm/agent.rb:130-136):

```ruby
def command_on_path?(bin)
  return File.executable?(bin) if bin.include?("/")
  …
end
```

Every `available?` assertion in the suite passes a **bare** name
(test/test_llm.rb:113, 119, 123 — `"definitely-not-a-real-binary-xyz"`,
`"ruby"`, and Hermes' endpoint probe). The `/`-branch is untested. A port that
implements `command_on_path?` as PATH-search-only passes all three. It is not
named in any `oracle_gaps`. Add a sentence to `agent-harness-protocol`; it is a
one-line gap and it guards four `fixtures_todo`s.

## 7. The fixture story's stated *reason* is wrong; the conclusion survives by coincidence

`agent-harness-protocol`'s `fixtures_todo` argues: *"Because `command_on_path?`
checks `File.executable?` directly for any command containing `/`, a relative
`./fake-harness` resolves against the copy root the harness is chdir'd into, so
no case needs to set PATH."*

Two different resolutions are being conflated, and I tested both:

- `available?` runs in the **`tasks` process**. `File.executable?("./fake-harness")`
  resolves against *that process's* cwd. The `chdir:` is nowhere in play.
- The spawn (`system(*argv, chdir: @root)`) resolves `./fake-harness` against
  the chdir'd directory — confirmed empirically: from cwd `ct`,
  `File.executable?("./fake-harness")` is `false` while
  `system("./fake-harness", chdir: "sub")` succeeds.

**The conclusion still holds**, for reasons the todo does not state: the
runner's case `cwd` defaults to `"."` = copy root
(porting/runners/ruby/run:660), `TASKS_DIR` is pinned to the copy root, and
`cp -a <fixture>/store/. <copy>/` lands `tasks.jsonl` at the copy root, so
`data_dir = File.dirname(ORG)` is also the copy root. Both resolutions coincide.

A case that sets `cwd` to a subdirectory — which the protocol explicitly permits
— breaks `available?` while the spawn would have worked, and the failure
presents as "the agent isn't available", indistinguishable from the no-fixture
baseline. **Fix:** restate the reason and add "cases against this fixture must
not set `cwd`".

Everything else in the fixture mechanics checks out and I verified it directly:
`cp -a …/store/.` carries dotfiles, `FileUtils.mkdir_p(<copy>/.config/tasks)`
(run:558) leaves a shipped `config` file intact, and `LLM::Config.load` reaches
it through `Tasks::Config.config_file(env)` → `XDG_CONFIG_HOME`, which the
runner pins to `<copy>/.config`.

## 8. `agent-diff-report`'s oracle silently needs `create-basic`

`test_diff_includes_a_committed_memory_edit_alongside_task_files`
(test/test_cli_mutations.rb:3715) runs `run_cli_at(org, archive, "capture",
"water the garden")` through the binary and asserts `assert_includes
result.diff, "tasks.jsonl"` — it cannot pass unless `capture` writes. The slice
`depends_on: ["config-resolution"]` only, and no gap explains it. `reach` misses
it because the drive is a CLI verb, not a store method. `cli-prompt-command` got
the equivalent explanation sentence for its own `capture` drive; this one did
not. Add the sentence (a dep edge is the wrong fix — any writer would do).

## 9. The fake harness's `--exit N` control is not buildable as written

`agent-harness-protocol`'s `fixtures_todo` specifies a harness that *"exits with
the status named in its first `--exit N` argument"*. `cmd_prompt` does
`prompt = words.join(" ")` and each adapter passes that as **one** argv element,
so `tasks -p "--exit 3"` hands the harness a single argument `"--exit 3"`, never
the two tokens a normal argv scan looks for. (For Cursor and Hermes it is
embedded even deeper — inside the system-context-prepended element.) The corpus
is still buildable, but the harness must scan argv elements for an *embedded*
`--exit N`. manifest.md requires a todo "named precisely enough to build"; this
detail is not.

## 10. The PROCESS-SPAWN GAP paragraph is pasted verbatim into slices it does not describe

All 8 records carry the identical sentence ending *"the argv this layer builds —
the single thing every adapter exists to get right"*. Three of the eight build
no argv at all: `prompt-facts` renders a text block, `agent-diff-report` shells
out to `git` for its own purposes, `agent-request-queue` never spawns anything
(its own next gap says so). The paragraph is true of the campaign and false of
those layers. `oracle_gaps` earn their keep by being specific; boilerplate that
is wrong in three of eight places teaches the next reader to skim them.

## 11. `plan_phase` names a phase the port plan does not have

The record says `"Phase 5 (agent execution and external process invocation)"`.
`docs/plans/deprecated/tasks-go-port-plan.md:556` — "**Phase 5: replace the CLI and
HTTP adapters**", whose body does include "agent invocation", so the *number* is
right. The other four campaign records quote the plan's own title. Use
`"Phase 5 (replace the CLI and HTTP adapters)"`.

## 12. One agent-area test is still claimed by nobody

`test/test_cli_mutations.rb#test_config_reports_the_sibling_memory_path`
(line 3662) sits inside the file's "agent memory guardrails" block and is
claimed by no slice. Its behavior is covered — `config-resolution` claims
`test/test_config.rb#test_cli_config_reports_memory_from_tasks_file_sibling_and_existence`,
which asserts the same resolution — so this is a duplicate-at-CLI-level, not a
hole. But "unclaimed on purpose" should be written down somewhere, per the
project's own standard; `config-resolution`'s gap sentence is the place.

## 13. `agent-harness-adapters`' fixtures_todo contradicts itself in one paragraph

It requires all three `<provider>_command` keys so *"each provider's argv can be
observed by running `tasks -p --provider <name>`"*, then says Hermes'
endpoint probe *"is observably the thing that makes it unavailable"* — i.e.
Hermes never spawns and its argv is never observable. Both halves are correct;
the paragraph reads as if all three argvs were obtainable. Say two.

---

# Where I attacked and found nothing

These are clean bills on categories I genuinely tried to break.

**Completeness of the source inventory.** I walked `lib/llm/**`, `lib/tasks/**`,
`lib/tui/**` and `bin/tasks` independently. Every one of the six files in
`lib/llm/`, plus `lib/tasks/agent_context.rb`, `lib/tasks/agent_diff.rb`,
`lib/tasks/prompt_facts.rb`, `lib/tui/agent_queue.rb` and `bin/tasks`, appears
in some campaign-10 slice's `source_paths`. A grep for every consumer of
`AgentContext|AgentQueue|AgentDiff|PromptFacts|LLM::|LLM.` outside `lib/llm/`
returns only `bin/tasks` (sliced), `lib/tasks/config.rb` (sliced twice, by
`config-resolution` and `prompt-facts`, deliberately and with a gap sentence
explaining the file-not-test overlap), `lib/tui/app.rb` and
`lib/tui/agent_activity.rb` — both explicitly deferred to campaign 12 and named
test-by-test. **There is no API surface for agent execution**, which I confirmed
rather than assumed.

**Completeness of the test inventory.** Zero unclaimed tests in `test_llm.rb`
(35/35), `test_prompt_facts.rb` (10/10), `test_agent_context.rb` (15/15),
`test_agent_queue.rb` (13/13). The only agent-area unclaimed tests are the 4 in
`test_agent_activity.rb` and the 12 in `test_app_agent_queue.rb` — the campaign-12
deferral, named with the right counts. (The "plus 13 more across test_app.rb and
test_app_modals.rb" is an estimate I could not verify precisely — my own
name-based scan finds ~10 unambiguously agent-execution ones plus a larger set of
TUI-prompt-widget tests — but it is a deferral to an unseeded campaign, so the
precision does not matter yet, and 4+12+13=29 is at least arithmetically what it
claims.)

**The 88-test claim, verified mechanically.** Old manifest: 454 distinct claimed
tests. New: 542. Newly claimed: **exactly 88**. Every one of the 88 has
**exactly one** owner. The only 4 double-claimed tests in the whole file are all
pre-existing (`check-meta-and-ids`/`store-snapshot-items`,
`query-projects`/`cli-read-json-envelopes`,
`store-canonical-write`/`delete-task`,
`query-list-filters`/`task-view-projection`) and are the legitimate shared-obligation
case manifest.md permits. No slice lists any test twice.

**`agent-request-queue`'s "no fixture" position is honest, not evasive.** This
was the finding I most expected to make and could not. The shape (`fixtures: []`
+ `fixtures_todo: null`) is one of manifest.md's three honest shapes *provided
`notes` says why in as many words* — and the notes do, invoking the
`query-filter-parse` precedent, and the class comment at lib/tui/agent_queue.rb:6
independently says "it never reads or writes task data itself". Its first
oracle_gap is the loudest sentence in the file: `NO CONFORMANCE CASE CAN DRIVE
THIS SLICE, and the reason is not a fixture gap`, with the correct structural
reason (the case-list format drives one argv; a queue is a sequence of
interleaved calls). It also correctly gets no fixture-gate issue in `plan`,
unlike the other seven. This is the right answer, said loudly enough.

**`agent-harness-protocol`'s TERM→KILL ladder is genuinely provable by its
listed tests.** `test_async_agent_cancellation_escalates_when_child_ignores_term`
(test/test_llm.rb:316) spawns a child that `trap('TERM') {}`, cancels, and
asserts `process_status.signaled?` and `termsig == Signal.list.fetch("KILL")`.
The escalation is real oracle, not aspiration. What that test *cannot* prove —
that the signal goes to the negative pid, i.e. the whole group — is exactly what
the slice's `notes` names as expected defect #1 and what gap #1 says. The
unreached `rescue StandardError` / `Errno::ECHILD` / `detach_unreaped_child`
branches are named in gap #5. Honest.

**Campaign-number cross-references survive.** Playbook item 10 is "Agent
execution and platform process trees"
(docs/plans/deprecated/language-porting-playbook.md:502) — the mapping is right.
Every campaign reference across the file (2×2, 4×3, 5×15, 6×20, 7×5, 8×11, 9×1,
10×9, 12×3) still matches the playbook's items: 5 = temporal/recurrence, 6 =
locking/journal/undo, 9 = OpenAPI server, 12 = Bubble Tea TUI. Nothing in this
pass invalidates a campaign-5, 6, 7, 8, 9 or 12 sentence — **except** finding 3.

**The runner-protocol and abort-path claims, run rather than read.** Against a
real `valid/small-gtd` copy under the complete pin set from
`porting/runners/README.md` (`PATH=/usr/bin:/bin:/usr/sbin:/sbin`, `TASKS_DIR`
and `HOME` = copy root, `XDG_*`, `TZ`, `TASKS_PIN_*`, …):

```
tasks -p "water the garden"   → agent 'claude-cli' not available …   exit 1
tasks -p --json foo           → -p has no --json: …                  exit 1
tasks -p --provider bogus hi  → unknown LLM provider: "bogus" …      exit 1
tasks -p --provider hermes hi → agent 'hermes' not available …       exit 1
```

The `cli-prompt-command` claim that the pinned PATH makes every provider
unavailable and `-p` deterministically abort with exit 1 is **correct** for
three of its four cases. Only the empty-prompt case fails (finding 2).

**The observation-schema gap is accurately described.**
`porting/specs/observations.schema.json`'s `process` block describes the
*invoked* process (exit status, termsig, timed_out, stdout/stderr bytes). There
is no field of any kind for a child the subject spawned, its argv, its
environment, its process group, or signals delivered to it. The recurring gap
sentence is factually right about the schema, whatever finding 10 says about
where it was pasted.

**Assorted specifics I spot-checked and found accurate.** `prompt_facts` really
is in `tasks config --json` (bin/tasks:2914), so `prompt-facts`' "the one slice
fully conformance-testable today except for the fixture" is true.
`MEMORY_MAX_BYTES = 16 * 1024` and the delimiter refusal is
`raw.include?(MEMORY_BEGIN) || raw.include?(MEMORY_END)` — the 16384/16385
boundary pair and the literal `----- END AGENT MEMORY -----` line in
`agent-context-assembly`'s `fixtures_todo` are exactly right.
`porting/fixtures/valid/restricted-mode-store/perms.json` and
`porting/fixtures/valid/symlinked-store` both exist as cited. The
`agent-harness-adapters` notes claim — Claude takes the system context as
`--append-system-prompt`, Cursor and Hermes prepend it to the prompt text
because their CLIs have no such flag — is correct in all three adapters, and it
is the right reason to keep them as one slice. The campaign-10 record in
`campaigns.jsonl` carries the same five keys as the other four.

---

## Ranked summary

| # | Severity | Finding |
|---|---|---|
| 1 | **must fix** | `llm-provider-registry` claims two adapter-argv tests owned by a downstream slice; `reach` structurally cannot see it |
| 2 | **must fix** | `tasks -p` with no words emits a Ruby NameError backtrace; the slice's `fixtures_todo` calls that case observable today |
| 3 | **must fix** | `cli-mutation-json-envelopes`' gap sentence left saying no slice can claim a test `cli-prompt-command` now claims |
| 4 | **must fix** | `llm-provider-registry`'s abort message does not name the model; the `valid/llm-config` fixture exposes one bit, not "every branch" |
| 5 | **must fix** | whole-file reformat hides the two surgical edits and destroys per-record blame |
| 6 | later | `command_on_path?`'s explicit-path branch — the corpus's load-bearing mechanism — untested and ungapped |
| 7 | later | fixture story's stated reason conflates parent cwd with child chdir; true only while cases leave `cwd` at `"."` |
| 8 | later | `agent-diff-report`'s oracle drives `capture` with no dep and no explanation |
| 9 | later | fake harness's `--exit N` convention not buildable as specified |
| 10 | later | PROCESS-SPAWN GAP paragraph pasted into three slices it does not describe |
| 11 | later | `plan_phase` renames the port plan's Phase 5 |
| 12 | later | `test_config_reports_the_sibling_memory_path` unclaimed and unremarked |
| 13 | later | `agent-harness-adapters`' fixtures_todo promises three observable argvs, delivers two |

**LAND WITH FIXES** — 1 through 5.
