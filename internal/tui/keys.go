package tui

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/tasks/internal/tui/term/clipboard"
	"github.com/marcus/tasks/internal/tui/term/input"
	"github.com/marcus/tasks/internal/tui/term/shortcuts"
)

// handleKey is the whole keyboard surface.
//
// Ruby decodes raw bytes itself — CSI parsing, an escape-versus-alt timing
// heuristic, bracketed paste framing, a UTF-8 continuation buffer. None of that
// is ported: Bubble Tea delivers a decoded KeyPressMsg, and re-deriving it would
// be re-solving a solved problem in a place where a bug shows up as a corrupted
// screen. What IS ported is which key does what — and, because the shortcut
// registry is keyed by the byte sequences a terminal sends, the FIRST thing
// this does is turn a decoded message back into that sequence so one registry
// serves the help modal, the palette and live dispatch alike.
//
// Bracketed paste arrives separately as tea.PasteMsg (see Model.Update).
func (m *Model) handleKey(message tea.KeyPressMsg) tea.Cmd {
	sequence := KeySequence(message)
	if sequence == "" {
		return nil
	}

	// A dirty draft's quit confirmation outranks everything: it is a question
	// the user has been asked and has not answered.
	editor := m.taskEditor
	if editor == nil {
		editor = m.suspendedTaskEditor
	}
	if editor != nil && editor.PendingQuit() {
		return m.modalConfirmationKey(sequence)
	}
	if m.agentQuitPending {
		return m.modalConfirmationKey(sequence)
	}
	// Global bindings (ctrl-c) reach through every mode, so a wedged overlay
	// can always be escaped.
	if m.dispatchAction(sequence, shortcuts.Global) {
		return m.maybeQuit()
	}
	if m.mode == ModeTaskEdit && m.taskEditor != nil {
		if !m.dispatchAction(sequence, shortcuts.TaskEdit) {
			m.taskEditInputKey(sequence)
		}
		return nil
	}
	if m.mode == ModeList && m.suspendedTaskEditor != nil &&
		m.panel != nil && m.panel.Kind == PanelSuspendedTaskEdit {
		switch sequence {
		case "e":
			if m.suspendedTaskEditor.Missing() {
				m.showSuspendedEditorPanel()
				m.Flash("Task no longer exists; local field retained for copy or discard")
				return nil
			}
			if !m.selectedTarget(m.suspendedTaskEditor.TargetID()) {
				m.showSuspendedEditorPanel()
				m.Flash("paused task draft: switch to its task to resume · y copies · esc discards")
				return nil
			}
			m.StartTaskEdit("")
			return nil
		case "y":
			m.copyMissingEditorField()
			return nil
		case "\x1b":
			m.suspendedTaskEditor = nil
			m.suspendedTaskPanel = nil
			m.panel = nil
			m.editorMessage = ""
			m.Flash("discarded local draft for paused task")
			return nil
		}
	}

	switch m.mode {
	case ModeForm:
		if !m.dispatchAction(sequence, shortcuts.Form) {
			m.formKey(sequence)
		}
	case ModePalette:
		if !m.dispatchAction(sequence, shortcuts.Picker) {
			m.paletteKey(sequence)
		}
	case ModeContextPalette:
		if !m.dispatchAction(sequence, shortcuts.ContextPicker) {
			m.contextPaletteKey(sequence)
		}
	case ModeModal:
		m.modalKey(sequence)
	case ModeModalFilter:
		if !m.dispatchAction(sequence, shortcuts.ModalFilter) {
			m.modalFilterKey(sequence)
		}
	case ModeFilter:
		if !m.dispatchAction(sequence, shortcuts.Filter) {
			m.filterKey(sequence)
		}
	case ModePrompt:
		if !m.dispatchAction(sequence, shortcuts.Prompt) {
			m.promptKey(sequence)
		}
	default:
		m.listKey(sequence)
	}
	return m.maybeQuit()
}

// handlePaste routes Bubble Tea's bracketed-paste event before shortcut
// conversion. Pasted `q`, Escape-looking bytes, and newlines are data, never
// commands; every destination uses its own editor sanitizer.
func (m *Model) handlePaste(text string) {
	switch m.mode {
	case ModePrompt:
		m.promptEditor().Insert(text)
	case ModeForm:
		if m.form != nil {
			m.form.Paste(text)
		}
	case ModeTaskEdit:
		if m.taskEditor != nil {
			m.processEditorOutcome(m.taskEditor.Paste(text))
		}
	case ModePalette:
		if m.actionPalette != nil {
			m.resolvePaletteOutcome(m.actionPalette, m.actionPalette.Paste(text))
		}
	case ModeContextPalette:
		if m.contextPalette != nil {
			m.applyContextOutcome(m.contextPalette.Paste(text))
		}
	case ModeFilter:
		if m.filterEditor().Insert(text) == input.Changed {
			m.RefreshRows()
		}
	case ModeModalFilter:
		if m.modalFilterEditor().Insert(text) == input.Changed {
			m.modal.SetFilter(m.modalFilterEditor().Text())
		}
	default:
		m.PromptPaste(text)
	}
}

func (m *Model) maybeQuit() tea.Cmd {
	if m.quitting {
		return tea.Quit
	}
	return nil
}

func (m *Model) finishQuit() {
	m.Save()
	if !m.embedded {
		m.quitting = true
		return
	}
	// Embedded quit LATCHES rather than terminating, whether or not the host
	// suppressed Tasks' own quit chrome. Refusing the action outright made
	// QuitRequested/ClearQuitRequest unreachable under SuppressQuit, which is
	// exactly the configuration a host that owns quit runs in.
	m.quitRequested = true
}

// KeySequence turns a decoded Bubble Tea v2 key press into the raw byte
// sequence the shortcut registry and the text editor are both keyed by.
//
// It is exported because the differential harness drives the model with the
// SAME sequences it feeds Ruby's key handler, which is the only way the two
// interaction traces are comparable. An external Bubble Tea v2 host can pass
// tea.KeyPressMsg values straight into Update without translation.
func KeySequence(message tea.KeyPressMsg) string {
	// Printable characters: Text is set for shifted/unshifted glyphs ("a",
	// "A", "!", …). Ctrl/Alt combos leave Text empty.
	if message.Text != "" {
		if message.Mod&tea.ModAlt != 0 {
			return "\x1b" + message.Text
		}
		return message.Text
	}

	// Ctrl+letter → C0 control byte (ctrl-a = 0x01 … ctrl-z = 0x1a).
	// Unbound ctrl chords must return "" immediately: falling through to the
	// printable/special paths would turn ctrl-z into "z" (defer), ctrl-x into
	// "x" (archive), ctrl-q into "q" (quit). v1 only mapped explicit KeyCtrl*
	// entries and ignored the rest.
	if message.Mod&tea.ModCtrl != 0 {
		return ctrlSequence(message.Code)
	}

	// Shift+Tab is the only shifted special the registry binds. Require the
	// exact modifier set: Shift+Alt+Tab and friends were unmapped in v1.
	if message.Code == tea.KeyTab && message.Mod == tea.ModShift {
		return "\x1b[Z"
	}

	if named, found := keyCodeSequences[message.Code]; found {
		// v1 gave each modified special its own KeyType (KeyShiftUp, …) and
		// only mapped the bare keys plus alt-↑/alt-↓. Dropping unsupported
		// modifiers here would turn Shift+Up into plain Up and move selection.
		switch message.Mod {
		case 0:
			return named
		case tea.ModAlt:
			switch named {
			case "\x1b[A", "\x1b[B", "\x1b[C", "\x1b[D":
				// xterm's Alt modifier is 3. Up/down remain list reorder
				// bindings; left/right are standard word navigation in editors.
				return strings.Replace(named, "\x1b[", "\x1b[1;3", 1)
			case "\x7f":
				// macOS terminals encode Option-Backspace as Meta-DEL.
				return "\x1b\x7f"
			case "\x1b[3~":
				return "\x1b[3;3~"
			default:
				return ""
			}
		default:
			return ""
		}
	}

	// Fallback: a lone printable Code with empty Text (e.g. space via KeySpace
	// when the host did not populate Text). Only bare and Alt-only forms are
	// accepted — same modifier discipline as the specials path.
	if message.Code > 0 && message.Code < unicode.MaxASCII && unicode.IsPrint(message.Code) {
		text := string(message.Code)
		switch message.Mod {
		case 0:
			return text
		case tea.ModAlt:
			return "\x1b" + text
		default:
			return ""
		}
	}
	return ""
}

// ctrlSequence maps a key Code under ModCtrl to the C0 byte the registry uses.
// Only the letters the registry (or editor) actually binds are produced.
func ctrlSequence(code rune) string {
	letter := code
	if letter >= 'A' && letter <= 'Z' {
		letter = letter - 'A' + 'a'
	}
	if letter < 'a' || letter > 'z' {
		return ""
	}
	ctrl := byte(letter - 'a' + 1)
	if _, bound := ctrlBound[ctrl]; !bound {
		return ""
	}
	return string([]byte{ctrl})
}

// ctrlBound is the set of ctrl letters the v1 keyTypeSequences table exposed.
// Unlisted ctrl combos stay ignored rather than inventing new bindings.
var ctrlBound = map[byte]struct{}{
	0x01: {}, // ctrl-a
	0x02: {}, // ctrl-b
	0x03: {}, // ctrl-c
	0x04: {}, // ctrl-d
	0x05: {}, // ctrl-e
	0x06: {}, // ctrl-f
	0x08: {}, // ctrl-h
	0x0b: {}, // ctrl-k
	0x0c: {}, // ctrl-l
	0x0e: {}, // ctrl-n
	0x0f: {}, // ctrl-o
	0x10: {}, // ctrl-p
	0x12: {}, // ctrl-r
	0x13: {}, // ctrl-s
	0x15: {}, // ctrl-u
	0x17: {}, // ctrl-w
}

// keyCodeSequences maps Bubble Tea v2 key Codes onto terminal bytes. Only the
// keys the registry or the editor actually bind are listed; anything else is
// deliberately unmapped and therefore ignored rather than guessed at.
//
// Shift+Tab and Ctrl+letter are handled in KeySequence via modifiers, not here.
var keyCodeSequences = map[rune]string{
	tea.KeyEnter:     "\r",
	tea.KeyEsc:       "\x1b",
	tea.KeyTab:       "\t",
	tea.KeySpace:     " ",
	tea.KeyBackspace: "\x7f",
	tea.KeyDelete:    "\x1b[3~",
	tea.KeyUp:        "\x1b[A",
	tea.KeyDown:      "\x1b[B",
	tea.KeyRight:     "\x1b[C",
	tea.KeyLeft:      "\x1b[D",
	tea.KeyHome:      "\x1b[H",
	tea.KeyEnd:       "\x1b[F",
	tea.KeyPgUp:      "\x1b[5~",
	tea.KeyPgDown:    "\x1b[6~",
}

// keyMessage is the inverse of KeySequence: it turns a registry byte sequence
// back into the KeyPressMsg Bubble Tea v2 would deliver. Shared by tests.
func keyMessage(sequence string) tea.KeyPressMsg {
	switch sequence {
	case "\x1b[Z":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "\x1b[1;3A":
		return tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}
	case "\x1b[1;3B":
		return tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt}
	}
	for code, bytes := range keyCodeSequences {
		if bytes == sequence {
			return tea.KeyPressMsg{Code: code}
		}
	}
	if len(sequence) == 1 {
		b := sequence[0]
		if _, bound := ctrlBound[b]; bound {
			return tea.KeyPressMsg{Code: rune('a' + b - 1), Mod: tea.ModCtrl}
		}
	}
	if strings.HasPrefix(sequence, "\x1b") && len(sequence) > 1 {
		rest := sequence[1:]
		// CSI / SS3 sequences are not alt+printable.
		if rest != "" && rest[0] != '[' && rest[0] != 'O' {
			r, _ := utf8.DecodeRuneInString(rest)
			if r != utf8.RuneError {
				return tea.KeyPressMsg{Code: r, Text: rest, Mod: tea.ModAlt}
			}
		}
	}
	if sequence == "" {
		return tea.KeyPressMsg{}
	}
	r, _ := utf8.DecodeRuneInString(sequence)
	return tea.KeyPressMsg{Code: r, Text: sequence}
}

// dispatchAction looks one sequence up in a context and runs it.
//
// A matched-but-unavailable action CONSUMES its key and says why. That is the
// rule that keeps `a` from meaning "approve" on a task and silently meaning
// something else on a row where approval does not apply.
func (m *Model) dispatchAction(sequence string, context shortcuts.Context) bool {
	entry, found := shortcuts.Match(sequence, context, m.availability)
	if !found {
		return false
	}
	if entry.Availability != "" && !m.availability(entry.Availability) {
		m.refuseUnavailable(entry)
		return true
	}
	if handler, present := m.handlers()[entry.Handler]; present {
		handler(sequence)
		return true
	}
	if reason, named := unbuiltHandlers[entry.Handler]; named {
		m.Flash(reason)
		return true
	}
	// A registry entry with no handler and no recorded reason is a porting
	// omission, not a user error — say so rather than swallowing the key.
	m.Flash("“" + entry.Description + "” has no handler in this build")
	return true
}

// refuseUnavailable is Ruby's `unavailable_action`, and it is deliberately
// NARROW: only the ordering and delegation families explain themselves.
//
// The rest are consumed in silence on purpose. Those keys are unavailable
// because of what is selected, which the user can see; a flash on every one
// would turn ordinary navigation into a stream of notifications. The two that
// DO speak are the ones whose unavailability is not visible on the row —
// "you are not in Outline" and "this task's state cannot be delegated".
func (m *Model) refuseUnavailable(entry shortcuts.Entry) {
	switch entry.Handler {
	case "move_subtree_up", "move_subtree_down", "indent_subtree", "outdent_subtree":
		m.Flash("ordering requires the unfiltered Outline tab")
	case "delegate_selected", "set_work_ref_selected":
		m.refuseDelegation()
	}
}

// refuseDelegation is Ruby's `unavailable_delegation`: a consumed delegation key
// still owes the reader a reason, because silently swallowing D on a proposal
// reads as a broken keyboard.
func (m *Model) refuseDelegation() {
	if m.CurrentProject() != nil {
		m.needsTask()
		return
	}
	item := m.CurrentItem()
	if item == nil {
		m.Flash("nothing selected")
		return
	}
	if isProposedState(item.State) {
		m.Flash("approve the proposal first \u2014 a proposal can't be delegated")
		return
	}
	m.Flash(strings.ToLower(item.State) + " tasks can't be delegated")
}

// listKey tries the detail context first when the panel is open, so `e` edits
// the task whose details are showing rather than renaming a project.
func (m *Model) listKey(sequence string) {
	if m.panel != nil && m.panel.Kind == PanelDetail && m.dispatchAction(sequence, shortcuts.Detail) {
		return
	}
	if m.dispatchAction(sequence, shortcuts.List) {
		return
	}
	if extra, present := extraListKeys[sequence]; present {
		extra(m)
	}
}

// extraListKeys are bindings this build has that the Ruby registry does not.
//
// They are kept OUT of the registry on purpose: the registry is the shared
// contract that generates help and the palette, and inventing entries in it
// would make the Go help modal advertise keys the Ruby one does not have. These
// two are pure navigation, they mutate nothing, and vi users reach for them.
var extraListKeys = map[string]func(*Model){
	"g": (*Model).selectFirst,
	"G": (*Model).selectLast,
}

// -- the filter line ----------------------------------------------------------------

func (m *Model) filterKey(sequence string) {
	switch sequence {
	case "\x1b":
		// Escape clears the filter ENTIRELY rather than reverting the edit: a
		// user pressing escape in a search box wants the search gone.
		m.filter = ""
		m.filterEditor().Clear()
		m.mode = ModeList
		m.RefreshRows()
	case "\r", "\n":
		m.filter = strings.TrimSpace(m.filterEditor().Text())
		m.filterEditor().Clear()
		m.mode = ModeList
		m.RefreshRows()
	default:
		if m.filterEditor().HandleKey(sequence) == input.Changed {
			m.RefreshRows()
		}
	}
}

// -- modals -------------------------------------------------------------------------

func (m *Model) modalKey(sequence string) {
	if m.modal == nil {
		m.mode = ModeList
		return
	}
	entry, bound := shortcuts.Match(sequence, shortcuts.Modal, m.availability)
	if bound && m.availability(entry.Availability) && m.dispatchAction(sequence, shortcuts.Modal) {
		return
	}
	if m.modalKeyStartsTyping(sequence) {
		m.ModalStartFilter()
		m.modalFilterKey(sequence)
		return
	}
}

func (m *Model) modalConfirmationAvailable() bool {
	if m.modal == nil {
		return false
	}
	switch m.modal.Kind() {
	case ModalProjectCompleteConfirm, ModalProjectArchiveConfirm,
		ModalArchiveConfirm, ModalDeleteConfirm, ModalDeleteCascadeConfirm,
		ModalAgentQueueCancel, ModalTaskDraftQuitConfirm, ModalAgentQuitConfirm:
		return true
	default:
		return false
	}
}

func (m *Model) modalConfirmationAcceptsEnter() bool {
	if m.modal == nil {
		return false
	}
	switch m.modal.Kind() {
	case ModalProjectCompleteConfirm, ModalProjectArchiveConfirm,
		ModalAgentQueueCancel, ModalTaskDraftQuitConfirm, ModalAgentQuitConfirm:
		return true
	default:
		return false
	}
}

// modalConfirmationKey is the one semantic path used by terminal keys and
// host command invocation. Individual modal kinds retain their established
// confirmation behavior, including which of them accept Return.
func (m *Model) modalConfirmationKey(sequence string) tea.Cmd {
	editor := m.taskEditor
	if editor == nil {
		editor = m.suspendedTaskEditor
	}
	if editor != nil && editor.PendingQuit() {
		return m.taskDraftQuitKey(sequence)
	}
	if m.agentQuitPending {
		return m.agentQuitKey(sequence)
	}
	if m.modal == nil {
		return nil
	}
	switch m.modal.Kind() {
	case ModalProjectCompleteConfirm:
		m.projectCompleteConfirmKey(sequence)
	case ModalProjectArchiveConfirm:
		m.projectArchiveConfirmKey(sequence)
	case ModalArchiveConfirm:
		m.archiveConfirmKey(sequence)
	case ModalDeleteConfirm, ModalDeleteCascadeConfirm:
		m.deleteConfirmKey(sequence)
	case ModalAgentQueueCancel:
		m.agentQueueCancelKey(sequence)
	case ModalTaskDraftQuitConfirm:
		return m.taskDraftQuitKey(sequence)
	case ModalAgentQuitConfirm:
		return m.agentQuitKey(sequence)
	}
	return nil
}

// modalKeyStartsTyping: typing a character with no modal binding of its own
// opens the live filter immediately, so `/` is only needed to resume editing an
// already-committed filter. j/k/q/? stay reserved for scrolling and closing.
func (m *Model) modalKeyStartsTyping(sequence string) bool {
	if m.modal == nil || !m.modal.Filterable() {
		return false
	}
	if entry, bound := shortcuts.Match(sequence, shortcuts.Modal, m.availability); bound && m.availability(entry.Availability) {
		return false
	}
	return input.Printable(sequence)
}

func (m *Model) modalFilterKey(sequence string) {
	if m.modal == nil {
		m.mode = ModeList
		return
	}
	switch sequence {
	case "\x1b":
		m.modal.SetFilter("")
		m.modalFilterEditor().Clear()
		m.mode = ModeModal
	case "\r", "\n":
		// The filter applied live; Enter simply stops typing and keeps it.
		m.mode = ModeModal
	default:
		if m.modalFilterEditor().HandleKey(sequence) == input.Changed {
			m.modal.SetFilter(m.modalFilterEditor().Text())
		}
	}
}

// -- the quick form ------------------------------------------------------------------

func (m *Model) formKey(sequence string) {
	if m.form == nil {
		m.mode = ModeList
		return
	}
	switch m.form.HandleKey(sequence) {
	case QuickFormCancelled:
		m.CloseForm(false)
	case QuickFormSubmitted:
		m.CloseForm(true)
	}
}

// -- the palettes ---------------------------------------------------------------------

func (m *Model) paletteKey(sequence string) {
	palette := m.actionPalette
	if palette == nil {
		m.mode = ModeList
		return
	}
	m.resolvePaletteOutcome(palette, palette.HandleKey(sequence))
}

// resolvePaletteOutcome closes the palette BEFORE running the action, so an
// action that opens another overlay is not fighting a palette that is still
// nominally open.
func (m *Model) resolvePaletteOutcome(palette *ActionPalette, outcome PaletteOutcome) {
	switch outcome.Kind {
	case PickerCancelled:
		m.CloseActionPalette()
	case PickerAccepted:
		if !outcome.Execute {
			return
		}
		entry := outcome.Entry
		if m.read != nil && m.read.Stale() {
			m.Refresh()
			if m.actionPalette != palette {
				return
			}
		}
		if target := palette.TargetID(); target != "" && !m.selectedTarget(target) {
			m.actionPalette = nil
			m.mode = ModeList
			return
		}
		m.CloseActionPalette()
		if handler, present := m.handlers()[entry.Handler]; present {
			handler("")
			return
		}
		if reason, named := unbuiltHandlers[entry.Handler]; named {
			m.Flash(reason)
			return
		}
		m.restoreActionPalette(palette, "“"+entry.Description+"” has no handler in this build")
	}
}

// restoreActionPalette puts a palette back with an error on it — unless the
// task it was opened for is gone, in which case a detail-context command must
// NOT survive and act on the fallback selection.
func (m *Model) restoreActionPalette(palette *ActionPalette, message string) {
	if palette == nil {
		if message != "" {
			m.Flash(message)
		}
		return
	}
	currentID := ""
	if item := m.CurrentItem(); item != nil {
		currentID = item.ID
	}
	targetMissing := palette.TargetID() != "" && currentID != palette.TargetID()
	if targetMissing || (palette.ReturnMode() == ReturnModal && m.modal == nil) {
		m.actionPalette = nil
		if message != "" {
			m.Flash(message)
		}
		return
	}
	m.actionPalette = palette
	m.mode = ModePalette
	if message != "" {
		palette.Fail(message)
	}
}

func (m *Model) contextPaletteKey(sequence string) {
	palette := m.contextPalette
	if palette == nil {
		m.mode = ModeList
		return
	}
	// The key and the click share applyContextOutcome, so a pointer and a
	// keyboard can never apply different filter sets from the same picker.
	m.applyContextOutcome(palette.HandleKey(sequence))
}

// -- the task editor --------------------------------------------------------------------

// taskEditKey routes one key into the durable editor. Two keys are deliberately
// NOT the editor's: ctrl-k and ctrl-l resize the panel without saving, so a
// user can make room to read while a field is mid-edit.
func (m *Model) taskEditInputKey(sequence string) {
	editor := m.taskEditor
	if editor == nil {
		m.mode = ModeList
		return
	}
	switch sequence {
	case "\x0b":
		m.ResizePanel(2)
		return
	case "\x0c":
		m.ResizePanel(-2)
		return
	}
	if editor.Missing() {
		// A vanished task keeps the editor open just long enough to rescue what
		// was typed: `y` copies the focused value, escape discards it.
		switch sequence {
		case "y":
			m.copyMissingEditorField()
			return
		case "\x1b", editorCtrlO:
			m.CloseTaskEdit("Task no longer exists; local edit discarded")
			return
		}
	}
	m.processEditorOutcome(editor.HandleKey(sequence))
}

// processEditorOutcome routes one editor answer, in Ruby's order.
//
// The message goes to the EDITOR PANEL, not to the status line — the panel is
// where the user is looking, and a flash three seconds long is the wrong place
// for "press escape again to discard this field". Only the two outcomes that
// are about the TASK rather than the field — it vanished, or it changed
// underneath — also flash, because those matter even if the panel is not where
// the eye is.
func (m *Model) processEditorOutcome(outcome EditorOutcome) {
	m.editorMessage = outcome.Message
	switch outcome.Status {
	case EditorMissing, EditorConflicted:
		m.Flash(outcome.Message)
	case EditorConfirmation:
		m.editorMessage = outcome.Message + " \u00b7 y accepts \u00b7 n cancels"
	}

	if outcome.Wrote && m.taskEditor != nil {
		// The store moved; the list must show what it now says, still following
		// the task by its stable id.
		target := m.taskEditor.TargetID()
		m.selectedID = target
		m.Refresh()
		// A save can move a task OUT of the view it was edited in — putting a
		// task on hold removes it from Next. Leaving the editor open on a row
		// that is no longer there would strand the cursor, so the editor closes
		// and says both what happened and where the selection went.
		if !m.rowsContain(target) {
			explanation := "Saved; task left the " + m.view + " view"
			if destination := m.CurrentItem(); destination != nil {
				explanation += " \u00b7 selected " + destination.Title
			}
			// Ruby closes the panel when the edited target is no longer
			// selectable. Leaving the replacement row's detail open makes it
			// look as though the edit moved to a different task.
			m.panel = nil
			m.CloseTaskEdit(explanation)
			return
		}
	}

	if outcome.Status == EditorFinished {
		m.CloseTaskEdit(outcome.Message)
	}
}

func (m *Model) rowsContain(id string) bool {
	for _, row := range m.rows {
		if row.Item != nil && row.Item.ID == id {
			return true
		}
	}
	return false
}

func (m *Model) copyMissingEditorField() {
	value := ""
	editor := m.taskEditor
	if editor == nil {
		editor = m.suspendedTaskEditor
	}
	if editor != nil {
		value = valueText(editor.CopyValue())
	}
	if value == "" {
		m.Flash("nothing to copy")
		return
	}
	if command := clipboard.Command(); command == nil || !clipboard.Copy(value, command) {
		m.Flash("no clipboard tool found (pbcopy/wl-copy/xclip/xsel)")
		return
	}
	m.Flash("copied the unsaved value")
}

// taskDraftQuitKey answers the "discard your unsaved draft?" question.
func (m *Model) taskDraftQuitKey(sequence string) tea.Cmd {
	editor := m.taskEditor
	if editor == nil {
		editor = m.suspendedTaskEditor
	}
	if editor == nil {
		m.clearQuitConfirmation(true)
		return nil
	}
	outcome := editor.HandleQuitConfirmation(sequence)
	switch outcome.Status {
	case EditorQuitConfirmed:
		m.clearQuitConfirmation(false)
		if m.taskEditor == editor {
			m.taskEditor = nil
		}
		if m.suspendedTaskEditor == editor {
			m.suspendedTaskEditor = nil
			m.suspendedTaskPanel = nil
		}
		if m.queueHasWork() {
			m.queue.Shutdown()
		}
		m.finishQuit()
		return m.maybeQuit()
	case EditorQuitCancelled:
		m.clearQuitConfirmation(true)
		m.Flash(outcome.Message)
	default:
		if sequence == "q" || sequence == "\x03" {
			m.Flash("confirmation still open — y/return discards and quits · n/esc keeps editing")
		}
	}
	return nil
}

func (m *Model) agentQuitKey(sequence string) tea.Cmd {
	switch sequence {
	case "y", "Y", "\r", "\n":
		m.clearQuitConfirmation(false)
		if m.queue != nil {
			m.queue.Shutdown()
		}
		m.finishQuit()
		return m.maybeQuit()
	case "n", "N", "\x1b":
		m.clearQuitConfirmation(true)
		m.Flash("quit cancelled — agent queue kept")
	case "q", "\x03":
		m.Flash("confirmation still open — y/return quits · n/esc keeps running")
	}
	return nil
}

func (m *Model) clearQuitConfirmation(restore bool) {
	retainedModal, retainedMode := m.quitReturnModal, m.quitReturnMode
	retainedMessage := m.quitReturnMessage
	m.quitReturnModal = nil
	m.quitReturnMode = ""
	m.quitReturnMessage = ""
	m.agentQuitPending = false
	if restore {
		m.modal = retainedModal
		m.mode = retainedMode
		m.editorMessage = retainedMessage
		return
	}
	m.modal = nil
	m.mode = ModeList
}

// -- selection helpers ---------------------------------------------------------------

func (m *Model) selectFirst() {
	if selectable := m.selectableIndexes(); len(selectable) > 0 {
		m.selectRow(selectable[0])
	}
}

func (m *Model) selectLast() {
	if selectable := m.selectableIndexes(); len(selectable) > 0 {
		m.selectRow(selectable[len(selectable)-1])
	}
}

func (m *Model) scrollPanel(direction int, half bool) {
	if m.panel == nil {
		return
	}
	height := m.layout().BodyHeight
	if half {
		m.panel.ScrollHalf(direction, height)
		return
	}
	m.panel.ScrollPage(direction, height)
}

// dismiss is Escape's ladder, in Ruby's order: the search filter, then the
// context filters, then the detail panel. The two rungs this build does not
// have — cancelling a running agent request, and dismissing the response pane —
// sit ABOVE these in Ruby and belong to the agent packet; they will be inserted
// at the top rather than changing the order of what is here.
//
// Escape never quits. Quitting is `q` or ctrl-c, so a reflex press cannot end
// the session with an editor open.
func (m *Model) dismiss() {
	switch {
	case m.queue != nil && m.queue.Active():
		event := m.queue.CancelActive()
		m.recordAgentResult(event.Request)
		m.Refresh()
		m.advanceQueue()
		m.Flash(fmt.Sprintf("cancelled agent request #%d", event.Request.ID))
	case m.respOpen:
		m.respOpen = false
	case m.filter != "":
		m.filter = ""
		m.RefreshRows()
		m.Flash("filter cleared")
	case len(m.contextFilters) > 0:
		m.contextFilters = nil
		m.RefreshRows()
		m.Flash("context filter cleared")
	case m.panel != nil:
		m.panel = nil
	}
}
