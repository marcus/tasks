// check-task-fields-probe emits the per-record task diagnostics owned by the
// check-task-fields slice. Like the other probes it is not `tasks check`: the
// report, its summary line, and its exit status are check-report-and-cli's
// slice, and the tree rules are check-tree-structure's. Both channels are
// printed because the split between them — a warning leaves the file usable —
// is the behavior this slice has to prove.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"tasks-go/internal/check"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: check-task-fields-probe TASKS_JSONL")
		os.Exit(2)
	}

	encoded, err := json.Marshal(check.Check(flag.Arg(0)))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}
