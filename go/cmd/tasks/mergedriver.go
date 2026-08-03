package main

import (
	"os"

	"tasks-go/internal/merge"
)

// merge-driver is Git plumbing, not a user command: Git invokes it with three
// temporary merge-stage paths and the working pathname, and copies whatever the
// driver leaves in the ours file back over the working tree whatever the exit
// status.
//
// It depends only on the four explicit paths Git supplied — never on the
// configured task store — which is why it takes nothing from the surface it is
// handed.
func init() {
	register("merge-driver", func(_ *surfaceContext, args []string) int {
		return merge.RunDriver(args, os.Stdout, os.Stderr, env.Get("TASKS_MERGE_VERBOSE") == "1")
	})
}
