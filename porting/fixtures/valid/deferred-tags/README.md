# valid/deferred-tags

The `defer` tag — `Store::DEFER_TAG`, the only on-disk mechanism for putting
something on indefinite hold — did not appear anywhere in the corpus. This
fixture is the store for every read-side branch that depends on it: filter
matching (`--deferred`, `--someday`, `--unavailable` across scopes), the
`deferred` / `available` / `availability_reason` / `availability_blocker_id`
fields of the task projection, and the tag-projection asymmetry between
`TaskView#tags` and the API's `Representation.task`.

## What it exercises

| Record | Shape |
|---|---|
| `de000002` | open, no hold — the control |
| `de000003` | **own hold**, plus a `@context` and an ordinary tag (the projection case) |
| `de000004` | **own hold** on a parent |
| `de000005` | child of `de000004`: **held only by inheritance**, `deferred? == false` |
| `de000006` | grandchild: hold inherited from **two levels up** |
| `de000007` | own hold on a `WAITING` task — a hold is state-independent |
| `de000008` | unavailable because of a **future date**, carrying no `defer` tag |
| `de000009` | `DONE` while still carrying `defer` |
| `de00000a` | `PROPOSED` while carrying `defer` |
| `de0000b1` | `archive.jsonl`: an archived `DONE` record still carrying `defer` |

`de000008` is what separates the two meanings of "deferred" (see finding 1), and
`de000009` / `de00000a` / `de0000b1` are what make `--deferred` at a non-open
scope return anything at all.

**No mutation path produces `de000009`, `de00000a`, or `de0000b1`.** Closing a
task strips `DEFER_TAG` (`Store#state`, and the `close_open_descendants`
cascade), so a closed record carrying `defer` can only arrive by hand-edit or by
a merge. They are structurally valid — `check` passes — and the read side has a
branch for them that nothing else in the corpus reaches, which is exactly why
they are here.

## What a correct implementation must do

- Treat `defer` as an **own** hold, and compute effective availability by
  walking the task's *task* ancestors: nearest deferred ancestor wins and
  becomes `availability_blocker_id`, with reason `ancestor_on_hold`.
- Order the availability answer so `closed` and `proposed` short-circuit ahead
  of the hold walk — a closed or proposed record reports those reasons even
  when it carries `defer`.
- Keep `defer` inside `TaskView#tags` and strip it from the API representation.
- Implement `--deferred` as *scope-dependent* (finding 1).

## Recorded Ruby outcome

Environment as in the corpus README (`TASKS_TIMEZONE=UTC`, `TASKS_DEVICE=fixture`,
empty `XDG_CONFIG_HOME`, `TASKS_DIR` at a copy).

```console
$ tasks check
ok — 9 tasks parsed, no structural errors
exit 0

$ tasks check --all-files
ok — 10 records parsed, no structural errors
exit 0
```

### Filters

```console
$ tasks list
TODO
  Open task with no hold at all  @computer
exit 0
```

```console
$ tasks list --deferred
TODO
  Own hold, with a context and an ordinary tag  @computer  (on hold)
  Held parent  (on hold)
  Held only by inheritance  @home  (on hold via Held parent)
  Unavailable by a future date, not by a hold  ~1/1  (unavailable until 2099-01-01)

NEXT
  Held two levels up  (on hold via Held parent)

WAITING
  Own hold on a WAITING task  (on hold)
exit 0
```

`tasks list --unavailable` and `tasks list --deferred --unavailable` print
exactly the same six rows.

```console
$ tasks list --someday
TODO
  Own hold, with a context and an ordinary tag  @computer  (on hold)
  Held parent  (on hold)

WAITING
  Own hold on a WAITING task  (on hold)
exit 0
```

`tasks list --someday --unavailable` prints the same three rows.

```console
$ tasks list --done --deferred
DONE
  Done record that still carries the defer tag  (on hold)
exit 0

$ tasks list --archived --deferred
DONE
  Archived record that still carries the defer tag  (on hold)  (archived)
exit 0

$ tasks list --proposed --deferred
PROPOSED
  Proposed record carrying the defer tag  (on hold)
exit 0

$ tasks list --all --deferred
PROPOSED
  Proposed record carrying the defer tag  (on hold)

TODO
  Own hold, with a context and an ordinary tag  @computer  (on hold)
  Held parent  (on hold)

WAITING
  Own hold on a WAITING task  (on hold)

DONE
  Done record that still carries the defer tag  (on hold)
  Archived record that still carries the defer tag  (on hold)  (archived)
exit 0
```

`tasks list --all --someday` prints exactly the same six rows as
`tasks list --all --deferred`.

```console
$ tasks list --deferred --someday
--deferred and --someday are mutually exclusive
exit 1
```

### Projection

`tasks show --json` (the CLI's own envelope: `tags` minus contexts, `defer`
retained):

```console
$ tasks show de000003 --json
{"id":"de000003",…,"tags":["defer","research"],"contexts":["@computer"],"deferred":true,…,
 "available":false,"availability_reason":"on_hold","availability_blocker_id":"de000003",…}
exit 0

$ tasks show de000005 --json
{"id":"de000005",…,"tags":[],"contexts":["@home"],"deferred":false,…,
 "available":false,"availability_reason":"ancestor_on_hold","availability_blocker_id":"de000004",…}
exit 0

$ tasks show de000009 --json
ref outside scope: de000009 — task is DONE; expected an open or PROPOSED task
exit 2
```

`TaskView#to_h` against `Representation.task`, over every record — the second
line of each pair is the API projection:

```text
de000002 TaskView  tags=["@computer"]                     deferred=false available=true  reason="available"       blocker=nil
de000002 API       tags=[]                                deferred=false
de000003 TaskView  tags=["defer", "@computer", "research"] deferred=true  available=false reason="on_hold"         blocker="de000003"
de000003 API       tags=["research"]                      deferred=true
de000004 TaskView  tags=["defer"]                         deferred=true  available=false reason="on_hold"         blocker="de000004"
de000004 API       tags=[]                                deferred=true
de000005 TaskView  tags=["@home"]                         deferred=false available=false reason="ancestor_on_hold" blocker="de000004"
de000005 API       tags=[]                                deferred=false
de000006 TaskView  tags=[]                                deferred=false available=false reason="ancestor_on_hold" blocker="de000004"
de000006 API       tags=[]                                deferred=false
de000008 TaskView  tags=[]                                deferred=false available=false reason="scheduled"       blocker="de000008"
de000008 API       tags=[]                                deferred=false
de000009 TaskView  tags=["defer"]                         deferred=true  available=false reason="closed"          blocker=nil
de000009 API       tags=[]                                deferred=true
de00000a TaskView  tags=["defer"]                         deferred=true  available=false reason="proposed"        blocker=nil
de00000a API       tags=[]                                deferred=true
de0000b1 TaskView  tags=["defer"]                         deferred=true  available=false reason="closed"          blocker=nil
de0000b1 API       tags=[]                                deferred=true
```

## Findings

**1. `--deferred` means two different things depending on scope.** At the
default open scope it means *unavailable for any reason* — so `de000008`, which
carries no `defer` tag and is merely dated 2099, is listed by `--deferred` and
is identical to `--unavailable`. At any non-open scope (`--done`, `--archived`,
`--proposed`, `--all`) it drops to the literal tag test, and there
`--deferred` and `--someday` return the same set. `--someday` is the only
spelling that always means "carries the `defer` tag".

**2. `--someday` reports own holds only.** `de000005` and `de000006`, held by an
ancestor, are absent from `--someday` and present in `--deferred`/`--unavailable`.
The inherited hold is an availability fact, not a tag fact.

**3. The `(on hold)` annotation is a tag test, not an availability test.** A
`DONE`, archived, or `PROPOSED` record carrying `defer` renders `(on hold)` even
though its `availability_reason` is `closed` or `proposed`. The two surfaces do
not agree, and the projection is the one to port from.

**4. The blocker is the nearest deferred ancestor, not the immediate parent.**
`de000006`'s parent `de000005` is not itself deferred; the reported
`availability_blocker_id` is `de000004`, two levels up.

**5. `+defer` matches the tag, but the default scope hides every match.**
`tasks list +defer` prints `No matching tasks.`, and so does `tasks list
+research` — not because the tag filter cannot see `defer` (it can: `+defer`
matches `Item#tags` like any other tag), but because the open scope's
availability filter runs first and removes every held task. `--someday +defer`
and `--all +defer` both return them. A port that gets the conjunction order
wrong produces output here and passes everywhere else in the corpus.

```console
$ tasks list +defer
No matching tasks.
exit 0

$ tasks list --someday +defer
TODO
  Own hold, with a context and an ordinary tag  @computer  (on hold)
  Held parent  (on hold)

WAITING
  Own hold on a WAITING task  (on hold)
exit 0
```
