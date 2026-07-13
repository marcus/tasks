# frozen_string_literal: true

require "securerandom"
require_relative "task_edit_form"
require_relative "../tasks/patch_result"
require_relative "../tasks/task_patch"

module Tui
  # Coordinates one durable task-edit pass. It translates TermForm transitions
  # into typed Store patches while retaining all recoverable local state.
  class TaskEditorSession
    Outcome = Data.define(:status, :form_transition, :patch_result, :message, :data) do
      def initialize(status:, form_transition: nil, patch_result: nil, message: nil, data: nil)
        super(status: status.to_sym, form_transition: form_transition,
              patch_result: patch_result, message: message, data: data)
      end

      def finished? = status == :finished
      def conflict? = status == :conflict
      def missing? = status == :missing
      def confirmation? = status == :confirmation
      def invalid? = status == :invalid
      def changed? = patch_result&.changed? || status == :changed
      def handled? = status != :unhandled
    end

    Confirmation = Data.define(
      :token, :field, :value, :message, :summary, :request, :finish, :expectations
    )
    Conflict = Data.define(:field, :local_value, :fresh_value, :snapshot, :result)

    CTRL_S = "\x13"
    CTRL_O = "\x0f"

    attr_reader :store, :target_id, :snapshot, :coalesce_key, :edit_form,
                :pending_confirmation, :conflict, :pending_revert, :kept_copy,
                :last_result

    alias form_adapter edit_form

    def initialize(store:, target: nil, target_id: nil, coalesce_key: nil, **form_options)
      @store = store
      @target_id = stable_id(target_id || target)
      raise ArgumentError, "task editor requires a stable target id" if @target_id.empty?

      @coalesce_key = (coalesce_key || SecureRandom.hex(16)).to_s.dup.freeze
      @pending_confirmation = nil
      @pending_revert = nil
      @conflict = nil
      @kept_copy = nil
      @last_result = nil
      @snapshot = store.edit_snapshot(@target_id)
      @missing = @snapshot.nil?
      @edit_form = TaskEditForm.new(snapshot: @snapshot, store: store, **form_options) if @snapshot
    end

    def form = edit_form&.form
    def missing? = @missing
    def inert? = missing?
    def dirty?(field = nil) = edit_form ? edit_form.dirty?(field) : false
    def focused_key = edit_form&.focused_key
    def pending_blur = edit_form&.pending_blur
    def render_model = edit_form&.render_model
    def read_only = edit_form ? edit_form.read_only : { id: target_id, closed: nil }.freeze
    def copy_value = kept_copy || (edit_form && focused_key && edit_form.value(focused_key))

    def handle(input)
      return outcome(:missing, message: "Task no longer exists", data: copy_value) if missing?

      raw = raw_input(input)
      return save if raw == CTRL_S
      return finish if raw == CTRL_O
      return handle_confirmation(input) if pending_confirmation

      second_revert = pending_revert && escape?(input) && pending_revert == focused_key
      @pending_revert = nil unless escape?(input)
      return revert_dirty_field if second_revert && !picker_open?

      transition = form.handle(input)
      case transition.type
      when :commit_requested then commit_request(transition.request)
      when :invalid
        outcome(:invalid, form_transition: transition, message: first_error(transition.errors))
      when :cancel_requested then handle_cancel(transition)
      when :unhandled then outcome(:unhandled, form_transition: transition)
      else
        @pending_revert = nil if transition.changed? || transition.focus_changed?
        outcome(transition.type, form_transition: transition)
      end
    end

    # Save the focused field in place. The form keeps focus after acceptance.
    def save
      return outcome(:missing, message: "Task no longer exists", data: copy_value) if missing?
      return outcome(:confirmation, data: pending_confirmation) if pending_confirmation

      transition = form.request_commit(intended_focus: focused_key)
      return outcome(:invalid, form_transition: transition, message: first_error(transition.errors)) if transition.invalid?

      commit_request(transition.request)
    end

    # Finish immediately when clean, or finish only after the focused buffer is
    # accepted. A refusal always leaves the editor active and recoverable.
    def finish
      return outcome(:missing, message: "Task no longer exists", data: copy_value) if missing?
      return outcome(:confirmation, data: pending_confirmation) if pending_confirmation
      return outcome(:finished) unless dirty?(focused_key)

      transition = form.request_commit(intended_focus: focused_key)
      return outcome(:invalid, form_transition: transition, message: first_error(transition.errors)) if transition.invalid?

      commit_request(transition.request, finish: true)
    end

    # Refreshes clean fields and their expectations. Dirty buffers retain both
    # their text and the expectation against which the eventual patch compares.
    def refresh
      return outcome(:confirmation, data: pending_confirmation) if pending_confirmation

      fresh = store.edit_snapshot(target_id)
      return become_missing unless fresh

      @snapshot = fresh
      edit_form.refresh_snapshot(fresh)
      outcome(:refreshed)
    end

    def confirm!
      confirmation = pending_confirmation
      return outcome(:handled) unless confirmation

      result = persist(
        confirmation.request,
        value: confirmation.value,
        confirmation: {
          token: confirmation.token,
          expected: confirmation.expectations,
        },
        finish: confirmation.finish,
        retain_pending_on_conflict: true,
      )
      @pending_confirmation = nil unless result.conflict?
      result
    end
    alias confirm_confirmation! confirm!

    def cancel_confirmation!
      confirmation = pending_confirmation
      return outcome(:handled) unless confirmation

      @pending_confirmation = nil
      transition = edit_form.reject_commit(token: confirmation.request.token)
      outcome(:confirmation_cancelled, form_transition: transition,
              message: "Change cancelled; local value retained")
    end
    alias reject_confirmation! cancel_confirmation!

    # Discard the conflicting local field and adopt the latest persisted value.
    def reload_conflict!
      return outcome(:handled) unless conflict

      field = conflict.field
      fresh = store.edit_snapshot(target_id)
      return become_missing unless fresh

      if pending_confirmation
        edit_form.reject_commit(token: pending_confirmation.request.token)
        @pending_confirmation = nil
      end
      @snapshot = fresh
      edit_form.reload_field(field, fresh)
      edit_form.refresh_snapshot(fresh)
      @conflict = nil
      @kept_copy = nil
      outcome(:conflict_reloaded, data: field)
    end
    alias reload_field! reload_conflict!

    # "Revert local" is deliberately non-destructive to persisted data: it
    # means abandon the local conflicting buffer and show the live field.
    def revert_local!
      reload_conflict!
    end
    alias revert_conflict! revert_local!

    # Preserve the local value as an explicit copy payload. The conflict stays
    # active until reload/revert, so this action can never become an overwrite.
    def keep_for_copy!
      return outcome(:handled) unless conflict

      @kept_copy = immutable_copy(conflict.local_value)
      outcome(:copy_kept, message: "Local value retained for copy", data: kept_copy)
    end
    alias keep_conflict_copy! keep_for_copy!

    private

    def commit_request(request, finish: false)
      semantic = edit_form.semantic_value(request.field_key, request.proposed_value)
      if (confirmation = consequence_for(request, semantic, finish: finish))
        @pending_confirmation = confirmation
        return outcome(:confirmation, form_transition: nil,
                       message: confirmation.message, data: confirmation)
      end

      persist(request, value: semantic, finish: finish)
    end

    def persist(request, value:, confirmation: nil, finish: false,
                retain_pending_on_conflict: false)
      patch = Tasks::TaskPatch.new(
        id: target_id,
        field: request.field_key,
        value: value,
        expected: edit_form.expected_for(request.field_key),
        coalesce_key: coalesce_key,
        confirmation: confirmation,
      )
      result = store.patch_task!(patch)
      @last_result = result

      if result.ok?
        fresh = result.snapshot || store.edit_snapshot(target_id)
        return become_missing unless fresh

        @snapshot = fresh
        @conflict = nil
        transition = edit_form.accept_commit(fresh, token: request.token)
        return outcome(:finished, form_transition: transition, patch_result: result,
                       data: result.summary) if finish

        return outcome(result.status, form_transition: transition,
                       patch_result: result, data: result.summary)
      end

      case result.status
      when :conflict
        transition = unless retain_pending_on_conflict
                       edit_form.reject_commit(
                         errors: { request.field_key => ["Changed externally; reload, revert, or keep for copy"] },
                         token: request.token,
                       )
                     end
        local = edit_form.value(request.field_key)
        fresh_value = result.snapshot && snapshot_value(result.snapshot, request.field_key)
        @conflict = Conflict.new(field: request.field_key, local_value: immutable_copy(local),
                                 fresh_value: immutable_copy(fresh_value),
                                 snapshot: result.snapshot, result: result)
        if result.snapshot
          @snapshot = result.snapshot
          edit_form.refresh_snapshot(result.snapshot)
        end
        outcome(:conflict, form_transition: transition, patch_result: result,
                message: "Field changed externally", data: @conflict)
      when :missing
        edit_form.reject_commit(message: "Task no longer exists", token: request.token)
        become_missing(result)
      else
        errors = result.field_errors.empty? ? { request.field_key => result.errors } : result.field_errors
        errors = { request.field_key => [result.status.to_s.tr("_", " ")] } if errors.values.flatten.empty?
        transition = edit_form.reject_commit(errors: errors, token: request.token)
        if result.snapshot
          @snapshot = result.snapshot
          edit_form.refresh_snapshot(result.snapshot)
        end
        outcome(:invalid, form_transition: transition, patch_result: result,
                message: result.errors.first || result.status.to_s.tr("_", " "), data: result.summary)
      end
    end

    def consequence_for(request, value, finish:)
      field = request.field_key
      old = snapshot_value(snapshot, field)
      summary = nil
      message = nil

      case field
      when :location
        return nil if value == old
        from = snapshot.metadata[:parent_title] || old
        destination = edit_form.option_label(:location, value)
        size = Array(snapshot.metadata[:subtree_ids]).length
        message = "Move this task subtree (#{size} task#{size == 1 ? "" : "s"}) from #{from} to #{destination}?"
        summary = { from: old, to: value, subtree_ids: snapshot.metadata[:subtree_ids] }
      when :state
        return nil if value == old
        if value == "DONE" && snapshot.recurrence
          message = "Completing this recurring task advances its recurrence. Continue?"
        elsif value == "DONE"
          descendants = open_descendant_ids
          message = descendants.empty? ? "Mark this task done?" : "Mark this task done and cascade to #{descendants.length} descendant(s)?"
        elsif value == "CANCELLED"
          message = "Cancel this task?"
        else
          message = "Change state from #{old} to #{value}?"
        end
        summary = { from: old, to: value, subtree_ids: snapshot.metadata[:subtree_ids] }
      when :recurrence
        return nil if value == old
        message = value ? "Set recurrence to #{value}?" : "Clear recurrence?"
        summary = { from: old, to: value }
      when :scheduled, :deadline
        other = field == :scheduled ? snapshot.deadline : snapshot.scheduled
        if value.nil? && old && other.nil? && snapshot.recurrence
          message = "Clearing the final date also clears recurrence. Continue?"
          summary = { field: field, clears_recurrence: true }
        elsif value && snapshot.state == "INBOX"
          message = "Adding this date promotes the task from INBOX to TODO. Continue?"
          summary = { field: field, promotes_to: "TODO" }
        end
      end
      return nil unless message

      Confirmation.new(token: SecureRandom.hex(12), field: field, value: immutable_copy(value),
                       message: message.freeze, summary: immutable_copy(summary),
                       request: request, finish: finish,
                       expectations: confirmation_expectations(field, snapshot, summary))
    end

    def handle_confirmation(input)
      raw = raw_input(input)
      return confirm! if ["y", "Y", "\r", "\n"].include?(raw)
      return cancel_confirmation! if ["n", "N", "\e"].include?(raw)

      outcome(:confirmation, message: pending_confirmation.message, data: pending_confirmation)
    end

    def handle_cancel(transition)
      if dirty?(focused_key)
        @pending_revert = focused_key
        outcome(:revert_pending, form_transition: transition,
                message: "Press Escape again to discard this field")
      else
        outcome(:finished, form_transition: transition)
      end
    end

    def revert_dirty_field
      field = focused_key
      edit_form.revert_field(field)
      @pending_revert = nil
      @conflict = nil if conflict&.field == field
      outcome(:reverted, message: "Discarded unsaved #{field}", data: field)
    end

    def picker_open?
      field = focused_key && form.field(focused_key)
      field&.respond_to?(:open?) && field.open?
    end

    def become_missing(result = nil)
      @missing = true
      @pending_confirmation = nil
      outcome(:missing, patch_result: result, message: "Task no longer exists", data: copy_value)
    end

    def snapshot_value(value, field)
      value[normalize_field(field)]
    end

    def normalize_field(field)
      field = field.to_sym
      field == :body ? :body : (field == :recur ? :recurrence : field)
    end

    def stable_id(target)
      value = target.respond_to?(:id) ? target.id : target
      value.to_s.dup.freeze
    end

    def raw_input(input)
      return input if input.is_a?(String)
      return input.raw if input.respond_to?(:raw) && input.raw
      return TermForm::Event::KEY_BYTES[input.key] if input.respond_to?(:key) && input.key.is_a?(Symbol)
      return input.text if input.respond_to?(:text)

      nil
    end

    def escape?(input) = raw_input(input) == "\e"

    def first_error(errors)
      errors&.values&.flatten&.first
    end

    def open_descendant_ids
      ids = Array(snapshot.metadata[:subtree_ids]).drop(1)
      store.items.select { |item| ids.include?(item.id) && item.open? }.map(&:id)
    end

    def confirmation_expectations(field, value, summary)
      owned = {}
      values = {}
      predicates = {}
      case field
      when :location, :state
        owned[field] = value.expected_for(field)
      when :recurrence
        owned[:recurrence] = value.expected_for(:recurrence)
        predicates[:any_live_date] = true
      when :scheduled, :deadline
        owned[field] = value.expected_for(field)
        if summary[:clears_recurrence]
          other = field == :scheduled ? :deadline : :scheduled
          owned[:recurrence] = value.expected_for(:recurrence)
          predicates[:date_presence] = { other => false }
        elsif summary[:promotes_to]
          values[:state] = value.state
        end
      end
      immutable_copy(owned: owned, values: values, predicates: predicates)
    end

    def immutable_copy(value)
      case value
      when Array then value.map { |item| immutable_copy(item) }.freeze
      when Hash then value.to_h { |key, item| [key, immutable_copy(item)] }.freeze
      when String then value.dup.freeze
      else value
      end
    end

    def outcome(status, form_transition: nil, patch_result: nil, message: nil, data: nil)
      Outcome.new(status: status, form_transition: form_transition,
                  patch_result: patch_result, message: message, data: data)
    end
  end
end
