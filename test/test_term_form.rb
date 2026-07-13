# frozen_string_literal: true

require "minitest/autorun"
require_relative "../lib/term_form"

class TestTermForm < Minitest::Test
  NestedValue = Struct.new(:name, :items, keyword_init: true)

  def test_event_normalization_and_typed_transitions
    event = TermForm::Event.normalize(type: :change, key: :name, value: "Ada")
    transition = TermForm::Transition.new(:changed, event: event, changed_key: :name)

    assert_equal :change, event.type
    assert_equal :name, event.key
    assert_equal "Ada", event.value
    assert_equal false, TermForm::Event.new(:change, value: false).value
    assert transition.changed?
    refute transition.invalid?
    assert_raises(ArgumentError) { TermForm::Transition.new(:mystery) }
  end

  def test_key_map_is_injected_and_unknown_input_remains_semantic
    key_map = TermForm::KeyMap.new({ "j" => :next, "s" => { type: :commit, intended_focus: :second } }, defaults: false)
    form = simple_form(key_map: key_map)

    assert_equal :second, form.handle("j").focus_key
    commit = form.handle("s")
    assert commit.commit_requested?
    assert_equal :second, commit.request.intended_focus

    input = key_map.event_for("x")
    assert_equal :input, input.type
    assert_equal "x", input.text
  end

  def test_key_map_defensively_copies_mutable_keys
    raw_key = +"j"
    key_map = TermForm::KeyMap.new({ raw_key => :next }, defaults: false)
    raw_key.replace("mutated")

    assert_equal :next, key_map.event_for("j").type
    assert_equal :input, key_map.event_for("mutated").type
  end

  def test_form_rejects_duplicate_keys
    duplicate_fields = [field(:same), field(:same)]
    error = assert_raises(ArgumentError) do
      TermForm::Form.new(groups: [group(:main, duplicate_fields)])
    end
    assert_match(/duplicate TermForm key: same/, error.message)

    error = assert_raises(ArgumentError) do
      TermForm::Form.new(groups: [group(:same, [field(:same)])])
    end
    assert_match(/duplicate TermForm key: same/, error.message)
  end

  def test_values_baselines_and_context_are_defensive_read_only_copies
    source = { nested: ["original"] }
    form = TermForm::Form.new(groups: [group(:main, [field(:payload, value: source)])])
    source[:nested] << "outside"

    assert_equal({ nested: ["original"] }, form.value(:payload))
    assert form.values.frozen?
    assert form.values[:payload].frozen?
    assert form.values[:payload][:nested].frozen?
    assert_raises(FrozenError) { form.context.values[:payload][:nested] << "mutation" }

    returned = form.value(:payload)
    assert_raises(FrozenError) { returned[:nested] << "mutation" }
    assert_equal({ nested: ["original"] }, form.baseline(:payload))
  end

  def test_forward_and_backward_traversal_skip_hidden_and_disabled_fields
    fields = [
      field(:first),
      field(:hidden, visible: false),
      field(:disabled, enabled: false),
      field(:last),
    ]
    form = TermForm::Form.new(groups: [group(:main, fields)])

    assert_equal :first, form.focus_key
    assert_equal :last, form.focus_next.focus_key
    assert_equal :first, form.focus_next.focus_key
    assert_equal :last, form.focus_previous.focus_key
    assert_equal %i[first last], form.focusable_fields.map(&:key)
    assert_equal %i[first disabled last], form.visible_fields.map(&:key)
  end

  def test_group_visibility_and_enabled_state_participate_in_traversal
    form = TermForm::Form.new(groups: [
      group(:off, [field(:unavailable)], enabled: false),
      group(:hidden, [field(:invisible)], visible: false),
      group(:on, [field(:available)]),
    ])

    assert_equal :available, form.focus_key
    assert_equal [:available], form.focusable_fields.map(&:key)
    assert_equal %i[unavailable available], form.visible_fields.map(&:key)
  end

  def test_reactive_properties_recompute_and_move_focus_when_current_field_hides
    fields = [
      field(:mode, value: "advanced"),
      field(:details, value: "x", visible: ->(context) { context[:mode] == "advanced" }),
      field(:finish),
    ]
    form = TermForm::Form.new(groups: [group(:main, fields)], focus: :details)

    transition = form.set_value(:mode, "simple")

    assert transition.changed?
    assert_equal :finish, form.focus_key
    assert_equal %i[mode finish], form.visible_fields.map(&:key)
  end

  def test_reactive_properties_accept_zero_arity_and_context_arity_callables
    fields = [
      field(:gate, value: true),
      field(:zero, label: -> { "Zero" }, visible: -> { true }, enabled: -> { true }),
      field(:contextual, label: ->(context) { context[:gate] ? "On" : "Off" },
            visible: ->(context) { context[:gate] }, enabled: ->(context) { context[:gate] }),
    ]
    form = TermForm::Form.new(groups: [
      group(:main, fields, label: ->(context) { context[:gate] ? "Enabled" : "Disabled" },
            visible: -> { true }, enabled: -> { true }),
    ])

    assert_equal %i[gate zero contextual], form.focusable_fields.map(&:key)
    assert_equal "Enabled", form.render_model.groups.first.label
    assert_equal %w[gate Zero On], form.render_model.rows.map(&:label)
    form.set_value(:gate, false)
    assert_equal %i[gate zero], form.focusable_fields.map(&:key)
    assert_equal "Disabled", form.render_model.groups.first.label
  end

  def test_validation_uses_current_context_and_ignores_hidden_or_disabled_fields
    fields = [
      field(:mode, value: "short"),
      field(:name, value: "", required: true),
      field(:dependent, value: "too long", validate: ->(value, context) { "too long" if value.length > context[:mode].length }),
      field(:hidden, value: "", visible: false, required: true),
      field(:disabled, value: "", enabled: false, required: true),
    ]
    form = TermForm::Form.new(groups: [group(:main, fields)])

    errors = form.validate
    assert_equal ["is required"], errors[:name]
    assert_equal ["too long"], errors[:dependent]
    refute errors.key?(:hidden)
    refute errors.key?(:disabled)

    form.set_value(:name, "Ada")
    form.set_value(:dependent, "ok")
    assert form.valid?
  end

  def test_invalid_commit_focuses_first_error_and_does_not_become_pending
    form = TermForm::Form.new(groups: [group(:main, [
      field(:first, value: ""),
      field(:required, value: "", required: true),
    ])], focus: :first)

    transition = form.request_commit(intended_focus: :first)

    assert transition.invalid?
    assert_equal :required, transition.focus_key
    refute form.pending?
  end

  def test_rejection_retains_dirty_value_focus_and_cursor
    fields = [
      field(:title, value: "old", cursor: ->(value) { value.length }),
      field(:notes, value: "notes"),
    ]
    form = TermForm::Form.new(groups: [group(:main, fields)], focus: :title)
    form.set_value(:title, "edited")
    before = form.render_model

    request = form.request_commit(intended_focus: :notes)
    assert request.commit_requested?
    rejected = form.reject_commit(message: "save failed", token: request.request.token)

    assert rejected.commit_rejected?
    assert_equal "edited", form.value(:title)
    assert_equal "old", form.baseline(:title)
    assert_equal :title, form.focus_key
    assert_equal before.cursor, form.render_model.cursor
    assert form.dirty?(:title)
    refute form.pending?
    assert_equal ["save failed"], form.errors[:base]
  end

  def test_dirty_tab_requests_a_field_commit_and_holds_focus_until_accept
    form = simple_form
    form.set_value(:first, "dirty first")

    transition = form.handle("\t")
    request = transition.request

    assert transition.commit_requested?
    assert form.pending?
    assert_equal :first, form.focus_key
    assert_equal :first, request.field_key
    assert_equal "dirty first", request.proposed_value
    assert_equal "first", request.expected_baseline
    assert_equal :second, request.intended_focus
    assert_equal :next, request.direction
    assert_equal :next, request.intended_direction
    assert request.frozen?
    assert request.proposed_value.frozen?

    form.accept_commit(token: request.token)
    assert_equal :second, form.focus_key
    refute form.dirty?(:first)
  end

  def test_dirty_shift_tab_requests_backward_commit_and_holds_focus
    form = simple_form
    form.focus(:second)
    form.set_value(:second, "dirty second")

    transition = form.handle("\e[Z")

    assert transition.commit_requested?
    assert_equal :second, form.focus_key
    assert_equal :second, transition.request.field_key
    assert_equal :first, transition.request.intended_focus
    assert_equal :previous, transition.request.direction

    form.reject_commit(token: transition.request.token)
    assert_equal :second, form.focus_key
  end

  def test_semantic_focus_event_uses_the_same_dirty_two_phase_guard
    form = simple_form
    form.set_value(:first, "dirty first")

    transition = form.handle(type: :focus, key: :second, direction: :direct)

    assert transition.commit_requested?
    assert_equal :first, form.focus_key
    assert_equal :first, transition.request.field_key
    assert_equal :second, transition.request.intended_focus
    assert_equal :direct, transition.request.direction

    form.accept_commit(token: transition.request.token)
    assert_equal :second, form.focus_key
  end

  def test_public_focus_helpers_share_the_dirty_departure_guard
    form = simple_form
    form.set_value(:first, "dirty first")

    transition = form.focus_next

    assert transition.commit_requested?
    assert_equal :first, form.focus_key
    assert_equal :next, transition.request.direction
    form.reject_commit(token: transition.request.token)

    transition = form.focus(:second)
    assert transition.commit_requested?
    assert_equal :first, form.focus_key
  end

  def test_clean_tab_and_shift_tab_move_without_requesting_commit
    form = simple_form

    assert form.handle("\t").focus_changed?
    refute form.pending?
    assert_equal :second, form.focus_key
    assert form.handle("\e[Z").focus_changed?
    assert_equal :first, form.focus_key
  end

  def test_accept_uses_fresh_baselines_without_erasing_edits_made_while_pending
    form = simple_form
    form.set_value(:first, "submitted first")
    request = form.request_commit(intended_focus: :second)
    form.set_value(:second, "edited while saving")

    accepted = form.accept_commit(values: { first: "canonical first", second: "server second" }, token: request.request.token)

    assert accepted.commit_accepted?
    assert_equal "canonical first", form.value(:first)
    assert_equal "canonical first", form.baseline(:first)
    assert_equal "edited while saving", form.value(:second)
    assert_equal "server second", form.baseline(:second)
    assert form.dirty?(:second)
    refute form.dirty?(:first)
    assert_equal :second, form.focus_key
  end

  def test_accept_without_fresh_values_accepts_submitted_snapshot
    form = simple_form
    form.set_value(:first, "submitted")
    request = form.request_commit

    form.accept_commit(token: request.request.token)

    assert_equal "submitted", form.baseline(:first)
    refute form.dirty?
  end

  def test_refresh_during_pending_then_accept_does_not_redirty_clean_remote_value
    form = simple_form
    form.set_value(:first, "submitted first")
    request = form.handle("\t").request

    form.refresh(values: { second: "remote second" })
    assert_equal "remote second", form.value(:second)
    refute form.dirty?(:second)

    form.accept_commit(token: request.token)

    assert_equal "remote second", form.value(:second)
    assert_equal "remote second", form.baseline(:second)
    refute form.dirty?(:second)
    assert_equal :second, form.focus_key
  end

  def test_same_field_refresh_during_pending_wins_over_implicit_stale_accept
    form = simple_form
    request = form.request_commit.request

    form.refresh(values: { first: "remote first" })
    form.accept_commit(token: request.token)

    assert_equal "remote first", form.value(:first)
    assert_equal "remote first", form.baseline(:first)
    refute form.dirty?(:first)
    refute form.pending?
  end

  def test_same_field_refresh_rejects_stale_accept_when_pending_buffer_is_still_dirty
    form = simple_form
    form.set_value(:first, "submitted first")
    request = form.handle("\t").request
    form.refresh(values: { first: "remote first" })

    error = assert_raises(ArgumentError) { form.accept_commit(token: request.token) }

    assert_equal "commit token is stale after refresh", error.message
    assert_equal "submitted first", form.value(:first)
    assert_equal "remote first", form.baseline(:first)
    assert form.dirty?(:first)
    assert form.pending?
    assert_equal :first, form.focus_key
  end

  def test_refresh_keeps_hidden_pending_owner_as_semantic_focus_until_rejection
    form = TermForm::Form.new(groups: [group(:main, [
      field(:mode, value: "advanced"),
      field(:details, value: "old", visible: ->(context) { context[:mode] == "advanced" },
            cursor: ->(value) { value.length }),
      field(:finish, value: "done"),
    ])], focus: :details)
    form.set_value(:details, "dirty")
    request = form.focus_next.request

    refreshed = form.refresh(values: { mode: "simple" })
    model = refreshed.render_model

    assert refreshed.refreshed?
    assert form.pending?
    assert_same request, form.pending_commit
    assert_equal :details, form.focus_key
    assert_equal :details, request.field_key
    assert_equal %i[mode finish], form.visible_fields.map(&:key)
    assert_equal %i[mode finish], form.focusable_fields.map(&:key)
    assert_equal %i[mode details finish], model.rows.map(&:key)
    assert_equal :details, model.focused_key
    assert_equal :details, model.focused_row.key
    assert model.focused_row.focused?
    assert model.focused_row.pending?
    refute model.focused_row.enabled?
    assert_equal TermForm::RenderModel::Cursor.new(1, 5, :details), model.cursor

    assert_raises(ArgumentError) { form.accept_commit(token: request.token + 1) }
    assert_raises(ArgumentError) { form.reject_commit(token: request.token + 1) }
    assert_same request, form.pending_commit
    assert_equal :details, form.focus_key
    assert_equal :details, form.render_model.focused_row.key

    rejected = form.reject_commit(message: "save failed", token: request.token)

    assert rejected.commit_rejected?
    refute form.pending?
    assert_equal :mode, form.focus_key
    assert_equal %i[mode finish], form.render_model.rows.map(&:key)
    assert_equal :mode, form.render_model.focused_row.key
    assert_raises(RuntimeError) { form.accept_commit(token: request.token) }
    assert_equal :mode, form.focus_key
  end

  def test_refresh_keeps_disabled_pending_owner_focused_until_acceptance
    form = TermForm::Form.new(groups: [group(:main, [
      field(:mode, value: "advanced"),
      field(:details, value: "old", enabled: ->(context) { context[:mode] == "advanced" }),
      field(:finish, value: "done"),
    ])], focus: :details)
    form.set_value(:details, "dirty")
    request = form.focus_next.request

    form.refresh(values: { mode: "simple" })
    model = form.render_model

    assert form.pending?
    assert_equal :details, form.focus_key
    assert_equal :details, model.focused_key
    assert_equal :details, model.focused_row.key
    assert model.focused_row.pending?
    refute model.focused_row.enabled?
    assert_equal %i[mode finish], form.focusable_fields.map(&:key)

    accepted = form.accept_commit(token: request.token)

    assert accepted.commit_accepted?
    refute form.pending?
    refute form.dirty?(:details)
    assert_equal "dirty", form.baseline(:details)
    assert_equal :finish, form.focus_key
    assert_equal :finish, form.render_model.focused_row.key
    refute form.render_model.rows.any?(&:pending?)
    assert_raises(RuntimeError) { form.reject_commit(token: request.token) }
    assert_equal :finish, form.focus_key
  end

  def test_pending_request_is_cancelled_when_buffer_returns_to_baseline_before_navigation
    form = simple_form
    form.set_value(:first, "dirty first")
    request = form.handle("\t").request
    form.set_value(:first, "first")

    transition = form.handle("\t")

    assert transition.focus_changed?
    assert_equal :second, form.focus_key
    refute form.pending?
    assert_raises(RuntimeError) { form.accept_commit(token: request.token) }
  end

  def test_refresh_during_pending_and_accept_preserve_another_dirty_buffer
    form = simple_form
    form.set_value(:second, "local second")
    form.focus(:first)
    form.set_value(:first, "submitted first")
    request = form.handle("\t").request

    form.refresh(values: { second: "remote second" })
    form.accept_commit(token: request.token)

    assert_equal "local second", form.value(:second)
    assert_equal "remote second", form.baseline(:second)
    assert form.dirty?(:second)
  end

  def test_refresh_updates_clean_values_and_baselines_but_preserves_dirty_values
    form = simple_form
    form.set_value(:second, "local second")
    old_focus = form.focus_key

    transition = form.refresh(values: { first: "remote first", second: "remote second" })

    assert transition.refreshed?
    assert_equal "remote first", form.value(:first)
    assert_equal "remote first", form.baseline(:first)
    assert_equal "local second", form.value(:second)
    assert_equal "remote second", form.baseline(:second)
    assert form.dirty?(:second)
    assert_equal old_focus, form.focus_key
  end

  def test_render_model_is_semantic_and_exposes_focused_row_and_cursor
    fields = [
      field(:name, value: "Ada", required: true, cursor: 2, metadata: { kind: :text }),
      field(:locked, value: "fixed", enabled: false),
      field(:hidden, visible: false),
    ]
    form = TermForm::Form.new(groups: [group(:identity, fields, label: "Identity")])
    form.set_value(:name, "Grace")
    model = form.render_model

    assert_equal [:identity], model.groups.map(&:key)
    assert_equal %i[name locked], model.rows.map(&:key)
    assert_equal :name, model.focused_key
    assert_equal :name, model.focused_row.key
    assert_equal 0, model.focused_row_index
    assert_equal TermForm::RenderModel::Cursor.new(0, 2, :name), model.cursor
    assert model.focused_row.focused?
    assert model.focused_row.dirty?
    assert model.focused_row.required?
    assert_equal({ kind: :text }, model.focused_row.metadata)
    refute model.rows.last.enabled?
    assert model.frozen?
  end

  def test_custom_struct_values_labels_and_render_data_are_deep_copied_and_frozen
    value = NestedValue.new(name: +"before", items: [[+"nested"]])
    label = +"Mutable label"
    metadata = { options: [NestedValue.new(name: +"meta", items: [])] }
    form = TermForm::Form.new(groups: [group(:main, [
      field(:structured, value: value, label: label, metadata: metadata),
    ])])

    value.name.replace("outside")
    value.items.first.first.replace("outside")
    label.replace("outside")
    metadata[:options].first.name.replace("outside")

    stored = form.value(:structured)
    row = form.render_model.focused_row
    assert_equal "before", stored.name
    assert_equal "nested", stored.items.first.first
    assert stored.frozen?
    assert stored.name.frozen?
    assert stored.items.frozen?
    assert stored.items.first.frozen?
    assert stored.items.first.first.frozen?
    assert_equal "Mutable label", row.label
    assert row.label.frozen?
    assert_equal "meta", row.metadata[:options].first.name
    assert row.metadata[:options].first.frozen?

    form.set_value(:structured, NestedValue.new(name: "dirty", items: [["request"]]))
    request = form.request_commit.request
    assert request.proposed_value.frozen?
    assert request.proposed_value.items.first.first.frozen?
    assert request.expected_baseline.frozen?
  end

  def test_pending_commit_is_single_flight_and_tokens_are_checked
    form = simple_form
    request = form.request_commit

    assert form.request_commit.commit_pending?
    assert_raises(ArgumentError) { form.accept_commit(token: request.request.token + 1) }
    assert form.pending?
    form.reject_commit(token: request.request.token)
    assert_raises(RuntimeError) { form.reject_commit }
  end

  private

  def field(key, **options)
    TermForm::Field.new(key: key, **options)
  end

  def group(key, fields, **options)
    TermForm::Group.new(key: key, fields: fields, **options)
  end

  def simple_form(key_map: TermForm::KeyMap.new)
    TermForm::Form.new(
      groups: [group(:main, [field(:first, value: "first"), field(:second, value: "second")])],
      key_map: key_map,
    )
  end
end
