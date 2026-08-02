// format-parse-probe emits the direct parser result used to compare the
// format-parse slice before the check command exists in the Go port.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"

	"tasks-go/internal/record"
)

type output struct {
	Records []map[string]any `json:"records"`
	Errors  []parseError     `json:"errors"`
}

type parseError struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: format-parse-probe PATH")
		os.Exit(2)
	}

	input, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	result := record.Parse(input)
	out := output{
		Records: make([]map[string]any, 0, len(result.Records)),
		Errors:  make([]parseError, 0, len(result.Errors)),
	}
	for _, parsed := range result.Records {
		fields := make(map[string]any, len(parsed.Fields)+1)
		for _, field := range parsed.Fields {
			value, err := transport(field.Value)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fields[field.Key] = value
		}
		fields["line"] = json.RawMessage(strconv.Itoa(parsed.Line))
		out.Records = append(out.Records, fields)
	}
	for _, parseErr := range result.Errors {
		out.Errors = append(out.Errors, parseError{Line: parseErr.Line, Message: parseErr.Message})
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}

// transport preserves parsed JSON values for the differential runner while
// giving Ruby's non-finite Float values a JSON-safe, typed representation.
// It is probe-only: persisted JSONL remains the raw record bytes.
func transport(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return transportValue(value), nil
}

func transportValue(value any) any {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(string(typed), 64)
		if err != nil && math.IsInf(parsed, 0) {
			infinity := "Infinity"
			if math.Signbit(parsed) {
				infinity = "-Infinity"
			}
			return map[string]string{"$tasks_format_probe_type": "non-finite-float", "value": infinity}
		}
		return typed
	case []any:
		for index := range typed {
			typed[index] = transportValue(typed[index])
		}
		return typed
	case map[string]any:
		for key, child := range typed {
			typed[key] = transportValue(child)
		}
		return typed
	default:
		return value
	}
}
