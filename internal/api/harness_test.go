package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/tasks/internal/application"
	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/taskquery"
	"github.com/marcus/tasks/internal/temporal"
)

// The shared fixture. It is test/test_helper.rb's FIXTURE_RECORDS with the two
// additions test/api/test_app.rb's `api_fixture` makes — a weekly recurrence on
// the flight task, a defer tag on the plants task, and the child/grandchild pair
// under the PR task — so a Go test named after a Ruby one is answering the same
// question about the same data.
const (
	fixInbox  = "aaaa0001"
	fixGarden = "aaaa0002"
	fixWork   = "aaaa0003"
	fixFlight = "aaaa0004"
	fixPR     = "aaaa0005"
	fixEval   = "aaaa0006"
	fixTravel = "aaaa0007"
	fixOld    = "aaaa0008"
	fixHome   = "aaaa0009"
	fixPlants = "aaaa000a"
	fixChild  = "bbbb0001"
	fixGrand  = "bbbb0002"
)

const fixtureOrg = `{"type":"meta","version":2}
{"type":"section","id":"aaaa0001","title":"Inbox"}
{"type":"task","id":"aaaa0002","parent":"aaaa0001","state":"INBOX","title":"random thought about the garden"}
{"type":"section","id":"aaaa0003","title":"Work"}
{"type":"task","id":"aaaa0004","parent":"aaaa0003","state":"NEXT","priority":"A","title":"Book flight in Concur","tags":["@computer","important","urgent"],"deadline":"2026-07-02","recur":".+1w"}
{"type":"task","id":"aaaa0005","parent":"aaaa0003","state":"NEXT","priority":"B","title":"Review PR backlog","tags":["@computer","important"]}
{"type":"task","id":"bbbb0001","parent":"aaaa0005","state":"TODO","title":"Child"}
{"type":"task","id":"bbbb0002","parent":"bbbb0001","state":"TODO","title":"Grandchild"}
{"type":"task","id":"aaaa0006","parent":"aaaa0003","state":"TODO","priority":"A","title":"Midyear self-eval","tags":["@computer","important"],"scheduled":"2026-07-03"}
{"type":"task","id":"aaaa0007","parent":"aaaa0003","state":"WAITING","title":"Travel desk reply","tags":["@email","urgent"],"body":"Some note line."}
{"type":"task","id":"aaaa0008","parent":"aaaa0003","state":"DONE","priority":"C","title":"Old finished thing","tags":["@computer"],"closed":"2026-06-20"}
{"type":"section","id":"aaaa0009","title":"Home"}
{"type":"task","id":"aaaa000a","parent":"aaaa0009","state":"NEXT","title":"Water the plants","tags":["@home","defer"]}
`

const fixtureArchive = `{"type":"meta","version":2}
{"type":"section","id":"cccc0001","title":"Archive"}
{"type":"task","id":"aaaa0005","parent":"cccc0001","state":"DONE","title":"Archived duplicate","closed":"2026-07-01"}
{"type":"task","id":"dddd0001","parent":"cccc0001","state":"DONE","title":"Archived anchor","closed":"2026-07-02"}
`

// harness is one server over one throwaway store pair.
type harness struct {
	t       *testing.T
	dir     string
	org     string
	archive string
	server  *Server
	logs    *strings.Builder
	now     time.Time
	ids     chan string
}

// newHarness builds a server whose clock and id mint are PINNED. Both are
// injected rather than read from the environment for the reason
// lib/tasks/determinism.rb states: the adapter boundary is the one place a pin
// turns into a value, and a test that could not pin them would be asserting
// against the wall clock.
func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWith(t, fixtureOrg, fixtureArchive, "")
}

func newHarnessWith(t *testing.T, org, archive, hostContext string) *harness {
	t.Helper()
	return build(t, t.TempDir(), org, archive, hostContext, true)
}

// newHarnessSharing builds a SECOND independent server over the first's files —
// no shared Go value between them, which is the point: it stands in for a second
// process, and anything the API got right only by holding state in memory fails
// against it.
func newHarnessSharing(t *testing.T, other *harness) *harness {
	t.Helper()
	h := build(t, other.dir, "", "", "", false)
	return h
}

func build(t *testing.T, dir, org, archive, hostContext string, seed bool) *harness {
	t.Helper()
	h := &harness{
		t:       t,
		dir:     dir,
		org:     filepath.Join(dir, "tasks.jsonl"),
		archive: filepath.Join(dir, "archive.jsonl"),
		logs:    &strings.Builder{},
		now:     time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
		ids:     make(chan string, 64),
	}
	if seed {
		// The pinned id sequence belongs to the FIRST server over a store. A
		// second server minting from the same pinned sequence would collide on
		// purpose, which is not what the concurrency tests are asking about, so
		// a sharing harness leaves the mint random.
		for index := 1; index <= 64; index++ {
			h.ids <- fmt.Sprintf("eeee%04x", index)
		}
		if err := os.WriteFile(h.org, []byte(org), 0o644); err != nil {
			t.Fatalf("write org: %v", err)
		}
		if archive != "" {
			if err := os.WriteFile(h.archive, []byte(archive), 0o644); err != nil {
				t.Fatalf("write archive: %v", err)
			}
		}
	} else {
		close(h.ids)
	}

	context := func() temporal.Context {
		built, err := temporal.NewContext(h.now, "Etc/UTC", 12)
		if err != nil {
			t.Fatalf("temporal context: %v", err)
		}
		return built
	}
	newStore := func() *store.Store {
		options := store.Options{
			JournalDir: filepath.Join(dir, "journal"),
			Device:     "pinned",
			Now:        func() time.Time { return h.now },
			MaxDepth:   4,
		}
		if seed {
			options.IDSource = func() string { return <-h.ids }
		}
		return store.NewWriter(h.org, h.archive, options)
	}
	app, err := application.New(application.Options{
		Factory:         func() application.Store { return newStore() },
		TemporalContext: context,
		HostContext:     hostContext,
	})
	if err != nil {
		t.Fatalf("application: %v", err)
	}
	var requestCount atomic.Int64
	server, err := New(Options{
		App:             app,
		Read:            NewStoreReader(newStore, context),
		Changesets:      func() Changesets { return newStore() },
		TemporalContext: context,
		QueryOptions:    []taskquery.Option{},
		Port:            4747,
		MaxDepth:        4,
		UrgentDays:      3,
		Timezone:        "Etc/UTC",
		TimeFormat:      12,
		Logger:          h.logs,
		RequestIDs: func() string {
			// Atomic because the concurrency tests drive one server from many
			// goroutines, exactly as an http.Server does.
			return fmt.Sprintf("req_%016x", requestCount.Add(1))
		},
		Clock: func() time.Time { return h.now },
	})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	h.server = server
	return h
}

// answer is one response, decoded enough for a test to assert on.
type answer struct {
	Status  int
	Header  http.Header
	Body    string
	Decoded map[string]any
}

func (a answer) etag() string { return a.Header.Get("etag") }

// dig walks the decoded body, returning nil for a missing path.
func (a answer) dig(path ...string) any {
	var current any = a.Decoded
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

func (a answer) code() string {
	value, _ := a.dig("error", "code").(string)
	return value
}

func (a answer) message() string {
	value, _ := a.dig("error", "message").(string)
	return value
}

// data is the `data` member as an object.
func (a answer) data() map[string]any {
	value, _ := a.dig("data").(map[string]any)
	return value
}

// rows is the `data` member as an array of objects.
func (a answer) rows() []map[string]any {
	list, _ := a.dig("data").([]any)
	rows := make([]map[string]any, 0, len(list))
	for _, element := range list {
		if object, ok := element.(map[string]any); ok {
			rows = append(rows, object)
		}
	}
	return rows
}

// ids is the id of every row, which is what most list assertions compare.
func (a answer) ids() []string {
	values := []string{}
	for _, row := range a.rows() {
		value, _ := row["id"].(string)
		values = append(values, value)
	}
	return values
}

type request struct {
	method  string
	path    string
	body    string
	headers map[string]string
	// noContentType suppresses the default application/json, for the media-type
	// refusals.
	contentType string
}

func (h *harness) do(r request) answer {
	h.t.Helper()
	var reader io.Reader
	if r.body != "" {
		reader = strings.NewReader(r.body)
	}
	httpRequest := httptest.NewRequest(r.method, r.path, reader)
	httpRequest.Host = "127.0.0.1:4747"
	if r.body != "" {
		contentType := r.contentType
		if contentType == "" {
			contentType = "application/json"
		}
		httpRequest.Header.Set("Content-Type", contentType)
		httpRequest.Header.Set("Content-Length", fmt.Sprint(len(r.body)))
	} else if r.contentType != "" {
		httpRequest.Header.Set("Content-Type", r.contentType)
	}
	for name, value := range r.headers {
		if name == "Host" {
			httpRequest.Host = value
			continue
		}
		httpRequest.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	h.server.ServeHTTP(recorder, httpRequest)
	result := recorder.Result()
	body, _ := io.ReadAll(result.Body)
	answered := answer{Status: result.StatusCode, Header: result.Header, Body: string(body)}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &answered.Decoded)
	}
	return answered
}

func (h *harness) get(path string) answer { return h.do(request{method: "GET", path: path}) }

func (h *harness) json(method, path, body string, headers map[string]string) answer {
	return h.do(request{method: method, path: path, body: body, headers: headers})
}

func (h *harness) withIfMatch(tag string) map[string]string {
	return map[string]string{"If-Match": tag}
}

// etagOf reads a task and returns its current ETag, which is how every write in
// these tests obtains its precondition.
func (h *harness) etagOf(id string) string {
	h.t.Helper()
	answered := h.get("/api/v1/tasks/" + id)
	if answered.Status != 200 {
		h.t.Fatalf("etagOf(%s): %d %s", id, answered.Status, answered.Body)
	}
	return answered.etag()
}

func (h *harness) storeBytes() []byte {
	h.t.Helper()
	raw, err := os.ReadFile(h.org)
	if err != nil {
		h.t.Fatalf("read store: %v", err)
	}
	return raw
}

func (h *harness) writeStore(content string) {
	h.t.Helper()
	if err := os.WriteFile(h.org, []byte(content), 0o644); err != nil {
		h.t.Fatalf("write store: %v", err)
	}
}

func assertStatus(t *testing.T, answered answer, want int) {
	t.Helper()
	if answered.Status != want {
		t.Fatalf("status = %d, want %d (body %s)", answered.Status, want, answered.Body)
	}
}

// assertError is test_app.rb's assert_error: the status, the machine code, a
// string message, an object of details, and a well-formed request id.
func assertError(t *testing.T, answered answer, status int, code string) {
	t.Helper()
	assertStatus(t, answered, status)
	if got := answered.code(); got != code {
		t.Fatalf("error code = %q, want %q (body %s)", got, code, answered.Body)
	}
	if answered.message() == "" {
		t.Errorf("error message is empty: %s", answered.Body)
	}
	if _, ok := answered.dig("error", "details").(map[string]any); !ok {
		t.Errorf("error details is not an object: %s", answered.Body)
	}
	requestID, _ := answered.dig("error", "request_id").(string)
	if !strings.HasPrefix(requestID, "req_") {
		t.Errorf("request_id = %q", requestID)
	}
}

func assertStrings(t *testing.T, got, want []string, context string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", context, got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("%s = %v, want %v", context, got, want)
		}
	}
}

func stringsOf(value any) []string {
	list, _ := value.([]any)
	values := []string{}
	for _, element := range list {
		text, _ := element.(string)
		values = append(values, text)
	}
	return values
}
