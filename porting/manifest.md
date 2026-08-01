# `manifest.jsonl` — the slice record

`manifest.jsonl` is the port's unit of account. One line per **proof-sized
capability**: one reviewable change, one evidence bundle, one td issue. It is
the only place that says how much behavior there is left to move, and
`manifest-issues progress` is the only place that counts it. There is no
hand-written percentage anywhere in this tree, and adding one is a regression.

`campaigns.jsonl` sits beside it with one line per campaign — the playbook's
"proposed Tasks sequence" groupings. Campaign titles live there rather than
being repeated on every slice, so a retitled campaign cannot half-drift across
twenty records.

## Who writes what

| Field | Written by | When |
|---|---|---|
| `id`, `campaign`, `behavior`, `target_package` | the agent that slices | at slicing, then effectively frozen — `id` is the durable identity in td and on disk |
| `depends_on`, `risk` | the agent that slices | at slicing; changed only with a stated reason in the td issue |
| `source_paths`, `source_sha` | the agent that characterizes | at slicing, refreshed only by an agent that has re-characterized against the new Ruby |
| `ruby_tests`, `oracle_gaps` | the agent that characterizes | before translating |
| `fixtures`, `fixtures_todo` | the fixture author (td-a1d16a) and the slice's agent | as the corpus lands |
| `observable_outputs`, `platforms`, `perf_budget` | the agent that characterizes | before translating |
| `status` | the agent holding the td issue | at every handoff |
| `evidence` | derived — always `porting/evidence/<id>/` | never edited by hand |
| `intentional_differences` | **Marcus only** | when he accepts a divergence; the record also lands in `intentional-differences.md` |
| `notes` | anyone | whenever something is worth the next agent's attention |

Editing a td issue's title or body does **not** feed back here. The projection
is one-way: change the manifest, run `porting/manifest-issues sync`.

## Fields

| Field | Type | Meaning and allowed values |
|---|---|---|
| `id` | string | Stable slug, unique across the file. Appears in td as the label `slice:<id>`, on disk as `porting/evidence/<id>/`. Never reused, never renamed. |
| `campaign` | integer | Campaign number from the playbook's "A proposed Tasks sequence". Must have a record in `campaigns.jsonl`. |
| `behavior` | string | What observable behavior this slice ports, in one sentence. Becomes the td issue title. |
| `target_package` | string | Intended Go package (`internal/record`, `internal/store`, …). A hint for slicing, not a commitment; two slices may share one. |
| `depends_on` | array of `id` | Slices that must be green first. Edges must be **true**, not merely plausible: canonical write precedes anything that mutates, parsing precedes queries. Cycles are a validation failure. |
| `risk` | `low` \| `medium` \| `high` | PORTING.md's tier table, which decides the required evidence. Tiering *up* is allowed and must be explained in `notes`; tiering down is not. |
| `source_paths` | array of repo paths | The Ruby this slice **ports** — deliberately the narrow set. Not the drift query's argument: that is the transitive require closure of these paths (below). |
| `source_sha` | 40-char sha | The Ruby revision this slice was characterized against. See below. |
| `ruby_tests` | array of `path` or `path#test_name` | The existing Ruby oracle. Every entry is checked to exist by `manifest-issues validate`. Empty is only allowed when `oracle_gaps` says why. |
| `oracle_gaps` | array of strings | What the Ruby suite does *not* prove for this slice, and what is deliberately excluded (usually because it belongs to a later campaign). A gap is a finding to act on, not a blank to leave. |
| `fixtures` | array of repo paths under `porting/fixtures/` | Fixture directories this slice is proved against — the directory, not the `store/` inside it, because a runner copies the whole thing. Checked to exist. |
| `fixtures_todo` | string or null | What corpus the slice still needs, named precisely enough to build. A slice may not pass `characterizing` while this is non-null, and its td issue is gated behind a fixture-gap issue for as long as it is set (below). A near-miss wired into `fixtures` is worse than an honest todo: it makes a slice look provable when it is not. |
| `observable_outputs` | array of strings | What a user or an agent can actually see change: bytes, stdout, exit status, resource fields. The comparator's scope for this slice. |
| `platforms` | array | `["any"]`, or the specific targets when the behavior is platform-dependent (`darwin`, `linux`, `windows`). |
| `perf_budget` | string or null | A budget only where one is real. `null` is honest; an invented number is not. |
| `status` | enum, below | Where the slice is. The only progress signal. |
| `evidence` | string | Always `porting/evidence/<id>/`. td log entries point at it; evidence never lives only inside td. |
| `intentional_differences` | array of strings | Accepted divergences from Ruby. Only Marcus adds one. |
| `notes` | string or null | Anything the next agent should know before starting — especially the expected defect. |

### `status`

| Value | Means |
|---|---|
| `not_started` | seeded, unclaimed |
| `characterizing` | capturing the Ruby oracle; no Go written |
| `translating` | Go being written against the capture |
| `conformance` | differential conformance running |
| `reviewing` | in review at this slice's tier |
| `ported` | terminal — proved, landed, evidence bundled |
| `not_applicable` | terminal — the behavior does not exist in the port (with a reason in `notes`) |
| `blocking_cutover` | terminal — known unported and named as a cutover blocker |
| `blocked` | stopped on an escalation; the td issue says on what |

`ported`, `not_applicable`, and `blocking_cutover` are the three terminal
statuses PORTING.md's drift rule accepts.

## `source_sha`: how it is chosen, and how it is refreshed

`source_sha` is what makes the drift rule enforceable. PORTING.md: *if mainline
Ruby changed since a manifest entry's `source_sha`, that entry must end
**ported**, **not applicable**, or **blocking cutover**.* Without a real sha
that rule is decoration.

### The closure, and why `source_paths` is not the drift query

A slice ports the files in `source_paths`. It has to *reproduce* the behavior
those files produce — and much of that behavior is produced by code they call.
`check.rb` validates a recur cookie through `Recur` and an `updated` value
through `UpdateStamp`; `task_queries.rb` requires nine modules and names one;
`store.rb` requires nineteen. Drop `"wed"` from `Recur::DAYS` and a fixture the
port is proved against changes its `check` outcome — while a
`source_paths`-only drift query reports nothing at all. A drift rule with that
hole is decoration again, in the more dangerous direction: it reads green.

So drift watches the **transitive `require_relative` closure** of
`source_paths`, computed rather than stored — a derived set copied into the
manifest is a second thing to rot. Inspect it with:

```sh
porting/manifest-issues closure          # per slice: what is watched, and the last commit that touched it
porting/manifest-issues closure --json
```

Only `require_relative` is followed. `require "json"` is stdlib, and a stdlib
version bump is not what this rule is for. Coverage is checkable as a
consequence: every file under `lib/tasks/` sits in some slice's closure except
`lib/tasks/api/`, which no campaign 2-4 slice ports.

**Chosen** — the last commit that touched the slice's **closure**, at the moment
the slice is characterized:

```sh
git log -1 --format=%H -- $(porting/manifest-issues closure --json | …)
```

Not `HEAD`: a sha that moves whenever any unrelated file changes would report
drift on every commit, and a rule that always fires is a rule nobody reads.
Pinning to the closure's own last-touch commit means drift fires exactly when
code that produces *this slice's* behavior moved.

**Checked** — `porting/manifest-issues drift` runs, per slice:

```sh
git log --format=%H <source_sha>..HEAD -- <closure...>
```

Non-empty output is drift. It exits non-zero when a drifted slice is not at a
terminal status, which is the CI gate PORTING.md describes.

**Refreshed** — only by an agent that has *re-characterized the slice against
the new Ruby*: re-read the diff, re-capture the oracle, confirm the port still
matches or file the parity work. Then bump `source_sha` to the new last-touch
commit in the same commit that records what was re-proved. Refreshing the sha
to silence `drift` without redoing the capture is the same unforgivable move as
blessing Go output — it launders an unexamined change into a green manifest.

## Out of port scope — do not seed slices for these

**Marcus, 2026-08-01: schema-v1 migration and org-mode are out of scope for the
port. The reason is that no schema-v1 users exist.** This is a scope decision,
not an unfinished slice, and it does not go through the
intentional-differences path.

It is no longer a carve-out, either. **td-09f7de deleted the migration machinery
from Ruby** rather than asking every mutation slice to file an intentional
difference for a dead branch: there is nothing left in the oracle to skip.
Removed there: `tasks migrate` and its dispatch, `Store#migrate_schema!`,
`MigrationResult`, `Application#migrate_schema`, the `:migration_required`
status across `MutationResult` / `ApplicationReadResult` / `Store::CheckedRead`,
the API's `409 schema_migration_required`, the cross-version branch in
`jsonl_merge`, and the TUI's migrate prompt. Org-mode is gone entirely — no
importer, no compatibility case, no future use.

What replaced it is contract, and **is** in scope:

- Any declared `meta` version other than `Format::VERSION` — older or newer —
  is refused identically. `Check` reports `unsupported meta version <n>
  (expected 2)`; `check-meta-and-ids` carries that.
- The store gates mutations on the same rule (`Store#unsupported_schema?`,
  checking both the live file and the archive) and refuses with the typed
  `:unsupported_schema` status, writing nothing. The API maps it to
  `503 unsupported_schema_version`.
- A port that omits the gate is not "missing a dead branch"; it is a
  data-corruption bug, because v1 bytes would then be parsed as v2.

## Validation

```sh
porting/manifest-issues validate    # exits non-zero and names every problem
```

It checks: required keys and no unmodelled ones; `risk` and `status`
vocabularies; every `campaign` has a record; unique ids; dependencies resolve
and contain no cycle; `evidence` matches the id; `source_sha` is a real 40-char
sha; every `source_paths` entry exists; **every `ruby_tests` entry exists — the
file, and the `def test_...` inside it**; every `fixtures` entry exists; and
that a slice with no oracle test says so in `oracle_gaps` rather than leaving
the field silently blank.

The fixture fields have three honest shapes and one dishonest one. Wired
fixtures; or a `fixtures_todo` naming what the corpus lacks; or — for a slice
that genuinely operates on no store, like `query-filter-parse`, whose corpus is
an argv list — neither, provided `notes` says in as many words why it needs no
fixture. Both blank and silent is the shape validation rejects: it reads as
"wired" and proves nothing.

`sync` refuses to run against a manifest that does not validate.

## Projecting into td

```sh
porting/manifest-issues plan        # what sync would do; touches nothing
porting/manifest-issues sync        # create/update; safe to re-run
porting/manifest-issues sync --json # machine-readable actions + summary
porting/manifest-issues progress    # the only progress count that exists
```

One epic per campaign, one issue per slice, `depends_on` wired as td dependency
edges, so "next ready work" is a td query and the fleet needs no second
scheduler.

Identity is a **label**, never a title: the issue for slice `format-parse` is
whichever issue carries `slice:format-parse`. Retitle it, close it, reopen it —
the mapping survives, because nothing about it is derived from text a human
might edit. Two issues carrying one identity label is therefore not a state to
resolve quietly, and `sync` **refuses to run** until it is fixed by hand:
last-one-wins would repoint every dependency edge at the newcomer and silently
orphan the issue an agent may already have claimed, then converge and report
"nothing to do" over a permanently wrong graph. Two concurrent syncs are enough
to produce it.

Edges are cleaned up as well as added. An edge from a slice to an issue this
script owns — a retired slice's issue included — or to an id td no longer
resolves at all is removed, because neither can ever be satisfied, and in a
fleet whose ready-work query is a dependency query, an unsatisfiable edge means
permanently unstartable. An edge to a live issue outside the fleet is a human's
and stays. Epics carry `porting-campaign` plus `campaign:<n>`; slice issues
carry `porting-slice`, `slice:<id>`, `risk:<tier>` and their `campaign:<n>`
membership. Labels outside that set (a human's `flaky`) are left alone.

Every write is preceded by the read that decides whether it is needed, so a
second run creates nothing, updates nothing, and touches no edge — and a run
interrupted halfway is safe to repeat.

### Fixture gaps are gates, not footnotes

`td ready` lists open issues whose dependencies are met. A slice whose
`fixtures_todo` is non-null cannot pass `characterizing`, so listing it there
sends a claiming agent at work it must hand straight back — the queue lies to
the fleet, and the cost is a wasted claim per gap per agent.

So `sync` projects each non-null `fixtures_todo` as its own issue — labelled
`porting-fixture-gate` plus `fixture-gate:<slice-id>`, parented to the campaign
epic, carrying the todo text verbatim — and wires the slice to depend on it.
The consequences are the ones wanted: the slice leaves `td ready`, the *fixture
work* enters it, and the gap is visible as work rather than as a field nobody
queries.

The gate is temporary by construction. Wire the corpus, set `fixtures_todo` to
null, re-run `sync`: the edge is removed and the gap issue is reported as an
`ORPHAN` for a human to close, because it holds the record of what was built.
Nothing about this writes a td status — `td block` would, and would stomp a live
claim; a dependency edge does not.

Two things `sync` deliberately does not do:

- **It never writes a td status.** td owns work state (`open`, `in_progress`,
  `in_review`); the manifest's `status` is port progress. Syncing either
  direction would let a generator run stomp on a live claim.
- **It never deletes.** A td issue labelled for a slice the manifest no longer
  has is reported as `ORPHAN` and left alone — dropping a slice is a decision,
  and the issue may hold the evidence of why.
