package tui

import (
	"math"
	"sort"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
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
		return isOpenState(item.State) && !unfiledProject(q.projectName(item))
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
	case ViewInbox:
		// Intake groups by the SAME enclosing section Projects groups by — see
		// projectName — so a task filed under Aviator sits with the rest of the
		// Aviator intake rather than beside whatever chore shares its file line.
		// Everything with no project of its own collapses into ONE bucket, which
		// SortedGroups then keeps at the end of the block.
		return []string{q.intakeGroupKey(item)}
	default:
		return []string{""}
	}
}

// UnfiledIntake is the one bucket every intake row with no project of its own
// lands in: a task sitting directly in the file's Inbox section, and a task
// sitting under no section at all, are the same thing to a reader triaging
// them — unfiled. It is a display key as well as a bucket key, so it is spelled
// the way the file spells that section rather than as a marker word.
const UnfiledIntake = "Inbox"

// unfiledProject reports a project name that is not a project: the Inbox, or no
// enclosing section at all. Projects excludes those rows; intake gathers them
// into its trailing group. One spelling, so the two views cannot drift on what
// counts as filed.
func unfiledProject(name string) bool { return name == "" || name == UnfiledIntake }

// intakeGroupKey is an intake row's bucket: its project, or UnfiledIntake.
func (q ViewQuery) intakeGroupKey(item store.Item) string {
	name := q.projectName(item)
	if unfiledProject(name) {
		return UnfiledIntake
	}
	return name
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

// groupItems adapts the flat item path onto the node-shaped grouping API by
// wrapping each item in a throwaway node. The alternative is two copies of the
// grouping policy, one per shape, and they would drift.
func groupItems(query ViewQuery, items []store.Item) map[string][]*taskquery.Node {
	nodes := make([]*taskquery.Node, 0, len(items))
	for index := range items {
		nodes = append(nodes, &taskquery.Node{Item: &items[index]})
	}
	return query.GroupedNodes(nodes)
}

// SortedGroups orders the buckets. Most views order by key text; Projects
// orders by the soonest date anywhere in the bucket, then by key, and Inbox
// borrows that same sequence outright — see orderGroups.
func (q ViewQuery) SortedGroups(groups map[string][]*taskquery.Node) []Group {
	out := make([]Group, 0, len(groups))
	for key, nodes := range groups {
		out = append(out, Group{Key: key, Nodes: nodes})
	}
	return q.orderGroups(out)
}

// orderGroups is the group order alone, over buckets already built. It is
// separate from SortedGroups because the approvals queue arrives pre-ranked and
// must keep that rank INSIDE each bucket — see GroupItemsInOrder — while still
// laying the buckets out the way every other grouped view does.
func (q ViewQuery) orderGroups(groups []Group) []Group {
	out := append([]Group{}, groups...)
	if q.View != ViewProjects && q.View != ViewInbox {
		sort.SliceStable(out, func(left, right int) bool { return out[left].Key < out[right].Key })
		return out
	}
	// Inbox does not compute a project order of its own; it adopts the Projects
	// view's. Two orders derived from two different sets of rows would disagree —
	// the approvals block holds proposals, the accepted block holds captures, and
	// each would rank the same two projects by whatever dates its own handful of
	// rows carried. One borrowed sequence keeps the two blocks, and the two tabs,
	// telling the same story.
	ranks := map[string]int{}
	if q.View == ViewInbox {
		ranks = q.projectRanks()
	}
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
		// The unfiled bucket is a REMAINDER, not a project, so it goes last
		// whatever dates it happens to carry. Interleaving it would put "things
		// I have not decided where to put" in the middle of the themes, which is
		// the one place a triage pass cannot use it. Projects never produces the
		// bucket at all, so the term is inert there.
		leftUnfiled, rightUnfiled := out[left].Key == UnfiledIntake, out[right].Key == UnfiledIntake
		if leftUnfiled != rightUnfiled {
			return rightUnfiled
		}
		// A section the Projects view lists comes in that view's sequence. A
		// section it does not list — a holding pen that contains nothing but
		// proposals, say — has no place in that sequence, so it falls in behind
		// the projects and orders itself by the rule Projects itself uses.
		leftRank, leftKnown := ranks[out[left].Key]
		rightRank, rightKnown := ranks[out[right].Key]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown && leftRank != rightRank {
			return leftRank < rightRank
		}
		leftKey, rightKey := soonest(out[left]), soonest(out[right])
		if leftKey != rightKey {
			return leftKey < rightKey
		}
		return out[left].Key < out[right].Key
	})
	return out
}

// projectRanks is the Projects view's own group order, as key → position.
//
// It is measured over the WHOLE live store, unfiltered: a `/` search or an `@`
// context decides which groups are PAINTED, not what sequence they come in, and
// an order that reshuffled while a filter was typed would be no order at all.
func (q ViewQuery) projectRanks() map[string]int {
	ranks := map[string]int{}
	if q.Queries == nil {
		return ranks
	}
	projects := NewViewQuery(ViewProjects, q.Queries, q.UrgentDays, q.ShowDeferred, nil)
	for position, group := range projects.SortedGroups(groupItems(projects, q.Queries.LiveItems())) {
		ranks[group.Key] = position
	}
	return ranks
}

// GroupItemsInOrder buckets items by GroupKeys, PRESERVING the order they were
// given inside each bucket, and returns the buckets in the view's group order.
//
// It is the approvals queue's path: that list is already ranked by the core's
// shared triage order, and re-sorting a bucket by the view's own key would throw
// that ranking away. Eligibility is the caller's — the queue's rows are PROPOSED
// and no view rule admits them.
func (q ViewQuery) GroupItemsInOrder(items []store.Item) []Group {
	held := append([]store.Item{}, items...)
	groups := []Group{}
	index := map[string]int{}
	for position := range held {
		for _, key := range q.GroupKeys(held[position]) {
			slot, seen := index[key]
			if !seen {
				slot = len(groups)
				index[key] = slot
				groups = append(groups, Group{Key: key})
			}
			groups[slot].Nodes = append(groups[slot].Nodes, &taskquery.Node{Item: &held[position]})
		}
	}
	return q.orderGroups(groups)
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
