package query

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// decode mirrors the JSON boundary the probe uses: generic shapes with numbers
// preserved as json.Number.
func decode(t *testing.T, document string) any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(document))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode(%s): %v", document, err)
	}
	return value
}

func TestCoerceStringsMatchesRubyArrayToS(t *testing.T) {
	cases := []struct {
		document string
		want     []string
	}{
		{`null`, []string{}},
		{`"@work"`, []string{"@work"}},
		{`["@work"]`, []string{"@work"}},
		{`[]`, []string{}},
		{`1`, []string{"1"}},
		{`true`, []string{"true"}},
		// nil.to_s is "", not "nil": the recorded oracle shows an empty string.
		{`["@work",1,true,null,{"key":"value"}]`, []string{"@work", "1", "true", "", `{"key" => "value"}`}},
		{`[[1,"two",null]]`, []string{`[1, "two", nil]`}},
		{`[{}]`, []string{"{}"}},
		// Kernel#Array on a Hash yields its [key, value] pairs.
		{`{"a":1,"b":null}`, []string{`["a", 1]`, `["b", nil]`}},
	}
	for _, testCase := range cases {
		if got := CoerceStrings(decode(t, testCase.document)); !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("CoerceStrings(%s) = %#v, want %#v", testCase.document, got, testCase.want)
		}
	}
}

func TestCoerceStringsInspectsStringsInsideCollections(t *testing.T) {
	cases := []struct {
		document string
		want     string
	}{
		{`[{"key":"a\"b"}]`, `{"key" => "a\"b"}`},
		{`[{"key":"a\\b"}]`, `{"key" => "a\\b"}`},
		{`[{"key":"line\nbreak"}]`, `{"key" => "line\nbreak"}`},
		{`[{"key":"tab\there"}]`, `{"key" => "tab\there"}`},
		{`[{"key":"\u0001"}]`, `{"key" => "\x01"}`},
		{`[{"key":"#{x}"}]`, `{"key" => "\#{x}"}`},
		{`[{"key":"plain # hash"}]`, `{"key" => "plain # hash"}`},
		{`[{"key":"héllo"}]`, `{"key" => "héllo"}`},
	}
	for _, testCase := range cases {
		got := CoerceStrings(decode(t, testCase.document))
		if len(got) != 1 || got[0] != testCase.want {
			t.Errorf("CoerceStrings(%s) = %#v, want [%q]", testCase.document, got, testCase.want)
		}
	}
}

// A top-level string is one element, never one element per character: Ruby's
// Array("abc") is ["abc"].
func TestCoerceStringsNeverSplitsScalars(t *testing.T) {
	for _, document := range []string{`"abc"`, `"@a @b"`, `12`, `false`} {
		if got := CoerceStrings(decode(t, document)); len(got) != 1 {
			t.Errorf("CoerceStrings(%s) = %#v, want one element", document, got)
		}
	}
}

func TestCoerceStringsFeedsFilterConstruction(t *testing.T) {
	filter, err := NewFilter(FilterOptions{
		Contexts: CoerceStrings(decode(t, `"@work"`)),
		Text:     CoerceStrings(decode(t, `["Plan",3]`)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := filter.Contexts(); !reflect.DeepEqual(got, []string{"@work"}) {
		t.Fatalf("Contexts() = %#v", got)
	}
	if got := filter.TextQuery(); got != "plan 3" {
		t.Fatalf("TextQuery() = %q", got)
	}
}
