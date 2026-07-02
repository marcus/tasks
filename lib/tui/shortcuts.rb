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
      Entry.new(seqs: %w[1 2 3 4],    keys: "1-4",     desc: "jump to view",                     action: :jump_view),
      Entry.new(seqs: ["\r", "\n"],   keys: "return",  desc: "task details",                     action: :open_detail),
      Entry.new(seqs: ["c"],          keys: "c",       desc: "complete selected task",           action: :complete_selected),
      Entry.new(seqs: ["d"],          keys: "d",       desc: "reschedule — fri · +3 · 07-15",    action: :open_date_popup),
      Entry.new(seqs: ["x"],          keys: "x",       desc: "archive DONE/CANCELLED items",     action: :archive_sweep),
      Entry.new(seqs: ["K"],          keys: "K",       desc: "raise priority (→ A)",             action: :raise_priority),
      Entry.new(seqs: ["J"],          keys: "J",       desc: "lower priority (→ none)",          action: :lower_priority),
      Entry.new(seqs: ["y"],          keys: "y",       desc: "yank task ref (paste to claude)",  action: :yank_ref),
      Entry.new(seqs: ["Y"],          keys: "Y",       desc: "yank task as markdown",            action: :yank_markdown),
      Entry.new(seqs: ["p"],          keys: "p",       desc: "paste task ref into the prompt",   action: :paste_ref),
      Entry.new(seqs: ["\t", ":"],    keys: "tab / :", desc: "ask claude — CRUD anything",       action: :focus_prompt),
      Entry.new(seqs: ["\e[5~"],      keys: "pgup",    desc: "scroll claude response up",        action: :resp_up),
      Entry.new(seqs: ["\e[6~"],      keys: "pgdn",    desc: "scroll claude response down",      action: :resp_down),
      Entry.new(seqs: ["\e"],         keys: "esc",     desc: "dismiss response / cancel claude", action: :dismiss_or_cancel),
      Entry.new(seqs: ["?"],          keys: "?",       desc: "keyboard shortcuts",               action: :open_help),
      Entry.new(seqs: ["q", "\x03"],  keys: "q",       desc: "quit",                             action: :quit),
    ].freeze

    def self.find(seq) = LIST.find { |e| e.seqs.include?(seq) }
  end
end
