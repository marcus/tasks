// Package api is the Go port of lib/tasks/api — the loopback HTTP surface
// described by docs/api/openapi.yaml.
//
// It is an ADAPTER and nothing else. Every rule about tasks lives in
// internal/application and internal/store; what happens here is transport:
// routing, host and origin enforcement, body and query validation, the
// If-Match precondition, the JSON envelope, and the mapping from the shared
// outcome vocabulary onto HTTP status codes.
//
// Concurrency is this surface's distinguishing risk. Unlike the CLI, one
// process serves many requests against one pair of files, so every handler
// builds its own store through a factory, holds no cross-request state, and
// relies on the store's own lock and revision preconditions rather than on any
// coordination invented here. The Server value itself is immutable after
// construction apart from the mutex that serializes its log sink.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// bodyLimit is App::BODY_LIMIT.
const bodyLimit = 64 * 1024

// taskIDPattern is App::TASK_ID.
var taskIDPattern = regexp.MustCompile(`^[0-9a-f]{8}$`)

// explainPath and the projection window the explain endpoint documents.
const (
	explainPath         = "/api/v1/recurrence/explain"
	explainCountDefault = 5
	explainCountMax     = 50
)

// forwardedHeaders are the proxy headers a loopback-only server refuses
// outright rather than trying to interpret.
var forwardedHeaders = []string{
	"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port",
}

// Options is the immutable construction settings of one server.
type Options struct {
	// App is the shared facade every rule about tasks goes through.
	App *application.Application

	// Read is the coherent checked read every store-backed GET uses. See
	// seams.go for why it is not a method on App yet.
	Read Reader

	// Changesets applies an atomic multi-field PATCH. A nil value makes PATCH
	// refuse with 501 rather than silently degrade to a single-field write.
	Changesets func() Changesets

	// TemporalContext is the clock and zone every request reads.
	TemporalContext func() temporal.Context

	Port       int
	MaxDepth   int
	UrgentDays int
	Timezone   string
	TimeFormat int

	// QueryOptions are the link shorthands and systems every read model built
	// here is configured with, so an HTTP resource and `tasks links` classify a
	// URL the same way.
	QueryOptions []taskquery.Option

	// Logger receives one JSON line per request. nil disables logging.
	Logger io.Writer

	// RequestIDs and Clock are injectable so a test can pin both halves of the
	// log line and the error envelope.
	RequestIDs func() string
	Clock      func() time.Time
}

// Server is the http.Handler. Everything it holds is immutable after
// construction except the log mutex, which exists because the ONE thing this
// adapter genuinely shares between concurrent requests is the log sink.
type Server struct {
	options        Options
	allowedHosts   []string
	allowedOrigins []string

	// logMutex serializes writes to Options.Logger.
	//
	// An io.Writer carries no concurrency guarantee — a strings.Builder is
	// unsafe outright, and even a file only appends atomically up to PIPE_BUF —
	// so two requests finishing at once would interleave inside a JSON object
	// and produce a line no consumer can parse. The race detector found this
	// against the concurrency tests; the mutex is the whole fix, and it is held
	// only around the write.
	logMutex sync.Mutex
}

// New validates the options and builds a server.
func New(options Options) (*Server, error) {
	if options.App == nil {
		return nil, fmt.Errorf("api: an application is required")
	}
	if options.Read == nil {
		return nil, fmt.Errorf("api: a checked reader is required")
	}
	if options.Port == 0 {
		options.Port = 4747
	}
	if options.Timezone == "" {
		options.Timezone = "Etc/UTC"
	}
	if options.TimeFormat == 0 {
		options.TimeFormat = 12
	}
	if options.RequestIDs == nil {
		options.RequestIDs = defaultRequestID
	}
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.TemporalContext == nil {
		options.TemporalContext = func() temporal.Context {
			built, err := temporal.NewContext(time.Now().UTC(), options.Timezone, options.TimeFormat)
			if err != nil {
				return temporal.Context{Now: time.Now().UTC(), Timezone: time.UTC, TimezoneID: "Etc/UTC"}
			}
			return built
		}
	}
	port := strconv.Itoa(options.Port)
	hosts := []string{"127.0.0.1:" + port, "localhost:" + port}
	origins := make([]string, 0, len(hosts))
	for _, host := range hosts {
		origins = append(origins, "http://"+host)
	}
	return &Server{options: options, allowedHosts: hosts, allowedOrigins: origins}, nil
}

func defaultRequestID() string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		// A request id is a correlation token, not a secret. Falling back to a
		// constant would make two concurrent requests indistinguishable in the
		// log, so refuse to pretend and name the failure instead.
		return "req_unavailable"
	}
	return "req_" + hex.EncodeToString(buffer)
}

// response is one shaped answer: a status, the headers this route adds on top
// of the common three, and the body bytes. A nil body is the 204 case.
type response struct {
	status  int
	headers map[string]string
	body    []byte
}

func (s *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	requestID := s.options.RequestIDs()
	started := s.options.Clock()
	route := routeName(request.URL.Path)

	answer, err := s.handle(request, requestID)
	if err != nil {
		answer = s.errorResponse(err, requestID)
	}

	s.logRequest(request.Method, route, answer.status, requestID, started)
	s.write(writer, answer, requestID)
}

// handle runs the transport gates and then the route.
func (s *Server) handle(request *http.Request, requestID string) (response, error) {
	if err := s.enforceHost(request); err != nil {
		return response{}, err
	}
	if err := s.enforceOrigin(request); err != nil {
		return response{}, err
	}
	return s.dispatch(request, requestID)
}

// errorResponse turns any error into the one error envelope. A non-httpError is
// an unexpected failure, and it must never reach a client: its text could name
// a path, a task title, or a token.
func (s *Server) errorResponse(err error, requestID string) response {
	failure, ok := err.(*httpError)
	if !ok {
		failure = errorOf(503, "unavailable")
	}
	w := jsonout.New()
	writeError(w, failure.Code, failure.Message, requestID, failure.Details)
	return response{status: failure.Status, headers: failure.Headers, body: w.Bytes()}
}

// write emits the response with the three headers every answer carries.
func (s *Server) write(writer http.ResponseWriter, answer response, requestID string) {
	header := writer.Header()
	header.Set("x-request-id", requestID)
	header.Set("cache-control", "no-store")
	for name, value := range answer.headers {
		header.Set(name, value)
	}
	if answer.body == nil {
		header.Del("content-type")
		header.Set("content-length", "0")
		writer.WriteHeader(answer.status)
		return
	}
	header.Set("content-type", "application/json")
	header.Set("content-length", strconv.Itoa(len(answer.body)))
	writer.WriteHeader(answer.status)
	_, _ = writer.Write(answer.body)
}

// enforceHost is App#enforce_host!. A loopback-only server accepts exactly two
// Host values and no proxy header at all: anything else means the request did
// not come from this machine's own client, whatever it claims.
func (s *Server) enforceHost(request *http.Request) error {
	for _, name := range forwardedHeaders {
		if request.Header.Get(name) != "" {
			return errorWith(400, "malformed_request", "Forwarded host headers are not accepted.")
		}
	}
	host := strings.ToLower(request.Host)
	for _, allowed := range s.allowedHosts {
		if host == allowed {
			return nil
		}
	}
	return errorWith(400, "malformed_request", "The Host header is not allowed.")
}

// enforceOrigin is App#enforce_origin!: a browser-supplied Origin on a mutating
// request must name this server.
func (s *Server) enforceOrigin(request *http.Request) error {
	switch request.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	for _, allowed := range s.allowedOrigins {
		if origin == allowed {
			return nil
		}
	}
	return errorOf(403, "forbidden_origin")
}

// dispatch is App#dispatch, in the same order, so a path that matches two
// patterns resolves the same way in both implementations.
func (s *Server) dispatch(request *http.Request, requestID string) (response, error) {
	method, path := request.Method, request.URL.Path
	switch {
	case method == http.MethodGet && path == "/healthz":
		return s.health(request)
	case method == http.MethodGet && path == "/readyz":
		return s.readiness(request)
	case method == http.MethodGet && path == "/api/v1/meta":
		return s.meta(request)
	case method == http.MethodGet && path == "/api/v1/sections":
		return s.sections(request)
	case method == http.MethodGet && path == "/api/v1/tasks":
		return s.listTasks(request)
	case method == http.MethodPost && path == "/api/v1/tasks":
		return s.createTask(request, requestID)
	case method == http.MethodGet && path == explainPath:
		return s.explainRecurrence(request)
	}

	if match := taskPath.FindStringSubmatch(path); match != nil {
		id, err := validTaskID(match[1])
		if err != nil {
			return response{}, err
		}
		switch method {
		case http.MethodGet:
			return s.getTask(request, id)
		case http.MethodPatch:
			return s.updateTask(request, id, requestID)
		case http.MethodDelete:
			return s.deleteTask(request, id, requestID)
		}
	}

	if match := decisionPath.FindStringSubmatch(path); match != nil && method == http.MethodPost {
		id, err := validTaskID(match[1])
		if err != nil {
			return response{}, err
		}
		return s.decideProposal(request, id, match[2], requestID)
	}

	if match := delegationPath.FindStringSubmatch(path); match != nil && method == http.MethodPost {
		id, err := validTaskID(match[1])
		if err != nil {
			return response{}, err
		}
		return s.delegationAction(request, id, match[2], requestID)
	}

	if match := workRefPath.FindStringSubmatch(path); match != nil && method == http.MethodPut {
		id, err := validTaskID(match[1])
		if err != nil {
			return response{}, err
		}
		return s.putWorkRef(request, id, requestID)
	}

	if match := delegationNotePath.FindStringSubmatch(path); match != nil && method == http.MethodPut {
		id, err := validTaskID(match[1])
		if err != nil {
			return response{}, err
		}
		return s.putDelegationNote(request, id, requestID)
	}

	switch {
	case method == http.MethodGet && path == "/api/v1/projects":
		return s.listProjects(request)
	case method == http.MethodPost && path == "/api/v1/projects":
		return s.createProject(request, requestID)
	}

	if match := projectPath.FindStringSubmatch(path); match != nil {
		id, err := validTaskID(match[1])
		if err != nil {
			return response{}, err
		}
		switch match[2] {
		case "":
			if method == http.MethodGet {
				return s.getProject(request, id)
			}
			if method == http.MethodPatch {
				return s.renameProject(request, id, requestID)
			}
		case "/complete":
			if method == http.MethodPost {
				return s.completeProject(request, id, requestID)
			}
		case "/drop":
			if method == http.MethodPost {
				return s.dropProject(request, id, requestID)
			}
		case "/reopen":
			if method == http.MethodPost {
				return s.reopenProject(request, id, requestID)
			}
		case "/archive":
			if method == http.MethodPost {
				return s.archiveProject(request, id, requestID)
			}
		}
	}

	return response{}, errorOf(404, "not_found")
}

var (
	taskPath       = regexp.MustCompile(`^/api/v1/tasks/([^/]+)$`)
	decisionPath   = regexp.MustCompile(`^/api/v1/tasks/([^/]+)/(approve|reject|unreject)$`)
	delegationPath = regexp.MustCompile(`^/api/v1/tasks/([^/]+)/(delegate|undelegate|claim|release)$`)
	workRefPath    = regexp.MustCompile(`^/api/v1/tasks/([^/]+)/work_ref$`)
	// The briefing has its own route for the same reason work_ref does: an
	// owner correcting instructions should not have to restate the delegation.
	delegationNotePath = regexp.MustCompile(`^/api/v1/tasks/([^/]+)/delegation_note$`)
	projectPath        = regexp.MustCompile(`^/api/v1/projects/([^/]+?)(/complete|/drop|/reopen|/archive)?$`)
)

func validTaskID(value string) (string, error) {
	if taskIDPattern.MatchString(value) {
		return value, nil
	}
	return "", errorWith(400, "malformed_request", "The task id is malformed.")
}

// literalRoutes are logged verbatim, having no id segment to template away.
var literalRoutes = map[string]bool{
	"/healthz": true, "/readyz": true, "/api/v1/meta": true, "/api/v1/sections": true,
	"/api/v1/tasks": true, "/api/v1/projects": true, explainPath: true,
}

// routeName is App#route_name: the templated path a log line names, so a log
// can be aggregated without leaking task ids.
func routeName(path string) string {
	if literalRoutes[path] {
		return path
	}
	if match := actionRoute.FindStringSubmatch(path); match != nil {
		return "/api/v1/tasks/{id}/" + match[1]
	}
	if taskPath.MatchString(path) {
		return "/api/v1/tasks/{id}"
	}
	if completeRoute.MatchString(path) {
		return "/api/v1/projects/{id}/complete"
	}
	if dropRoute.MatchString(path) {
		return "/api/v1/projects/{id}/drop"
	}
	if reopenRoute.MatchString(path) {
		return "/api/v1/projects/{id}/reopen"
	}
	if archiveRoute.MatchString(path) {
		return "/api/v1/projects/{id}/archive"
	}
	if projectRoute.MatchString(path) {
		return "/api/v1/projects/{id}"
	}
	return "unmatched"
}

var (
	actionRoute   = regexp.MustCompile(`^/api/v1/tasks/[^/]+/(delegate|undelegate|claim|release|work_ref|delegation_note)$`)
	completeRoute = regexp.MustCompile(`^/api/v1/projects/[^/]+/complete$`)
	dropRoute     = regexp.MustCompile(`^/api/v1/projects/[^/]+/drop$`)
	reopenRoute   = regexp.MustCompile(`^/api/v1/projects/[^/]+/reopen$`)
	archiveRoute  = regexp.MustCompile(`^/api/v1/projects/[^/]+/archive$`)
	projectRoute  = regexp.MustCompile(`^/api/v1/projects/[^/]+$`)
)

// logRequest writes the one structured line per request. It names the ROUTE,
// never the path, and never the store: a log a user pastes into an issue must
// not carry their task ids or file locations.
func (s *Server) logRequest(method, route string, status int, requestID string, started time.Time) {
	if s.options.Logger == nil {
		return
	}
	elapsed := s.options.Clock().Sub(started).Seconds() * 1000
	rounded := float64(int64(elapsed*100+0.5)) / 100
	w := jsonout.New()
	w.BeginObject()
	w.KeyStr("event", "http_request")
	w.KeyStr("request_id", requestID)
	w.KeyStr("method", method)
	w.KeyStr("route", route)
	w.KeyInt("status", status)
	w.Key("duration_ms")
	w.Raw(json.RawMessage(rubyFloat(rounded)))
	w.EndObject()
	line := w.String() + "\n"

	s.logMutex.Lock()
	defer s.logMutex.Unlock()
	_, _ = io.WriteString(s.options.Logger, line)
}

// rubyFloat spells a Float the way Ruby's Float#to_s does for the one value
// this package emits: an integral value keeps its `.0`, which Go's shortest
// round-trip formatting drops.
func rubyFloat(value float64) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	if !strings.ContainsAny(text, ".eE") {
		text += ".0"
	}
	return text
}
