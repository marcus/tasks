#!/usr/bin/env python3
"""Generate the composition review's adversarial corpus.

The prior sweeps for this slice were exhaustive over *single* code points per
sigil, over String#inspect, and over Float#to_s literals. This corpus attacks
what those shapes cannot express: symbol names of two or more code points that
mix a sigil, a trailing `?`/`!`/`=`, and printable/non-printable non-ASCII;
nested containers and extreme numeric literals reaching Object#to_s through
`Array(values).map(&:to_s)`; and parse_cli argument shapes around the regex
branch boundaries.
"""

import io
import json
import os

cases = []


def add(case_id, **fields):
    cases.append(dict(case_id=case_id, **fields))


# --- 1. Multi-code-point symbol names, reached through unknown keywords ---
SYMBOL_NAMES = [
    # ASCII compositions of the suffix and sigil rules
    "a=", "A=", "a?", "a!", "Foo=", "a?=", "a!=", "a==", "@a=", "$a=", "@@a=",
    "@a?", "$a?", "@@a?", "_", "_a1", "a1", "1a", "a b",
    # operator-table members and near misses
    "..", "...", "&&", "||", "::", "&.", "!@", "~@", "<<=", "=>", "+@", "-@",
    "[]", "[]=", "`", "<=>", "!~", "=~", "**",
    # global shapes that need two or more characters after the sigil
    "$-a", "$-1", "$-_", "$-ab", "$--", "$0", "$00", "$0123", "$1", "$19",
    "$", "$-", "$_x", "$;", "$%",
    # non-ASCII compositions the single-code-point sweep cannot express
    "aé", "éa", "é?", "é=", "@é1", "@@éZ",
    "$éa", "$-é", "$-éé", "aé1_",
    "é͸",          # printable then non-printable
    "͸é",          # non-printable first
    "a­", "­a",    # SOFT HYPHEN: printable to Ruby
    "a", "a",    # NEL: the symbolPrintable exception
    "İ", "İa", "ß", "ﬁ",
    "αβ", "Ωm",
    "\U0001F600a", "a\U0001F600",
    "$-͸",              # non-printable behind the $- sigil
    "@", "@@b",
    "a​b",              # ZWSP: printable to Ruby
    "a b",              # LINE SEPARATOR: not printable
    "ab",              # C0 in the middle
    "\t", "a\tb", "a\nb",
]
for index, name in enumerate(SYMBOL_NAMES):
    add("comp-sym-%03d" % index, operation="new", kwargs={name: 1})

add("comp-sym-order-1", operation="new", kwargs={"$-a": 1, "a b": 2, "@@x": 3})
add("comp-sym-order-2", operation="new", kwargs={"zz": 1, "scope": "open", "aa": 2})

# --- 2. Numeric and container rendering through Array(values).map(&:to_s) ---
NUMBER_LITERALS = [
    "0", "-0", "1e400", "-1e400", "5e-324", "1e-323", "1e15", "1e16", "1e17",
    "1234567890123456", "12345678901234567", "0.1", "1.0", "100.0", "1e100",
    "-0.0", "0.0001", "0.00001", "1e-5", "123456789012345678901234567890",
    "-123456789012345678901234567890", "3.141592653589793", "2.5e-10",
    "9007199254740993", "1.7976931348623157e308", "2.2250738585072014e-308",
]
RAW_CASES = [("comp-num-%03d" % index, "new", '{"contexts": [%s]}' % literal)
             for index, literal in enumerate(NUMBER_LITERALS)]

NESTED = [
    '{"contexts": [[1, [2, {"a": [true, null, 1.5]}]]]}',
    '{"contexts": [{}]}',
    '{"contexts": [[]]}',
    '{"contexts": {"b": 1, "a": 2, "b": 3}}',
    '{"contexts": [{"b": 1, "a": 2}]}',
    '{"tags": [{"k": {"n": [1, "s"]}}]}',
    '{"text": [[["deep"]]]}',
    '{"contexts": [1, "two", true, false, null, 3.5, [], {}]}',
]
RAW_CASES += [("comp-nest-%03d" % index, "new", raw)
              for index, raw in enumerate(NESTED)]

# --- 4. Scalar coercion of scope, priority, and state ---
SCALARS = [
    '{"scope": ["open"]}', '{"scope": {"a": 1}}', '{"scope": 1}',
    '{"scope": true}', '{"scope": ""}', '{"priority": ""}', '{"priority": "a"}',
    '{"priority": "ß"}', '{"priority": false}', '{"priority": []}',
    '{"state": "todo"}', '{"state": "ınbox"}', '{"state": 0}',
    '{"state": ""}', '{"state": "DONE", "scope": "open"}',
    '{"state": "DONE", "scope": "done"}', '{"state": "PROPOSED", "scope": "all"}',
    '{"deferred_only": 0, "someday_only": ""}',
    '{"unavailable_only": [], "scope": "open"}', '{"body_search": {}}',
    '{"recurring_only": null}',
]
RAW_CASES += [("comp-scalar-%03d" % index, "new", raw)
              for index, raw in enumerate(SCALARS)]

# --- 3. parse_cli argument shapes ---
ARGVS = [
    [], [""], ["-"], ["--"], ["-A"], ["-D", "-A", "-B"], ["-Ax"], ["@"],
    ["@ctx", "@ctx"], ["+"], ["+\n"], ["+a\nb"], ["+\nab"], ["/"], ["/text"],
    ["//x"], ["-a", "--all"], ["--open", "-o"], ["--open", "--done"],
    ["--json", "--json"], ["-b", "-R", "-D"], ["--someday", "--on-hold"],
    ["--deferred", "--someday"], ["--unavailable", "--done"],
    ["--agent-ready", "--all"], ["--delegated", "--agent-ready"],
    ["--delegated", "--done"], ["-x"], ["-d"], ["--proposed"], ["-C"],
    ["-A", "-C"], ["--Open"], ["-o", "-o"], ["ALPHA", "Beta"],
    ["İstanbul"], ["ΟΔΟΣ"], ["ẞ"], [chr(0x1C89)], [chr(0x16EA0)], [chr(0x130)],
    ["a", "b", "c"], ["/İ", "ΣΣ"],
    ["+tag", "@ctx", "/txt", "free"], ["-A", "-a", "+t", "@c", "/s", "plain"],
]
for index, argv in enumerate(ARGVS):
    add("comp-cli-%03d" % index, operation="parse_cli", argv=argv)

TARGET = os.path.join(os.path.dirname(os.path.abspath(__file__)), "cases.jsonl")
with io.open(TARGET, "w", encoding="utf-8") as handle:
    for case in cases:
        handle.write(json.dumps(case, ensure_ascii=False) + "\n")
    for case_id, operation, raw in RAW_CASES:
        prefix = json.dumps({"case_id": case_id, "operation": operation},
                            ensure_ascii=False)
        handle.write(prefix[:-1] + ', "kwargs": ' + raw + "}\n")

print("%d cases -> %s" % (len(cases) + len(RAW_CASES), TARGET))
