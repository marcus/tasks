package store

import (
	"encoding/json"
	"testing"

	"github.com/marcus/tasks/internal/record"
)

func TestSnapshotAccessorsReturnDeepCopies(t *testing.T) {
	item := Item{
		ID:            "live",
		Title:         "original",
		AllTags:       []string{"all"},
		Tags:          []string{"tag"},
		Contexts:      []string{"@context"},
		ScheduledTime: json.RawMessage(`{"local":"09:00"}`),
		DeadlineTime:  json.RawMessage(`{"local":"17:00"}`),
		Delegation:    json.RawMessage(`{"to":"Sam"}`),
	}
	parsed := record.Record{Line: 2, Fields: []record.Field{{Key: "title", Value: json.RawMessage(`"original"`)}}}
	snapshot := &Snapshot{
		items:          []Item{item},
		archiveItems:   []Item{item},
		liveRecords:    []record.Record{parsed},
		archiveRecords: []record.Record{parsed},
	}

	assertItemsDetached := func(t *testing.T, first, second func() []Item) {
		t.Helper()
		got := first()
		got[0].Title = "changed"
		got[0].AllTags[0] = "changed"
		got[0].Tags[0] = "changed"
		got[0].Contexts[0] = "changed"
		got[0].ScheduledTime[0] = '!'
		got[0].DeadlineTime[0] = '!'
		got[0].Delegation[0] = '!'
		got[0] = Item{}

		fresh := second()
		if fresh[0].Title != "original" || fresh[0].AllTags[0] != "all" || fresh[0].Tags[0] != "tag" || fresh[0].Contexts[0] != "@context" {
			t.Fatalf("item accessor leaked mutation: %+v", fresh[0])
		}
		if fresh[0].ScheduledTime[0] != '{' || fresh[0].DeadlineTime[0] != '{' || fresh[0].Delegation[0] != '{' {
			t.Fatalf("item raw value leaked mutation: %+v", fresh[0])
		}
	}
	assertItemsDetached(t, snapshot.Items, snapshot.Items)
	assertItemsDetached(t, snapshot.ArchiveItems, snapshot.ArchiveItems)

	assertRecordsDetached := func(t *testing.T, first, second func() []record.Record) {
		t.Helper()
		got := first()
		got[0].Line = 99
		got[0].Fields[0].Key = "changed"
		got[0].Fields[0].Value[0] = '!'
		got[0].Fields = nil

		fresh := second()
		if fresh[0].Line != 2 || fresh[0].Fields[0].Key != "title" || fresh[0].Fields[0].Value[0] != '"' {
			t.Fatalf("record accessor leaked mutation: %+v", fresh[0])
		}
	}
	assertRecordsDetached(t, snapshot.LiveRecords, snapshot.LiveRecords)
	assertRecordsDetached(t, snapshot.ArchiveRecords, snapshot.ArchiveRecords)
}

func TestSnapshotTreeResultsAreDeepCopies(t *testing.T) {
	root := Item{ID: "root", AllTags: []string{"root-tag"}, Delegation: json.RawMessage(`{"to":"Root"}`)}
	child := Item{ID: "child", Parent: "root", HasParent: true, Contexts: []string{"@child"}, DeadlineTime: json.RawMessage(`{"local":"12:00"}`)}
	snapshot := &Snapshot{items: []Item{root, child}}

	roots := snapshot.Roots()
	roots[0].AllTags[0] = "changed"
	roots[0].Delegation[0] = '!'
	children := snapshot.ChildrenOf("root")
	children[0].Contexts[0] = "changed"
	children[0].DeadlineTime[0] = '!'

	freshRoots := snapshot.Roots()
	freshChildren := snapshot.ChildrenOf("root")
	if freshRoots[0].AllTags[0] != "root-tag" || freshRoots[0].Delegation[0] != '{' {
		t.Fatalf("Roots leaked mutation: %+v", freshRoots[0])
	}
	if freshChildren[0].Contexts[0] != "@child" || freshChildren[0].DeadlineTime[0] != '{' {
		t.Fatalf("ChildrenOf leaked mutation: %+v", freshChildren[0])
	}
	if got := snapshot.Items(); got[0].AllTags[0] != "root-tag" || got[1].Contexts[0] != "@child" {
		t.Fatalf("tree result mutated snapshot items: %+v", got)
	}
}

func TestSnapshotCopiesPreserveNilAndEmpty(t *testing.T) {
	zero := &Snapshot{}
	if zero.Items() != nil || zero.ArchiveItems() != nil || zero.LiveRecords() != nil || zero.ArchiveRecords() != nil {
		t.Fatal("nil snapshot slices must stay nil")
	}

	snapshot := &Snapshot{
		items: []Item{{
			AllTags:       nil,
			Tags:          []string{},
			Contexts:      nil,
			ScheduledTime: nil,
			DeadlineTime:  json.RawMessage{},
			Delegation:    json.RawMessage{},
		}},
		archiveItems: []Item{},
		liveRecords: []record.Record{
			{Fields: nil},
			{Fields: []record.Field{}},
			{Fields: []record.Field{{Key: "nil", Value: nil}, {Key: "empty", Value: json.RawMessage{}}}},
		},
		archiveRecords: []record.Record{},
	}
	items := snapshot.Items()
	if items == nil || items[0].AllTags != nil || items[0].Tags == nil || items[0].Contexts != nil {
		t.Fatalf("item slice nil/empty changed: %+v", items[0])
	}
	if items[0].ScheduledTime != nil || items[0].DeadlineTime == nil || items[0].Delegation == nil {
		t.Fatalf("item raw nil/empty changed: %+v", items[0])
	}
	if snapshot.ArchiveItems() == nil || snapshot.ArchiveRecords() == nil {
		t.Fatal("non-nil empty outer slices must stay non-nil")
	}
	records := snapshot.LiveRecords()
	if records[0].Fields != nil || records[1].Fields == nil || records[2].Fields[0].Value != nil || records[2].Fields[1].Value == nil {
		t.Fatalf("record nil/empty changed: %+v", records)
	}
}

func TestSnapshotFieldBaselinesComeFromItsHeldRecords(t *testing.T) {
	target, _ := writerFixture(t, patchFixture)
	snapshot, err := target.ReadSnapshot(false)
	if err != nil {
		t.Fatal(err)
	}
	item := snapshot.Items()[0]
	fields := []PatchField{FieldTitle, FieldState, FieldDeadline, FieldTags}
	baselines, found := snapshot.FieldBaselines(item.ID, fields)
	if !found {
		t.Fatalf("no baselines for %s", item.ID)
	}
	if baselines[FieldTitle] != item.Title {
		t.Fatalf("title baseline = %q, want %q", baselines[FieldTitle], item.Title)
	}
	if _, found := snapshot.FieldBaselines("does-not-exist", fields); found {
		t.Fatal("a missing id returned baselines")
	}
}
