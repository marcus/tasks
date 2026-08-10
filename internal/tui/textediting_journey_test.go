package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func updateAlt(h *modelHarness, code rune) {
	h.model.Update(tea.KeyPressMsg{Code: code, Mod: tea.ModAlt})
}

func typeThroughUpdate(h *modelHarness, text string) {
	for _, r := range text {
		h.model.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestOptionWordEditingThroughPromptAndListFilter(t *testing.T) {
	prompt := newAgentHarness(t, scripted("done", true))
	prompt.pressKeys("\t")
	typeThroughUpdate(prompt.modelHarness, "alpha beta")
	updateAlt(prompt.modelHarness, tea.KeyLeft)
	prompt.model.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if got := prompt.model.PromptText(); got != "alpha Xbeta" {
		t.Fatalf("prompt after option-left = %q", got)
	}
	prompt.model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModAlt})
	if got := prompt.model.PromptText(); got != "alpha beta" {
		t.Fatalf("prompt after option-backspace = %q", got)
	}

	filter := newModelHarness(t, harnessOptions{})
	filter.pressKeys("/")
	typeThroughUpdate(filter, "alpha beta")
	updateAlt(filter, tea.KeyLeft)
	filter.model.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if got := filter.model.filterEditor().Text(); got != "alpha Xbeta" {
		t.Fatalf("filter after persistent option-left = %q", got)
	}
}

func TestOptionWordEditingThroughQuickFormAndTaskEditor(t *testing.T) {
	quick := newModelHarness(t, harnessOptions{})
	quick.selectRowByID(fixFlight)
	quick.pressKeys("d")
	typeThroughUpdate(quick, "alpha beta")
	updateAlt(quick, tea.KeyLeft)
	quick.model.Update(tea.KeyPressMsg{Code: tea.KeyDelete, Mod: tea.ModAlt})
	if got := quick.model.Form().Text(); got != "alpha " {
		t.Fatalf("quick form after option-right-delete = %q", got)
	}

	task := newEditorHarness(t, fixFlight, "title")
	typeThroughUpdate(task.modelHarness, " alpha beta")
	updateAlt(task.modelHarness, tea.KeyLeft)
	task.model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModAlt})
	if got := task.editor.Form().Value("title"); got != "Book flight in Concur beta" {
		t.Fatalf("task editor after option-backspace = %#v", got)
	}
}
