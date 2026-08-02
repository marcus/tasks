package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"tasks-go/internal/query"
)

type input struct {
	CaseID     string          `json:"case_id"`
	Operation  string          `json:"operation"`
	Argv       []string        `json:"argv"`
	ArgvBase64 []string        `json:"argv_base64"`
	Kwargs     json.RawMessage `json:"kwargs"`
}

// argv decodes the byte-level argument encoding a case may use instead of
// `argv`: JSONL cannot carry an argument whose bytes are not valid UTF-8, and
// ParseCLI treats those arguments differently from every valid one.
func (in input) argv() ([]string, error) {
	if in.ArgvBase64 == nil {
		return in.Argv, nil
	}
	args := make([]string, 0, len(in.ArgvBase64))
	for _, encoded := range in.ArgvBase64 {
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, err
		}
		args = append(args, string(decoded))
	}
	return args, nil
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
		var args []string
		if args, err = in.argv(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		var parsed query.ParsedFilter
		parsed, err = query.ParseCLI(args)
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
	// Ruby rejects unrecognised keywords before initialize's body runs, so this
	// precedes every coercion and every domain rule below.
	order, err := keywordOrder(raw)
	if err != nil {
		return query.FilterOptions{}, err
	}
	if err := rejectUnknownKeywords(order); err != nil {
		return query.FilterOptions{}, err
	}
	// Ruby coerces or truth-tests every keyword before a domain rule sees it,
	// so nothing here may reach a typed decode: a strict decoder would reject
	// values Ruby accepts, and would answer with a decoder message where Ruby
	// answers with the ported domain message.
	booleans := map[string]*bool{
		"deferred_only": &options.DeferredOnly, "unavailable_only": &options.UnavailableOnly,
		"someday_only": &options.SomedayOnly, "recurring_only": &options.RecurringOnly,
		"body_search": &options.BodySearch, "delegated_only": &options.DelegatedOnly,
		"agent_ready_only": &options.AgentReadyOnly,
	}
	for name, target := range booleans {
		raw, present := fields[name]
		if !present {
			continue
		}
		value, err := decodeGeneric(raw)
		if err != nil {
			return query.FilterOptions{}, err
		}
		*target = query.CoerceBool(value)
	}
	// `scope.to_s` runs unconditionally, so an explicit null scope becomes the
	// empty scope and is rejected by name; `priority&.to_s` and `state&.to_s`
	// skip null, which stays indistinguishable from omitted.
	scalars := map[string]struct {
		target      **string
		nullCoerces bool
	}{
		"scope":    {&options.Scope, true},
		"priority": {&options.Priority, false},
		"state":    {&options.State, false},
	}
	for name, scalar := range scalars {
		raw, present := fields[name]
		if !present {
			continue
		}
		value, err := decodeGeneric(raw)
		if err != nil {
			return query.FilterOptions{}, err
		}
		if value == nil && !scalar.nullCoerces {
			continue
		}
		coerced := query.CoerceString(value)
		*scalar.target = &coerced
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

// knownKeywords is TaskFilter#initialize's keyword list. Anything else is what
// Ruby's interpreter reports before the body runs.
var knownKeywords = map[string]bool{
	"scope": true, "deferred_only": true, "unavailable_only": true,
	"someday_only": true, "recurring_only": true, "body_search": true,
	"contexts": true, "tags": true, "priority": true, "state": true,
	"text": true, "delegated_only": true, "agent_ready_only": true,
}

// keywordOrder returns the object's keys in the order they were written. A Go
// map cannot carry that order, and the order is observable: the ArgumentError
// names unknown keywords as the caller gave them, not sorted.
func keywordOrder(raw json.RawMessage) ([]string, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil { // consumes `{`
		return nil, err
	}
	var order []string
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, isKey := token.(string)
		if !isKey {
			return nil, fmt.Errorf("kwargs key is not a string: %v", token)
		}
		// A repeated key keeps its first position, as Ruby's Hash#[]= does.
		if !seen[key] {
			seen[key] = true
			order = append(order, key)
		}
		var discard json.RawMessage
		if err := decoder.Decode(&discard); err != nil {
			return nil, err
		}
	}
	return order, nil
}

func rejectUnknownKeywords(order []string) error {
	var unknown []string
	for _, key := range order {
		if !knownKeywords[key] {
			unknown = append(unknown, query.InspectSymbol(key))
		}
	}
	switch len(unknown) {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("unknown keyword: %s", unknown[0])
	default:
		return fmt.Errorf("unknown keywords: %s", strings.Join(unknown, ", "))
	}
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
