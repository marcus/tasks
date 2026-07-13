# frozen_string_literal: true

require_relative "../term_form/text"

module Tui
  # Compatibility name for the TUI's single-line editor. The grapheme-aware
  # implementation is neutral so TermForm can reuse it without loading TUI.
  class TextInput < TermForm::TextEditor
  end
end
