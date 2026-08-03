"""Fourth differential batch: multi-field changesets — the applied ORDER, the
interactions between coupled fields, and the composite commands."""
import json, os, itertools
SCRATCH = os.path.dirname(os.path.abspath(__file__))

META = {"type": "meta", "version": 2}
SEC = {"type": "section", "id": "aa000001", "title": "Inbox"}


def task(**kw):
    base = {"type": "task", "id": "aa000010", "parent": "aa000001", "state": "TODO",
            "title": "Work", "tags": ["@home", "urgent", "@errands"], "body": "A note."}
    base.update(kw)
    return base


def t(x): return {"kind": "text", "text": x}
def none(): return {"kind": "none"}
def b(x): return {"kind": "bool", "bool": x}
def l(x): return {"kind": "list", "list": x}
def td(a, r): return {"kind": "tag_delta", "add": a, "remove": r}
def dt(d, local=None, tz=None):
    return {"kind": "temporal", "date": d, "local": local, "timezone": tz, "fold": 0}


def case(name, rec, changes, today="2026-06-10"):
    return {"name": name, "records": [META, SEC, rec], "id": rec["id"],
            "field": "state", "value": {"kind": "none"},
            "changes": [{"field": f, "value": v} for f, v in changes], "today": today}


records = {
    "plain": task(),
    "sched": task(scheduled="2026-06-15"),
    "dead": task(deadline="2026-06-20"),
    "both": task(scheduled="2026-06-15", deadline="2026-06-20"),
    "recur": task(scheduled="2026-06-08", recur=".+1w"),
    "lead": task(deadline="2026-08-01", lead="3w"),
    "inbox": task(state="INBOX", tags=[]),
    "defer": task(tags=["@home", "defer", "research"]),
    "proposed": task(state="PROPOSED"),
}

singles = [
    ("title", t("Renamed")),
    ("priority", t("A")),
    ("priority", none()),
    ("body", t("changed")),
    ("contexts", l(["@office"])),
    ("tags", l(["calm"])),
    ("deferred", b(True)),
    ("deferred", b(False)),
    ("scheduled", dt("2026-07-01")),
    ("scheduled", none()),
    ("deadline", dt("2026-07-10")),
    ("deadline", none()),
    ("recurrence", t("m:15")),
    ("recurrence", none()),
    ("lead", t("1w")),
    ("lead", none()),
    ("state", t("DONE")),
    ("state", t("NEXT")),
    ("tag_delta", td(["new"], ["urgent"])),
    ("activate", b(True)),
    ("date_clear", none()),
]

cases = []
# Every single field, over every record shape: the changeset path has to agree
# with the patch path on all of them.
for name, rec in records.items():
    for field, value in singles:
        cases.append(case(f"one-{name}-{field}", rec, [(field, value)]))

# Pairs whose ORDER matters, and the pairs the validator refuses.
pairs = [
    [("title", t("Renamed")), ("state", t("DONE"))],
    [("state", t("DONE")), ("title", t("Renamed"))],
    [("scheduled", none()), ("deadline", dt("2026-07-10"))],
    [("deadline", dt("2026-07-10")), ("scheduled", none())],
    [("scheduled", none()), ("deadline", none())],
    [("deadline", none()), ("recurrence", t("m:15"))],
    [("recurrence", t("m:15")), ("deadline", dt("2026-07-10"))],
    [("lead", t("1w")), ("deadline", dt("2026-07-10"))],
    [("deadline", dt("2026-07-10")), ("lead", t("1w"))],
    [("contexts", l(["@office"])), ("tags", l(["calm"]))],
    [("tags", l(["calm"])), ("contexts", l(["@office"]))],
    [("deferred", b(True)), ("priority", t("A"))],
    [("tag_delta", td(["x"], [])), ("tags", l(["calm"]))],
    [("date_clear", none()), ("scheduled", dt("2026-07-01"))],
    [("activate", b(True)), ("deferred", b(False))],
    [("activate", b(True)), ("title", t("Renamed"))],
    [("title", t("Renamed")), ("priority", t("B")), ("body", t("x")), ("state", t("NEXT"))],
    [("recur", t("m:15")), ("recurrence", t("m:15"))],
    [("title", t("")), ("priority", t("A"))],
    [("priority", t("A")), ("title", t(""))],
    [("nonsense", t("x"))],
]
for name, rec in records.items():
    for index, changes in enumerate(pairs):
        cases.append(case(f"pair-{name}-{index}", rec, changes))

json.dump(cases, open(os.path.join(SCRATCH, "changeset.json"), "w"), indent=1)
print(len(cases))
