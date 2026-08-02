package main

import (
	"testing"

	"tasks-go/internal/query"
)

func TestDecodeFilterOptionsDistinguishesNullScopeFromOmission(t *testing.T) {
	omitted, err := decodeFilterOptions([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if omitted.Scope != nil {
		t.Fatalf("omitted scope = %q, want nil", *omitted.Scope)
	}

	explicitNull, err := decodeFilterOptions([]byte(`{"scope":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if explicitNull.Scope == nil || *explicitNull.Scope != "" {
		t.Fatalf("explicit null scope = %#v, want explicit empty string", explicitNull.Scope)
	}
	if _, err := query.NewFilter(explicitNull); err == nil || err.Error() != "unknown task scope: " {
		t.Fatalf("NewFilter(explicit null) error = %v", err)
	}
}

func TestDecodeFilterOptionsRejectsUnknownKeywordsInGivenOrder(t *testing.T) {
	// Ruby raises before initialize's body, so an unknown keyword outranks the
	// domain error a sibling keyword would otherwise produce.
	cases := []struct{ kwargs, want string }{
		{`{"nope":1}`, "unknown keyword: :nope"},
		{`{"nope":1,"also":2}`, "unknown keywords: :nope, :also"},
		{`{"also":2,"nope":1}`, "unknown keywords: :also, :nope"},
		{`{"scope":"done","nope":1}`, "unknown keyword: :nope"},
		{`{"state":"BLOCKED","nope":1}`, "unknown keyword: :nope"},
		{`{"a b":1}`, `unknown keyword: :"a b"`},
		{`{"nope":1,"nope":2}`, "unknown keyword: :nope"},
	}
	for _, testCase := range cases {
		_, err := decodeFilterOptions([]byte(testCase.kwargs))
		if err == nil || err.Error() != testCase.want {
			t.Errorf("decodeFilterOptions(%s) error = %v, want %q", testCase.kwargs, err, testCase.want)
		}
	}
	if _, err := decodeFilterOptions([]byte(`{"scope":"done"}`)); err != nil {
		t.Errorf("decodeFilterOptions with only known keywords = %v", err)
	}
}
