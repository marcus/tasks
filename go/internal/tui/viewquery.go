package tui

import (
	"math"
	"sort"

	"tasks-go/internal/store"
	"tasks-go/internal/taskquery"
)

// The six tabs, in order. The number is part of the label because the number is
// also the key that jumps to the tab.
const (
	ViewAgenda    = "agenda"
	ViewNext      = "next"
	ViewQuadrants = "quadrants"
	ViewProjects  = "projects"
	ViewOutline   = "outline"
	ViewInbox     = "inbox"
)

// Tab is one entry of the header strip.
type Tab struct {
	Label   string
	Compact string
	Minimum string
	Key     string
}

// Tabs is the canonical tab order. Views, the jump keys and the session's
// saved view all read this one list.
var Tabs = []Tab{
	{"1 Agenda", "1 Ag", "1", ViewAgenda},
	{"2 Next", "2 Nx", "2", ViewNext},
	{"3 Quadrants", "3 Q", "3", ViewQuadrants},
	{"4 Projects", "4 Pr", "4", ViewProjects},
	{"5 Outline", "5 Out", "5", ViewOutline},
	{"6 Inbox", "6 In", "6", ViewInbox},
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

// ViewQuery is the canonical semantic query for both the flat and the tree row
// builders — the port of Tui::Views::Query.
//
// It owns per-view eligibility, classification and grouping, and item ordering.
// Tree builders only decide which matching nodes become anchors and then render
// their descendants; they never re-decide what a view means. That split is what
// keeps the Agenda tab and `tasks agenda` from drifting apart.
type ViewQuery struct {
	View           string
	Queries        *taskquery.Queries
	UrgentDays     int
	ShowDeferred   bool
	ContextFilters []string
}

// NewViewQuery builds the query for one view.
func NewViewQuery(view string, queries *taskquery.Queries, urgentDays int, showDeferred bool,
	contextFilters []string) ViewQuery {
	if urgentDays <= 0 {
		urgentDays = taskquery.DefaultUrgentDays
	}
	return ViewQuery{
		View: view, Queries: queries, UrgentDays: urgentDays,
		ShowDeferred: showDeferred, ContextFilters: contextFilters,
	}
}

// Eligible is view-only eligibility: the per-view state/date rule, and then
// availability.
//
// The active context filter is deliberately NOT folded in here. Agenda anchors
// a root when ANY subtree item is eligible, and an undated @work parent whose
// only dates sit on untagged children must keep that thread. Context is a
// separate predicate; Matching is the conjunction.
//
// The two conjuncts are ordered cheap-first on purpose: the view rule reads
// fields already on the Item, while the availability test walks task ancestors.
func (q ViewQuery) Eligible(item store.Item) bool {
	if !q.ViewRule(item) {
		return false
	}
	return q.ShowDeferred || q.available(item)
}

// ViewRule is the per-view state and date test, availability aside.
func (q ViewQuery) ViewRule(item store.Item) bool {
	switch q.View {
	case ViewAgenda:
		return isOpenState(item.State) && (item.Deadline != "" || item.Scheduled != "")
	case ViewNext:
		return item.State == "NEXT"
	case ViewQuadrants:
		return isOpenState(item.State)
	case ViewInbox:
		return item.State == "INBOX"
	case ViewProjects:
		project := q.projectName(item)
		return isOpenState(item.State) && project != "" && project != "Inbox"
	default:
		return false
	}
}

// ContextMatch is true when no context filter is active, or the item carries
// any selected context. Contexts are one OR facet; the view and text predicates
// compose by AND.
func (q ViewQuery) ContextMatch(item store.Item) bool {
	if len(q.ContextFilters) == 0 {
		return true
	}
	for _, context := range item.Contexts {
		for _, wanted := range q.ContextFilters {
			if context == wanted {
				return true
			}
		}
	}
	return false
}

// Matching is the composite the filtered views select and anchor on.
func (q ViewQuery) Matching(item store.Item) bool {
	return q.Eligible(item) && q.ContextMatch(item)
}

// GroupKeys is the group or groups an item belongs to. A task with several
// contexts appears once per context in the Next view — which is exactly why
// selection has to follow an id AND an occurrence, not an id alone.
func (q ViewQuery) GroupKeys(item store.Item) []string {
	switch q.View {
	case ViewNext:
		if len(item.Contexts) == 0 {
			return []string{"(no context)"}
		}
		return append([]string{}, item.Contexts...)
	case ViewQuadrants:
		return []string{q.Queries.QuadrantOf(item, q.UrgentDays)}
	case ViewProjects:
		return []string{q.projectName(item)}
	default:
		return []string{""}
	}
}

// sortKey is the ordering tuple for one item, as three comparable components.
// Ruby builds an array; Go compares in the same order.
type sortKey struct {
	instant  float64
	priority string
	title    string
}

func (q ViewQuery) sortKeyOf(item store.Item) sortKey {
	switch q.View {
	case ViewAgenda:
		return sortKey{instant: q.temporalSortKey(item), priority: priorityKey(item)}
	case ViewNext:
		return sortKey{priority: priorityKey(item)}
	case ViewProjects:
		return sortKey{
			instant:  q.temporalSortKey(item),
			priority: priorityKey(item),
			title:    item.Title,
		}
	default:
		return sortKey{instant: float64(item.Line)}
	}
}

func lessSortKey(left, right sortKey) bool {
	if left.instant != right.instant {
		return left.instant < right.instant
	}
	if left.priority != right.priority {
		return left.priority < right.priority
	}
	return left.title < right.title
}

// Select keeps the matching items, in the order given.
func (q ViewQuery) Select(items []store.Item) []store.Item {
	out := []store.Item{}
	for _, item := range items {
		if q.Matching(item) {
			out = append(out, item)
		}
	}
	return out
}

// Sort orders items by the view's key. STABLE, deliberately: Ruby's sort_by is
// not, and Wave 1 already had to fix one list whose tie order was whatever
// introsort left behind. A list that permutes under an unrelated edit is how a
// user completes the wrong row.
func (q ViewQuery) Sort(items []store.Item) []store.Item {
	out := append([]store.Item{}, items...)
	sort.SliceStable(out, func(left, right int) bool {
		return lessSortKey(q.sortKeyOf(out[left]), q.sortKeyOf(out[right]))
	})
	return out
}

// SelectNodes and SortNodes are the tree-mode twins: the same policy applied to
// nodes through their items.
func (q ViewQuery) SelectNodes(nodes []*taskquery.Node) []*taskquery.Node {
	out := []*taskquery.Node{}
	for _, node := range nodes {
		if node.Task() && q.Matching(*node.Item) {
			out = append(out, node)
		}
	}
	return out
}

// SortNodes orders nodes by their items' view key, stably.
func (q ViewQuery) SortNodes(nodes []*taskquery.Node) []*taskquery.Node {
	out := append([]*taskquery.Node{}, nodes...)
	sort.SliceStable(out, func(left, right int) bool {
		return lessSortKey(q.sortKeyOf(*out[left].Item), q.sortKeyOf(*out[right].Item))
	})
	return out
}

// Group is one named bucket of nodes, already sorted.
type Group struct {
	Key   string
	Nodes []*taskquery.Node
}

// GroupedNodes buckets nodes by GroupKeys and sorts each bucket.
func (q ViewQuery) GroupedNodes(nodes []*taskquery.Node) map[string][]*taskquery.Node {
	groups := map[string][]*taskquery.Node{}
	for _, node := range q.SelectNodes(nodes) {
		for _, key := range q.GroupKeys(*node.Item) {
			groups[key] = append(groups[key], node)
		}
	}
	for key, list := range groups {
		groups[key] = q.SortNodes(list)
	}
	return groups
}

// SortedGroups orders the buckets. Every view but Projects orders by key text;
// Projects orders by the soonest date anywhere in the bucket, then by key.
func (q ViewQuery) SortedGroups(groups map[string][]*taskquery.Node) []Group {
	out := make([]Group, 0, len(groups))
	for key, nodes := range groups {
		out = append(out, Group{Key: key, Nodes: nodes})
	}
	if q.View == ViewProjects {
		soonest := func(group Group) float64 {
			best := math.Inf(1)
			for _, node := range group.Nodes {
				if key := q.temporalSortKey(*node.Item); key < best {
					best = key
				}
			}
			return best
		}
		sort.SliceStable(out, func(left, right int) bool {
			leftKey, rightKey := soonest(out[left]), soonest(out[right])
			if leftKey != rightKey {
				return leftKey < rightKey
			}
			return out[left].Key < out[right].Key
		})
		return out
	}
	sort.SliceStable(out, func(left, right int) bool { return out[left].Key < out[right].Key })
	return out
}

func (q ViewQuery) available(item store.Item) bool {
	return q.Queries.AvailabilityFor(item).Available()
}

// temporalSortKey is the INSTANT a dated item comes due, as a float so an
// undated item can sort last by being +Inf. A deadline sorts by the moment it
// stops being on time; an available-from date sorts by the moment it opens.
func (q ViewQuery) temporalSortKey(item store.Item) float64 {
	if value, ok := q.Queries.DeadlineValue(item); ok {
		if boundary, err := value.DueBoundary(q.Queries.Context()); err == nil {
			return float64(boundary.UnixNano())
		}
	}
	if value, ok := q.Queries.ScheduledValue(item); ok {
		if instant, err := value.ReleaseInstant(q.Queries.Context()); err == nil {
			return float64(instant.UnixNano())
		}
	}
	return math.Inf(1)
}

// projectName is the enclosing project SECTION's title. Projects groups by the
// containing section in both flat and tree modes so subtasks cannot become
// pseudo-projects.
func (q ViewQuery) projectName(item store.Item) string {
	node := q.Queries.NodeFor(item)
	if node == nil {
		return ""
	}
	section := projectSection(node)
	if section == nil {
		return ""
	}
	return section.Title
}

// projectSection climbs past every task ancestor, open or closed.
func projectSection(node *taskquery.Node) *taskquery.Node {
	ancestor := node.Parent
	for ancestor != nil && ancestor.Task() {
		ancestor = ancestor.Parent
	}
	return ancestor
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
