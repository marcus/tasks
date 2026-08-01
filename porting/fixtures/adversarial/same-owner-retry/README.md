# adversarial/same-owner-retry

One task already `claimed` by `harness/model/worker-alpha`, and one task
`ready` and unclaimed. The fixture exists to be driven by **the same worker**
that already holds the claim.

## What it exercises

- Whether re-claiming your own claim is idempotent. **It is not.**
- The contrast with `adversarial/conflicting-claim`, which runs the identical
  command as a different worker.
- The compare-and-set on `delegation.status`: `ready → claimed` is the only
  permitted transition, evaluated under the store lock against freshly read
  records.
- A second, genuinely claimable task in the same store, so the fixture also
  covers the success path.

## Recorded behavior

```console
$ tasks claim "Draft the neighbourhood newsletter" --worker harness/model/worker-alpha
conflict: already claimed by harness/model/worker-alpha at 2026-08-01T18:11:01Z
exit 1

$ tasks claim "Replace the door handle" --worker harness/model/worker-alpha
claimed by harness/model/worker-alpha: Replace the door handle
exit 0
```

## Finding

A worker retrying its own claim — after a crash, a timeout, or a lost response —
is refused with the same conflict a losing racer gets, and the holder named in
the message is the retrying worker itself. The gate is on `status`, not on
identity.

This is recorded, not judged. It is a plausible source of divergence in a port
(idempotent retry is the "obvious" design), so it needs a conformance case
rather than an assumption.

## Recorded `tasks check` outcome

```console
$ tasks check
ok — 3 tasks parsed, no structural errors
exit 0
```
