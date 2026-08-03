package journal

import (
	"os"
	"path/filepath"
	"testing"
)

// Every kind of journal trouble degrades to an empty history. That is the
// contract the whole feature rests on: tasks.jsonl is the source of truth and
// the journal is convenience state, so a damaged or foreign journal must mean
// "nothing to undo" — never a crash, and never replaying someone else's edits
// over your file.
func TestDamagedHistoriesDegradeToNothingToUndo(t *testing.T) {
	org := filepath.Join(t.TempDir(), "tasks.jsonl")
	if err := os.WriteFile(org, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	canonical := Canonical(org)

	cases := []struct {
		name  string
		index string
	}{
		{"wrong version", `{"version":99,"org":"` + canonical + `","cursor":0,"states":[{"org_sha":null,"archive_sha":null}]}`},
		{"someone else's store", `{"version":1,"org":"/elsewhere/tasks.jsonl","cursor":1,"states":[{"org_sha":null,"archive_sha":null},{"org_sha":null,"archive_sha":null,"label":"x"}]}`},
		{"cursor past the end", `{"version":1,"org":"` + canonical + `","cursor":7,"states":[{"org_sha":null,"archive_sha":null}]}`},
		{"negative cursor", `{"version":1,"org":"` + canonical + `","cursor":-1,"states":[{"org_sha":null,"archive_sha":null}]}`},
		{"malformed sha", `{"version":1,"org":"` + canonical + `","cursor":0,"states":[{"org_sha":"nothex","archive_sha":null}]}`},
		{"not JSON", `{`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(tc.index), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, ok := Open(dir, org).Plan(-1); ok {
				t.Fatalf("planned an undo from a %s journal", tc.name)
			}
		})
	}
}

// A blob that no longer hashes to its own name invalidates the plan rather than
// being restored. Writing unverified bytes over a task file is the one outcome
// worse than refusing to undo.
func TestATamperedBlobInvalidatesThePlan(t *testing.T) {
	org := filepath.Join(t.TempDir(), "tasks.jsonl")
	if err := os.WriteFile(org, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha := "0000000000000000000000000000000000000000000000000000000000000000"
	if err := os.WriteFile(filepath.Join(dir, "blobs", sha), []byte("not what the name says"), 0o644); err != nil {
		t.Fatal(err)
	}
	index := `{"version":1,"org":"` + Canonical(org) + `","cursor":1,"states":[` +
		`{"org_sha":"` + sha + `","archive_sha":null},` +
		`{"org_sha":null,"archive_sha":null,"label":"capture: x"}]}`
	if err := os.WriteFile(filepath.Join(dir, "index.json"), []byte(index), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := Open(dir, org).Plan(-1); ok {
		t.Fatalf("planned an undo whose target blob does not verify")
	}
}
