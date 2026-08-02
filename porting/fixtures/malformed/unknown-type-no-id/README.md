# malformed/unknown-type-no-id

An unknown record type that has no ID.

## What a correct implementation must do

The unknown type short-circuits the record before ID validation, producing only:

```console
error  line 2: unknown record type "widget"
1 error(s), 0 warning(s)
exit 1
```
