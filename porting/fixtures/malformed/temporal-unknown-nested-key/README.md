# malformed/temporal-unknown-nested-key

Two tasks whose temporal objects carry a key this binary does not know:
`scheduled_time.precision` on one, `deadline_time.calendar_uid` on the other.
This is the **drop** half of `Format::NESTED_FORWARD_COMPAT` — the half
`compat/forward-compat-unknown-keys` cannot show, because the only nested object
in that list is `delegation`, which *keeps* its unknown keys.

## Why `malformed/` and not `compat/`

An unknown key inside `scheduled_time` / `deadline_time` is a hard `check`
error, not a warning: `Check.check_temporal_time` subtracts the known set
(`local`, `timezone`, `fold`) and errors on any remainder. `compat/` is for
bytes a newer binary produced that this one *tolerates*; these bytes are
refused, so they belong with the other stores that exit 1. The asymmetry with
`delegation` — same skew, opposite verdict — is the whole point of the fixture,
and putting the two halves in different classes is what makes it visible.

## What it exercises

- `Check.check_temporal_time`'s unknown-key branch, on both temporal fields, so
  the two distinct messages (`scheduled_time has unknown keys: …` /
  `deadline_time has unknown keys: …`) are both pinned.
- The unknown key sits *after* the declared keys in the source bytes, so a
  correct writer's output differs from the input by a pure deletion.
- That reads tolerate what writes refuse: `tasks list` renders both tasks and
  exits 0 while any mutation refuses with nothing written.

## What a correct implementation must do

Error, once per offending object, naming the field and the unknown keys joined
by `", "`. On the emission side, `Format.dump_record` must drop keys not in
`%w[local timezone fold]` from a temporal object, because `scheduled_time` and
`deadline_time` are deliberately absent from `NESTED_FORWARD_COMPAT` — the
opposite of what it must do for `delegation`.

## Recorded Ruby outcome

Recorded against `c500866` on ruby 4.0.6 (arm64-darwin23), under the corpus
environment (`TASKS_TIMEZONE=UTC`, `TASKS_DEVICE=fixture`, empty
`XDG_CONFIG_HOME`, `TASKS_DIR` at a copy).

```console
$ tasks check
error  line 3: scheduled_time has unknown keys: precision
error  line 4: deadline_time has unknown keys: calendar_uid
2 error(s), 0 warning(s)
exit 1
```

Reads tolerate it — and the unknown key does not disturb the rendering:

```console
$ tasks list
TODO
  Start time carrying an unknown nested key  ~6/16 8:30a Europe/London
  Due time carrying an unknown nested key  6/18 5:00p

exit 0
```

## Finding: the drop is unreachable from any store-level invocation

The stated repair path — "dropping it on the next write is the repair path, not
data loss" (`Format::NESTED_FORWARD_COMPAT`'s comment) — never runs, because
every mutation preflights `Check` against the whole file and this store fails
that preflight:

```console
$ tasks priority a1000002 A
failed to set priority
task file is already invalid — run `tasks check` (nothing was written)
exit 1
```

The file is unchanged. So the store is wedged: the write that would repair it is
the write the error blocks, and the only way out is a hand edit. No fixture can
close this, because no CLI invocation reaches `Format.dump_record`'s drop branch
on a store carrying the key — the drop is observable only at the unit level.

A port must reproduce **this** behavior (refuse, write nothing), not the
comment's. Whether the refusal should have a repair escape hatch is a Ruby
design question, recorded here and deliberately not fixed for the corpus.
