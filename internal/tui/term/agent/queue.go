// Package agent is the serial coordinator for autonomous TUI agent requests
// and the presentation of their transcripts. Each request owns the
// provider/model selected when it was submitted, but at most one adapter is
// ever running. The queue records transcripts and outcomes for presentation; it
// never reads or writes task data itself.
//
// Go port of Ruby's lib/tui/agent_queue.rb and lib/tui/agent_activity.rb.
// Unlike Ruby's single-threaded original, every operation takes a mutex, so the
// FIFO order Marcus relies on holds even when submissions arrive from a
// background reader.
package agent

import (
	"fmt"
	"sync"
	"time"
)

const (
	// MaxPending is how many requests may wait behind the running one.
	MaxPending = 100
	// HistoryLimit is how many finished requests are retained.
	HistoryLimit = 50
)

// Status is a request's lifecycle state.
type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
)

// Finished reports whether a status is terminal.
func (s Status) Finished() bool {
	return s == Succeeded || s == Failed || s == Cancelled
}

// Entry names the provider/model a request was submitted with. The LLM layer
// supplies the real implementation; the queue only reads it.
type Entry interface {
	Provider() string
	Model() string
	// UILabel is the short "provider:model" form the activity view shows.
	UILabel() string
}

// SimpleEntry is a provider/model pair with no label mapping — enough for
// callers and tests that do not need the LLM registry's display names.
type SimpleEntry struct {
	ProviderName string
	ModelName    string
	Label        string
}

func (e SimpleEntry) Provider() string { return e.ProviderName }
func (e SimpleEntry) Model() string    { return e.ModelName }
func (e SimpleEntry) UILabel() string {
	if e.Label != "" {
		return e.Label
	}
	return e.ProviderName + ":" + e.ModelName
}

// ProcessStatus describes how an adapter's process ended.
type ProcessStatus struct {
	// Present is false when the adapter never produced a process status.
	Present    bool
	Exited     bool
	ExitStatus int
	Signaled   bool
	Signal     int
}

// Adapter is one live agent process. Only one is ever running per queue.
type Adapter interface {
	// Available re-checks the provider at start time.
	Available() bool
	Start(prompt, model string) error
	// Pump drains one readable chunk; done reports that the process finished.
	Pump() (done bool, err error)
	Cancel() error
	Output() string
	Success() bool
	// ExitStatus reports the process exit code, if one is known.
	ExitStatus() (int, bool)
	ProcessStatus() ProcessStatus
}

// Snapshot is an immutable view of one request.
type Snapshot struct {
	ID         int
	Prompt     string
	Entry      Entry
	Status     Status
	QueuedAt   float64
	StartedAt  *float64
	FinishedAt *float64
	Output     string
	ExitStatus *int
	Error      string
}

// Finished reports whether the request reached a terminal status.
func (s Snapshot) Finished() bool { return s.Status.Finished() }

// Elapsed is how long the request has been (or was) running, in seconds.
func (s Snapshot) Elapsed(now float64) float64 {
	if s.StartedAt == nil {
		return 0
	}
	end := now
	if s.FinishedAt != nil {
		end = *s.FinishedAt
	}
	if d := end - *s.StartedAt; d > 0 {
		return d
	}
	return 0
}

// Submission is the outcome of an enqueue attempt.
type Submission struct {
	Request *Snapshot
	Error   string
}

// Accepted reports whether the request joined the queue.
func (s Submission) Accepted() bool { return s.Request != nil }

// EventType distinguishes a start from a completion.
type EventType string

const (
	Started  EventType = "started"
	Finished EventType = "finished"
)

// Event reports a queue transition. Type is empty when nothing happened.
type Event struct {
	Type    EventType
	Request Snapshot
}

// Occurred reports whether the call produced a transition.
func (e Event) Occurred() bool { return e.Type != "" }

type item struct {
	id         int
	prompt     string
	entry      Entry
	status     Status
	queuedAt   float64
	startedAt  *float64
	finishedAt *float64
	output     string
	exitStatus *int
	err        string
}

func (i *item) finished() bool { return i.status.Finished() }

// Options configures a queue.
type Options struct {
	// Factory builds a fresh adapter for an entry, with the current system
	// context, only when a request actually starts.
	Factory func(Entry) (Adapter, error)
	// Availability is a lightweight probe run at enqueue so an unavailable
	// provider is rejected immediately without building the (context-bearing,
	// possibly failing) adapter. A nil probe means "assume available";
	// StartNext's re-check is the backstop either way.
	Availability func(Entry) bool
	// Clock returns monotonic seconds. Defaults to process-monotonic time.
	Clock        func() float64
	MaxPending   int
	HistoryLimit int
}

// Queue is the serial request coordinator.
type Queue struct {
	mu           sync.Mutex
	factory      func(Entry) (Adapter, error)
	availability func(Entry) bool
	clock        func() float64
	maxPending   int
	historyLimit int

	items   []*item
	pending []*item
	active  *item
	adapter Adapter
	nextID  int
}

var processStart = time.Now()

// NewQueue builds a queue. Factory must be non-nil.
func NewQueue(opts Options) (*Queue, error) {
	maxPending := opts.MaxPending
	if maxPending == 0 {
		maxPending = MaxPending
	}
	historyLimit := opts.HistoryLimit
	if historyLimit == 0 {
		historyLimit = HistoryLimit
	}
	if maxPending <= 0 {
		return nil, fmt.Errorf("max_pending must be positive")
	}
	if historyLimit <= 0 {
		return nil, fmt.Errorf("history_limit must be positive")
	}
	if opts.Factory == nil {
		return nil, fmt.Errorf("agent factory is required")
	}
	clock := opts.Clock
	if clock == nil {
		clock = func() float64 { return time.Since(processStart).Seconds() }
	}
	return &Queue{
		factory: opts.Factory, availability: opts.Availability, clock: clock,
		maxPending: maxPending, historyLimit: historyLimit, nextID: 1,
	}, nil
}

// Enqueue submits a prompt. A full queue or an unavailable provider is a
// rejection, and no request is recorded.
func (q *Queue) Enqueue(prompt string, entry Entry) Submission {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.pending) >= q.maxPending {
		return Submission{Error: fmt.Sprintf("agent queue is full (%d waiting)", q.maxPending)}
	}
	if q.availability != nil && !q.availability(entry) {
		return Submission{Error: fmt.Sprintf(
			"%s not available — check the CLI is installed and any local model server is running",
			entry.Provider())}
	}

	it := &item{
		id: q.nextID, prompt: prompt, entry: entry,
		status: Queued, queuedAt: q.clock(),
	}
	q.nextID++
	q.items = append(q.items, it)
	q.pending = append(q.pending, it)
	snap := q.snapshot(it)
	return Submission{Request: &snap}
}

// StartNext attempts exactly one queued request. A start-time
// availability/spawn failure is a finished event so the caller can report it
// and call again; a successful start returns Started and owns the single live
// adapter.
func (q *Queue) StartNext() Event {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.active != nil || len(q.pending) == 0 {
		return Event{}
	}
	it := q.pending[0]
	q.pending = q.pending[1:]
	now := q.clock()
	it.startedAt = &now

	// Build the real adapter now, with context as it stands at start time — a
	// factory error is reported as a failed request, never a crash.
	adapter, err := q.factory(it.entry)
	if err != nil {
		q.active = nil
		q.adapter = nil
		q.finishItem(it, Failed, "", nil, "could not start agent: "+err.Error())
		return Event{Type: Finished, Request: q.snapshot(it)}
	}
	if !adapter.Available() {
		q.finishItem(it, Failed, "", nil, it.entry.Provider()+" became unavailable before start")
		return Event{Type: Finished, Request: q.snapshot(it)}
	}

	it.status = Running
	q.active = it
	q.adapter = adapter
	if err := adapter.Start(it.prompt, it.entry.Model()); err != nil {
		q.active = nil
		q.adapter = nil
		q.finishItem(it, Failed, "", nil, "could not start agent: "+err.Error())
		return Event{Type: Finished, Request: q.snapshot(it)}
	}
	return Event{Type: Started, Request: q.snapshot(it)}
}

// Pump drains one readable chunk. A finished adapter is recorded, but the next
// request is deliberately not started here: the application first reloads task
// state, then advances the queue, to preserve a visible checkpoint between runs.
func (q *Queue) Pump() Event {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.active == nil {
		return Event{}
	}
	it := q.active
	adapter := q.adapter
	done, err := adapter.Pump()
	if err != nil {
		cancelErr := adapter.Cancel()
		message := "agent stream failed: " + err.Error()
		if cancelErr != nil {
			message += " (cleanup also failed: " + cancelErr.Error() + ")"
		}
		q.finishItem(it, Failed, adapter.Output(), exitPtr(adapter), message)
		q.clearActive()
		return Event{Type: Finished, Request: q.snapshot(it)}
	}
	if !done {
		return Event{}
	}

	status := Succeeded
	errText := ""
	if !adapter.Success() {
		status = Failed
		errText = processError(adapter)
	}
	q.finishItem(it, status, adapter.Output(), exitPtr(adapter), errText)
	q.clearActive()
	return Event{Type: Finished, Request: q.snapshot(it)}
}

// CancelActive stops the running request, preserving its partial output.
func (q *Queue) CancelActive() Event {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.active == nil {
		return Event{}
	}
	it := q.active
	adapter := q.adapter
	_ = adapter.Cancel()
	q.finishItem(it, Cancelled, adapter.Output(), exitPtr(adapter), "cancelled")
	q.clearActive()
	return Event{Type: Finished, Request: q.snapshot(it)}
}

// CancelPending discards everything waiting without touching the live adapter.
func (q *Queue) CancelPending() []Snapshot {
	q.mu.Lock()
	defer q.mu.Unlock()

	cancelled := make([]Snapshot, 0, len(q.pending))
	for _, it := range q.pending {
		q.finishItem(it, Cancelled, "", nil, "cancelled before start")
		cancelled = append(cancelled, q.snapshot(it))
	}
	q.pending = nil
	return cancelled
}

// Shutdown is the exit path: stop everything without advancing to another
// request.
func (q *Queue) Shutdown() []Snapshot {
	active := q.CancelActive()
	cancelled := q.CancelPending()
	var out []Snapshot
	if active.Occurred() {
		out = append(out, active.Request)
	}
	return append(out, cancelled...)
}

func (q *Queue) Active() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.active != nil
}

func (q *Queue) Pending() bool { return q.PendingCount() > 0 }

func (q *Queue) PendingCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

// SubmittedCount is how many requests have ever been accepted.
func (q *Queue) SubmittedCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.nextID - 1
}

// Any reports whether any request is retained.
func (q *Queue) Any() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) > 0
}

// Work reports whether anything is running or waiting.
func (q *Queue) Work() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.active != nil || len(q.pending) > 0
}

// Now samples the queue's own monotonic clock. Activity rendering must use the
// same clock domain as queuedAt/startedAt.
func (q *Queue) Now() float64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.clock()
}

// ActiveOutput is the live transcript of the running request.
func (q *Queue) ActiveOutput() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.adapter == nil {
		return ""
	}
	return q.adapter.Output()
}

// ActiveRequest snapshots the running request, if any.
func (q *Queue) ActiveRequest() (Snapshot, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.active == nil {
		return Snapshot{}, false
	}
	return q.snapshot(q.active), true
}

// Requests snapshots every retained request, oldest first.
func (q *Queue) Requests() []Snapshot {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Snapshot, 0, len(q.items))
	for _, it := range q.items {
		out = append(out, q.snapshot(it))
	}
	return out
}

// LatestFinished is the most recently completed request.
func (q *Queue) LatestFinished() (Snapshot, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := len(q.items) - 1; i >= 0; i-- {
		if q.items[i].finished() {
			return q.snapshot(q.items[i]), true
		}
	}
	return Snapshot{}, false
}

func (q *Queue) finishItem(it *item, status Status, output string, exit *int, errText string) {
	it.status = status
	now := q.clock()
	it.finishedAt = &now
	it.output = output
	it.exitStatus = exit
	it.err = errText
	q.trimHistory()
}

func (q *Queue) clearActive() {
	q.active = nil
	q.adapter = nil
}

// trimHistory evicts only the oldest finished requests.
func (q *Queue) trimHistory() {
	finished := 0
	for _, it := range q.items {
		if it.finished() {
			finished++
		}
	}
	excess := finished - q.historyLimit
	if excess <= 0 {
		return
	}
	kept := q.items[:0]
	for _, it := range q.items {
		if excess > 0 && it.finished() {
			excess--
			continue
		}
		kept = append(kept, it)
	}
	q.items = kept
}

func (q *Queue) snapshot(it *item) Snapshot {
	output := it.output
	if it == q.active && q.adapter != nil {
		output = q.adapter.Output()
	}
	var exit *int
	if it.exitStatus != nil {
		v := *it.exitStatus
		exit = &v
	}
	var started, finished *float64
	if it.startedAt != nil {
		v := *it.startedAt
		started = &v
	}
	if it.finishedAt != nil {
		v := *it.finishedAt
		finished = &v
	}
	return Snapshot{
		ID: it.id, Prompt: it.prompt, Entry: it.entry, Status: it.status,
		QueuedAt: it.queuedAt, StartedAt: started, FinishedAt: finished,
		Output: output, ExitStatus: exit, Error: it.err,
	}
}

func exitPtr(a Adapter) *int {
	if code, ok := a.ExitStatus(); ok {
		return &code
	}
	return nil
}

func processError(a Adapter) string {
	status := a.ProcessStatus()
	switch {
	case !status.Present:
		return "agent exited without a process status"
	case status.Exited:
		return fmt.Sprintf("agent exited %d", status.ExitStatus)
	case status.Signaled:
		return fmt.Sprintf("agent terminated by signal %d", status.Signal)
	default:
		return "agent did not exit cleanly"
	}
}
