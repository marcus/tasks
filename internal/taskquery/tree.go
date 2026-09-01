// Package taskquery is the read model over a store snapshot: the structural
// tree, effective availability, and the selections the read surfaces render.
// It is the Go counterpart of lib/tasks/tree.rb and the read half of
// lib/tasks/task_queries.rb.
//
// Nothing here writes. The whole package answers questions about a snapshot a
// caller is already holding, so two renderings of the same snapshot can never
// disagree.
package taskquery

import (
	"strings"

	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/store"
)

// Node is one record in the structural index: a section or a task, with its own
// body lines and its children. Hierarchy comes straight from each record's
// `parent` pointer — no star counting, no block inference — so a whole class of
// boundary bugs is structurally absent.
type Node struct {
	Title string
	Line  int
	Level int
	// ID is the record's stable 8-hex id — a task's or a section's. Sections
	// carry no Item, so the id lives on the node itself; it is what the
	// outliner's fold state and selection follow for section rows.
	ID       string
	Item     *store.Item // nil for a section
	Body     []string
	Children []*Node
	Parent   *Node
	// State and Closed are the section lifecycle pair when this node is a
	// section. Tasks carry lifecycle on their Item, sections carry it here
	// so row builders can ask a node directly without a second lookup.
	State     string
	Closed    string
	HasClosed bool
}

// Closed reports whether this section node is closed.
func (n *Node) ClosedView() bool { return n != nil && n.HasClosed }

// Open reports whether this section node is open.
func (n *Node) Open() bool { return n == nil || !n.HasClosed }

// ProjectSection climbs ancestors to the section that is a child of the
// Projects root, or nil when there is none. It is the resolver the task
// detail rail uses to name the project a task belongs to.
func (n *Node) ProjectSection() *Node {
	if n == nil {
		return nil
	}
	current := n.Parent
	for current != nil {
		if current.Section() && current.Parent != nil && isProjectsRoot(current.Parent) {
			return current
		}
		current = current.Parent
	}
	return nil
}

func isProjectsRoot(node *Node) bool {
	if node == nil || node.Parent != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(node.Title), "Projects")
}

// Task reports a node that carries an item.
func (n *Node) Task() bool { return n != nil && n.Item != nil }

// Section reports a node that does not.
func (n *Node) Section() bool { return n != nil && n.Item == nil }

// Project is the nearest ancestor headline: a task's project, or a subtask's
// parent task.
func (n *Node) Project() *Node {
	if n == nil {
		return nil
	}
	return n.Parent
}

// OpenProject is the task's project FOR GROUPING: the nearest ancestor headline
// that is a section or an OPEN task, climbing past closed ancestors. A closed
// ancestor is transparent, the same way the outliner hoists open descendants
// out of a closed parent. Deferred tasks count as open — a deferred project
// still owns its subtasks.
func (n *Node) OpenProject() *Node {
	ancestor := n.Parent
	for ancestor != nil && ancestor.Task() && !isOpen(ancestor.Item.State) {
		ancestor = ancestor.Parent
	}
	return ancestor
}

// Tree is the forest plus the line index every per-item lookup goes through.
type Tree struct {
	Roots       []*Node
	NodesByLine map[int]*Node
}

// BuildTree reconstructs the forest in one pass over the live records, linking
// each to its parent by id. The file is in DFS pre-order, so a single forward
// pass suffices and no ordering is inferred.
func BuildTree(records []record.Record, items []store.Item) Tree {
	itemsByLine := map[int]*store.Item{}
	for index := range items {
		itemsByLine[items[index].Line] = &items[index]
	}
	tree := Tree{NodesByLine: map[int]*Node{}}
	byID := map[string]*Node{}
	for _, parsed := range records {
		if stringOf(parsed, "type") == "meta" {
			continue
		}
		item := itemsByLine[parsed.Line]
		title := stringOf(parsed, "title")
		if item != nil {
			title = item.Title
		}
		body := []string{}
		if text := stringOf(parsed, "body"); text != "" {
			body = strings.Split(text, "\n")
		}
		node := &Node{Title: title, Line: parsed.Line, Level: 1, Item: item, Body: body, Children: []*Node{}}
		if id := stringOf(parsed, "id"); id != "" {
			node.ID = id
			byID[id] = node
		}
		if state := stringOf(parsed, "state"); state != "" {
			node.State = state
			if closed := stringOf(parsed, "closed"); closed != "" {
				node.Closed = closed
				node.HasClosed = true
			}
		}
		if parentID := stringOf(parsed, "parent"); parentID != "" {
			if parent, found := byID[parentID]; found {
				node.Parent = parent
				node.Level = parent.Level + 1
				parent.Children = append(parent.Children, node)
				tree.NodesByLine[node.Line] = node
				continue
			}
		}
		tree.Roots = append(tree.Roots, node)
		tree.NodesByLine[node.Line] = node
	}
	return tree
}

func stringOf(parsed record.Record, key string) string {
	for _, field := range parsed.Fields {
		if field.Key != key {
			continue
		}
		var value string
		if err := unmarshalString(field.Value, &value); err != nil {
			return ""
		}
		return value
	}
	return ""
}
