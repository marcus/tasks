# legacy/schema-v1-time-metadata

A version 1 header on a record that carries `scheduled_time` — a combination
no writer of either version ever produces, and the one case `migrate` must
refuse rather than silently accept.

## What it exercises

- The migrator's v1 content rule: "version 1 must not contain time metadata".
- A malformed *legacy* store, as distinct from a malformed current one: the
  version is wrong AND the content contradicts the version.

## What a correct implementation must do

Refuse the migration with `status: :invalid` and an error naming the file, and
write nothing — not the backup, not the new header.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 1: unsupported meta version 1 (expected 2)
1 error(s), 0 warning(s)
exit 1
```

`check` reports only the version. The time-metadata contradiction is diagnosed
by `tasks migrate`, not by `check` — a port that folds the two checks together
will emit an extra error here.
