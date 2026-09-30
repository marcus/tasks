package main

import (
	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/temporal"
)

// The taskless previews for date and lead input — `due --explain`,
// `schedule --explain`, and `lead --explain` — are `recur --explain`'s
// siblings and the CLI twins of GET /dates/parse and GET /lead/explain. They
// parse with the same shared functions the write paths use, never touch the
// store, and their --json payloads are the HTTP `data` members exactly.

// dateExplain parses a date expression with the free-text grammar (trailing
// zone and fold words included), after applying any --timezone/--floating/
// --fold flags, in the configured date order.
func (s *surfaceContext) dateExplain(input string, flags temporalOptions, asJSON bool) int {
	context, status := s.temporalContext()
	if status != 0 {
		return status
	}
	explanation := temporal.Explain(input, temporal.ParseOptions{
		Today: context.LocalDate(), Order: s.dateOrder(),
		Timezone: flags.timezone, Floating: flags.floating, Fold: flags.fold,
		FoldSpecified: flags.foldGiven,
	}, context)

	if asJSON {
		w := jsonWriter()
		writeDateExplanation(w, explanation)
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		if !explanation.HasValue {
			return 1
		}
		return 0
	}
	if !explanation.HasValue {
		return abort(explanation.Error)
	}
	out(temporalValueLabel(explanation.Value) + " — " + explanation.Human)
	return 0
}

// writeDateExplanation is the /dates/parse `data` object.
func writeDateExplanation(w *jsonout.Writer, explanation temporal.Explanation) {
	w.BeginObject()
	w.KeyStr("input", explanation.Input)
	if !explanation.HasValue {
		w.KeyStr("error", explanation.Error)
		w.EndObject()
		return
	}
	value := explanation.Value
	w.KeyStr("date", value.Date.ISO())
	w.Key("time")
	if value.AllDay() {
		w.Null()
	} else {
		w.BeginObject()
		w.KeyStr("local", value.LocalTime)
		w.KeyStrOrNull("timezone", value.Timezone)
		w.KeyInt("fold", value.Fold)
		w.EndObject()
	}
	w.KeyStr("human", explanation.Human)
	w.EndObject()
}

// leadExplain previews a lead span and, given --anchor, the date its window
// would open before that date.
func (s *surfaceContext) leadExplain(input, anchorText string, hasAnchor, asJSON bool) int {
	anchor := temporal.Date{}
	if hasAnchor {
		context, status := s.temporalContext()
		if status != 0 {
			return status
		}
		parsed, ok := temporal.ParseWhen(anchorText, context.LocalDate(), s.dateOrder())
		if !ok {
			return abort("unrecognized date: " + anchorText)
		}
		anchor = parsed
	}
	explanation := lead.Explain(input, anchor, hasAnchor)

	if asJSON {
		w := jsonWriter()
		w.BeginObject()
		w.KeyStr("input", explanation.Input)
		if explanation.Error != "" {
			w.KeyStr("error", explanation.Error)
		} else {
			w.KeyStrOrNull("canonical", explanation.Canonical)
			w.KeyStr("human", explanation.Human)
			w.Key("opens")
			if explanation.HasOpens {
				w.Str(explanation.Opens.ISO())
			} else {
				w.Null()
			}
		}
		w.EndObject()
		if err := w.Err(); err != nil {
			return abort(err.Error())
		}
		out(w.String())
		if explanation.Error != "" {
			return 1
		}
		return 0
	}
	if explanation.Error != "" {
		return abort(explanation.Error + "\n" + leadHint)
	}
	if explanation.Canonical == "" {
		out("off — clears any lead time on the task")
		return 0
	}
	described, _ := lead.Describe(explanation.Canonical)
	line := explanation.Canonical + " — " + described
	switch {
	case !hasAnchor:
		line += " the task's date"
	case explanation.HasOpens:
		line += " " + anchor.ISO() + " — opens " + explanation.Opens.ISO()
	default:
		// An hour span opens at an instant that depends on the anchor's time
		// of day, which a date alone does not say.
		line += " " + anchor.ISO()
	}
	out(line)
	return 0
}
