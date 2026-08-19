package tui

import (
	"strings"

	"github.com/marcus/tasks/internal/record"
	"github.com/marcus/tasks/internal/tui/term/shortcuts"
)

// ModalContent is a built overlay body: what a Modal is constructed from.
type ModalContent struct {
	Title              string
	Lines              []string
	FilterGroups       []string
	FilterContextLines []bool
}

// helpGroups is the order the help modal shows the contexts in — the task list
// first, because that is where a reader of the help almost always is.
var helpGroups = []struct {
	Title   string
	Context shortcuts.Context
}{
	{"in the task list", shortcuts.List},
	{"in task details", shortcuts.Detail},
	{"while editing a task", shortcuts.TaskEdit},
	{"in a modal", shortcuts.Modal},
	{"everywhere", shortcuts.Global},
}

// HelpContent is the `?` overlay, generated ENTIRELY from the shortcut
// registry, for the delegation mode vocabulary this store enforces.
//
// The vocabulary is a parameter because the registry cannot hold one: it is
// built during init, before any store exists. The overlay is where a user goes
// to LEARN the keys, so quoting the built-in modes here while the `D` prompt
// and the delegate refusal quoted the configured ones would make the two places
// a user looks disagree about the same word.
//
// Generating it is the whole point: a hand-written help list is a second source
// of truth that goes stale the first time a binding moves, and a task list whose
// help lies about its keys is worse than one with no help at all.
//
// The filter groups are the section titles, so filtering the modal keeps a
// matched binding's heading visible — a bare "x archive" with no section above
// it does not tell you where the key works.
func HelpContent(styler Styler, modes record.ModeVocabulary) ModalContent {
	keyWidth := 0
	for _, entry := range shortcuts.Registry {
		if entry.HideInHelp {
			continue
		}
		key := entry.DisplayKey
		if entry.HelpDisplayKey != "" {
			key = entry.HelpDisplayKey
		}
		if width := len([]rune(key)); width > keyWidth {
			keyWidth = width
		}
	}
	lines := []string{}
	groups := []string{}
	contextLines := []bool{}
	add := func(group, line string, contextLine bool) {
		lines = append(lines, line)
		groups = append(groups, group)
		contextLines = append(contextLines, contextLine)
	}
	for index, group := range helpGroups {
		entries, err := shortcuts.Entries(group.Context, false)
		if err != nil {
			continue
		}
		if index > 0 {
			add(group.Title, "", true)
		}
		add(group.Title, styler.Paint("section", group.Title), true)
		for _, entry := range shortcuts.WithModesAll(entries, modes) {
			if entry.HideInHelp {
				continue
			}
			key, description := entry.DisplayKey, entry.Description
			if entry.HelpDisplayKey != "" {
				key = entry.HelpDisplayKey
			}
			if entry.HelpDescription != "" {
				description = entry.HelpDescription
			}
			add(group.Title, styler.Paint("accent", padRunes(key, keyWidth))+" "+description, false)
		}
	}
	add("everywhere", "", true)
	add("everywhere", styler.Paint("muted",
		"prompt/quick-form input: return submits · esc cancels · ctrl-a/e/b/f move"), false)
	return ModalContent{
		Title: "keyboard shortcuts", Lines: lines, FilterGroups: groups,
		FilterContextLines: contextLines,
	}
}

// padRunes is String#ljust in cells. Display keys are ASCII in the registry, so
// rune count and cell count agree; measuring in runes keeps this independent of
// the styler, which has not painted the key yet at this point.
func padRunes(text string, width int) string {
	if pad := width - len([]rune(text)); pad > 0 {
		return text + strings.Repeat(" ", pad)
	}
	return text
}
