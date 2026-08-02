// Command format-emit-float-diff reports every float literal in the Ruby
// oracle capture whose Go canonical spelling differs from Ruby's. It records
// the divergence, it never resolves one: the output names Ruby's spelling as
// the expected value and Go's as the defect.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"tasks-go/internal/record"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: format-emit-float-diff <float-spellings.json>")
		os.Exit(2)
	}
	raw, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var oracle struct {
		Ruby      string            `json:"ruby"`
		JSON      string            `json:"json"`
		Spellings map[string]string `json:"spellings"`
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	type divergence struct {
		Literal string `json:"literal"`
		Ruby    string `json:"ruby"`
		Go      string `json:"go"`
	}
	divergences := []divergence{}
	for literal, want := range oracle.Spellings {
		result := record.Parse([]byte(`{"n":` + literal + "}\n"))
		if len(result.Records) != 1 {
			fmt.Fprintf(os.Stderr, "literal %s did not parse: %v\n", literal, result.Errors)
			os.Exit(1)
		}
		line, err := record.DumpRecord(result.Records[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "literal %s did not dump: %v\n", literal, err)
			os.Exit(1)
		}
		got := strings.TrimSuffix(strings.TrimPrefix(line, `{"n":`), `}`)
		if got != want {
			divergences = append(divergences, divergence{Literal: literal, Ruby: want, Go: got})
		}
	}
	sort.Slice(divergences, func(i, j int) bool { return divergences[i].Literal < divergences[j].Literal })

	out, err := json.MarshalIndent(map[string]any{
		"ruby":        oracle.Ruby,
		"json":        oracle.JSON,
		"literals":    len(oracle.Spellings),
		"divergences": divergences,
	}, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(out))
}
