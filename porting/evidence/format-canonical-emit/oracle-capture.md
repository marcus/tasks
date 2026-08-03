# Canonical emission — Ruby oracle capture

Captured from Ruby at `cb08c87ade3362fd8f09992c9d8d80a15dafba2c` on
2026-08-02. `porting/manifest-issues drift --json` reported no drift for the
manifest's `68fdeea770a4afafde956594e2636c4dd46c11a8` source pin.

## Assertions

`ruby test/test_format.rb` passed: 26 runs, 58 assertions, no failures,
errors, or skips. The slice-owned tests establish these exact rules:

- Emit known keys in this order: `type`, `id`, `parent`, `state`, `priority`,
  `title`, `tags`, `scheduled`, `scheduled_time`, `deadline`, `deadline_time`,
  `recur`, `lead`, `lead_skip`, `delegation`, `closed`, `archived`, `body`,
  `updated`.
- Emit other top-level keys after known keys in their input insertion order.
- Omit `nil`, empty strings, and empty arrays; omit the parser-only `line`
  field. `dump([])` is exactly the empty string; a nonempty dump has one line
  per record and one trailing newline.
- Stringify only top-level symbol keys. Preserve UTF-8 characters directly;
  do not escape them as `\\u` sequences.

This tick intentionally does not translate. Nested-object ordering and the
temporal/delegation forward-compat distinction belong to
`format-nested-key-order`; the one temporal sample below is a boundary marker,
not evidence that this slice owns that rule.

## Direct boundary observations

```json
{
  "scrambled": "{\"type\":\"task\",\"id\":\"i1\",\"parent\":\"p1\",\"state\":\"NEXT\",\"title\":\"T\",\"body\":\"note\"}",
  "omissions": "{\"type\":\"task\",\"id\":\"i1\",\"title\":\"T\"}",
  "unknown": "{\"type\":\"task\",\"title\":\"T\",\"zeta\":1,\"alpha\":2}",
  "unicode": "{\"type\":\"task\",\"title\":\"Café — résumé naïve\"}",
  "temporal_unknown_boundary": "{\"type\":\"task\",\"title\":\"T\",\"scheduled\":\"2026-07-27\",\"scheduled_time\":{\"local\":\"09:00\"}}"
}
```

## Fixture round trips

The capture read fixture files as UTF-8 text, parsed with `Tasks::Format`, then
dumped the returned records. `wrong-key-order` is deliberately the sole
non-identical input: it has valid JSON but noncanonical ordering, and Ruby
rewrites it to the canonical 582-byte form.

| Fixture | Records | Input SHA-256 | Dump SHA-256 | Exact bytes |
| --- | ---: | --- | --- | --- |
| valid/empty-store | 1 | `32c39db1a9da0270bf0134d63d7e52ce9771d06d81e61e5e9c9ed8610e00bf60` | same | yes |
| valid/single-task | 2 | `6f1ce7683239606fb0c73730bb5173188a05c55f3512ef927f6534a69013bd60` | same | yes |
| valid/small-gtd | 14 | `e0ade36b8374dc044b3c0b277e60edb9c2051591785be8b8340d265d0688eabe` | same | yes |
| valid/deep-nesting | 12 | `13a349a178477d415de5143e97c514d204f5a6fdcc28c5818498bcdf07b31eb9` | same | yes |
| valid/full-field-matrix | 34 | `b1520184b16fe4ebf48839fad38beb615d4e9f17d334ec9b75275de0d33fd4d7` | same | yes |
| valid/archive-pair tasks | 6 | `2088e054eaf73252f97824a008cbf24e6ccdda3ed3dbce0a9d45e7faa70e1a37` | same | yes |
| valid/archive-pair archive | 7 | `771f2c1fb5a3b661635686ee718435aae708a4d86ac7ff8bffaf941e0492154a` | same | yes |
| valid/scale-ordering | 461 | `c74ec4a9754fab1c0773a5da2bd9e657c9dda48b8fec537f2bc2837ef43917d6` | same | yes |
| compat/forward-compat-unknown-keys | 4 | `92db49c74378e03ed55304fb73098d280a3552ced9e4f43fc1addb3fda1e6e77` | same | yes |
| compat/future-schema-v3 | 3 | `f6e61e3ae473ae675be734e7704889506681c0b0a1287f1bcff1f797e105b4f8` | same | yes |
| malformed/wrong-key-order | 7 | `39f0fc8dddcb3920e62c22c4f5f7e16a8295b89bf0133eaca3397de39899e554` | `9e94e743468cee9a48d30fb5c5181e260179a60f7f1249e2d41226ef1c3dac0c` | no |

The next role is translation (top-tier or mid-tier implementation context),
followed by byte-level differential conformance, high-risk fault/stress proof,
and two independent reviews. No Go output has been accepted or used here.
