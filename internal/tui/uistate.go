package tui

import (
	"fmt"

	"github.com/marcus/tasks/internal/tui/term/input"
)

// ModeModalFilter is the live line filter inside a filterable modal. It is a
// mode of its own rather than a flag on ModeModal because the key routing is
// genuinely different: while it is active, `j` types a letter instead of
// scrolling.
const ModeModalFilter Mode = "modal_filter"

// modeTransitions is the legal move set, exactly as lib/tui/ui_state.rb
// declares it.
//
// This table is the reason an overlay cannot be half-open. Every mode change
// goes through SetMode, so there is no path where the mode says "form" and the
// form is nil — which is what would produce a screen the keyboard does nothing
// to.
var modeTransitions = map[Mode][]Mode{
	ModeList:           {ModeList, ModePrompt, ModeFilter, ModeModal, ModeForm, ModeFieldModal, ModePalette, ModeContextPalette, ModeLinkPicker, ModeTaskEdit},
	ModePrompt:         {ModePrompt, ModeList, ModeModal},
	ModeFilter:         {ModeFilter, ModeList},
	ModeModal:          {ModeModal, ModeList, ModeModalFilter, ModeForm, ModeFieldModal, ModePalette, ModeContextPalette},
	ModeModalFilter:    {ModeModalFilter, ModeModal, ModeList},
	ModeForm:           {ModeForm, ModeList, ModeModal},
	ModeFieldModal:     {ModeFieldModal, ModeList, ModeModal},
	ModePalette:        {ModePalette, ModeList, ModeModal},
	ModeContextPalette: {ModeContextPalette, ModeList, ModeModal},
	ModeLinkPicker:     {ModeLinkPicker, ModeList},
	ModeTaskEdit:       {ModeTaskEdit, ModeList},
}

// ErrIllegalTransition is what SetMode reports for a move the table forbids.
type ErrIllegalTransition struct{ From, To Mode }

func (e ErrIllegalTransition) Error() string {
	return fmt.Sprintf("illegal TUI transition: %s -> %s", e.From, e.To)
}

// Mode is the current interaction mode.
func (m *Model) Mode() Mode { return m.mode }

// SetMode moves to a mode, refusing a move the table forbids or whose overlay
// is missing.
//
// The overlay checks are not defensive noise. Ruby raises here, and it raises
// because every one of these has been reachable: a modal dismissed by an
// external refresh while a palette opened over it, a form whose return mode
// points at a modal that is gone. Failing loudly at the transition is what
// keeps those from becoming a frozen screen.
func (m *Model) SetMode(target Mode) error {
	allowed := modeTransitions[m.mode]
	legal := false
	for _, candidate := range allowed {
		if candidate == target {
			legal = true
			break
		}
	}
	if !legal {
		return ErrIllegalTransition{From: m.mode, To: target}
	}
	switch target {
	case ModeModal, ModeModalFilter:
		if m.modal == nil {
			return fmt.Errorf("%s mode requires a modal", target)
		}
		if target == ModeModalFilter && !m.modal.Filterable() {
			return fmt.Errorf("modal_filter mode requires a filterable modal")
		}
	case ModeForm:
		if m.form == nil {
			return fmt.Errorf("form mode requires a form")
		}
		if m.form.ReturnMode == ReturnModal && m.modal == nil {
			return fmt.Errorf("form returning to modal requires a retained modal")
		}
	case ModeFieldModal:
		if m.fieldModal == nil {
			return fmt.Errorf("field_modal mode requires a field modal")
		}
		if m.fieldModal.ReturnMode() == ReturnModal && m.modal == nil {
			return fmt.Errorf("field modal returning to modal requires a retained modal")
		}
	case ModePalette:
		if m.actionPalette == nil {
			return fmt.Errorf("palette mode requires an action palette")
		}
		if m.actionPalette.ReturnMode() == ReturnModal && m.modal == nil {
			return fmt.Errorf("palette returning to modal requires a retained modal")
		}
	case ModeContextPalette:
		if m.contextPalette == nil {
			return fmt.Errorf("context_palette mode requires a context palette")
		}
	case ModeLinkPicker:
		if m.linkPicker == nil {
			return fmt.Errorf("link_picker mode requires a link picker")
		}
	case ModeTaskEdit:
		if m.taskEditor == nil {
			return fmt.Errorf("task_edit mode requires a task editor")
		}
	}
	m.mode = target
	return nil
}

// -- overlay lifetimes ------------------------------------------------------------
//
// Removing an overlay can happen after an external file reload, so none of
// these may leave the mode pointing at an object that no longer exists.

// SetModal opens or clears the modal overlay.
func (m *Model) SetModal(modal *Modal) {
	if modal != nil && m.mode == ModeModalFilter && !modal.Filterable() {
		// Replacing a filtered modal with an unfilterable one would strand the
		// keyboard in a mode the new modal cannot answer.
		return
	}
	m.modal = modal
	if modal != nil {
		return
	}
	switch {
	case m.mode == ModeForm && m.form != nil && m.form.ReturnMode == ReturnModal:
		m.form = nil
		m.mode = ModeList
	case m.mode == ModeFieldModal && m.fieldModal != nil && m.fieldModal.ReturnMode() == ReturnModal:
		m.fieldModal = nil
		m.mode = ModeList
	case m.mode == ModePalette && m.actionPalette != nil && m.actionPalette.ReturnMode() == ReturnModal:
		m.actionPalette = nil
		m.mode = ModeList
	case m.mode == ModeModal || m.mode == ModeModalFilter:
		m.mode = ModeList
	}
}

// Modal is the open overlay, or nil.
func (m *Model) Modal() *Modal { return m.modal }

// SetForm opens or clears the quick-form popup.
func (m *Model) SetForm(form *QuickForm) {
	if form != nil && form.ReturnMode == ReturnModal && m.modal == nil {
		return
	}
	m.form = form
	if form == nil && m.mode == ModeForm {
		m.mode = ModeList
	}
}

// Form is the open quick form, or nil.
func (m *Model) Form() *QuickForm { return m.form }

// SetActionPalette opens or clears the action palette.
func (m *Model) SetActionPalette(palette *ActionPalette) {
	if palette != nil && palette.ReturnMode() == ReturnModal && m.modal == nil {
		return
	}
	m.actionPalette = palette
	if palette == nil && m.mode == ModePalette {
		m.mode = ModeList
	}
}

// ActionPalette is the open action palette, or nil.
func (m *Model) ActionPalette() *ActionPalette { return m.actionPalette }

// SetContextPalette opens or clears the context palette.
func (m *Model) SetContextPalette(palette *ContextPalette) {
	m.contextPalette = palette
	if palette == nil && m.mode == ModeContextPalette {
		m.mode = ModeList
	}
}

// ContextPalette is the open context palette, or nil.
func (m *Model) ContextPalette() *ContextPalette { return m.contextPalette }

// SetLinkPicker opens or clears the task-link picker.
func (m *Model) SetLinkPicker(picker *LinkPicker) {
	m.linkPicker = picker
	if picker == nil && m.mode == ModeLinkPicker {
		m.mode = ModeList
	}
}

// LinkPicker is the open task-link picker, or nil.
func (m *Model) LinkPicker() *LinkPicker { return m.linkPicker }

// SetTaskEditor opens or clears the durable task editor.
func (m *Model) SetTaskEditor(editor *TaskEditorSession) {
	m.taskEditor = editor
	if editor == nil && m.mode == ModeTaskEdit {
		m.mode = ModeList
	}
}

// TaskEditor is the open editor session, or nil.
func (m *Model) TaskEditor() *TaskEditorSession { return m.taskEditor }

// modalFilterEditor is the buffer behind the modal's `/` line, created lazily so
// a model built without one still works.
func (m *Model) modalFilterEditor() *input.Editor {
	if m.modalFilterInput == nil {
		m.modalFilterInput = input.New("", input.Options{})
	}
	return m.modalFilterInput
}

// ModalFilterInput is the text in the modal's filter line.
func (m *Model) ModalFilterInput() string { return m.modalFilterEditor().Text() }

// filterEditor is the persistent buffer behind the list's `/` filter. Keeping
// the editor (rather than rebuilding one from its text for every key) preserves
// cursor movement across Bubble Tea Update calls.
func (m *Model) filterEditor() *input.Editor {
	if m.filterInput == nil {
		m.filterInput = input.New("", input.Options{})
	}
	return m.filterInput
}

// ContextFilters is the active `@` filter set.
func (m *Model) ContextFilters() []string { return append([]string{}, m.contextFilters...) }
