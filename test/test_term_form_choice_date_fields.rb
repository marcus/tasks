# frozen_string_literal: true

require "minitest/autorun"
require_relative "../lib/term_form"

class TestTermFormChoiceDateFields < Minitest::Test
  TODAY = Date.new(2026, 7, 13)

  def test_select_searches_and_selects_without_leaking_picker_keys_to_traversal
    field = TermForm::Fields::Select.new(
      key: :state,
      value: "TODO",
      options: [["INBOX", "Inbox"], ["TODO", "To do"], ["WAITING", "Waiting"]],
    )
    form = form_with(field, TermForm::Fields::Input.new(key: :after))

    assert form.handle("w").handled?
    assert field.open?
    assert_equal ["WAITING"], field.filtered_options(form.context).map(&:value)
    assert form.handle("\r").changed?
    assert_equal "WAITING", form.value(:state)
    refute field.open?

    transition = form.handle("\t")
    assert transition.commit_requested?
    assert_equal :after, transition.request.intended_focus
  end

  def test_select_keeps_a_vanished_dynamic_selection_and_marks_it_invalid
    source = [["A", "Alpha"], ["B", "Beta"]]
    field = TermForm::Fields::Select.new(key: :choice, value: "B", options: -> { source })
    form = form_with(field, TermForm::Fields::Input.new(key: :after))

    source.replace([["A", "Alpha"]])
    errors = form.validate

    assert_equal "B", form.value(:choice)
    assert_equal ["selection is no longer available"], errors[:choice]
    assert form.render_model.focused_row.metadata[:invalid_selection]
    assert_equal ["A"], form.render_model.focused_row.metadata[:options].map { |option| option[:value] }
  end

  def test_select_escape_closes_search_before_requesting_form_cancel
    field = TermForm::Fields::Select.new(key: :choice, value: "A", options: %w[A B])
    form = form_with(field)

    form.handle("b")
    assert form.handle("\e").handled?
    refute field.open?
    assert_equal "", field.query
    assert form.handle("\e").cancel_requested?
  end

  def test_choice_fields_honor_decoded_keys_and_types_before_raw_aliases
    key_map = TermForm::KeyMap.new({
      "j" => TermForm::Event.key(:down),
      "n" => TermForm::Event.key(:return),
      "q" => TermForm::Event.key(:escape),
      "o" => TermForm::Event.new(:commit),
      "c" => TermForm::Event.new(:cancel),
      "\e[B" => TermForm::Event.new(:cancel),
    }, defaults: false)
    select = TermForm::Fields::Select.new(key: :choice, value: "A", options: %w[A B])
    form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, fields: [select])], key_map: key_map,
    )

    assert form.handle("o").handled?
    assert select.open?
    assert_equal "", select.query
    assert form.handle("\e[B").handled?, "decoded cancel type must beat raw down"
    refute select.open?
    assert_equal "", select.query

    form.handle("o")
    assert form.handle("c").handled?
    refute select.open?

    assert form.handle("j").handled?
    assert_equal 1, select.highlight_index
    assert_equal "", select.query
    assert form.handle("n").changed?
    assert_equal "B", form.value(:choice)

    multi = TermForm::Fields::MultiSelect.new(key: :tokens, options: %w[A B])
    multi_form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, fields: [multi])], key_map: key_map,
    )
    multi_form.handle("j")
    assert multi_form.handle("n").changed?
    assert_equal ["B"], multi_form.value(:tokens)
    assert_equal "", multi.query
    multi_form.handle("j")
    assert multi_form.handle("q").handled?
    assert_equal "", multi.query
  end

  def test_confirm_and_date_picker_honor_decoded_right_down_return_and_cancel
    key_map = TermForm::KeyMap.new({
      "n" => TermForm::Event.key(:right),
      "y" => TermForm::Event.key(:left),
      "r" => TermForm::Event.key(:return),
      "j" => TermForm::Event.key(:down),
      "l" => TermForm::Event.key(:right),
      "q" => TermForm::Event.key(:escape),
    }, defaults: false)
    confirm = TermForm::Fields::Confirm.new(key: :confirm, value: false)
    confirm_form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, fields: [confirm])], key_map: key_map,
    )

    assert confirm_form.handle("n").changed?, "decoded right must beat raw n"
    assert_equal true, confirm_form.value(:confirm)
    assert confirm_form.handle("y").changed?, "decoded left must beat raw y"
    assert_equal false, confirm_form.value(:confirm)
    assert confirm_form.handle("r").changed?
    assert_equal true, confirm_form.value(:confirm)

    date = date_field(value: TODAY)
    date_form = TermForm::Form.new(
      groups: [TermForm::Group.new(key: :main, fields: [date])], key_map: key_map,
    )
    assert date_form.handle("r").handled?
    date_form.handle("j")
    date_form.handle("l")
    assert_equal TODAY + 8, date.picker_date
    assert date_form.handle("q").handled?
    refute date.picker_open?
    assert_equal TODAY, date_form.value(:date)
    assert_equal TODAY.iso8601, date.text

    date_form.handle("r")
    date_form.handle("l")
    assert date_form.handle("r").changed?
    assert_equal TODAY + 1, date_form.value(:date)
    assert_equal (TODAY + 1).iso8601, date.text
  end

  def test_dynamic_option_shrink_clamps_highlight_for_metadata_and_selection
    source = %w[A B C]
    field = TermForm::Fields::Select.new(key: :choice, value: "A", options: -> { source })
    form = form_with(field)

    form.handle("\e[B")
    form.handle("\e[B")
    assert_equal 2, field.highlight_index
    source.replace(%w[A B])

    options = form.render_model.focused_row.metadata[:options]
    assert_equal 1, field.highlight_index
    assert_equal [false, true], options.map { |option| option[:highlighted] }
    assert form.handle("\r").changed?
    assert_equal "B", form.value(:choice)
  end

  def test_static_options_and_suggestions_are_snapshotted_but_callables_stay_dynamic
    label = +"Alpha"
    note = +"original"
    static_options = [{ value: +"A", label: label, metadata: { note: note } }]
    static_suggestions = [+"today"]
    select = TermForm::Fields::Select.new(key: :choice, value: "A", options: static_options)
    date = date_field(suggestions: static_suggestions)
    form = form_with(select, date)

    label.replace("changed")
    note.replace("changed")
    static_options << "B"
    static_suggestions.first.replace("changed")
    static_suggestions << "tomorrow"

    option = select.options(form.context).fetch(0)
    assert_equal "Alpha", option.label
    assert_equal "original", option.metadata[:note]
    assert option.frozen?
    assert option.value.frozen?
    assert option.label.frozen?
    assert option.metadata.frozen?
    suggestions = date.metadata_for(nil, form.context)[:suggestions]
    assert_equal ["today"], suggestions
    assert suggestions.frozen?
    assert suggestions.first.frozen?

    dynamic_options = %w[A]
    dynamic_suggestions = %w[today]
    live_select = TermForm::Fields::Select.new(key: :live_choice, value: "A", options: -> { dynamic_options })
    live_date = date_field(suggestions: -> { dynamic_suggestions })
    live_form = form_with(live_select, live_date)
    dynamic_options << "B"
    dynamic_suggestions << "tomorrow"
    assert_equal %w[A B], live_select.options(live_form.context).map(&:value)
    assert_equal %w[today tomorrow], live_date.metadata_for(nil, live_form.context)[:suggestions]
  end

  def test_multi_select_normalizes_ordered_tokens_deduplicates_and_creates
    normalizer = ->(token) { token.to_s.sub(/\A@?/, "@").downcase }
    field = TermForm::Fields::MultiSelect.new(
      key: :contexts,
      value: ["Home", "@home", "Work"],
      options: ["@home", "@work"],
      creatable: true,
      normalize: normalizer,
    )
    form = form_with(field)

    assert_equal %w[@home @work], form.value(:contexts)
    %w[E r r a n d s].each { |character| form.handle(character) }
    assert form.handle("\r").changed?
    assert_equal %w[@home @work @errands], form.value(:contexts)

    assert form.handle("\x7f").changed?
    assert_equal %w[@home @work], form.value(:contexts)
    assert_equal %w[@home @work], form.render_model.focused_row.metadata[:tokens]
  end

  def test_non_creatable_multi_select_reports_vanished_tokens
    available = %w[one two]
    field = TermForm::Fields::MultiSelect.new(
      key: :tokens, value: %w[one two], options: ->(_context) { available }, creatable: false,
    )
    form = form_with(field)

    available.delete("two")

    assert_equal %w[one two], form.value(:tokens)
    assert_equal ["selection is no longer available: two"], form.validate[:tokens]
  end

  def test_choice_and_date_baselines_use_the_same_normalization_as_values
    tokens = TermForm::Fields::MultiSelect.new(
      key: :tokens, value: %w[one], baseline: %w[one one], options: %w[one],
    )
    date = date_field(value: TODAY, baseline: TODAY.iso8601)
    form = form_with(tokens, date)

    assert_equal %w[one], form.baseline(:tokens)
    assert_equal TODAY, form.baseline(:date)
    refute form.dirty?(:tokens)
    refute form.dirty?(:date)
  end

  def test_confirm_supports_boolean_keys_and_semantic_consequence
    field = TermForm::Fields::Confirm.new(
      key: :deferred,
      value: false,
      yes_label: "Defer",
      no_label: "Active",
      consequence: ->(context) { context[:deferred] ? "Hidden from active lists" : nil },
    )
    form = form_with(field)

    assert form.handle("y").changed?
    assert_equal true, form.value(:deferred)
    metadata = form.render_model.focused_row.metadata
    assert_equal "Hidden from active lists", metadata[:consequence]
    assert_equal [false, true], metadata[:options].map { |option| option[:value] }
    assert metadata[:options].last[:selected]
    assert form.handle("n").changed?
    assert_equal false, form.value(:deferred)
  end

  def test_date_input_uses_injected_parser_formatter_today_and_preview
    parser = lambda do |text, today|
      case text
      when "tomorrow" then today + 1
      else Date.iso8601(text)
      end
    end
    formatter = ->(date) { date.strftime("%Y-%m-%d (%a)") }
    field = date_field(parser: parser, formatter: formatter)
    form = form_with(field)

    "tomorrow".each_char { |character| form.handle(character) }

    assert_equal TODAY + 1, form.value(:date)
    assert_equal "tomorrow", field.text
    assert_equal "2026-07-14 (Tue)", field.preview
    assert_equal "2026-07-14 (Tue)", form.render_model.focused_row.metadata[:preview]
  end

  def test_date_parser_standard_errors_become_validation_errors_but_fatal_exceptions_escape
    field = date_field(parser: ->(_text) { raise RuntimeError, "parser unavailable" })
    form = form_with(field)

    assert form.handle("x").changed?
    assert_equal "x", form.value(:date)
    assert_equal ["is not a valid date"], form.validate[:date]

    fatal = date_field(parser: ->(_text) { raise Interrupt, "stop" })
    fatal_form = form_with(fatal)
    assert_raises(Interrupt) { fatal_form.handle("x") }
  end

  def test_empty_date_is_an_explicit_unset_and_invalid_text_blocks_commit
    field = date_field(value: TODAY)
    form = form_with(field, TermForm::Fields::Input.new(key: :after))

    field.cursor.times { form.handle("\x7f") }
    assert_nil form.value(:date)
    assert_empty form.validate

    form.handle("x")
    assert_equal "x", form.value(:date)
    assert_equal ["is not a valid date"], form.validate[:date]
    assert form.handle("\t").invalid?
  end

  def test_date_picker_escape_return_and_day_week_today_navigation
    field = date_field(value: Date.new(2026, 7, 14))
    form = form_with(field)

    assert form.handle("\r").handled?
    assert field.picker_open?
    assert_equal Date.new(2026, 7, 14), field.picker_date
    form.handle("\e[C")
    form.handle("\e[B")
    assert_equal Date.new(2026, 7, 22), field.picker_date
    form.handle("t")
    assert_equal TODAY, field.picker_date
    assert form.handle("\e").handled?
    refute field.picker_open?
    assert_equal Date.new(2026, 7, 14), form.value(:date)

    form.handle("\r")
    form.handle("\e[D")
    assert form.handle("\r").changed?
    assert_equal Date.new(2026, 7, 13), form.value(:date)
    assert_equal "2026-07-13", field.text
  end

  def test_date_picker_clamps_month_navigation_and_crosses_leap_boundaries
    field = date_field(value: Date.new(2024, 1, 31))
    form = form_with(field)

    form.handle("\r")
    form.handle("\e[6~")
    assert_equal Date.new(2024, 2, 29), field.picker_date
    form.handle("\e[6~")
    assert_equal Date.new(2024, 3, 29), field.picker_date
    form.handle("\e[5~")
    assert_equal Date.new(2024, 2, 29), field.picker_date
  end

  def test_date_picker_semantics_and_narrow_render_stay_inside_cell_budget
    field = date_field(value: Date.new(2026, 7, 13), suggestions: ["today", "tomorrow", "金曜日"])
    form = form_with(field)
    form.handle("\r")

    metadata = form.render_model.focused_row.metadata
    assert_equal Date.new(2026, 7, 13), metadata[:picker][:selected]
    assert_equal 6, metadata[:picker][:weeks].length
    assert_equal ["today", "tomorrow", "金曜日"], metadata[:suggestions]

    field.render(width: 12, height: 3).lines.each do |line|
      assert_operator TermForm::Text.cell_width(line), :<=, 12
    end
    field.render(width: 20).lines.each do |line|
      assert_operator TermForm::Text.cell_width(line), :<=, 20
    end
  end

  private

  def date_field(value: nil, parser: nil, formatter: nil, suggestions: [], **options)
    TermForm::Fields::DateInput.new(
      key: :date,
      value: value,
      parser: parser || ->(text, _today) { Date.iso8601(text) },
      formatter: formatter || ->(date) { date.iso8601 },
      today: -> { TODAY },
      suggestions: suggestions,
      **options,
    )
  end

  def form_with(*fields)
    TermForm::Form.new(groups: [TermForm::Group.new(key: :main, fields: fields)])
  end
end
