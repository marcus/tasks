package api

import (
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/marcus/tasks/internal/jsonout"
	"github.com/marcus/tasks/internal/lead"
	"github.com/marcus/tasks/internal/temporal"
)

// The taskless parse previews: the friendly text a person types into a date
// or lead field, read by the same parser the write paths and the TUI use,
// without naming a task and without touching the store. They are
// /recurrence/explain's siblings and share its conventions: `input` is
// required and non-blank (else 422), "this is not a date" is a 200 answer with
// an `error` member rather than a request error, and the envelope carries
// `data` only because no store was read.

const (
	datesParsePath  = "/api/v1/dates/parse"
	leadExplainPath = "/api/v1/lead/explain"
)

// parseDate is the API twin of the TUI's date field and of the CLI's
// `due`/`schedule` expression: it answers what `input` means as a date, in the
// server's zone and configured date order, so a browser field can preview it
// and then send the ISO date and time object a write takes.
func (s *Server) parseDate(request *http.Request) (response, error) {
	params, err := queryParams(request, "input")
	if err != nil {
		return response{}, err
	}
	input, err := requiredInput(params)
	if err != nil {
		return response{}, err
	}
	context := s.options.TemporalContext()
	explanation := temporal.Explain(input, temporal.ParseOptions{
		Today: context.LocalDate(), Order: temporal.OrderNamed(s.options.DateOrder),
	}, context)

	w := jsonout.New()
	w.BeginObject()
	w.Key("data")
	w.BeginObject()
	w.KeyStr("input", explanation.Input)
	if !explanation.HasValue {
		w.KeyStr("error", explanation.Error)
	} else {
		value := explanation.Value
		w.KeyStr("date", value.Date.ISO())
		w.Key("time")
		if value.AllDay() {
			w.Null()
		} else {
			// Exactly the TaskTimeInput shape, so a client can send it back
			// unchanged as `deadline_time` / `scheduled_time`.
			w.BeginObject()
			w.KeyStr("local", value.LocalTime)
			w.KeyStrOrNull("timezone", value.Timezone)
			w.KeyInt("fold", value.Fold)
			w.EndObject()
		}
		w.KeyStr("human", explanation.Human)
	}
	w.EndObject()
	w.EndObject()
	return response{status: 200, body: w.Bytes()}, nil
}

// explainLead previews a lead-time input: the canonical span a write would
// store, how `lead_human` renders it, and — given the task's anchor date — the
// date its window opens, which is `lead_opens` before any write.
func (s *Server) explainLead(request *http.Request) (response, error) {
	params, err := queryParams(request, "input", "anchor")
	if err != nil {
		return response{}, err
	}
	input, err := requiredInput(params)
	if err != nil {
		return response{}, err
	}
	anchorText, anchor, err := anchorParam(params)
	if err != nil {
		return response{}, err
	}
	explanation := lead.Explain(input, anchor, anchorText != "")

	w := jsonout.New()
	w.BeginObject()
	w.Key("data")
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
	w.EndObject()
	return response{status: 200, body: w.Bytes()}, nil
}

// requiredInput is the `input` rule every preview shares: present and
// non-blank, or a 422 naming the parameter.
func requiredInput(params url.Values) (string, error) {
	if !params.Has("input") {
		return "", validationError(reason("input", "is required"))
	}
	input := params.Get("input")
	if strings.TrimSpace(input) == "" {
		return "", validationError(reason("input", "must be non-empty text"))
	}
	return input, nil
}

// anchorParam reads the optional `anchor=YYYY-MM-DD` — the date a preview
// measures from instead of today. Absent is ("", zero, nil); anything present
// that is not a real calendar date is a 422, because a malformed parameter is a
// request error rather than an answer about the input.
func anchorParam(params url.Values) (string, temporal.Date, error) {
	if !params.Has("anchor") {
		return "", temporal.Date{}, nil
	}
	date, ok := temporal.ParseDate(params.Get("anchor"))
	if !ok {
		return "", temporal.Date{}, validationError(reason("anchor", "must be a real YYYY-MM-DD date"))
	}
	return date.ISO(), date, nil
}

// writeStringMap emits a config map with sorted keys, so /meta is byte-stable
// for one configuration.
func writeStringMap(w *jsonout.Writer, values map[string]string) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	w.BeginObject()
	for _, key := range keys {
		w.KeyStr(key, values[key])
	}
	w.EndObject()
}
