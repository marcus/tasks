import json, os
SCRATCH = os.path.dirname(os.path.abspath(__file__))
F = "full-field-matrix"; I = "interleaved-tags"; T = "temporal-both-times"


def c(fx, i, f, v, **kw):
    d = {"fixture": fx, "id": i, "field": f, "value": v}
    d.update(kw)
    return d


none = {"kind": "none"}


def t(x): return {"kind": "text", "text": x}
def b(x): return {"kind": "bool", "bool": x}
def l(x): return {"kind": "list", "list": x}
def td(a, r): return {"kind": "tag_delta", "add": a, "remove": r}
def dt(d, local=None, tz=None, fold=0):
    return {"kind": "temporal", "date": d, "local": local, "timezone": tz, "fold": fold}


cases = []
for v in [t("Renamed task"), t("   padded   "), t(""), t("   "), none, b(True), t("naïve 🌱")]:
    cases.append(c(F, "f0000012", "title", v))
for v in [t("A"), t("C"), none, t("D"), t("")]:
    cases.append(c(F, "f0000012", "priority", v))
for v in [t("new body"), t(""), t("line1\nline2\n\n  trailing  "), none]:
    cases.append(c(F, "f0000060", "body", v))
for i in ["f0000012", "c1a2b3d3", "c1a2b3d4"]:
    fx = F if i.startswith("f") else I
    for v in [b(True), b(False), none, t("true")]:
        cases.append(c(fx, i, "deferred", v))
for i in ["c1a2b3d1", "c1a2b3d2", "c1a2b3d3", "c1a2b3d4"]:
    for v in [l([]), l(["@office"]), l(["@office", "@home"]), l(["bad"]), l(["@"]), l(["@a", "@a"]), t("@x")]:
        cases.append(c(I, i, "contexts", v))
    for v in [l([]), l(["alpha"]), l(["alpha", "beta", "gamma"]), l(["@x"]), l(["defer"]), l([""]), l(["a", "a"])]:
        cases.append(c(I, i, "tags", v))
for v in [td(["new"], []), td([], ["urgent"]), td(["defer"], ["@home"]), td([], []), td(["@x", "y"], ["alpha"])]:
    cases.append(c(I, "c1a2b3d1", "tag_delta", v))
    cases.append(c(I, "c1a2b3d2", "tag_delta", v))
for i, f in [("f0000011", "scheduled"), ("f0000020", "scheduled"), ("f0000021", "deadline"),
             ("f0000022", "scheduled"), ("f0000022", "deadline"), ("f0000040", "scheduled"),
             ("f0000040", "deadline"), ("f0000041", "deadline"), ("f0000023", "scheduled"),
             ("f0000030", "scheduled"), ("f0000034", "deadline")]:
    for v in [dt("2026-07-01"), dt("2026-07-01", "09:30"), dt("2026-07-01", "09:30", "Europe/London"), none, t("nope")]:
        cases.append(c(F, i, f, v))
cases.append(c(T, "e1000002", "scheduled", none))
cases.append(c(T, "e1000002", "deadline", none))
cases.append(c(T, "e1000002", "scheduled", dt("2026-06-17", "08:00", "America/New_York")))
for i in ["f0000020", "f0000021", "f0000022", "f0000011", "f0000030", "f0000042"]:
    for v in [none, t("scheduled"), t("deadline"), t("bogus")]:
        cases.append(c(F, i, "date_clear", v))
for i in ["f0000020", "f0000021", "f0000011", "f0000030", "f0000010", "f0000022", "f0000040"]:
    for v in [t(".+1w"), t("m:15"), t("nonsense"), none, t("off"), t("+9999y"), t("2y:02:5fri")]:
        cases.append(c(F, i, "recurrence", v))
for i in ["f0000020", "f0000021", "f0000022", "f0000011", "f0000040", "f0000042", "f0000041"]:
    for v in [t("3w"), t("2d"), t("5h"), none, t("off"), t("bogus"), t("9999y")]:
        cases.append(c(F, i, "lead", v))
for i in ["f0000020", "f0000011", "f0000040", "f0000042", "f0000023", "c1a2b3d3"]:
    fx = F if i.startswith("f") else I
    for v in [b(True), b(False), none]:
        cases.append(c(fx, i, "activate", v))
for i in ["f0000011", "f0000012", "f0000013", "f0000015", "f0000010", "f0000030", "f0000034",
          "f0000070", "f0000050", "f0000051", "f0000052", "f0000042"]:
    for v in [t("DONE"), t("NEXT"), t("PROPOSED"), t("CANCELLED"), t("TODO"), t("BOGUS"), none]:
        cases.append(c(F, i, "state", v))
cases.append(c(F, "f0000012", "location", t("f0000070")))
json.dump(cases, open(os.path.join(SCRATCH, "fields.json"), "w"), indent=1)
print(len(cases))
