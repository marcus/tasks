#!/usr/bin/env python3
"""probe_diff.py — compare two probe objects field for field.

    probe_diff.py <ruby.json> <go.json> <name>

Exit 0 when the two objects are identical outside `environment`, 1 otherwise,
printing the differing fields. It is deliberately not a plain `diff`: the
report names WHICH field diverged, because "the pins disagree" and "the
revision algorithm disagrees" are different findings that a textual diff of a
one-line JSON document renders identically.

`environment` is the only exclusion, and it is not a normalization: its four
fields are each implementation's self-report about its own runtime, so they can
never agree and porting/compare records them without comparing them. Anything
else that differs is a Go defect.
"""

import json
import sys


def load(path):
    with open(path, "rb") as handle:
        raw = handle.read()
    if not raw.strip():
        return None
    return json.loads(raw)


def report(name, field, ruby, go):
    print(f"      {field}\n        ruby: {str(ruby)[:300]}\n        go:   {str(go)[:300]}")


def main():
    ruby_path, go_path, name = sys.argv[1], sys.argv[2], sys.argv[3]
    try:
        ruby = load(ruby_path)
    except Exception as error:  # noqa: BLE001 — a probe that printed garbage is the finding
        print(f"RUBY-PROBE-FAILED {name}: {error}")
        return 1
    try:
        go = load(go_path)
    except Exception as error:  # noqa: BLE001
        print(f"GO-PROBE-FAILED   {name}: {error}")
        return 1
    if ruby is None or go is None:
        print(f"EMPTY-OUTPUT      {name}")
        return 1

    ruby.pop("environment", None)
    go.pop("environment", None)
    if ruby == go:
        print(f"identical {name}")
        return 0

    print(f"MISMATCH  {name}")
    for key in sorted(set(ruby) | set(go)):
        left, right = ruby.get(key), go.get(key)
        if left == right:
            continue
        if key == "pins":
            by_name = lambda pins: {pin["name"]: pin for pin in pins or []}  # noqa: E731
            left_pins, right_pins = by_name(left), by_name(right)
            for pin in sorted(set(left_pins) | set(right_pins)):
                if left_pins.get(pin) != right_pins.get(pin):
                    report(name, f"pins[{pin}]", left_pins.get(pin), right_pins.get(pin))
        elif isinstance(left, dict) and isinstance(right, dict):
            for field in sorted(set(left) | set(right)):
                if left.get(field) != right.get(field):
                    report(name, f"{key}.{field}", left.get(field), right.get(field))
        else:
            report(name, key, left, right)
    return 1


if __name__ == "__main__":
    sys.exit(main())
