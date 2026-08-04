package query

import (
	"reflect"
	"testing"
)

// decode mirrors the JSON boundary the probe uses: the package's own
// order-preserving decoder, not encoding/json's generic decode.
func decode(t *testing.T, document string) any {
	t.Helper()
	value, err := DecodeValue([]byte(document))
	if err != nil {
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
		{`[{"key":"\u0001"}]`, `{"key" => "\u0001"}`},
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

func TestCoerceBoolIsRubyTruthiness(t *testing.T) {
	// Only nil and false are false in Ruby; `0` and `""` are true, which is the
	// whole reason a JSON boolean decode cannot stand in for `!!value`.
	cases := []struct {
		document string
		want     bool
	}{
		{"null", false},
		{"false", false},
		{"true", true},
		{"0", true},
		{`""`, true},
		{`"yes"`, true},
		{"[]", true},
		{"{}", true},
	}
	for _, testCase := range cases {
		if got := CoerceBool(decode(t, testCase.document)); got != testCase.want {
			t.Errorf("CoerceBool(%s) = %t, want %t", testCase.document, got, testCase.want)
		}
	}
}

func TestCoerceStringMatchesRubyToS(t *testing.T) {
	cases := []struct {
		document string
		want     string
	}{
		{"null", ""},
		{`"a"`, "a"},
		{"5", "5"},
		{"true", "true"},
		{"false", "false"},
		{`["a",1]`, `["a", 1]`},
		{`{"key":"value"}`, `{"key" => "value"}`},
	}
	for _, testCase := range cases {
		if got := CoerceString(decode(t, testCase.document)); got != testCase.want {
			t.Errorf("CoerceString(%s) = %q, want %q", testCase.document, got, testCase.want)
		}
	}
}

func TestCoercedScalarsReachTheDomainRule(t *testing.T) {
	// A coerced non-String value must be rejected by the ported domain message,
	// the slice's observable output, rather than by a decoder.
	priority := CoerceString(decode(t, "5"))
	if _, err := NewFilter(FilterOptions{Priority: &priority}); err == nil ||
		err.Error() != "priority must be A, B, C, or none" {
		t.Errorf("NewFilter(priority: 5) error = %v", err)
	}
	scope := CoerceString(decode(t, "5"))
	if _, err := NewFilter(FilterOptions{Scope: &scope}); err == nil ||
		err.Error() != "unknown task scope: 5" {
		t.Errorf("NewFilter(scope: 5) error = %v", err)
	}
}

func TestInspectSymbolMatchesRubySymbolInspect(t *testing.T) {
	// Every expectation is a captured `Symbol#inspect` result, not a guess:
	// see the compatibility evidence archived at ruby-final-2026-08-04.
	cases := []struct{ name, want string }{
		{"nope", ":nope"}, {"a_b9", ":a_b9"}, {"_x", ":_x"}, {"_", ":_"},
		{"A", ":A"}, {"CONST", ":CONST"}, {"end", ":end"}, {"nil", ":nil"},
		{"a?", ":a?"}, {"a!", ":a!"}, {"a=", ":a="},
		{"é", ":é"}, {"é?", ":é?"}, {"αβ", ":αβ"},
		{"+", ":+"}, {"[]=", ":[]="}, {"+@", ":+@"}, {"`", ":`"},
		{"~@", `:"~@"`}, {"!@", `:"!@"`},
		{"@x", ":@x"}, {"@@x", ":@@x"}, {"$x", ":$x"}, {"$1", ":$1"},
		{"$12", ":$12"}, {"$!", ":$!"}, {"$;", ":$;"},
		{"@1", `:"@1"`}, {"@@1", `:"@@1"`}, {"@", `:"@"`}, {"@@", `:"@@"`},
		{"$", `:"$"`}, {"@a?", `:"@a?"`}, {"a?=", `:"a?="`},
		{"a b", `:"a b"`}, {"a-b", `:"a-b"`}, {"a.b", `:"a.b"`},
		{"A::B", `:"A::B"`}, {"1a", `:"1a"`}, {"", `:""`},
		{"tab\there", `:"tab\there"`}, {`quote"q`, `:"quote\"q"`},
		{`back\s`, `:"back\\s"`},
	}
	for _, testCase := range cases {
		if got := InspectSymbol(testCase.name); got != testCase.want {
			t.Errorf("InspectSymbol(%q) = %s, want %s", testCase.name, got, testCase.want)
		}
	}
}
