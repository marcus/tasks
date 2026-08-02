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
