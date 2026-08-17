package taskquery

import (
	"sort"

	"github.com/marcus/tasks/internal/store"
)

// RankByPriorityThenDue is the shared TRIAGE order: the one ranking every
// surface uses when a queue exists to be decided rather than browsed.
//
//	priority A > B > C > none
//	within a band, the soonest date boundary first
//	an undated row after every dated row in its band
//	canonical file/DFS order as the final tiebreak
//
// It is a core function, not an adapter detail, because the ORDER is part of
// what the list means: the agent-ready queue is taken from the top, and the
// Approvals queue is scanned from the top. A sort that lived in whichever
// surface happened to print it would drift between CLI, TUI and HTTP the first
// time one of them grew a new caller.
//
// The date used is agendaSortKey — the deadline boundary if there is one, else
// the moment an available-from date opens, else a sentinel far future, which is
// exactly what puts undated rows last inside their band. Priority strictly
// dominates the date: a C due tomorrow still ranks below an undated A.
//
// The sort is stable, so items tying on both keys keep the order the snapshot
// handed over, which is file order for a flat list and DFS order for a walked
// tree. Callers that want file/DFS order untouched simply do not call this.
func (q *Queries) RankByPriorityThenDue(items []store.Item) []store.Item {
	ranked := append([]store.Item{}, items...)
	sort.SliceStable(ranked, func(left, right int) bool {
		return q.PriorityThenDueBefore(ranked[left], ranked[right])
	})
	return ranked
}

// PriorityThenDueBefore is RankByPriorityThenDue's comparator on its own, for
// callers that already own a sort — a row builder ordering rows rather than
// items, say — and must order by the same rule rather than a lookalike.
//
// It is deliberately NOT a total order: it reports nothing about two items that
// tie on priority and date, leaving the caller's stable sort to preserve
// whatever order they arrived in.
func (q *Queries) PriorityThenDueBefore(left, right store.Item) bool {
	leftPriority, rightPriority := priorityKey(left), priorityKey(right)
	if leftPriority != rightPriority {
		return leftPriority < rightPriority
	}
	return q.agendaSortKey(left).Before(q.agendaSortKey(right))
}
