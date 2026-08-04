package agent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Mirrors test/test_agent_queue.rb, plus the queue-level invariants of
// test/test_app_agent_queue.rb (its App-level assertions belong to the shell
// package), plus a concurrency test Ruby could not have.

type fakeAgent struct {
	mu            sync.Mutex
	availability  []bool
	success       bool
	finalOutput   string
	startErr      error
	pumpErr       error
	started       [][2]string
	output        string
	processStatus ProcessStatus
	exitStatus    *int
	running       bool
	cancelled     bool
}

func newFake() *fakeAgent {
	return &fakeAgent{success: true, finalOutput: "ok"}
}

func (f *fakeAgent) Available() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.availability) == 0 {
		return true
	}
	if len(f.availability) > 1 {
		v := f.availability[0]
		f.availability = f.availability[1:]
		return v
	}
	return f.availability[0]
}

func (f *fakeAgent) Start(prompt, model string) error {
	if f.startErr != nil {
		return f.startErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, [2]string{prompt, model})
	f.running = true
	return nil
}

func (f *fakeAgent) Pump() (bool, error) {
	if f.pumpErr != nil {
		return false, f.pumpErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.output += f.finalOutput
	f.running = false
	code := 0
	if !f.success {
		code = 7
	}
	f.exitStatus = &code
	f.processStatus = ProcessStatus{Present: true, Exited: true, ExitStatus: code}
	return true, nil
}

func (f *fakeAgent) Cancel() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancelled = true
	f.running = false
	f.processStatus = ProcessStatus{Present: true, Signaled: true, Signal: 15}
	f.exitStatus = nil
	return nil
}

func (f *fakeAgent) Output() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.output
}

func (f *fakeAgent) setOutput(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.output = s
}

func (f *fakeAgent) startedCalls() [][2]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][2]string(nil), f.started...)
}

func (f *fakeAgent) Success() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.success && !f.cancelled
}

func (f *fakeAgent) ExitStatus() (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.exitStatus == nil {
		return 0, false
	}
	return *f.exitStatus, true
}

func (f *fakeAgent) ProcessStatus() ProcessStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.processStatus
}

func entry(model string) SimpleEntry {
	return SimpleEntry{ProviderName: "claude-cli", ModelName: model, Label: "claude:" + model}
}

func entryFor(provider, model string) SimpleEntry {
	return SimpleEntry{ProviderName: provider, ModelName: model, Label: provider + ":" + model}
}

// queueWith builds a queue whose factory hands out the given fakes in order and
// records which entry each build was asked for.
func queueWith(t *testing.T, agents []*fakeAgent, mutate func(*Options)) (*Queue, *[]Entry) {
	t.Helper()
	built := &[]Entry{}
	remaining := append([]*fakeAgent(nil), agents...)
	opts := Options{
		Factory: func(e Entry) (Adapter, error) {
			*built = append(*built, e)
			if len(remaining) == 0 {
				return nil, errors.New("no fake agent left")
			}
			next := remaining[0]
			remaining = remaining[1:]
			return next, nil
		},
		// Default probe = available: the enqueue-time rejection is exercised
		// explicitly by passing Availability.
		Availability: func(Entry) bool { return true },
	}
	if mutate != nil {
		mutate(&opts)
	}
	q, err := NewQueue(opts)
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	return q, built
}

func prompts(snaps []Snapshot) []string {
	out := make([]string, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.Prompt)
	}
	return out
}

func statuses(snaps []Snapshot) []Status {
	out := make([]Status, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.Status)
	}
	return out
}

func outputs(snaps []Snapshot) []string {
	out := make([]string, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, s.Output)
	}
	return out
}

func equalStrings(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRunsThreeRequestsFIFOWithOnlyOneLiveAdapter(t *testing.T) {
	agents := []*fakeAgent{newFake(), newFake(), newFake()}
	agents[0].finalOutput, agents[1].finalOutput, agents[2].finalOutput = "one", "two", "three"
	q, _ := queueWith(t, agents, nil)
	for _, prompt := range []string{"first", "second", "third"} {
		if !q.Enqueue(prompt, entry("sonnet")).Accepted() {
			t.Fatalf("%q was rejected", prompt)
		}
	}

	if got := q.StartNext(); got.Type != Started {
		t.Fatalf("start = %#v", got)
	}
	if calls := agents[0].startedCalls(); len(calls) != 1 || calls[0] != [2]string{"first", "sonnet"} {
		t.Fatalf("first agent started %v", calls)
	}
	if len(agents[1].startedCalls()) != 0 {
		t.Fatal("a second adapter must not be live")
	}
	if q.PendingCount() != 2 {
		t.Fatalf("pending = %d", q.PendingCount())
	}

	first := q.Pump().Request
	if first.Status != Succeeded || first.Output != "one" {
		t.Fatalf("first = %#v", first)
	}
	if got := q.StartNext(); got.Type != Started {
		t.Fatalf("second start = %#v", got)
	}
	if calls := agents[1].startedCalls(); len(calls) != 1 || calls[0] != [2]string{"second", "sonnet"} {
		t.Fatalf("second agent started %v", calls)
	}
	if second := q.Pump().Request; second.Output != "two" {
		t.Fatalf("second output = %q", second.Output)
	}
	q.StartNext()
	third := q.Pump().Request

	all := q.Requests()
	for _, s := range statuses(all) {
		if s != Succeeded {
			t.Fatalf("statuses = %v", statuses(all))
		}
	}
	if !equalStrings(outputs(all), "one", "two", "three") {
		t.Fatalf("outputs = %v", outputs(all))
	}
	if third.Output != "three" {
		t.Fatalf("third = %q", third.Output)
	}
	if q.Work() {
		t.Fatal("queue still reports work")
	}
}

func TestEachRequestSnapshotsItsProviderAndModel(t *testing.T) {
	agents := []*fakeAgent{newFake(), newFake()}
	q, built := queueWith(t, agents, nil)
	first := entry("sonnet")
	second := entryFor("hermes", "qwen")
	q.Enqueue("one", first)
	q.StartNext()
	// The adapter is built at start time, so only the started request's entry
	// has reached the factory — but both snapshots already carry their own.
	if len(*built) != 1 || (*built)[0] != Entry(first) {
		t.Fatalf("built = %v", *built)
	}
	q.Enqueue("two", second)
	requests := q.Requests()
	if requests[0].Entry != Entry(first) || requests[1].Entry != Entry(second) {
		t.Fatalf("entries = %v / %v", requests[0].Entry, requests[1].Entry)
	}
	q.Pump()
	q.StartNext()
	if len(*built) != 2 || (*built)[1] != Entry(second) {
		t.Fatalf("built = %v", *built)
	}
	if calls := agents[1].startedCalls(); len(calls) != 1 || calls[0] != [2]string{"two", "qwen"} {
		t.Fatalf("second agent started %v", calls)
	}
}

// Ruby freezes a dup of the entry so a later UI selection cannot rewrite a
// queued request. In Go the same guarantee comes from the entry being a value:
// mutating the caller's copy after submission cannot reach the snapshot.
func TestEntrySnapshotCannotBeChangedBySourceOrConsumer(t *testing.T) {
	q, built := queueWith(t, []*fakeAgent{newFake()}, nil)
	source := entry("sonnet")
	accepted := q.Enqueue("stable", source)

	source.ModelName = "opus"
	if accepted.Request.Entry.Model() != "sonnet" {
		t.Fatalf("snapshot model = %q", accepted.Request.Entry.Model())
	}
	if q.Requests()[0].Entry.Model() != "sonnet" {
		t.Fatalf("stored model = %q", q.Requests()[0].Entry.Model())
	}
	q.StartNext()
	if (*built)[0].Model() != "sonnet" {
		t.Fatalf("factory saw model %q", (*built)[0].Model())
	}
}

func TestRejectsUnavailableOrOverCapacityWithoutRecordingRequest(t *testing.T) {
	// An unavailable provider is rejected by the enqueue-time probe, before any
	// adapter is built — so no fake agent is even consumed.
	q, built := queueWith(t, nil, func(o *Options) {
		o.Availability = func(Entry) bool { return false }
	})
	rejected := q.Enqueue("keep me", entry("sonnet"))
	if rejected.Accepted() || !strings.Contains(rejected.Error, "not available") {
		t.Fatalf("rejection = %#v", rejected)
	}
	if len(q.Requests()) != 0 || len(*built) != 0 {
		t.Fatal("a rejected request must record nothing and build no adapter")
	}

	agents := []*fakeAgent{newFake(), newFake(), newFake()}
	q, _ = queueWith(t, agents, func(o *Options) { o.MaxPending = 1 })
	q.Enqueue("active", entry("sonnet"))
	q.StartNext()
	if !q.Enqueue("waiting", entry("sonnet")).Accepted() {
		t.Fatal("the first waiting request must be accepted")
	}
	full := q.Enqueue("extra", entry("sonnet"))
	if full.Accepted() || !strings.Contains(full.Error, "queue is full") {
		t.Fatalf("full = %#v", full)
	}
	if !equalStrings(prompts(q.Requests()), "active", "waiting") {
		t.Fatalf("requests = %v", prompts(q.Requests()))
	}
}

func TestStartFailureIsRecordedAndDoesNotStrandLaterWork(t *testing.T) {
	broken := newFake()
	broken.startErr = errors.New("gone")
	good := newFake()
	q, _ := queueWith(t, []*fakeAgent{broken, good}, nil)
	q.Enqueue("broken", entry("sonnet"))
	q.Enqueue("good", entry("sonnet"))

	failed := q.StartNext()
	if failed.Request.Status != Failed || !strings.Contains(failed.Request.Error, "could not start") {
		t.Fatalf("failed = %#v", failed.Request)
	}
	if got := q.StartNext(); got.Type != Started {
		t.Fatalf("next start = %#v", got)
	}
	if calls := good.startedCalls(); len(calls) != 1 || calls[0] != [2]string{"good", "sonnet"} {
		t.Fatalf("good agent started %v", calls)
	}
}

func TestFactoryErrorAtStartBecomesAFailedEventNotACrash(t *testing.T) {
	// A factory that fails models a memory-context build error. The first
	// request fails carrying the message; a later request whose factory
	// succeeds still starts.
	good := newFake()
	calls := 0
	q, err := NewQueue(Options{
		Factory: func(Entry) (Adapter, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("task-set memory over budget")
			}
			return good, nil
		},
		Availability: func(Entry) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	q.Enqueue("first", entry("sonnet"))
	q.Enqueue("second", entry("sonnet"))

	failed := q.StartNext()
	if failed.Request.Status != Failed || !strings.Contains(failed.Request.Error, "task-set memory over budget") {
		t.Fatalf("failed = %#v", failed.Request)
	}
	if q.Active() {
		t.Fatal("a failed start must leave no active request")
	}
	if got := q.StartNext(); got.Type != Started {
		t.Fatalf("second start = %#v", got)
	}
	if calls := good.startedCalls(); len(calls) != 1 || calls[0] != [2]string{"second", "sonnet"} {
		t.Fatalf("good agent started %v", calls)
	}
}

func TestProviderBecomingUnavailableAtStartFailsOneRequest(t *testing.T) {
	// The probe passes at enqueue, but the adapter built at start reports itself
	// unavailable — the request fails cleanly rather than starting.
	a := newFake()
	a.availability = []bool{false}
	q, _ := queueWith(t, []*fakeAgent{a}, nil)
	if !q.Enqueue("later", entry("sonnet")).Accepted() {
		t.Fatal("enqueue rejected")
	}
	event := q.StartNext()
	if event.Request.Status != Failed || !strings.Contains(event.Request.Error, "became unavailable") {
		t.Fatalf("event = %#v", event.Request)
	}
	if q.Work() {
		t.Fatal("queue still reports work")
	}
}

func TestNonzeroExitRetainsOutputAndError(t *testing.T) {
	a := newFake()
	a.success = false
	a.finalOutput = "partial transcript"
	q, _ := queueWith(t, []*fakeAgent{a}, nil)
	q.Enqueue("fail", entry("sonnet"))
	q.StartNext()
	result := q.Pump().Request

	if result.Status != Failed {
		t.Fatalf("status = %q", result.Status)
	}
	if result.ExitStatus == nil || *result.ExitStatus != 7 {
		t.Fatalf("exit status = %v", result.ExitStatus)
	}
	if result.Output != "partial transcript" {
		t.Fatalf("output = %q", result.Output)
	}
	if !strings.Contains(result.Error, "exited 7") {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestPumpExceptionFailsOneRequestCleansUpAndAllowsNext(t *testing.T) {
	broken := newFake()
	broken.pumpErr = errors.New("read exploded")
	good := newFake()
	q, _ := queueWith(t, []*fakeAgent{broken, good}, nil)
	q.Enqueue("broken", entry("sonnet"))
	q.Enqueue("good", entry("sonnet"))
	q.StartNext()

	failed := q.Pump()
	if failed.Request.Status != Failed || !strings.Contains(failed.Request.Error, "stream failed: read exploded") {
		t.Fatalf("failed = %#v", failed.Request)
	}
	broken.mu.Lock()
	cancelled := broken.cancelled
	broken.mu.Unlock()
	if !cancelled {
		t.Fatal("a broken adapter must be cancelled")
	}
	if got := q.StartNext(); got.Type != Started {
		t.Fatalf("next start = %#v", got)
	}
	if calls := good.startedCalls(); len(calls) != 1 || calls[0] != [2]string{"good", "sonnet"} {
		t.Fatalf("good agent started %v", calls)
	}
}

func TestCancelActivePreservesPartialOutputAndLeavesPendingForAdvance(t *testing.T) {
	active := newFake()
	active.setOutput("partial")
	waiting := newFake()
	q, _ := queueWith(t, []*fakeAgent{active, waiting}, nil)
	q.Enqueue("active", entry("sonnet"))
	q.StartNext()
	q.Enqueue("waiting", entry("sonnet"))

	cancelled := q.CancelActive().Request
	if cancelled.Status != Cancelled || cancelled.Output != "partial" {
		t.Fatalf("cancelled = %#v", cancelled)
	}
	if q.PendingCount() != 1 {
		t.Fatalf("pending = %d", q.PendingCount())
	}
	if got := q.StartNext(); got.Type != Started {
		t.Fatalf("advance = %#v", got)
	}
	if calls := waiting.startedCalls(); len(calls) != 1 || calls[0] != [2]string{"waiting", "sonnet"} {
		t.Fatalf("waiting agent started %v", calls)
	}
}

func TestCancelPendingNeverTouchesActiveAdapter(t *testing.T) {
	active := newFake()
	q, _ := queueWith(t, []*fakeAgent{active, newFake(), newFake()}, nil)
	q.Enqueue("active", entry("sonnet"))
	q.StartNext()
	q.Enqueue("two", entry("sonnet"))
	q.Enqueue("three", entry("sonnet"))

	cancelled := q.CancelPending()
	if len(cancelled) != 2 || cancelled[0].ID != 2 || cancelled[1].ID != 3 {
		t.Fatalf("cancelled = %#v", cancelled)
	}
	for _, s := range cancelled {
		if s.Status != Cancelled {
			t.Fatalf("status = %q", s.Status)
		}
	}
	active.mu.Lock()
	running, wasCancelled := active.running, active.cancelled
	active.mu.Unlock()
	if !running || wasCancelled {
		t.Fatal("the live adapter must be untouched")
	}
	if !q.Active() || q.Pending() {
		t.Fatalf("active = %v, pending = %v", q.Active(), q.Pending())
	}
}

func TestHistoryLimitEvictsOnlyOldestFinishedRequests(t *testing.T) {
	agents := []*fakeAgent{newFake(), newFake(), newFake(), newFake()}
	q, _ := queueWith(t, agents, func(o *Options) { o.HistoryLimit = 2 })
	for index := 0; index < 4; index++ {
		q.Enqueue(fmt.Sprintf("request %d", index), entry("sonnet"))
		q.StartNext()
		q.Pump()
	}
	requests := q.Requests()
	if len(requests) != 2 || requests[0].ID != 3 || requests[1].ID != 4 {
		t.Fatalf("requests = %#v", requests)
	}
	latest, ok := q.LatestFinished()
	if !ok || latest.ID != 4 {
		t.Fatalf("latest = %#v", latest)
	}
}

func TestLiveSnapshotExposesStreamedOutputWithoutMutableInternals(t *testing.T) {
	a := newFake()
	q, _ := queueWith(t, []*fakeAgent{a}, nil)
	q.Enqueue("stream", entry("sonnet"))
	q.StartNext()
	a.setOutput("live")

	snapshot, ok := q.ActiveRequest()
	if !ok || snapshot.Output != "live" {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if got := q.ActiveOutput(); got != "live" {
		t.Fatalf("ActiveOutput = %q", got)
	}
}

// From test_app_agent_queue.rb: quitting cancels the running request and every
// waiting one, without advancing to another request.
func TestShutdownCancelsActiveAndPending(t *testing.T) {
	q, _ := queueWith(t, []*fakeAgent{newFake(), newFake()}, nil)
	q.Enqueue("active", entry("sonnet"))
	q.StartNext()
	q.Enqueue("waiting", entry("sonnet"))

	cancelled := q.Shutdown()
	if len(cancelled) != 2 {
		t.Fatalf("cancelled = %#v", cancelled)
	}
	if q.Work() {
		t.Fatal("shutdown left work behind")
	}
	for _, s := range statuses(q.Requests()) {
		if s != Cancelled {
			t.Fatalf("statuses = %v", statuses(q.Requests()))
		}
	}
}

func TestNewQueueRejectsInvalidLimits(t *testing.T) {
	factory := func(Entry) (Adapter, error) { return newFake(), nil }
	if _, err := NewQueue(Options{Factory: factory, MaxPending: -1}); err == nil {
		t.Fatal("negative max pending accepted")
	}
	if _, err := NewQueue(Options{Factory: factory, HistoryLimit: -1}); err == nil {
		t.Fatal("negative history limit accepted")
	}
	if _, err := NewQueue(Options{}); err == nil {
		t.Fatal("a queue with no factory was accepted")
	}
}

// FIFO is an interaction contract, so it must hold when submissions race.
// Run with -race.
func TestConcurrentEnqueuePreservesFIFOOrderAndIDs(t *testing.T) {
	const n = 50
	agents := make([]*fakeAgent, n)
	for i := range agents {
		agents[i] = newFake()
	}
	q, _ := queueWith(t, agents, nil)

	var wg sync.WaitGroup
	ids := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sub := q.Enqueue(fmt.Sprintf("prompt %d", i), entry("sonnet"))
			if !sub.Accepted() {
				t.Errorf("prompt %d rejected: %s", i, sub.Error)
				return
			}
			ids[i] = sub.Request.ID
		}(i)
	}
	wg.Wait()

	seen := map[int]bool{}
	for _, id := range ids {
		if id < 1 || id > n {
			t.Fatalf("id %d out of range", id)
		}
		if seen[id] {
			t.Fatalf("id %d handed out twice", id)
		}
		seen[id] = true
	}

	// Requests come back in submission order, and running them drains in that
	// same order.
	requests := q.Requests()
	if len(requests) != n {
		t.Fatalf("%d requests", len(requests))
	}
	for i, r := range requests {
		if r.ID != i+1 {
			t.Fatalf("request %d has id %d — FIFO order broken", i, r.ID)
		}
	}
	for i := 0; i < n; i++ {
		event := q.StartNext()
		if event.Type != Started || event.Request.ID != i+1 {
			t.Fatalf("start %d = %#v", i, event)
		}
		if event.Request.Prompt != requests[i].Prompt {
			t.Fatalf("start %d ran %q, want %q", i, event.Request.Prompt, requests[i].Prompt)
		}
		q.Pump()
	}
}

// Readers must not tear while a writer advances the queue. Run with -race.
func TestConcurrentReadersDoNotRaceWithQueueAdvance(t *testing.T) {
	agents := make([]*fakeAgent, 20)
	for i := range agents {
		agents[i] = newFake()
	}
	q, _ := queueWith(t, agents, nil)
	for i := 0; i < 20; i++ {
		q.Enqueue(fmt.Sprintf("p%d", i), entry("sonnet"))
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = q.Requests()
				_, _ = q.ActiveRequest()
				_ = q.ActiveOutput()
				_ = q.PendingCount()
				_ = q.Work()
				_, _ = q.LatestFinished()
			}
		}()
	}
	for i := 0; i < 20; i++ {
		q.StartNext()
		q.Pump()
	}
	close(stop)
	wg.Wait()

	if q.Work() {
		t.Fatal("queue still reports work")
	}
}
