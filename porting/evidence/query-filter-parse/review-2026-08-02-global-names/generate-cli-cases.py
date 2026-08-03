import json

argvs = [
    ["-A"], ["-B"], ["-C"], ["-a"], ["-A\n"], ["\n-A"], ["-ABC"], ["-A="],
    ["-D"], ["-o"], ["-d"], ["-x"], ["-b"], ["-R"], ["--json"],
    ["--open", "--open"], ["--open", "--done"], ["--all", "--archived"],
    ["--proposed"], ["--someday"], ["--on-hold"], ["--deferred", "--someday"],
    ["--unavailable"], ["--unavailable", "--done"], ["--agent-ready"],
    ["--agent-ready", "--all"], ["--delegated", "--agent-ready"],
    ["--delegated", "--done"], ["--nope"], ["-"], ["--"], ["---"],
    ["+"], ["+a"], ["+a\nb"], ["+\nb"], ["+\n"], ["+ab\ncd\nef"], ["++a"],
    ["+a\rb"], ["+ é"], ["/"], ["/x"], ["//"], ["/@a"], ["/-A"], ["/+t"],
    ["@"], ["@a"], ["@a b"], ["@\n"], ["\n@a"], ["a"], [""], [" "], ["\n"],
    ["x", "-A", "+t", "@c", "/text"], ["--nope", "--open"],
    ["--open", "--nope"], ["--done", "--all", "--nope"],
    ["-A", "-B"], ["+t", "+t"], ["@c", "@c"],
    ["--body", "--recurring", "--delegated"],
    ["é"], ["İ"], ["/İx"], ["+İ"],
]

kwargs_cases = [
    {}, {"scope": "open"}, {"scope": "all"}, {"scope": "proposed"},
    {"scope": "done"}, {"scope": "archived"}, {"scope": "nope"},
    {"scope": "OPEN"}, {"scope": "Open"},
    {"state": "todo"}, {"state": "TODO"}, {"state": "ToDo"},
    {"state": "done"}, {"state": "DONE"}, {"state": "nope"}, {"state": ""},
    {"scope": "open", "state": "DONE"}, {"scope": "open", "state": "WAITING"},
    {"scope": "done", "state": "CANCELLED"}, {"scope": "done", "state": "TODO"},
    {"scope": "proposed", "state": "PROPOSED"},
    {"scope": "proposed", "state": "INBOX"},
    {"scope": "all", "state": "PROPOSED"}, {"scope": "archived", "state": "DONE"},
    {"scope": "archived"}, {"scope": "all"},
    {"priority": "a"}, {"priority": "A"}, {"priority": "d"}, {"priority": ""},
    {"priority": "ß"}, {"priority": "ﬀ"}, {"priority": "ı"},
    {"state": "ınbox"}, {"state": "inboı"},
    {"scope": "ınbox"},
    {"text": ["A", "B"]}, {"text": ["İstanbul"]}, {"text": []},
    {"contexts": ["@a"], "tags": ["t"]},
    {"deferred_only": True, "someday_only": True},
    {"unavailable_only": True, "scope": "all"},
]

with open("cli-cases.jsonl", "w") as handle:
    for index, argv in enumerate(argvs):
        handle.write(json.dumps({"case_id": "cli-%03d" % index,
                                 "operation": "parse_cli", "argv": argv}) + "\n")
    for index, kwargs in enumerate(kwargs_cases):
        handle.write(json.dumps({"case_id": "new-%03d" % index,
                                 "operation": "new", "kwargs": kwargs}) + "\n")
print(len(argvs) + len(kwargs_cases))
