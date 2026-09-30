package api

import (
	"net/http"
	"regexp"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
)

// The views read: the TUI's tabs as data. Selection, grouping and order all
// come from taskquery — NamedView for the four grouped views, Outline for the
// tree — which is exactly what the TUI builds its rows from. This file only
// spells the result on the wire.

var viewPath = regexp.MustCompile(`^/api/v1/views/([^/]+)$`)

// routedViews is every name this route answers, in the order a refusal lists
// them.
func routedViews() []string {
	return append(append([]string{}, taskquery.NamedViews...), taskquery.ViewOutline)
}

func (s *Server) view(request *http.Request, name string) (response, error) {
	if name == taskquery.ViewOutline {
		return s.outlineView(request)
	}
	if !containsString(taskquery.NamedViews, name) {
		return response{}, unknownView(name)
	}
	params, err := queryParams(request, "include_unavailable")
	if err != nil {
		return response{}, err
	}
	no := false
	includeUnavailable, err := booleanQuery(params, "include_unavailable", &no)
	if err != nil {
		return response{}, err
	}
	read, readErr := s.options.Read()
	if readErr != nil || !read.OK() {
		return response{}, readFailure(read, readErr)
	}
	// No UrgentDays here: the view classifies against the read model's own
	// configured window, the one every Task.quadrant in the same response
	// comes from, so the groups and the rows can never disagree.
	result, _ := read.Queries.NamedView(name, taskquery.NamedViewOptions{
		IncludeUnavailable: *includeUnavailable,
	})
	resources := newResourceContext(read.Queries)
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) {
		w.BeginObject()
		w.KeyStr("name", result.Name)
		w.KeyStr("today", read.Queries.Today().ISO())
		w.KeyBool("include_unavailable", *includeUnavailable)
		w.Key("tasks")
		w.BeginArray()
		for _, item := range result.Items {
			resources.writeTask(w, item)
		}
		w.EndArray()
		w.Key("groups")
		w.BeginArray()
		for _, group := range result.Groups {
			w.BeginObject()
			w.KeyStrOrNull("block", group.Block)
			w.KeyStr("key", group.Key)
			w.KeyStr("label", group.Label)
			w.Key("task_ids")
			w.Strings(itemIDs(group.Items))
			w.EndObject()
		}
		w.EndArray()
		w.EndObject()
	}, read.Revision)
	return response{
		status: 200, headers: map[string]string{"etag": etag(read.Revision)}, body: w.Bytes(),
	}, nil
}

// outlineView is the whole live tree as the Outline tab paints it.
func (s *Server) outlineView(request *http.Request) (response, error) {
	params, err := queryParams(request, "include_closed")
	if err != nil {
		return response{}, err
	}
	no := false
	includeClosed, err := booleanQuery(params, "include_closed", &no)
	if err != nil {
		return response{}, err
	}
	read, readErr := s.options.Read()
	if readErr != nil || !read.OK() {
		return response{}, readFailure(read, readErr)
	}
	nodes := read.Queries.Outline(*includeClosed)
	resources := newResourceContext(read.Queries)
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) {
		w.BeginObject()
		w.KeyStr("name", taskquery.ViewOutline)
		w.KeyStr("today", read.Queries.Today().ISO())
		w.KeyBool("include_closed", *includeClosed)
		w.Key("nodes")
		w.BeginArray()
		for _, node := range nodes {
			w.BeginObject()
			w.KeyStr("kind", node.Kind)
			w.KeyStrOrNull("id", node.ID)
			w.KeyStrOrNull("parent_id", node.ParentID)
			w.KeyInt("depth", node.Depth)
			w.Key("section")
			if node.Kind == taskquery.OutlineSectionNode {
				writeOutlineSection(w, node)
			} else {
				w.Null()
			}
			w.Key("task")
			if node.Item != nil {
				resources.writeTask(w, *node.Item)
			} else {
				w.Null()
			}
			w.EndObject()
		}
		w.EndArray()
		w.EndObject()
	}, read.Revision)
	return response{
		status: 200, headers: map[string]string{"etag": etag(read.Revision)}, body: w.Bytes(),
	}, nil
}

// writeOutlineSection is the Section resource plus the node's tallies.
func writeOutlineSection(w *jsonout.Writer, node taskquery.OutlineNode) {
	w.BeginObject()
	writeSectionMembers(w, node.Section)
	w.KeyInt("task_count", node.Counts.Shown)
	w.KeyInt("open_task_count", node.Counts.Open)
	w.KeyInt("closed_task_count", node.Counts.Closed)
	w.KeyInt("hidden_closed_count", node.Counts.HiddenClosed)
	w.EndObject()
}

// unknownView names the missing VIEW, not a missing task: the id-shaped
// sentence would send a client looking for a bad id.
func unknownView(name string) error {
	return errorWith(404, "not_found", "No view with that name.").withDetails(pairDetails(
		detailPair{Key: "name", Value: name},
		detailPair{Key: "views", Value: func(w *jsonout.Writer) { w.Strings(routedViews()) }},
	))
}

func itemIDs(items []store.Item) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}
