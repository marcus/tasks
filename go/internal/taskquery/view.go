package taskquery

import (
	"strings"
	"time"

	"tasks-go/internal/store"
	"tasks-go/internal/temporal"
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
	for _, item := range q.snapshot.Items {
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
func (q *Queries) LiveItems() []store.Item { return q.snapshot.Items }

// OpenStates is the accepted-and-not-finished vocabulary, which is the default
// scope for every ref.
func OpenStates() []string { return append([]string{}, openStates...) }
