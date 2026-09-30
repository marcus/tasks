package taskquery

import (
	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/store"
)

// The Outline as data: the whole live tree in file order, sections and tasks
// interleaved exactly as the file holds them, with the Outline tab's rules
// about what is shown. The TUI builds its rows from the same predicates below,
// and the HTTP outline read returns OutlineNodes, so a web client rebuilding
// the tab never has to rediscover how a closed parent hoists its open children.

// Section kinds. They name the ROLE the model already gives a section, not a
// stored field — sections carry no role of their own yet, so the Inbox, the
// Projects root and the saved GTD lists are recognised by title exactly as the
// Projects listing recognises them.
const (
	SectionInbox        = "inbox"
	SectionProjectsRoot = "projects_root"
	SectionProject      = "project"
	SectionArea         = "area"
	SectionList         = "list"
	SectionSubsection   = "subsection"
)

// SectionInfo is one live section's own fields plus its role.
//
// Kind is structural: an "area" here is any top-level section that is not a
// reserved list, whether or not it holds open work today. The Projects
// listing's narrower sense — an area appears only while it has open work — is
// a rule about that listing, not about the section.
type SectionInfo struct {
	ID        string
	Title     string
	ParentID  string
	Kind      string
	Body      string
	HasBody   bool
	State     string
	Closed    string
	HasClosed bool
}

// Sections is every live section in file order.
func (q *Queries) Sections() []SectionInfo {
	sections := make([]SectionInfo, 0, len(q.liveSections()))
	for _, section := range q.liveSections() {
		sections = append(sections, q.sectionInfo(section))
	}
	return sections
}

// SectionKind is the role of a live section by id, or ok=false when the id is
// not a live section.
func (q *Queries) SectionKind(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	for _, section := range q.liveSections() {
		if stringOf(section, "id") == id {
			return q.sectionKind(section), true
		}
	}
	return "", false
}

func (q *Queries) sectionInfo(section record.Record) SectionInfo {
	info := SectionInfo{
		ID: stringOf(section, "id"), Title: stringOf(section, "title"),
		ParentID: stringOf(section, "parent"), Kind: q.sectionKind(section),
	}
	if body := stringOf(section, "body"); body != "" {
		info.Body, info.HasBody = body, true
	}
	// The lifecycle pair is read the way ProjectView reads it: a closed date
	// only counts alongside a state.
	if state := stringOf(section, "state"); state != "" {
		info.State = state
		if closed := stringOf(section, "closed"); closed != "" {
			info.Closed, info.HasClosed = closed, true
		}
	}
	return info
}

func (q *Queries) sectionKind(section record.Record) string {
	root, hasRoot := q.projectsRoot()
	rootID := stringOf(root, "id")
	parent, title := stringOf(section, "parent"), stringOf(section, "title")
	switch {
	case hasRoot && stringOf(section, "id") == rootID:
		return SectionProjectsRoot
	case hasRoot && parent == rootID:
		return SectionProject
	case parent != "":
		return SectionSubsection
	case isInboxTitle(title):
		return SectionInbox
	case reservedList(title):
		return SectionList
	default:
		return SectionArea
	}
}

// SectionActionRefusal is why a section cannot take a structural action, or ""
// when it can. action is "rename", "complete", "drop", "reopen" or "archive".
//
// Two sections hold the file together rather than holding work: the Inbox is
// where every unfiled capture lands, and the Projects root is what makes its
// children projects. Renaming, closing or archiving either would silently
// change where captures go or what counts as a project, so they are refused.
// Reopening is always allowed — it is the way back if one was closed anyway.
func SectionActionRefusal(kind, action string) string {
	if action == "reopen" {
		return ""
	}
	switch kind {
	case SectionInbox:
		return "The Inbox is where unfiled captures land; it cannot be renamed, closed, or archived."
	case SectionProjectsRoot:
		return "The Projects heading holds every project; it cannot be renamed, closed, or archived."
	}
	return ""
}

// -- the Outline's rules -------------------------------------------------------

// OutlineShows reports whether the Outline paints a node's own row. A closed
// section, and a task the Outline's view rule hides (a proposal, or a closed
// task while closed rows are hidden), is TRANSPARENT rather than pruned: its
// children carry on at the same depth, which is what keeps an open task from
// vanishing under a parent someone completed.
func OutlineShows(node *Node, showClosed bool) bool {
	if node.Section() {
		return showClosed || !node.HasClosed
	}
	return NewViewQuery(ViewOutline, nil, 0, false, nil).ShowingClosed(showClosed).ViewRule(*node.Item)
}

// OutlineRenders reports whether anything at all paints beneath a node — not
// `len(node.Children) > 0`, because a hidden row is transparent and a task
// whose only children are hidden paints nothing under it.
func OutlineRenders(node *Node, showClosed bool) bool {
	for _, child := range node.Children {
		if OutlineShows(child, showClosed) || OutlineRenders(child, showClosed) {
			return true
		}
	}
	return false
}

// OutlineShownCount is every task beneath a node that the Outline is showing,
// folded or not — the number a section's badge carries.
func OutlineShownCount(node *Node, showClosed bool) int {
	total := 0
	for _, child := range node.Children {
		if child.Task() && OutlineShows(child, showClosed) {
			total++
		}
		total += OutlineShownCount(child, showClosed)
	}
	return total
}

// OutlineHiddenClosedCount is how many closed tasks and closed sections the
// closed toggle is holding back beneath a node. It is zero while closed rows
// are shown: nothing is being held back.
func OutlineHiddenClosedCount(node *Node, showClosed bool) int {
	if showClosed {
		return 0
	}
	total := 0
	for _, child := range node.Children {
		if child.Section() && child.HasClosed {
			total++
		}
		if child.Task() && isClosed(child.Item.State) {
			total++
		}
		total += OutlineHiddenClosedCount(child, showClosed)
	}
	return total
}

// Outline node kinds.
const (
	OutlineSectionNode = "section"
	OutlineTaskNode    = "task"
)

// OutlineCounts are a section node's task tallies, all at any depth.
type OutlineCounts struct {
	// Shown is what the Outline paints beneath the section right now.
	Shown int
	// Open and Closed count tasks by lifecycle, whatever the toggle says.
	Open   int
	Closed int
	// HiddenClosed is the closed tasks and sections the toggle is holding back.
	HiddenClosed int
}

// OutlineNode is one row of the Outline: a section or a task, the id of the
// nearest SHOWN ancestor it nests under ("" at the top), and its depth among
// shown rows. Because hidden rows are transparent, ParentID and Depth describe
// the outline as painted, which can differ from the record's own parent.
type OutlineNode struct {
	Kind     string
	ID       string
	ParentID string
	Depth    int
	Item     *store.Item
	Section  SectionInfo
	Counts   OutlineCounts
}

// Outline is the live tree as the Outline tab paints it, in file order: one
// flat DFS pre-order list. Proposals never appear (Approvals owns undecided
// work); closed tasks and closed sections appear only when showClosed is set.
// Availability is not a filter here — the outline is the whole tree.
//
// Presentation the tab layers on top — fold state, and the urgency sub-bands it
// draws inside a section whose children are all tasks — is not part of the
// answer; the order is the file's.
func (q *Queries) Outline(showClosed bool) []OutlineNode {
	nodes := []OutlineNode{}
	var walk func(node *Node, depth int, parentID string)
	walk = func(node *Node, depth int, parentID string) {
		if !OutlineShows(node, showClosed) {
			for _, child := range node.Children {
				walk(child, depth, parentID)
			}
			return
		}
		entry := OutlineNode{ID: node.ID, ParentID: parentID, Depth: depth}
		if node.Section() {
			entry.Kind = OutlineSectionNode
			entry.Section = q.sectionInfo(q.records[store.SourceLive][node.Line])
			entry.Counts = OutlineCounts{
				Shown:        OutlineShownCount(node, showClosed),
				HiddenClosed: OutlineHiddenClosedCount(node, showClosed),
			}
			entry.Counts.Open, entry.Counts.Closed = lifecycleCounts(node)
		} else {
			entry.Kind = OutlineTaskNode
			entry.Item = node.Item
		}
		nodes = append(nodes, entry)
		for _, child := range node.Children {
			walk(child, depth+1, node.ID)
		}
	}
	for _, root := range q.tree.Roots {
		walk(root, 0, "")
	}
	return nodes
}

// lifecycleCounts is the open and closed tasks anywhere beneath a node.
func lifecycleCounts(node *Node) (open, closed int) {
	for _, child := range node.Children {
		if child.Task() {
			switch {
			case isOpen(child.Item.State):
				open++
			case isClosed(child.Item.State):
				closed++
			}
		}
		childOpen, childClosed := lifecycleCounts(child)
		open, closed = open+childOpen, closed+childClosed
	}
	return open, closed
}
