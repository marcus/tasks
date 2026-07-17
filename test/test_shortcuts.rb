# frozen_string_literal: true

require_relative "test_helper"
require "tui/shortcuts"
require "tui/modals"
require "tui/app"

class TestShortcuts < Minitest::Test
  S = Tui::Shortcuts
  A = Tui::Ansi

  def changed_entry(entry = S::REGISTRY.first, **changes)
    S::Entry.new(**entry.to_h.merge(changes))
  end

  def test_registry_validates_against_app
    assert S.validate!(Tui::App)
  end

  def test_every_entry_declares_context_handler_availability_and_metadata
    S::REGISTRY.each do |entry|
      if entry.sequences.empty?
        assert entry.palette, "keyless actions must be palette-enabled"
      else
        refute_empty entry.sequences
      end
      refute_empty entry.display_key
      refute_empty entry.description
      refute_empty entry.contexts
      assert_kind_of Symbol, entry.handler
      assert entry.availability
      assert_includes [TrueClass, FalseClass, NilClass, Symbol, Proc], entry.palette.class
      assert_includes [NilClass, Symbol, Hash], entry.form.class
      assert_includes [NilClass, Symbol, Hash], entry.confirmation.class
    end
  end

  def test_context_lookup_keeps_task_actions_in_list_and_detail_only
    assert_equal :complete_selected, S.match("c", :list).handler
    assert_equal :complete_selected, S.match("c", :detail).handler
    assert_nil S.match("c", :modal)
    assert_nil S.match("x", :detail), "list-only archive must not leak into details"
    assert_nil S.match("\e[C", :detail), "list navigation must not leak into details"
  end

  def test_modal_navigation_resolves_independently
    assert_equal :modal_half_down, S.match("\x04", :modal).handler
    assert_equal :modal_half_up, S.match("\x15", :modal).handler
    assert_equal :modal_page_down, S.match("\x06", :modal).handler
    assert_equal :modal_page_down, S.match("\e[6~", :modal).handler
    assert_equal :modal_page_up, S.match("\x02", :modal).handler
    assert_equal :modal_start_filter, S.match("/", :modal).handler
    assert_equal :close_modal, S.match("\e", :modal).handler
    assert_equal :close_modal, S.match("q", :modal).handler
  end

  def test_at_opens_context_palette_from_list
    assert_equal :open_context_palette, S.match("@", :list).handler
    assert_nil S.match("@", :detail)
    assert_nil S.match("@", :modal)
  end

  def test_ordering_bindings_cover_csi_and_escape_prefixed_alt_variants
    up = ["\e[1;3A", "\e\e[A", "\ek"]
    down = ["\e[1;3B", "\e\e[B", "\ej"]
    up.each { |sequence| assert_equal :move_subtree_up, S.match(sequence, :list).handler }
    down.each { |sequence| assert_equal :move_subtree_down, S.match(sequence, :list).handler }
    assert_equal :indent_subtree, S.match(">", :list).handler
    assert_equal :outdent_subtree, S.match("<", :list).handler
  end

  def test_sixth_view_has_a_direct_jump_key
    entry = S.match("6", :list)
    assert_equal :jump_view, entry.handler
    assert_equal "1-6", entry.display_key
  end

  def test_unknown_lookup_context_is_rejected
    error = assert_raises(ArgumentError) { S.entries(:bogus) }
    assert_match(/unknown shortcut context/, error.message)
  end

  def test_global_binding_resolves_in_every_context
    %i[list detail task_edit modal global].each do |context|
      assert_equal :quit, S.match("\x03", context).handler
    end
  end


  def test_task_edit_bindings_are_complete_and_isolated
    assert_equal :start_task_edit, S.match("e", :detail).handler
    assert_equal :focus_prompt, S.match("\t", :detail).handler
    assert_equal :start_task_edit_last, S.match("\e[Z", :detail).handler
    assert_equal :task_edit_input, S.match("\t", :task_edit).handler
    assert_equal :task_edit_input, S.match("\x13", :task_edit).handler
    assert_equal :task_edit_input, S.match("\x0f", :task_edit).handler
    assert_equal :grow_task_panel, S.match("\x0b", :task_edit).handler
    assert_equal :shrink_task_panel, S.match("\x0c", :task_edit).handler
    assert_nil S.match("j", :task_edit), "list movement must not leak into editor dispatch"
  end

  def test_global_dispatch_precedes_every_input_mode
    %i[list prompt form palette filter modal modal_filter].each do |mode|
      app = Tui::App.allocate
      ui = Tui::UiState.new(view: :agenda)
      case mode
      when :form
        ui.form = Struct.new(:return_mode).new(:list)
      when :palette
        ui.action_palette = Struct.new(:return_mode).new(:list)
      when :modal, :modal_filter
        ui.modal = Struct.new(:kind, :filterable?).new(:help, true)
        ui.mode = :modal
      end
      ui.mode = mode unless mode == :list || mode == :modal
      app.instance_variable_set(:@ui, ui)
      app.instance_variable_set(:@quit, false)
      queue = Object.new
      queue.define_singleton_method(:work?) { false }
      app.instance_variable_set(:@agent_queue, queue)
      app.send(:handle_key, "\x03")
      assert app.instance_variable_get(:@quit), "ctrl-c did not quit from #{mode} mode"
    end
  end

  def test_validation_rejects_duplicate_key_in_same_context
    duplicate = changed_entry(S::REGISTRY.first, handler: :select_next)
    error = assert_raises(ArgumentError) { S.validate!(nil, entries: [S::REGISTRY.first, duplicate]) }
    assert_match(/duplicate shortcut/, error.message)
  end

  def test_validation_rejects_duplicate_sequences_inside_one_entry
    duplicate = changed_entry(sequences: ["k", "k"])
    error = assert_raises(ArgumentError) { S.validate!(nil, entries: [duplicate]) }
    assert_match(/sequences must be unique/, error.message)
  end

  def test_validation_allows_palette_only_action_and_rejects_unreachable_action
    palette_only = changed_entry(sequences: [], display_key: "palette", palette: true)
    assert S.validate!(nil, entries: [palette_only])

    unreachable = changed_entry(sequences: [], palette: false)
    error = assert_raises(ArgumentError) { S.validate!(nil, entries: [unreachable]) }
    assert_match(/must be palette-enabled/, error.message)
  end

  def test_validation_allows_same_key_in_modal_and_palette_only_detail_context
    modal = changed_entry(contexts: [:modal])
    detail = changed_entry(contexts: [:detail], handler: :select_next)
    assert S.validate!(nil, entries: [modal, detail])
  end

  def test_validation_allows_same_key_in_list_and_plain_modal
    list = changed_entry(contexts: [:list])
    modal = changed_entry(contexts: [:modal], handler: :modal_up)
    assert S.validate!(nil, entries: [list, modal])
  end

  def test_validation_rejects_missing_handler_and_availability_hook
    missing_handler = changed_entry(handler: :not_an_app_handler)
    error = assert_raises(ArgumentError) { S.validate!(Tui::App, entries: [missing_handler]) }
    assert_match(/missing shortcut handler/, error.message)

    missing_availability = changed_entry(availability: :not_an_app_predicate)
    error = assert_raises(ArgumentError) { S.validate!(Tui::App, entries: [missing_availability]) }
    assert_match(/missing shortcut availability/, error.message)

    missing_palette_availability = changed_entry(palette: :not_an_app_predicate)
    error = assert_raises(ArgumentError) { S.validate!(Tui::App, entries: [missing_palette_availability]) }
    assert_match(/missing shortcut palette availability/, error.message)
  end

  def test_validation_rejects_invalid_metadata
    bad_form = changed_entry(form: Object.new)
    error = assert_raises(ArgumentError) { S.validate!(nil, entries: [bad_form]) }
    assert_match(/form metadata/, error.message)

    bad_confirmation = changed_entry(confirmation: { label: "missing kind" })
    error = assert_raises(ArgumentError) { S.validate!(nil, entries: [bad_confirmation]) }
    assert_match(/confirmation metadata/, error.message)
  end

  def test_validation_rejects_palette_handler_that_requires_the_original_key
    jump = S::REGISTRY.find { |entry| entry.handler == :jump_view }
    invalid = changed_entry(jump, palette: true)
    error = assert_raises(ArgumentError) { S.validate!(Tui::App, entries: [invalid]) }
    assert_match(/must not require a key/, error.message)
  end

  def test_palette_entries_are_contextual_available_and_executable_without_a_key
    app = Tui::App.allocate
    item = Struct.new(:scheduled, :deadline).new(nil, nil)
    app.define_singleton_method(:current_item) { item }
    app.define_singleton_method(:selected_action_available?) { true }
    app.define_singleton_method(:recurrence_action_available?) { false }
    app.define_singleton_method(:link_action_available?) { false }
    app.define_singleton_method(:action_available?) { true }

    entries = S.palette_entries(:detail, app)
    handlers = entries.map(&:handler)
    assert_includes handlers, :complete_selected
    refute_includes handlers, :open_recur_popup
    refute_includes handlers, :open_link
    refute_includes handlers, :open_action_palette
    assert entries.all? { |entry| app.method(entry.handler).arity.zero? }
  end

  def test_unavailable_binding_consumes_key_without_calling_handler
    app = Tui::App.allocate
    app.instance_variable_set(:@quit, false)
    unavailable = changed_entry(handler: :quit, availability: ->(_receiver) { false })
    S.stub(:match, unavailable) do
      assert app.send(:dispatch_action, "q", :list)
    end
    refute app.instance_variable_get(:@quit)
  end

  def test_modal_filter_binding_is_unavailable_but_consumed_in_task_detail
    modal = Struct.new(:filterable?).new(false)
    app = Tui::App.allocate
    ui = Tui::UiState.new(view: :agenda)
    ui.modal = modal
    app.instance_variable_set(:@ui, ui)
    entry = S.match("/", :modal)

    refute S.available?(entry, app)
    assert app.send(:dispatch_action, "/", :modal)
  end

  def test_help_modal_is_generated_from_registry_with_context_labels
    help = Tui::Modals.help
    text = help[:lines].map { |line| A.strip(line) }.join("\n")
    S::REGISTRY.each do |entry|
      assert_includes text, entry.description
      assert_includes text, entry.display_key
    end
    assert_includes text, "in the task list"
    assert_includes text, "in task details"
    assert_includes text, "while editing a task"
    assert_includes text, "in a modal"
    assert_includes text, "everywhere"
    assert_equal "keyboard shortcuts", help[:title]
  end
end
