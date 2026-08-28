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
	View         string
	Styler       Styler
	Queries      *taskquery.Queries
	Items        []store.Item // already narrowed by the active `/` and `@` filters
	Tree         []*taskquery.Node
	UseTree      bool
	Collapsed    map[string]bool
	ShowDeferred bool
	// ShowRejected reveals recently declined proposals inside APPROVALS. It is
	// off by default: intake is a queue of undecided work, and a decision already
	// made is not part of it until the reviewer asks to see it.
	ShowRejected bool
	// ShowClosed reveals DONE and CANCELLED rows in the Outline. Off by
	// default: the outline is the whole live tree, and a section that has been
	// triaged but not yet swept to archive.jsonl is mostly closed leftovers.
	ShowClosed     bool
	UrgentDays     int
	ContextFilters []string
	// TextFilter is the active `/` search, lowercased by the caller's own rule
	// (plain title substring). Items already arrives narrowed by it; builders
	// that fetch rows from Queries directly (revealed rejects) must apply it
	// themselves so a filtered view never shows rows the filter hides.
	TextFilter   string
	Projects     []taskquery.ProjectView
	IntakeCounts IntakeCounts

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
	return NewViewQuery(view, r.Queries, r.UrgentDays, r.ShowDeferred, r.ContextFilters).
		ShowingClosed(r.ShowClosed)
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
			Text: urgencyBand(request, item) + taskBody(request, item),
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
				Text:          urgencyBand(request, item) + taskBody(request, item),
				Item:          &item,
				ContextExcept: group.Key,
			})
		}
		sections = append(sections, Section{Label: group.Key, Slot: "context", Rows: rows})
	}
	return renderSections(request, nextSections(request, sections), dateMeta(request))
}

func quadrantsFlat(request BuildRequest) []Row {
	query := request.query(ViewQuadrants)
	groups := groupItems(query, request.Items)
	return quadrantRows(request, groups, func(node *taskquery.Node) []Row {
		item := *node.Item
		return []Row{{Text: urgencyBand(request, item) + taskBody(request, item), Item: &item}}
	})
}

func inboxFlatSection(request BuildRequest) []Row {
	query := request.query(ViewInbox)
	return intakeGroupRows(request, query.SortedGroups(groupItems(query, request.Items)),
		oneTaskEach,
		func(node *taskquery.Node) []Row {
			item := *node.Item
			// The marker gutter is the inbox VIEW's column, not tree mode's: the
			// APPROVALS block above shares this list's left edge, and an edge that
			// moved when `/` dropped the view into flat mode would be no edge at all.
			return []Row{{
				Text: urgencyBand(request, item) + MarkLeaf + taskBody(request, item),
				Item: &item,
			}}
		})
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
			func(item store.Item) string { return urgencyBand(request, item) },
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
			from := len(rows)
			rows = appendSubtree(request, rows, anchor, "",
				func(item store.Item) string { return urgencyBand(request, item) },
				func(item store.Item) string { return taskBody(request, item) })
			for index := from; index < len(rows); index++ {
				rows[index].ContextExcept = group.Key
			}
		}
		sections = append(sections, Section{Label: group.Key, Slot: "context", Rows: rows})
	}
	return renderSections(request, nextSections(request, sections), dateMeta(request))
}

func quadrantsTree(request BuildRequest) []Row {
	query := request.query(ViewQuadrants)
	anchors := query.SelectNodes(anchorRoots(request))
	groups := query.GroupedNodes(anchors)
	return quadrantRows(request, groups, func(node *taskquery.Node) []Row {
		return appendSubtree(request, nil, node, "",
			func(item store.Item) string { return urgencyBand(request, item) },
			func(item store.Item) string { return taskBody(request, item) })
	})
}

func inboxTreeSection(request BuildRequest) []Row {
	query := request.query(ViewInbox)
	anchors := query.GroupedNodes(maximalAnchors(request, query))
	return intakeGroupRows(request, query.SortedGroups(anchors),
		func(anchor *taskquery.Node) int { return intakeTaskCount(request, query, anchor) },
		func(anchor *taskquery.Node) []Row {
			return appendSubtree(request, nil, anchor, "",
				func(item store.Item) string { return urgencyBand(request, item) },
				func(item store.Item) string { return taskBody(request, item) })
		})
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

// -- the next empty state --------------------------------------------------

// nextSections hands the Next builders their sections back, or — when nothing
// carries an explicit NEXT mark — the one block that explains the emptiness.
func nextSections(request BuildRequest, sections []Section) []Section {
	if len(sections) > 0 {
		return sections
	}
	return []Section{nextEmptyState(request)}
}

// nextEmptyState is the block a Next tab with no marked actions paints instead
// of a blank pane.
//
// An empty Next is not the same fact as an empty task list. Capture, approval
// and dating all land work in TODO, so the usual reason this list is empty is
// that nobody has NAMED a next action — while the agenda next door is full. A
// blank pane says "broken"; this block says which of the two it is, counts what
// the agenda is holding, and names both ways to mark a row.
//
// The count comes from the SAME items the tabs are showing — already narrowed
// by the active `/` and `@` filters — so the number here and the number of rows
// on Agenda cannot disagree.
func nextEmptyState(request BuildRequest) Section {
	styler := request.styler()
	dated := len(request.query(ViewAgenda).Select(request.Items))
	lines := []string{"No explicit next actions."}
	switch {
	case dated == 0:
		lines = append(lines, "No dated work on Agenda either.")
	case dated == 1:
		lines = append(lines, "1 dated item is waiting on Agenda.")
	default:
		lines = append(lines, fmt.Sprintf("%d dated items are waiting on Agenda.", dated))
	}
	// Short on purpose: the meta column takes eleven cells and the placeholder
	// indent seven, so a line much past fifty characters loses its tail — and
	// the tail here is the command.
	lines = append(lines, "Mark one with N, or: tasks state <ref> NEXT")
	rows := make([]Row, 0, len(lines))
	for _, line := range lines {
		// Padded like a row, as every placeholder is: it occupies rows' places,
		// and a frame that paints a background must find the same width under it.
		rows = append(rows, withMeta(request,
			headerRow(styler.Paint("muted", placeholderIndent+line)), "", ""))
	}
	return Section{Label: "NEXT", Slot: "context", Rows: rows}
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
	// Approvals is a decision QUEUE, so it is ranked by the core's shared triage
	// order — the same call `list --proposed` and `scope=proposed` make — rather
	// than left in file order. The row already carries the two facts that order
	// reads by: the priority letter leads the body, and the shared date column
	// spells the deadline, so a scan can see WHY row two follows row one.
	if request.Queries != nil {
		proposals = request.Queries.RankByPriorityThenDue(proposals)
	}
	// APPROVALS carries its two keys in the rule's badge rather than as a
	// sentence after the label: the badge is where a section says its one fact,
	// and "there are 3 of these and a/r act on them" is one fact.
	approvals := fmt.Sprintf("%d", request.IntakeCounts.Approvals)
	if request.IntakeCounts.Approvals > 0 {
		approvals = "a·r  " + approvals
	}
	rejects := rejectedRows(request)
	empty := headerRow(styler.Paint("muted", placeholderIndent+"Nothing pending approval"))
	if len(rejects) > 0 {
		approvals += styler.Paint("muted", "  ·  "+fmt.Sprintf("%d rejected", len(rejects)))
	}
	inboxEmpty := headerRow(styler.Paint("muted", placeholderIndent+"Inbox empty. ✨"))
	return renderSections(request, []Section{
		{
			Label: "APPROVALS", Slot: "approval_section", Right: approvals,
			RightSlot: "approval_section",
			Rows:      approvalSectionRows(request, proposals, rejects, empty), Empty: &empty,
		},
		{
			Label: "INBOX", Slot: "inbox_section",
			Right:     fmt.Sprintf("%d", request.IntakeCounts.Inbox),
			RightSlot: "inbox_section", Rows: inboxRows, Empty: &inboxEmpty,
		},
	}, dateMeta(request))
}

// approvalSectionRows keeps the "Nothing pending approval" placeholder honest:
// revealed rejects are decided work, so when they are the only rows, the queue
// itself is still empty and the placeholder must say so above them.
func approvalSectionRows(request BuildRequest, proposals []store.Item, rejects []Row, empty Row) []Row {
	rows := approvalRows(request, proposals)
	if len(rows) == 0 && len(rejects) > 0 {
		rows = []Row{empty}
	}
	return append(rows, rejects...)
}

// rejectedRows is the revealed tail of APPROVALS: recently declined proposals,
// newest first, dimmed and stamped with the day they were declined so they can
// never be mistaken for work still awaiting a decision. They are ordinary
// selectable rows, which is what makes restore one keystroke from intake.
//
// "Recent" is taskquery.RecentRejects — the same rule, from the same code, that
// `tasks list --rejected` prints.
func rejectedRows(request BuildRequest) []Row {
	if !request.ShowRejected || request.Queries == nil {
		return []Row{}
	}
	styler := request.styler()
	rows := []Row{}
	text := strings.ToLower(strings.TrimSpace(request.TextFilter))
	for _, item := range request.Queries.RecentRejects() {
		if len(request.ContextFilters) > 0 && !request.query(ViewInbox).ContextMatch(item) {
			continue
		}
		if text != "" && !strings.Contains(strings.ToLower(item.Title), text) {
			continue
		}
		if item.Source != store.SourceLive {
			// An archived decline is shown for the record but cannot be restored in
			// place, and a row that looks actionable and is not would be a lie.
			item := item
			rows = append(rows, Row{Text: styler.Paint("muted",
				placeholderIndent+item.Rejected+"  ✗ "+item.Title+"  (archived)"), Item: &item})
			continue
		}
		item := item
		rows = append(rows, Row{Text: styler.Paint("muted",
			placeholderIndent+item.Rejected+"  ✗ "+item.Title+"  (a restores)"), Item: &item})
	}
	return rows
}

// approvalRows paints the undecided queue. Every proposal is a leaf here — the
// queue lists each one on its own line whatever its parentage, so none of them
// folds — but the row still pays the marker gutter, because the INBOX block
// below shares this list's left edge and an edge only half the screen keeps is
// not an edge. A leaf marker claims no hit target, so the two cells stay inert
// under the mouse.
//
// The queue arrives already ranked by the core's triage order, so it is bucketed
// by GroupItemsInOrder, which keeps that rank inside each project rather than
// re-sorting it away.
func approvalRows(request BuildRequest, proposals []store.Item) []Row {
	query := request.query(ViewInbox)
	return intakeGroupRows(request, query.GroupItemsInOrder(proposals),
		oneTaskEach,
		func(node *taskquery.Node) []Row {
			item := *node.Item
			return []Row{{
				Text: urgencyBand(request, item) + MarkLeaf + taskBody(request, item),
				Item: &item,
			}}
		})
}

// intakeGroupRows lays one intake block out as its project groups: a heading per
// project, its rows beneath, blocks separated by a blank line, and the unfiled
// remainder last — the groups arrive already in the view's order, unfiled tail
// included, from ViewQuery.
//
// A group with no rows is dropped rather than headed — an empty heading costs
// two lines to say nothing, the same reason a Section with a nil Empty drops
// whole. And when the ONLY group is the unfiled one, the headings go too: a
// heading that repeats the section rule directly above it ("INBOX", then
// "Inbox") is noise, and an inbox nobody has filed yet is the common case.
//
// Selection is untouched by any of this. Headings are chrome, so they are not
// selectable, and every task row still carries the same Item and therefore the
// same durable id the cursor follows across a rebuild.
//
// `count` is how many TASKS a group's badge stands for, which is not how many
// rows it painted — see intakeTaskCount.
func intakeGroupRows(request BuildRequest, groups []Group, count func(*taskquery.Node) int,
	body func(*taskquery.Node) []Row) []Row {

	type block struct {
		key   string
		tasks int
		rows  []Row
	}
	blocks := []block{}
	for _, group := range groups {
		current := block{key: group.Key}
		for _, node := range group.Nodes {
			current.tasks += count(node)
			current.rows = append(current.rows, body(node)...)
		}
		if len(current.rows) == 0 {
			continue
		}
		blocks = append(blocks, current)
	}
	headed := len(blocks) > 1 || (len(blocks) == 1 && blocks[0].key != UnfiledIntake)
	out := []Row{}
	for _, current := range blocks {
		if !headed {
			out = append(out, current.rows...)
			continue
		}
		if len(out) > 0 {
			out = append(out, chromeRow(""))
		}
		out = append(out, groupRule(request, current.key, current.tasks))
		out = append(out, current.rows...)
	}
	return out
}

// oneTaskEach is the count rule for a list of rows that are each exactly one
// task: the approvals queue, and the flat inbox a `/` search drops the view
// into. Nothing rides along and nothing folds, so the group's tasks are its
// nodes.
func oneTaskEach(*taskquery.Node) int { return 1 }

// intakeTaskCount is how many INBOX TASKS an anchor's subtree holds — what a
// group heading in tree mode counts.
//
// It is the rule Model.intakeCounts spells for the section badge, applied one
// group at a time, and it is deliberately NOT a row count. Tree mode rides
// non-matching descendants along under a matching anchor for context, so rows
// overcount; and collapsing an anchor hides rows without emptying the group, so
// rows shrink under a fold. Either way the headings would stop summing to the
// badge above them, and a reader would be told two different numbers about the
// same list. So it walks the visible subtree, ignoring collapse, and counts only
// what the badge counts.
func intakeTaskCount(request BuildRequest, query ViewQuery, node *taskquery.Node) int {
	total := 0
	if node.Task() && query.Matching(*node.Item) {
		total = 1
	}
	for _, child := range visibleChildren(request, node) {
		total += intakeTaskCount(request, query, child)
	}
	return total
}

// -- outline ---------------------------------------------------------------

func buildOutline(request BuildRequest) []Row {
	if !request.UseTree {
		rows := []Row{}
		for _, item := range request.Items {
			if !outlineShows(request, item) {
				continue
			}
			item := item
			rows = append(rows, Row{Text: outlineBody(request, item), Item: &item})
		}
		return withMetaRows(request, rows)
	}
	rows := []Row{}
	for _, root := range request.Tree {
		rows = appendOutlineNode(request, rows, root, 0, "")
	}
	return withMetaRows(request, dropTrailingBlank(rows))
}

// appendOutlineNode walks one node.
//
// The redesign's central move: a SECTION is a selectable ROW, not a chrome
// rule. Every section — Inbox, a project, a calendar entry — carries its own
// fold marker, its rolled-up ProjectView (so rename, capture, complete and
// archive all work from the outline), and a mouse-hittable chevron. The rule
// line survives as decoration painted INSIDE the row, running from the label to
// the shared meta column where the section's task count sits, so the outline
// keeps the design system's section vocabulary while gaining OmniFocus-style
// direct manipulation of the sections themselves.
func appendOutlineNode(request BuildRequest, rows []Row, node *taskquery.Node, depth int, band string) []Row {
	indent := strings.Repeat("  ", depth)
	if node.Section() {
		// Only a TOP-LEVEL section opens with a blank line. A nested project
		// heading is part of its parent's block, and spacing every one of them
		// turned a list of four projects into a screenful of air — the reader
		// scrolls past whitespace looking for the next word.
		if len(rows) > 0 && depth == 0 {
			rows = append(rows, chromeRow(""))
		}
		rows = append(rows, outlineSectionRow(request, node, depth))
		if node.ID != "" && request.Collapsed[node.ID] && outlineRenders(request, node) {
			return rows
		}
		if banded, ok := outlineUrgencyBands(request, node, depth); ok {
			return append(rows, banded...)
		}
		for _, child := range node.Children {
			rows = appendOutlineNode(request, rows, child, depth+1, band)
		}
		return rows
	}
	// A row the view is not showing — a proposal, or a closed task while the
	// toggle is off — is TRANSPARENT rather than pruned: its children carry on
	// at the same depth. That is what keeps an open task from vanishing under a
	// parent someone completed, and it is the same hoisting the other tree
	// views do through anchorRoots.
	if !outlineShows(request, *node.Item) {
		for _, child := range node.Children {
			rows = appendOutlineNode(request, rows, child, depth, band)
		}
		return rows
	}
	// Foldability is about what PAINTS, not about what the file holds: a task
	// whose only children are hidden renders nothing under it, so it wears a
	// leaf's marker and does not offer a fold that would hide nothing.
	expandable := outlineRenders(request, node)
	folded := expandable && node.ID != "" && request.Collapsed[node.ID]
	marker := MarkLeaf
	switch {
	case !expandable:
	case folded:
		marker = MarkCollapsed
	default:
		marker = MarkExpanded
	}
	body := outlineBody(request, *node.Item)
	if expandable {
		body = request.styler().Paint("outline_container", body)
	}
	head := urgencyBandIn(request, *node.Item, band)
	text := head + indent + marker + body
	if folded {
		text += foldedCount(request, outlineDescendantCount(request, node))
	}
	row := Row{Text: text, Item: node.Item, Node: node}
	if expandable {
		row.MarkerBegin = request.styler().Width(head) + request.styler().Width(indent)
		row.MarkerEnd = row.MarkerBegin + 2
	}
	rows = append(rows, row)
	if folded {
		return rows
	}
	for _, child := range node.Children {
		rows = appendOutlineNode(request, rows, child, depth+1, band)
	}
	return rows
}

// urgencyBands are the outline's within-section groups, in painted order. They
// are the SAME three-way split the band glyph paints — a sub-rule is the band's
// legend, not a second grouping the reader has to learn.
var urgencyBands = []struct{ Key, Label, Slot string }{
	{"overdue", "overdue", "due_overdue"},
	{"today", "today", "due_soon"},
	{"later", "later", "outline_thread"},
}

// bandSlots is urgencyBands by key — the stripe colour every row in a band
// wears, including the rows that carry no date of their own. A stripe with
// holes in it is not a stripe; the band rule promises a run of rows, and each
// row has to continue it or the promise reads as a rendering fault.
var bandSlots = func() map[string]string {
	out := map[string]string{}
	for _, band := range urgencyBands {
		out[band.Key] = band.Slot
	}
	return out
}()

// outlineUrgencyBands splits a section's tasks into overdue / today / later,
// each under its own sub-rule.
//
// It applies only to a section whose children are ALL tasks — a plain GTD list
// like Inbox or Next Actions. A section that contains sub-sections (the
// Projects heading, the calendar) is already grouped by something the author
// chose, and a second grouping cut across it would fight the first.
//
// It also declines when everything lands in one band: a lone `later` rule over
// a list where nothing is due says only that nothing is due, which the absence
// of any red band already said. ok=false means "render this section normally".
func outlineUrgencyBands(request BuildRequest, node *taskquery.Node, depth int) ([]Row, bool) {
	if len(node.Children) == 0 {
		return nil, false
	}
	for _, child := range node.Children {
		if !child.Task() {
			return nil, false
		}
	}
	byBand := map[string][]Row{}
	for _, child := range node.Children {
		band := outlineBandKey(request, child)
		byBand[band] = appendOutlineNode(request, byBand[band], child, depth+1, bandSlots[band])
	}
	filled := 0
	for _, band := range urgencyBands {
		if len(byBand[band.Key]) > 0 {
			filled++
		}
	}
	if filled < 2 {
		return nil, false
	}
	rows := []Row{}
	for _, band := range urgencyBands {
		body := byBand[band.Key]
		if len(body) == 0 {
			continue
		}
		if len(rows) > 0 {
			rows = append(rows, chromeRow(""))
		}
		rows = append(rows, outlineBandRule(request, band.Label, band.Slot, countSelectable(body)))
		rows = append(rows, body...)
	}
	return rows, true
}

// outlineBandKey is the band a subtree sits in: the most urgent thing anywhere
// in it, so a parent never sorts calmer than a child it is hiding nothing from.
func outlineBandKey(request BuildRequest, node *taskquery.Node) string {
	best := "later"
	var walk func(*taskquery.Node)
	walk = func(current *taskquery.Node) {
		if current.Item != nil {
			// The same deadline-only rule the band glyph uses — see bandDays —
			// so a row's band and the sub-rule it sits under can never disagree
			// about how urgent it is.
			if days, ok := bandDays(request, *current.Item); ok {
				switch {
				case days < 0:
					best = "overdue"
				case days == 0 && best != "overdue":
					best = "today"
				}
			}
		}
		for _, child := range current.Children {
			walk(child)
		}
	}
	walk(node)
	return best
}

// outlineBandRule is a band's sub-rule: the band glyph in the band's own colour,
// the label, a dim fill, and the count on the shared meta column. It is quieter
// than a section rule by design — a band divides a block, it does not open one.
func outlineBandRule(request BuildRequest, label, slot string, count int) Row {
	styler := request.styler()
	// A band rule is chrome, so it is painted flush to the pane edge and buys
	// back the cursor field itself. It takes NO depth indent: the band column is
	// the same column on every row at every depth, and a rule whose glyph does
	// not sit on that column is not the top of the stripe it claims to head.
	indent := strings.Repeat(" ", CursorField)
	return subRule(request, indent+styler.Paint(slot, Band)+" "+styler.Paint("muted", label), count)
}

// outlineSectionRow builds one selectable section row: chevron, title, an
// inline date range for a calendar entry, the rule fill, and the task count in
// the shared meta column.
//
// The row wears the same three-cell head every task row wears (a section has
// no priority letter, so its head is blank), which is what lines a section's
// chevron up with its child rows' markers one indent deeper — the whole view
// reads as ONE tree rather than as rules with lists between them.
func outlineSectionRow(request BuildRequest, node *taskquery.Node, depth int) Row {
	styler := request.styler()
	expandable := outlineRenders(request, node)
	folded := expandable && node.ID != "" && request.Collapsed[node.ID]
	marker := MarkLeaf
	switch {
	case !expandable:
	case folded:
		marker = MarkCollapsed
	default:
		marker = MarkExpanded
	}
	title, start, end, hasDates := sectionDateRange(node.Title)
	slot := "section"
	if depth > 0 {
		slot = "project"
	}
	// A section heading spends the BAND column on its own chevron. A section is
	// not due; the cell that would carry its urgency is free, and putting the
	// chevron there is what pulls the headings out to the left edge where the
	// design has them, one step outside the rows they contain.
	head := strings.Repeat("  ", depth)
	text := head + marker + styler.Paint(slot, title)
	if hasDates {
		if human := humanDateRange(request, start, end); human != "" {
			text += "  " + styler.Paint("muted", human)
		}
	}
	row := Row{Text: text, Node: node}
	if request.Queries != nil {
		if view, ok := request.Queries.SectionView(node.ID); ok {
			held := view
			row.Project = &held
		}
	}
	if expandable {
		row.MarkerBegin = styler.Width(head)
		row.MarkerEnd = row.MarkerBegin + 2
	}
	badge := outlineSectionBadge(request, node)
	// The rule fill runs to one cell before the count, the same way every other
	// rule in the view does — see ruledHead. It is painted only when the frame
	// is wide enough for the meta column at all; narrow frames degrade to the
	// bare label, exactly as section rules do.
	if left, ok := metaColumns(request, 0); ok && badge != "" {
		row.Text = ruledHead(request, row.Text, badge, "muted", left)
		return row
	}
	return withMeta(request, row, badge, "muted")
}

// outlineShows is the Outline's per-row rule, asked of ViewQuery rather than
// spelled here: PROPOSED never appears (Inbox/Approvals owns undecided work),
// and DONE/CANCELLED appear only while the reader has asked for them.
//
// It goes through the shared query for the same reason every other view's rule
// does — so a headless outline dump and this tab cannot come to disagree about
// what "the outline" means.
func outlineShows(request BuildRequest, item store.Item) bool {
	return request.query(ViewOutline).ViewRule(item)
}

// outlineRenders reports whether anything at all paints beneath a node.
//
// It is NOT `len(node.Children) > 0`: a hidden row is transparent, so a closed
// task can still hoist an open descendant into view, and a task whose only
// children are hidden paints nothing under it at all. Fold markers, fold state
// and the mouse's chevron target all key off this, because a chevron that opens
// onto nothing is a lie about the tree.
func outlineRenders(request BuildRequest, node *taskquery.Node) bool {
	for _, child := range node.Children {
		if child.Section() || outlineShows(request, *child.Item) {
			return true
		}
		if outlineRenders(request, child) {
			return true
		}
	}
	return false
}

// outlineSectionBadge is a section's value in the shared meta column.
//
// The count is what the section is SHOWING — never what the file holds — so it
// agrees with the rows under it whatever the toggle is set to. What the toggle
// is holding back is then named outright, `4 · 2 closed`, rather than folded
// into the number: a badge that quietly counted hidden rows would send the
// reader looking for work that is not on the screen, and a section whose
// children are ALL closed would otherwise read as an empty project rather than
// a finished one.
func outlineSectionBadge(request BuildRequest, node *taskquery.Node) string {
	badge := ""
	if count := outlineTaskCount(request, node); count > 0 {
		badge = fmt.Sprintf("%d", count)
	}
	hidden := outlineHiddenClosedCount(request, node)
	if hidden == 0 {
		return badge
	}
	leftover := fmt.Sprintf("· %d closed", hidden)
	if badge == "" {
		return leftover
	}
	return badge + " " + leftover
}

// outlineTaskCount is the section badge's number: every task anywhere beneath
// that the current filter is showing, folded or not. It counts TASKS rather
// than rows so collapsing a subtree cannot make a section look emptier than it
// is.
func outlineTaskCount(request BuildRequest, node *taskquery.Node) int {
	total := 0
	for _, child := range node.Children {
		if child.Task() && outlineShows(request, *child.Item) {
			total++
		}
		total += outlineTaskCount(request, child)
	}
	return total
}

// outlineHiddenClosedCount is how many closed tasks the toggle is holding back
// anywhere beneath a node — the number behind a section's `· N closed` marker.
// With the toggle on it is zero by construction: nothing is being held back.
func outlineHiddenClosedCount(request BuildRequest, node *taskquery.Node) int {
	if request.ShowClosed {
		return 0
	}
	total := 0
	for _, child := range node.Children {
		if child.Task() && isClosedState(child.Item.State) {
			total++
		}
		total += outlineHiddenClosedCount(request, child)
	}
	return total
}

// outlineDescendantCount is the dim count a folded row carries: the tasks that
// fold hid, which is the tasks the view would otherwise be showing.
func outlineDescendantCount(request BuildRequest, node *taskquery.Node) int {
	total := 0
	for _, child := range node.Children {
		if child.Task() && outlineShows(request, *child.Item) {
			total++
		}
		total += outlineDescendantCount(request, child)
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
	// Tree mode (design 6b): the outline only shows what is LIVE. A project with
	// work under it is a foldable heading with its outliner body beneath; every
	// project with nothing to show folds together into ONE trailing row naming
	// them, so a long tail of dormant projects costs a line rather than a
	// screenful — and the section badge says how much of the list is moving.
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
		dormant := []taskquery.ProjectView{}
		for _, project := range members {
			project := project
			anchors := query.SortNodes(anchorsForProject(request, project))
			if len(anchors) == 0 {
				dormant = append(dormant, project)
				continue
			}
			folded := project.ID != "" && request.Collapsed[project.ID]
			marker := MarkExpanded
			if folded {
				marker = MarkCollapsed
			}
			text, slot := projectMeta(request, project)
			row := Row{
				Text:    projectRow(request, project, marker),
				Project: &project,
				Node:    projectNode(request, project),
			}
			row.MarkerBegin = BandField
			row.MarkerEnd = row.MarkerBegin + 2
			rows = append(rows, withMeta(request, row, text, slot))
			if folded {
				continue
			}
			for _, anchor := range anchors {
				// outlineBody, not taskBody: a task under a project heading is
				// the same row the outline shows, state glyph included. Two
				// views of one tree must not speak two vocabularies.
				rows = appendSubtree(request, rows, anchor, "  ",
					func(item store.Item) string { return urgencyBand(request, item) },
					func(item store.Item) string { return outlineBody(request, item) })
			}
		}
		if len(dormant) > 0 {
			rows = append(rows, withMeta(request,
				dormantProjectsRow(request, dormant), "no tasks", "muted"))
		}
		// The badge counts PROJECTS, not the task rows under them — a section of
		// projects is a list of projects — and names the live share when part of
		// the list has gone quiet.
		//
		// Design 6b spells this "1 open of 4"; the shared meta column is ten
		// cells, which that phrasing overruns and the frame then truncates. The
		// ratio form says the same thing in eight, and matches the `next/open`
		// ratio the project rows already carry one column below.
		right := fmt.Sprintf("%d", len(members))
		if len(dormant) > 0 {
			right = fmt.Sprintf("%d/%d open", len(members)-len(dormant), len(members))
		}
		sections = append(sections, Section{
			Label: label, Slot: "section", Right: right, Rows: rows,
		})
	}
	return renderSections(request, sections, dateMeta(request))
}

// dormantProjectsRow is design 6b's collapsed tail: every project with nothing
// live under it, named on one line. It is deliberately NOT selectable — it
// stands for several projects at once, so there is no single thing to act on;
// it exists to prove those projects are still there.
func dormantProjectsRow(request BuildRequest, projects []taskquery.ProjectView) Row {
	styler := request.styler()
	titles := make([]string, 0, len(projects))
	for _, project := range projects {
		titles = append(titles, project.Title)
	}
	head := strings.Repeat(" ", BandField) + styler.Paint("muted", MarkCollapsed)
	return headerRow(head + styler.Paint("muted", strings.Join(titles, " · ")))
}

// projectNode is the tree node a project row folds. Selection and folding both
// speak node identity, so a project heading carries the same node its tasks
// hang off.
func projectNode(request BuildRequest, project taskquery.ProjectView) *taskquery.Node {
	if request.Queries == nil {
		return nil
	}
	return request.Queries.Tree().NodesByLine[project.Line]
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
	return anchorsUnder(request, request.Queries.Tree().NodesByLine[project.Line])
}

// anchorsUnder is every anchor root sitting at or under a tree node.
func anchorsUnder(request BuildRequest, root *taskquery.Node) []*taskquery.Node {
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
				Text: urgencyBand(request, item) + taskBody(request, item), Item: &item,
			})
		}
		rows = append(rows, headerRow(""))
	}
	return withMetaRows(request, dropTrailingBlank(rows))
}

// projectRow is a project's own row: a fold marker, its name, and a warning
// when nothing in it is actionable. The counts live in the shared meta column
// as `next/open` — see projectMeta — so the row itself carries the one thing
// the column cannot: that this project has stalled.
//
// The row wears the blank priority head every task row wears, so the project's
// chevron lands in the same screen column its children's markers do and the
// section reads as one tree.
func projectRow(request BuildRequest, project taskquery.ProjectView, marker string) string {
	styler := request.styler()
	head := strings.Repeat(" ", BandField) + marker + styler.Paint("project", project.Title)
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

func dropTrailingBlank(rows []Row) []Row {
	if len(rows) > 0 && rows[len(rows)-1].Text == "" && !rows[len(rows)-1].Selectable() {
		return rows[:len(rows)-1]
	}
	return rows
}
