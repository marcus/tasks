// check-meta-and-ids-probe emits the metadata and ID diagnostics owned by the
// check-meta-and-ids slice. It is intentionally not the eventual `tasks check`
// command: task-field and tree diagnostics are separate porting slices.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"tasks-go/internal/check"
)

func main() {
	allFiles := flag.Bool("all-files", false, "also check archive.jsonl beside the live store")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: check-meta-and-ids-probe [--all-files] TASKS_JSONL")
		os.Exit(2)
	}

	live := flag.Arg(0)
	result := check.Check(live)
	if *allFiles {
		result = check.CheckStore(live, filepath.Join(filepath.Dir(live), "archive.jsonl"))
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
