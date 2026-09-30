package lead

import "github.com/marcus/tasks/internal/temporal"

// Explanation is the answer to "what would this lead text store, and when
// would its window open", computed without a task and without a write — the
// lead twin of recur.Explain.
//
// Three shapes, told apart structurally: a span has Canonical and Human; a
// clearing word has HasCanonical with an empty Canonical; unreadable input has
// only Error.
type Explanation struct {
	Input        string
	Canonical    string
	HasCanonical bool
	// Human is the span as `lead_human` renders it: "3 weeks".
	Human string
	// Opens is the date a CALENDAR span's window opens before the anchor, set
	// only when the caller supplied an anchor. A clock span (`5h`) opens at an
	// instant that depends on the anchor's time of day, which a date alone
	// cannot say, so it never has one here.
	Opens    temporal.Date
	HasOpens bool
	Error    string
}

// Explain parses input with Parse and, given an anchor date (the task's
// deadline, else its available-from date), derives the gate date the same way
// the read model does.
func Explain(input string, anchor temporal.Date, hasAnchor bool) Explanation {
	result := Parse(input)
	if result.Error != "" {
		return Explanation{Input: input, Error: result.Error}
	}
	if result.IsOff() {
		return Explanation{Input: input, HasCanonical: true, Human: "no lead time"}
	}
	human, _ := Humanize(result.Canonical)
	explanation := Explanation{Input: input, Canonical: result.Canonical, HasCanonical: true, Human: human}
	if hasAnchor {
		if gate, ok := GateDate(anchor, result.Canonical); ok {
			explanation.Opens, explanation.HasOpens = gate, true
		}
	}
	return explanation
}
