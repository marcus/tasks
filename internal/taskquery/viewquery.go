package taskquery

import (
	"math"
	"sort"

	"github.com/marcus/tasks/internal/store"
)

// The per-view semantics every grouped read shares: which rows a named view
// admits, how it groups them, and in what order. It began life inside the TUI
// and moved here so the HTTP views read and the TUI tabs are computed by the
// SAME functions — a web client asking for the agenda gets the buckets the
// Agenda tab paints, rather than a port of them.

// The named views, spelled once. The TUI's tab keys are these values.
const (
	ViewAgenda    = "agenda"
	ViewNext      = "next"
	ViewQuadrants = "quadrants"
	ViewProjects  = "projects"
	ViewOutline   = "outline"
	ViewInbox     = "inbox"
)

// ViewQuery is the canonical semantic query for both the flat and the tree row
// builders — the port of Tui::Views::Query.
//
// It owns per-view eligibility, classification and grouping, and item ordering.
// Tree builders only decide which matching nodes become anchors and then render
// their descendants; they never re-decide what a view means. That split is what
// keeps the Agenda tab and `tasks agenda` from drifting apart.
type ViewQuery struct {
	View           string
	Queries        *Queries
	UrgentDays     int
	ShowDeferred   bool
	ContextFilters []string
	// ShowClosed reveals DONE and CANCELLED rows in the views whose rule keeps
	// them out by default — Outline and Projects. It is off in the zero value
	// and off out of NewViewQuery, so a caller that has never heard of the
	// toggle gets the working view rather than the graveyard.
	ShowClosed bool
}

// NewViewQuery builds the query for one view. Closed rows start hidden; a
// caller that wants them says so with ShowingClosed.
func NewViewQuery(view string, queries *Queries, urgentDays int, showDeferred bool,
	contextFilters []string) ViewQuery {
	if urgentDays <= 0 {
		urgentDays = DefaultUrgentDays
	}
	return ViewQuery{
		View: view, Queries: queries, UrgentDays: urgentDays,
		ShowDeferred: showDeferred, ContextFilters: contextFilters,
	}
}

// ShowingClosed returns the same query with the closed-row toggle set. It is a
// separate builder rather than a seventh constructor argument so the reveal
// stays opt-in for every caller that never mentions it.
func (q ViewQuery) ShowingClosed(show bool) ViewQuery {
	q.ShowClosed = show
	return q
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
	// Availability answers "can this be worked on now", which is a question only
	// LIVING work has: AvailabilityFor reports every closed row as unavailable by
	// construction. A row a view admitted BECAUSE it is finished must therefore
	// skip the gate, or the closed reveal would do nothing until Z was on too —
	// and Z is supposed to compose with it, not license it.
	if isClosed(item.State) {
		return true
	}
	return q.ShowDeferred || q.available(item)
}

// ViewRule is the per-view state and date test, availability aside.
//
// Outline and Projects are the two rules that read a toggle rather than only
// the item, because what they exclude is a CHOICE rather than a property of the
// row. Outline is the whole live tree with PROPOSED taken out unconditionally —
// undecided work belongs to Inbox/Approvals — and closed work taken out until
// the reader asks for it, because a triaged section is otherwise mostly
// cancelled leftovers nobody has swept. Projects is the same question from the
// other end: the tab is a commitment listing, so it anchors on open work and
// widens to finished work on request. Spelling both here rather than inside the
// row builders is what lets a headless dump hide the same rows the tabs do.
func (q ViewQuery) ViewRule(item store.Item) bool {
	switch q.View {
	case ViewOutline:
		if isProposed(item.State) {
			return false
		}
		// Hidden is the narrow case, on purpose: only a genuinely CLOSED row
		// goes away. A row with a state nobody recognizes is not history, it is
		// a defect, and this is the tab a reader opens to find one.
		return q.ShowClosed || !isClosed(item.State)
	case ViewAgenda:
		return isOpen(item.State) && (item.Deadline != "" || item.Scheduled != "")
	case ViewNext:
		return item.State == "NEXT"
	case ViewQuadrants:
		return isOpen(item.State)
	case ViewInbox:
		return item.State == "INBOX"
	case ViewProjects:
		return q.projectsState(item.State) && !unfiledProject(q.projectName(item))
	default:
		return false
	}
}

// projectsState is the Projects tab's state test, both halves stated as
// POSITIVE membership: open work always, finished work when the reader has
// asked for it.
//
// It is deliberately not Outline's "everything except closed". A state nobody
// recognizes — a hand-edited `TODOO`, a record with no state at all — is
// neither commitment nor history, so it is out of Projects in BOTH modes and
// the closed toggle never claims it: `C` reveals what you finished, and a
// broken row is not that. The Outline is the tab that surfaces a defect, and it
// still does, because isClosed reads the closed vocabulary directly rather
// than negating the open one.
func (q ViewQuery) projectsState(state string) bool {
	if isOpen(state) {
		return true
	}
	return q.ShowClosed && isClosed(state)
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
		return sortKey{instant: q.TemporalSortKey(item), priority: priorityKey(item)}
	case ViewNext:
		return sortKey{priority: priorityKey(item)}
	case ViewProjects:
		return sortKey{
			instant:  q.TemporalSortKey(item),
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
func (q ViewQuery) SelectNodes(nodes []*Node) []*Node {
	out := []*Node{}
	for _, node := range nodes {
		if node.Task() && q.Matching(*node.Item) {
			out = append(out, node)
		}
	}
	return out
}

// SortNodes orders nodes by their items' view key, stably.
func (q ViewQuery) SortNodes(nodes []*Node) []*Node {
	out := append([]*Node{}, nodes...)
	sort.SliceStable(out, func(left, right int) bool {
		return lessSortKey(q.sortKeyOf(*out[left].Item), q.sortKeyOf(*out[right].Item))
	})
	return out
}

// Group is one named bucket of nodes, already sorted.
type Group struct {
	Key   string
	Nodes []*Node
}

// GroupedNodes buckets nodes by GroupKeys and sorts each bucket.
func (q ViewQuery) GroupedNodes(nodes []*Node) map[string][]*Node {
	groups := map[string][]*Node{}
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

// GroupItems adapts the flat item path onto the node-shaped grouping API by
// wrapping each item in a throwaway node. The alternative is two copies of the
// grouping policy, one per shape, and they would drift.
func (q ViewQuery) GroupItems(items []store.Item) map[string][]*Node {
	held := append([]store.Item{}, items...)
	nodes := make([]*Node, 0, len(held))
	for index := range held {
		nodes = append(nodes, &Node{Item: &held[index]})
	}
	return q.GroupedNodes(nodes)
}

// SortedGroups orders the buckets. Most views order by key text; Projects
// orders by the soonest date anywhere in the bucket, then by key, and Inbox
// borrows that same sequence outright — see orderGroups.
func (q ViewQuery) SortedGroups(groups map[string][]*Node) []Group {
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
			if key := q.TemporalSortKey(*node.Item); key < best {
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
//
// The closed reveal is deliberately NOT threaded through either. The sequence
// is "which project is moving soonest", and a project that only rose into the
// ranking because someone pressed C to look at its history is not moving at
// all — the Inbox would reorder itself under a keystroke aimed at another tab.
func (q ViewQuery) projectRanks() map[string]int {
	ranks := map[string]int{}
	if q.Queries == nil {
		return ranks
	}
	projects := NewViewQuery(ViewProjects, q.Queries, q.UrgentDays, q.ShowDeferred, nil)
	for position, group := range projects.SortedGroups(projects.GroupItems(q.Queries.LiveItems())) {
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
			groups[slot].Nodes = append(groups[slot].Nodes, &Node{Item: &held[position]})
		}
	}
	return q.orderGroups(groups)
}

func (q ViewQuery) available(item store.Item) bool {
	return q.Queries.AvailabilityFor(item).Available()
}

// TemporalSortKey is the INSTANT a dated item comes due, as a float so an
// undated item can sort last by being +Inf. A deadline sorts by the moment it
// stops being on time; an available-from date sorts by the moment it opens.
func (q ViewQuery) TemporalSortKey(item store.Item) float64 {
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
func projectSection(node *Node) *Node {
	ancestor := node.Parent
	for ancestor != nil && ancestor.Task() {
		ancestor = ancestor.Parent
	}
	return ancestor
}
