package api

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The change-detection surface: conditional GET on /meta, and the SSE stream.
//
// Both answer one question — has either file changed since I last looked? —
// and both answer it from the global store revision, which is a digest of the
// bytes and nothing else. Neither parses or validates the store to decide that
// nothing changed; that is what makes a one-second poll cheap.

const (
	eventsPath = "/api/v1/events"

	// defaultEventStreamLimit caps concurrently open streams. Every open
	// stream holds one handler goroutine and polls the store, so the cap is the
	// thread budget the contract promised before SSE shipped. A loopback
	// server has one user; eight covers a browser with several tabs.
	defaultEventStreamLimit = 8
	// defaultEventPoll is how often an open stream re-reads the revision.
	defaultEventPoll = time.Second
	// defaultEventHeartbeat is the idle comment interval, well under the
	// timeouts proxies and browsers apply to a silent connection.
	defaultEventHeartbeat = 15 * time.Second
)

// eventStreams is the one piece of cross-request state the stream route needs:
// how many streams are open, and whether the server is shutting down.
type eventStreams struct {
	mu     sync.Mutex
	open   int
	limit  int
	closed bool
	done   chan struct{}
}

func newEventStreams(limit int) *eventStreams {
	if limit <= 0 {
		limit = defaultEventStreamLimit
	}
	return &eventStreams{limit: limit, done: make(chan struct{})}
}

// acquire takes a stream slot, or refuses when the budget is spent or the
// server is shutting down.
func (e *eventStreams) acquire() (func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errorWith(503, "unavailable", "The server is shutting down.")
	}
	if e.open >= e.limit {
		return nil, errorWith(503, "unavailable",
			"Too many open event streams; close one or poll /meta with If-None-Match.").
			withDetails(pairDetails(
				detailPair{Key: "reason", Value: "stream_limit"},
				detailPair{Key: "limit", Value: e.limit},
			)).
			withHeader("retry-after", "5")
	}
	e.open++
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			e.open--
			e.mu.Unlock()
		})
	}, nil
}

// close ends every open stream and refuses new ones. It is idempotent.
func (e *eventStreams) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.closed {
		e.closed = true
		close(e.done)
	}
}

// openStreams is how many streams are live, for tests and diagnostics.
func (e *eventStreams) openStreams() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.open
}

// CloseStreams ends every open event stream and refuses new ones. An
// http.Server's Shutdown waits for handlers to return but never cancels them,
// and a stream never returns on its own, so the command registers this with
// RegisterOnShutdown.
func (s *Server) CloseStreams() { s.streams.close() }

// serveEvents is GET /api/v1/events. It runs on the request's own goroutine
// and starts none, so a stream that ends — client gone, server shutting down,
// write failed — leaves nothing behind.
func (s *Server) serveEvents(writer http.ResponseWriter, request *http.Request, requestID string) {
	started := s.options.Clock()
	status := 200
	defer func() { s.logRequest(request.Method, eventsPath, status, requestID, started) }()

	fail := func(err error) {
		answer := s.errorResponse(err, requestID)
		status = answer.status
		s.write(writer, answer, requestID)
	}
	if err := s.enforceHost(request); err != nil {
		fail(err)
		return
	}
	if _, err := queryParams(request); err != nil {
		fail(err)
		return
	}
	flusher, canFlush := writer.(http.Flusher)
	if !canFlush {
		fail(notImplemented("stream events", "the connection cannot be flushed"))
		return
	}
	release, err := s.streams.acquire()
	if err != nil {
		fail(err)
		return
	}
	defer release()
	current, err := s.options.App.StoreRevision()
	if err != nil {
		fail(unavailableError(err.Error()))
		return
	}

	header := writer.Header()
	header.Set("x-request-id", requestID)
	header.Set("cache-control", "no-store")
	header.Set("content-type", "text/event-stream")
	writer.WriteHeader(200)

	// The first frame is the revision at connect time, so a client that
	// reconnects learns about a change it missed while it was away. A client
	// that reconnects already holding it (EventSource sends the last `id:` as
	// Last-Event-ID) is not told again.
	if request.Header.Get("Last-Event-ID") != current {
		if writeStoreChanged(writer, current) != nil {
			return
		}
	}
	flusher.Flush()

	poll := time.NewTicker(s.eventPoll())
	defer poll.Stop()
	heartbeat := time.NewTicker(s.eventHeartbeat())
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-s.streams.done:
			return
		case <-poll.C:
			revision, err := s.options.App.StoreRevision()
			// A revision that cannot be read right now (a writer holding the
			// lock past the timeout) is not a change; the next tick retries.
			if err != nil || revision == current {
				continue
			}
			current = revision
			if writeStoreChanged(writer, current) != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(writer, ": keep-alive\n\n"); err != nil {
				return
			}
		}
		flusher.Flush()
	}
}

// writeStoreChanged writes one `store.changed` frame. The revision is also the
// event id, which is what lets Last-Event-ID suppress a redundant first frame.
func writeStoreChanged(writer io.Writer, revision string) error {
	_, err := fmt.Fprintf(writer, "id: %s\nevent: store.changed\ndata: {\"store_revision\":%q}\n\n",
		revision, revision)
	return err
}

func (s *Server) eventPoll() time.Duration {
	if s.options.EventPollInterval > 0 {
		return s.options.EventPollInterval
	}
	return defaultEventPoll
}

func (s *Server) eventHeartbeat() time.Duration {
	if s.options.EventHeartbeat > 0 {
		return s.options.EventHeartbeat
	}
	return defaultEventHeartbeat
}

// noneMatch reports whether an If-None-Match header names the entity tag whose
// unquoted value is `value` (for /meta, the revision joined to its document
// digest).
//
// If-None-Match uses the weak comparison (RFC 9110 §13.1.2), so `W/"…"` matches
// the strong tag this server issues, and a list of tags matches when any one
// does. `*` matches any current representation, which is only known after a
// successful read, so the cheap path ignores it and the full read decides.
func noneMatch(header string, value string, allowStar bool) bool {
	if header == "" || value == "" {
		return false
	}
	for _, candidate := range strings.Split(header, ",") {
		tag := strings.TrimSpace(candidate)
		if tag == "*" {
			if allowStar {
				return true
			}
			continue
		}
		tag = strings.TrimPrefix(tag, "W/")
		if tag == etag(value) {
			return true
		}
	}
	return false
}
