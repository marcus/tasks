"""Structural diff of two probe captures, keyed by case_id.

Byte comparison is wrong here: Ruby's JSON.generate emits U+2028 and U+2029 raw
where Go's encoder escapes them, for the same decoded value. Decode, then
compare.

    python3 compare.py RUBY.jsonl GO.jsonl [--max N]

Exit 0 when every case matches, 1 otherwise; mismatches print to stdout.
"""
import json
import sys


def load(path):
    records = {}
    with open(path, encoding="utf-8") as handle:
        for line in handle:
            if not line.strip():
                continue
            record = json.loads(line)
            records[record["case_id"]] = record
    return records


def main():
    ruby_path, go_path = sys.argv[1], sys.argv[2]
    limit = int(sys.argv[sys.argv.index("--max") + 1]) if "--max" in sys.argv else 20
    ruby, go = load(ruby_path), load(go_path)

    mismatches = []
    if ruby.keys() != go.keys():
        mismatches.append(("<case set>", sorted(ruby.keys() ^ go.keys())[:limit], None))
    for case_id in ruby:
        if case_id in go and ruby[case_id] != go[case_id]:
            mismatches.append((case_id, ruby[case_id], go[case_id]))

    print(f"{len(ruby)} cases, {len(ruby) - len(mismatches)} match, "
          f"{len(mismatches)} mismatch")
    for case_id, expected, actual in mismatches[:limit]:
        print(f"\n{case_id}\n  ruby: {json.dumps(expected, ensure_ascii=False)[:400]}"
              f"\n  go:   {json.dumps(actual, ensure_ascii=False)[:400]}")
    if len(mismatches) > limit:
        print(f"\n... {len(mismatches) - limit} more")
    return 1 if mismatches else 0


if __name__ == "__main__":
    sys.exit(main())
