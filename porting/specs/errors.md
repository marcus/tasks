# errors.md — how the diagnostic surface is observed and compared

Companion to [`observations.schema.json`](observations.schema.json) and
[`determinism.md`](determinism.md). It answers one question for every failure a
`tasks` invocation can produce: **which part of it is contract, and which part is
formatting?** A port that gets the contract right and the formatting wrong is a
cosmetic diff to argue about. A port that gets the formatting right and the
contract wrong is a bug the harness must fail on.

This file is normative for the comparator. If a comparison rule is not written
here, the default is *compare it byte for byte* — loosening a comparison is a
decision, and decisions are written down.

## The three layers

An error reaches the user through three independent channels. They are ranked by
how strongly the port is bound to them.

| Layer | Where it lands | Binding |
|---|---|---|
| **Exit status** | `process.exit_status` | **Contract.** Compared exactly, always. |
| **Structured error** | `process.stdout` under `--json`; the HTTP response body | **Contract.** Compared as canonical JSON: same keys, same values, same types. |
| **Diagnostic text** | `process.stderr` (and human-mode stdout) | **Contract by default**, compared byte for byte. See "Formatting" below for the narrow, enumerated exceptions. |

### Exit status is the smallest and strongest contract

`tasks` publishes three statuses (`docs/cli-spec.md`, "Exit codes"):

- `0` — success.
- `1` — error: bad arguments, validation failure, corrupt file, refused
  mutation, unparseable schedule.
- `2` — ref resolution failure: no match, or ambiguous match.

`2` exists specifically so an agent can branch — refine the ref rather than
abort. That makes the **`1` vs `2` distinction a product feature**, and the most
important single assertion in the whole error surface. A Go port that collapses
them into "nonzero" passes a naive comparator and breaks every agent using the
CLI. Under `--json` an exit-`2` refusal also hands the agent the candidate set
as data so it *can* refine — see "The `--json` error envelope" below.

Rules:

- Compare `exit_status` exactly on every case, success and failure alike.
- Never accept "both nonzero" as a match.
- `process.signal` non-null is never a pass. A crash is a crash even if the case
  expected a failure.
- `process.timed_out` true is never a pass.

### Structured errors are compared as data, not as text

Where a command emits JSON (`--json` on reads and mutations; the whole HTTP
surface), the comparison is over the parsed value, not the serialized bytes:

- Same set of keys — a key present in one side and absent in the other is a
  failure, including when its value would be null. An omitted key and a null key
  are different answers.
- Same values, with the same JSON types. `"1"` and `1` are a failure.
- Array order is significant. Diagnostics are reported in file order, and file
  order is part of what makes `check` output readable.
- Object key *order* inside a JSON payload printed to stdout is **not** compared
  (unlike the JSONL store, where key order is a hard byte contract — see below).
  Stdout JSON is consumed by parsers.

Note the asymmetry deliberately: **JSONL store bytes are compared byte for byte,
including key order and omitted defaults; stdout JSON is compared as parsed
data.** The store is a durable format two implementations must round-trip; a
printed payload is a message.

### The `--json` error envelope

A command that emits a JSON result under `--json` owes one on its refusal paths
too. Otherwise `--json` degrades to "nothing on stdout", which a caller cannot
tell apart from "no result" — and a harness cannot tell apart from anything at
all. The envelope is an object with at least:

| Key | Meaning |
|---|---|
| `error` | The condition, as a stable token (`store_invalid`, `unsupported_schema_version`, `unavailable`, `conflict`, `stale`, `not_found`, …). Branch on this, not on the message. |
| `action` | The command that refused, sub-verb included (`capture`, `project create`). |
| `message` | The same sentence that went to stderr, verbatim, newlines and all. |
| `rolled_back` | **On a failed mutation, required.** `true` when the implementation wrote and then restored the previous bytes; `false` when it never wrote. |

Individual conditions add keys (a claim conflict adds `holder` and `at`).

`rolled_back` is the one that carries information nothing else does. A mutation
that wrote and reverted and a mutation refused before writing exit the same way
and leave byte-identical files behind; the boolean is the only channel that
separates them, which is why the harness records it as `files.rolled_back` and
compares it directly. Exit status stays the human one — the envelope is
additional, never a substitute.

Ref-resolution failures (exit `2`) emit the same envelope, with one added key
(td-d842ed). They used to print nothing on stdout, which made `--json` degrade
to empty output for the exact caller exit `2` was invented for: an agent that
should refine an ambiguous ref rather than abort had to parse the stderr prose
back out. The envelope is:

| Key | Meaning |
|---|---|
| `error` | `not_found` (no match, or an `L<line>` that names nothing), `ambiguous` (more than one in-scope match), or `out_of_scope` (the ref names a live task or tasks the command's scope excludes). |
| `action` | As above — the command that refused. |
| `message` | The full stderr text verbatim, newlines and all. For an ambiguous ref that includes the candidate lines. |
| `candidates` | The candidate set as **data**, never the stderr lines reformatted. Always present, `[]` when there is nothing to offer. Task refs: `{id, line, state, title}`. Project refs: `{id, line, kind, title}`. Order matches the stderr list, which is file order. |

Three properties of that envelope are contract, not decoration:

- The **exit status stays `2`**. The envelope is additive; it does not soften
  the 1-vs-2 distinction in any way.
- **stderr is unchanged, byte for byte, with and without `--json`.** stdout was
  empty before; only stdout gained anything.
- `rolled_back` is **absent** here. Nothing was attempted, let alone written —
  ref resolution refuses before the mutation begins.

### Diagnostic text is contract until proved otherwise

stderr is compared byte for byte by default. That is a strong requirement and it
is deliberate: the Ruby CLI's diagnostics are the product's error UX, they are
what an agent reads back to a user, and several of them contain the actionable
next step (`run \`check\``, "first valid time is 09:00", the ambiguous-ref
candidate list). Rewording them is a behavior change, which the porting
non-negotiables forbid.

Byte comparison of stderr covers the parts that are easy to get wrong:

- The **candidate list** on an ambiguous ref, and its order.
- The **repair hint** distinguishing a post-write validation rollback from a
  stale-line conflict — two failures that look identical in the store and are
  not the same event.
- The **rollback hint, and which STAGE it names.** A rolled-back mutation gets
  exactly one of two sentences appended to the command's own message, and which
  one is chosen is contract (td-fea097):

  | Stage | Sentence |
  |---|---|
  | Post-write validation failed (the bytes landed, `check` refused them, they were reverted) | ``file failed validation after the edit — run `tasks check``` |
  | The write itself failed (the atomic replace raised; validation never ran) | `could not write the task file — the previous contents were restored (nothing was changed)` |

  A port that keys this off "was it rolled back" alone reproduces the bug this
  replaced: it sends the user to `tasks check` for a file that is valid, and
  blames a stage that never ran. The write sentence deliberately carries **no
  path and no exception text** — the underlying message is
  `Permission denied @ rb_sysopen - <abs path>`, and interpolating it would make
  stderr differ per machine, which is exactly the determinism failure td-231878
  removed. Both sentences are compared byte for byte like everything else here.
- **Line and record numbers** in `check` diagnostics.
- The **line ordering** of multiple diagnostics.

## Formatting — the enumerated exceptions

These, and only these, are treated as presentation and excluded from the byte
comparison of a stream. Each is excluded because a *user* cannot make it differ
between two implementations given identical pins, or because the harness itself
caused it.

1. **ANSI colour sequences.** Colour is applied only when stdout is a tty
   (`$stdout.tty?`), and the harness redirects every stream to a file, so both
   implementations emit uncoloured output. The comparator therefore does not
   need to strip anything — but if a case is ever run under a pty, colour is
   compared, not stripped: which words are highlighted is a real difference.

   Say the consequence plainly rather than filing it as an exclusion: this is
   not "colour is presentation", it is "colour is unreachable". No case in the
   corpus produces an ANSI escape, so a green run is not evidence about colour
   rendering in either direction, and a port that emitted none, or the wrong
   codes, passes every case. The tty-ness is pinned by the harness process and
   recorded in `invocation.tty` so the gap is legible in the evidence;
   `porting/specs/determinism.md` § Colour is the full record, including the
   four environment names the implementation actually reads.
2. **Absolute paths inside the fixture copy.** Each run gets its own copy at its
   own path, so a diagnostic naming the store's absolute path differs by
   construction. The comparator rewrites the copy root to a fixed token in both
   streams before comparing, and nothing else. The path *relative to the copy
   root* stays compared: naming the wrong file is a real bug.
3. **Nothing else.** In particular, trailing whitespace, blank-line placement,
   capitalisation, punctuation, and pluralisation are all compared. They are
   cheap to match and expensive to argue about case by case.

## Failure shapes the corpus must distinguish

The port plan calls these out as distinctions that are part of the product. Each
is a pair that looks alike in at least one channel, so each needs an observation
that separates them. The observation schema is shaped to make each separable:

| Pair | What separates them in an observation |
|---|---|
| Invalid changeset rejected *before* lookup vs. rejected after | `exit_status` plus stderr text; `files.deltas` empty in both. |
| Stale field revision vs. missing task | `exit_status` (1 vs 2) and the structured error; `revisions.resources` present in one. |
| Failed post-write validation (wrote, rolled back) vs. never wrote | `files.rolled_back` true vs false, with `files.deltas` empty in both. This is exactly why `rolled_back` exists as its own field: the filesystem cannot tell you. It is read from the `--json` error envelope above, never from the diagnostic wording. |
| Failed WRITE (rolled back) vs. failed post-write VALIDATION (rolled back) | Both are `rolled_back` true with `files.deltas` empty, and `rolled_back` alone cannot separate them — the **diagnostic sentence** does, and the structured error code agrees with it (`unavailable` for the write, `store_invalid` for validation). They are different events with different next steps, so naming the wrong one is a defect even though every other channel matches. |
| Ref failure vs. refused mutation | `exit_status` 2 vs 1, and the envelope: an exit-2 envelope carries `candidates` and no `rolled_back`; an exit-1 mutation envelope carries `rolled_back` and no `candidates`. |
| Same-owner worker retry vs. conflicting claim | `exit_status` and the structured error; store bytes differ (one updates, one does not). |
| Store invalid vs. schema-version migration required | Structured error code and stderr text; both exit 1. |
| Malformed line skipped (warning) vs. store rejected (error) | `exit_status` 0 vs 1, and which of the two lists the diagnostic lands in. |

## Non-UTF-8 diagnostics

The malformed and adversarial fixtures contain invalid UTF-8 on purpose, and
diagnostics quote the offending bytes. That is why `process.stdout` and
`process.stderr` are captured as base64 with a `valid_utf8` flag rather than as
JSON strings: a JSON string cannot round-trip an invalid byte, and a lossy decode
would silently equalise two implementations that mangle the bytes differently.

Comparison rule: a digest is authoritative, `bytes_base64` is for reading, and
`text` is a convenience decode that exists only when `valid_utf8` is true and is
never compared.

*Which* digest depends on whether the capture was truncated. An untruncated
stream is compared on its decoded bytes after the copy-root rewrite (exclusion 2
above). A stream past the 256 KiB embed limit has no bytes left to rewrite, so it
is compared on `sha256_normalized` — the same rewrite applied before hashing, at
capture time. `sha256` (over the raw bytes) stays recorded and stays comparable
only when both sides ran at one absolute path: it is the field that made
truncation and cross-path comparison mutually exclusive, which is why the
normalized one exists.

## What is not compared at all

- `metrics.*` — advisory. Performance is a separate gate with separate
  thresholds; it must never be able to fail a conformance case, and it must never
  be able to pass one either.
- `observation_id`, `fixture.copy_root`, `implementation.*` — harness and
  provenance metadata.
- `environment.*` — recorded so a difference elsewhere can be attributed to a
  tzdb release, never itself an assertion. This applies to the fields the
  IMPLEMENTATION genuinely answers (`tzdb_version`, `locale`): a conformance run
  whose two sides disagree only there, and agree everywhere else, is fine; one
  that disagrees there *and* also elsewhere must be re-run with the environments
  matched before the difference is classified. `platform`, `filesystem`, and
  `umask` are different in kind — they are HARNESS-SUPPLIED HOST FACTS
  (porting/runners/README.md § "environment.platform is a host fact, not a
  probe answer"), computed once by the harness and stamped on both sides, so a
  disagreement in one of them is never a legitimate environment difference to
  re-run around. It means the harness or the run itself is broken, and is
  classified `harness_error` — see `porting/compare/README.md` § Classification.

## Classifying a mismatch

The porting non-negotiables require every mismatch to be classified, never
blessed. For an error-surface mismatch the classification is usually decided by
which layer differs:

- **exit status differs** → Go defect, until proved otherwise. There is no
  formatting explanation for an exit status.
- **structured error differs** → Go defect, or a missing oracle case if the Ruby
  side turns out to be under-specified for that input.
- **stderr text differs but exit status and structured error match** → still a Go
  defect by default. It becomes an intentional difference only if Marcus decides
  the Ruby wording was wrong, and that decision is recorded in
  `porting/intentional-differences.md` before the expectation changes.
- **only `environment.tzdb_version` or `environment.locale` differs** →
  nondeterminism to pin, not a defect. Add the pin, do not normalize the
  output.
- **`environment.platform`, `.filesystem`, or `.umask` differs, at all** →
  `harness_error`. These are harness-supplied host facts, identical by
  construction on a working run; a disagreement means the harness or the run
  environment is broken, not something about the port.
