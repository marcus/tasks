# malformed/bad-utf8

A title containing the byte sequence `C3 28`, which is not valid UTF-8.

## What it exercises

- Encoding validation ahead of parsing: the whole file is rejected before any
  line is looked at.
- Line **0** as the "whole file" line number — the only non-positive line number
  the diagnostics use.
- That one bad byte fails the file rather than one record.

## Recorded `tasks check` outcome

```console
$ tasks check
error  line 0: file is not valid UTF-8
1 error(s), 0 warning(s)
exit 1
```

A port must reproduce the line-0 convention and the whole-file scope. Go's
`utf8.Valid` gives the same verdict, but Go strings tolerate invalid UTF-8 where
Ruby raises, so a port that never checks explicitly will silently accept this
file.
