package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/taskquery"
)

// -- conditional GET on /meta ---------------------------------------------------

func TestMetaAnswersAMatchingIfNoneMatchWith304AndNoBody(t *testing.T) {
	h := newHarness(t)
	first := h.get("/api/v1/meta")
	assertStatus(t, first, 200)
	tag := first.etag()
	if first.Header.Get("cache-control") != "no-cache" {
		t.Errorf("meta cache-control = %q", first.Header.Get("cache-control"))
	}

	for _, header := range []string{tag, "W/" + tag, `"s1.other", ` + tag, "*"} {
		cached := h.do(request{method: "GET", path: "/api/v1/meta",
			headers: map[string]string{"If-None-Match": header}})
		if cached.Status != 304 || cached.Body != "" {
			t.Fatalf("If-None-Match %s: %d %q", header, cached.Status, cached.Body)
		}
		if cached.etag() != tag {
			t.Errorf("304 etag = %q, want %q", cached.etag(), tag)
		}
		if cached.Header.Get("content-length") != "" || cached.Header.Get("content-type") != "" {
			t.Errorf("304 carries entity headers: %v", cached.Header)
		}
	}

	miss := h.do(request{method: "GET", path: "/api/v1/meta",
		headers: map[string]string{"If-None-Match": `"s1.other"`}})
	assertStatus(t, miss, 200)

	h.patchPriority(fixFlight, "C")
	changed := h.do(request{method: "GET", path: "/api/v1/meta",
		headers: map[string]string{"If-None-Match": tag}})
	assertStatus(t, changed, 200)
	if changed.etag() == tag {
		t.Fatal("a write did not change the meta ETag")
	}
}

// The /meta body is a function of the store AND the process's configuration and
// build, so a restart under different config must not be answered 304 against a
// tag from before it, even though the store bytes are identical.
func TestMetaETagChangesWithConfigurationOverTheSameStore(t *testing.T) {
	h := newHarness(t)
	before := h.get("/api/v1/meta")
	assertStatus(t, before, 200)
	tag := before.etag()
	storeRevision, _ := before.dig("meta", "store_revision").(string)

	for name, change := range map[string]func(*Options){
		"timezone":    func(o *Options) { o.Timezone = "Europe/Berlin" },
		"date_order":  func(o *Options) { o.DateOrder = "dmy" },
		"time_format": func(o *Options) { o.TimeFormat = 24 },
		"max_depth":   func(o *Options) { o.MaxDepth = 6 },
		"urgent_days": func(o *Options) {
			o.QueryOptions = append(append([]taskquery.Option{}, o.QueryOptions...), taskquery.WithUrgentDays(7))
		},
	} {
		options := h.server.options
		change(&options)
		restarted, err := New(options)
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "/api/v1/meta", nil)
		request.Host = "127.0.0.1:4747"
		request.Header.Set("If-None-Match", tag)
		restarted.ServeHTTP(recorder, request)
		if recorder.Code != 200 {
			t.Errorf("%s changed but the old tag was answered %d", name, recorder.Code)
			continue
		}
		var body struct {
			Meta struct {
				StoreRevision string `json:"store_revision"`
			} `json:"meta"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &body)
		if body.Meta.StoreRevision != storeRevision {
			t.Errorf("%s: store_revision = %q, want the unchanged %q", name, body.Meta.StoreRevision, storeRevision)
		}
		if recorder.Header().Get("etag") == tag {
			t.Errorf("%s changed but the ETag did not", name)
		}
	}

	// The bare store revision — what an /events frame carries — is not a /meta
	// tag, so a client that builds one from an SSE frame gets a 200, never a
	// stale 304.
	bare := h.do(request{method: "GET", path: "/api/v1/meta",
		headers: map[string]string{"If-None-Match": `"` + storeRevision + `"`}})
	assertStatus(t, bare, 200)
}

// The 304 is the cheap path: it must not open a checked snapshot.
func TestMetaNotModifiedSkipsTheCheckedRead(t *testing.T) {
	h := newHarness(t)
	var reads atomic.Int64
	options := h.server.options
	inner := options.Read
	options.Read = func() (CheckedRead, error) {
		reads.Add(1)
		return inner()
	}
	server, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	h.server = server
	tag := h.get("/api/v1/meta").etag()
	counted := reads.Load()
	for index := 0; index < 5; index++ {
		cached := h.do(request{method: "GET", path: "/api/v1/meta",
			headers: map[string]string{"If-None-Match": tag}})
		assertStatus(t, cached, 304)
	}
	if reads.Load() != counted {
		t.Fatalf("five 304s performed %d checked reads", reads.Load()-counted)
	}
}

// A revision the client never received a 200 for — here, one taken while the
// store is invalid — must not be confirmed by `*`, which only a successful read
// may answer.
func TestMetaIfNoneMatchStarStillRefusesAnInvalidStore(t *testing.T) {
	h := newHarness(t)
	h.writeStore("{not-json\n")
	answered := h.do(request{method: "GET", path: "/api/v1/meta",
		headers: map[string]string{"If-None-Match": "*"}})
	assertError(t, answered, 503, "store_invalid")
}

// -- /events --------------------------------------------------------------------

// openAndCancelStream drives one stream whose client is already gone, which is
// the cheapest proof the route is dispatched and returns on its own.
func openAndCancelStream(t *testing.T, server *Server) *httptest.ResponseRecorder {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest("GET", eventsPath, nil).WithContext(ctx)
	request.Host = "127.0.0.1:4747"
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, request)
	return recorder
}

// eventServer is a real HTTP server over the harness's store with a fast poll,
// so a test observes frames on a live connection.
func eventServer(t *testing.T, h *harness, limit int) (*Server, *httptest.Server) {
	t.Helper()
	options := h.server.options
	options.EventStreamLimit = limit
	options.EventPollInterval = 10 * time.Millisecond
	options.EventHeartbeat = 40 * time.Millisecond
	// Its own log sink: the harness's builder is serialized by the harness
	// server's mutex, which this second server does not share.
	options.Logger = &strings.Builder{}
	server, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	// Close blocks until every handler has returned, so a stream that
	// outlived its client would hang the test here rather than leak silently.
	live := httptest.NewServer(server)
	t.Cleanup(live.Close)
	return server, live
}

type stream struct {
	response *http.Response
	cancel   context.CancelFunc
	lines    chan string
}

func openStream(t *testing.T, live *httptest.Server, headers map[string]string) (*stream, *http.Response) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, "GET", live.URL+eventsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "127.0.0.1:4747"
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := live.Client().Do(request)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if response.StatusCode != 200 {
		cancel()
		return nil, response
	}
	opened := &stream{response: response, cancel: cancel, lines: make(chan string, 64)}
	go func() {
		defer close(opened.lines)
		scanner := bufio.NewScanner(response.Body)
		for scanner.Scan() {
			opened.lines <- scanner.Text()
		}
	}()
	t.Cleanup(opened.close)
	return opened, response
}

func (s *stream) close() {
	s.cancel()
	_ = s.response.Body.Close()
}

// nextEvent reads until one complete frame, skipping heartbeat comments.
func (s *stream) nextEvent(t *testing.T) map[string]string {
	t.Helper()
	frame := map[string]string{}
	deadline := time.After(3 * time.Second)
	for {
		select {
		case line, open := <-s.lines:
			if !open {
				t.Fatal("stream closed before a frame arrived")
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			if line == "" {
				if len(frame) > 0 {
					return frame
				}
				continue
			}
			name, value, _ := strings.Cut(line, ": ")
			frame[name] = value
		case <-deadline:
			t.Fatal("no event within 3s")
		}
	}
}

// nextComment reads until a heartbeat comment line.
func (s *stream) nextComment(t *testing.T) string {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case line, open := <-s.lines:
			if !open {
				t.Fatal("stream closed before a heartbeat")
			}
			if strings.HasPrefix(line, ":") {
				return line
			}
			if strings.HasPrefix(line, "event:") {
				t.Fatalf("unexpected event before heartbeat: %q", line)
			}
		case <-deadline:
			t.Fatal("no heartbeat within 3s")
		}
	}
}

func frameRevision(t *testing.T, frame map[string]string) string {
	t.Helper()
	if frame["event"] != "store.changed" {
		t.Fatalf("frame = %v", frame)
	}
	var payload struct {
		StoreRevision string `json:"store_revision"`
	}
	if err := json.Unmarshal([]byte(frame["data"]), &payload); err != nil {
		t.Fatalf("frame data %q: %v", frame["data"], err)
	}
	if frame["id"] != payload.StoreRevision {
		t.Fatalf("frame id %q != data revision %q", frame["id"], payload.StoreRevision)
	}
	return payload.StoreRevision
}

func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEventsStreamAnnouncesStoreChanges(t *testing.T) {
	h := newHarness(t)
	_, live := eventServer(t, h, 0)
	opened, response := openStream(t, live, nil)
	if response.Header.Get("content-type") != "text/event-stream" {
		t.Fatalf("content-type = %q", response.Header.Get("content-type"))
	}
	if got := frameRevision(t, opened.nextEvent(t)); got != h.storeRevision() {
		t.Fatalf("first frame %q, store %q", got, h.storeRevision())
	}

	h.patchPriority(fixFlight, "C")
	if got := frameRevision(t, opened.nextEvent(t)); got != h.storeRevision() {
		t.Fatalf("change frame %q, store %q", got, h.storeRevision())
	}
	// An out-of-band write — another process, an editor — is a change too.
	// The write is not atomic, so the poll may also catch the truncated file
	// in between; the stream must still converge on the final bytes.
	h.writeStore(strings.Replace(string(h.storeBytes()), "Water the plants", "Water the ferns", 1))
	want := h.storeRevisionFromFiles()
	for got := ""; got != want; {
		got = frameRevision(t, opened.nextEvent(t))
	}
	if comment := opened.nextComment(t); comment != ": keep-alive" {
		t.Fatalf("heartbeat = %q", comment)
	}
}

func TestEventsSkipsTheFirstFrameForAClientThatIsCurrent(t *testing.T) {
	h := newHarness(t)
	_, live := eventServer(t, h, 0)
	opened, _ := openStream(t, live, map[string]string{"Last-Event-ID": h.storeRevision()})
	opened.nextComment(t)
	h.patchPriority(fixFlight, "C")
	if got := frameRevision(t, opened.nextEvent(t)); got != h.storeRevision() {
		t.Fatalf("change frame %q", got)
	}
}

func TestEventsCapOpenStreamsAndReleaseOnDisconnect(t *testing.T) {
	h := newHarness(t)
	server, live := eventServer(t, h, 1)
	first, _ := openStream(t, live, nil)
	first.nextEvent(t)

	_, refused := openStream(t, live, nil)
	if refused.StatusCode != 503 {
		t.Fatalf("second stream = %d", refused.StatusCode)
	}
	var envelope struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.NewDecoder(refused.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	_ = refused.Body.Close()
	if envelope.Error.Code != "unavailable" || envelope.Error.Details["reason"] != "stream_limit" ||
		refused.Header.Get("retry-after") == "" {
		t.Fatalf("refusal = %+v %v", envelope, refused.Header)
	}

	first.close()
	waitFor(t, "the disconnected stream to release its slot", func() bool {
		return server.streams.openStreams() == 0
	})
	again, _ := openStream(t, live, nil)
	if again == nil {
		t.Fatal("a freed slot was not reusable")
	}
	again.nextEvent(t)
}

func TestCloseStreamsEndsOpenStreamsAndRefusesNewOnes(t *testing.T) {
	h := newHarness(t)
	server, live := eventServer(t, h, 0)
	opened, _ := openStream(t, live, nil)
	opened.nextEvent(t)

	server.CloseStreams()
	waitFor(t, "the stream to end", func() bool { return server.streams.openStreams() == 0 })
	deadline := time.After(3 * time.Second)
	for drained := false; !drained; {
		select {
		case _, open := <-opened.lines:
			drained = !open
		case <-deadline:
			t.Fatal("the client never saw the stream end")
		}
	}
	_, refused := openStream(t, live, nil)
	if refused.StatusCode != 503 {
		t.Fatalf("stream after shutdown = %d", refused.StatusCode)
	}
	_ = refused.Body.Close()
	server.CloseStreams() // idempotent
}

func TestEventsEnforceHostAndQuery(t *testing.T) {
	h := newHarness(t)
	wrongHost := h.do(request{method: "GET", path: eventsPath, headers: map[string]string{"Host": "evil.example"}})
	assertError(t, wrongHost, 400, "malformed_request")
	assertError(t, h.get(eventsPath+"?since=1"), 422, "validation_failed")
	if !strings.Contains(h.logs.String(), `"route":"/api/v1/events"`) {
		t.Fatalf("events requests are not logged by route: %s", h.logs.String())
	}
}
