# frozen_string_literal: true

# Compat shim — see lib/tasks/dates.rb.
require_relative "../tasks/dates"

module Tui
  Dates = Tasks::Dates
end
