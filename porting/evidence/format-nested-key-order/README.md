# `format-nested-key-order` oracle capture

This directory records the Ruby writer contract for manifest slice
`format-nested-key-order`, characterized against source revision
`68fdeea770a4afafde956594e2636c4dd46c11a8` (`lib/tasks/format.rb` and
`lib/tasks/delegation.rb`). The scope is nested serialization only: temporal
objects have declared keys and discard unknown keys; delegation has declared
keys and preserves unknown keys after them in source insertion order.

The CLI never serializes a record during a read, and mutations are campaign 4.
`ruby-direct/observations.jsonl` is therefore captured by the read-only
[`format-nested-key-order-probe`](../../runners/ruby/format-nested-key-order-probe),
from [`format-nested-key-order-direct.jsonl`](../../runners/cases/format-nested-key-order-direct.jsonl).
It pins five writer boundaries:

- temporal declared order (`local`, `timezone`, `fold`) and dropping an unknown;
- delegation declared order followed by unknown keys in original insertion order;
- complete omission of a nested object that becomes empty;
- retention of `false` and `0` while empty nested values omit; and
- pass-through of malformed non-object nested values for `Check` to diagnose.

The focused Ruby oracle passed: 7 runs, 12 assertions, 0 failures, 0 errors,
0 skips.

```sh
ruby test/test_format.rb -n '/test_lead_pair_and_delegation_sit_between_recur_and_closed|test_delegation_emits_in_fixed_nested_order_with_absent_keys_omitted|test_delegation_omits_absent_nested_values_and_the_empty_object|test_delegation_round_trips_through_parse|test_unknown_delegation_keys_emit_after_the_declared_ones_and_round_trip|test_unknown_temporal_time_keys_are_still_dropped/'
ruby test/test_check.rb -n '/test_unknown_delegation_keys_survive_a_round_trip/'
TZ=UTC porting/runners/ruby/format-nested-key-order-probe \
  porting/runners/cases/format-nested-key-order-direct.jsonl \
  > porting/evidence/format-nested-key-order/ruby-direct/observations.jsonl
TZ=UTC porting/runners/ruby/format-nested-key-order-probe \
  porting/runners/cases/format-nested-key-order-direct.jsonl \
  | diff -u porting/evidence/format-nested-key-order/ruby-direct/observations.jsonl -
```

## Next step

Medium-risk translation must be performed by a different session. Add only the
nested writer to `internal/record`, then create a Go counterpart of this probe
and compare its JSONL output byte-for-byte with `ruby-direct/observations.jsonl`.
Do not use `encoding/json` for the final writer: its object-key behavior cannot
establish the captured order contract.
