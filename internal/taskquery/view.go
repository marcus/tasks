package taskquery

import (
	"strings"
	"time"

	"github.com/marcus/tasks/internal/store"
	"github.com/marcus/tasks/internal/temporal"
)

// Headline is the item's summary rendered from its own fields, star-less:
// state, optional priority cookie, title, trailing tag cluster in STORED order.
// One derivation, so the string can never fork between a read command and the
// report a mutation prints.
func Headline(item store.Item) string {
	headline := item.State + " "
	if item.Priority != "" {
		headline += "[#" + item.Priority + "] "
	}
	headline += item.Title
	if len(item.AllTags) > 0 {
		headline += " :" + strings.Join(item.AllTags, ":") + ":"
	}
	return headline
}

// FindLive is the live item with this id, if any. Availability names a blocker
// by id; a renderer that wants its title has to look it up.
func (q *Queries) FindLive(id string) (store.Item, bool) {
	if id == "" {
		return store.Item{}, false
	}
	for _, item := range q.snapshot.Items() {
		if item.HasID && item.ID == id {
			return item, true
		}
	}
	return store.Item{}, false
}

// LeadOpens is the date this task's OWN lead window opens. It is present even
// while an ancestor's gate or a hold is what currently hides the task, and
// absent once `activate` has released the current occurrence.
func (q *Queries) LeadOpens(item store.Item) (temporal.Date, bool) {
	_, value, state := q.leadGate(item)
	if state != gatePresent {
		return temporal.Date{}, false
	}
	return value.Date, true
}

// LeadOpensValue is the window's explaining value: the date, plus the wall time
// when a CLOCK lead put the opening at an instant no date can express. A
// renderer needs the whole value, not only its date, or a clock lead would
// print the day it opens and lose the hour.
func (q *Queries) LeadOpensValue(item store.Item) (temporal.Value, bool) {
	_, value, state := q.leadGate(item)
	if state != gatePresent {
		return temporal.Value{}, false
	}
	return value, true
}

// LeadWindowValue is where this task's lead window sits for its CURRENT anchor,
// whether or not `activate` has already released it. That is the question
// `show` asks: the record still carries the span, and a reader looking at the
// record wants to see the date it produces. LeadOpensValue answers the other
// question — "is a window still holding this task back" — and is what the
// availability-shaped surfaces read.
func (q *Queries) LeadWindowValue(item store.Item) (temporal.Value, bool) {
	_, value, state := q.leadGateWith(item, "")
	if state != gatePresent {
		return temporal.Value{}, false
	}
	return value, true
}

// LeadAnchorValue is the stamp a lead is measured back from: the deadline when
// there is one, otherwise the available-from date.
func (q *Queries) LeadAnchorValue(item store.Item) (temporal.Value, bool) {
	if deadline, ok := q.DeadlineValue(item); ok {
		return deadline, true
	}
	return q.ScheduledValue(item)
}

// LeadOpensAt is the same window as an instant, which is the only shape a clock
// lead can be expressed in.
func (q *Queries) LeadOpensAt(item store.Item) (time.Time, bool) {
	instant, _, state := q.leadGate(item)
	if state != gatePresent {
		return time.Time{}, false
	}
	return instant.UTC(), true
}

// Project is the task's project FOR DISPLAY: the title of the nearest ancestor
// headline that is a section or an open task. A task whose every ancestor is a
// closed task, and which has no section above it, has none — it falls out of
// the Projects view rather than heading a dead group.
func (q *Queries) Project(item store.Item) (string, bool) {
	node := q.NodeFor(item)
	if node == nil {
		return "", false
	}
	project := node.OpenProject()
	if project == nil {
		return "", false
	}
	return project.Title, true
}

// APITime is TemporalValue#api_time: the stored halves plus what they resolve
// to for THIS reader. Both are needed — the stored zone is what the user wrote,
// the effective zone and instant are what it means right now.
type APITime struct {
	Local             string
	Timezone          string
	Fold              int
	EffectiveTimezone string
	Instant           string
}

// APITimeFor renders a value's time half, or ok=false for an all-day value,
// which has no wall time to report.
func (q *Queries) APITimeFor(value temporal.Value, present bool) (APITime, bool) {
	if !present || value.AllDay() {
		return APITime{}, false
	}
	instant, err := value.Instant(q.context)
	if err != nil {
		return APITime{}, false
	}
	return APITime{
		Local:             value.LocalTime,
		Timezone:          value.Timezone,
		Fold:              value.Fold,
		EffectiveTimezone: value.EffectiveZone(q.context).String(),
		Instant:           instant.UTC().Format("2006-01-02T15:04:05Z"),
	}, true
}

// LiveItems is the live file's tasks in file order — the population ref
// resolution addresses. The archive is deliberately absent: a ref names
// something you can still act on.
func (q *Queries) LiveItems() []store.Item { return q.snapshot.Items() }

// ArchiveItems is the swept file's tasks in file order. Only the surfaces that
// deliberately widen to closed history read it.
func (q *Queries) ArchiveItems() []store.Item { return q.snapshot.ArchiveItems() }

// OpenStates is the accepted-and-not-finished vocabulary, which is the default
// scope for every ref.
func OpenStates() []string { return append([]string{}, openStates...) }

// ClosedStates is the finished vocabulary. `recur` reads it to refuse a live
// repeater on a task `done` can never reach to roll — dead recurrence that
// would sit in the file forever looking like a schedule.
func ClosedStates() []string { return append([]string{}, closedStates...) }
