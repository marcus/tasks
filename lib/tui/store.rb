# frozen_string_literal: true

# Compat shim: the model layer lives in lib/tasks/ (shared by the CLI and
# the TUI). Tui keeps aliases so UI code and older requires keep working.
require_relative "../tasks/store"

module Tui
  Item  = Tasks::Item
  Store = Tasks::Store
end
