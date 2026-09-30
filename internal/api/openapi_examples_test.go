package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"gopkg.in/yaml.v3"
)

// The contract's examples are documentation a client copies, so an example
// its own schema rejects is a lie in the one place a reader trusts most. This
// test validates EVERY example in docs/api/openapi.yaml — media-type examples
// on requests and responses (inline or through components/examples),
// parameter and header examples, and schema-level examples — against the
// schema it illustrates, with a JSON Schema 2020-12 validator (the dialect
// OpenAPI 3.1 schemas are written in). It is what keeps a new required member
// from leaving the examples behind.

const openAPIResource = "file:///openapi.json"

func TestOpenAPIExamplesMatchTheirSchemas(t *testing.T) {
	doc := loadOpenAPI(t)
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(openAPIResource, doc); err != nil {
		t.Fatalf("add openapi resource: %v", err)
	}

	cases := collectOpenAPIExamples(t, doc)
	if len(cases) < 50 {
		// A walker that silently stopped finding examples would pass vacuously.
		t.Fatalf("found only %d examples; the walker lost track of the document", len(cases))
	}
	compiled := map[string]*jsonschema.Schema{}
	for _, example := range cases {
		schema, ok := compiled[example.schema]
		if !ok {
			var err error
			schema, err = compiler.Compile(openAPIResource + "#" + example.schema)
			if err != nil {
				t.Errorf("%s: compile %s: %v", example.where, example.schema, err)
				continue
			}
			compiled[example.schema] = schema
		}
		if err := schema.Validate(example.value); err != nil {
			t.Errorf("%s does not match its schema:\n%v", example.where, err)
		}
	}
}

// exampleCase is one example and the JSON pointer of the schema it illustrates.
type exampleCase struct {
	where  string
	schema string
	value  any
}

func loadOpenAPI(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "api", "openapi.yaml"))
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		t.Fatalf("parse openapi: %v", err)
	}
	doc, ok := yamlValue(&node).(map[string]any)
	if !ok {
		t.Fatalf("openapi document is not a mapping")
	}
	return doc
}

// yamlValue converts a YAML node into the JSON data model. It goes through the
// node rather than decoding into `any` so an unquoted date stays the string
// the JSON wire carries instead of becoming a time.Time.
func yamlValue(node *yaml.Node) any {
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return nil
		}
		return yamlValue(node.Content[0])
	case yaml.AliasNode:
		return yamlValue(node.Alias)
	case yaml.MappingNode:
		object := make(map[string]any, len(node.Content)/2)
		for index := 0; index+1 < len(node.Content); index += 2 {
			object[node.Content[index].Value] = yamlValue(node.Content[index+1])
		}
		return object
	case yaml.SequenceNode:
		list := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			list = append(list, yamlValue(child))
		}
		return list
	}
	switch node.ShortTag() {
	case "!!null":
		return nil
	case "!!bool":
		var value bool
		_ = node.Decode(&value)
		return value
	case "!!int", "!!float":
		return json.Number(node.Value)
	}
	return node.Value
}

func pointerToken(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// openAPIWalker resolves the document's own `$ref`s to components.
type openAPIWalker struct {
	t     *testing.T
	doc   map[string]any
	cases []exampleCase
}

// deref follows a local `$ref`, returning the target and its pointer.
func (w *openAPIWalker) deref(value any, pointer string) (map[string]any, string) {
	object, _ := value.(map[string]any)
	for object != nil {
		ref, ok := object["$ref"].(string)
		if !ok {
			break
		}
		if !strings.HasPrefix(ref, "#/") {
			w.t.Fatalf("%s: non-local $ref %q", pointer, ref)
		}
		pointer = strings.TrimPrefix(ref, "#")
		var current any = w.doc
		for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			next, _ := current.(map[string]any)
			current = next[token]
		}
		object, _ = current.(map[string]any)
		if object == nil {
			w.t.Fatalf("unresolved $ref %q", ref)
		}
	}
	return object, pointer
}

func collectOpenAPIExamples(t *testing.T, doc map[string]any) []exampleCase {
	w := &openAPIWalker{t: t, doc: doc}
	paths, _ := doc["paths"].(map[string]any)
	for _, path := range sortedKeys(paths) {
		item, _ := paths[path].(map[string]any)
		base := "/paths/" + pointerToken(path)
		w.parameters(item["parameters"], base+"/parameters", path)
		for _, method := range sortedKeys(item) {
			operation, ok := item[method].(map[string]any)
			if !ok || method == "parameters" {
				continue
			}
			where := strings.ToUpper(method) + " " + path
			opBase := base + "/" + method
			w.parameters(operation["parameters"], opBase+"/parameters", where)
			if body, pointer := w.deref(operation["requestBody"], opBase+"/requestBody"); body != nil {
				w.content(body["content"], pointer+"/content", where+" request")
			}
			responses, _ := operation["responses"].(map[string]any)
			for _, code := range sortedKeys(responses) {
				response, pointer := w.deref(responses[code], opBase+"/responses/"+code)
				label := fmt.Sprintf("%s %s", where, code)
				w.content(response["content"], pointer+"/content", label)
				headers, _ := response["headers"].(map[string]any)
				for _, name := range sortedKeys(headers) {
					header, headerPointer := w.deref(headers[name], pointer+"/headers/"+pointerToken(name))
					w.examplesOf(header, headerPointer, label+" header "+name)
				}
			}
		}
	}
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	for _, name := range sortedKeys(schemas) {
		w.schema(schemas[name], "/components/schemas/"+pointerToken(name), "schema "+name)
	}
	return w.cases
}

func (w *openAPIWalker) parameters(value any, pointer, where string) {
	list, _ := value.([]any)
	for index, raw := range list {
		parameter, parameterPointer := w.deref(raw, fmt.Sprintf("%s/%d", pointer, index))
		name, _ := parameter["name"].(string)
		w.examplesOf(parameter, parameterPointer, where+" parameter "+name)
	}
}

func (w *openAPIWalker) content(value any, pointer, where string) {
	content, _ := value.(map[string]any)
	for _, mediaType := range sortedKeys(content) {
		media, _ := content[mediaType].(map[string]any)
		w.examplesOf(media, pointer+"/"+pointerToken(mediaType), where+" "+mediaType)
	}
}

// examplesOf validates a parameter's, header's, or media type's `example` and
// named `examples` against its `schema`.
func (w *openAPIWalker) examplesOf(holder map[string]any, pointer, where string) {
	if holder == nil || holder["schema"] == nil {
		return
	}
	schema := pointer + "/schema"
	if example, ok := holder["example"]; ok {
		w.cases = append(w.cases, exampleCase{where: where + " example", schema: schema, value: example})
	}
	examples, _ := holder["examples"].(map[string]any)
	for _, name := range sortedKeys(examples) {
		example, _ := w.deref(examples[name], pointer+"/examples/"+pointerToken(name))
		if value, ok := example["value"]; ok {
			w.cases = append(w.cases, exampleCase{where: where + " example " + name, schema: schema, value: value})
		}
	}
}

// schema walks a schema and its subschemas for schema-level examples.
func (w *openAPIWalker) schema(value any, pointer, where string) {
	schema, ok := value.(map[string]any)
	if !ok {
		return
	}
	if example, ok := schema["example"]; ok {
		w.cases = append(w.cases, exampleCase{where: where + " example", schema: pointer, value: example})
	}
	if examples, ok := schema["examples"].([]any); ok {
		for index, example := range examples {
			w.cases = append(w.cases, exampleCase{
				where: fmt.Sprintf("%s examples[%d]", where, index), schema: pointer, value: example,
			})
		}
	}
	for _, keyword := range []string{"properties", "patternProperties", "$defs"} {
		members, _ := schema[keyword].(map[string]any)
		for _, name := range sortedKeys(members) {
			w.schema(members[name], pointer+"/"+keyword+"/"+pointerToken(name), where+"."+name)
		}
	}
	for _, keyword := range []string{"items", "additionalProperties", "not", "contains", "propertyNames"} {
		w.schema(schema[keyword], pointer+"/"+keyword, where+"."+keyword)
	}
	for _, keyword := range []string{"oneOf", "anyOf", "allOf", "prefixItems"} {
		list, _ := schema[keyword].([]any)
		for index, member := range list {
			w.schema(member, fmt.Sprintf("%s/%s/%d", pointer, keyword, index), fmt.Sprintf("%s.%s[%d]", where, keyword, index))
		}
	}
}

// Examples are hand-written; responses are not. This validates what the server
// actually answers on the read routes (and a completing PATCH, which carries
// `meta.effects`) against the same schemas, so a schema that rejects a real
// response — an ambiguous nullable `oneOf`, a member the server emits that the
// contract forgot — fails here rather than in a client's validator.
func TestLiveResponsesMatchTheirSchemas(t *testing.T) {
	doc := loadOpenAPI(t)
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	if err := compiler.AddResource(openAPIResource, doc); err != nil {
		t.Fatalf("add openapi resource: %v", err)
	}
	h := newHarness(t)
	check := func(method, route, target string, answered answer, status int) {
		t.Helper()
		if answered.Status != status {
			t.Errorf("%s %s = %d, want %d: %s", method, target, answered.Status, status, answered.Body)
			return
		}
		pointer := fmt.Sprintf("/paths/%s/%s/responses/%d/content/application~1json/schema",
			pointerToken(route), strings.ToLower(method), status)
		schema, err := compiler.Compile(openAPIResource + "#" + pointer)
		if err != nil {
			t.Fatalf("compile %s: %v", pointer, err)
		}
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(answered.Body))
		if err != nil {
			t.Fatalf("%s %s: decode: %v", method, target, err)
		}
		if err := schema.Validate(value); err != nil {
			t.Errorf("%s %s response does not match its schema:\n%v", method, target, err)
		}
	}
	reads := []struct{ route, target string }{
		{"/meta", "/meta"},
		{"/sections", "/sections"},
		{"/tasks", "/tasks?scope=all"},
		{"/tasks/{id}", "/tasks/" + fixFlight},
		{"/projects", "/projects"},
		{"/views/outline", "/views/outline?include_closed=true"},
		{"/views/{name}", "/views/agenda"},
		{"/views/{name}", "/views/quadrants"},
		{"/views/{name}", "/views/inbox"},
		{"/dates/parse", "/dates/parse?input=fri+4pm"},
		{"/dates/parse", "/dates/parse?input=in+2+weeks"},
		{"/dates/parse", "/dates/parse?input=not+a+date"},
		{"/history", "/history"},
	}
	for _, read := range reads {
		check("GET", read.route, read.target, h.get("/api/v1"+read.target), 200)
	}
	done := h.json("PATCH", "/api/v1/tasks/"+fixPR, `{"state":"DONE"}`, h.withIfMatch(h.etagOf(fixPR)))
	check("PATCH", "/tasks/{id}", "/tasks/"+fixPR, done, 200)
	rolled := h.json("PATCH", "/api/v1/tasks/"+fixFlight, `{"state":"DONE"}`, h.withIfMatch(h.etagOf(fixFlight)))
	check("PATCH", "/tasks/{id}", "/tasks/"+fixFlight, rolled, 200)
	check("GET", "/history", "/history", h.get("/api/v1/history"), 200)
}
