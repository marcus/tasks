package shortcuts

import (
	"fmt"
	"strings"
)

// ExportedBinding is one host-consumable default key binding.
type ExportedBinding struct {
	Key       string
	CommandID string
	Context   string
}

// ExportedCommand is host-facing command-palette and footer metadata.
type ExportedCommand struct {
	ID              string
	FooterLabel     string
	Description     string
	Context         string
	FooterPriority  int
	DefaultBindings []string
}

// ExportedContext describes a stable interaction context even when its keys
// are owned by an input widget rather than the shortcut registry.
type ExportedContext struct {
	Name              string
	ConsumesTextInput bool
}

type contextProjection struct {
	name      string
	textInput bool
	sources   []Context
}

// hostContextProjections describes how runtime focus layers project the one
// registry. It contains no keys or command metadata; those always come from
// Registry. Source order matches dispatch precedence.
var hostContextProjections = []contextProjection{
	{name: "list", sources: []Context{List, Global}},
	{name: "detail", sources: []Context{Detail, List, Global}},
	{name: "task_edit", textInput: true, sources: []Context{TaskEdit, Global}},
	{name: "modal", sources: []Context{Modal, Global}},
	{name: "modal_filter", textInput: true, sources: []Context{ModalFilter, Global}},
	{name: "form", textInput: true, sources: []Context{Form, Global}},
	{name: "picker", textInput: true, sources: []Context{Picker, Global}},
	{name: "context_picker", textInput: true, sources: []Context{ContextPicker, Global}},
	{name: "filter", textInput: true, sources: []Context{Filter, Global}},
	{name: "prompt", textInput: true, sources: []Context{Prompt, Global}},
	{name: "response", sources: []Context{List, Global}},
	{name: "response_detail", sources: []Context{Detail, List, Global}},
	{name: "agent_activity", sources: []Context{Modal, Global}},
	{name: "agent_activity_filter", textInput: true, sources: []Context{ModalFilter, Global}},
}

// ExportContexts returns every stable host focus context.
func ExportContexts() []ExportedContext {
	out := make([]ExportedContext, 0, len(hostContextProjections))
	for _, projection := range hostContextProjections {
		out = append(out, ExportedContext{
			Name: projection.name, ConsumesTextInput: projection.textInput,
		})
	}
	return out
}

// ExportBindings projects Registry's raw terminal sequences into Bubble Tea's
// canonical key names. Conditional same-key actions remain in registry order.
func ExportBindings() []ExportedBinding {
	var out []ExportedBinding
	seen := map[string]bool{}
	for _, projection := range hostContextProjections {
		for _, entry := range projectedEntries(projection) {
			if entry.DocOnly {
				continue
			}
			for _, sequence := range entry.Sequences {
				key := canonicalKey(sequence)
				if key == "" {
					continue
				}
				identity := projection.name + "\x00" + key + "\x00" + entry.CommandID
				if seen[identity] {
					continue
				}
				seen[identity] = true
				out = append(out, ExportedBinding{
					Key: key, CommandID: entry.CommandID, Context: projection.name,
				})
			}
		}
	}
	return out
}

// ExportCommands returns one command per command ID and host context, with all
// of that entry's default bindings folded onto it.
func ExportCommands() []ExportedCommand {
	var out []ExportedCommand
	indexes := map[string]int{}
	for _, projection := range hostContextProjections {
		for _, entry := range projectedEntries(projection) {
			if entry.DocOnly {
				continue
			}
			identity := projection.name + "\x00" + entry.CommandID
			index, present := indexes[identity]
			if !present {
				index = len(out)
				indexes[identity] = index
				out = append(out, ExportedCommand{
					ID: entry.CommandID, FooterLabel: entry.FooterLabel,
					Description: entry.Description, Context: projection.name,
					FooterPriority: entry.FooterPriority,
				})
			}
			for _, sequence := range entry.Sequences {
				out[index].DefaultBindings = appendUnique(out[index].DefaultBindings, canonicalKey(sequence))
			}
		}
	}
	return out
}

func projectedEntries(projection contextProjection) []Entry {
	var out []Entry
	for _, source := range projection.sources {
		for _, entry := range Registry {
			if entry.hasContext(source) {
				out = append(out, entry)
			}
		}
	}
	return out
}

// EntriesForHostContext returns registry entries in runtime dispatch order.
func EntriesForHostContext(name string) []Entry {
	for _, projection := range hostContextProjections {
		if projection.name == name {
			return append([]Entry{}, projectedEntries(projection)...)
		}
	}
	return nil
}

func appendUnique(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, current := range values {
		if current == value {
			return values
		}
	}
	return append(values, value)
}

func canonicalKey(sequence string) string {
	switch sequence {
	case "\r", "\n":
		return "enter"
	case "\t":
		return "tab"
	case "\x1b":
		return "esc"
	case "\x1b[Z":
		return "shift+tab"
	case "\x1b[A":
		return "up"
	case "\x1b[B":
		return "down"
	case "\x1b[C":
		return "right"
	case "\x1b[D":
		return "left"
	case "\x1b[3~":
		return "delete"
	case "\x1b[5~":
		return "pgup"
	case "\x1b[6~":
		return "pgdown"
	case "\x1b[1;3A", "\x1b\x1b[A":
		return "alt+up"
	case "\x1b[1;3B", "\x1b\x1b[B":
		return "alt+down"
	case " ":
		return "space"
	}
	if len(sequence) == 1 && sequence[0] > 0 && sequence[0] < 0x20 {
		return fmt.Sprintf("ctrl+%c", 'a'+sequence[0]-1)
	}
	if strings.HasPrefix(sequence, "\x1b") && len(sequence) > 1 {
		rest := strings.TrimPrefix(sequence, "\x1b")
		if len([]rune(rest)) == 1 {
			return "alt+" + rest
		}
	}
	return sequence
}
