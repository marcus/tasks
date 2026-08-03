package query

import (
	"strings"
	"testing"
)

// Symbol#inspect and String#inspect do not share an escape vocabulary: an
// unnamed C0 or DEL character is `\xNN` in a Symbol and `\uNNNN` in a String.
// The named escapes are shared and must stay shared.
func TestInspectSymbolUsesTheHexEscapeForControlCharacters(t *testing.T) {
	cases := []struct{ name, want string }{
		{"\x01", `:"\x01"`},
		{"\x7f", `:"\x7F"`},
		{"a\x00b", `:"a\x00b"`},
		{"\n\t\a\v\b\f\r\x1b", `:"\n\t\a\v\b\f\r\e"`},
	}
	for _, testCase := range cases {
		if got := InspectSymbol(testCase.name); got != testCase.want {
			t.Errorf("InspectSymbol(%q) = %s, want %s", testCase.name, got, testCase.want)
		}
	}
}

// A bare symbol admits a non-ASCII character only where String#inspect would
// print it unescaped — plus U+0085, which a String escapes and a Symbol prints
// bare. The sigil branches are governed by the same rule.
func TestBareSymbolNameGatesNonASCIIOnPrintability(t *testing.T) {
	bare := []string{
		"é", "αβ", "→", "①", "あ", "\U0001f600",
		"\u00ad", "\u200b", "\ufeff", "\U0001d173",
		"\u0085", "a\u0085", "\u0085a", "@é", "@@é", "$é",
	}
	for _, name := range bare {
		if got := InspectSymbol(name); got != ":"+name {
			t.Errorf("InspectSymbol(%q) = %s, want the bare form", name, got)
		}
	}
	quoted := []string{
		"\u0080", "\u2028", "\ufffe", "\u0378", "@\u0080", "@@\u0080", "$\u0080",
	}
	for _, name := range quoted {
		if got := InspectSymbol(name); !strings.HasPrefix(got, `:"`) {
			t.Errorf("InspectSymbol(%q) = %s, want the quoted form", name, got)
		}
	}
}

// Ruby's one-character globals are a fixed twenty-character set, not "any one
// character after `$`". The digit and identifier branches are unaffected.
func TestGlobalNameUsesTheFixedSpecialSet(t *testing.T) {
	for _, character := range specialGlobals {
		name := "$" + string(character)
		if got := InspectSymbol(name); got != ":"+name {
			t.Errorf("InspectSymbol(%q) = %s, want the bare form", name, got)
		}
	}
	for _, character := range " #%()-[]^{|}" {
		name := "$" + string(character)
		if got := InspectSymbol(name); !strings.HasPrefix(got, `:"`) {
			t.Errorf("InspectSymbol(%q) = %s, want the quoted form", name, got)
		}
	}
	for _, name := range []string{"$1", "$99", "$foo", "$_"} {
		if got := InspectSymbol(name); got != ":"+name {
			t.Errorf("InspectSymbol(%q) = %s, want the bare form", name, got)
		}
	}
	if got := InspectSymbol("$"); got != `:"$"` {
		t.Errorf(`InspectSymbol("$") = %s, want :"$"`, got)
	}
}

// `$-X` is bare for exactly one identifier character after the dash, non-ASCII
// admitted on the same printability rule a solo bare symbol uses. Length is
// strict in both directions: `$-` alone and `$-ab` quote.
func TestDashGlobalNameTakesExactlyOneIdentifierCharacter(t *testing.T) {
	bare := []string{
		"$-w", "$-Z", "$-0", "$-9", "$-_",
		"$-é", "$-À", "$-あ", "$-\U0001f600", "$-\u0085", "$-\u200b",
	}
	for _, name := range bare {
		if got := InspectSymbol(name); got != ":"+name {
			t.Errorf("InspectSymbol(%q) = %s, want the bare form", name, got)
		}
	}
	quoted := []string{
		"$-", "$--", "$-!", "$- ", "$-.", "$-$", "$-@", "$-\x01", "$-\x7f",
		"$-ww", "$-_x", "$-\u0080", "$-͸",
	}
	for _, name := range quoted {
		if got := InspectSymbol(name); !strings.HasPrefix(got, `:"`) {
			t.Errorf("InspectSymbol(%q) = %s, want the quoted form", name, got)
		}
	}
}

// A digit global is bare as `$0` — a member of Ruby's punct set, which ends the
// name after one character — or as digits led by 1-9. A leading zero in a
// longer name quotes.
func TestDigitGlobalNameRejectsALeadingZero(t *testing.T) {
	for _, name := range []string{"$0", "$1", "$9", "$10", "$19", "$90", "$190"} {
		if got := InspectSymbol(name); got != ":"+name {
			t.Errorf("InspectSymbol(%q) = %s, want the bare form", name, got)
		}
	}
	for _, name := range []string{"$00", "$01", "$09", "$0123", "$0a", "$1a"} {
		if got := InspectSymbol(name); !strings.HasPrefix(got, `:"`) {
			t.Errorf("InspectSymbol(%q) = %s, want the quoted form", name, got)
		}
	}
}
