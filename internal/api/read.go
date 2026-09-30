package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/marcus/tasks/internal/buildinfo"
	"github.com/marcus/tasks/internal/check"
	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/query"
	"github.com/marcus/tasks/internal/recur"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/timezones"
)

// The read surface. Every handler here performs at most ONE checked read, so a
// resource and the store revision it is tagged with always describe the same
// bytes.

// listQueryKeys is App::LIST_QUERY_KEYS.
var listQueryKeys = []string{
	"scope", "state", "context", "tag", "priority", "text", "body",
	"deferred", "available", "recurring", "delegated",
}

// delegationScopes are the two open-live refinements `scope` accepts on top of
// the lifecycle scopes.
var delegationScopes = []string{"delegated", "agent_ready"}

// The lifecycle vocabularies /meta publishes. They are spelled here because
// taskquery keeps its copies unexported; a divergence would be caught by the
// differential comparison against Ruby's /meta, which is where it belongs.
var (
	metaProposedStates    = []string{"PROPOSED"}
	metaOpenStates        = []string{"INBOX", "TODO", "NEXT", "WAITING"}
	metaClosedStates      = []string{"DONE", "CANCELLED"}
	metaPriorities        = []string{"A", "B", "C"}
	metaDelegationKinds   = []string{"human", "agent"}
	metaDelegationStatues = []string{"delegated", "ready", "claimed"}
)

func (s *Server) health(request *http.Request) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	w := jsonout.New()
	w.BeginObject()
	w.KeyStr("status", "ok")
	w.EndObject()
	return response{status: 200, body: w.Bytes()}, nil
}

func (s *Server) readiness(request *http.Request) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	read, err := s.options.Read()
	if err != nil || !read.OK() {
		return response{}, readFailure(read, err)
	}
	w := jsonout.New()
	w.BeginObject()
	w.KeyStr("status", "ready")
	w.EndObject()
	return response{status: 200, body: w.Bytes()}, nil
}

func (s *Server) meta(request *http.Request) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	// The document is config and build, never store content, so it is
	// rendered before any read and its digest joins the store revision in the
	// ETag. A 304 then answers "neither the store nor the document changed":
	// restarting under a different zone, date order, link config or build
	// changes the tag even when the store bytes did not.
	document := s.metaDocument()
	digest := metaDigest(document)

	// Conditional polling: a matching If-None-Match is answered from the
	// revision digest alone, without parsing or validating the store.
	conditional := request.Header.Get("If-None-Match")
	if conditional != "" {
		if revision, err := s.options.App.StoreRevision(); err == nil &&
			noneMatch(conditional, metaETagValue(revision, digest), false) {
			return metaNotModified(revision, digest), nil
		}
	}
	read, err := s.options.Read()
	if err != nil || !read.OK() {
		return response{}, readFailure(read, err)
	}
	if noneMatch(conditional, metaETagValue(read.Revision, digest), true) {
		return metaNotModified(read.Revision, digest), nil
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) { w.Raw(document) }, read.Revision)
	return response{
		status: 200,
		headers: map[string]string{
			"etag": etag(metaETagValue(read.Revision, digest)), "cache-control": metaCacheControl,
		},
		body: w.Bytes(),
	}, nil
}

// metaDocument renders the /meta `data` member. Nothing in it comes from the
// store's CONTENT: it is this process's configuration, this build's
// vocabularies and capabilities, and the delegation modes the store is
// configured with.
func (s *Server) metaDocument() []byte {
	w := jsonout.New()
	w.BeginObject()
	w.KeyStr("api_version", "v1")
	w.KeyStr("server_mode", "loopback")
	w.Key("states")
	w.Strings(taskquery.StateOrder())
	w.Key("proposed_states")
	w.Strings(metaProposedStates)
	w.Key("open_states")
	w.Strings(metaOpenStates)
	w.Key("closed_states")
	w.Strings(metaClosedStates)
	w.Key("priorities")
	w.Strings(metaPriorities)
	w.Key("delegation_kinds")
	w.Strings(metaDelegationKinds)
	w.Key("delegation_modes")
	// The mode vocabulary comes from the store this server writes through,
	// read per request, never from a literal or a start-up snapshot. It is
	// store CONFIGURATION, not store content, so it costs no read.
	w.Strings(s.options.App.DelegationModes().Modes())
	w.Key("delegation_statuses")
	w.Strings(metaDelegationStatues)
	w.KeyInt("max_depth", s.options.MaxDepth)
	w.KeyInt("urgent_days", s.urgentDays)
	w.KeyStr("timezone", s.options.Timezone)
	w.KeyInt("time_format", s.options.TimeFormat)
	w.KeyStr("tzdb_version", timezones.TZDBVersion())
	w.KeyStr("temporal_precision", "minute")
	w.KeyStr("date_order", s.options.DateOrder)
	// The resolved link configuration, so a client can expand a shorthand
	// or classify a URL exactly as a write and a read here would. These are
	// config values (URL templates and hosts), never filesystem paths.
	w.Key("link_shorthands")
	writeStringMap(w, s.options.App.LinkShorthands())
	w.Key("link_systems")
	writeStringMap(w, s.options.App.LinkSystems())
	w.Key("capabilities")
	w.BeginObject()
	// Capabilities advertise what THIS server routes, and every flag here is
	// dispatched; TestAdvertisedCapabilitiesAreReallyRouted holds each one to
	// it.
	for _, capability := range metaCapabilities {
		w.KeyBool(capability, true)
	}
	w.EndObject()
	w.EndObject()
	return w.Bytes()
}

// metaCapabilities are the feature flags /meta publishes, in document order. A
// client feature-detects on these rather than on a version number, so each
// later feature gets its own flag: an older binary simply omits it.
var metaCapabilities = []string{
	"projects", "undo", "redo", "archive_sweep", "events", "views",
	"activate", "patch_deltas", "date_parse", "lead_explain", "outline",
}

// metaDigest is the short digest of the /meta document and the build that
// rendered it. The build is included because a new binary can change what
// the same config means without changing a byte of the document.
func metaDigest(document []byte) string {
	hash := sha256.New()
	hash.Write([]byte(buildinfo.Version + "\x00" + buildinfo.Commit + "\x00"))
	hash.Write(document)
	return hex.EncodeToString(hash.Sum(nil))[:12]
}

// metaETagValue is the /meta entity tag: the store revision and the document
// digest. Clients treat it as opaque; it is deliberately NOT the bare store
// revision /events carries, so a tag built from an SSE frame never matches.
func metaETagValue(revision, digest string) string {
	if revision == "" {
		return ""
	}
	return revision + "." + digest
}

// metaCacheControl lets a browser keep /meta and revalidate it on every use,
// which turns an ordinary fetch() poll into the conditional GET above. Every
// other route stays no-store.
const metaCacheControl = "no-cache"

func metaNotModified(revision, digest string) response {
	return response{status: 304, headers: map[string]string{
		"etag": etag(metaETagValue(revision, digest)), "cache-control": metaCacheControl,
	}}
}

func (s *Server) sections(request *http.Request) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	read, err := s.options.Read()
	if err != nil || !read.OK() {
		return response{}, readFailure(read, err)
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) {
		w.BeginArray()
		for _, section := range read.Queries.Sections() {
			writeSection(w, section)
		}
		w.EndArray()
	}, read.Revision)
	return response{status: 200, body: w.Bytes()}, nil
}

func (s *Server) listTasks(request *http.Request) (response, error) {
	params, err := queryParams(request, listQueryKeys...)
	if err != nil {
		return response{}, err
	}
	filter, wantAvailable, err := buildFilter(params)
	if err != nil {
		return response{}, err
	}
	read, readErr := s.options.Read()
	if readErr != nil || !read.OK() {
		return response{}, readFailure(read, readErr)
	}
	items := read.Queries.List(filter)
	if wantAvailable != nil && *wantAvailable {
		kept := []store.Item{}
		for _, item := range items {
			if read.Queries.AvailabilityFor(item).Available() {
				kept = append(kept, item)
			}
		}
		items = kept
	}
	resources := newResourceContext(read.Queries)
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) {
		w.BeginArray()
		for _, item := range items {
			resources.writeTask(w, item)
		}
		w.EndArray()
	}, read.Revision)
	return response{status: 200, body: w.Bytes()}, nil
}

// buildFilter is the whole of App#list_tasks' query validation. It returns the
// domain filter plus the `available=true` post-selection, which is a refinement
// over the answered list rather than part of the filter.
func buildFilter(params url.Values) (query.Filter, *bool, error) {
	scope := "open"
	if params.Has("scope") {
		scope = params.Get("scope")
	}
	if !knownScope(scope) {
		return query.Filter{}, nil, validationError(reason("scope",
			"must be open, proposed, done, archived, all, rejected, delegated, or agent_ready"))
	}
	agentReadyOnly := scope == "agent_ready"
	delegated, err := booleanQuery(params, "delegated", nil)
	if err != nil {
		return query.Filter{}, nil, err
	}
	delegatedOnly := scope == "delegated" || (delegated != nil && *delegated)
	lifecycleScope := scope
	if containsString(delegationScopes, scope) {
		lifecycleScope = "open"
	}

	var state *string
	if params.Has("state") {
		value := params.Get("state")
		if !containsString(taskquery.StateOrder(), value) {
			return query.Filter{}, nil, validationError(reason("state", "must be a documented task state"))
		}
		state = &value
	}
	if err := delegationScopeConflict(scope, delegated, state); err != nil {
		return query.Filter{}, nil, err
	}

	var priority *string
	if params.Has("priority") {
		value := params.Get("priority")
		if !containsString(metaPriorities, value) {
			return query.Filter{}, nil, validationError(reason("priority", "must be A, B, or C"))
		}
		priority = &value
	}

	contexts := []string{}
	if params.Has("context") {
		value := params.Get("context")
		if !strings.HasPrefix(value, "@") {
			value = "@" + value
		}
		if value == "@" {
			return query.Filter{}, nil, validationError(reason("context", "must name a context"))
		}
		contexts = append(contexts, value)
	}
	tags := []string{}
	if params.Has("tag") {
		value := params.Get("tag")
		if strings.HasPrefix(value, "@") || value == store.DeferTag {
			return query.Filter{}, nil, validationError(reason("tag", "must be an ordinary tag"))
		}
		tags = append(tags, value)
	}
	text := []string{}
	if params.Has("text") {
		text = append(text, params.Get("text"))
	}

	no := false
	body, err := booleanQuery(params, "body", &no)
	if err != nil {
		return query.Filter{}, nil, err
	}
	deferred, err := booleanQuery(params, "deferred", &no)
	if err != nil {
		return query.Filter{}, nil, err
	}
	recurring, err := booleanQuery(params, "recurring", &no)
	if err != nil {
		return query.Filter{}, nil, err
	}
	available, err := booleanQuery(params, "available", nil)
	if err != nil {
		return query.Filter{}, nil, err
	}
	if available != nil && !*available && scope != "open" {
		return query.Filter{}, nil, validationError(reason("available", "false is only valid with scope=open"))
	}

	filter, buildErr := query.NewFilter(query.FilterOptions{
		Scope: &lifecycleScope, State: state, Priority: priority,
		Contexts: contexts, Tags: tags, Text: text, BodySearch: *body,
		SomedayOnly:     *deferred,
		UnavailableOnly: available != nil && !*available,
		RecurringOnly:   *recurring,
		DelegatedOnly:   delegatedOnly, AgentReadyOnly: agentReadyOnly,
	})
	if buildErr != nil {
		return query.Filter{}, nil, validationError(reason("query", safeArgumentMessage(buildErr.Error())))
	}
	return filter, available, nil
}

// delegationScopeConflict is App#delegation_scope_conflict!: the two delegation
// scopes are open-live slices, so combining either with a closed state used to
// return an empty 200 a client cannot tell from "nothing is delegated".
func delegationScopeConflict(scope string, delegated *bool, state *string) error {
	agentReady := scope == "agent_ready"
	if agentReady && delegated != nil {
		return validationError(reason("delegated",
			"is not valid with scope=agent_ready, which is already the unclaimed "+
				"agent slice; use scope=open&delegated=true for every open marker"))
	}
	if scope == "delegated" && delegated != nil && !*delegated {
		return validationError(reason("delegated", "false contradicts scope=delegated"))
	}
	if !containsString(delegationScopes, scope) {
		return nil
	}
	if state == nil || containsString(metaOpenStates, *state) {
		return nil
	}
	hint := "use scope=all&delegated=true"
	if agentReady {
		hint = "only accepted live work is claimable"
	}
	return validationError(reason("state",
		*state+" is outside scope="+scope+", which lists open live tasks ("+hint+")"))
}

func knownScope(scope string) bool {
	for _, known := range []string{"open", "proposed", "done", "archived", "all", "rejected"} {
		if scope == known {
			return true
		}
	}
	return containsString(delegationScopes, scope)
}

// safeArgumentMessage is App#safe_argument_message: a domain refusal that
// happens to name a path must not be echoed to a client.
func safeArgumentMessage(text string) string {
	if strings.ContainsAny(text, `\/`) {
		return "invalid query value"
	}
	return text
}

func (s *Server) getTask(request *http.Request, id string) (response, error) {
	params, err := queryParams(request, "source")
	if err != nil {
		return response{}, err
	}
	source := "live"
	if params.Has("source") {
		source = params.Get("source")
	}
	if source != "live" && source != "archive" {
		return response{}, validationError(reason("source", "must be live or archive"))
	}
	read, readErr := s.options.Read()
	if readErr != nil || !read.OK() {
		return response{}, readFailure(read, readErr)
	}
	item, found := findInSource(read.Queries, id, store.Source(source))
	if !found {
		return response{}, errorOf(404, "not_found")
	}
	resources := newResourceContext(read.Queries)
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) { resources.writeTask(w, item) }, read.Revision)
	return response{
		status:  200,
		headers: map[string]string{"etag": etag(resources.revisionFor(item))},
		body:    w.Bytes(),
	}, nil
}

// findInSource is the exact-source lookup a resource read needs: live and
// archive are different resources that may legitimately share an id.
func findInSource(queries *taskquery.Queries, id string, source store.Source) (store.Item, bool) {
	items := queries.LiveItems()
	if source == store.SourceArchive {
		items = queries.ArchiveItems()
	}
	for _, item := range items {
		if item.HasID && item.ID == id {
			return item, true
		}
	}
	return store.Item{}, false
}

func (s *Server) listProjects(request *http.Request) (response, error) {
	params, err := queryParams(request, "closed")
	if err != nil {
		return response{}, err
	}
	closedParam := "exclude"
	if params.Has("closed") {
		closedParam = params.Get("closed")
	}
	switch closedParam {
	case "exclude", "include", "only":
	default:
		return response{}, validationError(reason("closed", "must be exclude, include, or only"))
	}
	read, err := s.options.Read()
	if err != nil || !read.OK() {
		return response{}, readFailure(read, err)
	}
	var views []taskquery.ProjectView
	switch closedParam {
	case "exclude":
		views = read.Queries.Projects()
	case "include":
		views = read.Queries.ProjectsIncluding(true)
	case "only":
		for _, view := range read.Queries.ProjectsIncluding(true) {
			if view.HasClosed {
				views = append(views, view)
			}
		}
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) {
		w.BeginArray()
		for _, view := range views {
			writeProject(w, view)
		}
		w.EndArray()
	}, read.Revision)
	return response{
		status: 200, headers: map[string]string{"etag": etag(read.Revision)}, body: w.Bytes(),
	}, nil
}

func (s *Server) getProject(request *http.Request, id string) (response, error) {
	if _, err := queryParams(request); err != nil {
		return response{}, err
	}
	read, err := s.options.Read()
	if err != nil || !read.OK() {
		return response{}, readFailure(read, err)
	}
	view, found := read.Queries.ProjectView(id)
	if !found {
		return response{}, errorOf(404, "not_found")
	}
	w := jsonout.New()
	writeSuccess(w, func(w *jsonout.Writer) { writeProject(w, view) }, read.Revision)
	return response{
		status: 200, headers: map[string]string{"etag": etag(read.Revision)}, body: w.Bytes(),
	}, nil
}

// explainRecurrence is the API twin of `tasks recur --explain`: parse,
// normalize, and project without naming a task and without touching the store.
// It is therefore the one read that stays available while the store is
// unreadable, and its envelope carries no store revision because no store was
// read.
func (s *Server) explainRecurrence(request *http.Request) (response, error) {
	params, err := queryParams(request, "input", "count", "anchor")
	if err != nil {
		return response{}, err
	}
	input, err := requiredInput(params)
	if err != nil {
		return response{}, err
	}
	count, err := explainCount(params)
	if err != nil {
		return response{}, err
	}
	anchor, _, err := anchorParam(params)
	if err != nil {
		return response{}, err
	}
	context := s.options.TemporalContext()
	today := context.LocalDate()
	explanation := recur.Explain(input, recur.NewCivilDate(int64(today.Year), int(today.Month), today.Day), count, anchor)

	w := jsonout.New()
	w.BeginObject()
	w.Key("data")
	w.BeginObject()
	w.KeyStr("input", explanation.Input)
	if explanation.Error != "" && !explanation.HasCanonical {
		w.KeyStr("error", explanation.Error)
		w.EndObject()
		w.EndObject()
		return response{status: 200, body: w.Bytes()}, nil
	}
	if explanation.HasCanonical && explanation.Canonical == "" {
		w.KeyNull("canonical")
	} else {
		w.KeyStrOrNull("canonical", explanation.Canonical)
	}
	w.KeyStrOrNull("human", explanation.Human)
	if explanation.HasNext {
		w.Key("next")
		w.BeginArray()
		for _, date := range explanation.Next {
			w.Str(date.String())
		}
		w.EndArray()
	}
	if explanation.Error != "" {
		w.KeyStr("error", explanation.Error)
	}
	w.EndObject()
	w.EndObject()
	return response{status: 200, body: w.Bytes()}, nil
}

// explainCount clamps rather than fails, matching the engine, so a client
// asking for "as many as you have" gets the ceiling instead of a 422. A count
// that is not an integer at all is still a bad request.
func explainCount(params url.Values) (int, error) {
	if !params.Has("count") {
		return explainCountDefault, nil
	}
	raw := params.Get("count")
	value, err := strconv.Atoi(raw)
	if err != nil || !integerText(raw) {
		return 0, validationError(reason("count", "must be an integer"))
	}
	if value < 0 {
		return 0, nil
	}
	if value > explainCountMax {
		return explainCountMax, nil
	}
	return value, nil
}

// integerText is Ruby's /\A-?\d+\z/: `+5`, `0x10` and ` 5 ` are refused, and
// strconv.Atoi accepts the first of those.
func integerText(value string) bool {
	digits := value
	if strings.HasPrefix(digits, "-") {
		digits = digits[1:]
	}
	if digits == "" {
		return false
	}
	for index := 0; index < len(digits); index++ {
		if digits[index] < '0' || digits[index] > '9' {
			return false
		}
	}
	return true
}

// readFailure is App#read_failure!: the read vocabulary mapped onto HTTP.
func readFailure(read CheckedRead, err error) error {
	if err != nil {
		return unavailableError(store.UnavailableMessage(err))
	}
	switch read.Status {
	case store.StatusUnsupportedSchema:
		return errorOf(503, "unsupported_schema_version").
			withDetails(pairDetails(detailPair{Key: "supported_version", Value: check.Version}))
	case store.StatusStoreInvalid:
		return errorOf(503, "store_invalid")
	}
	if read.Status == store.StatusUnavailable && len(read.Errors) > 0 &&
		store.IsLockTimeoutMessage(read.Errors[0].Message) {
		return unavailableError(read.Errors[0].Message)
	}
	return errorOf(503, "unavailable")
}

func unavailableError(text string) error {
	if store.IsLockTimeoutMessage(text) {
		return errorWith(503, "unavailable", text)
	}
	return errorOf(503, "unavailable")
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
