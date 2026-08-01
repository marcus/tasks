# adversarial/conflicting-claim

Byte-identical in structure to `same-owner-retry` (different generated ids), but
driven by a **second** worker: the genuine lost-race case.

## What it exercises

- The lost claim race: `worker-beta` attempts to claim work `worker-alpha`
  holds.
- The conflict message naming the current holder and the timestamp of the
  transition — which is why `Delegation` refuses control characters in a worker
  id: the loser renders the winner's id in its own terminal.
- Worker matching on release: a release by a worker that does not hold the claim
  is refused with a *different* message from the claim conflict.

## Recorded behavior

```console
$ tasks claim "Draft the neighbourhood newsletter" --worker harness/model/worker-beta
conflict: already claimed by harness/model/worker-alpha at 2026-08-01T18:11:02Z
exit 1

$ tasks release "Draft the neighbourhood newsletter" --worker harness/model/worker-beta
conflict: claim is held by harness/model/worker-alpha, not "harness/model/worker-beta"
exit 1
```

Note the quoting asymmetry: the holder is bare, the rejected worker is
`inspect`-quoted. Both spellings are observable and must be reproduced.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 3 tasks parsed, no structural errors
exit 0
```
