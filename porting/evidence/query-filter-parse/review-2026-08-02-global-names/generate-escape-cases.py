import json

texts = []
for code in list(range(0x00, 0x21)) + [0x7F, 0x85, 0xA0, 0xAD, 0x200B, 0x2028,
                                       0xFEFF, 0xFFFD, 0x10FFFF, 0x1F600,
                                       0xE000, 0x0300]:
    texts.append(chr(code))
    texts.append("a" + chr(code) + "b")
texts += ['#{', '#$', '#@', '#@@', '#a', '#', 'a#{b}', '"', '\\', 'a"b\\c',
          '\a\b\t\n\v\f\r\x1b', 'é', 'ﬀ']

cases = []
for index, text in enumerate(texts):
    # String path: Hash key rendered by Hash#to_s, i.e. String#inspect.
    cases.append({"case_id": "str-%03d" % index, "operation": "new",
                  "kwargs": {"contexts": [{text: text}]}})
    # Symbol path: the same characters as an unknown keyword name.
    cases.append({"case_id": "sym-%03d" % index, "operation": "new",
                  "kwargs": {text: True}})

with open("escape-cases.jsonl", "w") as handle:
    for case in cases:
        handle.write(json.dumps(case) + "\n")
print(len(cases))
