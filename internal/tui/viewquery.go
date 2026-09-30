package tui

import (
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

// The six tabs, in order. The number is part of the label because the number is
// also the key that jumps to the tab.
const (
	ViewAgenda    = taskquery.ViewAgenda
	ViewNext      = taskquery.ViewNext
	ViewQuadrants = taskquery.ViewQuadrants
	ViewProjects  = taskquery.ViewProjects
	ViewOutline   = taskquery.ViewOutline
	ViewInbox     = taskquery.ViewInbox
)

// Tab is one entry of the header strip, in the three sizes the strip degrades
// through as the terminal narrows.
//
// The names carry no jump key. The keys 1-6 still work, and they are advertised
// once — in the footer's `1-6 views` hint — rather than stamped onto all six
// tabs. A host that has taken the number row for itself
// (EmbeddedOptions.SuppressViewKeyHints) drops that one hint; the keys keep
// working either way, and the strip is unaffected because there is nothing in
// it to suppress.
type Tab struct {
	Label   string
	Compact string
	Minimum string

	Key string
}

// Tabs is the canonical tab order. Views, the jump keys and the session's
// saved view all read this one list.
var Tabs = []Tab{
	{"agenda", "ag", "ag", ViewAgenda},
	{"next", "nx", "nx", ViewNext},
	{"quadrants", "quad", "q", ViewQuadrants},
	{"projects", "proj", "pr", ViewProjects},
	{"outline", "out", "out", ViewOutline},
	{"inbox", "in", "in", ViewInbox},
}

// ViewKeys is the tab keys alone.
func ViewKeys() []string {
	keys := make([]string, 0, len(Tabs))
	for _, tab := range Tabs {
		keys = append(keys, tab.Key)
	}
	return keys
}

// IntakeCounts is what the Inbox tab badge and its section headers advertise.
type IntakeCounts struct {
	Inbox     int
	Approvals int
}

// ViewQuery is the canonical semantic query for every tab: per-view
// eligibility, classification, grouping and ordering. It lives in taskquery so
// the HTTP views read computes the same buckets from the same code; the TUI
// names it here so its builders read as they always have.
type ViewQuery = taskquery.ViewQuery

// Group is one named bucket of nodes, already sorted.
type Group = taskquery.Group

// UnfiledIntake is the one bucket every intake row with no project of its own
// lands in. See taskquery.UnfiledIntake.
const UnfiledIntake = taskquery.UnfiledIntake

// NewViewQuery builds the query for one view. Closed rows start hidden; a
// caller that wants them says so with ShowingClosed.
func NewViewQuery(view string, queries *taskquery.Queries, urgentDays int, showDeferred bool,
	contextFilters []string) ViewQuery {
	return taskquery.NewViewQuery(view, queries, urgentDays, showDeferred, contextFilters)
}

// groupItems is the flat item path's grouping, kept as a function so the
// builders read the same as the tree path's GroupedNodes call.
func groupItems(query ViewQuery, items []store.Item) map[string][]*taskquery.Node {
	return query.GroupItems(items)
}

// priorityKey is Ruby's `item.priority || "Z"`: unprioritized sorts after C.
func priorityKey(item store.Item) string {
	if item.Priority == "" {
		return "Z"
	}
	return item.Priority
}

func isOpenState(state string) bool {
	for _, open := range taskquery.OpenStates() {
		if open == state {
			return true
		}
	}
	return false
}

func isProposedState(state string) bool { return state == "PROPOSED" }

// isClosedState is DONE or CANCELLED: work whose lifecycle has ended.
//
// It asks the closed vocabulary DIRECTLY rather than spelling itself as
// "neither open nor proposed". The three predicates do not partition the
// states, because a state can also be nonsense — a hand-edited `TODOO`, or a
// record with no state at all — and the negation would file every one of those
// as finished. The Outline is where a reader goes to FIND a broken row, so a
// malformed task must stay on screen and must never be counted as history.
func isClosedState(state string) bool {
	for _, closed := range taskquery.ClosedStates() {
		if closed == state {
			return true
		}
	}
	return false
}
