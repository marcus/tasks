# malformed/missing-meta

The `meta` header removed; the file otherwise valid and correctly ordered.

## What it exercises

- The header requirement: line 1 must be `{"type":"meta","version":2}`.
- That a store with no version claim is refused rather than assumed current.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: line 1 must be a meta record ({"type":"meta","version":2})
1 error(s), 0 warning(s)
exit 1
```

One error only: the records themselves are fine, so the header is the whole
complaint.
