# malformed/meta-version-object-order

The schema version is a JSON object whose insertion order differs from lexical
key order: `{"b":1,"a":2}`.

## What a correct implementation must do

Ruby parses this into a Hash and its diagnostic uses `Hash#inspect`, preserving
the JSON member order:

```console
error  line 1: unsupported meta version {"b" => 1, "a" => 2} (expected 2)
1 error(s), 0 warning(s)
exit 1
```
