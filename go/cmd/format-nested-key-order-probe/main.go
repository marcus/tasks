// format-nested-key-order-probe mirrors the direct Ruby nested-writer oracle.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"tasks-go/internal/record"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: format-nested-key-order-probe CASES.jsonl")
		os.Exit(2)
	}
	input, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, line := range bytes.Split(input, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		var spec struct {
			CaseID string          `json:"case_id"`
			Record json.RawMessage `json:"record"`
			Notes  string          `json:"notes"`
		}
		if err := json.Unmarshal(line, &spec); err != nil {
			panic(err)
		}
		parsed := record.Parse(spec.Record)
		if len(parsed.Errors) != 0 || len(parsed.Records) != 1 {
			panic("case record did not parse")
		}
		dumped, err := record.DumpRecord(parsed.Records[0])
		if err != nil {
			panic(err)
		}
		reparsed := record.Parse([]byte(dumped))
		if len(reparsed.Errors) != 0 || len(reparsed.Records) != 1 {
			panic("dumped record did not parse")
		}
		out := map[string]any{"case_id": spec.CaseID, "dumped": dumped, "parsed": fields(reparsed.Records[0])}
		if spec.Notes != "" {
			out["notes"] = spec.Notes
		}
		encoded, err := json.Marshal(out)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(encoded))
	}
}

func fields(parsed record.Record) map[string]any {
	out := make(map[string]any, len(parsed.Fields)+1)
	for _, field := range parsed.Fields {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(field.Value))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			panic(err)
		}
		out[field.Key] = value
	}
	out["line"] = parsed.Line
	return out
}
