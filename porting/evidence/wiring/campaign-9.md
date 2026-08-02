# CAMPAIGN 9 — seeding the HTTP API

Companion to the 22 records in `campaign-9-records.jsonl`, which are proposed for
`porting/manifest.jsonl` and are **not** merged by this pass — `manifest.jsonl`
and `campaigns.jsonl` are merged centrally, and five sibling agents were seeding
campaigns 5, 6 and 12 at the same time.

Prerequisite reading, in order: `campaigns-5-6-9-12-inventory.md` (the partition
this pass consumes without re-litigating), then `campaign-10.md` (the quality bar,
and the precedent for a slice that the differential harness cannot prove).

Result: **22 slices, 52 → 74.** 109 previously unclaimed Ruby tests now have an
owner — all 108 in `test/api/*` plus the one test in `test/test_store.rb` the
inventory's Appendix A assigns to C9. **Four source files, plus `config.ru` and
`docs/api/openapi.yaml`**, neither of which was in any drift closure before.

Every slice carries `"fixtures": []`, `"fixtures_todo": null`, and an
`oracle_gaps` entry stating the method and its consequence. **No slice asks for
an HTTP surface.**

---

## 1. The method, decided before the slicing

This is the section that matters most, because it changes what a green run means
for a fifth of the manifest.

`surface: "http"` is reserved in `porting/specs/observations.schema.json` and
unimplemented in the runner, whose known-gaps list says flatly *"`http` is always
empty and `surface` is always `cli`."* The only HTTP field anywhere in the schema
is `revisions.http_etag` — there is no field for a status code, a request or
response header, a response body distinct from `process.stdout`, a method, or a
path. And the eleven-step copy-per-case protocol is copy → probe → invoke **one**
argv → probe → emit; a long-lived server that must be started, waited for, driven
N times and stopped does not fit it, and the before/after probes assume nothing is
holding the store between them.

The inventory's §7 recommendation was that campaign 9 "budget its first slice for
the harness, not the code." **Marcus's decision is the opposite, and it is taken:
do not extend the harness. Campaign 9 is proved by translated unit tests.** The
Go port reimplements `test/api/*` against the Go server, and those tests are the
evidence — the same shape `agent-request-queue` is in, and the same shape
`agent-diff-report` is in for a different structural reason.

The reasoning, recorded so it is not re-argued: this is a personal task manager,
and the API is a secondary surface behind the CLI and the TUI. A differential
HTTP harness would be a large, permanent investment in the conformance protocol —
a second surface in the runner, a server lifecycle in the copy protocol, and new
observation fields for status, headers and body — to prove one campaign of 22
slices. It is not worth its cost here.

**The consequence, stated plainly and repeated in all 22 records: a fully green
conformance run proves nothing about campaign 9, because no case in the corpus
touches the HTTP surface at all.** Absence of conformance cases here is a
recorded method choice, not an oversight and not a fixture gap. That is why
`fixtures_todo` is null everywhere rather than carrying twenty-two copies of the
same wish; a `fixtures_todo` is a request for a corpus, and no corpus can express
a request.

Two consequences that follow and are easy to miss:

- **Every `notes` field says the same thing about fixtures**, because the
  validator requires it: a slice with neither `fixtures` nor `fixtures_todo`
  must say in `notes` why it needs none (`manifest-issues validate` matches
  `/fixture/i` against `notes`). That is the third honest shape, and it is the
  one all 22 records take.
- **`reach` is blind to this campaign.** `VERB_OWNERS` maps store mutation verb
  methods only, so an oracle that reaches downstream through an HTTP route is
  invisible to the tool — it reports 0 unexplained no matter what campaign 9
  claims. `campaign-10.md` §4g found this; the inventory's §5e says it applies
  with more force here. Every record says so, and reading the test body is the
  only defence.

---

## 2. What the survey found that the inventory did not

The partition was consumed as authoritative and no test was moved between
buckets. Four things came out of reading the code that the inventory could not
have seen from counts.

### 2a. Seven documented endpoints are unrouted, not three

The brief named `/events`, `/history/undo|redo` and `/archive-sweeps`.
`docs/api/openapi.yaml` declares **26 paths**; `App#dispatch` routes **19**. The
seven with no route are:

| Path | Verb | Capability flag | Test |
|---|---|---|---|
| `/api/v1/history` | GET | — | — |
| `/api/v1/history/undo` | POST | `undo: false` | 404 asserted |
| `/api/v1/history/redo` | POST | `redo: false` | 404 asserted |
| `/api/v1/archive-preview` | GET | — | — |
| `/api/v1/archive-sweeps` | POST | `archive_sweep: false` | 404 asserted |
| `/api/v1/views/{name}` | GET | — | — |
| `/api/v1/events` | GET | `events: false` | — |

So **four of the seven are documented, unrouted, and proved by nothing**, and
three of those four are not even named in the capability map.
`/api/v1/views/{name}` is the dangerous one: it is documented with a five-value
enum and a described grouping contract, and `query-named-views` has already
ported the selection logic a Go agent would need to implement it. A port that
built it would break no test in this campaign and no capability assertion either.

### 2b. `bin/tasks-api` drops `host_context`, and no test can see it

`bin/tasks-api` serializes nine keys into `TASKS_API_RESOLVED_CONFIG` — `org`,
`archive`, `urgent_days`, `max_depth`, `timezone`, `time_format`, `links`,
`link_systems`, `port` — and `config.ru` rebuilds `Config::Paths` from exactly
those nine. `host_context` is not among them and defaults to `nil`, so
`Application.new(host_context: paths.host_context)` gets `nil` in production.

A user with `host_context.<hostname> = @home` configured therefore gets the
context applied by `tasks capture` and **not** by `POST /api/v1/tasks` through
the real server. The unit suite cannot see it —
`test_create_adds_configured_host_context_and_supports_explicit_opt_out` builds
the App directly and sets `paths.host_context` by hand — and
`test/api/test_black_box.rb`, the only suite that runs the real entrypoint, does
not check it. The same omission applies to `hostname`, `host_context_source`,
`host_contexts` and `sources`.

**This is a product bug, not a manifest bug**, and it is filed as an
`oracle_gaps` sentence on `api-server-entrypoint` and on
`api-task-write-lifecycle` rather than fixed. It should go to Marcus as a td
issue. The manifest consequence is the one that matters here: a Go entrypoint
that threads `host_context` through would be landing a behavior change under
cover of a translation, and PORTING.md forbids exactly that.

### 2c. Two files were in no drift closure and now are

`config.ru` lives at the repository root, so it is under neither `lib/` nor
`bin/` and appeared in **no bucket of the inventory's 94-file survey** and no
slice's closure. `docs/api/openapi.yaml` — 3600 lines of published contract, the
thing every `assert_contract_response` in the suite validates against — was in no
closure either. `api-server-entrypoint` names the first and `api-openapi-contract`
names the second, so both are watched from this pass onward.

### 2d. The global store revision is campaign 9's, and the per-task revision is campaign 6's

Both live in `lib/tasks/store.rb`, which is why this needs saying. The inventory
§2b lists `lib/tasks/store.rb` under C6 ("`with_lock`, `undo!`/`redo!`,
revisions") and does not list it under C9 at all. But `store_revision_for_contents`
— the `s1.<sha256>` digest over the live and archive raw bytes — is reached only
from `checked_read_snapshot`, which is called only from
`Application#checked_query`, whose own comment says it exists for API-grade
reads. Nothing else in the product uses it.

So `api-read-model-and-store-revision` names `lib/tasks/store.rb` and ports
`checked_read_snapshot` and the global digest; `revision_for(item)`, the
`own`/`location` component that becomes an ETag, stays campaign 6's, and
`api-etag-preconditions` ports only its transport. This is §3e's rule — the
dividing line is which side of the store boundary the test observes from —
applied to revisions rather than to locks. Nine slices already name
`lib/tasks/store.rb`; this is the tenth.

---

## 3. Where the cuts are

Twenty-two slices. The graph is a spine with four independent branches hanging
off it, and the order below is a valid topological order (every record's
`depends_on` names only ids declared before it, which is checked in §7).

```
api-openapi-contract
  └─ api-transport-envelope
       ├─ api-routing-and-service-metadata ── api-unrouted-endpoints
       ├─ api-recurrence-explain
       ├─ api-server-entrypoint
       └─ api-read-model-and-store-revision
            ├─ api-project-reads ─┬─ api-project-create
            │                     ├─ api-project-rename-and-complete
            │                     └─ api-project-archive
            └─ api-task-representation
                 ├─ api-task-collection-queries
                 └─ api-etag-preconditions
                      ├─ api-delegation-actions
                      └─ api-task-write-lifecycle
                           ├─ api-ordered-placement
                           ├─ api-proposal-decisions
                           ├─ api-temporal-fields
                           └─ api-recurrence-and-lead-fields
api-server-entrypoint + api-task-write-lifecycle + api-ordered-placement
                                        └─ api-cross-process-writes
api-server-entrypoint + api-read-model + api-etag-preconditions + api-ordered-placement
                                        └─ api-cross-process-freshness
```

Nineteen edges cross a campaign boundary into campaigns 2, 3 and 4, naming
eighteen distinct slices (`check-meta-and-ids`, `check-tree-structure`, `store-snapshot-items`,
`task-view-projection`, `query-filter-parse`, `query-list-filters`,
`query-projects`, `create-basic`, `changeset-apply-basic`, `delete-task`,
`task-placement`, `proposal-decisions`, `delegation-assign`,
`delegation-claim-release`, `section-create-and-rename`,
`project-complete-and-close`, `archive-project`, `config-resolution`), which is
correct and matches campaigns 7 and 10: the API is an adapter over a domain those
campaigns already own.

**Three edges are owed and cannot be written yet.** Campaigns 5, 6 and 12 were
being seeded in parallel with this pass, so their slice ids do not exist in
`porting/manifest.jsonl` and `validate` would reject them. The three are recorded
as `oracle_gaps` sentences on the slices that need them and must become
`depends_on` entries at merge:

1. `api-etag-preconditions` → campaign 6's per-task revision slice.
2. `api-temporal-fields` and `api-recurrence-and-lead-fields` → campaign 5's
   `TemporalValue`/`Timezones` and `Recur`/`Lead` slices.
3. `api-cross-process-writes` → campaign 6's journal slice (its undo half).

### 3a. Why the shell is three slices and not one

`api-openapi-contract`, `api-transport-envelope` and
`api-routing-and-service-metadata` could have been one "HTTP plumbing" slice.
They are three because they fail in three unrelated ways and want three
different reviews. The contract is a YAML document and a validator-strictness
question. The envelope is where the three separate body readers live — and they
*are* three, deliberately: `json_body` demands `application/json` before reading,
`read_optional_body` reads first and tolerates an empty body, and
`reject_delete_body!` reads first and raises 400 on a non-empty one. A port that
unifies them changes at least three status codes, and only one test covers the
DELETE pair. Routing is the one where an idiomatic Go router silently introduces
405s: **there is no 405 anywhere in this server**, a routed path reached with an
undeclared verb falls through every match to the terminal 404, and exactly one
test proves it, for three routes.

### 3b. Why the unrouted endpoints are a slice

Because the behavior being ported is an **absence**, and an absence is the one
thing a translator reads as an omission and fixes. `api-unrouted-endpoints` is a
low-risk slice with one test and no code, and it exists so the absence has an id,
a dependency edge and a status — visible in `progress`, claimable, reviewable.
`slicing.md` §1 item 2 already recorded what happens to an exclusion stated once
in prose and never picked up: `lib/tasks/opener.rb` sat in no slice for a whole
pass because `links-read` excluded it with a reason.

The record does three things a sentence in someone else's `oracle_gaps` could
not: it enumerates all seven paths and names the four with no proof (§2a); it
states that `/events` has no Ruby to port, that its own contract documents its
deferral and why (an open SSE response pins one Puma thread for its lifetime),
and that a Go implementation would be a new feature needing a Marcus decision;
and it quotes the Ruby's own rule for the future — a change that adds an endpoint
flips the capability flag and deletes the matching 404 assertion **in the same
change**.

The inventory §6 left three options open for `/events` — `not_applicable` with a
reason, `blocking_cutover`, or no slice. None was taken. `/events` is folded into
`api-unrouted-endpoints` alongside its six siblings, because a `not_applicable`
record for one of seven unrouted paths would imply the other six were applicable,
and a `blocking_cutover` would assert that HTTP event streaming blocks a cutover
that no user is waiting on.

### 3c. Why the two black-box slices are two

`api-cross-process-writes` (4 tests) is about writes interleaving; the assertions
are byte equality of the store after a race, and — in
`test_ordered_placements_survive_cli_churn_and_match_stable_id_cli_bytes` — byte
equality between a file the HTTP adapter wrote and a file the CLI wrote from the
same starting bytes, with only the two moved records' `updated` and `line`
stripped. That is the strongest assertion in the product and the one a port is
most likely to "fix" by widening the normalization, which would be the
never-bless-Go-output rule violated inside a comparator rather than inside an
expectation.

`api-cross-process-freshness` (4 tests) is about a running server observing
changes it did not make. What it really proves is that the server holds **no
state between requests** — no snapshot, no revision, no store handle — which is
stated nowhere in the code. The obvious optimization (cache the snapshot,
invalidate on mtime) breaks exactly one test, and breaks it in a way that looks
like flakiness on a filesystem with coarse mtime granularity.

Both slices are §3e's territory and both records repeat the inventory's warning:
campaign 6 must not claim these because the words "concurrent" and "lock" appear
in them, and campaign 9 must not claim
`test_journal.rb#test_concurrent_writers_do_not_lose_updates` because it looks
like the same property.

### 3d. Why recurrence and lead are one slice and explain is another

`normalize_recurrence` and `normalize_lead` are the same eleven lines with two
identifiers changed. Both test files say in their own headers that the model is
proven in campaign 5 and that what is asserted here is *transport*. Splitting
them would produce two slices with one shared decision and two identical reviews
— the argument `agent-harness-adapters` made for keeping three CLI adapters
together. Thirteen tests in one record is the campaign's largest `ruby_tests`
list and is the correct shape.

`api-recurrence-explain` is separate because it is the asymmetry: recurrence has
a taskless projection endpoint and lead does not. It is also the only route in
the API that is a pure function and the only one that stays available while the
store is unreadable — `test_explain_needs_no_readable_store` corrupts the store
and asserts a 200. **The obvious refactor — route explain through the same
`checked_query` gate as everything else — breaks that test and nothing else.**
It is the natural first slice to port in this campaign: nothing depends on it, it
depends only on the envelope, and it is the one slice tiered low.

### 3e. Why projects are four slices

`api-project-reads` owns the strict-nulls projection (`Representation.project`
emits every field with an explicit null, unlike `ProjectView#to_h`'s on-disk-lean
CLI shape — so the two renderings differ in *key set*, and a Go struct with
`omitempty` tags shared between them fails contract validation). `api-project-create`
owns the only mutation in the API that re-reads through a second round trip.
`api-project-rename-and-complete` is one slice because `project_after_mutation`
is one mechanism with two hand-built view synthesizers, and neither synthesizer
is provable without the other. `api-project-archive` is separate because it
shares none of that machinery and because its guard — deferred work counts as
open work and blocks an unforced archive — is four lines of adapter policy the
domain does not repeat, whose failure mode is data loss reachable from one HTTP
call.

The common thread across all four, and the reason three of them are tiered high:
**the project routes have no If-Match, because the domain exposes no per-section
revision.** Every project ETag is the global store revision, so the pre-read, the
write and the post-read are three unguarded lock acquisitions. `test_rename_requires_no_if_match`
is an absence assertion that a port hardening If-Match uniformly would break.

---

## 4. What is deliberately out of scope

- **The 125 PRIOR tests and the one PRIOR file** (`lib/tasks/operation_context.rb`).
  Untouched, per the inventory's instruction. `OperationContext`'s
  `SOURCES = %i[cli tui api]` makes it look like campaign 9's; it is the
  application facade's, and no campaign owns that yet. Campaign 10 declined it
  once for the same reason.
- **The journal, undo, redo and the archive sweep themselves** — campaign 6.
  Campaign 9 owns only the decision not to expose them over HTTP (§3b) and the
  fact that a CLI undo is visible to a running server (`api-cross-process-freshness`).
- **`Recur`, `Lead`, `TemporalValue`, `TemporalContext`, `Timezones`** — campaign
  5. Two campaign 9 slices assert HTTP behavior whose observable value is a
  string those files produce; both say so and both are exposed to landing before
  campaign 5 does.
- **`test/test_cli_json_coverage.rb`'s registry-wide tests** — campaign 8's final
  parity slice, which the inventory §7 item 5 forbids any of the four passes from
  seeding. Nothing here touches them.
- **Extending `porting/specs/observations.schema.json`.** No schema change is
  proposed, per the brief and per `campaign-10.md`'s precedent.

---

## 5. Existing records this pass falsifies

**None.** Every `oracle_gaps` sentence in the 52 existing records that mentions
the API stays true after these 22 land. In particular the sentence that appears
in twenty records — *"the API answers `503 unsupported_schema_version`"* — is
exactly what `api-read-model-and-store-revision` ports, and
`test_an_unsupported_schema_version_is_refused_on_read_and_on_write` is now
claimed rather than unowned.

**Two records are owed an id** — the "should amend" class the inventory §4
already anticipated, where the sentence stays true and acquires a slice it should
name:

1. **`task-view-projection`**, `oracle_gaps` (the three-tag-projections entry):
   > "`Representation.task` (the API, campaign 9) strips both"

   should name **`api-task-representation`**. This is the inventory's §4 item 12
   verbatim, and it is now discharged on this side: `api-task-representation`
   names the divergence, points back at `valid/deferred-tags`, and records that
   the fixture pins it for the CLI side only because no HTTP case can read it.

2. **`task-view-projection`**, `oracle_gaps` (the key-set entry):
   > "The only exact-key-set assertion in the suite is
   > test/api/test_app.rb#test_task_representation_and_source_exact_lookup … It
   > was claimed here until 2026-08-01 (td-940935) and has been dropped as
   > misattributed."

   That test now has an owner and the sentence should say which:
   **`api-task-representation`**. The asymmetry the sentence describes is real and
   worth keeping — the API's key set is pinned exactly, TaskView's by nothing.

Neither is applied here. `manifest.jsonl` is merged centrally, and
`campaign-10.md` §6's process note is the reason this pass touched no existing
line: change only the records you intend to change, and leave every other line
byte-identical.

---

## 6. What is left for the merge

1. **`source_sha` is the literal string `"PENDING"` in all 22 records**, per the
   brief. `validate` rejects it (22 lines of "source_sha is not a 40-char sha")
   and nothing else. The correct values were computed from `closure --json`'s
   `last_touch` against a merged scratch copy, as `manifest.md` prescribes, and
   there are three distinct ones:

   | Slices | `source_sha` |
   |---|---|
   | `api-openapi-contract` | `fc84567fe270472201110fc4da1493a0482519bf` |
   | `api-server-entrypoint` | `174165be85c9e2cefbe250b8bb71e72c543c2136` |
   | the other 20 | `87d8cc201410669e5b4ed1987eb44a01946ae92f` |

   They differ because the closures differ, which is the point of the rule.
   **Re-compute at merge rather than pasting these** — the tree will have moved,
   and a `source_sha` pinned without re-characterizing is the same unforgivable
   move as blessing Go output.

2. **A campaign 9 record in `campaigns.jsonl`.** Not written here. Suggested
   fields, matching the existing five: `plan_phase` "Phase 5 (adapters and
   surfaces)", title "OpenAPI server, ETags, error handling, events, and
   cross-process concurrency", and a summary that states the translated-unit-test
   method at campaign level so it is not discovered one record at a time.

3. **The three owed `depends_on` edges** into campaigns 5 and 6 (§3).

4. **The two `task-view-projection` amendments** (§5).

5. **The `host_context` bug filed as a td issue** (§2b).

**Nothing about the pre-existing drift is touched.** No campaign 9 slice names
`bin/tasks`, so none inherits the `9b9e6e9` closure the inventory §0 warns about,
and `drift` is clean against the merged scratch copy.

---

## 7. Verification

Run against a scratch merge (`porting/manifest.jsonl` + these records, plus a
scratch campaign 9 record) via the `MANIFEST_ISSUES_MANIFEST` /
`MANIFEST_ISSUES_CAMPAIGNS` seams, so nothing tracked was written:

```
porting/manifest-issues validate → 22 × "source_sha is not a 40-char sha"
                                   and NOTHING ELSE                    (exit 1)
porting/manifest-issues drift    → no drift: every slice's Ruby source is
                                   unchanged since its source_sha      (exit 0)
porting/manifest-issues reach    → 22 reach(es), 0 unexplained         (exit 0)
porting/manifest-issues progress → slices: 0/74 at a terminal status
                                   campaign 9: 0/22
bundle exec ruby test/api/all.rb → 108 runs, 2660 assertions, 0 failures
```

The one reach this pass added is `api-etag-preconditions`'
`test_preconditions_stale_current_and_delete_cascade_conflict`, whose oracle
drives `Store#apply_changeset!` directly to manufacture a stale precondition —
`changeset-apply-basic`'s verb, not upstream of this slice. No dependency edge is
added: a stale precondition needs *some* competing writer, not that one. It is
explained by name in `oracle_gaps`, following `agent-diff-report`'s precedent.

Checked by hand, additionally:

- **All 109 bucket tests are claimed exactly once**, computed as a set difference
  against every `def test_` in `test/api/*.rb` plus the single `test/test_store.rb`
  ref: `missing=[] extra=[]`. **No test is shared with any of the 52 existing
  records.**
- **No slice id collides** with the 52, and the 22 are unique among themselves.
- **Every `depends_on` names a real id** — 18 distinct campaign 2/3/4 ids over 19 edges,
  and 10 distinct intra-campaign ids over 27 edges — and the file is in
  topological order: no record depends
  on an id declared after it.
- **Every `source_paths` entry resolves.** Six distinct files across the campaign:
  the four the inventory assigned (`bin/tasks-api`, `lib/tasks/api/app.rb`,
  `errors.rb`, `representation.rb`), plus `config.ru` and `docs/api/openapi.yaml`
  which were in no closure, plus the five shared names the inventory §2b
  predicted (`lib/tasks/application.rb`, `application_read_result.rb`,
  `task_view.rb`, `config.rb`, and — with the §2d reason — `store.rb`, plus
  `task_queries.rb` and `delegation.rb` for drift on the vocabularies).
- **Key set and ordering are byte-identical** to the existing records', taken from
  `manifest.jsonl`'s first line rather than transcribed.
- **`fixtures == []` and `fixtures_todo == null` on all 22**, and every `notes`
  states why, which is what `validate` requires of the third honest shape.
