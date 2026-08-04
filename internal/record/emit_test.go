package record

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDumpRecordOrdersKnownKeysAndOmitsAbsentFields(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "insertion order does not survive",
			input: `{"body":"note","title":"T","state":"NEXT","parent":"p1","id":"i1","type":"task"}`,
			want:  `{"type":"task","id":"i1","parent":"p1","state":"NEXT","title":"T","body":"note"}`,
		},
		{
			name:  "nil, empty string, and empty array are absent fields",
			input: `{"type":"task","id":"i1","parent":null,"title":"T","tags":[],"body":""}`,
			want:  `{"type":"task","id":"i1","title":"T"}`,
		},
		{
			name:  "present values are not dropped by omission",
			input: `{"type":"task","id":"i1","title":"T","tags":["@home"],"lead":0,"archived":false}`,
			want:  `{"type":"task","id":"i1","title":"T","tags":["@home"],"lead":0,"archived":false}`,
		},
		{
			name:  "unknown keys follow known keys in insertion order",
			input: `{"zeta":1,"type":"task","alpha":2,"title":"T"}`,
			want:  `{"type":"task","title":"T","zeta":1,"alpha":2}`,
		},
		{
			name:  "unknown keys round-trip untouched",
			input: `{"type":"task","title":"T","future":{"b":2,"a":1},"list":[1,{"z":null}]}`,
			want:  `{"type":"task","title":"T","future":{"b":2,"a":1},"list":[1,{"z":null}]}`,
		},
		{
			name:  "non-ASCII is left unescaped",
			input: `{"type":"task","title":"Café — résumé naïve"}`,
			want:  `{"type":"task","title":"Café — résumé naïve"}`,
		},
		{
			name:  "the physical line marker never serializes",
			input: `{"type":"task","title":"T","line":9}`,
			want:  `{"type":"task","title":"T"}`,
		},
		{
			name:  "an empty object is an absent field",
			input: `{"type":"task","title":"T","delegation":{},"extra":{}}`,
			want:  `{"type":"task","title":"T"}`,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := DumpRecord(parseOne(t, testCase.input))
			if err != nil {
				t.Fatalf("DumpRecord: %v", err)
			}
			if got != testCase.want {
				t.Fatalf("DumpRecord =\n%s\nwant\n%s", got, testCase.want)
			}
		})
	}
}

func TestDumpRecordCanonicalizesNestedObjects(t *testing.T) {
	cases := []struct{ input, want string }{
		{`{"type":"task","title":"T","scheduled_time":{"unknown":1,"fold":1,"timezone":"Europe/Berlin","local":"09:00"}}`, `{"type":"task","title":"T","scheduled_time":{"local":"09:00","timezone":"Europe/Berlin","fold":1}}`},
		{`{"type":"task","title":"T","deadline_time":{"unknown":1}}`, `{"type":"task","title":"T"}`},
		{`{"type":"task","title":"T","delegation":{"zeta":false,"at":"2026-07-27T18:04:11Z","kind":"agent","alpha":0,"status":"ready","mode":"research"}}`, `{"type":"task","title":"T","delegation":{"kind":"agent","mode":"research","status":"ready","at":"2026-07-27T18:04:11Z","zeta":false,"alpha":0}}`},
		{`{"type":"task","title":"T","scheduled_time":"09:00","delegation":false}`, `{"type":"task","title":"T","scheduled_time":"09:00","delegation":false}`},
	}
	for _, testCase := range cases {
		got, err := DumpRecord(parseOne(t, testCase.input))
		if err != nil || got != testCase.want {
			t.Fatalf("DumpRecord(%s) = %q, %v; want %q", testCase.input, got, err, testCase.want)
		}
	}
}

// TestNestedCanonicalizationAcrossPermutations is the medium-tier property
// check for the nested writer. It makes the two deliberately different
// forward-compatibility rules falsifiable for every source ordering: temporal
// unknowns disappear, while delegation unknowns keep their relative order.
func TestNestedCanonicalizationAcrossPermutations(t *testing.T) {
	temporal := []nestedWireField{
		{key: "local", value: `"09:00"`},
		{key: "timezone", value: `"Europe/Berlin"`},
		{key: "fold", value: "false"},
		{key: "future_a", value: "1"},
		{key: "future_b", value: "2"},
	}
	for _, fields := range nestedPermutations(temporal) {
		got := dumpNested(t, "scheduled_time", fields)
		want := `{"type":"task","title":"T","scheduled_time":{"local":"09:00","timezone":"Europe/Berlin","fold":false}}`
		if got != want {
			t.Fatalf("temporal permutation %v = %s, want %s", nestedKeys(fields), got, want)
		}
	}

	delegation := []nestedWireField{
		{key: "kind", value: `"agent"`},
		{key: "mode", value: `"research"`},
		{key: "status", value: `"ready"`},
		{key: "assignee", value: `"worker"`},
		{key: "at", value: `"2026-07-27T18:04:11Z"`},
		{key: "work_ref", value: `"td-123"`},
	}
	delegationWant := `{"type":"task","title":"T","delegation":{"kind":"agent","mode":"research","status":"ready","assignee":"worker","at":"2026-07-27T18:04:11Z","work_ref":"td-123"}}`
	for _, fields := range nestedPermutations(delegation) {
		got := dumpNested(t, DelegationField, fields)
		if got != delegationWant {
			t.Fatalf("delegation declared-key permutation %v = %s, want %s", nestedKeys(fields), got, delegationWant)
		}
	}

	// Permuting both declared and unknown fields proves the unknown suffix is
	// source ordered, rather than merely preserving an incidental test order.
	forward := []nestedWireField{
		{key: "kind", value: `"agent"`},
		{key: "status", value: `"ready"`},
		{key: "zeta", value: "false"},
		{key: "alpha", value: "0"},
		{key: "future", value: `"kept"`},
	}
	for _, fields := range nestedPermutations(forward) {
		unknown := make([]nestedWireField, 0, 3)
		for _, field := range fields {
			if field.key == "zeta" || field.key == "alpha" || field.key == "future" {
				unknown = append(unknown, field)
			}
		}
		want := `{"type":"task","title":"T","delegation":{"kind":"agent","status":"ready"` + nestedObjectSuffix(unknown) + `}}`
		got := dumpNested(t, DelegationField, fields)
		if got != want {
			t.Fatalf("delegation forward-compat permutation %v = %s, want %s", nestedKeys(fields), got, want)
		}
	}
}

type nestedWireField struct{ key, value string }

func dumpNested(t *testing.T, key string, fields []nestedWireField) string {
	t.Helper()
	parts := make([]string, len(fields))
	for index, field := range fields {
		parts[index] = `"` + field.key + `":` + field.value
	}
	got, err := DumpRecord(parseOne(t, `{"type":"task","title":"T","`+key+`":{`+strings.Join(parts, ",")+`}}`))
	if err != nil {
		t.Fatalf("DumpRecord: %v", err)
	}
	return got
}

func nestedObjectSuffix(fields []nestedWireField) string {
	parts := make([]string, len(fields))
	for index, field := range fields {
		parts[index] = `,"` + field.key + `":` + field.value
	}
	return strings.Join(parts, "")
}

func nestedKeys(fields []nestedWireField) []string {
	keys := make([]string, len(fields))
	for index, field := range fields {
		keys[index] = field.key
	}
	return keys
}

func nestedPermutations(fields []nestedWireField) [][]nestedWireField {
	if len(fields) == 0 {
		return [][]nestedWireField{{}}
	}
	permutations := make([][]nestedWireField, 0)
	for index, field := range fields {
		rest := append([]nestedWireField{}, fields[:index]...)
		rest = append(rest, fields[index+1:]...)
		for _, permutation := range nestedPermutations(rest) {
			permutations = append(permutations, append([]nestedWireField{field}, permutation...))
		}
	}
	return permutations
}

func TestDumpFileShape(t *testing.T) {
	if got, err := Dump(nil); err != nil || got != "" {
		t.Fatalf("Dump(nil) = %q, %v; want empty string", got, err)
	}

	result := Parse([]byte("{\"type\":\"meta\",\"version\":2}\n{\"type\":\"task\",\"title\":\"T\"}\n"))
	text, err := Dump(result.Records)
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	want := "{\"type\":\"meta\",\"version\":2}\n{\"type\":\"task\",\"title\":\"T\"}\n"
	if text != want {
		t.Fatalf("Dump =\n%q\nwant\n%q", text, want)
	}

	line, err := DumpRecord(result.Records[0])
	if err != nil {
		t.Fatalf("DumpRecord: %v", err)
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("DumpRecord returned more than one line: %q", line)
	}
}

// TestEscapesMatchRubyGenerator pins the escape set Ruby's generator uses.
// Go's encoder escapes <, >, &, U+2028, and U+2029; Ruby leaves all five
// verbatim, so the emitter cannot delegate string encoding to encoding/json.
func TestEscapesMatchRubyGenerator(t *testing.T) {
	cases := []struct{ input, want string }{
		{input: `{"title":"a<b>c&d"}`, want: `{"title":"a<b>c&d"}`},
		{input: `{"title":"line sep par"}`, want: "{\"title\":\"line sep par\"}"},
		{input: `{"title":"tab\there\nnew"}`, want: `{"title":"tab\there\nnew"}`},
		{input: `{"title":"a\u0007b"}`, want: `{"title":"a\u0007b"}`},
		{input: `{"title":"quote\" back\\slash"}`, want: `{"title":"quote\" back\\slash"}`},
		{input: `{"title":"del"}`, want: "{\"title\":\"del\"}"},
	}
	for _, testCase := range cases {
		got, err := DumpRecord(parseOne(t, testCase.input))
		if err != nil {
			t.Fatalf("DumpRecord(%s): %v", testCase.input, err)
		}
		if got != testCase.want {
			t.Fatalf("DumpRecord(%s) = %q, want %q", testCase.input, got, testCase.want)
		}
	}
}

// TestNumbersUseRubySpelling covers the one place a value's source spelling is
// not preserved: Ruby round-trips numbers through Integer#to_s and
// Float#to_s, so the emitter must reproduce those spellings rather than the
// literal or Go's own shortest form.
func TestNumbersUseRubySpelling(t *testing.T) {
	cases := []struct{ literal, want string }{
		{literal: "1", want: "1"},
		{literal: "-0", want: "0"},
		{literal: "12345678901234567890123456789", want: "12345678901234567890123456789"},
		{literal: "1.50", want: "1.5"},
		{literal: "1e2", want: "100.0"},
		{literal: "1e14", want: "100000000000000.0"},
		{literal: "1e15", want: "1e+15"},
		{literal: "1e20", want: "1e+20"},
		{literal: "0.0001", want: "0.0001"},
		{literal: "1e-9", want: "0.000000001"},
		{literal: "1e-10", want: "1e-10"},
		{literal: "-2.5e-7", want: "-0.00000025"},
		{literal: "0.0", want: "0.0"},
		{literal: "-0.0", want: "-0.0"},
		{literal: "3.141592653589793", want: "3.141592653589793"},
	}
	for _, testCase := range cases {
		got, err := DumpRecord(parseOne(t, `{"n":`+testCase.literal+`}`))
		if err != nil {
			t.Fatalf("DumpRecord(%s): %v", testCase.literal, err)
		}
		if want := `{"n":` + testCase.want + `}`; got != want {
			t.Fatalf("DumpRecord(%s) = %s, want %s", testCase.literal, got, want)
		}
	}
}

// TestFloatSpellingsMatchTheRubyOracle replays every literal Ruby's generator
// was captured on. Ruby's spelling is the expected value everywhere, and every
// one of them now matches: float-divergences.json records the five that the
// pre-Grisu2 emitter got wrong and its open list is empty, which this test
// also asserts so the file cannot quietly regain entries.
func TestFloatSpellingsMatchTheRubyOracle(t *testing.T) {
	evidence := filepath.Join("..", "..", "testdata", "oracles")

	raw, err := os.ReadFile(filepath.Join(evidence, "float-spellings.json"))
	if err != nil {
		t.Fatalf("read float oracle: %v", err)
	}
	var oracle struct {
		Spellings map[string]string `json:"spellings"`
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatalf("decode float oracle: %v", err)
	}
	if len(oracle.Spellings) < 100 {
		t.Fatalf("float oracle holds only %d literals", len(oracle.Spellings))
	}

	recorded, err := os.ReadFile(filepath.Join(evidence, "float-divergences.json"))
	if err != nil {
		t.Fatalf("read recorded divergences: %v", err)
	}
	var known struct {
		Divergences []struct {
			Literal string `json:"literal"`
			Ruby    string `json:"ruby"`
			Go      string `json:"go"`
		} `json:"divergences"`
	}
	if err := json.Unmarshal(recorded, &known); err != nil {
		t.Fatalf("decode recorded divergences: %v", err)
	}
	if len(known.Divergences) != 0 {
		t.Fatalf("float-divergences.json lists %d open divergences", len(known.Divergences))
	}

	for literal, want := range oracle.Spellings {
		got, err := DumpRecord(parseOne(t, `{"n":`+literal+`}`))
		if err != nil {
			t.Fatalf("DumpRecord(%s): %v", literal, err)
		}
		got = strings.TrimSuffix(strings.TrimPrefix(got, `{"n":`), `}`)
		if got != want {
			t.Errorf("float spelling for %s = %s, want Ruby's %s", literal, got, want)
		}
	}
}

// TestNonFiniteFloatIsRefused mirrors Ruby's JSON::GeneratorError: its parser
// overflows 1e400 to Float::INFINITY and its generator then refuses to write
// it. Emitting Go's "+Inf" or a silently clamped value would be blessing Go
// output over the oracle.
func TestNonFiniteFloatIsRefused(t *testing.T) {
	if _, err := DumpRecord(parseOne(t, `{"n":1e400}`)); err == nil {
		t.Fatal("DumpRecord(1e400) succeeded; want a generator error")
	}
}

// TestFixtureDumpsMatchRubyBytes is the differential check against the
// recorded Ruby oracle: the compatibility evidence archived at ruby-final-2026-08-04
// lists the SHA-256 of each fixture's Ruby dump. Every hash below is copied
// from that capture, never from Go output.
func TestFixtureDumpsMatchRubyBytes(t *testing.T) {
	cases := []struct{ path, dump string }{
		{path: "valid/empty-store/store/tasks.jsonl", dump: "32c39db1a9da0270bf0134d63d7e52ce9771d06d81e61e5e9c9ed8610e00bf60"},
		{path: "valid/single-task/store/tasks.jsonl", dump: "6f1ce7683239606fb0c73730bb5173188a05c55f3512ef927f6534a69013bd60"},
		{path: "valid/small-gtd/store/tasks.jsonl", dump: "e0ade36b8374dc044b3c0b277e60edb9c2051591785be8b8340d265d0688eabe"},
		{path: "valid/deep-nesting/store/tasks.jsonl", dump: "13a349a178477d415de5143e97c514d204f5a6fdcc28c5818498bcdf07b31eb9"},
		{path: "valid/full-field-matrix/store/tasks.jsonl", dump: "b1520184b16fe4ebf48839fad38beb615d4e9f17d334ec9b75275de0d33fd4d7"},
		{path: "valid/archive-pair/store/tasks.jsonl", dump: "2088e054eaf73252f97824a008cbf24e6ccdda3ed3dbce0a9d45e7faa70e1a37"},
		{path: "valid/archive-pair/store/archive.jsonl", dump: "771f2c1fb5a3b661635686ee718435aae708a4d86ac7ff8bffaf941e0492154a"},
		{path: "valid/scale-ordering/store/tasks.jsonl", dump: "c74ec4a9754fab1c0773a5da2bd9e657c9dda48b8fec537f2bc2837ef43917d6"},
		{path: "compat/forward-compat-unknown-keys/store/tasks.jsonl", dump: "92db49c74378e03ed55304fb73098d280a3552ced9e4f43fc1addb3fda1e6e77"},
		{path: "compat/future-schema-v3/store/tasks.jsonl", dump: "f6e61e3ae473ae675be734e7704889506681c0b0a1287f1bcff1f797e105b4f8"},
		// The sole fixture Ruby rewrites: valid JSON in noncanonical order.
		{path: "malformed/wrong-key-order/store/tasks.jsonl", dump: "9e94e743468cee9a48d30fb5c5181e260179a60f7f1249e2d41226ef1c3dac0c"},
	}

	for _, testCase := range cases {
		t.Run(testCase.path, func(t *testing.T) {
			input, err := os.ReadFile(filepath.Join("..", "..", "testdata", "fixtures", testCase.path))
			if err != nil {
				t.Fatalf("read fixture: %v", err)
			}
			result := Parse(input)
			if !result.OK() {
				t.Fatalf("fixture did not parse cleanly: %v", result.Errors)
			}
			text, err := Dump(result.Records)
			if err != nil {
				t.Fatalf("Dump: %v", err)
			}
			sum := sha256.Sum256([]byte(text))
			if got := hex.EncodeToString(sum[:]); got != testCase.dump {
				t.Fatalf("dump sha256 = %s, want Ruby's %s", got, testCase.dump)
			}
		})
	}
}

func parseOne(t *testing.T, line string) Record {
	t.Helper()
	result := Parse([]byte(line + "\n"))
	if len(result.Records) != 1 {
		t.Fatalf("parse(%s) produced %d records, errors %v", line, len(result.Records), result.Errors)
	}
	var check map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &check); err != nil {
		t.Fatalf("test input is not a JSON object: %v", err)
	}
	return result.Records[0]
}
