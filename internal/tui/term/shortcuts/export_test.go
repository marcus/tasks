package shortcuts

import "testing"

func TestExportCoversEveryStableHostContext(t *testing.T) {
	want := map[string]bool{
		"list": true, "detail": true, "task_edit": true, "modal": true,
		"modal_filter": true, "form": true, "field_modal": true, "picker": true, "context_picker": true,
		"filter": true, "prompt": true, "response": true, "response_detail": true,
		"agent_activity": true, "agent_activity_filter": true,
	}
	for _, context := range ExportContexts() {
		delete(want, context.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing host contexts: %v", want)
	}

	bindingsByContext := map[string]int{}
	for _, binding := range ExportBindings() {
		bindingsByContext[binding.Context]++
	}
	for _, context := range ExportContexts() {
		if bindingsByContext[context.Name] == 0 {
			t.Errorf("context %q has no exported bindings", context.Name)
		}
	}
}

func TestExportedCommandsCarryCompleteRegistryMetadata(t *testing.T) {
	commands := ExportCommands()
	if len(commands) == 0 {
		t.Fatal("no commands exported")
	}
	for _, command := range commands {
		if command.ID == "" || command.FooterLabel == "" || command.Description == "" ||
			command.Context == "" || command.FooterPriority <= 0 {
			t.Errorf("incomplete command: %#v", command)
		}
	}

	assertCommand := func(context, id, label string, priority int, binding string) {
		t.Helper()
		for _, command := range commands {
			if command.Context != context || command.ID != id {
				continue
			}
			if command.FooterLabel != label || command.FooterPriority != priority ||
				!contains(command.DefaultBindings, binding) {
				t.Fatalf("%s/%s = %#v", context, id, command)
			}
			return
		}
		t.Fatalf("missing command %s/%s", context, id)
	}
	assertCommand("list", "open-detail", "Details", 1, "enter")
	assertCommand("detail", "start-task-edit", "Edit", 1, "e")
	assertCommand("task_edit", "task-edit-save", "Save", 1, "ctrl+s")
	assertCommand("modal", "close-modal", "Close", 1, "esc")
	assertCommand("prompt", "prompt-submit", "Submit", 1, "enter")
	assertCommand("filter", "filter-apply", "Apply", 1, "enter")
	assertCommand("modal_filter", "modal-filter-clear", "Clear", 1, "esc")
	assertCommand("form", "form-submit", "Submit", 1, "enter")
	assertCommand("picker", "picker-next", "Next", 4, "down")
	assertCommand("context_picker", "context-picker-toggle", "Toggle", 2, "space")
	assertCommand("response", "resp-up", "Scroll", 4, "pgup")
	assertCommand("response_detail", "start-task-edit", "Edit", 1, "e")
	assertCommand("agent_activity", "modal-down", "Scroll", 4, "down")
	assertCommand("agent_activity_filter", "modal-filter-apply", "Apply", 1, "enter")
}

func TestModalBindingsKeepDistinctSemanticCommandIDs(t *testing.T) {
	want := map[string]string{
		"y": "modal-confirm", "enter": "modal-confirm-default",
		"?": "close-modal-question",
	}
	for _, binding := range ExportBindings() {
		if binding.Context != "modal" || want[binding.Key] == "" {
			continue
		}
		if binding.CommandID == want[binding.Key] {
			delete(want, binding.Key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing exact modal bindings: %v", want)
	}
}

func TestModalProjectionIncludesBothConditionalCtrlCCommands(t *testing.T) {
	want := map[string]bool{"quit": true, "quit-confirmation-reminder": true}
	for _, binding := range ExportBindings() {
		if binding.Context == "modal" && binding.Key == "ctrl+c" {
			delete(want, binding.CommandID)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing projected ctrl-c commands: %v", want)
	}
}

func TestExportReadsRegistryRatherThanADuplicateKeyTable(t *testing.T) {
	original := Registry
	Registry = append(append([]Entry{}, Registry...), entry(Entry{
		Sequences: []string{"~"}, DisplayKey: "~", Description: "fixture action",
		Contexts: []Context{List}, Handler: "fixture_action",
	}))
	t.Cleanup(func() { Registry = original })

	for _, binding := range ExportBindings() {
		if binding.Context == "list" && binding.Key == "~" && binding.CommandID == "fixture-action" {
			return
		}
	}
	t.Fatal("registry addition did not reach exported bindings")
}

func TestCanonicalExportedKeyNamesMatchBubbleTeaVocabulary(t *testing.T) {
	cases := map[string]string{
		"\x03": "ctrl+c", "\x13": "ctrl+s", "\x1b[A": "up", "\x1b[3~": "delete",
		"\x1b[1;3A": "alt+up", "\x1bk": "alt+k", "\x1b[Z": "shift+tab", "\r": "enter",
	}
	for sequence, want := range cases {
		if got := canonicalKey(sequence); got != want {
			t.Errorf("canonicalKey(%q)=%q want %q", sequence, got, want)
		}
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
