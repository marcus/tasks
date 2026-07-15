# frozen_string_literal: true

module Tasks
  # Immutable result shared by Store mutations. Refusals are values rather than
  # exceptions so adapters can preserve their recovery state and map the same
  # outcome consistently for a CLI, TUI, or a future transport.
  class MutationResult
    STATUSES = %i[
      ok no_change not_found stale invalid conflict cycle too_deep store_invalid unavailable
    ].freeze

    # `missing` was the patch-only spelling before Store results became a
    # shared protocol. Accept it at construction time and retain #missing? so
    # existing editor code can migrate at its own pace, but always expose the
    # canonical public status.
    STATUS_ALIASES = { missing: :not_found }.freeze

    CLI_EXIT_CODES = {
      ok: 0,
      no_change: 0,
      not_found: 2,
      stale: 1,
      invalid: 1,
      conflict: 1,
      cycle: 1,
      too_deep: 1,
      store_invalid: 1,
      unavailable: 1,
    }.freeze

    TUI_STATUSES = {
      ok: :ok,
      no_change: :no_change,
      not_found: :missing,
      stale: :conflict,
      invalid: :invalid,
      conflict: :conflict,
      cycle: :invalid,
      too_deep: :invalid,
      store_invalid: :invalid,
      unavailable: :invalid,
    }.freeze

    TUI_MESSAGES = {
      not_found: "Task no longer exists",
      stale: "Field changed externally",
      invalid: "invalid",
      conflict: "Field changed externally",
      cycle: "cycle",
      too_deep: "too deep",
      store_invalid: "task list failed validation",
      unavailable: "task list unavailable",
    }.freeze

    attr_reader :status, :snapshot, :read_snapshot, :errors, :field_errors,
                :form_errors, :touched_ids, :summary, :store_revision

    def initialize(status:, snapshot: nil, read_snapshot: nil, errors: [], field_errors: {},
                   form_errors: nil, touched_ids: [], summary: nil, store_revision: nil)
      @status = self.class.normalize_status(status)

      @snapshot = snapshot
      @read_snapshot = read_snapshot
      @errors = immutable(Array(errors))
      @field_errors = immutable(field_errors)
      @form_errors = immutable(form_errors.nil? ? Array(errors) : Array(form_errors))
      @touched_ids = immutable(Array(touched_ids))
      @summary = immutable(summary)
      @store_revision = immutable(store_revision)
      freeze
    end

    def ok? = status == :ok || status == :no_change
    def changed? = status == :ok
    def no_change? = status == :no_change
    def not_found? = status == :not_found
    def missing? = not_found?
    def stale? = status == :stale
    def conflict? = status == :conflict
    def invalid? = status == :invalid
    def cycle? = status == :cycle
    def too_deep? = status == :too_deep
    def store_invalid? = status == :store_invalid
    def unavailable? = status == :unavailable

    # Adapter-only interpretations. They deliberately do not alter #status:
    # commands can reason about one vocabulary while each adapter preserves its
    # established external behavior during the staged migration.
    def cli_exit_code = CLI_EXIT_CODES.fetch(status)
    def tui_status = TUI_STATUSES.fetch(status)
    def tui_message = TUI_MESSAGES.fetch(status, status.to_s.tr("_", " "))

    def self.normalize_status(status)
      normalized = STATUS_ALIASES.fetch(status.to_sym, status.to_sym)
      return normalized if STATUSES.include?(normalized)

      raise ArgumentError, "unknown mutation status #{status.inspect}"
    end

    private

    def immutable(value)
      case value
      when Hash
        value.each_with_object({}) { |(key, item), copy| copy[key] = immutable(item) }.freeze
      when Array
        value.map { |item| immutable(item) }.freeze
      when String
        value.dup.freeze
      else
        value.freeze
      end
    end
  end

  # Keep the patch name available while the other Store mutations gradually
  # adopt the common result. It is an alias, not a wrapper, so all immutability
  # and adapter mappings are identical.
  PatchResult = MutationResult
end
