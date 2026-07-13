# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"
require "tui/text_input"

class TestApp < Minitest::Test
  def ui(app) = app.instance_variable_get(:@ui)

  # Records calls to #start and reports whatever running?/available? state we
  # set, so we can drive submit_prompt without spawning a real agent process.
  class FakeAgent
    attr_reader :started

    def initialize(running:, available: true)
      @running = running
      @available = available
      @started = []
    end

    def running? = @running
    def available? = @available
    def start(text, model:) = @started << [text, model]
  end

  def app_with(agent:, input:)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config)
      app.instance_variable_set(:@agent, agent)
      app.instance_variable_set(:@input, Tui::TextInput.new(input))
      yield app
    end
  end

  def test_submit_prompt_rejected_while_agent_running
    fake = FakeAgent.new(running: true)
    app_with(agent: fake, input: "reschedule the flight") do |app|
      app.send(:submit_prompt)
      assert_empty fake.started, "must not orphan the in-flight run by starting a second"
      assert_match(/still working/, app.instance_variable_get(:@flash))
      # input is cleared and focus returns to the list even on rejection
      assert_equal "", app.instance_variable_get(:@input)
      assert_equal :list, ui(app).mode
    end
  end

  def test_submit_prompt_ignores_blank_input_without_touching_agent
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "   ") do |app|
      app.send(:submit_prompt)
      assert_empty fake.started
      assert_nil app.instance_variable_get(:@flash)
    end
  end

  def test_submit_prompt_flashes_when_agent_unavailable
    fake = FakeAgent.new(running: false, available: false)
    app_with(agent: fake, input: "do a thing") do |app|
      app.send(:submit_prompt)
      assert_empty fake.started, "must not start an unavailable agent"
      assert_match(/not available/, app.instance_variable_get(:@flash))
    end
  end

  def test_submit_prompt_starts_agent_with_selected_model
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "reschedule the flight") do |app|
      app.send(:submit_prompt)
      assert_equal [["reschedule the flight", "sonnet"]], fake.started
    end
  end

  def test_terminal_size_uses_current_console_dimensions
    fake = FakeAgent.new(running: false)
    console = Struct.new(:winsize).new([13, 47])
    app_with(agent: fake, input: "") do |app|
      IO.stub(:console, console) do
        assert_equal [13, 47], app.send(:terminal_size)
      end
    end
  end

  def test_terminal_size_retains_narrow_but_renderable_dimensions
    fake = FakeAgent.new(running: false)
    console = Struct.new(:winsize).new([7, 11])
    app_with(agent: fake, input: "") do |app|
      IO.stub(:console, console) do
        assert_equal [7, 11], app.send(:terminal_size)
      end
    end
  end

  def test_footer_height_is_calculated_at_the_current_width
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "界 " * 60) do |app|
      ui(app).mode = :prompt
      narrow = app.send(:footer_size, width: 40)
      wide = app.send(:footer_size, width: 120)
      assert_operator narrow, :>, wide
      assert_operator narrow, :<=, Tui::App::PROMPT_MAX
    end
  end

  def test_paint_threads_one_terminal_size_through_frame_geometry
    fake = FakeAgent.new(running: false)
    console = Struct.new(:winsize).new([12, 43])
    captured = nil
    popup_geometry = nil
    builder = lambda do |**args|
      captured = args
      Array.new(args[:height], " " * args[:width])
    end
    popup_builder = lambda do |**args|
      popup_geometry = args
      nil
    end

    app_with(agent: fake, input: "") do |app|
      IO.stub(:console, console) do
        app.stub(:current_popup, popup_builder) do
          Tui::Frame.stub(:build, builder) { capture_io { app.send(:paint) } }
        end
      end
    end
    assert_equal 43, captured[:width]
    assert_equal 12, captured[:height]
    assert_equal 43, popup_geometry[:layout].width
    assert_equal 12, popup_geometry[:layout].height
    assert_equal captured[:footer].size, popup_geometry[:layout].footer_size
  end

  def test_paint_samples_terminal_size_once_during_resize
    fake = FakeAgent.new(running: false)
    calls = 0
    console = Object.new
    console.define_singleton_method(:winsize) do
      calls += 1
      calls == 1 ? [12, 43] : [40, 120]
    end
    captured = nil

    app_with(agent: fake, input: "") do |app|
      IO.stub(:console, console) do
        Tui::Frame.stub(:build, ->(**args) { captured = args; Array.new(args[:height], "") }) do
          capture_io { app.send(:paint) }
        end
      end
    end

    assert_equal 1, calls, "one frame must not mix dimensions across a resize"
    assert_equal [43, 12], captured.values_at(:width, :height)
  end

  def test_prompt_mode_hides_selection_without_scrolling_to_it
    fake = FakeAgent.new(running: false)
    captured = nil
    console = Struct.new(:winsize).new([8, 43])

    app_with(agent: fake, input: "ask") do |app|
      app.send(:rows)
      original_rows = app.instance_variable_get(:@rows).dup
      app.instance_variable_set(:@sel, original_rows.length - 1)
      ui(app).mode = :prompt
      IO.stub(:console, console) do
        Tui::Frame.stub(:build, ->(**args) { captured = args; Array.new(args[:height], "") }) do
          capture_io { app.send(:paint) }
        end
      end

      assert_nil captured[:selected]
      assert_equal 0, captured[:layout].viewport_offset
      assert_equal original_rows.first.item.id, captured[:rows].first.item.id
      assert_equal original_rows.length, captured[:rows].length
    end
  end

  def test_panel_closed_tab_focuses_prompt_without_rebinding_selection
    app_on(view: :agenda, select: "Book flight") do |app|
      selected_id = app.send(:current_item).id

      app.send(:handle_key, "\t")

      assert_equal :prompt, ui(app).mode
      assert_equal selected_id, ui(app).selected_id
      assert_nil ui(app).panel
    end
  end


  def test_detail_tab_and_shift_tab_enter_one_editor_at_first_and_last_fields
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      assert ui(app).panel

      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      assert_equal :task_edit, ui(app).mode
      assert_equal FIX[:flight], editor.target_id
      assert_equal :title, editor.focused_key
      assert_equal :task_edit, ui(app).panel.kind

      app.send(:handle_key, "\x0f")
      assert_equal :list, ui(app).mode
      assert_nil ui(app).task_editor
      assert_equal :detail, ui(app).panel.kind

      app.send(:handle_key, "\e[Z")
      assert_equal :task_edit, ui(app).mode
      assert_equal :state, ui(app).task_editor.focused_key
    end
  end

  def test_task_editor_opens_on_a_stable_target_in_all_five_views
    targets = {
      agenda: "Book flight",
      next: "Book flight",
      quadrants: "Book flight",
      inbox: "random thought",
      projects: "Book flight",
    }
    targets.each do |view, title|
      app_on(view: view, select: title) do |app|
        target_id = app.send(:current_item).id
        app.send(:handle_key, "\r")
        app.send(:handle_key, "e")
        assert_equal :task_edit, ui(app).mode, view.to_s
        assert_equal target_id, ui(app).task_editor.target_id, view.to_s
        assert_equal Tui::TaskEditForm::FIELD_ORDER,
                     ui(app).task_editor.edit_form.field_order, view.to_s
      end
    end
  end

  def test_editor_dispatch_precedes_list_prompt_and_colon_actions
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      selected_id = ui(app).selected_id

      app.send(:handle_key, "j")
      app.send(:handle_key, ":")

      assert_equal "Book flight in Concurj:", editor.edit_form.value(:title)
      assert_equal selected_id, ui(app).selected_id
      assert_equal :task_edit, ui(app).mode
      assert_nil ui(app).action_palette
    end
  end

  def test_task_editor_receives_one_unicode_bracketed_paste_event
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.focus(:body)
      before = File.binread(app.instance_variable_get(:@store).org)

      app.instance_variable_set(
        :@key_data,
        "\e[200~first 👩‍💻界\r\nsecond\tline\e[201~",
      )
      app.send(:drain_key_data)

      assert_equal :body, editor.focused_key
      assert_equal "first 👩‍💻界\nsecond line", editor.edit_form.value(:body)
      assert editor.dirty?(:body)
      assert_equal before, File.binread(app.instance_variable_get(:@store).org),
                   "paste must not blur or save the field"

      app.send(:handle_key, "\x13")
      assert_equal "first 👩‍💻界\nsecond line",
                   app.instance_variable_get(:@store).edit_snapshot(FIX[:flight]).body
    end
  end

  def test_multiline_unicode_notes_keep_exact_120_by_32_app_frame_geometry
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.focus(:body)
      editor.form.set_value(
        :body,
        (1..24).map { |index| "line #{index} · 👩‍💻界 · e\u0301 · detailed note" }.join("\n"),
      )

      frame = nil
      original_build = Tui::Frame.method(:build)
      console = Struct.new(:winsize).new([32, 120])
      IO.stub(:console, console) do
        Tui::Frame.stub(:build, ->(**args) { frame = original_build.call(**args) }) do
          capture_io { app.send(:paint) }
        end
      end

      assert_equal 32, frame.size
      assert frame.all? { |line| Tui::Ansi.vislen(line) == 120 },
             frame.map { |line| Tui::Ansi.vislen(line) }.inspect
      refute frame.any? { |line| line.match?(/[\r\n]/) }
      assert_match(/\A┌─+┐\z/, Tui::Ansi.strip(frame.first))
      assert_match(/\A└─+┘\z/, Tui::Ansi.strip(frame.last))
      refute ui(app).panel.lines.any? { |line| line.match?(/[\r\n]/) }
    end
  end

  def test_exact_boundary_notes_use_two_panel_rows_in_default_and_mono_frames
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.focus(:body)
      layout = app.send(:screen_layout, width: 120, height: 32, panel: ui(app).panel)
      first_line = "a" * (layout.panel_content_width - 12)
      editor.form.set_value(:body, "#{first_line}\nZ")

      original_build = Tui::Frame.method(:build)
      console = Struct.new(:winsize).new([32, 120])
      %w[default mono].each do |theme|
        Tui::Theme.configure!(name: theme)
        frame = nil
        IO.stub(:console, console) do
          Tui::Frame.stub(:build, ->(**args) { frame = original_build.call(**args) }) do
            capture_io { app.send(:paint) }
          end
        end

        panel_lines = ui(app).panel.lines.map { |line| Tui::Ansi.strip(line) }
        notes_row = panel_lines.index { |line| line.include?("Notes: #{first_line}") }
        refute_nil notes_row, theme
        assert_includes panel_lines.fetch(notes_row + 1), "│* Z", theme
        assert_equal 32, frame.size, theme
        assert frame.all? { |line| Tui::Ansi.vislen(line) == 120 }, theme
        refute frame.any? { |line| line.match?(/[\r\n]/) }, theme
        assert_match(/\A┌─+┐\z/, Tui::Ansi.strip(frame.first), theme)
        assert_match(/\A└─+┘\z/, Tui::Ansi.strip(frame.last), theme)
      end
    end
  end

  def test_ctrl_s_saves_in_place_and_ctrl_o_returns_to_read_panel
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "Book flight safely")

      app.send(:handle_key, "\x13")
      assert_equal :task_edit, ui(app).mode
      assert_equal :title, editor.focused_key
      assert_equal "Book flight safely", app.send(:current_item).title
      refute editor.dirty?(:title)

      app.send(:handle_key, "\x0f")
      assert_equal :list, ui(app).mode
      assert_equal :detail, ui(app).panel.kind
      assert_equal FIX[:flight], ui(app).panel.identity
    end
  end

  def test_dirty_active_editor_ctrl_c_requires_visible_cancelable_confirmation
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "UNSAVED-ACTIVE-DRAFT")
      before = File.binread(app.instance_variable_get(:@store).org)

      app.send(:handle_key, "\x03")

      refute app.instance_variable_get(:@quit)
      assert_same editor, ui(app).task_editor
      assert editor.dirty?(:title)
      assert_equal "UNSAVED-ACTIVE-DRAFT", editor.edit_form.value(:title)
      assert_equal :task_draft_quit_confirm, ui(app).modal.kind
      assert_match(/discard.*quit/i, ui(app).modal.lines.join(" "))
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)

      app.send(:handle_key, "\x03")
      refute app.instance_variable_get(:@quit), "repeated ctrl-c must not confirm draft loss"
      assert_same editor, ui(app).task_editor

      app.send(:handle_key, "n")
      refute app.instance_variable_get(:@quit)
      assert_nil ui(app).modal
      assert_equal :task_edit, ui(app).mode
      assert_same editor, ui(app).task_editor
      assert_equal "UNSAVED-ACTIVE-DRAFT", editor.edit_form.value(:title)
      assert_match(/retained/, app.instance_variable_get(:@flash))

      app.send(:handle_key, "\x03")
      app.send(:handle_key, "y")
      assert app.instance_variable_get(:@quit)
      assert_nil ui(app).task_editor
      assert_equal before, File.binread(app.instance_variable_get(:@store).org),
                   "confirmed quit discards only the local buffer"
    end
  end

  def test_dirty_suspended_editor_q_requires_visible_cancelable_confirmation
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "UNSAVED-SUSPENDED-DRAFT")
      before = File.binread(app.instance_variable_get(:@store).org)
      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }
      assert_same editor, app.instance_variable_get(:@suspended_task_editor)

      app.send(:handle_key, "q")

      refute app.instance_variable_get(:@quit)
      assert_equal :task_draft_quit_confirm, ui(app).modal.kind
      assert_same editor, app.instance_variable_get(:@suspended_task_editor)
      assert_equal "UNSAVED-SUSPENDED-DRAFT", editor.edit_form.value(:title)
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)

      app.send(:handle_key, "q")
      refute app.instance_variable_get(:@quit), "repeated q must not confirm draft loss"
      app.send(:handle_key, "\e")
      refute app.instance_variable_get(:@quit)
      assert_nil ui(app).modal
      assert_same editor, app.instance_variable_get(:@suspended_task_editor)
      assert_equal "UNSAVED-SUSPENDED-DRAFT", editor.edit_form.value(:title)

      app.send(:handle_key, "q")
      app.send(:handle_key, "\r")
      assert app.instance_variable_get(:@quit)
      assert_nil app.instance_variable_get(:@suspended_task_editor)
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)
    end
  end

  def test_clean_active_and_suspended_editors_keep_immediate_quit_behavior
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      refute ui(app).task_editor.dirty?

      app.send(:handle_key, "\x03")

      assert app.instance_variable_get(:@quit)
      assert_nil ui(app).modal
    end

    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }
      refute editor.dirty?

      app.send(:handle_key, "q")

      assert app.instance_variable_get(:@quit)
      assert_nil ui(app).modal
    end
  end

  def test_dirty_quit_confirmation_precedes_and_restores_prompt_palette_and_modal
    {
      prompt: ->(app) { app.send(:handle_key, "p"); app.instance_variable_get(:@input) },
      palette: ->(app) { app.send(:handle_key, ":"); ui(app).action_palette },
      modal: ->(app) { app.send(:handle_key, "?"); ui(app).modal },
    }.each do |expected_mode, open_overlay|
      app_on(view: :agenda, select: "Book flight") do |app|
        app.send(:handle_key, "\r")
        app.send(:handle_key, "\t")
        editor = ui(app).task_editor
        editor.form.set_value(:title, "#{expected_mode}-safe-draft")
        small = Struct.new(:winsize).new([7, 46])
        IO.stub(:console, small) { capture_io { app.send(:paint) } }
        underlying = open_overlay.call(app)
        underlying_value = case expected_mode
                           when :prompt then underlying.to_s
                           when :palette then underlying.input.to_s
                           when :modal then underlying.scroll
                           end
        assert_equal expected_mode, ui(app).mode

        app.send(:handle_key, "\x03")
        assert_equal :task_draft_quit_confirm, ui(app).modal.kind
        app.send(:handle_key, "n")

        refute app.instance_variable_get(:@quit)
        assert_equal expected_mode, ui(app).mode
        case expected_mode
        when :prompt
          assert_same underlying, app.instance_variable_get(:@input)
          assert_equal underlying_value, underlying.to_s
        when :palette
          assert_same underlying, ui(app).action_palette
          assert_equal underlying_value, underlying.input.to_s
        when :modal
          assert_same underlying, ui(app).modal
          assert_equal underlying_value, underlying.scroll
        end
        assert_same editor, app.instance_variable_get(:@suspended_task_editor)
        assert_equal "#{expected_mode}-safe-draft", editor.edit_form.value(:title)
      end
    end
  end

  def test_panel_resize_preserves_entire_editor_and_performs_no_write
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "draft text")
      field = editor.form.field(:title)
      field.handle_key("\e[D")
      before = File.binread(app.instance_variable_get(:@store).org)
      identity = [editor.object_id, editor.target_id, editor.focused_key,
                  editor.edit_form.value(:title), field.cursor, editor.coalesce_key]

      app.send(:handle_key, "\x0b")
      app.send(:handle_key, "\x0c")

      assert_equal :standard, ui(app).panel_mode
      assert_equal identity,
                   [ui(app).task_editor.object_id, ui(app).task_editor.target_id,
                    ui(app).task_editor.focused_key,
                    ui(app).task_editor.edit_form.value(:title), field.cursor,
                    ui(app).task_editor.coalesce_key]
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)
    end
  end

  def test_terminal_resize_preserves_dirty_picker_session_without_write
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.focus(:deadline)
      editor.form.set_value(:deadline, Date.new(2026, 7, 20))
      editor.handle("\r")
      assert editor.form.field(:deadline).picker_open?
      before = File.binread(app.instance_variable_get(:@store).org)
      coalesce_key = editor.coalesce_key
      console = Struct.new(:winsize).new([18, 60])

      IO.stub(:console, console) { capture_io { app.send(:paint) } }

      assert_same editor, ui(app).task_editor
      assert_equal :deadline, editor.focused_key
      assert editor.form.field(:deadline).picker_open?
      assert_equal Date.new(2026, 7, 20), editor.edit_form.value(:deadline)
      assert_equal coalesce_key, editor.coalesce_key
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)
    end
  end


  def test_below_minimum_height_suspends_editor_and_reentry_preserves_draft
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      panel = ui(app).panel
      editor.form.set_value(:title, "narrow draft")
      panel.instance_variable_set(:@scroll, 3)
      before = File.binread(app.instance_variable_get(:@store).org)
      coalesce_key = editor.coalesce_key
      captured = nil
      console = Struct.new(:winsize).new([7, 46])

      IO.stub(:console, console) do
        Tui::Frame.stub(:build, ->(**args) { captured = args; Array.new(args[:height], "") }) do
          capture_io { app.send(:paint) }
        end
      end

      refute captured[:layout].editable_panel?
      assert_equal :list, ui(app).mode
      assert_nil ui(app).task_editor
      assert_equal "narrow draft", editor.edit_form.value(:title)
      assert_equal :detail, ui(app).panel.kind
      assert_match(/editing paused/, app.instance_variable_get(:@flash))
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)

      # The invisible editor no longer captures list keys.
      original_id = ui(app).selected_id
      app.send(:handle_key, "j")
      refute_equal original_id, ui(app).selected_id
      assert_equal "narrow draft", editor.edit_form.value(:title)

      app.send(:handle_key, "k")
      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "\t") }
      assert_equal :task_edit, ui(app).mode
      assert_same editor, ui(app).task_editor
      assert_same panel, ui(app).panel
      assert_equal 3, ui(app).panel.scroll
      assert_equal "narrow draft", editor.edit_form.value(:title)
      assert_equal coalesce_key, editor.coalesce_key
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)
    end
  end

  def test_enter_task_edit_rejects_46_by_6_and_7_but_shows_field_at_46_by_8
    [6, 7].each do |height|
      app_on(view: :agenda, select: "Book flight") do |app|
        app.send(:handle_key, "\r")
        console = Struct.new(:winsize).new([height, 46])
        IO.stub(:console, console) { app.send(:handle_key, "\t") }
        assert_equal :list, ui(app).mode, "46x#{height}"
        assert_nil ui(app).task_editor
        assert_equal :detail, ui(app).panel.kind
        assert_match(/46×8/, app.instance_variable_get(:@flash))
      end
    end

    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      console = Struct.new(:winsize).new([8, 46])
      captured = nil
      IO.stub(:console, console) do
        app.send(:handle_key, "\t")
        Tui::Frame.stub(:build, ->(**args) { captured = args; Array.new(args[:height], "") }) do
          capture_io { app.send(:paint) }
        end
      end
      assert_equal :task_edit, ui(app).mode
      assert captured[:layout].editable_panel?
      assert_equal 1, captured[:panel][:lines].size
      assert_match(/Book flight/, Tui::Ansi.strip(captured[:panel][:lines].first))
    end
  end

  def test_deleted_suspended_target_becomes_missing_copyable_and_discardable
    app_on(view: :agenda, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "recover this deleted draft")
      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }

      rewrite_records(app) { |records| records.reject! { |record| record["id"] == target_id } }

      assert editor.missing?
      assert_equal :list, ui(app).mode
      assert_nil ui(app).task_editor
      assert_equal :suspended_task_edit, ui(app).panel.kind
      assert_match(/Task no longer exists/, ui(app).panel.lines.first)
      assert_match(/cop(?:y|ies).*discard/, app.instance_variable_get(:@flash))

      # Widening and Tab cannot activate or confirm the hidden missing session.
      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "\t") }
      assert_equal :list, ui(app).mode
      assert_match(/y copies.*esc discards/, app.instance_variable_get(:@flash))

      copied = nil
      Tui::Clipboard.stub(:copy, ->(value) { copied = value; true }) do
        app.send(:handle_key, "y")
      end
      assert_equal "recover this deleted draft", copied
      assert editor.missing?

      app.send(:handle_key, "\e")
      assert_nil app.instance_variable_get(:@suspended_task_editor)
      assert_nil ui(app).panel
      assert_match(/discarded local draft/, app.instance_variable_get(:@flash))

      app.send(:handle_key, "\r")
      replacement_id = app.send(:current_item).id
      IO.stub(:console, wide) { app.send(:handle_key, "\t") }
      assert_equal :task_edit, ui(app).mode
      refute_same editor, ui(app).task_editor
      assert_equal replacement_id, ui(app).task_editor.target_id
    end
  end

  def test_done_suspended_target_uses_inert_recovery_then_allows_new_editor
    app_on(view: :next, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "draft for externally done task")
      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }

      rewrite_records(app) do |records|
        record = records.find { |candidate| candidate["id"] == target_id }
        record["state"] = "DONE"
        record["closed"] = "2026-07-13"
      end

      refute editor.missing?
      assert_equal target_id, editor.target_id
      assert_equal "draft for externally done task", editor.edit_form.value(:title)
      refute_equal target_id, ui(app).selected_id
      assert_equal :suspended_task_edit, ui(app).panel.kind
      assert_match(/hidden from the canonical views/, ui(app).panel.lines.first)
      assert_nil app.send(:suspended_target_canonical_view)

      before = File.binread(app.instance_variable_get(:@store).org)
      app.send(:handle_key, "\x13")
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)
      assert_equal "draft for externally done task", editor.edit_form.value(:title)

      copied = nil
      Tui::Clipboard.stub(:copy, ->(value) { copied = value; true }) do
        app.send(:handle_key, "y")
      end
      assert_equal "draft for externally done task", copied

      app.send(:handle_key, "\e")
      assert_nil app.instance_variable_get(:@suspended_task_editor)
      app.send(:handle_key, "\r")
      replacement_id = app.send(:current_item).id
      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "\t") }
      assert_equal :task_edit, ui(app).mode
      refute_same editor, ui(app).task_editor
      assert_equal replacement_id, ui(app).task_editor.target_id
    end
  end

  def test_prompt_owns_y_text_and_escape_before_suspended_recovery
    app_on(view: :next, select: "Book flight") do |app|
      editor = prepare_done_suspended_recovery(app, draft: "prompt-safe draft")
      copied = []

      Tui::Clipboard.stub(:copy, ->(value) { copied << value; true }) do
        app.send(:handle_key, "p")
        assert_equal :prompt, ui(app).mode
        prefix = app.instance_variable_get(:@input).to_s

        %w[y space text].each_with_index do |text, index|
          app.send(:handle_key, " ") if index.positive?
          text.each_char { |key| app.send(:handle_key, key) }
        end
        app.send(:handle_key, "\e")

        assert_equal :list, ui(app).mode
        assert_equal "#{prefix}y space text", app.instance_variable_get(:@input).to_s
        assert_empty copied
        assert_same editor, app.instance_variable_get(:@suspended_task_editor)
        assert_nil ui(app).task_editor

        app.send(:handle_key, "y")
      end

      assert_equal ["prompt-safe draft"], copied
    end
  end

  def test_palette_owns_text_and_escape_before_suspended_recovery
    app_on(view: :next, select: "Book flight") do |app|
      editor = prepare_done_suspended_recovery(app, draft: "palette-safe draft")
      copied = []

      Tui::Clipboard.stub(:copy, ->(value) { copied << value; true }) do
        app.send(:handle_key, ":")
        assert_equal :palette, ui(app).mode
        %w[y d c r].each { |key| app.send(:handle_key, key) }
        assert_equal "ydcr", ui(app).action_palette.input.to_s
        app.send(:handle_key, "\e")

        assert_equal :list, ui(app).mode
        assert_empty copied
        assert_same editor, app.instance_variable_get(:@suspended_task_editor)
        assert_nil ui(app).task_editor

        app.send(:handle_key, "y")
      end

      assert_equal ["palette-safe draft"], copied
    end
  end

  def test_modal_owns_y_d_c_r_and_escape_before_suspended_recovery
    app_on(view: :next, select: "Book flight") do |app|
      editor = prepare_done_suspended_recovery(app, draft: "modal-safe draft")
      copied = []

      Tui::Clipboard.stub(:copy, ->(value) { copied << value; true }) do
        app.send(:handle_key, "?")
        assert_equal :modal, ui(app).mode
        %w[y d c r].each { |key| app.send(:handle_key, key) }
        assert_equal :modal, ui(app).mode
        app.send(:handle_key, "\e")

        assert_equal :list, ui(app).mode
        assert_empty copied
        assert_same editor, app.instance_variable_get(:@suspended_task_editor)
        assert_nil ui(app).task_editor

        app.send(:handle_key, "y")
      end

      assert_equal ["modal-safe draft"], copied
    end
  end

  def test_form_owns_y_d_c_r_and_escape_before_suspended_recovery
    app_on(view: :next, select: "Book flight") do |app|
      editor = prepare_done_suspended_recovery(app, draft: "form-safe draft")
      copied = []

      Tui::Clipboard.stub(:copy, ->(value) { copied << value; true }) do
        app.send(:handle_key, "d")
        assert_equal :form, ui(app).mode
        %w[y d c r].each { |key| app.send(:handle_key, key) }
        assert_equal "ydcr", ui(app).form.input.to_s
        app.send(:handle_key, "\e")

        assert_equal :list, ui(app).mode
        assert_empty copied
        assert_same editor, app.instance_variable_get(:@suspended_task_editor)
        assert_nil ui(app).task_editor

        app.send(:handle_key, "y")
      end

      assert_equal ["form-safe draft"], copied
    end
  end

  def test_location_move_out_of_projects_can_resume_from_another_canonical_view
    app_on(view: :projects, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "moved task draft")
      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }

      rewrite_records(app) do |records|
        record = records.delete(records.find { |candidate| candidate["id"] == target_id })
        record["parent"] = FIX[:inbox]
        records.insert(records.index { |candidate| candidate["id"] == FIX[:work] }, record)
      end

      assert_equal :suspended_task_edit, ui(app).panel.kind
      assert_match(/switch to agenda/, ui(app).panel.lines.first)
      assert_equal :agenda, app.send(:suspended_target_canonical_view)
      refute_equal target_id, ui(app).selected_id

      app.send(:handle_key, "1")
      assert_equal :agenda, ui(app).view
      assert_equal target_id, ui(app).selected_id
      assert_equal target_id, app.send(:current_item).id
      assert_equal :detail, ui(app).panel.kind

      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "\t") }
      assert_same editor, ui(app).task_editor
      assert_equal target_id, ui(app).task_editor.target_id
      assert_equal "moved task draft", editor.edit_form.value(:title)
    end
  end

  def test_deferred_suspended_target_recovers_when_deferred_rows_are_revealed
    app_on(view: :next, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "deferred task draft")
      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }

      rewrite_records(app) do |records|
        record = records.find { |candidate| candidate["id"] == target_id }
        record["tags"] = Array(record["tags"]) + ["defer"]
      end

      assert_equal :suspended_task_edit, ui(app).panel.kind
      assert_nil app.send(:suspended_target_canonical_view)
      assert_match(/not selectable/, app.instance_variable_get(:@flash))

      app.send(:handle_key, "Z")
      assert ui(app).show_deferred
      assert_equal target_id, ui(app).selected_id
      assert_equal target_id, app.send(:current_item).id
      assert_equal :detail, ui(app).panel.kind

      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "\t") }
      assert_same editor, ui(app).task_editor
      assert_equal "deferred task draft", editor.edit_form.value(:title)
    end
  end

  def test_confirmation_is_cancelled_on_suspend_and_rearmed_visibly_after_resume
    app_on(view: :next, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.focus(:state)
      editor.form.set_value(:state, "DONE")
      app.send(:handle_key, "\x13")
      assert editor.pending_confirmation

      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }
      assert_nil editor.pending_confirmation
      assert editor.dirty?(:state)
      assert_match(/Confirmation cancelled/, app.instance_variable_get(:@flash))

      # Read-mode y may run its visible list action, but cannot confirm DONE.
      Tui::Clipboard.stub(:copy, true) { app.send(:handle_key, "y") }
      task = app.instance_variable_get(:@store).items.find { |item| item.id == editor.target_id }
      assert_equal "NEXT", task.state

      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "\t") }
      assert_same editor, ui(app).task_editor
      assert_match(/Confirmation cancelled/, app.instance_variable_get(:@flash))
      assert_nil editor.pending_confirmation

      app.send(:handle_key, "\x13")
      assert editor.pending_confirmation
      assert_match(/Mark this task done.*y accepts.*n cancels/,
                   app.instance_variable_get(:@task_edit_message))
    end
  end

  def test_revert_prompt_is_cancelled_on_suspend_and_must_be_rearmed
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "keep this draft")
      app.send(:handle_key, "\e")
      assert_equal :title, editor.pending_revert

      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }
      assert_nil editor.pending_revert
      assert_equal "keep this draft", editor.edit_form.value(:title)
      assert_match(/Discard prompt cancelled/, app.instance_variable_get(:@flash))

      # Escape is now the visible read-panel action, not a hidden second revert.
      app.send(:handle_key, "\e")
      assert_nil ui(app).panel
      assert_equal "keep this draft", editor.edit_form.value(:title)

      app.send(:handle_key, "\r")
      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "\t") }
      assert_same editor, ui(app).task_editor
      assert_match(/Discard prompt cancelled/, app.instance_variable_get(:@flash))

      app.send(:handle_key, "\e")
      assert_equal :title, editor.pending_revert
      assert_equal "keep this draft", editor.edit_form.value(:title)
      app.send(:handle_key, "\e")
      assert_nil editor.pending_revert
      refute editor.dirty?(:title)
    end
  end

  def test_conflict_guidance_audits_that_local_value_is_retained
    app_on(view: :agenda, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "local conflicting title")
      rewrite_records(app) do |records|
        records.find { |record| record["id"] == target_id }["title"] = "external title"
      end

      app.send(:handle_key, "\x13")

      refute_nil editor.conflict
      assert_equal "local conflicting title", editor.edit_form.value(:title)
      assert_match(/Edit conflict.*local value retained/,
                   app.instance_variable_get(:@task_edit_message))

      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }
      assert_match(/Edit conflict.*local value retained/,
                   app.instance_variable_get(:@flash))
      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "\t") }
      assert_same editor, ui(app).task_editor
      assert_match(/Edit conflict.*local value retained/,
                   app.instance_variable_get(:@flash))
    end
  end

  def test_read_panel_resize_changes_named_preference_without_identity_change
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      identity = ui(app).panel.identity

      app.send(:handle_key, "\x0b")
      assert_equal :wide, ui(app).panel_mode
      assert_equal identity, ui(app).panel.identity

      app.send(:handle_key, "\x0c")
      assert_equal :standard, ui(app).panel_mode
      assert_equal identity, ui(app).panel.identity
    end
  end

  def test_successful_state_edit_that_leaves_view_exits_to_nearby_row
    app_on(view: :next, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.focus(:state)
      editor.form.set_value(:state, "DONE")

      app.send(:handle_key, "\x13")
      assert editor.pending_confirmation
      app.send(:handle_key, "y")

      assert_equal :list, ui(app).mode
      assert_nil ui(app).task_editor
      assert_nil ui(app).panel
      refute_equal target_id, ui(app).selected_id
      assert_match(/left the next view/, app.instance_variable_get(:@flash))
    end
  end

  def test_external_missing_editor_target_never_retargets_fallback_selection
    app_on(view: :agenda, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "\t")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "local recoverable draft")

      rewrite_records(app) { |records| records.reject! { |record| record["id"] == target_id } }

      assert_same editor, ui(app).task_editor
      assert editor.missing?
      assert_equal target_id, editor.target_id
      assert_equal "local recoverable draft", editor.edit_form.value(:title)
      refute_equal target_id, ui(app).selected_id
      assert_equal target_id, ui(app).panel.identity
      assert_match(/Task no longer exists.*y copies.*esc discards/,
                   app.instance_variable_get(:@flash))

      copied = nil
      Tui::Clipboard.stub(:copy, ->(value) { copied = value; true }) do
        app.send(:handle_key, "y")
      end
      assert_equal "local recoverable draft", copied
      assert_equal :task_edit, ui(app).mode

      app.send(:handle_key, "\e")
      assert_equal :list, ui(app).mode
      assert_nil ui(app).task_editor
      assert_nil ui(app).panel
    end
  end

  def test_shift_tab_csi_split_after_escape_is_dispatched_as_one_key
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "") do |app|
      dispatched = []
      chunks = ["\e".b, "[Z".b]
      reader = Object.new
      reader.define_singleton_method(:read_nonblock) { |_size| chunks.shift }
      original_stdin = $stdin
      $stdin = reader
      begin
        IO.stub(:select, [[reader], [], []]) do
          app.stub(:handle_key, ->(key) { dispatched << key }) do
            app.send(:read_keys)
          end
        end
      ensure
        $stdin = original_stdin
      end

      assert_equal ["\e[Z"], dispatched
      assert_equal "", app.instance_variable_get(:@key_data)
    end
  end

  def test_extracted_state_has_no_shadow_app_ivars
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "") do |app|
      extracted = %i[@mode @selected_id @view @filter @collapsed @show_deferred
                     @modal @form @action_palette]
      assert_empty extracted & app.instance_variables
      assert_instance_of Tui::UiState, ui(app)
    end
  end

  def test_popup_placement_uses_supplied_terminal_geometry
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:open_action_palette)
      app.instance_variable_set(:@sel, 99)
      popup = app.send(:current_popup, width: 42, height: 12, footer_size: 3)
      body_width = 42 - 4
      body_height = 12 - 5 - 3
      assert_operator popup[:row], :>=, 0
      assert_operator popup[:row] + popup[:lines].size, :<=, body_height
      assert_operator popup[:col], :>=, 0
      assert popup[:lines].all? { |line| Tui::Ansi.vislen(line) <= body_width },
             "palette is sized from the supplied 42-column terminal body"
    end
  end

  def test_form_popup_remains_visible_inside_an_eight_by_six_terminal
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:open_date_popup)
      popup = app.send(:current_popup, width: 8, height: 6, footer_size: 0)
      assert_equal 0, popup[:row]
      assert_equal 0, popup[:col]
      assert_equal 1, popup[:lines].size
      assert popup[:lines].all? { |line| Tui::Ansi.vislen(line) <= 4 }
    end
  end

  def test_palette_popup_remains_visible_inside_an_eight_by_six_terminal
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:open_action_palette)
      popup = app.send(:current_popup, width: 8, height: 6, footer_size: 0)
      assert_equal 0, popup[:row]
      assert_equal 0, popup[:col]
      assert_equal 1, popup[:lines].size
      assert popup[:lines].all? { |line| Tui::Ansi.vislen(line) <= 4 }
    end
  end

  def test_popup_placement_chooses_below_then_above_and_clamps_column
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "") do |app|
      popup = { lines: ["123456", "abcdef"], row: 99, col: 99 }
      below = Tui::ScreenLayout.new(width: 14, height: 11, footer: [], selected: 1)
                               .place_popup(popup, preferred_col: 8)
      assert_equal [2, 4], below.values_at(:row, :col)

      above = Tui::ScreenLayout.new(width: 14, height: 11, footer: [], selected: 5)
                               .place_popup(popup, preferred_col: 8)
      assert_equal [3, 4], above.values_at(:row, :col)
    end
  end

  def test_short_footer_keeps_active_filter_input_over_generic_hint
    fake = FakeAgent.new(running: false)
    app_with(agent: fake, input: "") do |app|
      ui(app).mode = :filter
      ui(app).filter_input.replace("界")
      footer = app.send(:fitted_footer, width: 8, height: 7)
      assert_equal 1, footer.size
      assert_includes Tui::Ansi.strip(footer.first), "界"
      refute_includes Tui::Ansi.strip(footer.first), "tab to ask"
    end
  end

  # -- deferral ----------------------------------------------------------------

  # Build an app on a sandbox gtd.org (optionally a modified fixture), park it
  # on a given view, and select the row whose item title includes `select`.
  def app_on(view:, select:, content: FIXTURE_ORG)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), content)
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config)
      ui(app).view = view
      app.send(:rows)
      rws = app.instance_variable_get(:@rows)
      idx = rws.index { |r| r.item&.title&.include?(select) }
      raise "no selectable row for #{select.inspect}" unless idx
      app.send(:select_row, idx)
      yield app
    end
  end

  def row_titles(app)
    (app.instance_variable_get(:@rows) || []).map { |r| r.item&.title }.compact
  end

  def test_defer_selected_marks_task_and_hides_it
    app_on(view: :next, select: "Water the plants") do |app|
      app.send(:defer_selected)
      store = app.instance_variable_get(:@store)
      assert store.items.find { |i| i.title.include?("Water the plants") }.deferred?
      # hidden by default (show_deferred is off), so it leaves the Next view
      refute_includes row_titles(app), "Water the plants"
      assert_match(/deferred/, app.instance_variable_get(:@flash))
    end
  end

  def test_toggle_deferred_view_reveals_and_hides
    app_on(view: :next, select: "Review PR", content: deferred_fixture) do |app|
      refute_includes row_titles(app), "Water the plants", "deferred hidden by default"
      app.send(:toggle_deferred_view)
      assert ui(app).show_deferred
      assert_includes row_titles(app), "Water the plants", "Z reveals deferred tasks"
      app.send(:toggle_deferred_view)
      refute ui(app).show_deferred
      refute_includes row_titles(app), "Water the plants", "Z again hides them"
    end
  end

  def test_filter_respects_deferred_parent_visibility
    content = dump_fixture([
      { "type" => "meta", "version" => 1 },
      { "type" => "section", "id" => "aaaa0001", "title" => "Work" },
      { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
        "title" => "deferred parent", "tags" => %w[defer] },
      { "type" => "task", "id" => "aaaa0003", "parent" => "aaaa0002", "state" => "NEXT",
        "title" => "child match" },
      { "type" => "task", "id" => "aaaa0004", "parent" => "aaaa0001", "state" => "NEXT",
        "title" => "live sibling" },
    ])

    app_on(view: :next, select: "live sibling", content: content) do |app|
      ui(app).filter = "child"
      app.send(:rows)
      refute_includes row_titles(app), "child match",
                      "flat filtering hides descendants of a deferred parent"

      ui(app).show_deferred = true
      app.send(:rows)
      assert_includes row_titles(app), "child match", "Z reveals the filtered descendant"
    end
  end

  def test_defer_selected_reactivates_when_already_deferred
    app_on(view: :next, select: "Review PR", content: deferred_fixture) do |app|
      ui(app).show_deferred = true # so the deferred task is selectable
      app.send(:rows)
      idx = app.instance_variable_get(:@rows).index { |r| r.item&.title&.include?("Water the plants") }
      app.send(:select_row, idx)
      app.send(:defer_selected)
      store = app.instance_variable_get(:@store)
      refute store.items.find { |i| i.title.include?("Water the plants") }.deferred?
      assert_match(/activated/, app.instance_variable_get(:@flash))
    end
  end

  # -- recurrence ------------------------------------------------------------

  RECUR_FIXTURE = dump_fixture([
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "cccc0001", "title" => "Work" },
    { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "NEXT",
      "title" => "Pay rent", "tags" => %w[@home], "deadline" => "2026-08-01", "recur" => "+1m" },
    { "type" => "task", "id" => "cccc0003", "parent" => "cccc0001", "state" => "NEXT",
      "title" => "Standup notes", "tags" => %w[@computer] },
  ])

  def test_open_recur_popup_prefills_current_cookie
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      assert_equal :form, ui(app).mode
      assert_equal :recurrence, ui(app).form.kind
      assert_instance_of TermForm::Fields::Input, ui(app).form.field
      assert_equal "+1m", ui(app).form.input
    end
  end

  def test_open_date_popup_uses_term_form_date_input_without_changing_quick_submit
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_date_popup)
      assert_instance_of TermForm::Fields::DateInput, ui(app).form.field

      ui(app).form.input.replace("2026-08-14")
      app.send(:handle_key, "\r")

      assert_equal :list, ui(app).mode
      assert_equal Date.new(2026, 8, 14), app.send(:current_item).deadline
    end
  end

  def test_date_and_recurrence_quick_actions_freeze_the_selected_task_id
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      selected_id = app.send(:current_item).id

      app.send(:handle_key, "d")
      assert_equal [:date, selected_id], [ui(app).form.kind, ui(app).form.target_id]

      app.send(:handle_key, "\e")
      app.send(:handle_key, "r")
      assert_equal [:recurrence, selected_id], [ui(app).form.kind, ui(app).form.target_id]
    end
  end

  def test_open_recur_popup_refuses_undated_task
    app_on(view: :next, select: "Standup notes", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      assert_equal :list, ui(app).mode, "no popup for a task with no date"
      assert_match(/schedule it first/, app.instance_variable_get(:@flash))
    end
  end

  def test_submit_recur_sets_cookie
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("weekly")
      app.send(:handle_key, "\r")
      store = app.instance_variable_get(:@store)
      assert_equal ".+1w", store.items.find { |i| i.title.include?("Pay rent") }.recur
      assert_equal :list, ui(app).mode
    end
  end

  def test_submit_recur_off_clears
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("off")
      app.send(:handle_key, "\r")
      assert_nil app.instance_variable_get(:@store).items.find { |i| i.title.include?("Pay rent") }.recur
    end
  end

  def test_submit_recur_reports_parse_error
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("bananas")
      app.send(:handle_key, "\r")
      assert_equal :form, ui(app).mode, "stays open on bad input"
      assert_match(/can't parse/, ui(app).form.error)
    end
  end

  def test_complete_selected_rolls_recurring_task_and_keeps_it
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:complete_selected)
      store = app.instance_variable_get(:@store)
      rent = store.items.find { |i| i.title.include?("Pay rent") }
      assert_equal "NEXT", rent.state, "recurring task stays open"
      assert_equal Date.new(2026, 9, 1), rent.deadline
      assert_match(/↻ Pay rent/, app.instance_variable_get(:@flash))
      # still selectable in the agenda view
      assert_includes row_titles(app), "Pay rent"
    end
  end

  # -- stable selection identity ---------------------------------------------

  SELECTION_FIXTURE = dump_fixture([
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "5e1e0001", "title" => "Work" },
    { "type" => "task", "id" => "5e1e0002", "parent" => "5e1e0001", "state" => "NEXT",
      "title" => "Alpha", "deadline" => "2026-07-11" },
    { "type" => "task", "id" => "5e1e0003", "parent" => "5e1e0001", "state" => "NEXT",
      "title" => "Beta", "deadline" => "2026-07-12" },
    { "type" => "task", "id" => "5e1e0004", "parent" => "5e1e0001", "state" => "NEXT",
      "title" => "Gamma", "deadline" => "2026-07-13" },
  ])

  def rewrite_records(app)
    store = app.instance_variable_get(:@store)
    records = Tasks::Format.parse(File.read(store.org, encoding: "UTF-8")).records
    yield records
    File.write(store.org, dump_fixture(records))
    app.send(:reload_store)
  end

  def prepare_done_suspended_recovery(app, draft:)
    target_id = app.send(:current_item).id
    app.send(:handle_key, "\r")
    app.send(:handle_key, "\t")
    editor = ui(app).task_editor
    editor.form.set_value(:title, draft)
    small = Struct.new(:winsize).new([7, 46])
    IO.stub(:console, small) { capture_io { app.send(:paint) } }

    rewrite_records(app) do |records|
      record = records.find { |candidate| candidate["id"] == target_id }
      record["state"] = "DONE"
      record["closed"] = "2026-07-13"
    end

    assert_equal :list, ui(app).mode
    assert_equal :suspended_task_edit, ui(app).panel.kind
    assert_same editor, app.instance_variable_get(:@suspended_task_editor)
    editor
  end

  def test_external_resort_retains_selected_task_by_id
    app_on(view: :agenda, select: "Beta", content: SELECTION_FIXTURE) do |app|
      old_row = app.instance_variable_get(:@sel)
      rewrite_records(app) do |records|
        records.find { |record| record["id"] == "5e1e0004" }["deadline"] = "2026-07-10"
      end

      assert_equal "Beta", app.send(:current_item).title
      assert_equal "5e1e0003", ui(app).selected_id
      refute_equal old_row, app.instance_variable_get(:@sel), "render coordinate follows the resort"
    end
  end

  def test_inserting_an_earlier_record_retains_id_across_line_shift
    app_on(view: :agenda, select: "Beta", content: SELECTION_FIXTURE) do |app|
      old_line = app.send(:current_item).line
      rewrite_records(app) do |records|
        records.insert(2,
          { "type" => "task", "id" => "5e1e0005", "parent" => "5e1e0001", "state" => "DONE",
            "title" => "Inserted history", "closed" => "2026-07-09" })
      end

      assert_equal "5e1e0003", app.send(:current_item).id
      assert_operator app.send(:current_item).line, :>, old_line
    end
  end

  def test_deleted_selection_falls_back_to_nearest_row_and_updates_id
    app_on(view: :agenda, select: "Beta", content: SELECTION_FIXTURE) do |app|
      rewrite_records(app) do |records|
        records.reject! { |record| record["id"] == "5e1e0003" }
      end

      assert_equal "Gamma", app.send(:current_item).title
      assert_equal "5e1e0004", ui(app).selected_id
    end
  end

  def test_view_filter_and_navigation_keep_id_synchronized
    app_on(view: :agenda, select: "Book flight", content: FIXTURE_ORG) do |app|
      app.send(:switch_view, 2)
      assert_equal FIX[:flight], app.send(:current_item).id

      ui(app).filter = "flight"
      app.send(:rows)
      assert_equal FIX[:flight], app.send(:current_item).id

      ui(app).filter = nil
      app.send(:rows)
      app.send(:move, 1)
      assert_equal app.send(:current_item).id, ui(app).selected_id
    end
  end

  def test_rebuild_keeps_selected_occurrence_when_task_has_multiple_contexts
    records = Tasks::Format.parse(SELECTION_FIXTURE).records
    beta = records.find { |record| record["id"] == "5e1e0003" }
    beta["tags"] = %w[@alpha @omega]
    content = dump_fixture(records)

    app_on(view: :next, select: "Beta", content: content) do |app|
      app.send(:move, 1)
      second_occurrence = app.instance_variable_get(:@sel)
      assert_equal "5e1e0003", app.send(:current_item).id

      app.send(:rows)
      assert_equal second_occurrence, app.instance_variable_get(:@sel)
      assert_equal "5e1e0003", ui(app).selected_id
    end
  end

  # -- outliner collapse / expand (h l H L) ----------------------------------

  # Work → "Ship release" (07-10) → "write notes" (07-12) → "grandchild task",
  # plus a sibling leaf "undated rider"; Home → "solo top" (07-15), a top-level
  # leaf. Rendered in agenda the rows are, in order: Ship release, write notes,
  # grandchild task, undated rider, solo top.
  NESTED_APP = dump_fixture([
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "aaaa0001", "title" => "Work" },
    { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
      "title" => "Ship release", "deadline" => "2026-07-10" },
    { "type" => "task", "id" => "aaaa0003", "parent" => "aaaa0002", "state" => "TODO",
      "title" => "write notes", "deadline" => "2026-07-12" },
    { "type" => "task", "id" => "aaaa0004", "parent" => "aaaa0003", "state" => "NEXT",
      "title" => "grandchild task" },
    { "type" => "task", "id" => "aaaa0005", "parent" => "aaaa0002", "state" => "TODO",
      "title" => "undated rider" },
    { "type" => "section", "id" => "aaaa0006", "title" => "Home" },
    { "type" => "task", "id" => "aaaa0007", "parent" => "aaaa0006", "state" => "NEXT",
      "title" => "solo top", "deadline" => "2026-07-15" },
  ])

  def sel_title(app)
    rws = app.instance_variable_get(:@rows)
    rws[app.instance_variable_get(:@sel)]&.item&.title
  end

  def collapsed(app) = ui(app).collapsed

  def test_collapse_selected_folds_subtree_and_holds_selection
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      before = row_titles(app).size
      app.send(:collapse_selected)
      titles = row_titles(app)
      assert_equal "Ship release", sel_title(app), "selection stays on the folded parent"
      refute_includes titles, "write notes", "subtree hidden"
      refute_includes titles, "grandchild task"
      refute_includes titles, "undated rider"
      assert_operator titles.size, :<, before, "rows shrank"
      ship = app.instance_variable_get(:@rows).find { |r| r.item&.title == "Ship release" }
      assert_includes Tui::Ansi.strip(ship.text), "(3)", "hidden-descendant count shows"
      assert_includes collapsed(app), "aaaa0002"
    end
  end

  def test_collapse_again_on_top_level_collapsed_is_noop
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      app.send(:collapse_selected) # fold
      folded = row_titles(app)
      sel = app.instance_variable_get(:@sel)
      app.send(:collapse_selected) # again: parent is a section → no-op
      assert_equal folded, row_titles(app)
      assert_equal sel, app.instance_variable_get(:@sel)
      assert_equal "Ship release", sel_title(app)
    end
  end

  def test_collapse_again_on_folded_child_jumps_to_parent
    app_on(view: :agenda, select: "write notes", content: NESTED_APP) do |app|
      app.send(:collapse_selected) # write notes has a child → folds
      assert_equal "write notes", sel_title(app)
      assert_includes collapsed(app), "aaaa0003"
      app.send(:collapse_selected) # folded now → climb to parent
      assert_equal "Ship release", sel_title(app)
    end
  end

  def test_collapse_on_leaf_jumps_to_parent
    app_on(view: :agenda, select: "grandchild task", content: NESTED_APP) do |app|
      app.send(:collapse_selected)
      assert_equal "write notes", sel_title(app), "leaf climbs to its parent row"
      assert_empty collapsed(app), "a leaf never folds anything"
    end
  end

  def test_collapse_on_top_level_leaf_is_noop
    app_on(view: :agenda, select: "solo top", content: NESTED_APP) do |app|
      before = row_titles(app)
      app.send(:collapse_selected)
      assert_equal "solo top", sel_title(app)
      assert_equal before, row_titles(app)
      assert_empty collapsed(app)
    end
  end

  def test_expand_selected_unfolds_and_holds_selection
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      app.send(:collapse_selected)
      refute_includes row_titles(app), "write notes"
      app.send(:expand_selected)
      assert_includes row_titles(app), "write notes", "subtree back"
      assert_equal "Ship release", sel_title(app)
      assert_empty collapsed(app)
    end
  end

  def test_expand_selected_on_expanded_node_is_noop
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      before = row_titles(app)
      app.send(:expand_selected) # nothing folded → no-op
      assert_equal before, row_titles(app)
      assert_empty collapsed(app)
    end
  end

  def test_collapse_all_folds_every_parent
    app_on(view: :agenda, select: "grandchild task", content: NESTED_APP) do |app|
      app.send(:collapse_all)
      set = collapsed(app)
      assert_includes set, "aaaa0002", "Ship release folded"
      assert_includes set, "aaaa0003", "write notes folded"
      refute_includes set, "aaaa0004", "the leaf grandchild is not a parent"
      refute_includes set, "aaaa0007", "the top-level leaf is not a parent"
      titles = row_titles(app)
      refute_includes titles, "write notes"
      refute_includes titles, "grandchild task"
      assert_includes titles, "Ship release"
      assert_includes titles, "solo top"
      # the selection sat on a now-hidden row; clamp lands it on a visible task
      landed = app.instance_variable_get(:@rows)[app.instance_variable_get(:@sel)]
      assert landed&.item, "selection clamps onto a visible task"
    end
  end

  def test_expand_all_restores_full_tree
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      app.send(:collapse_all)
      app.send(:expand_all)
      assert_empty collapsed(app)
      titles = row_titles(app)
      ["Ship release", "write notes", "grandchild task", "undated rider", "solo top"].each do |t|
        assert_includes titles, t
      end
    end
  end

  def test_collapse_expand_do_not_crash_during_filter
    app_on(view: :agenda, select: "Ship release", content: NESTED_APP) do |app|
      ui(app).filter = "e" # flat path: rows carry no node
      app.send(:rows)
      before = row_titles(app)
      app.send(:collapse_selected) # node nil → no-op
      app.send(:expand_selected)   # node nil → no-op
      assert_equal before, row_titles(app), "flat filter rows unchanged by h/l"
      # H/L still touch the store tree, but the flat filter rows don't change.
      app.send(:collapse_all)
      app.send(:expand_all)
      assert_equal before, row_titles(app)
    end
  end
end
