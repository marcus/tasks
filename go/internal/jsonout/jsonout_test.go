package jsonout

import "testing"

// The whole reason this package exists is that encoding/json cannot produce
// Ruby's bytes: it sorts object keys, escapes <, > and &, and escapes U+2028
// and U+2029. Every one of those would change a document compared byte for byte
// against the oracle.
func TestWritesRubyJSONGenerateBytes(t *testing.T) {
	w := New()
	w.BeginObject()
	w.KeyStr("z", "first")
	w.KeyStr("a", "second")
	w.KeyStr("html", "<a href=\"x\">&amp;</a>")
	w.KeyStr("separators", "line para ")
	w.KeyStr("control", "tab\there\nand\\slash")
	w.KeyNull("absent")
	w.KeyBool("flag", true)
	w.KeyInt("count", 3)
	w.Key("list")
	w.Strings([]string{"one", "two"})
	w.Key("nested")
	w.BeginObject()
	w.KeyStrOrNull("empty", "")
	w.EndObject()
	w.EndObject()

	want := `{"z":"first","a":"second","html":"<a href=\"x\">&amp;</a>",` +
		"\"separators\":\"line para \"," +
		`"control":"tab\there\nand\\slash","absent":null,"flag":true,"count":3,` +
		`"list":["one","two"],"nested":{"empty":null}}`
	if got := w.String(); got != want {
		t.Fatalf("document =\n%s\nwant\n%s", got, want)
	}
}

func TestEmptyContainers(t *testing.T) {
	w := New()
	w.BeginArray()
	w.BeginObject()
	w.EndObject()
	w.BeginArray()
	w.EndArray()
	w.EndArray()
	if got, want := w.String(), `[{},[]]`; got != want {
		t.Fatalf("document = %s, want %s", got, want)
	}
}
