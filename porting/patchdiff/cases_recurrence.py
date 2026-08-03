"""Second differential batch: recurrence completion and the interactions the
first batch's fixtures do not contain — timed anchors, paired dates, delegation
carry-forward, DST gaps, and catch-up cookies read from several `today`s."""
import json, os
SCRATCH = os.path.dirname(os.path.abspath(__file__))

META = {"type": "meta", "version": 2}
SEC = {"type": "section", "id": "aa000001", "title": "Inbox"}


def task(**kw):
    base = {"type": "task", "id": "aa000010", "parent": "aa000001", "state": "TODO",
            "title": "Recurring work"}
    base.update(kw)
    return base


def case(name, rec, field, value, today="2026-06-10"):
    return {"name": name, "records": [META, SEC, rec], "id": rec["id"],
            "field": field, "value": value, "today": today}


def t(x): return {"kind": "text", "text": x}
def none(): return {"kind": "none"}
def b(x): return {"kind": "bool", "bool": x}


cases = []
cookies = [".+1w", "+2d", "++1m", "w:mon,wed", "m:15", "y:07-04", ".+1m", "+1y",
           "m:-1", "m:2fri", "y:02:5fri", "++1w"]
todays = ["2026-06-10", "2026-01-01", "2027-02-28", "2026-12-31"]

# Completing a recurring task: the roll, across cookies, anchors and clocks.
for cookie in cookies:
    for today in todays:
        cases.append(case(f"recur-sched-{cookie}", task(scheduled="2026-06-08", recur=cookie),
                          "state", t("DONE"), today))
        cases.append(case(f"recur-dead-{cookie}", task(deadline="2026-06-08", recur=cookie),
                          "state", t("DONE"), today))
        cases.append(case(f"recur-both-{cookie}",
                          task(scheduled="2026-06-05", deadline="2026-06-08", recur=cookie),
                          "state", t("DONE"), today))

# Timed anchors, zones and a DST spring-forward gap.
timed = [
    task(scheduled="2026-06-08", scheduled_time={"local": "09:30"}, recur=".+1w"),
    task(deadline="2026-06-08", deadline_time={"local": "17:00", "timezone": "Europe/London"},
         recur="+1m"),
    task(scheduled="2026-03-01", scheduled_time={"local": "02:30", "timezone": "America/New_York"},
         recur="+1w"),
    task(scheduled="2026-11-01", scheduled_time={"local": "01:30",
                                                 "timezone": "America/Los_Angeles", "fold": 1},
         recur="+1w"),
    task(scheduled="2026-06-05", scheduled_time={"local": "23:00"},
         deadline="2026-06-08", deadline_time={"local": "07:00"}, recur=".+1w"),
]
for index, rec in enumerate(timed):
    for today in todays:
        cases.append(case(f"timed-{index}", rec, "state", t("DONE"), today))

# What the roll carries forward: body, tags, lead_skip, delegation.
carry = [
    task(scheduled="2026-06-08", recur=".+1w", body="Existing note."),
    task(scheduled="2026-06-08", recur=".+1w", tags=["defer", "@home", "urgent"]),
    task(scheduled="2026-06-08", recur=".+1w", lead="2d", lead_skip="2026-06-08"),
    task(scheduled="2026-06-08", recur=".+1w",
         delegation={"kind": "human", "status": "delegated",
                     "assignee": "sam@example.com", "at": "2026-06-01T10:00:00Z"}),
    task(scheduled="2026-06-08", recur=".+1w",
         delegation={"kind": "agent", "mode": "research", "status": "ready",
                     "at": "2026-06-01T10:00:00Z"}),
    task(scheduled="2026-06-08", recur=".+1w",
         delegation={"kind": "agent", "mode": "implement", "status": "claimed",
                     "assignee": "worker-1", "at": "2026-06-01T10:00:00Z",
                     "work_ref": "https://example.invalid/1"}),
    task(scheduled="2026-06-08", recur=".+1w", priority="A",
         body="Line one.\n- Did [2026-06-01]."),
]
for index, rec in enumerate(carry):
    cases.append(case(f"carry-{index}", rec, "state", t("DONE")))
    cases.append(case(f"carry-{index}-cancel", rec, "state", t("CANCELLED")))

# Rolls that cannot be stored, and cookies that name no reachable date.
edge = [
    (task(scheduled="9999-12-31", recur="+1y"), t("DONE")),
    (task(deadline="9999-12-31", recur="+2d"), t("DONE")),
    (task(scheduled="2027-02-01", recur="2y:02:5fri"), t("DONE")),
    (task(scheduled="2026-06-08", recur="not-a-cookie"), t("DONE")),
]
for index, (rec, value) in enumerate(edge):
    cases.append(case(f"edge-{index}", rec, "state", value))

# Dateless-intent retirement, and the lead/date interaction from both sides.
inter = [
    (task(scheduled="2026-06-08", recur=".+1w"), "scheduled", none()),
    (task(scheduled="2026-06-08", recur=".+1w", lead="2d"), "scheduled", none()),
    (task(scheduled="2026-06-08", deadline="2026-06-20", recur=".+1w"), "scheduled", none()),
    (task(scheduled="2026-06-08", deadline="2026-06-20", recur=".+1w"), "deadline", none()),
    (task(scheduled="2026-06-08", recur=".+1w", lead="2d"), "date_clear", none()),
    (task(deadline="2026-06-08", lead="3w"), "scheduled",
     {"kind": "temporal", "date": "2026-06-01", "local": None, "timezone": None, "fold": 0}),
    (task(scheduled="2026-06-08", lead="3w"), "deadline",
     {"kind": "temporal", "date": "2026-06-20", "local": None, "timezone": None, "fold": 0}),
    (task(state="INBOX"), "scheduled",
     {"kind": "temporal", "date": "2026-06-20", "local": "08:00", "timezone": "Etc/UTC", "fold": 0}),
    (task(scheduled="2026-06-08", recur=".+1w", lead="2d", lead_skip="2026-06-08"),
     "activate", b(True)),
    (task(scheduled="2026-08-08", tags=["defer"]), "activate", b(True)),
    (task(scheduled="2026-08-08", scheduled_time={"local": "09:00"}, tags=["defer"]),
     "activate", b(True)),
    (task(deadline="2026-08-08", lead="1w"), "activate", b(True)),
    (task(scheduled="2026-06-08", recur=".+1w"), "recurrence", t("m:15")),
    (task(scheduled="2026-06-08", recur=".+1w"), "recurrence", none()),
    (task(scheduled="2026-06-08", deadline="2026-06-20"), "lead", t("1w")),
    (task(deadline="0001-01-05"), "lead", t("9999y")),
    (task(deadline="2026-06-20", lead="3w"), "lead", none()),
]
for index, (rec, field, value) in enumerate(inter):
    for today in ["2026-06-10", "2026-09-01"]:
        cases.append(case(f"inter-{index}", rec, field, value, today))

json.dump(cases, open(os.path.join(SCRATCH, "recurrence.json"), "w"), indent=1)
print(len(cases))
