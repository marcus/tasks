# frozen_string_literal: true

module Tasks
  # The CLI's dispatch table as data.
  #
  # Structured output is a first-class surface: an agent scripting this CLI must
  # be able to ask "does this command give me a machine-readable result?" and get
  # a true answer without reading bin/tasks. This registry is that answer. It is
  # the single source for
  #
  #   * bin/tasks dispatch (names + aliases resolve here; a handler that has no
  #     entry, or an entry that has no handler, aborts at startup),
  #   * `tasks help --json`, which emits this table verbatim,
  #   * docs/cli-spec.md's "Structured output (--json) coverage" table, and
  #   * test/test_cli_json_coverage.rb, which runs every entry and asserts that a
  #     `json: true` command really does print one JSON document.
  #
  # `json: false` is an explicit opt-out and requires a `reason`. That is the
  # whole enforcement story: a new command cannot be added without deciding —
  # in this file, in the spec table, and in the coverage test's invocation
  # recipes — what its structured result is.
  module CliCommands
    # `aliases` are always FULL alternative spellings of `name`, so a sub-verb's
    # alias reads "project new", never a bare "new" that would look like a
    # top-level command. The two token readers below split that apart for the
    # two slots bin/tasks dispatches on.
    Command = Struct.new(:name, :aliases, :json, :reason, :early, keyword_init: true) do
      def json? = json == true

      # True for `merge-driver`, which Git invokes before any config or store
      # resolution and which therefore dispatches ahead of the registry.
      def early? = early == true

      def words = name.split(" ")
      def dispatch = words.first
      def subcommand? = words.length > 1

      # Tokens accepted in the first argv slot. A sub-verb contributes only its
      # group's name — `tasks new` must not become `tasks project new`.
      def dispatch_tokens = subcommand? ? [dispatch] : [dispatch, *aliases]

      # Tokens accepted in the second slot: "create", plus "new" from the
      # "project new" alias. Empty for a top-level command.
      def subcommand_tokens
        return [] unless subcommand?

        [words[1], *aliases.map { |spelling| spelling.split(" ")[1] }].compact
      end

      def to_h
        base = { name: name, aliases: aliases, json: json? }
        json? ? base : base.merge(json_reason: reason)
      end
    end

    def self.cmd(name, aliases: [], json: true, reason: nil, early: false)
      raise ArgumentError, "#{name}: json: false requires a reason" if !json && (reason.nil? || reason.empty?)
      raise ArgumentError, "#{name}: json: true takes no reason" if json && reason

      group = name.split(" ").first
      aliases.each do |spelling|
        next unless name.include?(" ")
        raise ArgumentError, "#{name}: alias #{spelling.inspect} must spell out the group" unless
          spelling.start_with?("#{group} ")
      end

      Command.new(name: name, aliases: aliases, json: json, reason: reason, early: early).freeze
    end
    private_class_method :cmd

    ALL = [
      # --- Read ---------------------------------------------------------------
      cmd("agenda", aliases: %w[a]),
      cmd("next", aliases: %w[n]),
      cmd("quadrants", aliases: %w[q]),
      cmd("inbox", aliases: %w[i]),
      cmd("projects", aliases: %w[pj]),
      cmd("list", aliases: %w[l]),
      cmd("show", aliases: %w[s]),
      cmd("links", aliases: %w[urls]),
      cmd("open", aliases: %w[o]),
      cmd("id"),
      cmd("check", aliases: %w[k]),

      # --- Projects -----------------------------------------------------------
      cmd("project create", aliases: ["project new"]),
      cmd("project show"),
      cmd("project complete", aliases: ["project done"]),
      cmd("project archive"),
      cmd("project rename"),

      # --- Capture ------------------------------------------------------------
      cmd("capture", aliases: %w[add c]),
      cmd("propose"),

      # --- Update -------------------------------------------------------------
      cmd("approve"),
      cmd("reject"),
      cmd("delegate"),
      cmd("undelegate"),
      cmd("workref", aliases: %w[work-ref]),
      cmd("claim"),
      cmd("release"),
      cmd("done", aliases: %w[complete close d]),
      cmd("due", aliases: %w[deadline reschedule]),
      cmd("schedule"),
      cmd("undate"),
      cmd("state", aliases: %w[mv]),
      cmd("cancel", aliases: %w[drop]),
      cmd("priority", aliases: %w[pri]),
      cmd("retitle", aliases: %w[rename]),
      cmd("tag"),
      cmd("note"),
      cmd("move"),
      cmd("delete"),
      cmd("recur", aliases: %w[repeat every]),
      cmd("lead", aliases: %w[leadtime lead-time]),
      cmd("defer", aliases: %w[snooze]),
      cmd("someday"),
      cmd("activate", aliases: %w[undefer resume]),

      # --- Lifecycle ----------------------------------------------------------
      cmd("archive", aliases: %w[x]),
      cmd("undo"),
      cmd("redo"),
      cmd("config"),
      cmd("help", aliases: %w[-h --help]),
      cmd("-p", aliases: %w[--prompt], json: false,
              reason: "The result is an LLM harness's free-form transcript, not a value this " \
                      "CLI computes; the mutations it makes are readable through the commands " \
                      "that do emit JSON."),
      cmd("merge-driver", json: false, early: true,
                          reason: "Git plumbing. Git supplies the three merge-stage paths and reads " \
                                  "the merged file and the exit code; stdout is not a result surface."),
    ].freeze

    NAMES = ALL.map(&:name).freeze

    # One entry per dispatch slot in bin/tasks — `project create` and `project
    # show` are two commands but one slot.
    DISPATCH_NAMES = ALL.map(&:dispatch).uniq.freeze

    # Dispatched before config/store resolution, so outside the handler table.
    EARLY_NAMES = ALL.select(&:early?).map(&:name).freeze

    # Every typeable first-slot token (canonical names and aliases) → its
    # dispatch name.
    TOKENS = ALL.each_with_object({}) { |c, h| c.dispatch_tokens.each { |t| h[t] = c.dispatch } }.freeze

    JSON_COMMANDS = ALL.select(&:json?).freeze
    OPT_OUTS = ALL.reject(&:json?).freeze

    def self.dispatch_for(token) = TOKENS[token]

    # The sub-verbs of one dispatch slot: {"create" => "project create", "new"
    # => "project create", …}. bin/tasks resolves `project <verb>` through this
    # so a sub-verb cannot exist without a registry entry either.
    def self.subcommands_of(group)
      ALL.each_with_object({}) do |command, h|
        next unless command.subcommand? && command.dispatch == group

        command.subcommand_tokens.each { |token| h[token] = command.name }
      end
    end
    def self.find(name) = ALL.find { |c| c.name == name }
    def self.to_h = { commands: ALL.map(&:to_h) }
  end
end
