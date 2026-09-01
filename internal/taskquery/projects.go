package taskquery

import (
	"sort"
	"strings"
	"time"

	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
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
	State       string
	Closed      string
	HasClosed   bool
}

// Closed reports whether the project section is closed (DONE or CANCELLED).
func (v ProjectView) ClosedView() bool { return v.HasClosed }

// Open reports whether the project is still open.
func (v ProjectView) Open() bool { return !v.HasClosed }

// Projects is every project and area as a rolled-up view.
//
// Projects are the section children of the top-level "Projects" heading, listed
// even when empty — an empty project is a commitment you have not started, and
// hiding it is how a project goes quietly missing. Areas are the other
// top-level lists that currently hold open, non-deferred work, excluding the
// reserved GTD lists (see reservedListTitles), the Projects heading itself, and
// everything inside its subtree (a nested sub-section rolls up into its project
// rather than listing beside it).
//
// Sorted projects before areas, then by soonest date (dateless last), then
// title, then file order.
func (q *Queries) Projects() []ProjectView {
	return q.ProjectsIncluding(false)
}

// ProjectsIncluding is Projects with an explicit closed filter. When
// includeClosed is false, closed projects are omitted — the default for every
// existing caller. When true, they are included.
func (q *Queries) ProjectsIncluding(includeClosed bool) []ProjectView {
	root, hasRoot := q.projectsRoot()
	views := []ProjectView{}
	for _, section := range q.liveSections() {
		if hasRoot && stringOf(section, "parent") == stringOf(root, "id") {
			view := q.buildProjectView(section, "project")
			if !includeClosed && view.HasClosed {
				continue
			}
			views = append(views, view)
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

// ReservedLists is every addressable reserved GTD list — the top-level sections
// the Projects listing deliberately omits, minus Inbox, which stays
// unaddressable. Callers that resolve a section by ref use it to widen the
// listing's candidate set without widening the listing itself.
func (q *Queries) ReservedLists() []ProjectView {
	root, hasRoot := q.projectsRoot()
	views := []ProjectView{}
	for _, section := range q.liveSections() {
		title := stringOf(section, "title")
		if stringOf(section, "parent") != "" || !reservedList(title) || isInboxTitle(title) {
			continue
		}
		if hasRoot && stringOf(section, "id") == stringOf(root, "id") {
			continue
		}
		views = append(views, q.buildProjectView(section, "list"))
	}
	return views
}

// ProjectView is a single project, area or reserved GTD list by section id, or
// ok=false when the id is not such a section: a task, the Projects heading, a
// nested sub-section, or an area with no open work today.
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
	// A reserved GTD list is excluded from the LISTING, not from resolution: it
	// is still a real section a caller can name, rename, complete or archive,
	// and dropping it here would leave a heading the TUI can still act on but
	// no CLI or API caller can address. It resolves as kind "list".
	// Inbox is the one exception: it is the capture bucket, and renaming,
	// completing or archiving it would take the destination every unfiled task
	// lands in. It has always been unresolvable and stays so.
	if stringOf(section, "parent") == "" && reservedList(stringOf(section, "title")) &&
		!isInboxTitle(stringOf(section, "title")) &&
		!(hasRoot && stringOf(section, "id") == stringOf(root, "id")) {
		return q.buildProjectView(section, "list"), true
	}
	if q.areaCandidate(section, root, hasRoot) {
		view := q.buildProjectView(section, "area")
		if view.OpenCount > 0 {
			return view, true
		}
	}
	return ProjectView{}, false
}

// SectionView is a rolled-up view of ANY live section by id — not just the
// projects and areas the Projects listing admits. The outline renders every
// section as a selectable row, and the row's actions (rename, capture,
// complete, archive) all take a bare section id, so the view exists for
// sections the listing would exclude: Inbox, the Projects heading, nested
// sub-sections, and areas with no open work.
//
// Kind is resolved the same way the listing resolves it — "project" for a
// child of the Projects root, "area" for any other top-level section — and
// falls back to "list" for everything nested deeper.
func (q *Queries) SectionView(id string) (ProjectView, bool) {
	if id == "" {
		return ProjectView{}, false
	}
	for _, section := range q.liveSections() {
		if stringOf(section, "id") != id {
			continue
		}
		root, hasRoot := q.projectsRoot()
		kind := "list"
		switch {
		case hasRoot && stringOf(section, "parent") == stringOf(root, "id"):
			kind = "project"
		case stringOf(section, "parent") == "" && !reservedList(stringOf(section, "title")):
			kind = "area"
		}
		return q.buildProjectView(section, kind), true
	}
	return ProjectView{}, false
}

// liveSections is the live file's section records in file order.
func (q *Queries) liveSections() []record.Record {
	if q.sections != nil {
		return q.sections
	}
	sections := []record.Record{}
	for _, parsed := range q.snapshot.LiveRecords() {
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
	return !reservedList(stringOf(section, "title"))
}

// reservedListTitles are the top-level GTD lists that are NOT areas of
// responsibility: they are saved queries that happen to be spelled as parents.
//
// Membership in each is already derivable from a task's own fields — Next
// Actions is `state == NEXT`, Waiting For is `state == WAITING`, Someday /
// Maybe is the own defer marker that `tasks someday` sets WITHOUT moving the
// record. Listing them beside real projects says a task is committed work when
// the data says the opposite, so they resolve to kind "list" and the Projects
// listing skips them. They remain ordinary sections on disk, keep their tasks,
// and still render in the outline.
//
// Matching is by title because sections carry no role of their own yet; a
// `role` field on the section record is the durable fix.
var reservedListTitles = map[string]bool{
	"inbox": true, "next actions": true, "next": true,
	"waiting for": true, "waiting": true,
	"someday / maybe": true, "someday/maybe": true, "someday": true,
}

func reservedList(title string) bool {
	return reservedListTitles[strings.ToLower(strings.TrimSpace(title))]
}

func isInboxTitle(title string) bool {
	return strings.ToLower(strings.TrimSpace(title)) == "inbox"
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
	if state := stringOf(section, "state"); state != "" {
		view.State = state
		if closed := stringOf(section, "closed"); closed != "" {
			view.Closed = closed
			view.HasClosed = true
		}
	}
	// A closed project is never stuck: no open NEXT is the point of a
	// commitment that is over.
	if view.HasClosed {
		view.Stuck = false
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
		// Closed projects sort after open ones. A dormant tail keeps meaning "an
		// open project with nothing live under it", so closed rows never join it.
		if leftView.HasClosed != rightView.HasClosed {
			return !leftView.HasClosed
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
