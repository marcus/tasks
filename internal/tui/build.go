package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

// BuildRequest is everything the row builders need. It is a value, so building
// rows is a pure function of a read model plus the UI's own choices — which is
// what makes every row test a table test rather than a screen scrape.
type BuildRequest struct {
	View           string
	Styler         Styler
	Queries        *taskquery.Queries
	Items          []store.Item // already narrowed by the active `/` and `@` filters
	Tree           []*taskquery.Node
	UseTree        bool
	Collapsed      map[string]bool
	ShowDeferred   bool
	UrgentDays     int
	ContextFilters []string
	Projects       []taskquery.ProjectView
	IntakeCounts   IntakeCounts

	// Width is the list's content width in cells, excluding the renderer's
	// cursor gutter. Only the agenda uses it, and only to align its date
	// column; zero means "not measured" and every builder still produces
	// correct rows. See agenda.go.
	Width int
}

func (r BuildRequest) styler() Styler {
	if r.Styler == nil {
		return PlainStyler{}
	}
	return r.Styler
}

func (r BuildRequest) query(view string) ViewQuery {
	return NewViewQuery(view, r.Queries, r.UrgentDays, r.ShowDeferred, r.ContextFilters)
}

// BuildRows is the single entry point: `Views.rows`.
//
// UseTree false is the flat path a `/` search always takes (its result shape is
// what filtering expects). UseTree true is the outliner, which nests each
// anchor's visible subtree beneath it with indent and markers.
func BuildRows(request BuildRequest) []Row {
	switch request.View {
	case ViewOutline:
		return buildOutline(request)
	case ViewProjects:
		return buildProjects(request)
	}
	if request.UseTree {
		switch request.View {
		case ViewAgenda:
			return agendaTree(request)
		case ViewNext:
			return nextTree(request)
		case ViewQuadrants:
			return quadrantsTree(request)
		case ViewInbox:
			return combinedInbox(request, inboxTreeSection(request))
		}
		return nil
	}
	switch request.View {
	case ViewAgenda:
		return agendaFlat(request)
	case ViewNext:
		return nextFlat(request)
	case ViewQuadrants:
		return quadrantsFlat(request)
	case ViewInbox:
		return combinedInbox(request, inboxFlatSection(request))
	}
	return nil
}

// -- flat builders --------------------------------------------------------

func agendaFlat(request BuildRequest) []Row {
	query := request.query(ViewAgenda)
	byBucket := map[string][]Row{}
	for _, item := range query.Sort(query.Select(request.Items)) {
		item := item
		bucket := itemBucket(request, item)
		byBucket[bucket] = append(byBucket[bucket], Row{
			Text: priorityField(request, item) + taskBody(request, item),
			Item: &item,
		})
	}
	return agendaGrouped(request, byBucket)
}

func nextFlat(request BuildRequest) []Row {
	query := request.query(ViewNext)
	sections := []Section{}
	for _, group := range query.SortedGroups(groupItems(query, request.Items)) {
		rows := []Row{}
		for _, node := range group.Nodes {
			item := *node.Item
			rows = append(rows, Row{
				Text: priorityField(request, item) + taskBodyExcept(request, item, group.Key),
				Item: &item,
			})
		}
		sections = append(sections, Section{Label: group.Key, Slot: "context", Rows: rows})
	}
	return renderSections(request, sections, dateMeta(request))
}

func quadrantsFlat(request BuildRequest) []Row {
	query := request.query(ViewQuadrants)
	groups := groupItems(query, request.Items)
	return quadrantRows(request, groups, func(node *taskquery.Node) []Row {
		item := *node.Item
		return []Row{{Text: priorityField(request, item) + taskBody(request, item), Item: &item}}
	})
}

func inboxFlatSection(request BuildRequest) []Row {
	query := request.query(ViewInbox)
	rows := []Row{}
	for _, item := range query.Sort(query.Select(request.Items)) {
		item := item
		rows = append(rows, Row{Text: priorityField(request, item) + taskBody(request, item), Item: &item})
	}
	return rows
}

// -- tree builders --------------------------------------------------------

func agendaTree(request BuildRequest) []Row {
	query := request.query(ViewAgenda)
	anchors := []*taskquery.Node{}
	for _, node := range anchorRoots(request) {
		items := subtreeItems(request, node)
		eligible, inContext := false, false
		for _, item := range items {
			eligible = eligible || query.Eligible(item)
			inContext = inContext || query.ContextMatch(item)
		}
		if eligible && inContext {
			anchors = append(anchors, node)
		}
	}
	sort.SliceStable(anchors, func(i, j int) bool {
		left, right := anchors[i], anchors[j]
		leftDate, rightDate := agendaAnchorDate(request, query, left), agendaAnchorDate(request, query, right)
		if leftDate != rightDate {
			return leftDate < rightDate
		}
		return priorityKey(*left.Item) < priorityKey(*right.Item)
	})
	byBucket := map[string][]Row{}
	for _, anchor := range anchors {
		bucket := itemBucket(request, anchorDateItem(request, query, anchor))
		byBucket[bucket] = appendSubtree(request, byBucket[bucket], anchor, "",
			func(item store.Item) string { return priorityField(request, item) },
			func(item store.Item) string { return taskBody(request, item) })
	}
	return agendaGrouped(request, byBucket)
}

func nextTree(request BuildRequest) []Row {
	query := request.query(ViewNext)
	anchors := maximalAnchors(request, query)
	sections := []Section{}
	for _, group := range query.SortedGroups(query.GroupedNodes(anchors)) {
		rows := []Row{}
		for _, anchor := range group.Nodes {
			rows = appendSubtree(request, rows, anchor, "",
				func(item store.Item) string { return priorityField(request, item) },
				func(item store.Item) string { return taskBodyExcept(request, item, group.Key) })
		}
		sections = append(sections, Section{Label: group.Key, Slot: "context", Rows: rows})
	}
	return renderSections(request, sections, dateMeta(request))
}

func quadrantsTree(request BuildRequest) []Row {
	query := request.query(ViewQuadrants)
	anchors := query.SelectNodes(anchorRoots(request))
	groups := query.GroupedNodes(anchors)
	return quadrantRows(request, groups, func(node *taskquery.Node) []Row {
		return appendSubtree(request, nil, node, "",
			func(item store.Item) string { return priorityField(request, item) },
			func(item store.Item) string { return taskBody(request, item) })
	})
}

func inboxTreeSection(request BuildRequest) []Row {
	query := request.query(ViewInbox)
	rows := []Row{}
	for _, anchor := range query.SortNodes(maximalAnchors(request, query)) {
		rows = appendSubtree(request, rows, anchor, "",
			func(item store.Item) string { return priorityField(request, item) },
			func(item store.Item) string { return taskBody(request, item) })
	}
	return rows
}

// maximalAnchors is the NEXT/INBOX anchor rule: a matching node anchors unless
// some ancestor within the same rendered subtree also matches, in which case it
// rides that ancestor's subtree instead of anchoring a duplicate group.
func maximalAnchors(request BuildRequest, query ViewQuery) []*taskquery.Node {
	matching := query.SelectNodes(visibleNodes(request))
	anchors := []*taskquery.Node{}
	for _, node := range matching {
		if !matchingAncestor(request, query, node) {
			anchors = append(anchors, node)
		}
	}
	return anchors
}

func matchingAncestor(request BuildRequest, query ViewQuery, node *taskquery.Node) bool {
	ancestor := node.Parent
	for ancestor != nil && nodeVisible(request, ancestor) {
		if query.Matching(*ancestor.Item) {
			return true
		}
		ancestor = ancestor.Parent
	}
	return false
}

// -- inbox and approvals ---------------------------------------------------

func combinedInbox(request BuildRequest, inboxRows []Row) []Row {
	styler := request.styler()
	proposals := []store.Item{}
	for _, item := range request.Items {
		if !isProposedState(item.State) {
			continue
		}
		if len(request.ContextFilters) == 0 || request.query(ViewInbox).ContextMatch(item) {
			proposals = append(proposals, item)
		}
	}
	// APPROVALS carries its two keys in the rule's badge rather than as a
	// sentence after the label: the badge is where a section says its one fact,
	// and "there are 3 of these and a/r act on them" is one fact.
	approvals := fmt.Sprintf("%d", request.IntakeCounts.Approvals)
	if request.IntakeCounts.Approvals > 0 {
		approvals = "a·r  " + approvals
	}
	empty := headerRow(styler.Paint("muted", placeholderIndent+"Nothing pending approval"))
	inboxEmpty := headerRow(styler.Paint("muted", placeholderIndent+"Inbox empty. ✨"))
	return renderSections(request, []Section{
		{
			Label: "APPROVALS", Slot: "approval_section", Right: approvals,
			RightSlot: "approval_section", Rows: approvalRows(request, proposals), Empty: &empty,
		},
		{
			Label: "INBOX", Slot: "inbox_section",
			Right:     fmt.Sprintf("%d", request.IntakeCounts.Inbox),
			RightSlot: "inbox_section", Rows: inboxRows, Empty: &inboxEmpty,
		},
	}, dateMeta(request))
}

func approvalRows(request BuildRequest, proposals []store.Item) []Row {
	rows := []Row{}
	for _, item := range proposals {
		item := item
		rows = append(rows, Row{Text: priorityField(request, item) + taskBody(request, item), Item: &item})
	}
	return rows
}

// -- outline ---------------------------------------------------------------

func buildOutline(request BuildRequest) []Row {
	if !request.UseTree {
		rows := []Row{}
		for _, item := range request.Items {
			if isProposedState(item.State) {
				continue
			}
			item := item
			rows = append(rows, Row{Text: outlineBody(request, item), Item: &item})
		}
		return withMetaRows(request, rows)
	}
	rows := []Row{}
	for _, root := range request.Tree {
		rows = appendOutlineNode(request, rows, root, 0)
	}
	return withMetaRows(request, dropTrailingBlank(rows))
}

// appendOutlineNode walks one node. A SECTION becomes a section rule carrying
// the count of task rows beneath it — which is why its children are built
// first: the rule cannot state a count it has not finished computing.
func appendOutlineNode(request BuildRequest, rows []Row, node *taskquery.Node, depth int) []Row {
	indent := strings.Repeat("  ", depth)
	if node.Section() {
		body := []Row{}
		for _, child := range node.Children {
			body = appendOutlineNode(request, body, child, depth+1)
		}
		if len(rows) > 0 {
			rows = append(rows, chromeRow(""))
		}
		rows = append(rows, sectionRow(request, indent+node.Title, "section",
			fmt.Sprintf("%d", countSelectable(body)), "muted"))
		return append(rows, body...)
	}
	if isProposedState(node.Item.State) {
		for _, child := range node.Children {
			rows = appendOutlineNode(request, rows, child, depth)
		}
		return rows
	}
	folded := node.Item.ID != "" && request.Collapsed[node.Item.ID] && len(node.Children) > 0
	marker := MarkLeaf
	switch {
	case len(node.Children) == 0:
	case folded:
		marker = MarkCollapsed
	default:
		marker = MarkExpanded
	}
	body := outlineBody(request, *node.Item)
	if len(node.Children) > 0 {
		body = request.styler().Paint("outline_container", body)
	}
	head := priorityField(request, *node.Item)
	text := head + indent + marker + body
	if folded {
		text += foldedCount(request, outlineDescendantCount(node))
	}
	row := Row{Text: text, Item: node.Item, Node: node}
	if len(node.Children) > 0 {
		row.MarkerBegin = request.styler().Width(head) + request.styler().Width(indent)
		row.MarkerEnd = row.MarkerBegin + 2
	}
	rows = append(rows, row)
	if folded {
		return rows
	}
	for _, child := range node.Children {
		rows = appendOutlineNode(request, rows, child, depth+1)
	}
	return rows
}

func outlineDescendantCount(node *taskquery.Node) int {
	total := 0
	for _, child := range node.Children {
		if child.Task() {
			total++
		}
		total += outlineDescendantCount(child)
	}
	return total
}

// -- projects --------------------------------------------------------------

func buildProjects(request BuildRequest) []Row {
	styler := request.styler()
	if !request.UseTree {
		return projectsFlat(request)
	}
	if request.Projects == nil {
		return []Row{headerRow(styler.Paint("muted", "Project data needs the task tree."))}
	}
	if len(request.Projects) == 0 {
		message := "No projects — add sections under Projects."
		if len(request.Items) == 0 {
			message = "No active projects."
		}
		return []Row{headerRow(styler.Paint("muted", message))}
	}
	// Tree mode: Phase-1 ProjectView headers, outliner body under each. Empty
	// projects still list (header only) — header rollups come from ProjectView,
	// not from body-row counting, so deferred-only projects stay visible.
	query := request.query(ViewProjects)
	sections := []Section{}
	for _, group := range [][2]any{
		{"PROJECTS", "project"},
		{"AREAS", "area"},
	} {
		label, kind := group[0].(string), group[1].(string)
		members := []taskquery.ProjectView{}
		for _, project := range request.Projects {
			if project.Kind == kind {
				members = append(members, project)
			}
		}
		if len(members) == 0 {
			continue
		}
		rows := []Row{}
		for _, project := range members {
			project := project
			text, slot := projectMeta(request, project)
			rows = append(rows, withMeta(request,
				Row{Text: projectRow(request, project), Project: &project}, text, slot))
			for _, anchor := range query.SortNodes(anchorsForProject(request, project)) {
				rows = appendSubtree(request, rows, anchor, "  ",
					func(item store.Item) string { return priorityField(request, item) },
					func(item store.Item) string { return taskBody(request, item) })
			}
		}
		// The badge counts PROJECTS, not the task rows under them — a section of
		// projects is a list of projects.
		sections = append(sections, Section{
			Label: label, Slot: "section", Right: fmt.Sprintf("%d", len(members)), Rows: rows,
		})
	}
	return renderSections(request, sections, dateMeta(request))
}

// projectMeta is a project row's value in the shared column: how much of it is
// actionable, as `next/open`. A project is the one row whose "when" is not the
// question — "is anything moving here" is.
func projectMeta(request BuildRequest, project taskquery.ProjectView) (string, string) {
	slot := "muted"
	if project.NextCount == 0 {
		slot = "warning"
	}
	return fmt.Sprintf("%d/%d", project.NextCount, project.OpenCount), slot
}

// anchorsForProject keeps anchor roots that belong under a ProjectView section.
// Ownership is by section identity (ProjectView.Line → tree node), not title:
// titles can collide. Nested sub-sections under a project roll up into that
// project the same way ProjectView.TaskIDs do — so body membership matches the
// header rollup. Intermediate open tasks never head a group; only real
// project/area sections do.
func anchorsForProject(request BuildRequest, project taskquery.ProjectView) []*taskquery.Node {
	if request.Queries == nil {
		return nil
	}
	root := request.Queries.Tree().NodesByLine[project.Line]
	if root == nil {
		return nil
	}
	out := []*taskquery.Node{}
	for _, anchor := range anchorRoots(request) {
		if nodeUnder(anchor, root) {
			out = append(out, anchor)
		}
	}
	return out
}

// nodeUnder reports whether node sits at or under ancestor in the tree.
func nodeUnder(node, ancestor *taskquery.Node) bool {
	for current := node; current != nil; current = current.Parent {
		if current == ancestor {
			return true
		}
	}
	return false
}

// projectsFlat is the pre-outliner Projects body, kept for the `/` and `@`
// filter path whose flat shape the filter view relies on.
func projectsFlat(request BuildRequest) []Row {
	styler := request.styler()
	query := request.query(ViewProjects)
	groups := groupItems(query, request.Items)
	if len(groups) == 0 {
		return []Row{headerRow(styler.Paint("muted", "No active projects."))}
	}
	rows := []Row{}
	for _, group := range query.SortedGroups(groups) {
		items := make([]store.Item, 0, len(group.Nodes))
		for _, node := range group.Nodes {
			items = append(items, *node.Item)
		}
		rows = append(rows, headerRow(projectHeader(request, group.Key, items)))
		for _, item := range items {
			item := item
			rows = append(rows, Row{
				Text: priorityField(request, item) + taskBody(request, item), Item: &item,
			})
		}
		rows = append(rows, headerRow(""))
	}
	return withMetaRows(request, dropTrailingBlank(rows))
}

// projectRow is a project's own row: its name, and a warning when nothing in it
// is actionable. The counts live in the shared meta column as `next/open` — see
// projectMeta — so the row itself carries the one thing the column cannot: that
// this project has stalled.
func projectRow(request BuildRequest, project taskquery.ProjectView) string {
	styler := request.styler()
	head := styler.Paint("project", project.Title)
	if project.Stuck {
		head += styler.Paint("warning", "  ⚠ stuck")
	}
	return head
}

func projectHeader(request BuildRequest, name string, items []store.Item) string {
	styler := request.styler()
	nexts := 0
	for _, item := range items {
		if item.State == "NEXT" {
			nexts++
		}
	}
	head := styler.Paint("project", name) + "  " +
		styler.Paint("muted", fmt.Sprintf("%d open", len(items)))
	slot := "muted"
	if nexts == 0 {
		slot = "warning"
	}
	head += styler.Paint(slot, fmt.Sprintf(" · %d next", nexts))
	return head
}

// -- quadrant layout -------------------------------------------------------

func quadrantRows(request BuildRequest, groups map[string][]*taskquery.Node,
	body func(*taskquery.Node) []Row) []Row {
	// The four quadrants always paint, empty or not: the grid IS the view, and a
	// quadrant with nothing in it is the most useful thing the view can say.
	empty := headerRow(request.styler().Paint("muted", placeholderIndent+"—"))
	sections := make([]Section, 0, len(taskquery.QuadrantLabels))
	for _, pair := range taskquery.QuadrantLabels {
		rows := []Row{}
		for _, node := range groups[pair[0]] {
			rows = append(rows, body(node)...)
		}
		label, slot := quadrantLabel(pair)
		sections = append(sections, Section{Label: label, Slot: slot, Rows: rows, Empty: &empty})
	}
	return renderSections(request, sections, dateMeta(request))
}

// quadrantLabel splits the canonical heading into the rule's label and the tone
// it carries. The shared label keeps the CLI and the TUI from drifting; the
// section rule wants the short form, and the urgency ladder gives the colour.
func quadrantLabel(pair [2]string) (string, string) {
	label, _, _ := strings.Cut(pair[1], "  (")
	switch pair[0] {
	case "Q1":
		return label, "due_overdue"
	case "Q2":
		return label, "due_week"
	case "Q3":
		return label, "due_soon"
	default:
		return label, "muted"
	}
}

// -- the subtree walker ----------------------------------------------------

// appendSubtree is depth-first over the anchor's visible subtree, one Row per
// visible node. `base` is the view's leading indent; each level below the
// anchor drops a dim thread line so a descendant reads as hanging off its
// parent, then the marker column, then the per-view body. A container row is
// bolded so it reads like a heading. A collapsed node emits only its own row,
// with a muted hidden count.
//
// `lead` is an optional per-item field painted BEFORE the indent and thread —
// the agenda's priority column, which has to keep its screen column at every
// depth or it is not a column. Views without one pass nil.
func appendSubtree(request BuildRequest, rows []Row, anchor *taskquery.Node, base string,
	lead func(store.Item) string, body func(store.Item) string) []Row {
	styler := request.styler()
	walkSubtree(request, anchor, 0, func(node *taskquery.Node, depth int, marker string, folded bool) {
		thread := ""
		if depth > 0 {
			thread = styler.Paint("outline_thread", strings.Repeat("│ ", depth))
		}
		head := ""
		if lead != nil {
			head = lead(*node.Item)
		}
		line := body(*node.Item)
		if marker != MarkLeaf {
			line = styler.Paint("outline_container", line)
		}
		text := head + base + thread + marker + line
		if folded {
			text += styler.Paint("muted", fmt.Sprintf(" (%d)", visibleDescendantCount(request, node)))
		}
		row := Row{Text: text, Item: node.Item, Node: node}
		if marker != MarkLeaf {
			row.MarkerBegin = styler.Width(head) + styler.Width(base) + styler.Width(thread)
			row.MarkerEnd = row.MarkerBegin + 2
		}
		rows = append(rows, row)
	})
	return rows
}

func walkSubtree(request BuildRequest, node *taskquery.Node, depth int,
	visit func(*taskquery.Node, int, string, bool)) {
	kids := visibleChildren(request, node)
	folded := len(kids) > 0 && node.Item != nil && node.Item.ID != "" && request.Collapsed[node.Item.ID]
	marker := MarkLeaf
	switch {
	case len(kids) == 0:
	case folded:
		marker = MarkCollapsed
	default:
		marker = MarkExpanded
	}
	visit(node, depth, marker, folded)
	if folded {
		return
	}
	for _, child := range kids {
		walkSubtree(request, child, depth+1, visit)
	}
}

// -- tree helpers ----------------------------------------------------------

// topLevelTaskNodes is every task with no task ancestor, however many nested
// SECTIONS sit above it. It recurses through section children rather than
// unwrapping one level, so a project-under-a-project heading does not silently
// drop its tasks from every tree view.
func topLevelTaskNodes(nodes []*taskquery.Node) []*taskquery.Node {
	out := []*taskquery.Node{}
	for _, node := range nodes {
		if node.Section() {
			out = append(out, topLevelTaskNodes(node.Children)...)
			continue
		}
		out = append(out, node)
	}
	return out
}

// anchorRoots is the roots of the maximal OPEN subtrees, HOISTED through closed
// ancestors: an open task under a closed ancestor is promoted to anchor level
// instead of vanishing with its pruned parent. Deferred-hiding wins over
// hoisting — once inside a deferred subtree nothing anchors. Order is DFS
// pre-order, so a hoisted anchor lands where its closed ancestor sat.
func anchorRoots(request BuildRequest) []*taskquery.Node {
	roots := []*taskquery.Node{}
	var walk func(*taskquery.Node)
	walk = func(node *taskquery.Node) {
		if !node.Task() {
			return
		}
		visible := nodeVisible(request, node)
		parentVisible := node.Parent != nil && nodeVisible(request, node.Parent)
		if visible && !parentVisible {
			roots = append(roots, node)
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	for _, node := range topLevelTaskNodes(request.Tree) {
		walk(node)
	}
	return roots
}

// nodeVisible is: open, and either deferred rows are being shown or this one is
// available. A hidden node takes its whole subtree with it.
func nodeVisible(request BuildRequest, node *taskquery.Node) bool {
	if !node.Task() || !isOpenState(node.Item.State) {
		return false
	}
	return request.ShowDeferred || request.Queries.AvailabilityFor(*node.Item).Available()
}

func visibleChildren(request BuildRequest, node *taskquery.Node) []*taskquery.Node {
	out := []*taskquery.Node{}
	for _, child := range node.Children {
		if nodeVisible(request, child) {
			out = append(out, child)
		}
	}
	return out
}

// visibleNodes is every render-eligible node, hoisted: each anchor root plus
// its visible subtree, ignoring collapse. Because the anchor roots partition
// the open nodes, every visible task appears here exactly once — the
// every-open-task-once invariant the next and inbox anchor rules rely on.
func visibleNodes(request BuildRequest) []*taskquery.Node {
	out := []*taskquery.Node{}
	var walk func(*taskquery.Node)
	walk = func(node *taskquery.Node) {
		out = append(out, node)
		for _, child := range visibleChildren(request, node) {
			walk(child)
		}
	}
	for _, node := range anchorRoots(request) {
		walk(node)
	}
	return out
}

func visibleDescendantCount(request BuildRequest, node *taskquery.Node) int {
	total := 0
	for _, child := range visibleChildren(request, node) {
		total += 1 + visibleDescendantCount(request, child)
	}
	return total
}

func subtreeItems(request BuildRequest, anchor *taskquery.Node) []store.Item {
	out := []store.Item{*anchor.Item}
	for _, child := range visibleChildren(request, anchor) {
		out = append(out, subtreeItems(request, child)...)
	}
	return out
}

// agendaAnchorDate is the earliest deadline-first date anywhere in the anchor's
// visible subtree. An anchor's later own date must not hide an earlier
// qualifying descendant date.
func agendaAnchorDate(request BuildRequest, query ViewQuery, node *taskquery.Node) float64 {
	best := query.temporalSortKey(*node.Item)
	for _, child := range visibleChildren(request, node) {
		if candidate := agendaAnchorDate(request, query, child); candidate < best {
			best = candidate
		}
	}
	return best
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

func dropTrailingBlank(rows []Row) []Row {
	if len(rows) > 0 && rows[len(rows)-1].Text == "" && !rows[len(rows)-1].Selectable() {
		return rows[:len(rows)-1]
	}
	return rows
}
