// Command patch-diff-probe applies ONE field patch through the Go store and
// prints the typed outcome, so a caller can run it beside the Ruby driver over
// identical fixtures and diff the outcome, the store bytes and the journal.
//
// It exists because Wave 1's real defects were all found this way — by running
// two implementations over the same bytes and comparing — and the CLI verbs
// that would otherwise drive these fields belong to another packet. The probe
// is the seam that lets the write path be compared before its commands exist.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
)

type spec struct {
	Org      string     `json:"org"`
	Archive  string     `json:"archive"`
	Journal  string     `json:"journal"`
	Device   string     `json:"device"`
	Now      string     `json:"now"`
	Today    string     `json:"today"`
	ID       string     `json:"id"`
	Field    string     `json:"field"`
	Label    string     `json:"label"`
	Value    valueSpec  `json:"value"`
	Verb     string     `json:"verb"`
	Worker   string     `json:"worker"`
	Force    bool       `json:"force"`
	WorkRef  string     `json:"work_ref"`
	Expected *valueSpec `json:"expected_override"`
}

type valueSpec struct {
	Kind     string   `json:"kind"`
	Text     string   `json:"text"`
	Bool     bool     `json:"bool"`
	List     []string `json:"list"`
	Add      []string `json:"add"`
	Remove   []string `json:"remove"`
	Date     string   `json:"date"`
	Local    string   `json:"local"`
	Timezone string   `json:"timezone"`
	Fold     int      `json:"fold"`
}

func (v valueSpec) patchValue() (store.PatchValue, error) {
	switch v.Kind {
	case "none":
		return store.NoValue(), nil
	case "text":
		return store.TextValue(v.Text), nil
	case "bool":
		return store.BoolValue(v.Bool), nil
	case "list":
		return store.ListValue(v.List), nil
	case "tag_delta":
		return store.TagDeltaValue(v.Add, v.Remove), nil
	case "date", "temporal":
		date, ok := temporal.ParseDate(v.Date)
		if !ok {
			return store.PatchValue{}, fmt.Errorf("bad date %q", v.Date)
		}
		value, err := temporal.NewValue(date, v.Local, v.Timezone, v.Fold, false)
		if err != nil {
			return store.PatchValue{}, err
		}
		return store.TemporalValue(value), nil
	}
	return store.PatchValue{}, fmt.Errorf("unknown value kind %q", v.Kind)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: patch-diff-probe <spec.json>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	var request spec
	if err := json.Unmarshal(raw, &request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	now, err := time.Parse(time.RFC3339, request.Now)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	value, err := request.Value.patchValue()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	writer := store.NewWriter(request.Org, request.Archive, store.Options{
		JournalDir: request.Journal, Device: request.Device,
		Now:           func() time.Time { return now.UTC() },
		IDSource:      func() string { return "aaaaaaaa" },
		CoalesceScope: "pinned-scope", MaxDepth: 5,
	})
	var result store.MutationResult
	switch request.Verb {
	case "undelegate":
		result = writer.Undelegate(request.ID, "")
	case "release":
		result = writer.Release(request.ID, request.Worker, request.Force, "")
	case "work_ref":
		result = writer.SetWorkRef(request.ID, request.WorkRef, request.Worker, "")
	default:
		field := store.PatchField(request.Field)
		expected, _ := writer.ExpectedFor(request.ID, field)
		result = writer.Patch(store.PatchRequest{
			ID: request.ID, Field: field, Value: value, Expected: expected,
			Label: request.Label, Today: request.Today,
		})
	}
	errors := result.Errors
	if errors == nil {
		errors = []string{}
	}
	out, _ := json.Marshal(map[string]any{
		"status": string(result.Status), "errors": errors, "rolled_back": result.RolledBack,
	})
	fmt.Println(string(out))
}
