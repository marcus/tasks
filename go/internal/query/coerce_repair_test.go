package query

import (
	"reflect"
	"strings"
	"testing"
	"unicode"
)

// Hash order is observable: Ruby preserves insertion order and JSON.parse
// inserts in document order, so a multi-key Hash renders as it was written.
// Sorting the keys — which a Go map forces — reorders every case below.
func TestObjectRendersInDocumentOrder(t *testing.T) {
	cases := []struct {
		document string
		want     []string
	}{
		{`[{"b":1,"a":2}]`, []string{`{"b" => 1, "a" => 2}`}},
		{`[{"a":2,"b":1}]`, []string{`{"a" => 2, "b" => 1}`}},
		{`{"z":1,"a":2}`, []string{`["z", 1]`, `["a", 2]`}},
		{`[{"z":{"y":1,"x":2}}]`, []string{`{"z" => {"y" => 1, "x" => 2}}`}},
		// Hash#[]= keeps a repeated key in its first position with its last value.
		{`[{"b":1,"a":2,"b":3}]`, []string{`{"b" => 3, "a" => 2}`}},
	}
	for _, testCase := range cases {
		if got := CoerceStrings(decode(t, testCase.document)); !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("CoerceStrings(%s) = %#v, want %#v", testCase.document, got, testCase.want)
		}
	}
}

// Every expectation is a captured Ruby `to_s`, not a guess: see
// porting/evidence/query-filter-parse/review-2026-08-02-coercion/number-to-s-ruby.jsonl.
// The literal's shape picks the class, so `1e2` is a Float and renders `100.0`
// while `100` is an Integer and renders `100`.
func TestNumberElementsMatchRubyToS(t *testing.T) {
	cases := []struct{ document, want string }{
		{`[0]`, "0"}, {`[-0]`, "0"}, {`[100]`, "100"},
		{`[12345678901234567890]`, "12345678901234567890"},
		{`[-12345678901234567890]`, "-12345678901234567890"},
		{`[1e2]`, "100.0"}, {`[1E2]`, "100.0"}, {`[1.0]`, "1.0"},
		{`[0.0]`, "0.0"}, {`[-0.0]`, "-0.0"}, {`[1e0]`, "1.0"},
		// The point may sit three places left of the digits before the layout
		// switches, so 1e-4 is fixed and 1e-5 is not.
		{`[0.0001]`, "0.0001"}, {`[1e-5]`, "1.0e-05"}, {`[-1e-7]`, "-1.0e-07"},
		{`[0.00012345]`, "0.00012345"},
		// At the sixteenth digit the layout stays fixed only while a fractional
		// digit remains: 1e15 has none, 1000000000000000.5 does.
		{`[999999999999999.9]`, "999999999999999.9"},
		{`[1e15]`, "1.0e+15"},
		{`[1000000000000000.5]`, "1000000000000000.5"},
		{`[1234567890123456.7]`, "1234567890123456.8"},
		{`[1.2345678901234568e16]`, "1.2345678901234568e+16"},
		{`[5e-324]`, "5.0e-324"},
		{`[1.7976931348623157e308]`, "1.7976931348623157e+308"},
		{`[6.02e23]`, "6.02e+23"},
		{`[0.30000000000000004]`, "0.30000000000000004"},
	}
	for _, testCase := range cases {
		got := CoerceStrings(decode(t, testCase.document))
		if len(got) != 1 || got[0] != testCase.want {
			t.Errorf("CoerceStrings(%s) = %#v, want [%q]", testCase.document, got, testCase.want)
		}
	}
}

// rubyPrintable carries the whole of String#inspect's escape decision, so its
// shape is worth pinning: a table that silently lost its R32 half would still
// pass every ASCII case above.
func TestRubyPrintableTablePins(t *testing.T) {
	if unicode.Is(rubyPrintable, 0xD800) {
		t.Error("surrogates must not be printable")
	}
	// Ruby's predicate is Onigmo's, not a general-category test: these four
	// format and space characters print raw while these four do not.
	for _, character := range []rune{0x00A0, 0x00AD, 0x200B, 0xFEFF} {
		if !unicode.Is(rubyPrintable, character) {
			t.Errorf("U+%04X should be printable", character)
		}
	}
	for _, character := range []rune{0x0085, 0x2028, 0x2029, 0x0378} {
		if unicode.Is(rubyPrintable, character) {
			t.Errorf("U+%04X should not be printable", character)
		}
	}
	if len(rubyPrintable.R32) == 0 {
		t.Error("R32 is empty: the table lost every codepoint above the BMP")
	}
}

// `\xNN` is Ruby's binary-string rendering and must never appear for the UTF-8
// strings JSON delivers. Above the BMP the escape switches to the braced form,
// and the hex is uppercase in both.
func TestInspectStringUsesTheUnicodeEscapeForms(t *testing.T) {
	cases := []struct{ element, want string }{
		{`"\u0001"`, `"\u0001"`},
		{`"\u0085"`, `"\u0085"`},
		{`"\u007F"`, `"\u007F"`},
		{`"\u0378"`, `"\u0378"`},
		{`"\u00AD"`, "\"\u00AD\""},
		{`"\u200B"`, "\"\u200B\""},
		{`"𐀌"`, `"\u{1000C}"`},
		{`"\uDBFF\uDFFD"`, "\"\U0010FFFD\""},
	}
	for _, testCase := range cases {
		got := CoerceStrings(decode(t, `[[`+testCase.element+`]]`))
		want := "[" + testCase.want + "]"
		if len(got) != 1 || got[0] != want {
			t.Errorf("CoerceStrings([[%s]]) = %#v, want [%q]", testCase.element, got, want)
		}
		if strings.Contains(got[0], `\x`) {
			t.Errorf("CoerceStrings([[%s]]) emitted the binary \\xNN form", testCase.element)
		}
	}
}
