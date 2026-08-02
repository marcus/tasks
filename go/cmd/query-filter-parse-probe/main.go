package main

import (
	"bufio"
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
		result := run(in)
		encoded, err := json.Marshal(result)
		if err != nil {
			panic(err)
		}
		fmt.Println(string(encoded))
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
	if err := json.Unmarshal(raw, &options); err != nil {
		return query.FilterOptions{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return query.FilterOptions{}, err
	}
	if scope, present := fields["scope"]; present && string(scope) == "null" {
		empty := ""
		options.Scope = &empty
	}
	return options, nil
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
