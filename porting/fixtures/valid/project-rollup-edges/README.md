# valid/project-rollup-edges

The roll-up edges `query-projects` has no store for: `held_count` (which was 0 on
every fixture, because nothing in the corpus carried the `defer` tag), and the
three exclusion edges — an empty project section, a done-only section, and a
sub-section nested inside a project.

## What it exercises

| Section | Shape | Roll-up |
|---|---|---|
| `Inbox` (`9c000001`) | top-level, holds an `INBOX` task | never listed — Inbox is excluded from areas |
| `Projects` (`9c000010`) | the root heading | never listed; its section children are the projects |
| `Project with mixed holds` (`9c000011`) | one open `NEXT`, one **own-held** task, one **held parent** with an inherited-hold child | `open 1 · next 1 · held 3`, not stuck |
| `Project held end to end` (`9c000020`) | an own-held task and its inherited-hold child | `open 0 · held 2`, stuck — still listed |
| `Empty project` (`9c000030`) | no children at all | `open 0 · held 0`, stuck — still listed |
| `Done-only project` (`9c000040`) | one `DONE` task | `open 0 · held 0`, stuck — still listed |
| `Project with a sub-section` (`9c000050`) | a **sub-section** (`9c000051`) holding one open and one held task | `open 1 · held 1`; the sub-section is not listed in its own right |
| `Area with open work` (`9c000060`) | top-level, one open `NEXT` and one held task | listed as an area, `open 1 · held 1` |
| `Area whose only open work is held` (`9c000070`) | top-level, one own-held task | **not listed at all** — `open_count` is 0 |

`held_count` covers both halves of the roll-up the slice's Ruby tests name: an
own hold (`9c000013`, `9c000021`, `9c000053`, `9c000062`, `9c000071`) and a hold
inherited from an ancestor task (`9c000015`, `9c000022`).

## What a correct implementation must do

- Roll a project up over **all** open descendants at any depth, including those
  inside a nested sub-section, splitting them into `task_ids`/`open_count` (not
  held) and `held_count` (own or inherited hold).
- List every section child of `Projects` even when it rolls up to zero, and list
  a top-level non-Inbox section as an area **only** when its `open_count` is
  positive — a held-only area disappears entirely, held work and all.
- Never list a section nested inside a project as a project or an area.
- Set `stuck` from `next_count == 0`, so a project with no open work at all is
  stuck.
- Sort projects before areas, then `next_date` (nil last), then title.

## Recorded Ruby outcome

Environment as in the corpus README (`TASKS_TIMEZONE=UTC`, `TASKS_DEVICE=fixture`,
empty `XDG_CONFIG_HOME`, `TASKS_DIR` at a copy).

```console
$ tasks check
ok — 13 tasks parsed, no structural errors
exit 0
```

```console
$ tasks projects
Projects
  Project with mixed holds    1 open · 1 next · next 6/20
  Done-only project           0 open · 0 next  (stuck)
  Empty project               0 open · 0 next  (stuck)
  Project held end to end     0 open · 0 next  (stuck)
  Project with a sub-section  1 open · 0 next  (stuck)

Areas
  Area with open work         1 open · 1 next
exit 0
```

```console
$ tasks projects --json
[{"id":"9c000011","title":"Project with mixed holds","parent_id":"9c000010","kind":"project","open_count":1,"next_count":1,"next_date":"2026-06-20","next_at":"2026-06-20T00:00:00Z","stuck":false,"held_count":3,"task_ids":["9c000012"]},
 {"id":"9c000040","title":"Done-only project","parent_id":"9c000010","kind":"project","open_count":0,"next_count":0,"stuck":true,"held_count":0,"task_ids":[]},
 {"id":"9c000030","title":"Empty project","parent_id":"9c000010","kind":"project","open_count":0,"next_count":0,"stuck":true,"held_count":0,"task_ids":[]},
 {"id":"9c000020","title":"Project held end to end","parent_id":"9c000010","kind":"project","open_count":0,"next_count":0,"stuck":true,"held_count":2,"task_ids":[]},
 {"id":"9c000050","title":"Project with a sub-section","parent_id":"9c000010","kind":"project","open_count":1,"next_count":0,"stuck":true,"held_count":1,"task_ids":["9c000052"]},
 {"id":"9c000060","title":"Area with open work","kind":"area","open_count":1,"next_count":1,"stuck":false,"held_count":1,"task_ids":["9c000061"]}]
exit 0
```

(One JSON document on one line; wrapped here for reading. `next_date`,
`next_time`, `next_at`, `parent_id` and `body` are omitted when absent.)

```console
$ tasks project show 9c000011
Project with mixed holds  [project]
  id:        9c000011
  1 open · 1 next · next 6/20
exit 0

$ tasks project show 9c000051
no match: 9c000051
exit 2

$ tasks project show 9c000070 --json
no match: 9c000070
exit 2
```

For the open/held split as `list` sees it:

```console
$ tasks list
INBOX
  Uncaptured note

TODO
  Rolls up into the parent project

NEXT
  Open next action  ~6/20
  Open area task
exit 0

$ tasks list --someday
TODO
  Own hold in a mixed project  (on hold)
  Held parent  (on hold)
  Own hold over the whole project  (on hold)
  Held inside the sub-section  (on hold)
  Held area task  (on hold)
  Own hold in an area  (on hold)
exit 0
```

## Findings

**1. `held_count` is JSON-only.** Neither `tasks projects` nor `tasks project
show` renders it; `Project with mixed holds` prints `1 open · 1 next` whether it
holds three parked tasks or none. The count exists for the archive refusal and
for API clients. A port comparing only the human surface would never notice a
wrong `held_count`.

**2. A held-only area vanishes; a held-only project does not.** `Area whose only
open work is held` is absent from the listing, and `tasks project show` on it
answers `no match` with exit 2 — its one open, parked task is unreachable
through this surface. `Project held end to end` is in exactly the same state and
is listed, because projects are listed unconditionally and areas are gated on
`open_count.positive?`. The asymmetry is in `TaskQueries#projects`, which builds
the area view and then discards it.

**3. Ordering puts every zero-date project in title order behind the dated one.**
`Done-only project`, `Empty project`, `Project held end to end`, `Project with a
sub-section` sort alphabetically after `Project with mixed holds`, which is the
only one with a `next_date`. Title order, not file order, breaks the tie.
