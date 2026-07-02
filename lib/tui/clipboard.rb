# frozen_string_literal: true

module Tui
  # System clipboard via whatever tool the platform has (no gem needed).
  module Clipboard
    COMMANDS = [
      ["pbcopy"],                              # macOS
      ["wl-copy"],                             # Wayland
      ["xclip", "-selection", "clipboard"],    # X11
      ["xsel", "--clipboard", "--input"],
    ].freeze

    def self.command
      return @command if defined?(@command)
      @command = COMMANDS.find { |c| system("command -v #{c.first} >/dev/null 2>&1") }
    end

    # Returns true on success. `cmd:` is injectable for tests.
    def self.copy(text, cmd: command)
      return false unless cmd
      IO.popen(cmd, "w") { |io| io.write(text) }
      $?.success?
    rescue Errno::ENOENT, Errno::EPIPE
      false
    end
  end
end
