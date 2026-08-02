// format-parse-probe emits the direct parser result used to compare the
// format-parse slice before the check command exists in the Go port.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"tasks-go/internal/record"
)

type output struct {
	Records []map[string]json.RawMessage `json:"records"`
	Errors  []parseError                 `json:"errors"`
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
		Records: make([]map[string]json.RawMessage, 0, len(result.Records)),
		Errors:  make([]parseError, 0, len(result.Errors)),
	}
	for _, parsed := range result.Records {
		fields := make(map[string]json.RawMessage, len(parsed.Fields)+1)
		for _, field := range parsed.Fields {
			fields[field.Key] = field.Value
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
