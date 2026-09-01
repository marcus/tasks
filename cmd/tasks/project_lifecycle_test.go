package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const projectLifecycleFixture = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Projects"}
{"type":"section","id":"aaaa0002","parent":"aaaa0001","title":"Active project"}
{"type":"task","id":"aaaa0003","parent":"aaaa0002","state":"NEXT","title":"Ship it","tags":["defer"],"recur":"+1w"}
{"type":"section","id":"aaaa0004","parent":"aaaa0001","state":"DONE","title":"Finished project","closed":"2026-09-01"}
`

func projectJSON(t *testing.T, result cliResult) map[string]any {
	t.Helper()
	if result.status != 0 {
		t.Fatalf("exit %d stderr=%q stdout=%q", result.status, result.stderr, result.stdout)
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(result.stdout), &value); err != nil {
		t.Fatalf("parse %q: %v", result.stdout, err)
	}
	return value
}

func TestCLIProjectListingsFilterClosedProjects(t *testing.T) {
	dir := seedStore(t, projectLifecycleFixture)
	if got := runCLI(t, dir, "projects").stdout; strings.Contains(got, "Finished project") {
		t.Fatalf("default listing exposed closed project:\n%s", got)
	}

	all := runCLI(t, dir, "projects", "--all", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(all.stdout), &rows); err != nil || all.status != 0 {
		t.Fatalf("all: status=%d parse=%v stdout=%q stderr=%q", all.status, err, all.stdout, all.stderr)
	}
	if len(rows) != 2 || rows[1]["id"] != "aaaa0004" || rows[1]["state"] != "DONE" ||
		rows[1]["closed"] != "2026-09-01" || rows[1]["stuck"] != false {
		t.Fatalf("all rows = %#v", rows)
	}

	closed := runCLI(t, dir, "projects", "--closed", "--json")
	rows = nil
	if err := json.Unmarshal([]byte(closed.stdout), &rows); err != nil || closed.status != 0 ||
		len(rows) != 1 || rows[0]["id"] != "aaaa0004" {
		t.Fatalf("closed: status=%d rows=%#v err=%v", closed.status, rows, err)
	}
	shown := projectJSON(t, runCLI(t, dir, "project", "show", "Finished", "--json"))
	if shown["state"] != "DONE" {
		t.Fatalf("closed show = %#v", shown)
	}
}

func TestCLIProjectCompleteAndUndoMoveSectionAndTaskTogether(t *testing.T) {
	dir := seedStore(t, projectLifecycleFixture)
	completed := runCLI(t, dir, "project", "complete", "Active project")
	if completed.status != 0 || !strings.Contains(completed.stdout, "project stamped DONE 2026-07-20") {
		t.Fatalf("complete = %#v", completed)
	}
	project := projectJSON(t, runCLI(t, dir, "project", "show", "aaaa0002", "--json"))
	if project["state"] != "DONE" || project["closed"] != "2026-07-20" {
		t.Fatalf("completed project = %#v", project)
	}

	undone := runCLI(t, dir, "undo")
	if undone.status != 0 || !strings.Contains(undone.stdout, "complete project: aaaa0002") {
		t.Fatalf("undo = %#v", undone)
	}
	project = projectJSON(t, runCLI(t, dir, "project", "show", "aaaa0002", "--json"))
	if _, present := project["state"]; present {
		t.Fatalf("undo left project closed = %#v", project)
	}
	task := projectJSON(t, runCLI(t, dir, "show", "aaaa0003", "--json"))
	if task["state"] != "NEXT" || task["recur"] != "+1w" {
		t.Fatalf("undo left task changed = %#v", task)
	}
}

func TestCLIProjectDropAliasAndReopenKeepTasksCancelled(t *testing.T) {
	dir := seedStore(t, projectLifecycleFixture)
	dropped := runCLI(t, dir, "project", "cancel", "Active project")
	if dropped.status != 0 || !strings.Contains(dropped.stdout, "project stamped CANCELLED 2026-07-20") {
		t.Fatalf("drop = %#v", dropped)
	}
	project := projectJSON(t, runCLI(t, dir, "project", "show", "aaaa0002", "--json"))
	if project["state"] != "CANCELLED" {
		t.Fatalf("dropped project = %#v", project)
	}
	reopened := projectJSON(t, runCLI(t, dir, "project", "reopen", "aaaa0002", "--json"))
	if _, present := reopened["state"]; present {
		t.Fatalf("reopened project still stamped = %#v", reopened)
	}
	done := runCLI(t, dir, "list", "--done", "--json")
	var tasks []map[string]any
	if err := json.Unmarshal([]byte(done.stdout), &tasks); err != nil || done.status != 0 {
		t.Fatalf("done list: status=%d parse=%v stdout=%q", done.status, err, done.stdout)
	}
	if len(tasks) != 1 || tasks[0]["id"] != "aaaa0003" || tasks[0]["state"] != "CANCELLED" {
		t.Fatalf("reopen changed task = %#v", tasks)
	}
}

func TestCLIHelpPublishesProjectLifecycleVerbs(t *testing.T) {
	dir := seedStore(t, projectLifecycleFixture)
	text := runCLI(t, dir, "help")
	for _, want := range []string{"project drop <ref>", "project reopen <ref>", "--closed / --all"} {
		if !strings.Contains(text.stdout, want) {
			t.Errorf("human help missing %q", want)
		}
	}
	registry := runCLI(t, dir, "help", "--json")
	for _, want := range []string{`"name":"project drop"`, `"name":"project reopen"`} {
		if !strings.Contains(registry.stdout, want) {
			t.Errorf("help JSON missing %q: %s", want, registry.stdout)
		}
	}
}
