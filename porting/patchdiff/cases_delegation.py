"""Third differential batch: the three delegation verbs the application seam
asked for — undelegate, release, and the work reference."""
import json, os
SCRATCH = os.path.dirname(os.path.abspath(__file__))

META = {"type": "meta", "version": 2}
SEC = {"type": "section", "id": "aa000001", "title": "Inbox"}


def task(**kw):
    base = {"type": "task", "id": "aa000010", "parent": "aa000001", "state": "TODO",
            "title": "Delegated work"}
    base.update(kw)
    return base


markers = {
    "none": None,
    "human": {"kind": "human", "status": "delegated", "assignee": "sam@example.com",
              "at": "2026-06-01T10:00:00Z"},
    "ready": {"kind": "agent", "mode": "research", "status": "ready",
              "at": "2026-06-01T10:00:00Z"},
    "claimed": {"kind": "agent", "mode": "implement", "status": "claimed",
                "assignee": "worker-1", "at": "2026-06-01T10:00:00Z"},
    "claimed-ref": {"kind": "agent", "mode": "implement", "status": "claimed",
                    "assignee": "worker-1", "at": "2026-06-01T10:00:00Z",
                    "work_ref": "https://example.invalid/1"},
    "human-ref": {"kind": "human", "status": "delegated", "assignee": "sam@example.com",
                  "at": "2026-06-01T10:00:00Z", "work_ref": "https://example.invalid/2"},
}
states = ["TODO", "WAITING", "DONE", "PROPOSED"]

cases = []
for name, marker in markers.items():
    for state in states:
        fields = {"state": state}
        if state == "DONE":
            fields["closed"] = "2026-06-01"
        if marker:
            fields["delegation"] = marker
        rec = task(**fields)
        common = {"records": [META, SEC, rec], "id": rec["id"], "field": "state",
                  "value": {"kind": "none"}}
        cases.append(dict(common, name=f"undelegate-{name}-{state}", verb="undelegate"))
        for worker, force in [("worker-1", False), ("worker-2", False), ("", True), ("", False)]:
            cases.append(dict(common, name=f"release-{name}-{state}-{worker}-{force}",
                              verb="release", worker=worker, force=force))
        for ref, worker in [("https://example.invalid/9", ""),
                            ("https://example.invalid/9", "worker-1"),
                            ("https://example.invalid/9", "worker-2"),
                            ("", ""), ("", "worker-1"),
                            ("  ", ""), ("a\nb", ""), ("x" * 501, "")]:
            cases.append(dict(common, name=f"workref-{name}-{state}", verb="work_ref",
                              work_ref=ref, worker=worker))

json.dump(cases, open(os.path.join(SCRATCH, "delegation.json"), "w"), indent=1)
print(len(cases))
