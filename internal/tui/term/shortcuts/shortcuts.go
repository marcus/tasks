// Package shortcuts is the declarative registry for every keyboard action shown
// in help. Contexts decide where a binding is active; the application owns only
// dispatch order and the action implementations. Optional form/confirmation
// metadata is consumed by the command palette.
//
// Go port of Ruby's lib/tui/shortcuts.rb. Handlers and availability predicates
// are named as strings here, exactly as Ruby names them as symbols; the
// application resolves a name to a method. Validation against a concrete
// handler set is offered as an option rather than performed by reflection.
package shortcuts

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Context is where a binding is active.
type Context string

const (
	List          Context = "list"
	Detail        Context = "detail"
	TaskEdit      Context = "task_edit"
	Modal         Context = "modal"
	Filter        Context = "filter"
	ModalFilter   Context = "modal_filter"
	Form          Context = "form"
	Picker        Context = "picker"
	ContextPicker Context = "context_picker"
	Prompt        Context = "prompt"
	Global        Context = "global"
)

// Contexts is the closed set of lookup contexts.
var Contexts = []Context{List, Detail, TaskEdit, Modal, Filter, ModalFilter, Form, Picker, ContextPicker, Prompt, Global}

// PaletteRule says whether an entry appears in the command palette.
type PaletteRule struct {
	// Always makes the entry unconditionally palette-visible.
	Always bool
	// Predicate names an availability method; empty means "not in the palette"
	// unless Always is set.
	Predicate string
}

// PaletteAlways and PaletteNever are the two constant rules.
var (
	PaletteAlways = PaletteRule{Always: true}
	PaletteNever  = PaletteRule{}
)

// PaletteWhen makes palette visibility conditional on a named predicate.
func PaletteWhen(predicate string) PaletteRule { return PaletteRule{Predicate: predicate} }

// Enabled reports whether this rule admits the entry, given a predicate
// resolver. A nil resolver treats every named predicate as false.
func (p PaletteRule) Enabled(resolve func(string) bool) bool {
	if p.Always {
		return true
	}
	if p.Predicate == "" {
		return false
	}
	if resolve == nil {
		return false
	}
	return resolve(p.Predicate)
}

// Active reports whether the rule is set at all (Ruby's truthy `palette`).
func (p PaletteRule) Active() bool { return p.Always || p.Predicate != "" }

// DefaultAvailability is the availability predicate an entry uses when it
// declares none.
const DefaultAvailability = "action_available?"

// Entry is one binding.
type Entry struct {
	Sequences []string
	// DisplayKey is what the help modal shows.
	DisplayKey      string
	HelpDisplayKey  string
	HelpDescription string
	HideInHelp      bool
	// Description is the help text; it doubles as the palette label.
	Description string
	// CommandID is the stable host-facing action identifier. Empty entries
	// derive it from Handler, keeping the registry as the only command table.
	CommandID string
	// FooterLabel and FooterPriority are host projection metadata. Priority
	// 1 is most important; 4+ is normally palette-only.
	FooterLabel    string
	FooterPriority int
	Contexts       []Context
	// Handler names the application method that performs the action. Empty
	// only for DocOnly entries.
	Handler string
	// Availability names the predicate that decides whether the action can run
	// right now. A bound but unavailable key is still consumed.
	Availability string
	Palette      PaletteRule
	// Form and Confirmation are metadata for the palette and future consumers.
	Form         string
	Confirmation string
	// DocOnly entries appear in help but bind no key and run no handler.
	DocOnly bool
}

func entry(e Entry) Entry {
	if e.Availability == "" {
		e.Availability = DefaultAvailability
	}
	if e.CommandID == "" && e.Handler != "" {
		e.CommandID = strings.ReplaceAll(e.Handler, "_", "-")
	}
	if e.FooterLabel == "" {
		e.FooterLabel = defaultFooterLabel(e.Description)
	}
	if e.FooterPriority == 0 {
		e.FooterPriority = 4
	}
	return e
}

func defaultFooterLabel(description string) string {
	word := strings.Fields(description)
	if len(word) == 0 {
		return "Action"
	}
	label := strings.Trim(word[0], "—…/()")
	if label == "" {
		return "Action"
	}
	first, size := utf8.DecodeRuneInString(label)
	return string(unicode.ToUpper(first)) + label[size:]
}

// Registry is the complete key table, in dispatch/help order.
var Registry = []Entry{
	entry(Entry{Sequences: []string{"\x1b[A", "k"}, DisplayKey: "↑ / k", Description: "select previous task", Contexts: []Context{List}, Handler: "select_prev"}),
	entry(Entry{Sequences: []string{"\x1b[B", "j"}, DisplayKey: "↓ / j", Description: "select next task", Contexts: []Context{List}, Handler: "select_next"}),
	entry(Entry{Sequences: []string{"\x1b[D"}, DisplayKey: "←", Description: "previous view", Contexts: []Context{List}, Handler: "prev_view"}),
	entry(Entry{Sequences: []string{"\x1b[C"}, DisplayKey: "→", Description: "next view", Contexts: []Context{List}, Handler: "next_view"}),
	entry(Entry{Sequences: []string{"1"}, DisplayKey: "1", HelpDisplayKey: "1-6", HelpDescription: "jump to view", Description: "jump to Agenda view", CommandID: "view-agenda", Contexts: []Context{List}, Handler: "jump_view"}),
	entry(Entry{Sequences: []string{"2"}, DisplayKey: "2", HideInHelp: true, Description: "jump to Next view", CommandID: "view-next", Contexts: []Context{List}, Handler: "jump_view"}),
	entry(Entry{Sequences: []string{"3"}, DisplayKey: "3", HideInHelp: true, Description: "jump to Quadrants view", CommandID: "view-quadrants", Contexts: []Context{List}, Handler: "jump_view"}),
	entry(Entry{Sequences: []string{"4"}, DisplayKey: "4", HideInHelp: true, Description: "jump to Projects view", CommandID: "view-projects", Contexts: []Context{List}, Handler: "jump_view"}),
	entry(Entry{Sequences: []string{"5"}, DisplayKey: "5", HideInHelp: true, Description: "jump to Outline view", CommandID: "view-outline", Contexts: []Context{List}, Handler: "jump_view"}),
	entry(Entry{Sequences: []string{"6"}, DisplayKey: "6", HideInHelp: true, Description: "jump to Inbox view", CommandID: "view-inbox", Contexts: []Context{List}, Handler: "jump_view"}),
	entry(Entry{Sequences: []string{"\x1b[1;3A", "\x1b\x1b[A", "\x1bk"}, DisplayKey: "alt-↑ / alt-k", Description: "Move up", Contexts: []Context{List}, Handler: "move_subtree_up", Availability: "ordering_action_available?", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"\x1b[1;3B", "\x1b\x1b[B", "\x1bj"}, DisplayKey: "alt-↓ / alt-j", Description: "Move down", Contexts: []Context{List}, Handler: "move_subtree_down", Availability: "ordering_action_available?", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{">"}, DisplayKey: ">", Description: "Indent", Contexts: []Context{List}, Handler: "indent_subtree", Availability: "ordering_action_available?", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"<"}, DisplayKey: "<", Description: "Outdent", Contexts: []Context{List}, Handler: "outdent_subtree", Availability: "ordering_action_available?", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"h"}, DisplayKey: "h", Description: "collapse subtree (again: to parent)", Contexts: []Context{List}, Handler: "collapse_selected", Palette: PaletteWhen("selected_action_available?")}),
	entry(Entry{Sequences: []string{"l"}, DisplayKey: "l", Description: "expand subtree", Contexts: []Context{List}, Handler: "expand_selected", Palette: PaletteWhen("selected_action_available?")}),
	entry(Entry{Sequences: []string{"H"}, DisplayKey: "H", Description: "collapse all subtrees", Contexts: []Context{List}, Handler: "collapse_all", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"L"}, DisplayKey: "L", Description: "expand all subtrees", Contexts: []Context{List}, Handler: "expand_all", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"\r", "\n"}, DisplayKey: "return", Description: "open / close task details", FooterLabel: "Details", FooterPriority: 1, Contexts: []Context{List}, Handler: "open_detail", Palette: PaletteWhen("selected_action_available?")}),
	entry(Entry{Sequences: []string{"c"}, DisplayKey: "c", Description: "complete selected task", FooterLabel: "Complete", FooterPriority: 1, Contexts: []Context{List, Detail}, Handler: "complete_selected", Palette: PaletteWhen("selected_action_available?")}),
	entry(Entry{Sequences: []string{"#", "\x1b[3~"}, DisplayKey: "# / del", Description: "delete selected task (hard delete; undoable)", Contexts: []Context{List, Detail}, Handler: "delete_selected", Palette: PaletteWhen("selected_action_available?"), Confirmation: "delete_preview"}),
	entry(Entry{Sequences: []string{"d"}, DisplayKey: "d", Description: "edit Deadline / Available from date or time", Contexts: []Context{List, Detail}, Handler: "open_date_popup", Palette: PaletteWhen("selected_action_available?"), Form: "date"}),
	entry(Entry{Sequences: []string{"r"}, DisplayKey: "r", Description: "Reject proposal", Contexts: []Context{List, Detail}, Handler: "reject_proposal", Availability: "proposal_action_available?", Palette: PaletteWhen("proposal_action_available?")}),
	entry(Entry{Sequences: []string{"r"}, DisplayKey: "r", Description: "recur — weekly · every mon · m:15 · off", Contexts: []Context{List, Detail}, Handler: "open_recur_popup", Palette: PaletteWhen("recurrence_action_available?"), Form: "recurrence"}),
	entry(Entry{Sequences: []string{"a"}, DisplayKey: "a", Description: "Approve proposal", Contexts: []Context{List, Detail}, Handler: "approve_proposal", Availability: "proposal_action_available?", Palette: PaletteWhen("proposal_action_available?")}),
	entry(Entry{Sequences: []string{"x"}, DisplayKey: "x", Description: "archive DONE/CANCELLED items", Contexts: []Context{List}, Handler: "archive_sweep", Palette: PaletteAlways, Confirmation: "archive_preview"}),
	entry(Entry{Sequences: []string{"e"}, DisplayKey: "e", Description: "rename selected project", Contexts: []Context{List}, Handler: "rename_project", Availability: "project_selected?", Palette: PaletteWhen("project_selected?"), Form: "project_rename"}),
	entry(Entry{Sequences: []string{"a"}, DisplayKey: "a", Description: "capture a task into the project", Contexts: []Context{List}, Handler: "capture_into_project", Availability: "project_selected?", Palette: PaletteWhen("project_selected?"), Form: "project_capture"}),
	entry(Entry{Sequences: []string{"z"}, DisplayKey: "z", Description: "defer until — date/time · someday · now", Contexts: []Context{List, Detail}, Handler: "defer_selected", Palette: PaletteWhen("selected_action_available?"), Form: "defer_until"}),
	entry(Entry{Sequences: []string{"D"}, DisplayKey: "D", Description: "Delegate… — email · refine · research · implement · release · off", Contexts: []Context{List, Detail}, Handler: "delegate_selected", Availability: "delegation_action_available?", Palette: PaletteWhen("delegation_action_available?"), Form: "delegate"}),
	entry(Entry{Sequences: []string{"W"}, DisplayKey: "W", Description: "Set work reference… — URL/id · off", Contexts: []Context{List, Detail}, Handler: "set_work_ref_selected", Availability: "delegation_action_available?", Palette: PaletteWhen("delegation_action_available?"), Form: "work_ref"}),
	entry(Entry{Sequences: []string{"Z"}, DisplayKey: "Z", Description: "show / hide unavailable tasks", Contexts: []Context{List}, Handler: "toggle_deferred_view", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"K"}, DisplayKey: "K", Description: "raise priority (→ A)", Contexts: []Context{List, Detail}, Handler: "raise_priority", Palette: PaletteWhen("selected_action_available?")}),
	entry(Entry{Sequences: []string{"J"}, DisplayKey: "J", Description: "lower priority (→ none)", Contexts: []Context{List, Detail}, Handler: "lower_priority", Palette: PaletteWhen("selected_action_available?")}),
	entry(Entry{Sequences: []string{"o"}, DisplayKey: "o", Description: "open task link(s) in browser", Contexts: []Context{List, Detail}, Handler: "open_link", Palette: PaletteWhen("link_action_available?")}),
	entry(Entry{Sequences: []string{"y"}, DisplayKey: "y", Description: "yank stable task id", Contexts: []Context{List, Detail}, Handler: "yank_ref", Palette: PaletteWhen("selected_action_available?")}),
	entry(Entry{Sequences: []string{"Y"}, DisplayKey: "Y", Description: "yank task as markdown", Contexts: []Context{List, Detail}, Handler: "yank_markdown", Palette: PaletteWhen("selected_action_available?")}),
	entry(Entry{Sequences: []string{"p"}, DisplayKey: "p", Description: "paste task id into the prompt", Contexts: []Context{List, Detail}, Handler: "paste_ref", Palette: PaletteWhen("selected_action_available?")}),
	entry(Entry{Sequences: []string{"e"}, DisplayKey: "e", Description: "edit task", FooterLabel: "Edit", FooterPriority: 1, Contexts: []Context{Detail}, Handler: "start_task_edit", Palette: PaletteWhen("selected_action_available?"), Form: "task_edit"}),
	entry(Entry{Sequences: []string{"\t"}, DisplayKey: "tab", Description: "ask the agent — CRUD anything", FooterLabel: "Ask", FooterPriority: 1, Contexts: []Context{Detail}, Handler: "focus_prompt"}),
	entry(Entry{Sequences: []string{"\x1b[Z"}, DisplayKey: "shift-tab", Description: "edit task from its last field", Contexts: []Context{Detail}, Handler: "start_task_edit_last"}),
	entry(Entry{Sequences: []string{"\x0b"}, DisplayKey: "ctrl-k", Description: "grow task panel", Contexts: []Context{Detail}, Handler: "grow_task_panel", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"\x0c"}, DisplayKey: "ctrl-l", Description: "shrink task panel", Contexts: []Context{Detail}, Handler: "shrink_task_panel", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"/"}, DisplayKey: "/", Description: "filter tasks by text", FooterLabel: "Filter", FooterPriority: 2, Contexts: []Context{List}, Handler: "start_filter", Palette: PaletteAlways, Form: "filter"}),
	entry(Entry{Sequences: []string{"@"}, DisplayKey: "@", Description: "filter tasks by @contexts", FooterLabel: "Contexts", FooterPriority: 2, Contexts: []Context{List}, Handler: "open_context_palette", Palette: PaletteAlways, Form: "context_filter"}),
	entry(Entry{Sequences: []string{"M"}, DisplayKey: "M", Description: "cycle agent/model", Contexts: []Context{List}, Handler: "toggle_model", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"A"}, DisplayKey: "A", Description: "open agent activity", Contexts: []Context{List}, Handler: "open_agent_activity", Availability: "agent_activity_available?", Palette: PaletteAlways}),
	entry(Entry{Sequences: nil, DisplayKey: "palette", Description: "cancel queued agent requests", Contexts: []Context{List}, Handler: "cancel_queued_agent_requests", Availability: "pending_agent_requests_available?", Palette: PaletteAlways, Confirmation: "agent_queue"}),
	entry(Entry{Sequences: []string{"u"}, DisplayKey: "u", Description: "undo last change", Contexts: []Context{List, Detail}, Handler: "undo_last", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"\x12"}, DisplayKey: "ctrl-r", Description: "redo", Contexts: []Context{List, Detail}, Handler: "redo_last", Palette: PaletteAlways}),
	entry(Entry{Sequences: []string{"\x15"}, DisplayKey: "ctrl-u", Description: "scroll task details up", Contexts: []Context{List}, Handler: "panel_half_up", Availability: "panel_scroll_available?"}),
	entry(Entry{Sequences: []string{"\x04"}, DisplayKey: "ctrl-d", Description: "scroll task details down", Contexts: []Context{List}, Handler: "panel_half_down", Availability: "panel_scroll_available?"}),
	entry(Entry{Sequences: []string{"\x02"}, DisplayKey: "ctrl-b", Description: "scroll task details one page up", Contexts: []Context{List}, Handler: "panel_page_up", Availability: "panel_scroll_available?"}),
	entry(Entry{Sequences: []string{"\x06"}, DisplayKey: "ctrl-f", Description: "scroll task details one page down", Contexts: []Context{List}, Handler: "panel_page_down", Availability: "panel_scroll_available?"}),
	entry(Entry{Sequences: []string{"\t"}, DisplayKey: "tab", Description: "ask the agent — CRUD anything", FooterLabel: "Ask", FooterPriority: 1, Contexts: []Context{List}, Handler: "focus_prompt", Palette: PaletteAlways, Form: "agent_prompt"}),
	entry(Entry{Sequences: []string{":"}, DisplayKey: ":", Description: "search available actions", FooterLabel: "Actions", FooterPriority: 1, Contexts: []Context{List, Detail}, Handler: "open_action_palette"}),
	entry(Entry{Sequences: []string{"\x1b[5~"}, DisplayKey: "pgup", Description: "scroll agent response up", Contexts: []Context{List}, Handler: "resp_up"}),
	entry(Entry{Sequences: []string{"\x1b[6~"}, DisplayKey: "pgdn", Description: "scroll agent response down", Contexts: []Context{List}, Handler: "resp_down"}),
	entry(Entry{Sequences: []string{"\x1b"}, DisplayKey: "esc", Description: "dismiss response / close task details", Contexts: []Context{List}, Handler: "dismiss_or_cancel"}),
	entry(Entry{Sequences: []string{"?"}, DisplayKey: "?", Description: "keyboard shortcuts", FooterLabel: "Help", FooterPriority: 3, Contexts: []Context{List}, Handler: "open_help", Palette: PaletteAlways}),
	entry(Entry{Sequences: nil, DisplayKey: "click", Description: "select task · click again for details · click tab to switch view", Contexts: []Context{List}, DocOnly: true}),
	entry(Entry{Sequences: nil, DisplayKey: "wheel", Description: "scroll list / panel / modal / agent response under the pointer", Contexts: []Context{List}, DocOnly: true}),
	entry(Entry{Sequences: []string{"q"}, DisplayKey: "q", Description: "quit (confirms unsaved draft)", FooterLabel: "Quit", FooterPriority: 3, Contexts: []Context{List}, Handler: "quit", Availability: "quit_available?", Palette: PaletteAlways}),

	entry(Entry{Sequences: []string{"\t"}, DisplayKey: "tab", Description: "save field and edit next", CommandID: "task-edit-next", FooterLabel: "Next Field", FooterPriority: 2, Contexts: []Context{TaskEdit}, Handler: "task_edit_input"}),
	entry(Entry{Sequences: []string{"\x1b[Z"}, DisplayKey: "shift-tab", Description: "save field and edit previous", CommandID: "task-edit-previous", FooterLabel: "Previous Field", FooterPriority: 3, Contexts: []Context{TaskEdit}, Handler: "task_edit_input"}),
	entry(Entry{Sequences: []string{"\x13"}, DisplayKey: "ctrl-s", Description: "save focused task field", CommandID: "task-edit-save", FooterLabel: "Save", FooterPriority: 1, Contexts: []Context{TaskEdit}, Handler: "task_edit_input"}),
	entry(Entry{Sequences: []string{"\x0f"}, DisplayKey: "ctrl-o", Description: "finish editing task", CommandID: "task-edit-finish", FooterLabel: "Finish", FooterPriority: 1, Contexts: []Context{TaskEdit}, Handler: "task_edit_input"}),
	entry(Entry{Sequences: []string{"\x0b"}, DisplayKey: "ctrl-k", Description: "grow task panel without saving", Contexts: []Context{TaskEdit}, Handler: "grow_task_panel"}),
	entry(Entry{Sequences: []string{"\x0c"}, DisplayKey: "ctrl-l", Description: "shrink task panel without saving", Contexts: []Context{TaskEdit}, Handler: "shrink_task_panel"}),
	entry(Entry{Sequences: []string{"\x1b"}, DisplayKey: "esc", Description: "close picker / confirm field revert / finish editing", CommandID: "task-edit-cancel", FooterLabel: "Cancel", FooterPriority: 2, Contexts: []Context{TaskEdit}, Handler: "task_edit_input"}),

	// Modal navigation is kept as an explicit context for blocking overlays.
	// Detail actions are palette metadata while the panel stays in list mode.
	entry(Entry{Sequences: []string{"\x1b[A", "k"}, DisplayKey: "↑ / k", Description: "scroll modal up", Contexts: []Context{Modal}, Handler: "modal_up", Availability: "modal_navigation_available?"}),
	entry(Entry{Sequences: []string{"\x1b[B", "j"}, DisplayKey: "↓ / j", Description: "scroll modal down", Contexts: []Context{Modal}, Handler: "modal_down", Availability: "modal_navigation_available?"}),
	entry(Entry{Sequences: []string{"\x15"}, DisplayKey: "ctrl-u", Description: "scroll half page up", Contexts: []Context{Modal}, Handler: "modal_half_up", Availability: "modal_navigation_available?"}),
	entry(Entry{Sequences: []string{"\x04"}, DisplayKey: "ctrl-d", Description: "scroll half page down", Contexts: []Context{Modal}, Handler: "modal_half_down", Availability: "modal_navigation_available?"}),
	entry(Entry{Sequences: []string{"\x02", "\x1b[5~"}, DisplayKey: "ctrl-b / pgup", Description: "scroll page up", Contexts: []Context{Modal}, Handler: "modal_page_up", Availability: "modal_navigation_available?"}),
	entry(Entry{Sequences: []string{"\x06", "\x1b[6~"}, DisplayKey: "ctrl-f / pgdn", Description: "scroll page down", Contexts: []Context{Modal}, Handler: "modal_page_down", Availability: "modal_navigation_available?"}),
	entry(Entry{Sequences: []string{"/"}, DisplayKey: "/", Description: "filter lines (shortcuts modal)", FooterLabel: "Filter", FooterPriority: 2, Contexts: []Context{Modal}, Handler: "modal_start_filter", Availability: "modal_filter_available?", Form: "modal_filter"}),
	entry(Entry{Sequences: []string{"y", "Y"}, DisplayKey: "y", Description: "confirm modal action", CommandID: "modal-confirm", FooterLabel: "Confirm", FooterPriority: 1, Contexts: []Context{Modal}, Handler: "modal_confirmation", Availability: "modal_confirmation_available?", HideInHelp: true}),
	entry(Entry{Sequences: []string{"\r", "\n"}, DisplayKey: "return", Description: "confirm default modal action", CommandID: "modal-confirm-default", FooterLabel: "Confirm", FooterPriority: 1, Contexts: []Context{Modal}, Handler: "modal_confirmation", Availability: "modal_confirmation_accepts_enter?", HideInHelp: true}),
	entry(Entry{Sequences: []string{"n", "N", "\x1b"}, DisplayKey: "n / esc", Description: "cancel modal action", CommandID: "modal-cancel", FooterLabel: "Cancel", FooterPriority: 2, Contexts: []Context{Modal}, Handler: "modal_confirmation", Availability: "modal_confirmation_available?", HideInHelp: true}),
	entry(Entry{Sequences: []string{"q"}, DisplayKey: "q", Description: "cancel modal action", CommandID: "modal-cancel-q", FooterLabel: "Cancel", FooterPriority: 2, Contexts: []Context{Modal}, Handler: "modal_confirmation", Availability: "modal_confirmation_accepts_q?", HideInHelp: true}),
	entry(Entry{Sequences: []string{"\x1b", "q", "\r", "\n"}, DisplayKey: "esc / q", Description: "close modal", FooterLabel: "Close", FooterPriority: 1, Contexts: []Context{Modal}, Handler: "close_modal", Availability: "modal_close_available?"}),
	entry(Entry{Sequences: []string{"?"}, DisplayKey: "?", Description: "close shortcuts or activity modal", CommandID: "close-modal-question", Contexts: []Context{Modal}, Handler: "close_modal", Availability: "modal_question_close_available?", HideInHelp: true}),
	entry(Entry{Sequences: []string{"n", "N"}, DisplayKey: "n", Description: "dismiss blocked archive notice", CommandID: "dismiss-archive-blocked", Contexts: []Context{Modal}, Handler: "close_modal", Availability: "archive_blocked_close_available?", HideInHelp: true}),

	entry(Entry{Sequences: []string{"\x1b"}, DisplayKey: "esc", Description: "cancel text filter", CommandID: "filter-cancel", FooterLabel: "Cancel", FooterPriority: 1, Contexts: []Context{Filter}, Handler: "filter_input"}),
	entry(Entry{Sequences: []string{"\r", "\n"}, DisplayKey: "return", Description: "apply text filter", CommandID: "filter-apply", FooterLabel: "Apply", FooterPriority: 1, Contexts: []Context{Filter}, Handler: "filter_input"}),
	entry(Entry{Sequences: []string{"\x1b"}, DisplayKey: "esc", Description: "clear modal filter", CommandID: "modal-filter-clear", FooterLabel: "Clear", FooterPriority: 1, Contexts: []Context{ModalFilter}, Handler: "modal_filter_input"}),
	entry(Entry{Sequences: []string{"\r", "\n"}, DisplayKey: "return", Description: "apply modal filter", CommandID: "modal-filter-apply", FooterLabel: "Apply", FooterPriority: 1, Contexts: []Context{ModalFilter}, Handler: "modal_filter_input"}),
	entry(Entry{Sequences: []string{"\x1b"}, DisplayKey: "esc", Description: "cancel form", CommandID: "form-cancel", FooterLabel: "Cancel", FooterPriority: 1, Contexts: []Context{Form}, Handler: "form_input"}),
	entry(Entry{Sequences: []string{"\r", "\n"}, DisplayKey: "return", Description: "submit form", CommandID: "form-submit", FooterLabel: "Submit", FooterPriority: 1, Contexts: []Context{Form}, Handler: "form_input"}),
	entry(Entry{Sequences: []string{"\x1b"}, DisplayKey: "esc", Description: "close picker", CommandID: "picker-cancel", FooterLabel: "Close", FooterPriority: 1, Contexts: []Context{Picker}, Handler: "picker_input"}),
	entry(Entry{Sequences: []string{"\r", "\n"}, DisplayKey: "return", Description: "choose picker action", CommandID: "picker-accept", FooterLabel: "Choose", FooterPriority: 1, Contexts: []Context{Picker}, Handler: "picker_input"}),
	entry(Entry{Sequences: []string{"\x1b[A", "\x10"}, DisplayKey: "↑ / ctrl-p", Description: "select previous picker row", CommandID: "picker-previous", FooterLabel: "Previous", Contexts: []Context{Picker}, Handler: "picker_input"}),
	entry(Entry{Sequences: []string{"\x1b[B", "\x0e"}, DisplayKey: "↓ / ctrl-n", Description: "select next picker row", CommandID: "picker-next", FooterLabel: "Next", Contexts: []Context{Picker}, Handler: "picker_input"}),
	entry(Entry{Sequences: []string{"\x1b"}, DisplayKey: "esc", Description: "close context picker", CommandID: "context-picker-cancel", FooterLabel: "Close", FooterPriority: 1, Contexts: []Context{ContextPicker}, Handler: "context_picker_input"}),
	entry(Entry{Sequences: []string{"\r", "\n"}, DisplayKey: "return", Description: "apply context selection", CommandID: "context-picker-apply", FooterLabel: "Apply", FooterPriority: 1, Contexts: []Context{ContextPicker}, Handler: "context_picker_input"}),
	entry(Entry{Sequences: []string{"\x1b[A", "\x10"}, DisplayKey: "↑ / ctrl-p", Description: "select previous context", CommandID: "context-picker-previous", FooterLabel: "Previous", Contexts: []Context{ContextPicker}, Handler: "context_picker_input"}),
	entry(Entry{Sequences: []string{"\x1b[B", "\x0e"}, DisplayKey: "↓ / ctrl-n", Description: "select next context", CommandID: "context-picker-next", FooterLabel: "Next", Contexts: []Context{ContextPicker}, Handler: "context_picker_input"}),
	entry(Entry{Sequences: []string{" "}, DisplayKey: "space", Description: "toggle selected context", CommandID: "context-picker-toggle", FooterLabel: "Toggle", FooterPriority: 2, Contexts: []Context{ContextPicker}, Handler: "context_picker_input"}),
	entry(Entry{Sequences: []string{"\x1b", "\t"}, DisplayKey: "esc / tab", Description: "leave agent prompt", CommandID: "prompt-close", FooterLabel: "Close", FooterPriority: 1, Contexts: []Context{Prompt}, Handler: "prompt_input"}),
	entry(Entry{Sequences: []string{"\r", "\n"}, DisplayKey: "return", Description: "submit agent prompt", CommandID: "prompt-submit", FooterLabel: "Submit", FooterPriority: 1, Contexts: []Context{Prompt}, Handler: "prompt_input"}),

	entry(Entry{Sequences: []string{"\x03"}, DisplayKey: "ctrl-c", Description: "quit (confirms unsaved draft)", FooterLabel: "Quit", FooterPriority: 4, Contexts: []Context{Global}, Handler: "quit", Availability: "quit_available?"}),
	entry(Entry{Sequences: []string{"\x03"}, DisplayKey: "ctrl-c", Description: "keep the quit confirmation open", CommandID: "quit-confirmation-reminder", Contexts: []Context{Global}, Handler: "modal_confirmation", Availability: "quit_confirmation_available?", HideInHelp: true}),
}

// ErrUnknownContext is returned for a lookup in a context that does not exist.
var ErrUnknownContext = errors.New("unknown shortcut context")

func knownContext(c Context) bool {
	for _, known := range Contexts {
		if known == c {
			return true
		}
	}
	return false
}

func (e Entry) hasContext(c Context) bool {
	for _, own := range e.Contexts {
		if own == c {
			return true
		}
	}
	return false
}

// Entries lists the bindings active in a context. Global bindings are included
// unless includeGlobal is false.
func Entries(context Context, includeGlobal bool) ([]Entry, error) {
	if !knownContext(context) {
		return nil, fmt.Errorf("%w %q", ErrUnknownContext, string(context))
	}
	var out []Entry
	for _, e := range Registry {
		if e.hasContext(context) || (includeGlobal && e.hasContext(Global)) {
			out = append(out, e)
		}
	}
	return out, nil
}

// Match returns the binding for a sequence in a context, even when it is
// unavailable: dispatch must consume such a key instead of falling through to
// another context. resolve answers availability predicates; a nil resolve
// returns the first binding regardless of availability.
func Match(sequence string, context Context, resolve func(string) bool) (Entry, bool) {
	entries, err := Entries(context, true)
	if err != nil {
		return Entry{}, false
	}
	var matches []Entry
	for _, e := range entries {
		for _, s := range e.Sequences {
			if s == sequence {
				matches = append(matches, e)
				break
			}
		}
	}
	if len(matches) == 0 {
		return Entry{}, false
	}
	if resolve == nil {
		return matches[0], true
	}
	for _, e := range matches {
		if resolve(e.Availability) {
			return e, true
		}
	}
	return matches[0], true
}

// PaletteEntries lists the context's palette-visible, currently available
// actions. Global bindings are excluded, as in Ruby. resolve answers both the
// availability and the palette predicates; a nil resolve admits nothing.
func PaletteEntries(context Context, resolve func(string) bool) []Entry {
	entries, err := Entries(context, false)
	if err != nil || resolve == nil {
		return nil
	}
	var out []Entry
	for _, e := range entries {
		if !resolve(e.Availability) {
			continue
		}
		if !e.Palette.Enabled(resolve) {
			continue
		}
		out = append(out, e)
	}
	return out
}

// ValidateOptions optionally checks handler and predicate names against the set
// the application actually implements.
type ValidateOptions struct {
	Entries []Entry
	// Handlers, when non-nil, must contain every entry's handler name.
	Handlers map[string]bool
	// Predicates, when non-nil, must contain every availability and palette
	// predicate name.
	Predicates map[string]bool
	// KeyedHandlers names handlers that require the original key. A
	// palette-enabled entry may not use one, because the palette invokes the
	// action without a key.
	KeyedHandlers map[string]bool
}

// Validate checks the registry's structural invariants and its per-context key
// collisions.
func Validate(opts ValidateOptions) error {
	entries := opts.Entries
	if entries == nil {
		entries = Registry
	}
	for _, e := range entries {
		if err := validateEntry(e, opts); err != nil {
			return err
		}
	}
	return validateCollisions(entries)
}

func validateEntry(e Entry, opts ValidateOptions) error {
	for _, s := range e.Sequences {
		if s == "" {
			return errors.New("shortcut sequences must be an array of non-empty strings")
		}
	}
	if len(e.Sequences) == 0 && !e.Palette.Active() && !e.DocOnly {
		return errors.New("a shortcut without key sequences must be palette-enabled")
	}
	seen := map[string]bool{}
	for _, s := range e.Sequences {
		if seen[s] {
			return errors.New("shortcut sequences must be unique")
		}
		seen[s] = true
	}
	if e.DisplayKey == "" {
		return errors.New("shortcut display key must be a non-empty string")
	}
	if !e.DocOnly && e.CommandID == "" {
		return errors.New("shortcut command id must be a non-empty string")
	}
	if e.FooterLabel == "" || e.FooterPriority <= 0 {
		return errors.New("shortcut footer metadata must include a label and positive priority")
	}
	if e.Description == "" {
		return errors.New("shortcut description must be a non-empty string")
	}
	if len(e.Contexts) == 0 {
		return errors.New("shortcut contexts are invalid")
	}
	seenContext := map[Context]bool{}
	for _, c := range e.Contexts {
		if seenContext[c] || !knownContext(c) {
			return errors.New("shortcut contexts are invalid")
		}
		seenContext[c] = true
	}
	if e.DocOnly {
		if e.Handler != "" {
			return errors.New("doc_only shortcuts must not declare a handler")
		}
		if len(e.Sequences) != 0 {
			return errors.New("doc_only shortcuts must not declare key sequences")
		}
		return nil
	}
	if e.Handler == "" {
		return errors.New("shortcut handler must be a method name")
	}
	if e.Availability == "" {
		return errors.New("shortcut availability must be a method name")
	}
	if opts.Handlers != nil && !opts.Handlers[e.Handler] {
		return fmt.Errorf("missing shortcut handler %s", e.Handler)
	}
	if opts.KeyedHandlers != nil && e.Palette.Active() && opts.KeyedHandlers[e.Handler] {
		return fmt.Errorf("palette shortcut handler %s must not require a key", e.Handler)
	}
	if opts.Predicates != nil {
		if !opts.Predicates[e.Availability] {
			return fmt.Errorf("missing shortcut availability %s", e.Availability)
		}
		if e.Palette.Predicate != "" && !opts.Predicates[e.Palette.Predicate] {
			return fmt.Errorf("missing shortcut palette availability %s", e.Palette.Predicate)
		}
	}
	return nil
}

type bindingKey struct {
	context  Context
	sequence string
}

func validateCollisions(entries []Entry) error {
	bindings := map[bindingKey]int{}
	for i, e := range entries {
		effective := e.Contexts
		if e.hasContext(Global) {
			effective = nil
			for _, c := range Contexts {
				if c != Global {
					effective = append(effective, c)
				}
			}
		}
		for _, context := range effective {
			for _, sequence := range e.Sequences {
				key := bindingKey{context, sequence}
				if other, ok := bindings[key]; ok && other != i &&
					entries[other].Availability == e.Availability {
					return fmt.Errorf("duplicate shortcut %q in %s: %s and %s",
						sequence, context, entries[other].Handler, e.Handler)
				}
				bindings[key] = i
			}
		}
	}
	return nil
}
