# `delegation-record-shape` oracle capture

This directory holds the Ruby oracle for manifest slice `delegation-record-shape`,
captured from source revision `46f4955ff1c5d567b50e6ae104d0dd4796fae911`
(`lib/tasks/delegation.rb`, blob `88d6e43a54ae1e23ace1edd5b0029da379941a5e`).
The slice is the schema half only: `FIELD`, `KEY_ORDER`, the canonical UTC
spelling of `at`, and the unknown-key set. Claim and release semantics are
campaign 4 and are not captured here.

Two oracles, because one surface does not reach the contract:

- **`ruby/`** — seven CLI observations from the language-neutral runner
  protocol, driven by
  [`porting/runners/cases/delegation-record-shape.jsonl`](../../runners/cases/delegation-record-shape.jsonl).
  This is where `Delegation#errors` is observable *line-stamped* by `check`, and
  where the forward-compatibility posture is observable at all.
- **`ruby-direct/`** — 102 observations from
  [`porting/runners/ruby/delegation-probe`](../../runners/ruby/delegation-probe),
  driven by
  [`delegation-record-shape-direct.jsonl`](../../runners/cases/delegation-record-shape-direct.jsonl).
  A fixture store cannot legally carry an invalid-UTF-8 assignee on a JSONL
  line, and no CLI command prints `ordered`, `unknown_keys`, or `stamp` — so the
  module's own surface is probed directly, exactly as `format-parse` did.

## What the CLI half proves

| Case | Outcome |
|---|---|
| `valid/full-field-matrix` | the human, ready-agent and claimed-agent shapes all pass `check` |
| `compat/forward-compat-unknown-keys` | `warn line 3: unknown delegation key "budget_tokens"` — a **warning**, exit 0. An unknown nested key must never fail the file |
| `malformed/wrong-types` | `error line 18: delegation.mode nil must be one of refine/research/implement`, stamped with the physical line |
| `valid/delegation-closed-provenance` | a closed task retains its claimed delegation and still validates |
| `list --all --delegated --json` (×3) | the delegation object reaches a consumer whole, unknown nested key included |

## What the direct half pins, and what will bite the translator

1. **Error wording is Ruby `#inspect` output.** Every message interpolates the
   offending value with `.inspect`, so the port must reproduce Ruby's spelling,
   not Go's `%q` or `%v`. The capture pins the cases that differ:

   | Input | Ruby renders |
   |---|---|
   | missing / `null` | `nil` |
   | `42` | `42` |
   | `["sam@example.com"]` | `["sam@example.com"]` |
   | ESC (U+001B) | `\e` |
   | U+0001, U+0085, U+007F | `\u0001`, `\u0085`, `\u007F` (uppercase hex) |
   | U+2028 | `\u2028` |
   | U+00A0, U+3000 | **left literal**, not escaped |
   | invalid byte `0xFF` | `\xFF` |

   The NBSP/U+3000-vs-U+2028 split is the trap: all three are refused by
   `WHITESPACE_RE`, and only one of the three is escaped in the message.

2. **Message order is source order**: kind errors, then `at`, then `work_ref`
   (`agent-mode-and-status-both-wrong` pins all three at once). A bad `kind`
   stops the cascade — `kind-missing` yields one message, not four.

3. **`key?`, not truthiness.** `human-mode-null`, `agent-ready-with-null-assignee`
   and `work-ref-null` all carry an explicit JSON `null` and are all refused: the
   guards ask whether the key is present.

4. **Both limits are characters, not bytes.** 200 (`assignee`) and 500
   (`work_ref`) are exact — `*-at-limit` passes and `*-over-limit` fails by one —
   and the multibyte cases prove a byte count would give a different answer. The
   over-limit `work_ref` message reports the *character* length it got.

5. **`ordered` and `unknown_keys` disagree on purpose.** `ordered` emits
   `KEY_ORDER` first, then unknown keys **in their own insertion order**, and
   drops anything `nil`-or-`empty?` — so `0` and `false` survive while `[]`, `{}`
   and `""` do not. `unknown_keys` is **sorted by byte order** (`Mixed` before
   `alpha`) and still names a key whose empty value `ordered` dropped.

6. **`work_ref` is validated only when the key is present**, and its
   invalid-UTF-8 case reports the *non-empty-string* message rather than a
   control-character one — `valid_encoding?` gates first. U+2028 in a `work_ref`
   is a **line break** (`LINE_BREAK_RE`), while the same character in an
   `assignee` is **whitespace**. Two rules, one character, different messages.

7. **`at` is shape-then-reality.** `AT_RE` is anchored `\A…\z`, so
   `at-trailing-newline` is refused; `2026-02-31T00:00:00Z` matches the shape and
   is still refused because `Time.iso8601` rolls it to Mar 3 and the round trip
   back through `stamp` no longer produces the original bytes. `at-leap-second`
   fails the same way.

8. **`stamp` converts, then truncates.** A non-UTC offset renders as the UTC
   instant (including across a date boundary), and `.999` seconds truncates
   toward the second rather than rounding.

## Verification

At capture time the focused Ruby selection passed: 3 runs / 69 assertions and
2 runs / 17 assertions, 0 failures, 0 errors, 0 skips.

```sh
ruby test/test_delegation.rb -n '/test_delegation_module_accepts_valid_objects_and_names_every_violation|test_control_characters_and_escapes_are_refused_in_every_identity_field|test_an_at_prefixed_word_is_not_an_email_address/'
ruby test/test_check.rb -n '/test_valid_delegations_pass_and_are_a_known_key|test_unknown_delegation_keys_do_not_fail_the_file/'
porting/evidence/capture --out porting/evidence/delegation-record-shape/ruby \
  --cases porting/runners/cases/delegation-record-shape.jsonl \
  --work /tmp/tasks-delegation-record-shape
porting/compare/validate porting/evidence/delegation-record-shape/ruby
TZ=UTC porting/runners/ruby/delegation-probe \
  porting/runners/cases/delegation-record-shape-direct.jsonl \
  > porting/evidence/delegation-record-shape/ruby-direct/observations.jsonl
```

`validate` reported 7/7 observations valid and internally coherent. The direct
probe is byte-reproducible: a second run diffs clean against the committed
observations.

## Next step

**Medium-risk translation, in a different session** (role boundary: the capturer
does not translate). Implement the delegation schema in `internal/record`
against these observations — nothing else — then compile and run differential
conformance against both halves. A Go `delegation-probe` emitting the same JSONL
shape is what makes `ruby-direct/` comparable; the CLI half waits on the `check`
and read slices that own those surfaces, so conformance for it is
observation-level, not `tasks-go check` output.

Do not let Go output define an expected result. If Ruby's `#inspect` spelling
turns out to be expensive to reproduce exactly, that is an intentional-difference
record for Marcus, not a quietly relaxed comparison.
