package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tasks-go/internal/record"
)

func TestSnapshotBuildsDefensiveItemsAndSourceIndexes(t *testing.T) {
	live := parseFixture(t, "malformed/wrong-types/store/tasks.jsonl")
	archive := parseFixture(t, "valid/archive-pair/store/archive.jsonl")
	snapshot := NewSnapshot(live.Records, archive.Records)

	items := snapshot.Items()
	if got, want := len(items), 19; got != want {
		t.Fatalf("live item count = %d, want %d", got, want)
	}
	integerID := items[0]
	if integerID.ID == nil || *integerID.ID != "12345678" {
		t.Fatalf("integer id = %v, want string 12345678", integerID.ID)
	}
	if got := items[4].Tags; len(got) != 0 {
		t.Fatalf("non-array tags = %#v, want empty", got)
	}
	if got := items[5].Tags; len(got) != 2 || got[1] != "7" {
		t.Fatalf("mixed tags = %#v, want [@home 7]", got)
	}
	if items[6].Scheduled != nil || items[7].Deadline != nil {
		t.Fatalf("invalid dates must remain nil: %#v %#v", items[6].Scheduled, items[7].Deadline)
	}
	if got, ok := snapshot.ItemByID(Archive, "a0000102"); !ok || got.Source != Archive {
		t.Fatalf("archive id index = %#v, %v", got, ok)
	}
}

func FuzzSnapshotAccessorsKeepMalformedJSONPrivate(f *testing.F) {
	for _, seed := range []string{"value", "nested", "\U0001f4cb"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		recordJSON, err := json.Marshal(map[string]any{
			"type":  "task",
			"id":    "private1",
			"state": map[string]any{"nested": []any{value}},
			"title": []any{map[string]any{"value": value}},
		})
		if err != nil {
			t.Fatal(err)
		}
		parsed := record.Parse(recordJSON)
		if !parsed.OK() {
			t.Fatalf("parse generated record: %#v", parsed.Errors)
		}

		snapshot := NewSnapshot(parsed.Records, nil)
		item := snapshot.Items()[0]
		item.State.(map[string]any)["nested"].([]any)[0] = "changed"
		item.Title.([]any)[0].(map[string]any)["value"] = "changed"

		again := snapshot.Items()[0]
		if got := again.State.(map[string]any)["nested"].([]any)[0]; got != value {
			t.Fatalf("state mutated through accessor = %#v, want %#v", got, value)
		}
		if got := again.Title.([]any)[0].(map[string]any)["value"]; got != value {
			t.Fatalf("title mutated through accessor = %#v, want %#v", got, value)
		}
	})
}

func TestSnapshotAccessorsDoNotPermitMutation(t *testing.T) {
	live := parseFixture(t, "valid/full-field-matrix/store/tasks.jsonl")
	snapshot := NewSnapshot(live.Records, nil)
	items := snapshot.Items()
	items[3].Tags[0] = "changed"
	items[3].ID = nil

	again := snapshot.Items()[3]
	if got, want := again.Tags[0], "@computer"; got != want {
		t.Fatalf("snapshot tag mutated to %q, want %q", got, want)
	}
	if again.ID == nil || *again.ID != "f0000013" {
		t.Fatalf("snapshot id mutated: %#v", again.ID)
	}
}

func TestSnapshotReadsDatesAndDoesNotCrossSources(t *testing.T) {
	live := parseFixture(t, "valid/full-field-matrix/store/tasks.jsonl")
	archive := parseFixture(t, "valid/archive-pair/store/archive.jsonl")
	snapshot := NewSnapshot(live.Records, archive.Records)

	item, ok := snapshot.ItemByID(Live, "f0000026")
	if !ok || item.Scheduled == nil || item.Scheduled.Format("2006-01-02") != "2028-02-29" {
		t.Fatalf("live leap-day item = %#v, %v", item, ok)
	}
	if _, ok := snapshot.ItemByID(Live, "a0000102"); ok {
		t.Fatal("archive item appeared in live index")
	}
}

func parseFixture(t *testing.T, path string) record.Result {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", "..", "porting", "fixtures", path))
	if err != nil {
		t.Fatal(err)
	}
	result := record.Parse(contents)
	if !result.OK() {
		t.Fatalf("parse fixture %s: %#v", path, result.Errors)
	}
	return result
}
