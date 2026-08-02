# malformed/meta-version-control-characters

The schema version is a string containing JSON control-character escapes.

## What a correct implementation must do

Ruby decodes the JSON string before its diagnostic calls `String#inspect`.
That spelling uses `\a` for bell and `\e` for ESC, while remaining controls
use uppercase four-digit Unicode escapes:

```console
error  line 1: unsupported meta version "\u0000\a\e\u001F" (expected 2)
1 error(s), 0 warning(s)
exit 1
```
