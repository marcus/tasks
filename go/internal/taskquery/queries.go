package taskquery

import (
	"encoding/json"
	"sort"
	"time"

	"tasks-go/internal/lead"
	"tasks-go/internal/links"
	"tasks-go/internal/query"
	"tasks-go/internal/record"
	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
)

// DeferTag is the semantic tag that means someday/maybe. It rides ALONGSIDE
// the task's real state rather than replacing it, the same way important and
// urgent do, so a deferred NEXT action is still a NEXT action when it comes
// back.
const DeferTag = "defer"

// The lifecycle vocabularies, spelled where lib/tasks/check.rb spells them.
var (
	proposedStates = []string{"PROPOSED"}
	openStates     = []string{"INBOX", "TODO", "NEXT", "WAITING"}
	closedStates   = []string{"DONE", "CANCELLED"}
	stateOrder     = append(append(append([]string{}, proposedStates...), openStates...), closedStates...)
)

// StateOrder is the order the grouped human views print states in.
func StateOrder() []string { return append([]string{}, stateOrder...) }

func isOpen(state string) bool     { return contains(openStates, state) }
func isClosed(state string) bool   { return contains(closedStates, state) }
func isProposed(state string) bool { return contains(proposedStates, state) }

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// Queries is the read model over one snapshot for one reader.
type Queries struct {
	snapshot *store.Snapshot
	context  temporal.Context
	tree     Tree
	records  map[store.Source]map[int]record.Record
	cache    map[availabilityKey]Availability

	// Link extraction is config-driven: the shorthand templates and custom
	// system hosts a user configured. They ride on the read model rather than on
	// the store so a caller holding a snapshot can answer "what does this task
	// point at" without a second configuration path.
	linkShorthands map[string]string
	linkSystems    map[string]string

	// Project rollup caches. The section list and the Projects root are scanned
	// once per reader: `projects` builds a view per section, and each view walks
	// a subtree, so re-deriving these per view would be quadratic in file size.
	sections           []record.Record
	projectsRootRecord record.Record
	projectsRootFound  bool
	projectsRootReady  bool
}

// Option configures a read model at construction. The variadic form is
// deliberate: a reader that does not care about link configuration should not
// have to name it.
type Option func(*Queries)

// WithLinkConfig supplies the configured shorthand templates (`link.<name>`)
// and self-hosted system hosts (`system.<name>`).
func WithLinkConfig(shorthands, systems map[string]string) Option {
	return func(q *Queries) {
		q.linkShorthands = shorthands
		q.linkSystems = systems
	}
}

type availabilityKey struct {
	source store.Source
	line   int
	id     string
	title  string
}

// New builds the read model. The tree indexes the LIVE file only; archive items
// have no structural context by construction, which is why an archived item's
// availability is `closed` without a walk.
func New(snapshot *store.Snapshot, context temporal.Context, options ...Option) *Queries {
	queries := &Queries{
		snapshot: snapshot,
		context:  context,
		tree:     BuildTree(snapshot.LiveRecords, snapshot.Items),
		records:  map[store.Source]map[int]record.Record{},
		cache:    map[availabilityKey]Availability{},
	}
	for _, group := range []struct {
		source  store.Source
		records []record.Record
	}{{store.SourceLive, snapshot.LiveRecords}, {store.SourceArchive, snapshot.ArchiveRecords}} {
		byLine := map[int]record.Record{}
		for _, parsed := range group.records {
			byLine[parsed.Line] = parsed
		}
		queries.records[group.source] = byLine
	}
	for _, option := range options {
		option(queries)
	}
	return queries
}

// Links is every web link the task points at: its title first, then its own
// body lines, extracted and classified once so `show`, `links` and `open` can
// never disagree about which URL is the task's first.
func (q *Queries) Links(item store.Item) []links.Link {
	text := append([]string{item.Title}, q.Body(item)...)
	return links.Extract(text, q.linkShorthands, q.linkSystems)
}

// Context is the reader this model answers for.
func (q *Queries) Context() temporal.Context { return q.context }

// Today is the reader's own calendar day, which every date-sensitive read is
// measured against.
func (q *Queries) Today() temporal.Date { return q.context.LocalDate() }

// Tree is the structural index over the live file.
func (q *Queries) Tree() Tree { return q.tree }

// NodeFor is the tree node an item sits at, or nil for an archive item.
func (q *Queries) NodeFor(item store.Item) *Node {
	if item.Source == store.SourceArchive {
		return nil
	}
	node := q.tree.NodesByLine[item.Line]
	if node != nil && node.Item != nil && node.Item.Line == item.Line {
		return node
	}
	// Lines can shift underneath a held item; an item that carries an id falls
	// back to finding its node by id rather than landing on whatever record now
	// occupies that line.
	if item.HasID {
		for _, candidate := range q.tree.NodesByLine {
			if candidate.Item != nil && candidate.Item.ID == item.ID {
				return candidate
			}
		}
	}
	return node
}

// Record is the raw record an item came from.
func (q *Queries) Record(item store.Item) (record.Record, bool) {
	parsed, found := q.records[item.Source][item.Line]
	return parsed, found
}

// Body is the item's own body lines. It never includes a child's body —
// children are separate records.
func (q *Queries) Body(item store.Item) []string {
	parsed, found := q.Record(item)
	if !found {
		return nil
	}
	text := ""
	for _, field := range parsed.Fields {
		if field.Key == "body" {
			_ = unmarshalString(field.Value, &text)
		}
	}
	if text == "" {
		return nil
	}
	return splitLines(text)
}

// ScheduledValue and DeadlineValue are the stored date plus whatever wall time
// and zone qualify it. They are built from the RECORD rather than the item so
// the time object travels with the date it belongs to.
func (q *Queries) ScheduledValue(item store.Item) (temporal.Value, bool) {
	return q.temporalValue(item, "scheduled")
}

func (q *Queries) DeadlineValue(item store.Item) (temporal.Value, bool) {
	return q.temporalValue(item, "deadline")
}

// temporalValue is TemporalValue.from_record with validate: false. The fallback
// matters: a value whose TIME half is unusable still has a usable DATE, and a
// reader that dropped the whole stamp would hide a dated task entirely rather
// than render it a little less precisely.
func (q *Queries) temporalValue(item store.Item, field string) (temporal.Value, bool) {
	stored := item.Scheduled
	timeRaw := item.ScheduledTime
	if field == "deadline" {
		stored, timeRaw = item.Deadline, item.DeadlineTime
	}
	date, ok := temporal.ParseDate(stored)
	if !ok {
		return temporal.Value{}, false
	}
	local, zone, fold := decodeTimeObject(timeRaw)
	value, err := temporal.NewValue(date, local, zone, fold, false)
	if err != nil {
		return temporal.Value{Date: date}, true
	}
	return value, true
}

func decodeTimeObject(raw json.RawMessage) (string, string, int) {
	if len(raw) == 0 {
		return "", "", 0
	}
	fields, err := record.Fields(raw)
	if err != nil {
		return "", "", 0
	}
	local, zone, fold := "", "", 0
	for _, field := range fields {
		switch field.Key {
		case "local":
			_ = unmarshalString(field.Value, &local)
		case "timezone":
			_ = unmarshalString(field.Value, &zone)
		case "fold":
			var value int
			if json.Unmarshal(field.Value, &value) == nil {
				fold = value
			}
		}
	}
	return local, zone, fold
}

// Deferred reports the someday/maybe hold.
func (q *Queries) Deferred(item store.Item) bool {
	return contains(item.Tags, DeferTag)
}

// Recurring reports a VALID repeater cookie. A cookie that does not match the
// grammar reads as non-recurring so completion closes the task normally —
// Check still reports the bad cookie.
func Recurring(item store.Item) bool { return item.Recur != "" && recurCookie(item.Recur) }

// Availability is the effective answer for one item: why it is or is not
// workable now, and what would change that.
type Availability struct {
	Reason      string
	BlockerID   string
	Scheduled   temporal.Date
	Value       *temporal.Value
	AvailableAt time.Time
}

// The reasons, in the order lib/tasks/task_queries.rb declares them.
const (
	ReasonAvailable         = "available"
	ReasonScheduled         = "scheduled"
	ReasonOnHold            = "on_hold"
	ReasonAncestorScheduled = "ancestor_scheduled"
	ReasonAncestorOnHold    = "ancestor_on_hold"
	ReasonProposed          = "proposed"
	ReasonClosed            = "closed"
)

// Available reports whether the item can be worked on now.
func (a Availability) Available() bool { return a.Reason == ReasonAvailable }

// AvailabilityFor is the effective availability of an item INCLUDING every task
// ancestor. Closed ancestors stay transparent to lifecycle hoisting, but their
// own timed or indefinite blocker still participates in this walk — a hold on a
// finished parent is still a hold the subtree inherited.
func (q *Queries) AvailabilityFor(item store.Item) Availability {
	key := availabilityKey{source: item.Source, line: item.Line, id: item.ID, title: item.Title}
	if !item.HasID {
		key.id = ""
	}
	if cached, found := q.cache[key]; found {
		return cached
	}
	computed := q.buildAvailability(item)
	q.cache[key] = computed
	return computed
}

type candidate struct {
	item     store.Item
	distance int
}

func (q *Queries) buildAvailability(item store.Item) Availability {
	if item.Source == store.SourceArchive || isClosed(item.State) {
		return Availability{Reason: ReasonClosed}
	}
	if !isOpen(item.State) {
		return Availability{Reason: ReasonProposed}
	}

	candidates := []candidate{{item: item, distance: 0}}
	node := q.NodeFor(item)
	var current *Node
	if node != nil {
		current = node.Parent
	}
	distance := 1
	for current != nil {
		if current.Task() && current.Item != nil {
			candidates = append(candidates, candidate{item: *current.Item, distance: distance})
			distance++
		}
		current = current.Parent
	}

	// An indefinite hold outranks every timed gate: there is no date at which
	// it releases, so reporting the date one would have released on would be a
	// promise nothing keeps.
	for _, entry := range candidates {
		if !q.Deferred(entry.item) {
			continue
		}
		reason := ReasonOnHold
		if entry.distance != 0 {
			reason = ReasonAncestorOnHold
		}
		return Availability{Reason: reason, BlockerID: entry.item.ID}
	}

	type gate struct {
		item     store.Item
		distance int
		instant  time.Time
		value    temporal.Value
	}
	gates := []gate{}
	for _, entry := range candidates {
		instant, value, ok := q.effectiveGate(entry.item)
		if !ok {
			continue
		}
		gates = append(gates, gate{item: entry.item, distance: entry.distance, instant: instant, value: value})
	}
	// The LATEST future gate wins, because every one of them has to pass before
	// the work can start. Ties break toward the nearest blocker, which is the
	// one a reader can act on.
	var latest *gate
	for index := range gates {
		if !gates[index].instant.After(q.context.Now) {
			continue
		}
		if latest == nil || gates[index].instant.After(latest.instant) ||
			(gates[index].instant.Equal(latest.instant) && gates[index].distance < latest.distance) {
			latest = &gates[index]
		}
	}
	if latest != nil {
		reason := ReasonScheduled
		if latest.distance != 0 {
			reason = ReasonAncestorScheduled
		}
		value := latest.value
		return Availability{
			Reason: reason, BlockerID: latest.item.ID, Scheduled: value.Date,
			Value: &value, AvailableAt: latest.instant.UTC(),
		}
	}
	return Availability{Reason: ReasonAvailable}
}

// effectiveGate is the ONE derivation of a candidate's own timed gate, for the
// task itself and for every ancestor alike. It returns an INSTANT rather than a
// date on purpose: an all-day gate releases at local midnight, and a clock lead
// releases at an instant no date can express.
//
// A lead REPLACES the available-from gate rather than joining it: the lead is
// measured from the anchor, and the store refuses the shapes that would leave a
// second, separately-meaningful available-from date behind.
func (q *Queries) effectiveGate(item store.Item) (time.Time, temporal.Value, bool) {
	gateInstant, gateValue, state := q.leadGate(item)
	switch state {
	case gateSkipped:
		// A released occurrence has NO own timed gate — not even its anchor's
		// available-from date, which would otherwise re-hide what `activate`
		// just released.
		return time.Time{}, temporal.Value{}, false
	case gatePresent:
		return gateInstant, gateValue, true
	}
	scheduled, ok := q.ScheduledValue(item)
	if !ok {
		return time.Time{}, temporal.Value{}, false
	}
	instant, err := scheduled.ReleaseInstant(q.context)
	if err != nil {
		return time.Time{}, temporal.Value{}, false
	}
	return instant, scheduled, true
}

type gateState int

const (
	gateAbsent gateState = iota
	gatePresent
	gateSkipped
)

// leadGate is a lead's derived gate, SKIPPED when `activate` already released
// this occurrence, or absent when there is no lead gate to derive — no anchor
// to measure from, or no valid span. Absent means "fall back to the
// available-from date"; skipped means "no own timed gate at all".
func (q *Queries) leadGate(item store.Item) (time.Time, temporal.Value, gateState) {
	return q.leadGateWith(item, item.LeadSkip)
}

// leadGateWith is the same derivation with the release stamp supplied, so a
// caller can ask the second question the surfaces need: not "is this task
// hidden right now" but "where does its window sit for this anchor at all".
// `show` asks the second one — it prints the span beside the date it produces
// even for an occurrence `activate` has already released, because the span is
// what the record says and the reader is looking at the record.
func (q *Queries) leadGateWith(item store.Item, released string) (time.Time, temporal.Value, gateState) {
	scheduled, hasScheduled := q.ScheduledValue(item)
	deadline, hasDeadline := q.DeadlineValue(item)

	anchorValue := temporal.Value{}
	hasAnchorValue := false
	if hasDeadline {
		anchorValue, hasAnchorValue = deadline, true
	} else if hasScheduled {
		anchorValue, hasAnchorValue = scheduled, true
	}

	deadlineDate := temporal.Date{}
	if hasDeadline {
		deadlineDate = deadline.Date
	}
	scheduledDate := temporal.Date{}
	if hasScheduled {
		scheduledDate = scheduled.Date
	}
	anchor, hasAnchor := lead.AnchorDate(deadlineDate, scheduledDate)
	if !hasAnchor {
		return time.Time{}, temporal.Value{}, gateAbsent
	}
	// Both halves matter: a stamp only ever releases a LEAD window, so a stray
	// `lead_skip` on a task with no lead can never erase that task's ordinary
	// available-from gate.
	if !lead.Span(item.Lead) {
		return time.Time{}, temporal.Value{}, gateAbsent
	}
	if released == anchor.ISO() {
		return time.Time{}, temporal.Value{}, gateSkipped
	}

	if lead.Clock(item.Lead) {
		if !hasAnchorValue {
			anchorValue = temporal.Value{Date: anchor}
		}
		instant, ok := lead.GateInstant(anchorValue, item.Lead, q.context)
		if !ok {
			return time.Time{}, temporal.Value{}, gateAbsent
		}
		return instant, q.clockGateDisplay(instant), gatePresent
	}

	date, ok := lead.GateDate(anchor, item.Lead)
	if !ok {
		return time.Time{}, temporal.Value{}, gateAbsent
	}
	// The gate is a DATE, released at start of day in the ANCHOR's own
	// effective zone: a zoned anchor fixes when the work is due, and its window
	// opens at midnight where that anchor lives. The display value stays
	// date-only, so the instant is computed here rather than re-derived from it.
	zone := q.context.Timezone
	if hasAnchorValue {
		zone = anchorValue.EffectiveZone(q.context)
	}
	instant, err := earliestOn(date, zone)
	if err != nil {
		return time.Time{}, temporal.Value{}, gateAbsent
	}
	return instant, temporal.Value{Date: date}, gatePresent
}

// clockGateDisplay is a DISPLAY value for a clock gate: the raw instant
// projected into the reader's zone. The gate itself stays the raw instant —
// this value is only ever rendered, never compared, so a DST fall-back that
// makes one local time mean two instants cannot move the window.
func (q *Queries) clockGateDisplay(instant time.Time) temporal.Value {
	local := instant.In(q.context.Timezone)
	return temporal.Value{
		Date:      temporal.DateOf(local),
		LocalTime: formatClock(local.Hour(), local.Minute()),
		Timezone:  q.context.TimezoneID,
	}
}

// List selects the items a filter names, in file order.
func (q *Queries) List(filter query.Filter) []store.Item {
	items := []store.Item{}
	for _, item := range q.sourceItems(filter) {
		if q.matches(item, filter) {
			items = append(items, item)
		}
	}
	if filter.AgentReadyOnly() {
		items = q.rankAgentReady(items)
	}
	return items
}

func (q *Queries) sourceItems(filter query.Filter) []store.Item {
	switch filter.Scope() {
	case query.ScopeArchived:
		return q.snapshot.ArchiveItems
	case query.ScopeAll:
		return append(append([]store.Item{}, q.snapshot.Items...), q.snapshot.ArchiveItems...)
	default:
		return q.snapshot.Items
	}
}

func (q *Queries) matches(item store.Item, filter query.Filter) bool {
	if !contains(filter.States(), item.State) {
		return false
	}
	if filter.Scope() == query.ScopeDone && item.Source != store.SourceLive {
		return false
	}
	if !q.deferredMatch(item, filter) {
		return false
	}
	if filter.RecurringOnly() && !Recurring(item) {
		return false
	}
	if filter.Priority() != "" && item.Priority != filter.Priority() {
		return false
	}
	for _, context := range filter.Contexts() {
		if !contains(allTags(item), context) {
			return false
		}
	}
	for _, tag := range filter.Tags() {
		if !contains(allTags(item), tag) {
			return false
		}
	}
	if !q.delegationMatch(item, filter) {
		return false
	}
	return q.textMatch(item, filter)
}

// allTags is the item's tag list as the record stored it: contexts and ordinary
// tags together. The snapshot splits them for rendering; a filter matches
// against the whole list, because `@home` and `important` are the same field.
func allTags(item store.Item) []string {
	return append(append([]string{}, item.Contexts...), item.Tags...)
}

// delegationMatch: `--delegated` is any marker at all — human or agent, ready
// or claimed — so the owner sees every handed-off task in one list.
// `--agent-ready` is the narrower claimable queue: agent kind, unclaimed,
// accepted live state, and actually workable right now.
func (q *Queries) delegationMatch(item store.Item, filter query.Filter) bool {
	if !filter.DelegatedOnly() && !filter.AgentReadyOnly() {
		return true
	}
	value := decodeDelegation(item.Delegation)
	if filter.DelegatedOnly() {
		return record.DelegationObject(value)
	}
	return record.DelegationReady(value) && item.Source == store.SourceLive &&
		isOpen(item.State) && q.AvailabilityFor(item).Available()
}

func decodeDelegation(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

func (q *Queries) deferredMatch(item store.Item, filter query.Filter) bool {
	if filter.SomedayOnly() {
		if !q.Deferred(item) {
			return false
		}
		if filter.UnavailableOnly() {
			return !q.AvailabilityFor(item).Available()
		}
		return true
	}
	if filter.UnavailableOnly() {
		return !q.AvailabilityFor(item).Available()
	}
	if filter.DeferredOnly() {
		if filter.Scope() == query.ScopeOpen {
			return !q.AvailabilityFor(item).Available()
		}
		return q.Deferred(item)
	}
	if filter.Scope() == query.ScopeOpen {
		return q.AvailabilityFor(item).Available()
	}
	return true
}

func (q *Queries) textMatch(item store.Item, filter query.Filter) bool {
	needle := filter.TextQuery()
	if needle == "" {
		return true
	}
	if containsFold(item.Title, needle) {
		return true
	}
	if !filter.BodySearch() {
		return false
	}
	return containsFold(joinLines(q.Body(item)), needle)
}

// rankAgentReady is the one list whose order is a contract: a heartbeat agent
// takes the first row it is capable of, so the ranking cannot live in whichever
// adapter happens to print it. Existing priority, then the soonest
// deadline-or-scheduled boundary, then canonical file order.
func (q *Queries) rankAgentReady(items []store.Item) []store.Item {
	ranked := append([]store.Item{}, items...)
	sort.SliceStable(ranked, func(left, right int) bool {
		leftPriority, rightPriority := priorityKey(ranked[left]), priorityKey(ranked[right])
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return q.agendaSortKey(ranked[left]).Before(q.agendaSortKey(ranked[right]))
	})
	return ranked
}

// priorityKey is Ruby's `item.priority || "Z"`: unprioritized sorts after C.
func priorityKey(item store.Item) string {
	if item.Priority == "" {
		return "Z"
	}
	return item.Priority
}

// agendaSortKey is the INSTANT a dated item comes due, which is what "soonest
// first" has to mean once a stamp can carry a wall time and a zone. A deadline
// sorts by the moment it stops being on time; an available-from date sorts by
// the moment it opens. An item with neither sorts last.
func (q *Queries) agendaSortKey(item store.Item) time.Time {
	if deadline, ok := q.DeadlineValue(item); ok {
		if boundary, err := q.dueBoundary(deadline); err == nil {
			return boundary
		}
	}
	if scheduled, ok := q.ScheduledValue(item); ok {
		if instant, err := scheduled.ReleaseInstant(q.context); err == nil {
			return instant
		}
	}
	return time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)
}

// dueBoundary is when a deadline stops being on time. A date-only deadline is
// on time all day, so its boundary is the first instant of the NEXT day; a
// timed one is on time until its own instant, exactly.
func (q *Queries) dueBoundary(value temporal.Value) (time.Time, error) {
	if !value.AllDay() {
		return value.Instant(q.context)
	}
	return earliestOn(value.Date.AddDays(1), q.context.Timezone)
}

// AgendaItems is the `agenda` view: every OPEN, AVAILABLE item carrying a date,
// soonest first, priority breaking ties, file order breaking those.
func (q *Queries) AgendaItems() []store.Item {
	selected := []store.Item{}
	for _, item := range q.snapshot.Items {
		if !isOpen(item.State) {
			continue
		}
		if !q.AvailabilityFor(item).Available() {
			continue
		}
		if item.Deadline == "" && item.Scheduled == "" {
			continue
		}
		selected = append(selected, item)
	}
	sort.SliceStable(selected, func(left, right int) bool {
		leftKey, rightKey := q.agendaSortKey(selected[left]), q.agendaSortKey(selected[right])
		if !leftKey.Equal(rightKey) {
			return leftKey.Before(rightKey)
		}
		return priorityKey(selected[left]) < priorityKey(selected[right])
	})
	return selected
}
