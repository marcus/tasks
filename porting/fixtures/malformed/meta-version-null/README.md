# malformed/meta-version-null

The schema version is JSON `null`.

## What a correct implementation must do

Ruby renders the decoded value as `nil`, not as JSON `null`:

```console
error  line 1: unsupported meta version nil (expected 2)
1 error(s), 0 warning(s)
exit 1
```
