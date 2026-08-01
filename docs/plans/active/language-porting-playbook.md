# A modern language-porting playbook

- **Status:** research and proposed method; no Tasks port is approved
- **Research snapshot:** 2026-07-31
- **Related issue:** `td-ecae44`
- **Companion plan:** [tasks-go-port-plan.md](tasks-go-port-plan.md)

## Recommendation for `tasks`

Build the Go version beside Ruby in the same repository, but do not join the
two runtimes with an in-process FFI bridge. Run them as separate executables
against copied fixture directories and compare their observable behavior.
Port one vertical capability at a time, keep Ruby as the oracle, and make every
landed Go slice pass a language-neutral conformance suite.

That combines the best parts of the current approaches:

- Bun's deterministic work manifest, specialized agent roles, adversarial
  source review, and explicit mainline-parity sweep;
- Turborepo and fish's insistence that the product keep working throughout the
  migration;
- TypeScript's old-and-new executables, pinned source revision, shared
  baselines, preview distribution, and easy fallback; and
- recent translation research's use of dependency analysis, compiler feedback,
  retrieved examples, tests, and performance stress cases.

The unit of progress should be a behavior that can be proved, not a count of
translated files. For Tasks, `create a task and emit canonical JSONL` is a good
unit. `Port lib/tasks/task.rb` is not: the behavior crosses parsing, validation,
placement, revisions, persistence, and output formatting.

## What recent ports actually did

### Bun: an agent-heavy Zig-to-Rust port merged through a six-day PR

Bun is the most aggressive public example I found. Pull request
[#30412](https://github.com/oven-sh/bun/pull/30412), titled “Rewrite Bun in
Rust,” opened on May 8, 2026 and merged on May 14. GitHub records 6,755 commits,
2,188 changed files, and just over one million added lines in the PR. The
maintainer says it passed Bun's pre-existing suite on all platforms, reduced
the binary by 3–8 MB, and was neutral to faster in benchmarks. The architecture
and data structures were deliberately kept largely the same; Bun did not adopt
an async Rust runtime as part of the port.

The interesting artifact is not the translated code. It is the temporary
factory under `.claude/workflows/` on the port branch:

1. [Phase A](https://github.com/oven-sh/bun/blob/ed1a70f81708d7d137de8de057d11668c5f4e220/.claude/workflows/phase-a-port.workflow.js)
   assigned one Zig file to each translation agent. Output paths were
   deterministic. The agent had to follow one porting guide and a
   pre-classified lifetime table, return structured status, and leave a port
   trailer. A separate adversarial agent compared the draft with the Zig file;
   a third step applied only the verifier's findings.
2. Compilation was a later phase, not proof that Phase A had succeeded. The
   workflows broke dependency cycles, established crate tiers, brought crates
   up in dependency order, and tracked gates, stubs, `todo!()` calls, unsafe
   blocks, and unresolved symbols as burn-down inventories.
3. [Phase B verification](https://github.com/oven-sh/bun/blob/ed1a70f81708d7d137de8de057d11668c5f4e220/.claude/workflows/phase-b2-verify.workflow.js)
   used two source-level reviewers per module. Disputed logic-bug claims went
   to a third agent. Reviewers were told that compiling Rust could still do the
   wrong thing and had to name the exact Rust-versus-Zig divergence.
4. [The “proper port” loop](https://github.com/oven-sh/bun/blob/ed1a70f81708d7d137de8de057d11668c5f4e220/.claude/workflows/phase-e-proper-port.workflow.js)
   rejected stubs, gated code, opaque substitute types, layering workarounds,
   dropped error paths, and non-idiomatic transliteration. Two reviewers had to
   accept a file; rejected files went through another porting pass.
5. [The test swarm](https://github.com/oven-sh/bun/blob/ed1a70f81708d7d137de8de057d11668c5f4e220/.claude/workflows/phase-g-test-swarm-v3.workflow.js)
   built once per round, ran test shards against both the system Bun baseline
   and the Rust build, saved a diagnostic per failing test file, assigned fixes,
   and sent each fix to two source reviewers. This amortized an expensive build
   across many agents.
6. [The mainline-parity pass](https://github.com/oven-sh/bun/blob/ed1a70f81708d7d137de8de057d11668c5f4e220/.claude/workflows/phase-h-main-parity.workflow.js)
   enumerated every commit that had changed Zig since the port branch point,
   found the corresponding Rust code, and asked reviewers to prove that the
   semantic fix was present. Missing fixes and their tests were ported before
   cutover.

Bun's current [repository guide](https://github.com/oven-sh/bun/blob/main/CLAUDE.md)
shows what survived the temporary factory: a roughly 200-crate Rust workspace,
generated Rust/C++ bindings around JavaScriptCore, fast crate-level checks,
cross-target compilation, ASAN-aware leak tests, and a rule that a regression
test must fail with the released Bun and pass with the local build.

This approach bought extraordinary speed by accepting a large, temporarily
messy intermediate state. Compilation, idiomatic cleanup, source fidelity,
runtime parity, Windows repair, unsafe-code review, performance, and drift from
current `main` were separate campaigns. That separation made the work measurable,
but the raw draft phase was not independently safe. The later phases did most
of the risk reduction.

For Tasks, copy the manifests, schemas, reviewers, and parity scan. Do not copy
the million-line draft-first strategy. Tasks is small enough to keep every
landed slice compiling and passing conformance.

### Turborepo: keep both languages live behind a reversible seam

Vercel moved about 70,000 lines from Go to Rust over 15 months. Its published
sequence is a good model for a migration that must keep shipping:

- A Rust launcher first wrapped the Go program. When linking Go as a C archive
  failed on musl, the team changed the seam to two processes exchanging small
  JSON messages. Because the boundary was serialized rather than a broad FFI
  surface, the packaging change did not disturb the ported logic.
- For deeper code, a “Rust-Go-Rust sandwich” let the Rust entrypoint call Go,
  while Go called newly ported Rust libraries. Protobuf defined the growing
  boundary, and Go build tags could restore the old implementation.
- Porting units were non-trivial enough to justify crossing the boundary but
  small enough for one pull request. The team avoided refactoring while
  translating.
- The final all-Rust path ran behind `--experimental-rust-codepath`. Vercel
  burned down differences until integration tests matched the Go version log
  line for log line and output for output, published a canary, ran it in its
  own monorepo, and released after 72 hours without reported errors.

The team later said it should have chosen one serialization format earlier,
shipped each bridge earlier, strengthened tests before starting, specified
globbing/file-watching/hashing behavior, and improved Windows coverage. See
[the initial port](https://vercel.com/blog/how-turborepo-is-porting-from-go-to-rust),
[the Zig-assisted sandwich](https://vercel.com/blog/how-we-continued-porting-turborepo-to-rust),
and [the final cutover](https://vercel.com/blog/finishing-turborepos-migration-from-go-to-rust).

The temporary bridge made sense because Turborepo could route meaningful
internal components between languages. A Ruby/Go bridge would add little to
Tasks: its durable JSONL, CLI, and HTTP contracts already provide cheaper
out-of-process comparison seams.

### fish: a working Ship of Theseus for almost two years

fish ported 57,000 lines of C++ to about 75,000 lines of Rust while keeping a
working shell at every stage. `autocxx` generated the bridge, and the team
started with builtins because each already looked like a small program with
arguments, streams, and an exit status. They kept the C++ structure intact so
mis-translations were easy to compare and resisted redesign during the port.

The bridge imposed real costs. Ownership that `autocxx` could not express led
to wrappers and copies, generated code slowed editor tooling, and performance
dipped until the bridge disappeared. Larger entangled components eventually
moved as units because more temporary FFI would have cost more than it saved.

The last C++ code disappeared in January 2024, but fish did not ship 4.0 until
February 2025. The project calls the intervening work the “second 90%”:
end-to-end scripts, pseudo-terminal interaction, portability, packaging, and
bugs caused by mistranslated error assumptions. Its existing E2E tests mattered
more than a new set of Rust unit tests. The full retrospective is
[Fish Of Theseus](https://fishshell.com/blog/rustport/).

fish is the best warning against measuring completion by source-language
deletion. No C++ remained roughly a year before the port was ready to release.

### TypeScript: a parallel replacement with the old repo pinned inside it

Microsoft chose a separate Go implementation and ships it beside the
JavaScript compiler as `tsgo`/TypeScript Native Preview. The stable JavaScript
line remains available while the Go compiler and language server approach
parity. Users can toggle the native VS Code service off immediately if it
misbehaves.

The [typescript-go repository](https://github.com/microsoft/typescript-go)
contains the main TypeScript repository as a submodule pinned to the revision
being ported. That supplies tests, generated inputs, and comparison baselines.
Its README records parity per capability rather than a single percentage:
parsing, module resolution, checking, emit, watch, build mode, incremental
build, language service, and API each have a status and explicit known
differences. `CHANGES.md` separates intentional semantic changes from defects.

The test runner tracks used baseline files and refuses stale ones. The public
contract is concrete: for completed areas the Go port should find the same
files and types and report the same errors, locations, and messages. Preview
packages and nightly editor builds turn real projects into a compatibility
corpus while fallback stays cheap. Microsoft's
[initial announcement](https://devblogs.microsoft.com/typescript/typescript-native-port/)
and [native-preview update](https://devblogs.microsoft.com/typescript/announcing-typescript-native-previews/)
describe the release strategy.

This is the closest shape to Tasks. It pays for two implementations during the
port, but avoids mixed-runtime production binaries and lets the old executable
remain an unambiguous oracle.

## What automated translation can and cannot do

There are three distinct jobs often hidden under “use an agent to port it”:

1. Preserve all source logic without omissions.
2. Make the target compile and fit its dependency graph.
3. Produce behaviorally correct, idiomatic, and efficient target code.

One pass rarely does all three.

[C2Rust](https://github.com/immunant/c2rust) takes the conservative compiler
route: reproduce C closely as unsafe Rust, keep the old tests passing, then
refactor toward safe and idiomatic Rust. This preserves more structure than an
LLM but deliberately leaves human or agent cleanup.

Recent research attacks the missing middle:

- [LLMigrate](https://arxiv.org/abs/2503.23791) uses static analysis and the
  call graph to select context, translates functions separately, reintegrates
  them, and uses compiler-driven repair. Its motivating failure mode is model
  “laziness”: plausible translations that silently omit code.
- The 2026 [LegacyTranslate](https://arxiv.org/abs/2603.14054) preprint splits
  PL/SQL-to-Java work into an initial translator, an API-grounding agent with a
  curated knowledge base, and a compiler/test-driven refinement agent. Its
  reported first-pass numbers are sobering: 45.6% compiled and 30.9% passed
  tests before the later stages improved them.
- Google's production [AI migration workflow](https://research.google/blog/accelerating-code-migrations-with-ai/)
  uses static analysis and code search to identify a tight superset of touched
  files, generates only changes that pass unit tests, then shards the result to
  the owners of affected code. Human effort remains concentrated in targeting
  and review.
- [TRACY](https://arxiv.org/abs/2508.11468), a 2025 translation benchmark,
  found that functional tests miss many performance failures. Its generated
  stress cases exposed algorithm, library/idiom, and resource-management
  mistakes; simple correctness tests missed 82.6% of the inefficient
  translations found by the stress suite.

The pattern is consistent. Agents are good at generating candidate code and
repairing concrete failures. Static dependency information, a target-language
API catalog, compiler output, old-versus-new execution, stress tests, and
review remain the control system.

## Choosing the migration shape

| Shape | Best when | Main benefit | Main cost |
|---|---|---|---|
| Mechanical transpile, then rehabilitate | Source and target have a trustworthy translator | High initial coverage; omissions are less likely | Output begins unsafe or unidiomatic; platform specialization can leak in |
| Mixed-language Ship of Theseus | A stable, cheap FFI boundary divides meaningful components | Every intermediate build can ship | Temporary bridge, duplicated types, packaging and performance complexity |
| Parallel replacement | Public contracts are stable and process/file boundaries are cheap | Clean target architecture and trivial fallback | Duplicate implementation until late; drift must be policed continuously |
| Whole-tree agent draft, then burn-down | The source is huge, architecture is retained, tests are deep, and time matters more than intermediate cleanliness | Massive parallelism | Enormous temporary defect inventory and expensive verification campaigns |
| Contract-driven vertical slices | Product behavior crosses source-file boundaries and can be observed externally | Each landed unit has user-visible proof | Requires a serious characterization harness before translation feels fast |

Tasks should use parallel replacement at the executable level and
contract-driven vertical slices inside the Go implementation.

## The porting control plane

Add a small, language-neutral control plane to the Tasks repository before
porting application code:

```text
porting/
  manifest.jsonl
  intentional-differences.md
  specs/
    observations.schema.json
    errors.md
  fixtures/
    valid/
    legacy/
    malformed/
    adversarial/
  runners/
    ruby
    go
  compare/
    cli
    http
    files
    journal
    performance
  evidence/
```

`manifest.jsonl` should have one record per proof-sized capability:

```json
{"id":"create-basic","depends_on":["store-canonical-write"],"risk":"high","source_sha":"...","ruby_tests":["..."],"fixtures":["..."],"status":"characterizing","owner":"...","intentional_differences":[]}
```

Useful fields are stable ID, behavior name, dependency IDs, source revision,
risk, fixtures, existing Ruby tests, observable outputs, target packages,
platforms, performance budget, current state, evidence locator, and unresolved
differences. Generate progress from this file. Do not maintain a second
hand-written percentage.

The observation format should record more than stdout:

```json
{
  "invocation": {"argv": [], "stdin": "", "env": {}},
  "process": {"exit": 0, "stdout": "", "stderr": ""},
  "http": [],
  "files": [{"path": "tasks.jsonl", "sha256": "...", "bytes_base64": "..."}],
  "journal": [],
  "events": [],
  "metrics": {"wall_ms": 0, "peak_rss_bytes": 0}
}
```

Pin or inject the clock, zoneinfo version, locale, terminal width, hostname,
device ID, random ID source, filesystem fixture, and agent runner. Normalize a
field only after documenting why users cannot observe it. Over-normalization
turns a conformance test into a difference-hiding machine.

## The per-slice loop

Each slice should fit in one reviewable change and run through the same loop:

### 1. Select from the dependency graph

Choose a capability whose dependencies are already green. Prefer an executable
vertical path over a low-level file count. Mark the source revision in the
manifest so later source changes can be detected.

### 2. Characterize Ruby before writing Go

Run the Ruby implementation over its existing tests plus sanitized fixtures.
Capture success, rejection, malformed input, boundary values, and state after
failure. Add missing cases now. Deliberately perturb a fixture or expected
result and prove the comparator catches it.

An agent writing the Go slice must not also decide what Ruby behavior counts as
the oracle. Oracle capture and target implementation are separate roles.

### 3. Translate for fidelity

Give the implementation agent the behavior spec, exact source slice, target
package interfaces, nearby accepted Go examples, and a narrow edit scope.
Require structured output: touched files, unresolved cases, new dependencies,
unsafe/platform-specific code, and claimed evidence.

Do not ask for cleanup, new features, or API improvements in this pass. Record
an intentional difference for later rather than quietly improving the port.

### 4. Compile and repair with bounded diagnostics

Feed compiler and static-analysis errors back to a repair agent in small
batches. The repair agent may change interfaces only inside the declared
slice. If a dependency or layering problem appears, update the manifest and
move the boundary; do not solve it with duplicate types, opaque maps, global
callbacks, or a permanent compatibility shim.

Stop after a fixed number of failed rounds. Repeated compiler repair often
means the selected slice or package boundary is wrong.

### 5. Review source fidelity before running broad tests

Use two different reviews:

- A source reviewer compares Go with the Ruby implementation and names omitted
  branches, altered ordering, changed defaults, error differences, and resource
  lifetime changes.
- A target-language reviewer looks for non-idiomatic Go, accidental aliasing,
  unnecessary allocation, bad goroutine ownership, platform leakage, and
  interfaces that will trap the next slice.

Reviewers return file, function, severity, evidence, and an exact correction.
They do not edit. A disputed high-risk semantic claim gets a third review or a
new executable test; a majority vote by models is not proof by itself.

### 6. Run differential conformance

Run Ruby and Go in isolated copies of the same fixture. Compare typed results
first and human formatting second. For mutations, compare final file bytes,
journal bytes, revisions, exit status, and the next read. For HTTP, include
headers, ETags, body limits, error envelopes, and event ordering.

On a mismatch, classify it as:

- Go defect;
- legacy Ruby defect that remains the compatibility rule for this port;
- nondeterminism that should be injected or normalized;
- intentional difference requiring Marcus's explicit decision; or
- missing oracle coverage.

Never update a golden result merely because Go produced it.

### 7. Attack the slice

Once examples match, add properties, fuzzing, fault injection, and stress:

- parser/dumper round trips and canonical byte stability;
- tree invariants under random move/complete/archive sequences;
- recurrence around DST gaps, folds, leap days, and month ends;
- crash points around lock, write, flush, validation, rename, and journal append;
- real competing processes and stale revisions;
- large task trees, long notes, many tags, and repeated edits; and
- time, allocation, file-descriptor, goroutine, and peak-RSS budgets.

The old implementation need not win every performance comparison, but a large
regression must be understood before landing.

### 8. Prove supported targets

Run normal Go tests and the race detector on macOS. Compile and exercise
filesystem/process behavior on Windows rather than relying on cross-compilation
alone. Mobile slices get simulator plus physical-device lifecycle tests once
native bindings exist. Platform-gated code needs a target matrix from its first
commit.

### 9. Land an evidence bundle

One slice lands only when the manifest entry points to:

- source revision and Ruby oracle capture;
- source-fidelity and Go reviews;
- compiler/static-analysis results;
- differential, property, fuzz, failure, stress, and platform results that
  apply to its risk level;
- every intentional difference; and
- a rollback path.

Keep commits small enough that a reviewer can compare source and target without
reconstructing the entire migration.

### 10. Scan for drift immediately

After every change to relevant Ruby code, identify affected manifest entries
and require one of three outcomes: ported to Go, explicitly not applicable, or
blocking the Go cutover. Carry the Ruby commit and matching Go commit in the
evidence. This is Bun's mainline-parity phase turned into a standing check.

Do not rely on developers remembering to make the second edit. CI should fail
when a changed Ruby behavior has no recorded disposition.

## Verification ladder

Each rung catches defects that the rung below cannot:

| Rung | Proof | Typical failure caught |
|---:|---|---|
| 0 | Manifest completeness and dependency scan | Forgotten files, generated code, callers, tests, platform branches |
| 1 | Format, compile, lint, static analysis | Invalid target code, bad imports, obvious misuse |
| 2 | Source-fidelity review | Omitted branch, reordered mutation, changed default or error path |
| 3 | Curated old-versus-new differential cases | Observable semantic and serialization differences |
| 4 | Property, metamorphic, and fuzz tests | Unknown edge cases and invariant violations |
| 5 | Fault injection and real-process contention | Lost updates, corrupt files, partial journal state, deadlocks |
| 6 | Stress, leak, and performance tests | Correct but pathologically slow or memory-hungry translations |
| 7 | Supported-target builds and runtime tests | OS filesystem, process, path, locale, and ABI differences |
| 8 | Shadow reads, dogfood, preview, and canary | Real inputs and workflows absent from the fixture corpus |
| 9 | Mainline parity closure and rollback rehearsal | Source drift and a one-way cutover |

Compilation is rung 1. A translated unit is not “done” because it compiles or
because its newly translated unit tests pass.

## Agent roles and boundaries

A useful porting fleet has narrow jobs:

| Role | May edit? | Evidence it consumes | Output |
|---|:---:|---|---|
| Inventory agent | No | Repository graph and tests | Manifest entries and dependency edges |
| Oracle agent | Tests/specs only | Ruby behavior and fixtures | Characterization cases and observations |
| Translation agent | Declared Go slice | Spec, Ruby source, accepted Go examples | Candidate implementation and unresolved list |
| Compiler repair agent | Same slice | Structured diagnostics | Minimal compile/static fixes |
| Source reviewer | No | Ruby, Go, diff, port spec | Named semantic divergences |
| Go reviewer | No | Go diff and package boundaries | Idiom, safety, ownership, and layering findings |
| Conformance runner | No | Both executables and fixtures | Machine-readable comparison report |
| Bug fixer | Declared slice | Failing evidence and reviewer findings | Focused corrections |
| Release verifier | No | Built artifacts and clean fixtures | Install, upgrade, rollback, and platform proof |

Keep reviewers read-only and use isolated worktrees for writers. Parallel agents
must not share a build directory, fixture directory, task store, test port, or
Git index. If an expensive build is amortized once per round as Bun did, fixes
made within that round remain hypotheses until the next build.

Structured schemas matter more than clever prompts. They let the orchestrator
distinguish a clean result from a skipped file, low confidence, unresolved
TODO, disputed review, stale baseline, or infrastructure failure.

## The minimum loop

The ten steps, ten rungs, and nine roles above are the full apparatus, and
the full apparatus is owed only to slices that can corrupt data or change
semantics silently. Running all of it on every slice is token-burning
ceremony. Scale by risk:

| Slice risk | Examples | Required |
|---|---|---|
| Low — pure reads and formatting | list filters, human output, help text | Oracle capture, translate, compile, differential conformance (rungs 0–3); one combined review |
| Medium — semantics without persistence | temporal parsing, tree queries, config resolution | Low tier plus property tests (rung 4) and the split source/Go reviews |
| High — anything that writes | store, journal, locking, archive, merge | The full loop, rungs 0–7 |

Rungs 8–9 are port-wide, not per-slice. Three things are never skipped,
because they are the port rather than its ceremony: the manifest entry, the
Ruby oracle capture, and differential conformance. Everything else is a
per-slice decision, and skipping a rung is recorded in the manifest entry,
not left as a silent default.

The role table is likewise a menu, not a mandatory cast. On a low-risk slice
one agent can translate, repair, and self-review, with the conformance runner
as the independent check. Separate writer and reviewer identities are
reserved for medium and high risk, where a reviewer rubber-stamping its own
work is a real failure mode.

## A proposed Tasks sequence

The dependency order from the Go-port plan becomes these proof-sized campaigns:

1. Harness, observation schema, fixture sanitizer, deterministic clock/ID
   adapters, and Ruby oracle capture.
2. Record parsing, canonical JSONL emission, validation, configuration, and
   `check`.
3. Read queries: `list`, `show`, Inbox, Next, agenda, projects, filters, and
   JSON envelopes.
4. Basic mutations and placement, followed by lifecycle, proposals, and
   delegation.
5. Temporal parsing, availability, recurrence, time zones, and calendar edges.
6. Locking, atomic replacement, revisions, rollback, journal, undo/redo, and
   coalescing.
7. Archive and Git merge behavior.
8. Full CLI grammar and human formatting.
9. OpenAPI server, ETags, error handling, events, and cross-process concurrency.
10. Agent execution and platform process trees.
11. Native binding facade and mobile lifecycle proof.
12. Bubble Tea TUI behavior.

Campaigns 2–7 should expose a tiny temporary Go test driver so the harness can
invoke application commands before the final CLI exists. That driver is a test
adapter, not a new public API.

Ruby and Go may read the same immutable fixture. They may write separate copies
and compare the results. They must never dual-write the live store. Shadowing
production reads is safe after the read path is mature; shadowing mutations
means replaying a captured operation against a disposable copy.

## Cutover rules

The binding promotion checklist lives in the port plan's Phase 8
([tasks-go-port-plan.md](tasks-go-port-plan.md)); this playbook does not keep
a second copy, so the two documents cannot drift. The principles behind it:

- ship the Go binary first as an opt-in preview under a different command,
  with one-step reversion, while Ruby stays authoritative;
- promote on recorded evidence — a green manifest, an empty parity queue,
  survival of real use with mismatch reporting on — never on looking
  feature-complete; and
- treat deleting Ruby as housekeeping after the compatibility window closes,
  not as the definition of a successful port.

## Sources

Primary project sources:

- [Bun rewrite PR #30412](https://github.com/oven-sh/bun/pull/30412),
  [Phase A workflow](https://github.com/oven-sh/bun/blob/ed1a70f81708d7d137de8de057d11668c5f4e220/.claude/workflows/phase-a-port.workflow.js),
  [Phase B verifier](https://github.com/oven-sh/bun/blob/ed1a70f81708d7d137de8de057d11668c5f4e220/.claude/workflows/phase-b2-verify.workflow.js),
  [test swarm](https://github.com/oven-sh/bun/blob/ed1a70f81708d7d137de8de057d11668c5f4e220/.claude/workflows/phase-g-test-swarm-v3.workflow.js),
  and [current repository guide](https://github.com/oven-sh/bun/blob/main/CLAUDE.md)
- Vercel's [Turborepo port series](https://vercel.com/blog/how-turborepo-is-porting-from-go-to-rust)
  and [completion retrospective](https://vercel.com/blog/finishing-turborepos-migration-from-go-to-rust)
- fish's [Rust-port retrospective](https://fishshell.com/blog/rustport/)
- Microsoft's [TypeScript native-port announcement](https://devblogs.microsoft.com/typescript/typescript-native-port/)
  and [typescript-go repository](https://github.com/microsoft/typescript-go)
- Google's [AI migration workflow](https://research.google/blog/accelerating-code-migrations-with-ai/)
- [C2Rust](https://github.com/immunant/c2rust)

Research papers used for the agent and performance sections:

- [LLMigrate](https://arxiv.org/abs/2503.23791), 2025 preprint
- [LegacyTranslate](https://arxiv.org/abs/2603.14054), 2026 preprint
- [TRACY](https://arxiv.org/abs/2508.11468), 2025 preprint
