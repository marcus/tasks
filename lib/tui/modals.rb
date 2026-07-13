# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"
require_relative "shortcuts"

module Tui
  # Content builders for modal overlays. Pure functions returning
  # { title:, lines: } — the app owns state, Frame owns drawing.
  module Modals
    A = Ansi
    T = Theme

    module_function

    # Generated entirely from the action registry. Shared task actions are
    # repeated in the detail section so their availability is unambiguous.
    def help
      key_w = Shortcuts::REGISTRY.map { |e| e.display_key.length }.max
      groups = [
        ["in the task list", Shortcuts.entries(:list, include_global: false)],
        ["in task details", Shortcuts.entries(:detail, include_global: false)],
        ["while editing a task", Shortcuts.entries(:task_edit, include_global: false)],
        ["in a modal", Shortcuts.entries(:modal, include_global: false)],
        ["everywhere", Shortcuts.entries(:global, include_global: false)],
      ]
      lines = []
      groups.each_with_index do |(title, entries), index|
        lines << "" unless index.zero?
        lines << T.paint(:section, title)
        entries.each { |entry| lines << shortcut_line(entry, key_w) }
      end
      lines << ""
      lines << T.paint(:muted, "prompt/quick-form input: return submits · esc cancels · ctrl-a/e/b/f move")
      { title: "keyboard shortcuts", lines: lines }
    end

    def shortcut_line(entry, key_w)
      "#{T.paint(:accent, entry.display_key.ljust(key_w))} #{entry.description}"
    end

  end
end
