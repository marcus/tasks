import json

names = []
# Global-name family: multi-codepoint after the sigil, the gap the
# single-codepoint sweep could not reach.
names += ["$-w", "$-1", "$-_", "$-é", "$--", "$-", "$-ww", "$-@",
          "$0", "$00", "$01", "$0123", "$1", "$10", "$123", "$0a",
          "$_", "$~", "$;", "$%", "$-À", "$- ", "$-あ", "$-_x",
          "$-\U0001F600", "$-", "$a1", "$aB", "$abc?", "$@", "$@@a"]
# Multi-codepoint identifiers and suffix rules.
names += ["ab", "Ab", "a1", "_a", "a?", "a!", "a=", "a?=", "a==", "a!!",
          "a?!", "αβ", "éa", "aé", "aé?", "@ab",
          "@@ab", "@a?", "@1", "@@1", "@", "@@", "$", "", "a b", "a-b",
          "ﬀ", "ß", "İ", "a​b", "​​",
          "ab", "ab", "ab", "ab", "é́",
          "Aé=", "@é", "@@é", "$é", "1a", "_", "__",
          "a\U0001F600b", "\U0001F600"]
# Operators, multi-character forms.
names += ["<=>", "<<", ">>", "===", "!=", "=~", "!~", "[]=", "[]", "+@",
          "-@", "~@", "!@", "**", "=", "**=", "<=", "x==", "<=>=", "[]=="]

with open("symbol-cases.jsonl", "w") as handle:
    for index, name in enumerate(names):
        handle.write(json.dumps({"case_id": "sym-%03d" % index,
                                 "operation": "new",
                                 "kwargs": {name: True}}) + "\n")
print(len(names))
