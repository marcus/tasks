package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tasks-go/internal/query"
)

type input struct {
	CaseID    string          `json:"case_id"`
	Operation string          `json:"operation"`
	Argv      []string        `json:"argv"`
	Kwargs    json.RawMessage `json:"kwargs"`
}
type output struct {
	CaseID string        `json:"case_id"`
	OK     bool          `json:"ok"`
	JSON   *bool         `json:"json"`
	Filter *filterOutput `json:"filter,omitempty"`
}
type errorResult struct {
	CaseID string      `json:"case_id"`
	OK     bool        `json:"ok"`
	Error  errorOutput `json:"error"`
}
type errorOutput struct {
	Class   string `json:"class"`
	Message string `json:"message"`
}
type filterOutput struct {
	Scope           string   `json:"scope"`
	IncludeArchive  bool     `json:"include_archive"`
	DeferredOnly    bool     `json:"deferred_only"`
	UnavailableOnly bool     `json:"unavailable_only"`
	SomedayOnly     bool     `json:"someday_only"`
	RecurringOnly   bool     `json:"recurring_only"`
	BodySearch      bool     `json:"body_search"`
	DelegatedOnly   bool     `json:"delegated_only"`
	AgentReadyOnly  bool     `json:"agent_ready_only"`
	Contexts        []string `json:"contexts"`
	Tags            []string `json:"tags"`
	Priority        *string  `json:"priority"`
	State           *string  `json:"state"`
	States          []string `json:"states"`
	Text            []string `json:"text"`
	TextQuery       string   `json:"text_query"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: query-filter-parse-probe CASES.jsonl")
		os.Exit(2)
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer file.Close()
	// Ruby's JSON.generate leaves `<`, `>`, and `&` unescaped; Go's default
	// encoder escapes them, which would diverge on any inspected value.
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var in input
		if err := json.Unmarshal(scanner.Bytes(), &in); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := encoder.Encode(run(in)); err != nil {
			panic(err)
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func run(in input) any {
	var filter query.Filter
	var jsonFlag *bool
	var err error
	switch in.Operation {
	case "parse_cli":
		var parsed query.ParsedFilter
		parsed, err = query.ParseCLI(in.Argv)
		filter = parsed.Filter()
		json := parsed.JSON()
		jsonFlag = &json
	case "new":
		var options query.FilterOptions
		options, err = decodeFilterOptions(in.Kwargs)
		if err == nil {
			filter, err = query.NewFilter(options)
		}
	default:
		err = fmt.Errorf("unknown operation: %s", in.Operation)
	}
	if err != nil {
		return errorResult{CaseID: in.CaseID, OK: false, Error: errorOutput{Class: "ArgumentError", Message: err.Error()}}
	}
	return output{CaseID: in.CaseID, OK: true, JSON: jsonFlag, Filter: project(filter)}
}

func decodeFilterOptions(raw json.RawMessage) (query.FilterOptions, error) {
	var options query.FilterOptions
	if len(raw) == 0 {
		return options, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return query.FilterOptions{}, err
	}
	// The coerced collections are decoded separately below; Ruby accepts any
	// value there, so they must not reach the typed decode.
	scalars := make(map[string]json.RawMessage, len(fields))
	for name, value := range fields {
		if name != "contexts" && name != "tags" && name != "text" {
			scalars[name] = value
		}
	}
	encoded, err := json.Marshal(scalars)
	if err != nil {
		return query.FilterOptions{}, err
	}
	if err := json.Unmarshal(encoded, &options); err != nil {
		return query.FilterOptions{}, err
	}
	if scope, present := fields["scope"]; present && string(scope) == "null" {
		empty := ""
		options.Scope = &empty
	}
	collections := map[string]*[]string{
		"contexts": &options.Contexts, "tags": &options.Tags, "text": &options.Text,
	}
	for name, target := range collections {
		raw, present := fields[name]
		if !present {
			continue
		}
		value, err := decodeGeneric(raw)
		if err != nil {
			return query.FilterOptions{}, err
		}
		*target = query.CoerceStrings(value)
	}
	return options, nil
}

// decodeGeneric keeps numbers as json.Number so integers stringify the way
// Ruby's Integer#to_s does rather than through float formatting.
func decodeGeneric(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func project(filter query.Filter) *filterOutput {
	priority, state := filter.Priority(), filter.State()
	var priorityOut, stateOut *string
	if priority != "" {
		priorityOut = &priority
	}
	if state != "" {
		stateOut = &state
	}
	return &filterOutput{Scope: filter.Scope(), IncludeArchive: filter.IncludeArchive(), DeferredOnly: filter.DeferredOnly(), UnavailableOnly: filter.UnavailableOnly(), SomedayOnly: filter.SomedayOnly(), RecurringOnly: filter.RecurringOnly(), BodySearch: filter.BodySearch(), DelegatedOnly: filter.DelegatedOnly(), AgentReadyOnly: filter.AgentReadyOnly(), Contexts: filter.Contexts(), Tags: filter.Tags(), Priority: priorityOut, State: stateOut, States: filter.States(), Text: filter.Text(), TextQuery: filter.TextQuery()}
}
