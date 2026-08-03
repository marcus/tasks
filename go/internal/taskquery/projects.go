package taskquery

import (
	"sort"
	"strings"
	"time"

	"tasks-go/internal/record"
	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
)

// ProjectView is the canonical project/area resource: a section rolled up over
// its open, non-deferred descendant tasks at any depth.
//
// Kind distinguishes a project (a section under the top-level "Projects"
// heading) from an area (any other top-level list that currently holds open
// work). Stuck flags a project or area with no open NEXT action — including one
// with zero open tasks. Line is the physical coordinate for adapters; like the
// task resource it never appears in the JSON, keeping reusable resources free
// of file positions. NextDate is the soonest deadline-or-scheduled date across
// the rolled-up open tasks, the key the listing sorts on (absent sorts last).
// HeldCount counts open descendant tasks excluded from the rollup because they
// are deferred or held (own or inherited hold); the archive refusal treats them
// as open work too, so a parked-but-open project cannot be swept by accident.
type ProjectView struct {
	ID          string
	Title       string
	ParentID    string
	HasParentID bool
	Kind        string
	Line        int
	OpenCount   int
	NextCount   int
	NextDate    temporal.Date
	HasNextDate bool
	NextTime    APITime
	HasNextTime bool
	NextAt      time.Time
	Stuck       bool
	Body        string
	HasBody     bool
	TaskIDs     []string
	HeldCount   int
}

// Projects is every project and area as a rolled-up view.
//
// Projects are the section children of the top-level "Projects" heading, listed
// even when empty — an empty project is a commitment you have not started, and
// hiding it is how a project goes quietly missing. Areas are the other
// top-level lists that currently hold open, non-deferred work, excluding Inbox,
// the Projects heading itself, and everything inside its subtree (a nested
// sub-section rolls up into its project rather than listing beside it).
//
// Sorted projects before areas, then by soonest date (dateless last), then
// title, then file order.
func (q *Queries) Projects() []ProjectView {
	root, hasRoot := q.projectsRoot()
	views := []ProjectView{}
	for _, section := range q.liveSections() {
		if hasRoot && stringOf(section, "parent") == stringOf(root, "id") {
			views = append(views, q.buildProjectView(section, "project"))
		}
	}
	for _, section := range q.liveSections() {
		if !q.areaCandidate(section, root, hasRoot) {
			continue
		}
		view := q.buildProjectView(section, "area")
		if view.OpenCount > 0 {
			views = append(views, view)
		}
	}
	return sortProjects(views)
}

// ProjectView is a single project or area by section id, or ok=false when the
// id is not such a section: a task, Inbox, the Projects heading, a nested
// sub-section, or an area with no open work today.
func (q *Queries) ProjectView(id string) (ProjectView, bool) {
	var section record.Record
	found := false
	for _, candidate := range q.liveSections() {
		if stringOf(candidate, "id") == id && id != "" {
			section, found = candidate, true
			break
		}
	}
	if !found {
		return ProjectView{}, false
	}
	root, hasRoot := q.projectsRoot()
	if hasRoot && stringOf(section, "parent") == stringOf(root, "id") {
		return q.buildProjectView(section, "project"), true
	}
	if q.areaCandidate(section, root, hasRoot) {
		view := q.buildProjectView(section, "area")
		if view.OpenCount > 0 {
			return view, true
		}
	}
	return ProjectView{}, false
}

// liveSections is the live file's section records in file order.
func (q *Queries) liveSections() []record.Record {
	if q.sections != nil {
		return q.sections
	}
	sections := []record.Record{}
	for _, parsed := range q.snapshot.LiveRecords {
		if stringOf(parsed, "type") == "section" {
			sections = append(sections, parsed)
		}
	}
	q.sections = sections
	return sections
}

// projectsRoot is the top-level section titled "Projects" (case-insensitive).
// Its direct child sections are projects; its whole subtree is excluded from
// the area listing.
func (q *Queries) projectsRoot() (record.Record, bool) {
	if q.projectsRootReady {
		return q.projectsRootRecord, q.projectsRootFound
	}
	q.projectsRootReady = true
	for _, section := range q.liveSections() {
		if stringOf(section, "parent") != "" {
			continue
		}
		if strings.ToLower(strings.TrimSpace(stringOf(section, "title"))) == "projects" {
			q.projectsRootRecord, q.projectsRootFound = section, true
			return q.projectsRootRecord, true
		}
	}
	return record.Record{}, false
}

// areaCandidate is a top-level section that is neither Inbox nor the Projects
// heading. Being top-level already excludes every section inside the Projects
// subtree, whose members carry a parent.
func (q *Queries) areaCandidate(section, root record.Record, hasRoot bool) bool {
	if stringOf(section, "parent") != "" {
		return false
	}
	if hasRoot && stringOf(section, "id") == stringOf(root, "id") {
		return false
	}
	return strings.ToLower(strings.TrimSpace(stringOf(section, "title"))) != "inbox"
}

// projectDescendants is the section's descendant TASKS at any depth, in DFS
// order, split into the rollup and the parked remainder. Deferral is effective
// (own or inherited hold), so a task under a deferred project drops out of the
// rollup too; a future-scheduled task still counts as open work, because it is
// committed, only not yet startable.
func (q *Queries) projectDescendants(section record.Record) (open, held []store.Item) {
	node := q.tree.NodesByLine[section.Line]
	if node == nil {
		return nil, nil
	}
	var walk func(*Node)
	walk = func(current *Node) {
		if current.Item != nil && isOpen(current.Item.State) {
			if q.projectDeferred(*current.Item) {
				held = append(held, *current.Item)
			} else {
				open = append(open, *current.Item)
			}
		}
		for _, child := range current.Children {
			walk(child)
		}
	}
	walk(node)
	return open, held
}

func (q *Queries) projectDeferred(item store.Item) bool {
	reason := q.AvailabilityFor(item).Reason
	return reason == ReasonOnHold || reason == ReasonAncestorOnHold
}

func (q *Queries) buildProjectView(section record.Record, kind string) ProjectView {
	openItems, heldItems := q.projectDescendants(section)
	nextCount := 0
	for _, item := range openItems {
		if item.State == "NEXT" {
			nextCount++
		}
	}

	// The soonest boundary across the rollup, first-in-file breaking ties —
	// Ruby's min_by keeps the first minimum, and the row this date labels has to
	// be reproducible.
	var nextItem store.Item
	var nextValue temporal.Value
	hasNext := false
	var nextKey time.Time
	for _, item := range openItems {
		value, ok := q.DeadlineValue(item)
		if !ok {
			value, ok = q.ScheduledValue(item)
		}
		if !ok {
			continue
		}
		key := q.agendaSortKey(item)
		if !hasNext || key.Before(nextKey) {
			nextItem, nextValue, nextKey, hasNext = item, value, key, true
		}
	}

	taskIDs := []string{}
	for _, item := range openItems {
		taskIDs = append(taskIDs, item.ID)
	}

	view := ProjectView{
		ID: stringOf(section, "id"), Title: stringOf(section, "title"),
		Kind: kind, Line: section.Line,
		OpenCount: len(openItems), NextCount: nextCount,
		Stuck: nextCount == 0, TaskIDs: taskIDs, HeldCount: len(heldItems),
	}
	if parent := stringOf(section, "parent"); parent != "" {
		view.ParentID, view.HasParentID = parent, true
	}
	if body := stringOf(section, "body"); body != "" {
		view.Body, view.HasBody = body, true
	}
	if hasNext {
		view.NextDate, view.HasNextDate = nextValue.Date, true
		view.NextTime, view.HasNextTime = q.APITimeFor(nextValue, true)
		view.NextAt = q.temporalBoundary(nextItem, nextValue).UTC()
	}
	return view
}

// temporalBoundary is the instant a dated item's stamp names: a deadline stops
// being on time at its boundary, an available-from date opens at its release.
func (q *Queries) temporalBoundary(item store.Item, value temporal.Value) time.Time {
	if _, hasDeadline := q.DeadlineValue(item); hasDeadline {
		if boundary, err := q.dueBoundary(value); err == nil {
			return boundary
		}
		return time.Time{}
	}
	instant, err := value.ReleaseInstant(q.context)
	if err != nil {
		return time.Time{}
	}
	return instant
}

func sortProjects(views []ProjectView) []ProjectView {
	sorted := append([]ProjectView{}, views...)
	rank := func(kind string) int {
		if kind == "project" {
			return 0
		}
		return 1
	}
	sort.SliceStable(sorted, func(left, right int) bool {
		leftView, rightView := sorted[left], sorted[right]
		if rank(leftView.Kind) != rank(rightView.Kind) {
			return rank(leftView.Kind) < rank(rightView.Kind)
		}
		if leftView.HasNextDate != rightView.HasNextDate {
			return leftView.HasNextDate
		}
		if leftView.HasNextDate && !leftView.NextDate.Equal(rightView.NextDate) {
			return leftView.NextDate.Before(rightView.NextDate)
		}
		return leftView.Title < rightView.Title
	})
	return sorted
}
