# malformed/meta-version-exponent

The schema version is JSON exponent notation: `2e0`.

## What a correct implementation must do

Ruby parses this as the Float `2.0`, so `tasks check` reports:

```console
error  line 1: unsupported meta version 2.0 (expected 2)
1 error(s), 0 warning(s)
exit 1
```
