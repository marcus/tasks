// store-snapshot-probe emits the direct Store Item projection used before the
// Go query and CLI surfaces exist. It is intentionally narrower than the
// language-neutral runner: it compares only this slice's read-model boundary.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"tasks-go/internal/store"
)

type output struct {
	Live    []item `json:"live"`
	Archive []item `json:"archive"`
}

type item struct {
	State     any      `json:"state"`
	Priority  any      `json:"priority"`
	Title     any      `json:"title"`
	Tags      []string `json:"tags"`
	Scheduled *string  `json:"scheduled"`
	Deadline  *string  `json:"deadline"`
	Recur     any      `json:"recur"`
	Lead      any      `json:"lead"`
	LeadSkip  any      `json:"lead_skip"`
	ID        *string  `json:"id"`
	Closed    *string  `json:"closed"`
	Line      int      `json:"line"`
	Source    string   `json:"source"`
}

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: store-snapshot-probe LIVE [ARCHIVE]")
		os.Exit(2)
	}

	paths := store.Paths{Live: os.Args[1]}
	if len(os.Args) == 3 {
		paths.Archive = os.Args[2]
	}
	snapshot, err := store.Capture(paths, store.Unlocked{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoded, err := json.Marshal(output{
		Live:    project(snapshot.Items()),
		Archive: project(snapshot.ArchiveItems()),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(encoded))
}

func project(items []store.Item) []item {
	projected := make([]item, len(items))
	for index, value := range items {
		projected[index] = item{
			State: value.State, Priority: value.Priority, Title: value.Title,
			Tags: value.Tags, Scheduled: isoDate(value.Scheduled), Deadline: isoDate(value.Deadline),
			Recur: value.Recur, Lead: value.Lead, LeadSkip: value.LeadSkip, ID: value.ID,
			Closed: isoDate(value.Closed), Line: value.Line, Source: string(value.Source),
		}
	}
	return projected
}

func isoDate(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.Format("2006-01-02")
	return &formatted
}
