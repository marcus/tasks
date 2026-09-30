package taskquery

import (
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
)

// A named view as a whole answer: the rows it admits in canonical order, and
// the groups a surface lays them out in. The TUI tabs and the HTTP views read
// are both built from ViewQuery, and this file is the part that turns one into
// a transport-neutral value — so a client that wants the Agenda tab's day
// buckets asks for them instead of re-deriving them from a task list.

// The agenda's day buckets, by key.
const (
	AgendaOverdue  = "overdue"
	AgendaToday    = "today"
	AgendaTomorrow = "tomorrow"
	AgendaLater    = "later"
)

// AgendaBuckets are the agenda's groups in painted order, with the label a
// surface prints. The TUI shouts them in its section rules; the text is the
// same word either way.
var AgendaBuckets = [][2]string{
	{AgendaOverdue, "Overdue"},
	{AgendaToday, "Today"},
	{AgendaTomorrow, "Tomorrow"},
	{AgendaLater, "Later"},
}

// DayBucket classifies a day delta. `scheduled` marks a date that is a start
// date rather than a deadline: a start date that has already passed is not
// overdue, it is startable, so it lands in TODAY.
func DayBucket(days int, scheduled bool) string {
	if scheduled && days < 0 {
		days = 0
	}
	switch {
	case days < 0:
		return AgendaOverdue
	case days == 0:
		return AgendaToday
	case days == 1:
		return AgendaTomorrow
	default:
		return AgendaLater
	}
}

// PrimaryDate is the date a row is filed under: the deadline if there is one,
// else the available-from date. kind is "deadline" or "scheduled".
func (q *Queries) PrimaryDate(item store.Item) (temporal.Date, string, temporal.Value, bool) {
	if item.Deadline != "" {
		if value, ok := q.DeadlineValue(item); ok {
			return value.Date, "deadline", value, true
		}
	}
	if item.Scheduled != "" {
		if value, ok := q.ScheduledValue(item); ok {
			return value.Date, "scheduled", value, true
		}
	}
	return temporal.Date{}, "", temporal.Value{}, false
}

// AgendaBucket is the day group one item belongs to, measured against this
// reader's today — the server's zone for the API, the terminal's for the TUI.
// An undated row files under later.
func (q *Queries) AgendaBucket(item store.Item) string {
	date, kind, _, ok := q.PrimaryDate(item)
	if !ok {
		return AgendaLater
	}
	return DayBucket(date.Sub(q.Today()), kind != "deadline")
}

// NamedViews are the views NamedView answers. Projects has its own resource and
// the outline its own shape (see Outline), so neither is here.
var NamedViews = []string{ViewAgenda, ViewNext, ViewQuadrants, ViewInbox}

// Inbox blocks: the intake tab is two queues, one above the other.
const (
	BlockApprovals = "approvals"
	BlockInbox     = "inbox"
)

// ViewGroup is one bucket of a named view. Block is empty except in the inbox,
// where it names which of the two queues the group belongs to.
type ViewGroup struct {
	Block string
	Key   string
	Label string
	Items []store.Item
}

// NamedViewResult is one named view: every row it admits, each once, in the
// view's canonical order — the order `tasks <view>` prints — and the groups a
// grouped surface lays them out in. A row can sit in several groups (a task
// with two contexts in Next) but appears in Items once.
type NamedViewResult struct {
	Name   string
	Items  []store.Item
	Groups []ViewGroup
}

// NamedViewOptions are the reader's choices a view honours.
type NamedViewOptions struct {
	// UrgentDays overrides the quadrant urgency horizon; zero means the read
	// model's own configured window (WithUrgentDays), the one QuadrantFor
	// classifies a single task against.
	UrgentDays int
	// IncludeUnavailable is the TUI's Z toggle: rows that are held, deferred
	// or not yet startable are admitted instead of hidden. It never reaches
	// Approvals, which is a decision queue with no availability gate.
	IncludeUnavailable bool
}

// NamedView answers one named view over the live file, or ok=false for a name
// that is not one.
func (q *Queries) NamedView(name string, options NamedViewOptions) (NamedViewResult, bool) {
	live := q.LiveItems()
	urgentDays := options.UrgentDays
	if urgentDays <= 0 {
		urgentDays = q.urgentDays
	}
	query := NewViewQuery(name, q, urgentDays, options.IncludeUnavailable, nil)
	result := NamedViewResult{Name: name, Items: []store.Item{}, Groups: []ViewGroup{}}
	switch name {
	case ViewAgenda:
		result.Items = query.Sort(query.Select(live))
		byBucket := map[string][]store.Item{}
		for _, item := range result.Items {
			bucket := q.AgendaBucket(item)
			byBucket[bucket] = append(byBucket[bucket], item)
		}
		for _, bucket := range AgendaBuckets {
			result.Groups = append(result.Groups, ViewGroup{
				Key: bucket[0], Label: bucket[1], Items: nonNil(byBucket[bucket[0]]),
			})
		}
	case ViewNext:
		result.Items = query.Sort(query.Select(live))
		for _, group := range query.SortedGroups(query.GroupItems(live)) {
			result.Groups = append(result.Groups, ViewGroup{
				Key: group.Key, Label: group.Key, Items: nodeItems(group.Nodes),
			})
		}
	case ViewQuadrants:
		// Every quadrant is present, empty ones included: an empty Q1 is the
		// reassuring answer, and a shape that dropped it would make a client
		// tell "nothing urgent" apart from "not sent".
		result.Items = query.Sort(query.Select(live))
		groups := query.GroupItems(live)
		for _, label := range QuadrantLabels {
			result.Groups = append(result.Groups, ViewGroup{
				Key: label[0], Label: label[1], Items: nodeItems(groups[label[0]]),
			})
		}
	case ViewInbox:
		// Approvals first, in the shared triage order, bucketed WITHOUT
		// re-sorting so that rank survives inside each project; then the
		// accepted inbox, grouped the way the tab groups it. Both blocks lay
		// their projects out in the Projects view's sequence, unfiled last.
		proposals := []store.Item{}
		for _, item := range live {
			if isProposed(item.State) {
				proposals = append(proposals, item)
			}
		}
		proposals = q.RankByPriorityThenDue(proposals)
		accepted := query.Sort(query.Select(live))
		result.Items = append(append([]store.Item{}, proposals...), accepted...)
		for _, group := range query.GroupItemsInOrder(proposals) {
			result.Groups = append(result.Groups, ViewGroup{
				Block: BlockApprovals, Key: group.Key, Label: group.Key, Items: nodeItems(group.Nodes),
			})
		}
		for _, group := range query.SortedGroups(query.GroupItems(live)) {
			result.Groups = append(result.Groups, ViewGroup{
				Block: BlockInbox, Key: group.Key, Label: group.Key, Items: nodeItems(group.Nodes),
			})
		}
	default:
		return NamedViewResult{}, false
	}
	return result, true
}

func nodeItems(nodes []*Node) []store.Item {
	items := make([]store.Item, 0, len(nodes))
	for _, node := range nodes {
		items = append(items, *node.Item)
	}
	return items
}

func nonNil(items []store.Item) []store.Item {
	if items == nil {
		return []store.Item{}
	}
	return items
}
