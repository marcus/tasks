#!/usr/bin/env python3
"""Differential harness: one field patch, two implementations, byte diff.

Copies a fixture store into two isolated temp dirs, applies the same change
through Ruby's Store and the Go store, and compares the typed outcome, the
resulting tasks.jsonl bytes and the journal tree byte for byte.
"""
import json, os, shutil, subprocess, sys, tempfile

ROOT = sys.argv[1] if len(sys.argv) > 1 else os.getcwd()
CASES = sys.argv[2] if len(sys.argv) > 2 else "fields.json"
SCRATCH = os.path.dirname(os.path.abspath(__file__))
GOBIN = os.environ.get("PATCH_DIFF_PROBE", os.path.join(SCRATCH, "patch-diff-probe"))
NOW = "2026-06-10T12:00:00Z"
TODAY = "2026-06-10"
DEVICE = "diffdev"


def prepare(case):
    root = tempfile.mkdtemp(prefix="patchdiff-")
    for side in ("ruby", "go"):
        store = os.path.join(root, side)
        os.makedirs(store)
        target = os.path.join(store, "tasks.jsonl")
        if "records" in case:
            with open(target, "w") as handle:
                handle.write("".join(json.dumps(r, separators=(",", ":")) + "\n"
                                     for r in case["records"]))
        else:
            shutil.copyfile(
                os.path.join(ROOT, "porting/fixtures/valid", case["fixture"], "store/tasks.jsonl"),
                target)
        os.makedirs(os.path.join(store, "journal"))
    return root


def spec_for(root, side, case):
    store = os.path.join(root, side)
    return {
        "org": os.path.join(store, "tasks.jsonl"),
        "archive": os.path.join(store, "archive.jsonl"),
        "journal": os.path.join(store, "journal"),
        "device": DEVICE, "now": NOW, "today": case.get("today", TODAY),
        "id": case["id"], "field": case["field"], "label": case.get("label", ""),
        "value": case["value"],
    }


def tree(root):
    out = {}
    for base, _, files in os.walk(root):
        for name in files:
            path = os.path.join(base, name)
            body = open(path, "rb").read().replace(root.encode(), b"<STORE>")
            out[os.path.relpath(path, root)] = body
    return out


def norm(text):
    try:
        return json.dumps(json.loads(text), sort_keys=True)
    except Exception:
        return text


def run(case):
    root = prepare(case)
    out = {}
    for side in ("ruby", "go"):
        spec = spec_for(root, side, case)
        path = os.path.join(root, f"{side}-spec.json")
        with open(path, "w") as handle:
            json.dump(spec, handle)
        argv = (["ruby", os.path.join(SCRATCH, "ruby_driver.rb"), path, os.path.join(ROOT, "lib")]
                if side == "ruby" else [GOBIN, path])
        proc = subprocess.run(argv, capture_output=True, text=True)
        out[side] = {"stdout": proc.stdout.strip(), "stderr": proc.stderr.strip()}

    problems = []
    if norm(out["ruby"]["stdout"]) != norm(out["go"]["stdout"]):
        problems.append(f"outcome: ruby={out['ruby']['stdout']!r} go={out['go']['stdout']!r} "
                        f"rubyerr={out['ruby']['stderr'][:300]!r} goerr={out['go']['stderr'][:200]!r}")
    for name in ("tasks.jsonl", "archive.jsonl"):
        left = os.path.join(root, "ruby", name)
        right = os.path.join(root, "go", name)
        lb = open(left, "rb").read() if os.path.exists(left) else None
        rb = open(right, "rb").read() if os.path.exists(right) else None
        if lb != rb:
            ll = (lb or b"").split(b"\n")
            rl = (rb or b"").split(b"\n")
            delta = [f"  R {x!r}" for x in ll if x not in rl] + [f"  G {x!r}" for x in rl if x not in ll]
            problems.append(f"{name} differs:\n" + "\n".join(delta[:6]))
    lj = tree(os.path.join(root, "ruby", "journal"))
    gj = tree(os.path.join(root, "go", "journal"))
    lj = {k: v.replace(b"/ruby/", b"/SIDE/") for k, v in lj.items()}
    gj = {k: v.replace(b"/go/", b"/SIDE/") for k, v in gj.items()}
    if sorted(lj) != sorted(gj):
        problems.append(f"journal listing: ruby={sorted(lj)} go={sorted(gj)}")
    else:
        for name in sorted(lj):
            if lj[name] == gj[name]:
                continue
            if norm(lj[name].decode("utf-8", "replace")) == norm(gj[name].decode("utf-8", "replace")):
                continue
            a, b = lj[name], gj[name]
            i = 0
            while i < min(len(a), len(b)) and a[i] == b[i]:
                i += 1
            problems.append(f"journal/{name} differs at {i}:\n  R {a[i-60:i+200]!r}\n  G {b[i-60:i+200]!r}")
    if not problems:
        shutil.rmtree(root)
    return problems, root


def main():
    cases = json.load(open(os.path.join(SCRATCH, CASES)))
    failures = 0
    for case in cases:
        problems, root = run(case)
        value = case["value"]
        label = (f"{case.get('fixture', case.get('name', 'inline'))}/{case['id']}/{case['field']}/"
                 f"{value.get('kind')}="
                 f"{value.get('text', value.get('list', value.get('bool', value.get('date', ''))))}")
        if problems:
            failures += 1
            print(f"FAIL {label}  ({root})")
            for problem in problems:
                print("   " + problem.replace("\n", "\n   "))
        else:
            print(f"ok   {label}")
    print(f"\n{len(cases) - failures}/{len(cases)} identical")
    return 1 if failures else 0


if __name__ == "__main__":
    sys.exit(main())
