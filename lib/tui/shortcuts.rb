# frozen_string_literal: true

module Tui
  # Single source of truth for list-mode keyboard shortcuts. Each entry maps
  # raw input sequences to an App action method, plus the display key and
  # description the help modal renders. Add a shortcut here and it exists in
  # both the dispatcher and the ? overlay.
  module Shortcuts
    Entry = Struct.new(:seqs, :keys, :desc, :action, keyword_init: true)

    LIST = [
      Entry.new(seqs: ["\e[A", "k"],  keys: "↑ / k",   desc: "select previous task",             action: :select_prev),
      Entry.new(seqs: ["\e[B", "j"],  keys: "↓ / j",   desc: "select next task",                 action: :select_next),
      Entry.new(seqs: ["\e[D"],       keys: "←",       desc: "previous view",                    action: :prev_view),
      Entry.new(seqs: ["\e[C"],       keys: "→",       desc: "next view",                        action: :next_view),
      Entry.new(seqs: %w[1 2 3 4 5],  keys: "1-5",     desc: "jump to view",                     action: :jump_view),
      Entry.new(seqs: ["h"],          keys: "h",       desc: "collapse subtree (again: to parent)", action: :collapse_selected),
      Entry.new(seqs: ["l"],          keys: "l",       desc: "expand subtree",                   action: :expand_selected),
      Entry.new(seqs: ["H"],          keys: "H",       desc: "collapse all subtrees",            action: :collapse_all),
      Entry.new(seqs: ["L"],          keys: "L",       desc: "expand all subtrees",              action: :expand_all),
      Entry.new(seqs: ["\r", "\n"],   keys: "return",  desc: "task details",                     action: :open_detail),
      Entry.new(seqs: ["c"],          keys: "c",       desc: "complete selected task",           action: :complete_selected),
      Entry.new(seqs: ["d"],          keys: "d",       desc: "reschedule — fri · +3 · 07-15",    action: :open_date_popup),
      Entry.new(seqs: ["r"],          keys: "r",       desc: "recur — weekly · 2w · .+1m · off",  action: :open_recur_popup),
      Entry.new(seqs: ["x"],          keys: "x",       desc: "archive DONE/CANCELLED items",     action: :archive_sweep),
      Entry.new(seqs: ["z"],          keys: "z",       desc: "defer / activate selected task",   action: :defer_selected),
      Entry.new(seqs: ["Z"],          keys: "Z",       desc: "show / hide deferred tasks",       action: :toggle_deferred_view),
      Entry.new(seqs: ["K"],          keys: "K",       desc: "raise priority (→ A)",             action: :raise_priority),
      Entry.new(seqs: ["J"],          keys: "J",       desc: "lower priority (→ none)",          action: :lower_priority),
      Entry.new(seqs: ["o"],          keys: "o",       desc: "open task link in browser",        action: :open_link),
      Entry.new(seqs: ["y"],          keys: "y",       desc: "yank task ref (paste to agent)",   action: :yank_ref),
      Entry.new(seqs: ["Y"],          keys: "Y",       desc: "yank task as markdown",            action: :yank_markdown),
      Entry.new(seqs: ["p"],          keys: "p",       desc: "paste task ref into the prompt",   action: :paste_ref),
      Entry.new(seqs: ["/"],          keys: "/",       desc: "filter tasks by text",             action: :start_filter),
      Entry.new(seqs: ["M"],          keys: "M",       desc: "cycle agent/model",                action: :toggle_model),
      Entry.new(seqs: ["u"],          keys: "u",       desc: "undo last change",                 action: :undo_last),
      Entry.new(seqs: ["\x12"],       keys: "ctrl-r",  desc: "redo",                             action: :redo_last),
      Entry.new(seqs: ["\t", ":"],    keys: "tab / :", desc: "ask the agent — CRUD anything",    action: :focus_prompt),
      Entry.new(seqs: ["\e[5~"],      keys: "pgup",    desc: "scroll agent response up",         action: :resp_up),
      Entry.new(seqs: ["\e[6~"],      keys: "pgdn",    desc: "scroll agent response down",       action: :resp_down),
      Entry.new(seqs: ["\e"],         keys: "esc",     desc: "dismiss response / cancel agent",  action: :dismiss_or_cancel),
      Entry.new(seqs: ["?"],          keys: "?",       desc: "keyboard shortcuts",               action: :open_help),
      Entry.new(seqs: ["q", "\x03"],  keys: "q",       desc: "quit",                             action: :quit),
    ].freeze

    # Modal-mode navigation: vim-style scrolling, filtering, closing. Task
    # actions (c, d, K, …) stay live inside a detail modal via App#modal_key's
    # fallthrough, so this list only owns the modal-generic keys.
    MODAL = [
      Entry.new(seqs: ["\e[A", "k"],      keys: "↑ / k",         desc: "scroll up · previous task (detail)",  action: :modal_up),
      Entry.new(seqs: ["\e[B", "j"],      keys: "↓ / j",         desc: "scroll down · next task (detail)",    action: :modal_down),
      Entry.new(seqs: ["\x15"],           keys: "ctrl-u",        desc: "scroll half page up",                 action: :modal_half_up),
      Entry.new(seqs: ["\x04"],           keys: "ctrl-d",        desc: "scroll half page down",               action: :modal_half_down),
      Entry.new(seqs: ["\x02", "\e[5~"],  keys: "ctrl-b / pgup", desc: "scroll page up",                      action: :modal_page_up),
      Entry.new(seqs: ["\x06", "\e[6~"],  keys: "ctrl-f / pgdn", desc: "scroll page down",                    action: :modal_page_down),
      Entry.new(seqs: ["/"],              keys: "/",             desc: "filter lines (shortcuts modal)",      action: :modal_start_filter),
      Entry.new(seqs: ["\e", "q", "\r", "\n", "?"], keys: "esc / q", desc: "close modal",                     action: :close_modal),
      Entry.new(seqs: ["\x03"],           keys: "ctrl-c",        desc: "quit",                                action: :quit),
    ].freeze

    def self.find(seq)       = LIST.find { |e| e.seqs.include?(seq) }
    def self.find_modal(seq) = MODAL.find { |e| e.seqs.include?(seq) }
  end
end
