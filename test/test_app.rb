# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"
require "tui/text_input"

class TestApp < Minitest::Test
  def ui(app) = app.instance_variable_get(:@ui)
  def intake_counts(app) = app.send(:tab_counts).fetch(:inbox).to_h

  PROPOSAL_APP = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "ee000001", "title" => "Inbox" },
    { "type" => "task", "id" => "ee000002", "parent" => "ee000001",
      "state" => "PROPOSED", "title" => "Alpha proposal", "body" => "Alpha rationale" },
    { "type" => "task", "id" => "ee000003", "parent" => "ee000001",
      "state" => "PROPOSED", "title" => "Beta proposal", "body" => "Beta rationale" },
    { "type" => "task", "id" => "ee000004", "parent" => "ee000001",
      "state" => "INBOX", "title" => "Accepted task" },
  ]).freeze

  FUTURE_PROPOSAL_APP = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "ed000001", "title" => "Inbox" },
    { "type" => "task", "id" => "ed000002", "parent" => "ed000001",
      "state" => "PROPOSED", "title" => "Visible proposal" },
    { "type" => "task", "id" => "ed000003", "parent" => "ed000001",
      "state" => "INBOX", "title" => "Existing capture" },
    { "type" => "task", "id" => "ed000004", "parent" => "ed000001",
      "state" => "PROPOSED", "title" => "Future proposal", "scheduled" => "2026-08-01" },
    { "type" => "task", "id" => "ed000005", "parent" => "ed000001",
      "state" => "INBOX", "title" => "Held capture", "tags" => ["defer"] },
  ]).freeze

  CONTEXT_PROPOSAL_APP = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "ef000001", "title" => "Inbox" },
    { "type" => "task", "id" => "ef000002", "parent" => "ef000001",
      "state" => "PROPOSED", "title" => "Home proposal", "tags" => ["@home"] },
    { "type" => "task", "id" => "ef000003", "parent" => "ef000001",
      "state" => "PROPOSED", "title" => "Work proposal", "tags" => ["@work"] },
  ]).freeze

  INBOX_BADGE_APP = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "f0000001", "title" => "Inbox" },
    { "type" => "task", "id" => "f0000002", "parent" => "f0000001",
      "state" => "INBOX", "title" => "Home capture", "tags" => ["@home"] },
    { "type" => "task", "id" => "f0000003", "parent" => "f0000001",
      "state" => "INBOX", "title" => "Work capture", "tags" => ["@work"] },
    { "type" => "task", "id" => "f0000004", "parent" => "f0000001",
      "state" => "INBOX", "title" => "Held capture", "tags" => ["defer"] },
    { "type" => "task", "id" => "f0000005", "parent" => "f0000001",
      "state" => "NEXT", "title" => "Already processed" },
  ]).freeze

  # Resolve the panel column count the same way the frame does, so resize
  # assertions read the realized width rather than the stored offset.
  def panel_width(app)
    height, width = app.send(:terminal_size)
    app.send(:screen_layout, width: width, height: height, panel: true).panel_width
  end

  def test_idle_minute_boundary_rebuilds_availability_without_a_file_change
    Dir.mktmpdir do |dir|
      today = Date.today
      records = FIXTURE_RECORDS.map(&:dup)
      task = records.find { |record| record["id"] == FIX[:plants] }
      task["scheduled"] = today.iso8601
      task["scheduled_time"] = { "local" => "09:31", "timezone" => "UTC" }
      File.write(File.join(dir, "tasks.jsonl"), Tasks::Format.dump(records))
      now = Time.utc(today.year, today.month, today.day, 9, 30)
      app = Tui::App.new(
        root: dir, paths: Tasks::Config.for_dir(dir), llm_config: default_llm_config,
        date_provider: -> { today }, time_provider: -> { now }
      )

      refute app.send(:read_model).task_for(FIX[:plants]).available?
      app.send(:idle_layout_changed?)
      now += 60
      assert app.send(:idle_layout_changed?)
      assert app.send(:read_model).task_for(FIX[:plants]).available?
    end
  end

  def test_schema_v1_opens_with_a_non_destructive_migration_prompt
    Dir.mktmpdir do |dir|
      records = FIXTURE_RECORDS.map(&:dup)
      records.first["version"] = 1
      raw = records.map { |record| JSON.generate(record) }.join("\n") + "\n"
      path = File.join(dir, "tasks.jsonl")
      File.write(path, raw)

      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir), llm_config: default_llm_config)
      assert_equal :modal, ui(app).mode
      assert_equal :migration_required, ui(app).modal.kind
      text = ui(app).modal.lines.join("\n")
      assert_includes text, "tasks migrate --dry-run"
      assert_includes text, ".v1.bak"
      assert_equal raw, File.read(path)

      app.send(:modal_key, "y")
      assert_equal :list, ui(app).mode
      migrated = Tasks::Format.parse(File.read(path)).records
      assert_equal 2, migrated.first.fetch("version")
      assert_equal raw, File.read("#{path}.v1.bak")
    end
  end

  def test_dismissed_schema_v1_prompt_keeps_archive_and_history_read_only
    Dir.mktmpdir do |dir|
      records = FIXTURE_RECORDS.map(&:dup)
      records.first["version"] = 1
      raw = records.map { |record| JSON.generate(record) }.join("\n") + "\n"
      path = File.join(dir, "tasks.jsonl")
      File.write(path, raw)

      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir), llm_config: default_llm_config)
      app.send(:modal_key, "\e")
      assert_equal :list, ui(app).mode

      app.send(:archive_sweep)
      assert_equal :migration_required, ui(app).modal.kind
      app.send(:modal_key, "\e")
      app.send(:undo_last)
      assert_equal :migration_required, ui(app).modal.kind
      assert_equal raw, File.read(path)
    end
  end

  # Single-run adapter fake used behind AgentQueue's injected factory.
  class FakeAgent
    attr_reader :started, :output, :process_status, :exit_status

    def initialize(running:, available: true)
      @running = running
      @available = available
      @started = []
      @output = +""
      @process_status = nil
      @exit_status = nil
    end

    def running? = @running
    def available? = @available
    def start(text, model:)
      @started << [text, model]
      @running = true
      self
    end
    def success? = true
    def cancel = @running = false
    def io = nil
  end

  def app_with(agent: nil, agents: nil, available: true, input:)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
      pool = Array(agents || [agent])
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config,
                         agent_factory: ->(_entry) { pool.shift || agent },
                         agent_probe: ->(_entry) { available })
      app.instance_variable_set(:@input, Tui::TextInput.new(input))
      yield app
    end
  end

  def test_submit_prompt_queues_while_agent_running
    active = FakeAgent.new(running: false)
    waiting = FakeAgent.new(running: false)
    app_with(agents: [active, waiting], input: "first request") do |app|
      app.send(:submit_prompt)
      app.instance_variable_get(:@input).replace("reschedule the flight")
      ui(app).mode = :prompt
      app.send(:submit_prompt)

      assert_equal [["first request", "sonnet"]], active.started
      assert_empty waiting.started, "queued adapter must not start alongside the active one"
      queue = app.instance_variable_get(:@agent_queue)
      assert_equal 1, queue.pending_count
      assert_match(/queued agent request/, app.instance_variable_get(:@flash))
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
    app_with(agent: fake, available: false, input: "do a thing") do |app|
      app.send(:submit_prompt)
      assert_empty fake.started, "must not start an unavailable agent"
      assert_match(/not available/, app.instance_variable_get(:@flash))
      assert_equal "do a thing", app.instance_variable_get(:@input).to_s
      assert_equal :prompt, ui(app).mode
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

  # Approving a proposal moves it between sections, so the paired count moves
  # together and selection advances through the decision queue before landing
  # in accepted Inbox work.
  def test_combined_inbox_decision_keys_update_counts_selection_and_history_immediately
    app_on(view: :inbox, select: "Alpha proposal", content: PROPOSAL_APP) do |app|
      assert_equal({ inbox: 1, approvals: 2 }, intake_counts(app))

      app.send(:handle_key, "a")
      assert_equal "INBOX", record_for(org_path(app), title: "Alpha proposal")["state"]
      assert_equal "Beta proposal", app.send(:current_item).title
      assert_equal({ inbox: 2, approvals: 1 }, intake_counts(app))

      app.send(:handle_key, "u")
      assert_equal "PROPOSED", record_for(org_path(app), title: "Alpha proposal")["state"]
      assert_equal({ inbox: 1, approvals: 2 }, intake_counts(app))

      app.send(:handle_key, "\x12")
      assert_equal "INBOX", record_for(org_path(app), title: "Alpha proposal")["state"]
      assert_equal({ inbox: 2, approvals: 1 }, intake_counts(app))

      app.send(:handle_key, "r")
      assert_equal "CANCELLED", record_for(org_path(app), title: "Beta proposal")["state"]
      assert_equal({ inbox: 2, approvals: 0 }, intake_counts(app))
      assert_equal "Alpha proposal", app.send(:current_item).title

      app.send(:handle_key, "u")
      assert_equal "PROPOSED", record_for(org_path(app), title: "Beta proposal")["state"]
      assert_equal({ inbox: 2, approvals: 1 }, intake_counts(app))
    end
  end

  def test_approving_manually_selected_last_proposal_wraps_to_earlier_proposal
    app_on(view: :inbox, select: "Beta proposal", content: PROPOSAL_APP) do |app|
      app.send(:handle_key, "a")

      assert_equal "INBOX", record_for(org_path(app), title: "Beta proposal")["state"]
      assert_equal "PROPOSED", record_for(org_path(app), title: "Alpha proposal")["state"]
      assert_equal "Alpha proposal", app.send(:current_item).title
      assert_equal({ inbox: 2, approvals: 1 }, intake_counts(app))
    end
  end

  def test_rejecting_manually_selected_last_proposal_wraps_to_earlier_proposal
    app_on(view: :inbox, select: "Beta proposal", content: PROPOSAL_APP) do |app|
      app.send(:handle_key, "r")

      assert_equal "CANCELLED", record_for(org_path(app), title: "Beta proposal")["state"]
      assert_equal "PROPOSED", record_for(org_path(app), title: "Alpha proposal")["state"]
      assert_equal "Alpha proposal", app.send(:current_item).title
      assert_equal({ inbox: 1, approvals: 1 }, intake_counts(app))
    end
  end

  def test_final_approval_selects_visible_capture_but_hidden_future_capture_falls_back
    today = -> { Date.new(2026, 7, 1) }
    app_on(view: :inbox, select: "Visible proposal", content: FUTURE_PROPOSAL_APP,
           date_provider: today) do |app|
      assert_equal({ inbox: 1, approvals: 2 }, intake_counts(app))
      ui(app).show_deferred = true
      assert_equal({ inbox: 2, approvals: 2 }, intake_counts(app))
      assert_equal 2, app.send(:rows).count { |row| row.item&.proposed? },
                   "Z never hides or adds proposals"
      ui(app).show_deferred = false
      app.send(:rows)

      app.send(:handle_key, "a")
      assert_equal "Future proposal", app.send(:current_item).title,
                   "another proposal remains the rapid-review target"

      app.send(:handle_key, "a")
      assert_equal({ inbox: 2, approvals: 0 }, intake_counts(app))
      refute_includes row_titles(app), "Future proposal"
      assert_equal "Visible proposal", app.send(:current_item).title,
                   "hidden approved work falls back to the nearest visible Inbox row"

      ui(app).show_deferred = true
      assert_equal({ inbox: 4, approvals: 0 }, intake_counts(app))
      assert_includes app.send(:rows).filter_map { |row| row.item&.title }, "Future proposal"
      assert_includes row_titles(app), "Held capture"
    end
  end

  def test_combined_inbox_counts_and_rows_respect_context_and_text_filters
    app_on(view: :inbox, select: "Home proposal", content: CONTEXT_PROPOSAL_APP) do |app|
      assert_equal({ inbox: 0, approvals: 2 }, intake_counts(app))

      ui(app).context_filters = ["@home"]
      assert_equal({ inbox: 0, approvals: 1 }, intake_counts(app))
      assert_equal ["Home proposal"], app.send(:rows).filter_map { |r| r.item&.title }

      ui(app).context_filters = ["@errand"]
      assert_equal({ inbox: 0, approvals: 0 }, intake_counts(app))
      assert_empty app.send(:rows).filter_map { |r| r.item&.title }

      ui(app).context_filters = []
      ui(app).filter = "work"
      assert_equal({ inbox: 0, approvals: 1 }, intake_counts(app))
      assert_equal ["Work proposal"], app.send(:rows).filter_map { |r| r.item&.title }
    end
  end

  # The inbox badge counts the captures the tab holds under the active filters:
  # the `@`/`/` filters narrow it, a non-INBOX state never counts, and an
  # indefinitely held capture stays out until `show_deferred` reveals it —
  # asserted against the rendered titles so badge and list can't drift.
  def test_inbox_badge_counts_only_the_captures_the_inbox_tab_would_show
    app_on(view: :inbox, select: "Home capture", content: INBOX_BADGE_APP) do |app|
      assert_equal({ inbox: 2, approvals: 0 }, intake_counts(app))
      assert_equal ["Home capture", "Work capture"],
                   app.send(:rows).filter_map { |r| r.item&.title }

      ui(app).show_deferred = true
      assert_equal({ inbox: 3, approvals: 0 }, intake_counts(app))
      assert_equal ["Home capture", "Work capture", "Held capture"],
                   app.send(:rows).filter_map { |r| r.item&.title }
      ui(app).show_deferred = false

      ui(app).context_filters = ["@home"]
      assert_equal({ inbox: 1, approvals: 0 }, intake_counts(app))
      assert_equal ["Home capture"], app.send(:rows).filter_map { |r| r.item&.title }

      ui(app).context_filters = ["@errand"]
      assert_equal({ inbox: 0, approvals: 0 }, intake_counts(app))
      assert_empty app.send(:rows).filter_map { |r| r.item&.title }

      ui(app).context_filters = []
      ui(app).filter = "capture"
      assert_equal({ inbox: 2, approvals: 0 }, intake_counts(app))
      assert_equal ["Home capture", "Work capture"],
                   app.send(:rows).filter_map { |r| r.item&.title }

      ui(app).filter = "processed"
      assert_equal({ inbox: 0, approvals: 0 }, intake_counts(app))
      assert_empty app.send(:rows).filter_map { |r| r.item&.title }
    end
  end

  NESTED_INBOX_APP = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "f1000001", "title" => "Inbox" },
    { "type" => "task", "id" => "f1000002", "parent" => "f1000001",
      "state" => "INBOX", "title" => "Tagged parent", "tags" => ["@home"] },
    { "type" => "task", "id" => "f1000003", "parent" => "f1000002",
      "state" => "INBOX", "title" => "Untagged inbox child" },
    { "type" => "task", "id" => "f1000004", "parent" => "f1000002",
      "state" => "NEXT", "title" => "Next rider" },
  ]).freeze

  # The badge counts tasks in the tab, not rows on screen, and in tree mode the
  # two legitimately differ in both directions. This pins that contract with the
  # rendered rows next to each count, so the gaps are deliberate and visible
  # rather than discovered later as a bug:
  #
  #   - a NEXT child rides along under an INBOX anchor for context. It is on
  #     screen and is not inbox work, so it is not counted.
  #   - under an `@` filter, an untagged INBOX child still rides along under its
  #     matching parent. It is on screen and does not match the filter, so it is
  #     not counted — the badge stays the number `tasks inbox @home` reports.
  #   - collapsing the anchor hides descendant rows but does not empty the
  #     inbox, so the count holds while the row disappears. Counting rows would
  #     make folding a subtree look like progress.
  def test_inbox_badge_counts_tasks_in_the_tab_not_rows_on_screen
    app_on(view: :inbox, select: "Tagged parent", content: NESTED_INBOX_APP) do |app|
      titles = -> { app.send(:rows).filter_map { |r| r.item&.title } }

      # Unfiltered and expanded: both INBOX tasks counted, the NEXT rider not.
      assert_equal({ inbox: 2, approvals: 0 }, intake_counts(app))
      assert_equal ["Tagged parent", "Untagged inbox child", "Next rider"], titles.call

      # `@home` matches only the parent; the untagged child still renders.
      ui(app).context_filters = ["@home"]
      assert_equal({ inbox: 1, approvals: 0 }, intake_counts(app))
      assert_equal ["Tagged parent", "Untagged inbox child", "Next rider"], titles.call

      # Folding removes the rows, not the inbox work.
      ui(app).context_filters = []
      ui(app).collapsed = Set.new(["f1000002"])
      assert_equal({ inbox: 2, approvals: 0 }, intake_counts(app))
      assert_equal ["Tagged parent"], titles.call
    end
  end

  # Contexts ride the inbox row itself, ahead of the badge column, so
  # processing a capture doesn't need the detail panel to answer "where?".
  def test_inbox_rows_show_their_contexts_inline
    app_on(view: :inbox, select: "Home capture", content: INBOX_BADGE_APP) do |app|
      texts = app.send(:rows).filter_map { |r| Tui::Ansi.strip(r.text) if r.item }

      assert_includes texts, "    Home capture  @home"
      assert_includes texts, "    Work capture  @work"
    end
  end

  # The constructor delegates its presentation-cache setup to clear_row_caches
  # rather than repeating the ivar list, so the two can't drift. Both halves of
  # that arrangement are load-bearing and neither shows up in feature tests: a
  # fresh App must already be in the cleared state, and clear_row_caches must
  # stay assignment-only so it is safe to call before the App is built.
  def test_a_fresh_app_starts_in_the_cleared_cache_state
    app_on(view: :agenda, select: "Book flight") do |app|
      fresh = Tui::App.new(root: File.dirname(org_path(app)),
                           paths: Tasks::Config.for_dir(File.dirname(org_path(app))),
                           llm_config: default_llm_config)
      before = fresh.instance_variables.sort.to_h { |n| [n, fresh.instance_variable_get(n)] }
      fresh.send(:clear_row_caches)
      after = fresh.instance_variables.sort.to_h { |n| [n, fresh.instance_variable_get(n)] }

      assert_equal before, after, "constructing an App must leave the caches cleared"
      assert_equal 0, before.fetch(:@row_item_count)
    end
  end

  def test_clearing_row_caches_only_assigns_and_needs_no_constructed_state
    # Runs against an allocated-but-uninitialized App: anything that reads
    # collaborators (@store, @ui, …) or calls out would blow up here, which is
    # exactly what would make the constructor's call unsafe.
    bare = Tui::App.allocate
    bare.send(:clear_row_caches)

    assert_equal 0, bare.instance_variable_get(:@row_item_count)
    assert_nil bare.instance_variable_get(:@rows)
    assert_nil bare.instance_variable_get(:@tab_counts)
  end

  def test_proposal_decision_keys_work_with_detail_panel_open
    app_on(view: :inbox, select: "Alpha proposal", content: PROPOSAL_APP) do |app|
      app.send(:handle_key, "\r")
      assert_equal :detail, ui(app).panel.kind
      assert_includes ui(app).panel.lines.join("\n"), "Alpha rationale"

      app.send(:handle_key, "r")
      assert_equal "CANCELLED", record_for(org_path(app), title: "Alpha proposal")["state"]
      assert_equal "Beta proposal", app.send(:current_item).title
    end
  end


  def test_detail_tab_focuses_prompt_while_shift_tab_enters_editor_at_last_field
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      assert ui(app).panel

      app.send(:handle_key, "\t")
      assert_equal :prompt, ui(app).mode
      assert_nil ui(app).task_editor
      assert_equal :detail, ui(app).panel.kind

      app.send(:handle_key, "\e")
      assert_equal :list, ui(app).mode
      app.send(:handle_key, "\e[Z")
      assert_equal :task_edit, ui(app).mode
      assert_equal :state, ui(app).task_editor.focused_key
    end
  end

  def test_task_editor_opens_on_a_stable_target_in_all_six_views
    targets = {
      agenda: "Book flight",
      next: "Book flight",
      quadrants: "Book flight",
      inbox: "random thought",
      projects: "Book flight",
      outline: "Book flight",
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
      app.send(:handle_key, "e")
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
      app.send(:handle_key, "e")
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
      app.send(:handle_key, "e")
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
      assert_match(/\A╭─+╮\z/, Tui::Ansi.strip(frame.first))
      assert_match(/\A╰─+╯\z/, Tui::Ansi.strip(frame.last))
      refute ui(app).panel.lines.any? { |line| line.match?(/[\r\n]/) }
    end
  end

  def test_exact_boundary_notes_use_two_panel_rows_in_default_and_mono_frames
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
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
        assert_match(/\A╭─+╮\z/, Tui::Ansi.strip(frame.first), theme)
        assert_match(/\A╰─+╯\z/, Tui::Ansi.strip(frame.last), theme)
      end
    end
  end

  def test_ctrl_s_saves_in_place_and_ctrl_o_returns_to_read_panel
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "Book flight safely")

      app.send(:handle_key, "\x13")
      assert_equal :task_edit, ui(app).mode
      refute app.send(:external_change?), "the editor's own write is absorbed by the watcher Store"
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
      app.send(:handle_key, "e")
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
      app.send(:handle_key, "e")
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
      app.send(:handle_key, "e")
      refute ui(app).task_editor.dirty?

      app.send(:handle_key, "\x03")

      assert app.instance_variable_get(:@quit)
      assert_nil ui(app).modal
    end

    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
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
        app.send(:handle_key, "e")
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

  def test_dirty_editor_quit_confirmation_also_accounts_for_agent_queue
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
      ui(app).task_editor.form.set_value(:title, "unsaved with agents")

      queue = Object.new
      queue.define_singleton_method(:work?) { true }
      queue.define_singleton_method(:active?) { true }
      queue.define_singleton_method(:pending_count) { 2 }
      queue.define_singleton_method(:shutdown) { @shutdown = true }
      queue.define_singleton_method(:shutdown?) { !!@shutdown }
      app.instance_variable_set(:@agent_queue, queue)

      app.send(:handle_key, "\x03")
      text = ui(app).modal.lines.join(" ")
      assert_includes text, "active request"
      assert_includes text, "2 queued requests"
      refute queue.shutdown?

      app.send(:handle_key, "\r")
      assert app.instance_variable_get(:@quit)
      assert queue.shutdown?
    end
  end

  def test_panel_resize_preserves_entire_editor_and_performs_no_write
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
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
      app.send(:handle_key, "e")
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
      assert_equal Date.new(2026, 7, 20), editor.edit_form.value(:deadline).date
      assert_equal coalesce_key, editor.coalesce_key
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)
    end
  end


  def test_below_minimum_height_suspends_editor_and_reentry_preserves_draft
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
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

      app.send(:handle_key, "\t")
      assert_equal :prompt, ui(app).mode
      assert_same editor, app.instance_variable_get(:@suspended_task_editor)
      assert_nil ui(app).task_editor
      assert_equal :detail, ui(app).panel.kind
      app.send(:handle_key, "\e")
      assert_equal :list, ui(app).mode

      # The invisible editor no longer captures list keys.
      original_id = ui(app).selected_id
      app.send(:handle_key, "j")
      refute_equal original_id, ui(app).selected_id
      assert_equal "narrow draft", editor.edit_form.value(:title)

      app.send(:handle_key, "k")
      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "e") }
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
        IO.stub(:console, console) { app.send(:handle_key, "e") }
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
        app.send(:handle_key, "e")
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
      app.send(:handle_key, "e")
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

      # Widening and explicit edit cannot activate or confirm the hidden missing session.
      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "e") }
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
      IO.stub(:console, wide) { app.send(:handle_key, "e") }
      assert_equal :task_edit, ui(app).mode
      refute_same editor, ui(app).task_editor
      assert_equal replacement_id, ui(app).task_editor.target_id
    end
  end

  def test_done_suspended_target_uses_inert_recovery_then_allows_new_editor
    app_on(view: :next, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
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
      assert_match(/switch to outline/, ui(app).panel.lines.first)
      assert_equal :outline, app.send(:suspended_target_canonical_view)

      app.send(:handle_key, "\t")
      assert_equal :prompt, ui(app).mode
      assert_same editor, app.instance_variable_get(:@suspended_task_editor)
      assert_equal :suspended_task_edit, ui(app).panel.kind
      app.send(:handle_key, "\e")
      assert_equal :list, ui(app).mode

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
      IO.stub(:console, wide) { app.send(:handle_key, "e") }
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
        assert_equal :modal_filter, ui(app).mode
        assert_equal "ydcr", ui(app).modal.filter
        app.send(:handle_key, "\e") # clears the typed filter, stays on the modal
        assert_equal :modal, ui(app).mode
        app.send(:handle_key, "\e") # closes the modal

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
      app.send(:handle_key, "e")
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
      IO.stub(:console, wide) { app.send(:handle_key, "e") }
      assert_same editor, ui(app).task_editor
      assert_equal target_id, ui(app).task_editor.target_id
      assert_equal "moved task draft", editor.edit_form.value(:title)
    end
  end

  def test_deferred_suspended_target_recovers_when_deferred_rows_are_revealed
    app_on(view: :next, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
      editor = ui(app).task_editor
      editor.form.set_value(:title, "deferred task draft")
      small = Struct.new(:winsize).new([7, 46])
      IO.stub(:console, small) { capture_io { app.send(:paint) } }

      rewrite_records(app) do |records|
        record = records.find { |candidate| candidate["id"] == target_id }
        record["tags"] = Array(record["tags"]) + ["defer"]
      end

      assert_equal :suspended_task_edit, ui(app).panel.kind
      assert_equal :outline, app.send(:suspended_target_canonical_view)
      assert_match(/switch to outline/, app.instance_variable_get(:@flash))

      app.send(:handle_key, "Z")
      assert ui(app).show_deferred
      assert_equal target_id, ui(app).selected_id
      assert_equal target_id, app.send(:current_item).id
      assert_equal :detail, ui(app).panel.kind

      wide = Struct.new(:winsize).new([18, 80])
      IO.stub(:console, wide) { app.send(:handle_key, "e") }
      assert_same editor, ui(app).task_editor
      assert_equal "deferred task draft", editor.edit_form.value(:title)
    end
  end

  def test_confirmation_is_cancelled_on_suspend_and_rearmed_visibly_after_resume
    app_on(view: :next, select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
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
      IO.stub(:console, wide) { app.send(:handle_key, "e") }
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
      app.send(:handle_key, "e")
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
      IO.stub(:console, wide) { app.send(:handle_key, "e") }
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
      app.send(:handle_key, "e")
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
      IO.stub(:console, wide) { app.send(:handle_key, "e") }
      assert_same editor, ui(app).task_editor
      assert_match(/Edit conflict.*local value retained/,
                   app.instance_variable_get(:@flash))
    end
  end

  def test_read_panel_resize_steps_one_column_without_identity_change
    console = Struct.new(:winsize).new([24, 80])
    app_on(view: :agenda, select: "Book flight") do |app|
      IO.stub(:console, console) do
        app.send(:handle_key, "\r")
        identity = ui(app).panel.identity
        base = panel_width(app)

        app.send(:handle_key, "\x0b") # ctrl-k grows by exactly one column
        assert_equal base + 1, panel_width(app)
        assert_equal identity, ui(app).panel.identity
        assert_match(/task panel: #{base + 1} cols/, app.instance_variable_get(:@flash))

        app.send(:handle_key, "\x0c") # ctrl-l returns the column
        assert_equal base, panel_width(app)
        assert_equal identity, ui(app).panel.identity
      end
    end
  end

  def test_read_panel_resize_clamps_hold_at_extremes
    console = Struct.new(:winsize).new([24, 80])
    app_on(view: :agenda, select: "Book flight") do |app|
      IO.stub(:console, console) do
        app.send(:handle_key, "\r")
        max = 76 - Tui::ScreenLayout::MIN_LIST_WIDTH # body_width - MIN_LIST_WIDTH

        60.times { app.send(:handle_key, "\x0b") } # push well past the wall
        assert_equal max, panel_width(app)

        # A single opposite press must move exactly one column — no banked
        # phantom columns from pressing past the clamp.
        app.send(:handle_key, "\x0c")
        assert_equal max - 1, panel_width(app)
      end
    end
  end

  def test_successful_state_edit_that_leaves_view_exits_to_nearby_row
    app_on(view: :next, select: "Book flight") do |app|
      target_id = app.send(:current_item).id
      app.send(:handle_key, "\r")
      app.send(:handle_key, "e")
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
      app.send(:handle_key, "e")
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
  def app_on(view:, select:, content: FIXTURE_ORG, date_provider: -> { Date.today },
             host_context: nil)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), content)
      paths = Tasks::Config.for_dir(dir)
      paths.host_context = host_context
      app = Tui::App.new(root: dir, paths: paths,
                         llm_config: default_llm_config, date_provider: date_provider)
      ui(app).view = view
      app.send(:rows)
      rws = app.instance_variable_get(:@rows)
      idx = rws.index { |r| r.item&.title&.include?(select) || r.project&.title&.include?(select) }
      raise "no selectable row for #{select.inspect}" unless idx
      app.send(:select_row, idx)
      yield app
    end
  end

  def row_titles(app)
    (app.instance_variable_get(:@rows) || []).map { |r| r.item&.title }.compact
  end

  def test_defer_until_date_sets_available_from_clears_hold_and_hides_task
    app_on(view: :next, select: "Water the plants") do |app|
      app.send(:defer_selected)
      assert_equal :defer_until, ui(app).form.kind
      ui(app).form.input.replace("+4")
      app.send(:handle_key, "\r")

      refute app.send(:external_change?), "the defer form's own write is absorbed"
      store = app.instance_variable_get(:@store)
      task = store.items.find { |i| i.title.include?("Water the plants") }
      refute task.deferred?
      assert_equal Date.today + 4, task.scheduled
      refute_includes row_titles(app), "Water the plants"
      refute_equal task.id, ui(app).selected_id, "selection recovers to a visible neighbor"
      assert_match(/available/, app.instance_variable_get(:@flash))
    end
  end

  def test_defer_until_someday_adds_indefinite_hold_and_hides_task
    app_on(view: :next, select: "Water the plants") do |app|
      app.send(:defer_selected)
      ui(app).form.input.replace("someday")
      app.send(:handle_key, "\r")

      task = app.instance_variable_get(:@store).items.find { |i| i.title.include?("Water the plants") }
      assert task.deferred?
      assert_nil task.scheduled
      refute_includes row_titles(app), "Water the plants"
      assert_match(/on hold/, app.instance_variable_get(:@flash))
    end
  end

  def test_defer_until_invalid_input_stays_open_and_escape_writes_nothing
    app_on(view: :next, select: "Water the plants") do |app|
      app.send(:defer_selected)
      ui(app).form.input.replace("eventually-ish")
      app.send(:handle_key, "\r")
      assert_equal :form, ui(app).mode
      assert_match(/can't parse/, ui(app).form.error)

      app.send(:handle_key, "\e")
      task = app.instance_variable_get(:@store).items.find { |i| i.title.include?("Water the plants") }
      refute task.deferred?
      assert_nil task.scheduled
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
      { "type" => "meta", "version" => 2 },
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

  def test_defer_until_now_reactivates_indefinite_task
    app_on(view: :next, select: "Review PR", content: deferred_fixture) do |app|
      ui(app).show_deferred = true # so the deferred task is selectable
      app.send(:rows)
      idx = app.instance_variable_get(:@rows).index { |r| r.item&.title&.include?("Water the plants") }
      app.send(:select_row, idx)
      app.send(:defer_selected)
      ui(app).form.input.replace("now")
      app.send(:handle_key, "\r")
      store = app.instance_variable_get(:@store)
      refute store.items.find { |i| i.title.include?("Water the plants") }.deferred?
      assert_match(/available now/, app.instance_variable_get(:@flash))
    end
  end

  def test_defer_until_now_clears_future_available_from
    future = (Date.today + 4).iso8601
    recs = FIXTURE_RECORDS.map(&:dup)
    plants = recs.find { |record| record["id"] == FIX[:plants] }
    plants["tags"] = plants["tags"] + ["defer"]
    plants["scheduled"] = future

    app_on(view: :next, select: "Review PR", content: dump_fixture(recs)) do |app|
      app.send(:toggle_deferred_view)
      idx = app.instance_variable_get(:@rows).index { |row| row.item&.id == FIX[:plants] }
      app.send(:select_row, idx)
      app.send(:defer_selected)
      ui(app).form.input.replace("now")
      app.send(:handle_key, "\r")

      task = app.instance_variable_get(:@store).items.find { |item| item.id == FIX[:plants] }
      refute task.deferred?
      assert_nil task.scheduled
      assert task.open?
    end
  end

  def test_defer_until_now_preserves_scheduled_only_recurrence
    future = (Date.today + 4).iso8601
    recs = FIXTURE_RECORDS.map(&:dup)
    plants = recs.find { |record| record["id"] == FIX[:plants] }
    plants["scheduled"] = future
    plants["recur"] = "+1w"

    app_on(view: :next, select: "Review PR", content: dump_fixture(recs)) do |app|
      app.send(:toggle_deferred_view)
      idx = app.instance_variable_get(:@rows).index { |row| row.item&.id == FIX[:plants] }
      app.send(:select_row, idx)
      app.send(:defer_selected)
      ui(app).form.input.replace("now")
      app.send(:handle_key, "\r")

      task = app.instance_variable_get(:@store).items.find { |item| item.id == FIX[:plants] }
      assert_nil task.scheduled
      assert_equal "+1w", task.recur, "activation owns availability without stopping recurrence"
      assert_match(/available now/, app.instance_variable_get(:@flash))
    end
  end

  def test_defer_success_reports_effective_ancestor_hold
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "aaaa0001", "title" => "Work" },
      { "type" => "task", "id" => "aaaa0002", "parent" => "aaaa0001", "state" => "NEXT",
        "title" => "held parent", "tags" => %w[defer] },
      { "type" => "task", "id" => "aaaa0003", "parent" => "aaaa0002", "state" => "NEXT",
        "title" => "blocked child" },
      { "type" => "task", "id" => "aaaa0004", "parent" => "aaaa0001", "state" => "NEXT",
        "title" => "visible sibling" },
    ]

    app_on(view: :next, select: "visible sibling", content: dump_fixture(records)) do |app|
      app.send(:toggle_deferred_view)
      idx = app.instance_variable_get(:@rows).index { |row| row.item&.id == "aaaa0003" }
      app.send(:select_row, idx)
      app.send(:defer_selected)
      ui(app).form.input.replace("now")
      app.send(:handle_key, "\r")

      assert_match(/on hold via parent held parent/, app.instance_variable_get(:@flash))
      refute_match(/available now/, app.instance_variable_get(:@flash))
    end
  end

  def test_defer_success_reports_later_effective_ancestor_date
    day = Date.new(2026, 7, 14)
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "bbbb0001", "title" => "Work" },
      { "type" => "task", "id" => "bbbb0002", "parent" => "bbbb0001", "state" => "NEXT",
        "title" => "later parent", "scheduled" => "2026-07-24" },
      { "type" => "task", "id" => "bbbb0003", "parent" => "bbbb0002", "state" => "NEXT",
        "title" => "blocked child" },
      { "type" => "task", "id" => "bbbb0004", "parent" => "bbbb0001", "state" => "NEXT",
        "title" => "visible sibling" },
    ]

    app_on(view: :next, select: "visible sibling", content: dump_fixture(records),
           date_provider: -> { day }) do |app|
      app.send(:toggle_deferred_view)
      idx = app.instance_variable_get(:@rows).index { |row| row.item&.id == "bbbb0003" }
      app.send(:select_row, idx)
      app.send(:defer_selected)
      ui(app).form.input.replace("+4")
      app.send(:handle_key, "\r")

      assert_match(/unavailable until 2026-07-24 via parent later parent/,
                   app.instance_variable_get(:@flash))
      task = app.instance_variable_get(:@store).items.find { |item| item.id == "bbbb0003" }
      assert_equal Date.new(2026, 7, 18), task.scheduled
    end
  end

  def test_memoized_read_model_refreshes_when_local_date_rolls_over
    day = Date.new(2026, 7, 14)
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "cccc0001", "title" => "Work" },
      { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001", "state" => "NEXT",
        "title" => "release tomorrow", "scheduled" => "2026-07-15" },
      { "type" => "task", "id" => "cccc0003", "parent" => "cccc0001", "state" => "NEXT",
        "title" => "visible sibling" },
    ]

    app_on(view: :next, select: "visible sibling", content: dump_fixture(records),
           date_provider: -> { day }) do |app|
      before = app.send(:read_model)
      refute_includes row_titles(app), "release tomorrow"

      day = Date.new(2026, 7, 15)
      app.send(:rows)

      assert_includes row_titles(app), "release tomorrow"
      refute_same before, app.send(:read_model)
      assert_equal day, app.instance_variable_get(:@read_model_today)
    end
  end

  def test_read_model_uses_one_clock_snapshot_for_date_and_exact_times
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
      calls = 0
      clock = lambda do
        calls += 1
        Time.utc(2026, 7, 14, 20, 30)
      end
      paths = Tasks::Config.for_dir(dir)
      app = Tui::App.new(
        root: dir, paths: paths, llm_config: default_llm_config, time_provider: clock
      )
      app.send(:invalidate_read_model)
      calls = 0

      model = app.send(:read_model)

      assert_equal 1, calls
      assert_equal Time.utc(2026, 7, 14, 20, 30), model.temporal_context.now
      assert_equal Date.new(2026, 7, 14), app.instance_variable_get(:@read_model_today)
    end
  end

  def test_read_model_falls_back_safely_when_configured_zone_creates_a_floating_gap
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "aa000001", "title" => "Work" },
      { "type" => "task", "id" => "aa000002", "parent" => "aa000001", "state" => "NEXT",
        "title" => "Gap task", "deadline" => "2026-03-08",
        "deadline_time" => { "local" => "02:30" } },
    ]
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), dump_fixture(records))
      paths = Tasks::Config.for_dir(dir).dup
      paths.timezone = "America/Los_Angeles"
      app = Tui::App.new(
        root: dir, paths: paths, llm_config: default_llm_config,
        time_provider: Time.utc(2026, 3, 8, 9)
      )

      model = app.send(:read_model)

      assert_equal "Gap task", model.items.first.title
      assert_equal :temporal_context_invalid, ui(app).modal.kind
      assert_includes ui(app).modal.lines.join(" "), "first valid time is 03:00"
    end
  end

  def test_defer_response_keeps_mutation_day_snapshot_across_midnight_rollover
    day = Date.new(2026, 7, 14)
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "dddd0001", "title" => "Work" },
      { "type" => "task", "id" => "dddd0002", "parent" => "dddd0001", "state" => "NEXT",
        "title" => "releases tomorrow", "scheduled" => "2026-07-15" },
      { "type" => "task", "id" => "dddd0003", "parent" => "dddd0002", "state" => "NEXT",
        "title" => "blocked child" },
      { "type" => "task", "id" => "dddd0004", "parent" => "dddd0001", "state" => "NEXT",
        "title" => "visible sibling" },
    ]

    app_on(view: :next, select: "visible sibling", content: dump_fixture(records),
           date_provider: -> { day }) do |app|
      app.send(:toggle_deferred_view)
      idx = app.instance_variable_get(:@rows).index { |row| row.item&.id == "dddd0003" }
      app.send(:select_row, idx)
      app.send(:defer_selected)

      application = app.instance_variable_get(:@application)
      rollover_application = Object.new
      rollover_application.define_singleton_method(:edit_snapshot) do |id|
        application.edit_snapshot(id)
      end
      rollover_application.define_singleton_method(:update_task) do |*args, **options|
        result = application.update_task(*args, **options)
        day = Date.new(2026, 7, 15)
        result
      end
      rollover_application.define_singleton_method(:read_tasks) do |**options|
        application.read_tasks(**options)
      end
      app.instance_variable_set(:@application, rollover_application)
      ui(app).show_deferred = false

      ui(app).form.input.replace("now")
      app.send(:handle_key, "\r")

      assert_match(/unavailable until 2026-07-15 via parent releases tomorrow/,
                   app.instance_variable_get(:@flash))
      refute_includes row_titles(app), "blocked child",
                      "response visibility stays on the Jul 14 mutation snapshot"
      assert_equal Date.new(2026, 7, 14), app.instance_variable_get(:@read_model_today)

      app.send(:rows)
      assert_includes row_titles(app), "blocked child",
                      "the next ordinary render advances to the provider's Jul 15"
    end
  end

  def test_header_counts_only_effectively_available_open_tasks_and_labels_reveal
    future = (Date.today + 4).iso8601
    recs = FIXTURE_RECORDS.map(&:dup)
    recs.find { |record| record["id"] == FIX[:plants] }["scheduled"] = future

    app_on(view: :next, select: "Review PR", content: dump_fixture(recs)) do |app|
      available_open = app.send(:read_model).tasks.count { |task| task.open? && task.available? }
      header = Tui::Ansi.strip(app.send(:header, 180))
      assert_includes header, "#{available_open} open"
      refute_includes header, "unavailable shown"

      app.send(:toggle_deferred_view)
      assert_includes Tui::Ansi.strip(app.send(:header, 180)), "unavailable shown"
    end
  end

  def test_timed_deferral_atomically_replaces_own_hold
    app_on(view: :next, select: "Review PR", content: deferred_fixture) do |app|
      ui(app).show_deferred = true
      app.send(:rows)
      idx = app.instance_variable_get(:@rows).index { |r| r.item&.title&.include?("Water the plants") }
      app.send(:select_row, idx)
      app.send(:defer_selected)
      ui(app).form.input.replace("+4")
      app.send(:handle_key, "\r")

      task = app.instance_variable_get(:@store).items.find { |i| i.title.include?("Water the plants") }
      refute task.deferred?
      assert_equal Date.today + 4, task.scheduled
    end
  end

  # -- recurrence ------------------------------------------------------------

  RECUR_FIXTURE = dump_fixture([
    { "type" => "meta", "version" => 2 },
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

      rendered = ui(app).form.popup(row: 0, col: 0, inline_input: ->(input) { input.to_s },
                                    max_width: 120, max_height: 4)[:lines]
                  .map { |line| Tui::Ansi.strip(line) }.join("\n")
      assert_includes rendered, "(now every month from the scheduled date)",
                      "the suffix glosses the prefilled cookie rather than repeating it"
    end
  end

  def test_open_date_popup_uses_atomic_temporal_input_without_changing_date_only_submit
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_date_popup)
      assert_instance_of Tui::TaskEditForm::TemporalInput, ui(app).form.field

      ui(app).form.input.replace("2026-08-14")
      app.send(:handle_key, "\r")

      assert_equal :list, ui(app).mode
      assert_equal Date.new(2026, 8, 14), app.send(:current_item).deadline
    end
  end


  def test_quick_date_popup_accepts_a_fixed_timed_value
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_date_popup)
      ui(app).form.input.replace("2026-11-01 1:30am America/New_York fold=later")
      app.send(:handle_key, "\r")

      value = app.send(:current_item).deadline_value
      assert_equal "01:30", value.local_time
      assert_equal "America/New_York", value.timezone
      assert_equal 1, value.fold
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
      assert_match(/Available from date or deadline/, app.instance_variable_get(:@flash))
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
      assert_match(/unrecognized schedule/, ui(app).form.error,
                   "the engine's own reason, not a generic parse failure")
    end
  end

  def test_submit_recur_accepts_a_calendar_phrase_and_stores_the_canonical_form
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("every mon wed")
      app.send(:handle_key, "\r")

      store = app.instance_variable_get(:@store)
      assert_equal "w:mon,wed", store.items.find { |i| i.title.include?("Pay rent") }.recur
      assert_equal :list, ui(app).mode
      assert_match(/↻ every Mon, Wed: Pay rent/, app.instance_variable_get(:@flash))
    end
  end

  def test_submit_recur_passes_a_canonical_calendar_cookie_through_unchanged
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("m:15")
      app.send(:handle_key, "\r")

      assert_equal "m:15", app.instance_variable_get(:@store).items
        .find { |i| i.title.include?("Pay rent") }.recur
    end
  end

  # A schedule can parse cleanly and still be unwritable; the store refuses it
  # with a sentence naming why, and the popup must show that sentence rather
  # than the "file changed underneath" wording reserved for real staleness.
  def test_submit_recur_surfaces_the_stores_refusal_reason
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("every 9999 years")
      app.send(:handle_key, "\r")

      assert_equal :form, ui(app).mode, "stays open on a refused schedule"
      assert_match(/outside the four-digit years/, ui(app).form.error)
      assert_equal "+1m", app.instance_variable_get(:@store).items
        .find { |i| i.title.include?("Pay rent") }.recur, "the stored schedule is untouched"
    end
  end

  RECUR_PREVIEW_TODAY = -> { Date.new(2026, 7, 28) }

  def test_recur_popup_previews_the_typed_schedule_in_its_footer
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE,
           date_provider: RECUR_PREVIEW_TODAY) do |app|
      app.send(:open_recur_popup)
      form = ui(app).form

      assert_equal "every month from the scheduled date → 2026-09-01 Tue · 2026-10-01 Thu · 2026-11-01 Sun",
                   form.hint, "the prefilled cookie previews on open"

      form.input.replace("every mon wed")
      assert_equal "every Mon, Wed → 2026-08-03 Mon · 2026-08-05 Wed · 2026-08-10 Mon", form.hint

      form.input.replace("off")
      assert_equal "no recurrence", form.hint

      form.input.replace("bananas")
      assert_equal "unrecognized schedule: \"bananas\"", form.hint

      form.input.replace("")
      assert_equal Tui::App::RECUR_POPUP_HINT, form.hint, "an empty input teaches the grammar"
    end
  end

  # The rendered footer line, with the renderer's "· " cue stripped.
  def popup_footer(form, width)
    lines = form.popup(row: 0, col: 0, inline_input: ->(input) { input.to_s },
                       max_width: width, max_height: 4)[:lines]
    line = lines.map { |l| Tui::Ansi.strip(l) }.find { |l| l.include?("· ") }
    line.to_s.sub(/\A[^·]*·\s/, "").sub(/\s*│?\s*\z/, "")
  end

  # The preview must never clip a date: "2026-08-0…" reads as a different day.
  # It sheds whole dates as the popup narrows, down to none.
  def test_recur_popup_preview_fits_whole_dates_to_the_popup_width
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE,
           date_provider: RECUR_PREVIEW_TODAY) do |app|
      app.send(:open_recur_popup)
      form = ui(app).form
      form.input.replace("every mon wed")

      wide = form.popup(row: 0, col: 0, inline_input: ->(input) { input.to_s },
                        max_width: 120, max_height: 4)[:lines]
                 .map { |line| Tui::Ansi.strip(line) }.join("\n")
      assert_includes wide, "repeat: every mon wed", "the raw input stays in the field"
      assert_includes wide, "every Mon, Wed → 2026-08-03 Mon · 2026-08-05 Wed · 2026-08-10 Mon"

      [Tui::App::RECUR_POPUP_WIDTH, 60, 52, 40, 30].each do |width|
        footer = popup_footer(form, width)
        refute_includes footer, "…", "footer clipped at #{width}: #{footer.inspect}"
        footer.scan(/\d{4}-\d\d-\d\d.{0,4}/).each do |date|
          assert_match(/\A\d{4}-\d\d-\d\d [A-Z][a-z]{2}\z/, date.strip,
                       "partial date at width #{width}: #{footer.inspect}")
        end
      end

      # A long gloss sheds every date before it would clip one.
      form.input.replace("+1m")
      assert_equal "every month from the scheduled date", popup_footer(form, 52)
    end
  end

  RECUR_CATCHUP_FIXTURE = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "dddd0001", "title" => "Work" },
    { "type" => "task", "id" => "dddd0002", "parent" => "dddd0001", "state" => "NEXT",
      "title" => "Weekly review", "deadline" => "2026-07-21", "recur" => "++1w" },
    { "type" => "task", "id" => "dddd0003", "parent" => "dddd0001", "state" => "NEXT",
      "title" => "Daily log", "deadline" => "2026-07-27", "recur" => "++1d" },
  ])

  # A preview that disagrees with the write is worse than no preview. For an
  # all-day stamp a catch-up series lands *on* the completion day, and the
  # projection has to say so.
  def test_catch_up_preview_names_the_date_completion_writes
    [["Weekly review", "++1w"], ["Daily log", "++1d"]].each do |title, cookie|
      app_on(view: :agenda, select: title, content: RECUR_CATCHUP_FIXTURE,
             date_provider: RECUR_PREVIEW_TODAY) do |app|
        preview = app.send(:recur_preview, cookie, anchor: app.send(:current_item).deadline)
        app.send(:complete_selected)
        rolled = app.instance_variable_get(:@store).items.find { |i| i.title.include?(title) }

        assert_equal Date.new(2026, 7, 28), rolled.deadline, "#{cookie} catches up to today"
        assert_includes preview, "→ #{rolled.deadline.iso8601}",
                        "#{cookie}: the first previewed date is the one done writes"
      end
    end
  end

  # edit_snapshot returns nil both for a vanished task and for a file that fails
  # its preflight check. Only the first is "task no longer exists".
  def test_recur_popup_reports_an_unreadable_file_as_a_reopen_not_a_missing_task
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:open_recur_popup)
      ui(app).form.input.replace("m:15")
      path = app.instance_variable_get(:@paths).org
      File.write(path, "#{File.read(path)}this is not json\n")

      app.send(:handle_key, "\r")

      assert_equal :form, ui(app).mode, "stays open so the edit can be retried"
      assert_equal "file changed underneath — reopen", ui(app).form.error
      assert_includes File.read(path), "Pay rent", "the record is still on disk"
    end
  end

  # The third explain shape: understood, but no occurrence exists from this
  # anchor. Canonical form, gloss, and the engine's reason all stay visible.
  def test_recur_preview_reports_a_schedule_that_never_fires
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE,
           date_provider: RECUR_PREVIEW_TODAY) do |app|
      preview = app.send(:recur_preview, "2y:02:5fri", anchor: Date.new(2027, 8, 1))

      assert_match(/\A2y:02:5fri — every 2 years on the 5th Friday of February — /, preview)
      assert_match(/may never fire for this anchor/, preview)
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

  def test_complete_selected_uses_the_injected_operation_date_for_completion_recurrence
    content = dump_fixture([
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "dc000001", "title" => "Work" },
      { "type" => "task", "id" => "dc000002", "parent" => "dc000001", "state" => "NEXT",
        "title" => "Injected cadence", "scheduled" => "2026-07-10", "recur" => ".+1w" },
    ])
    app_on(view: :agenda, select: "Injected cadence", content: content,
           date_provider: -> { Date.new(2030, 1, 1) }) do |app|
      app.send(:complete_selected)
      record = app.instance_variable_get(:@store).read_snapshot.live_records
                  .find { |candidate| candidate["id"] == "dc000002" }
      assert_equal "2030-01-08", record["scheduled"]
      assert_match(/- Did \[2030-01-01\]/, record.fetch("body"))
      assert_match(/2030-01-08/, app.instance_variable_get(:@flash))
    end
  end

  def test_quick_tui_mutations_use_the_stable_patch_adapter
    source = File.read(File.expand_path("../lib/tui/app.rb", __dir__), encoding: "UTF-8")
    legacy = /@store\.(?:complete!|set_priority!|reschedule!|set_date!|set_state!|undate!|retitle!|set_tags!|set_deferred!|set_recur!|add_note!|move!|move_under!|move_top!)/

    refute_match legacy, source
    assert_match(/def patch_task\(item, field:, value:, label:, today: current_date\)/, source)
    refute_match(/@store\.(?:edit_snapshot|patch_task!)/, source)
    assert_match(/@application\.edit_snapshot\(item\.id\)/, source)
    assert_match(/@application\.patch_task/, source)
  end

  def test_application_routed_quick_patch_is_not_mistaken_for_an_external_write
    app_on(view: :agenda, select: "Pay rent", content: RECUR_FIXTURE) do |app|
      app.send(:complete_selected)
      refute app.send(:external_change?)
    end
  end

  def test_tui_presentation_reads_use_the_application_model_not_the_mutation_store
    source = File.read(File.expand_path("../lib/tui/app.rb", __dir__), encoding: "UTF-8")
    refute_match(/@store\.(?:items|tree|body|links|node_for)/, source)
    assert_match(/Tasks::Application\.new/, source)

    app_with(input: "") do |app|
      app.send(:rows)
      mutation_store = Object.new
      %i[items tree body links node_for].each do |method|
        mutation_store.define_singleton_method(method) { raise "presentation read leaked to mutation Store: #{method}" }
      end
      app.instance_variable_set(:@store, mutation_store)

      rows = app.send(:rows)
      assert_includes rows.filter_map { |row| row.item&.id }, FIX[:flight]
      app.send(:show_detail)
      assert_equal :detail, ui(app).panel.kind
      assert_match(/Book flight in Concur/, ui(app).panel.lines.join("\n"))
      refute app.send(:link_action_available?)
    end
  end

  # -- delegation (D / W) ------------------------------------------------------

  DELEGATION_FIXTURE = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "bbbb0001", "title" => "Projects" },
    { "type" => "section", "id" => "bbbb0002", "parent" => "bbbb0001", "title" => "Research" },
    { "type" => "task", "id" => "bbbb0003", "parent" => "bbbb0002", "state" => "NEXT",
      "title" => "Compare CRDT libraries" },
    { "type" => "task", "id" => "bbbb0004", "parent" => "bbbb0002", "state" => "PROPOSED",
      "title" => "Suggested spike" },
    { "type" => "task", "id" => "bbbb0005", "parent" => "bbbb0002", "state" => "DONE",
      "title" => "Closed groundwork", "closed" => "2026-06-01" },
  ])

  def delegation_app(select: "Compare CRDT libraries", view: :outline, &block)
    app_on(view: view, select: select, content: DELEGATION_FIXTURE, &block)
  end

  # Drive the inline prompt exactly as a keypress would: open it, type, submit.
  def submit_form(app, key, text)
    app.send(:handle_key, key)
    ui(app).form.input.replace(text)
    app.send(:handle_key, "\r")
  end

  def delegation_of(app, id = "bbbb0003")
    Tasks::Format.parse(File.read(app.instance_variable_get(:@paths).org))
                 .records.find { |record| record["id"] == id }&.fetch("delegation", nil)
  end

  def test_delegate_key_opens_the_inline_prompt_with_the_current_state
    delegation_app do |app|
      app.send(:handle_key, "D")
      assert_equal :form, ui(app).mode
      assert_equal :delegate, ui(app).form.kind
      assert_equal "bbbb0003", ui(app).form.target_id
      assert_equal :list, ui(app).form.return_mode
      assert_equal "(not delegated)", ui(app).form.instance_variable_get(:@suffix)
      assert_equal "", ui(app).form.input.to_s
      hint = ui(app).form.instance_variable_get(:@hint)
      assert_equal "pat@example.com · refine · research · implement · release · off · esc cancels", hint
    end
  end

  def test_delegate_prompt_suffix_reports_the_live_delegation
    delegation_app do |app|
      submit_form(app, "D", "research")
      app.send(:handle_key, "D")
      assert_equal "(now agent-ready (research))", ui(app).form.instance_variable_get(:@suffix)
    end
  end

  def test_delegate_email_hands_the_task_to_a_person_and_reports_waiting
    delegation_app do |app|
      submit_form(app, "D", "pat@example.com")

      assert_equal :list, ui(app).mode
      delegation = delegation_of(app)
      assert_equal %w[human delegated pat@example.com], delegation.values_at("kind", "status", "assignee")
      assert_equal "WAITING", app.send(:read_model).task_for("bbbb0003").state
      assert_equal "delegated → pat@example.com (WAITING): Compare CRDT libraries",
                   app.instance_variable_get(:@flash)
      refute app.send(:external_change?), "the delegation's own write is absorbed"
    end
  end

  def test_delegate_each_agent_mode_and_its_unambiguous_prefix
    { "refine" => "refine", "ref" => "refine", "research" => "research", "res" => "research",
      "implement" => "implement", "imp" => "implement" }.each do |typed, mode|
      delegation_app do |app|
        submit_form(app, "D", typed)

        delegation = delegation_of(app)
        assert_equal ["agent", mode, "ready"], delegation.values_at("kind", "mode", "status"),
                     "#{typed.inspect} must delegate at #{mode}"
        refute delegation.key?("assignee"), "agent-ready work carries no assignee"
        assert_equal "agent-ready (#{mode}): Compare CRDT libraries",
                     app.instance_variable_get(:@flash)
        assert_equal "NEXT", app.send(:read_model).task_for("bbbb0003").state,
                     "agent delegation never moves lifecycle state"
      end
    end
  end

  def test_delegate_off_and_none_clear_the_marker
    %w[off none].each do |word|
      delegation_app do |app|
        submit_form(app, "D", "research")
        submit_form(app, "D", word)

        assert_nil delegation_of(app)
        assert_equal "undelegated: Compare CRDT libraries", app.instance_variable_get(:@flash)
      end
    end
  end

  def test_delegate_off_on_an_undelegated_task_writes_nothing
    delegation_app do |app|
      submit_form(app, "D", "off")
      assert_nil delegation_of(app)
      assert_equal "not delegated: Compare CRDT libraries", app.instance_variable_get(:@flash)
    end
  end

  def test_delegate_release_forces_a_stale_claim_back_to_the_ready_queue
    delegation_app do |app|
      submit_form(app, "D", "research")
      # A worker picks it up out-of-band; the owner never holds that worker id.
      assert app.instance_variable_get(:@store)
                .claim_task!("bbbb0003", worker: "claude-code/opus/9a11").ok?
      app.send(:reload_store)

      submit_form(app, "D", "release")

      delegation = delegation_of(app)
      assert_equal ["agent", "research", "ready"], delegation.values_at("kind", "mode", "status")
      refute delegation.key?("assignee")
      assert_equal "released · agent-ready (research): Compare CRDT libraries",
                   app.instance_variable_get(:@flash)
    end
  end

  def test_delegating_over_a_live_claim_reports_the_holder_and_keeps_the_prompt_open
    delegation_app do |app|
      submit_form(app, "D", "research")
      assert app.instance_variable_get(:@store)
                .claim_task!("bbbb0003", worker: "claude-code/opus/9a11").ok?
      app.send(:reload_store)

      submit_form(app, "D", "refine")

      assert_equal :form, ui(app).mode, "a refused delegation stays open for another answer"
      assert_match(/already claimed by claude-code\/opus\/9a11 at /, ui(app).form.error)
      assert_match(/off revokes it/, ui(app).form.error)
      assert_equal "claimed", delegation_of(app)["status"]
    end
  end

  def test_delegate_release_on_an_unclaimed_task_reports_the_precondition
    delegation_app do |app|
      submit_form(app, "D", "research")
      submit_form(app, "D", "release")

      assert_equal :form, ui(app).mode
      assert_equal "task is not claimed", ui(app).form.error
    end
  end

  def test_delegate_rejects_ambiguous_and_unparseable_input_without_writing
    delegation_app do |app|
      app.send(:handle_key, "D")

      ui(app).form.input.replace("re")
      app.send(:handle_key, "\r")
      assert_equal :form, ui(app).mode
      assert_equal "can't parse “re”; use an email, refine/research/implement, release, or off",
                   ui(app).form.error

      ui(app).form.input.replace("bananas")
      app.send(:handle_key, "\r")
      assert_equal "can't parse “bananas”; use an email, refine/research/implement, release, or off",
                   ui(app).form.error

      ui(app).form.input.replace("")
      app.send(:handle_key, "\r")
      assert_match(/can't parse/, ui(app).form.error)

      app.send(:handle_key, "\e")
      assert_equal :list, ui(app).mode
      assert_nil delegation_of(app)
    end
  end

  # `@` is the context-filter key, so a typo'd `@word` is one slip of muscle
  # memory away from the delegate prompt. It must be refused here, by name, and
  # never routed down the human branch to come back as a Store refusal about a
  # field the user did not know they were writing.
  def test_delegate_refuses_an_at_word_that_is_not_an_address_before_the_store
    ["@work", "@home", "pat @example.com", "pat@", "@", "pat@example", "a@b@c.com"].each do |typed|
      delegation_app do |app|
        submit_form(app, "D", typed)

        assert_equal :form, ui(app).mode, "#{typed.inspect} keeps the prompt open"
        assert_equal "“#{typed}” isn't an email address — use pat@example.com",
                     ui(app).form.error
        assert_nil delegation_of(app), "#{typed.inspect} must not write a delegation"
        assert_equal "NEXT", app.send(:read_model).task_for("bbbb0003").state,
                     "#{typed.inspect} must not flip the task to WAITING"
      end
    end
  end

  def test_delegate_accepts_ordinary_addresses_including_subdomains_and_plus_tags
    ["pat@example.com", "pat+tasks@mail.example.co.uk", "PAT@Example.com"].each do |typed|
      delegation_app do |app|
        submit_form(app, "D", typed)

        assert_equal typed, delegation_of(app)&.fetch("assignee"),
                     "#{typed.inspect} is a usable identifier"
      end
    end
  end

  # A one-character answer must never resolve to the widest authority
  # (`implement`) or to the one destructive verb (`off`/`none` revokes a live
  # claim with no confirmation). Only the spellings the plan promises resolve.
  def test_delegate_requires_three_characters_before_a_prefix_resolves
    assert_equal 3, Tui::App::DELEGATE_PREFIX_MIN
    # `i` was `implement`, `o`/`n` were `off`/`none`, and `r`/`re` guessed
    # across three words. None of them are spellings the plan promises.
    %w[o n i r re of no im].each do |typed|
      delegation_app do |app|
        submit_form(app, "D", typed)

        assert_equal :form, ui(app).mode, "#{typed.inspect} must not act"
        assert_equal "can't parse “#{typed}”; use an email, refine/research/implement, release, or off",
                     ui(app).form.error
        assert_nil delegation_of(app), "#{typed.inspect} must not write a delegation"
      end
    end
  end

  def test_short_input_cannot_revoke_a_live_claim
    delegation_app do |app|
      submit_form(app, "D", "research")
      assert app.instance_variable_get(:@store)
                .claim_task!("bbbb0003", worker: "claude-code/claude-fable-5/aaaa1111").ok?
      app.send(:reload_store)

      %w[o n].each do |typed|
        submit_form(app, "D", typed)
        assert_equal :form, ui(app).mode
        assert_match(/can't parse/, ui(app).form.error)
        assert_equal "claimed", delegation_of(app)["status"], "#{typed.inspect} must not revoke"
        app.send(:handle_key, "\e")
      end
    end
  end

  # Every promised spelling still resolves, including the clear words the CLI
  # owns — the TUI shares that vocabulary rather than keeping its own copy.
  def test_delegate_accepts_every_promised_spelling
    assert_same Tasks::DelegationCommand::CLEAR_WORDS, Tui::App::DELEGATE_CLEAR_WORDS,
                "the clear vocabulary has exactly one definition"

    { "ref" => :refine, "refine" => :refine, "res" => :research, "research" => :research,
      "imp" => :implement, "implement" => :implement }.each do |typed, mode|
      delegation_app do |app|
        submit_form(app, "D", typed)
        assert_equal mode.to_s, delegation_of(app)["mode"]
      end
    end

    (Tasks::DelegationCommand::CLEAR_WORDS + %w[non]).each do |typed|
      delegation_app do |app|
        submit_form(app, "D", "research")
        submit_form(app, "D", typed)
        assert_nil delegation_of(app), "#{typed.inspect} undelegates"
      end
    end

    %w[rel rele release].each do |typed|
      delegation_app do |app|
        submit_form(app, "D", "research")
        assert app.instance_variable_get(:@store)
                  .claim_task!("bbbb0003", worker: "claude-code/claude-fable-5/aaaa1111").ok?
        app.send(:reload_store)

        submit_form(app, "D", typed)
        assert_equal "ready", delegation_of(app)["status"], "#{typed.inspect} forces a release"
      end
    end
  end

  # The clear words drive `W` too, from the same shared constant.
  def test_work_ref_clear_words_come_from_the_shared_vocabulary
    Tasks::DelegationCommand::CLEAR_WORDS.each do |typed|
      delegation_app do |app|
        submit_form(app, "D", "research")
        submit_form(app, "W", "https://example.com/brief")
        submit_form(app, "W", typed.upcase)

        refute delegation_of(app).key?("work_ref"), "#{typed.inspect} clears the reference"
      end
    end
  end

  # The failure path reloads the store, and the reload detaches the prompt when
  # its target row is gone. A detached Form's inline error is never painted, so
  # the refusal has to arrive as a flash instead of the prompt simply vanishing.
  def test_delegate_flashes_when_the_target_disappears_mid_prompt
    delegation_app do |app|
      app.send(:handle_key, "D")
      assert_equal :form, ui(app).mode

      paths = app.instance_variable_get(:@paths)
      kept = File.readlines(paths.org).reject { |line| line.include?("bbbb0003") }
      File.write(paths.org, kept.join)

      ui(app).form.input.replace("research")
      app.send(:handle_key, "\r")

      assert_nil ui(app).form, "the prompt closes with its target"
      assert_equal :list, ui(app).mode, "no :form mode without a form"
      assert_equal "task no longer exists", app.instance_variable_get(:@flash)
    end
  end

  def test_delegation_keys_are_unavailable_on_projects_proposals_and_closed_tasks
    delegation_app(select: "Research", view: :projects) do |app|
      refute app.send(:delegation_action_available?)
      app.send(:handle_key, "D")
      assert_equal :list, ui(app).mode
      assert_equal "select a task for that", app.instance_variable_get(:@flash)
    end

    delegation_app(select: "Suggested spike", view: :inbox) do |app|
      refute app.send(:delegation_action_available?)
      app.send(:handle_key, "W")
      assert_equal :list, ui(app).mode
      assert_match(/proposal can't be delegated/, app.instance_variable_get(:@flash))
    end

    delegation_app(select: "Closed groundwork") do |app|
      refute app.send(:delegation_action_available?)
      app.send(:handle_key, "D")
      assert_equal :list, ui(app).mode
      assert_equal "done tasks can't be delegated", app.instance_variable_get(:@flash)
    end

    delegation_app do |app|
      assert app.send(:delegation_action_available?)
    end
  end

  def test_delegation_actions_are_palette_entries_only_for_an_eligible_task
    delegation_app do |app|
      handlers = Tui::Shortcuts.palette_entries(:list, app).map(&:handler)
      assert_includes handlers, :delegate_selected
      assert_includes handlers, :set_work_ref_selected
      descriptions = Tui::Shortcuts.palette_entries(:list, app).map(&:description)
      assert descriptions.any? { |text| text.start_with?("Delegate…") }
      assert descriptions.any? { |text| text.start_with?("Set work reference…") }
    end

    delegation_app(select: "Closed groundwork") do |app|
      handlers = Tui::Shortcuts.palette_entries(:list, app).map(&:handler)
      refute_includes handlers, :delegate_selected
      refute_includes handlers, :set_work_ref_selected
    end
  end

  def test_work_ref_prompt_prefills_the_current_reference_and_records_a_new_one
    delegation_app do |app|
      submit_form(app, "D", "research")

      app.send(:handle_key, "W")
      assert_equal :work_ref, ui(app).form.kind
      assert_equal "", ui(app).form.input.to_s
      ui(app).form.input.replace("https://example.com/brief")
      app.send(:handle_key, "\r")

      assert_equal "https://example.com/brief", delegation_of(app)["work_ref"]
      assert_equal "work ref → https://example.com/brief: Compare CRDT libraries",
                   app.instance_variable_get(:@flash)
      refute app.send(:external_change?)

      app.send(:handle_key, "W")
      assert_equal "https://example.com/brief", ui(app).form.input.to_s
      app.send(:handle_key, "\e")
    end
  end

  def test_work_ref_off_clears_the_reference_and_blank_input_refuses
    delegation_app do |app|
      submit_form(app, "D", "research")
      submit_form(app, "W", "https://example.com/brief")

      app.send(:handle_key, "W")
      ui(app).form.input.replace("")
      app.send(:handle_key, "\r")
      assert_equal :form, ui(app).mode, "a blank field must not silently wipe the reference"
      assert_match(/off to clear it/, ui(app).form.error)

      ui(app).form.input.replace("off")
      app.send(:handle_key, "\r")
      refute delegation_of(app).key?("work_ref")
      assert_equal "work ref cleared: Compare CRDT libraries", app.instance_variable_get(:@flash)
    end
  end

  def test_work_ref_refuses_an_undelegated_task_before_opening_a_prompt
    delegation_app do |app|
      app.send(:handle_key, "W")
      assert_equal :list, ui(app).mode
      assert_nil ui(app).form
      assert_match(/delegate the task first/, app.instance_variable_get(:@flash))
    end
  end

  def test_delegation_refreshes_the_open_detail_panel_and_the_row_marker
    delegation_app do |app|
      app.send(:open_detail)
      assert_equal :detail, ui(app).panel.kind

      submit_form(app, "D", "research")

      panel = ui(app).panel.lines.map { |line| Tui::Ansi.strip(line) }
      assert_includes panel, "delegation"
      assert panel.any? { |line| line =~ /mode\s+research/ }
      row = app.instance_variable_get(:@rows).find { |r| r.item&.id == "bbbb0003" }
      assert_includes Tui::Ansi.strip(row.text), "→research"
      assert_equal "bbbb0003", ui(app).selected_id
    end
  end

  def test_delegation_written_externally_shows_up_after_a_reload
    delegation_app do |app|
      assert app.instance_variable_get(:@store)
                .delegate_task!("bbbb0003", kind: "agent", mode: "refine").ok?
      app.send(:reload_store)

      row = app.instance_variable_get(:@rows).find { |r| r.item&.id == "bbbb0003" }
      assert_includes Tui::Ansi.strip(row.text), "→refine"
      app.send(:handle_key, "D")
      assert_equal "(now agent-ready (refine))", ui(app).form.instance_variable_get(:@suffix)
    end
  end

  # -- stable selection identity ---------------------------------------------

  SELECTION_FIXTURE = dump_fixture([
    { "type" => "meta", "version" => 2 },
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
    app.send(:handle_key, "e")
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
      before = app.instance_variable_get(:@read_model)
      rewrite_records(app) do |records|
        records.find { |record| record["id"] == "5e1e0004" }["deadline"] = "2026-07-10"
      end

      assert_equal "Beta", app.send(:current_item).title
      assert_equal "5e1e0003", ui(app).selected_id
      refute_equal old_row, app.instance_variable_get(:@sel), "render coordinate follows the resort"
      refute_same before, app.instance_variable_get(:@read_model), "external writes replace the immutable application read"
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

  # -- structural Outline ordering ------------------------------------------

  ORDERING_APP = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "0d000001", "title" => "Work" },
    { "type" => "task", "id" => "0d000002", "parent" => "0d000001", "state" => "NEXT",
      "title" => "Alpha" },
    { "type" => "task", "id" => "0d000003", "parent" => "0d000002", "state" => "DONE",
      "title" => "Alpha child", "closed" => "2026-07-10" },
    { "type" => "task", "id" => "0d000004", "parent" => "0d000001", "state" => "TODO",
      "title" => "Beta" },
    { "type" => "task", "id" => "0d000005", "parent" => "0d000004", "state" => "TODO",
      "title" => "Beta child" },
    { "type" => "task", "id" => "0d000006", "parent" => "0d000001", "state" => "WAITING",
      "title" => "Gamma", "tags" => ["defer"] },
    { "type" => "task", "id" => "0d000007", "parent" => "0d000001", "state" => "CANCELLED",
      "title" => "Delta", "closed" => "2026-07-11" },
  ])

  def ordering_ids(app)
    app.send(:read_model).tasks.map(&:id)
  end

  def ordering_semantics(text)
    Tasks::Format.parse(text).records.map do |record|
      record.reject { |key, _| key == "line" || key == "updated" }
    end
  end

  def test_outline_ordering_alt_up_encodings_move_a_collapsed_subtree_and_keep_selection
    ["\e[1;3A", "\e\e[A", "\ek"].each do |sequence|
      app_on(view: :outline, select: "Beta", content: ORDERING_APP) do |app|
        ui(app).collapsed.add("0d000004")
        app.send(:rows)
        app.send(:handle_key, sequence)

        refute app.send(:external_change?), "ordering's own write is absorbed"
        assert_equal %w[0d000004 0d000005 0d000002 0d000003 0d000006 0d000007],
                     ordering_ids(app), sequence.inspect
        assert_equal "0d000004", app.send(:current_item).id
        assert_includes ui(app).collapsed, "0d000004"
        assert_match(/move up: Beta/, app.instance_variable_get(:@flash))
      end
    end
  end

  def test_ordering_refresh_keeps_a_concurrent_write_visible_after_absorbing_its_own_write
    app_on(view: :outline, select: "Beta", content: ORDERING_APP) do |app|
      store = app.instance_variable_get(:@store)
      external = Tasks::Store.new(org: store.org, archive: app.instance_variable_get(:@paths).archive)
      original_absorb = app.method(:absorb_own_write)
      injected_absorb = lambda do |_context|
        result = external.create_task!(
          Tasks::CreateTask.new(title: "Concurrent task", parent_id: "0d000001")
        )
        assert result.ok?
        original_absorb.call
      end

      app.stub(:absorb_own_write, injected_absorb) { app.send(:move_subtree_up) }

      assert_includes app.send(:read_model).items.map(&:title), "Concurrent task"
      refute app.send(:external_change?)
    end
  end

  def test_outline_move_down_uses_the_sibling_after_next_or_appends
    app_on(view: :outline, select: "Beta", content: ORDERING_APP) do |app|
      app.send(:move_subtree_down)
      assert_equal %w[0d000002 0d000003 0d000006 0d000004 0d000005 0d000007], ordering_ids(app)
    end

    app_on(view: :outline, select: "Gamma", content: ORDERING_APP) do |app|
      app.send(:move_subtree_down)
      assert_equal %w[0d000002 0d000003 0d000004 0d000005 0d000007 0d000006], ordering_ids(app)
    end
  end

  def test_outline_first_down_and_last_up_use_exact_neighbor_slots
    app_on(view: :outline, select: "Alpha", content: ORDERING_APP) do |app|
      app.send(:move_subtree_down)
      assert_equal %w[0d000004 0d000005 0d000002 0d000003 0d000006 0d000007], ordering_ids(app)
    end

    app_on(view: :outline, select: "Delta", content: ORDERING_APP) do |app|
      app.send(:move_subtree_up)
      assert_equal %w[0d000002 0d000003 0d000004 0d000005 0d000007 0d000006], ordering_ids(app)
    end
  end

  def test_outline_indent_appends_under_previous_sibling_and_outdent_restores_after_parent
    app_on(view: :outline, select: "Beta", content: ORDERING_APP) do |app|
      original = File.binread(app.instance_variable_get(:@store).org)
      ui(app).collapsed.add("0d000002")
      app.send(:indent_subtree)

      beta = app.send(:read_model).task_for("0d000004")
      assert_equal "0d000002", beta.parent_id
      assert_equal %w[0d000003 0d000004],
                   app.send(:read_model).tasks.select { |task| task.parent_id == "0d000002" }.map(&:id)
      assert_equal "0d000004", app.send(:current_item).id
      refute_includes ui(app).collapsed, "0d000002", "indent expands the new parent to retain selection"

      app.send(:outdent_subtree)
      assert_equal ordering_semantics(original),
                   ordering_semantics(File.binread(app.instance_variable_get(:@store).org))
      assert_equal "0d000004", app.send(:current_item).id
    end
  end

  def test_outline_last_indent_and_outdent_from_middle_or_last_parent_use_exact_slots
    app_on(view: :outline, select: "Delta", content: ORDERING_APP) do |app|
      original = File.binread(app.instance_variable_get(:@store).org)
      app.send(:indent_subtree)
      assert_equal "0d000006", app.send(:read_model).task_for("0d000007").parent_id
      assert_equal %w[0d000002 0d000003 0d000004 0d000005 0d000006 0d000007], ordering_ids(app)

      app.send(:outdent_subtree)
      assert_equal ordering_semantics(original),
                   ordering_semantics(File.binread(app.instance_variable_get(:@store).org)),
                   "outdent from the last parent appends immediately after it"
    end

    app_on(view: :outline, select: "Beta child", content: ORDERING_APP) do |app|
      app.send(:outdent_subtree)
      assert_equal "0d000001", app.send(:read_model).task_for("0d000005").parent_id
      assert_equal %w[0d000002 0d000003 0d000004 0d000005 0d000006 0d000007], ordering_ids(app)
      top_level = app.send(:read_model).tasks.select { |task| task.parent_id == "0d000001" }.map(&:id)
      assert_equal %w[0d000002 0d000004 0d000005 0d000006 0d000007], top_level,
                   "outdent from a middle parent anchors before its next sibling"
    end
  end

  def test_outline_ordering_boundaries_do_not_write_or_create_history
    cases = [
      ["Alpha", :move_subtree_up, /already first/],
      ["Delta", :move_subtree_down, /already last/],
      ["Alpha", :indent_subtree, /preceding sibling/],
      ["Alpha", :outdent_subtree, /section level/],
    ]
    cases.each do |title, action, message|
      app_on(view: :outline, select: title, content: ORDERING_APP) do |app|
        before = File.binread(app.instance_variable_get(:@store).org)
        app.send(action)
        assert_equal before, File.binread(app.instance_variable_get(:@store).org), action
        assert_match message, app.instance_variable_get(:@flash), action
        assert_equal [:empty], app.instance_variable_get(:@store).undo!, action
      end
    end
  end

  def test_outline_reorder_is_one_journal_entry_and_u_restores_exact_bytes
    app_on(view: :outline, select: "Beta", content: ORDERING_APP) do |app|
      original = File.binread(app.instance_variable_get(:@store).org)
      app.send(:move_subtree_up)
      refute_equal original, File.binread(app.instance_variable_get(:@store).org)

      app.send(:handle_key, "u")
      assert_equal original, File.binread(app.instance_variable_get(:@store).org)
      assert_match(/undid: move up: Beta/, app.instance_variable_get(:@flash))
      assert_equal [:empty], app.instance_variable_get(:@store).undo!
    end
  end

  def test_ordering_keys_are_consumed_with_guidance_outside_unfiltered_outline
    app_on(view: :agenda, select: "Beta", content: SELECTION_FIXTURE) do |app|
      selected = app.send(:current_item).id
      app.send(:handle_key, "\ej")
      assert_equal selected, app.send(:current_item).id
      assert_match(/unfiltered Outline tab/, app.instance_variable_get(:@flash))
    end

    app_on(view: :outline, select: "Beta", content: ORDERING_APP) do |app|
      before = File.binread(app.instance_variable_get(:@store).org)
      ui(app).filter = "Beta"
      app.send(:rows)
      app.send(:handle_key, ">")
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)
      assert_match(/unfiltered Outline tab/, app.instance_variable_get(:@flash))

      ui(app).filter = nil
      ui(app).context_filter = "@work"
      app.send(:rows)
      app.send(:handle_key, "<")
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)
      assert_match(/unfiltered Outline tab/, app.instance_variable_get(:@flash))
    end
  end

  def test_ordering_palette_entries_exist_only_in_unfiltered_outline
    handlers = Tui::App::ORDERING_HANDLERS
    app_on(view: :outline, select: "Beta", content: ORDERING_APP) do |app|
      app.send(:open_action_palette)
      assert_equal handlers, ui(app).action_palette.entries.map(&:handler) & handlers
      app.send(:close_action_palette)

      ui(app).filter = "Beta"
      app.send(:rows)
      app.send(:open_action_palette)
      assert_empty ui(app).action_palette.entries.map(&:handler) & handlers
    end

    app_on(view: :agenda, select: "Beta", content: SELECTION_FIXTURE) do |app|
      app.send(:open_action_palette)
      assert_empty ui(app).action_palette.entries.map(&:handler) & handlers
    end
  end

  def test_escape_prefixed_alt_sequences_stay_atomic_across_split_reads
    cases = [
      [["\e".b, "k".b], "\ek"],
      [["\e".b, "\e".b, "[A".b], "\e\e[A"],
      [["\e[".b, "1;3".b, "A".b], "\e[1;3A"],
    ]
    cases.each do |chunks, expected|
      app_with(agent: FakeAgent.new(running: false), input: "") do |app|
        dispatched = []
        reader = Object.new
        reader.define_singleton_method(:read_nonblock) { |_size| chunks.shift }
        original_stdin = $stdin
        $stdin = reader
        begin
          IO.stub(:select, [[reader], [], []]) do
            app.stub(:handle_key, ->(key) { dispatched << key }) { app.send(:read_keys) }
          end
        ensure
          $stdin = original_stdin
        end
        assert_equal [expected], dispatched
        assert_equal "", app.instance_variable_get(:@key_data)
      end
    end
  end

  def test_coalesced_escape_and_non_alt_followup_remain_separate_keys
    app_with(agent: FakeAgent.new(running: false), input: "") do |app|
      ["\eq", "\e\r"].each do |input|
        dispatched = []
        app.instance_variable_set(:@key_data, input)
        app.stub(:handle_key, ->(key) { dispatched << key }) { app.send(:drain_key_data) }
        assert_equal ["\e", input[1..]], dispatched
      end
    end
  end

  TOO_DEEP_ORDERING_APP = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "de000001", "title" => "Deep" },
    { "type" => "task", "id" => "de000002", "parent" => "de000001", "state" => "TODO", "title" => "Depth one" },
    { "type" => "task", "id" => "de000003", "parent" => "de000002", "state" => "TODO", "title" => "Depth two" },
    { "type" => "task", "id" => "de000004", "parent" => "de000003", "state" => "TODO", "title" => "Depth three" },
    { "type" => "task", "id" => "de000005", "parent" => "de000004", "state" => "TODO", "title" => "Depth four A" },
    { "type" => "task", "id" => "de000006", "parent" => "de000004", "state" => "TODO", "title" => "Depth four B" },
  ])

  def test_indent_past_max_depth_is_visible_and_writes_nothing
    app_on(view: :outline, select: "Depth four B", content: TOO_DEEP_ORDERING_APP) do |app|
      before = File.binread(app.instance_variable_get(:@store).org)
      app.send(:indent_subtree)
      assert_equal before, File.binread(app.instance_variable_get(:@store).org)
      assert_match(/maximum task depth/, app.instance_variable_get(:@flash))
      assert_equal [:empty], app.instance_variable_get(:@store).undo!
    end
  end

  def test_cycle_stale_and_anchor_refusals_are_visible_without_writes
    expectations = {
      cycle: /own subtree/,
      stale: /changed underneath/,
      conflict: /anchor moved underneath/,
      not_found: /no longer exists/,
    }
    expectations.each do |status, message|
      app_on(view: :outline, select: "Beta", content: ORDERING_APP) do |app|
        before = File.binread(app.instance_variable_get(:@store).org)
        application = app.instance_variable_get(:@application)
        result = Tasks::MutationResult.new(status: status)
        rejecting = Object.new
        rejecting.define_singleton_method(:edit_snapshot) { |id| application.edit_snapshot(id) }
        rejecting.define_singleton_method(:read_tasks) { |**options| application.read_tasks(**options) }
        rejecting.define_singleton_method(:update_task) { |_command, today:, **_options| result }
        app.instance_variable_set(:@application, rejecting)
        app.send(:move_subtree_up)
        assert_equal before, File.binread(app.instance_variable_get(:@store).org), status
        assert_match message, app.instance_variable_get(:@flash), status
      end
    end
  end

  # -- outliner collapse / expand (h l H L) ----------------------------------

  # Work → "Ship release" (07-10) → "write notes" (07-12) → "grandchild task",
  # plus a sibling leaf "undated rider"; Home → "solo top" (07-15), a top-level
  # leaf. Rendered in agenda the rows are, in order: Ship release, write notes,
  # grandchild task, undated rider, solo top.
  NESTED_APP = dump_fixture([
    { "type" => "meta", "version" => 2 },
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

  # A `@` context filter (no `/` search) keeps the agenda on the tree path, so
  # subtasks render and H/L (collapse_all/expand_all) fold and unfold them. This
  # is the counterpart to the flat `/`-filter test above.
  NESTED_TAGGED_APP = dump_fixture([
    { "type" => "meta", "version" => 2 },
    { "type" => "section", "id" => "bbbb0001", "title" => "Work" },
    { "type" => "task", "id" => "bbbb0002", "parent" => "bbbb0001", "state" => "TODO",
      "title" => "Ship release", "tags" => %w[@work], "deadline" => "2026-07-10" },
    { "type" => "task", "id" => "bbbb0003", "parent" => "bbbb0002", "state" => "INBOX",
      "title" => "write notes" },
    { "type" => "section", "id" => "bbbb0004", "title" => "Home" },
    { "type" => "task", "id" => "bbbb0005", "parent" => "bbbb0004", "state" => "TODO",
      "title" => "mow lawn", "tags" => %w[@home], "deadline" => "2026-07-11" },
  ])

  def test_context_filter_agenda_shows_subtasks_and_collapses
    app_on(view: :agenda, select: "Ship release", content: NESTED_TAGGED_APP) do |app|
      ui(app).context_filter = "@work"
      app.send(:rows)

      rows = app.instance_variable_get(:@rows)
      parent = rows.find { |r| r.item&.title == "Ship release" }
      child  = rows.find { |r| r.item&.title == "write notes" }
      refute_nil parent.node, "context-filtered agenda stays on the tree path"
      refute_nil child, "an untagged subtask shows under its @work parent"
      refute_includes row_titles(app), "mow lawn", "the @home thread is scoped out"

      app.send(:select_row, rows.index(parent))
      app.send(:collapse_all)
      refute_includes row_titles(app), "write notes", "H folds the subtree"
      app.send(:expand_all)
      assert_includes row_titles(app), "write notes", "L unfolds it again"
    end
  end

  # The run loop's reload gate must survive the mutation Store consuming its
  # own mtime signal: an editor-session read (store.items during a cascade
  # confirmation) self-reloads @store, after which @store.changed? is false —
  # but the rendered read model is still pre-write and must trigger the reload.
  def test_external_change_detected_after_a_store_read_consumes_the_signal
    app_with(agent: FakeAgent.new(running: false), input: "") do |app|
      app.send(:read_model) # build the presentation model over the current file
      store = app.instance_variable_get(:@store)

      records = FIXTURE_RECORDS.map(&:dup)
      records << { "type" => "task", "id" => "bbbb0001", "parent" => FIX[:home],
                   "state" => "TODO", "title" => "External write" }
      File.write(store.org, Tasks::Format.dump(records))

      store.items # the signal-consuming read (editor session, archive preview)
      refute_predicate store, :changed?, "precondition: the store self-reloaded"

      assert app.send(:external_change?),
             "a stale read model must trigger the reload even after @store consumed the mtime signal"

      app.send(:reload_store)
      refute app.send(:external_change?)
      assert_includes app.send(:read_model).items.map(&:title), "External write"
    end
  end

  # -- Projects tab actions ----------------------------------------------------

  PROJ_DATE = -> { Date.new(2026, 7, 20) }

  def on_project(select, host_context: nil, &blk)
    app_on(view: :projects, select: select, content: PROJECTS_FIXTURE,
           date_provider: PROJ_DATE, host_context: host_context, &blk)
  end

  def org_path(app) = app.instance_variable_get(:@store).org
  def archive_path(app) = app.instance_variable_get(:@paths).archive

  def test_enter_on_project_row_opens_project_detail_and_nav_refreshes_it
    on_project("Site launch") do |app|
      app.send(:handle_key, "\r")
      assert_equal :project_detail, ui(app).panel.kind
      assert_equal PFIX[:site], ui(app).panel.identity
      assert_includes ui(app).panel.lines.map { |l| Tui::Ansi.strip(l) }.join("\n"), "Site launch"

      # Navigating to another project header refreshes the same panel to it.
      app.send(:reselect, PFIX[:reno])
      assert_equal :project_detail, ui(app).panel.kind
      assert_equal PFIX[:reno], ui(app).panel.identity
    end
  end

  def test_complete_project_confirms_then_closes_open_descendants
    on_project("Site launch") do |app|
      app.send(:handle_key, "c")
      assert_equal :project_complete_confirm, ui(app).modal.kind
      app.send(:handle_key, "y")

      refute app.send(:external_change?), "project completion's own write is absorbed"
      assert_equal "DONE", record_for(org_path(app), title: "Pick a static-site generator")["state"]
      assert_equal "DONE", record_for(org_path(app), title: "Draft the about page")["state"]
      assert Tasks::Check.check(org_path(app)).ok?, "file stays valid after completing a project"
      assert_match(/closed \d+ in Site launch/, app.instance_variable_get(:@flash))
      assert_equal PFIX[:site], ui(app).selected_id, "selection stays on the project"
    end
  end

  def test_archive_project_confirms_then_moves_subtree_to_archive_file
    on_project("Site launch") do |app|
      app.send(:handle_key, "x")
      assert_equal :project_archive_confirm, ui(app).modal.kind
      assert_includes ui(app).modal.lines.join(" "), "open task"
      app.send(:handle_key, "y")

      refute app.send(:external_change?), "project archive's own write is absorbed"
      assert_nil record_for(org_path(app), title: "Site launch"), "swept out of the live file"
      assert record_for(archive_path(app), title: "Site launch"), "moved into archive.jsonl"
      assert_match(/archived Site launch/, app.instance_variable_get(:@flash))
    end
  end

  def test_rename_project_prefills_submits_and_follows_the_id
    on_project("Stuck reno") do |app|
      app.send(:handle_key, "e")
      assert_equal :project_rename, ui(app).form.kind
      assert_equal "Stuck reno", ui(app).form.input.to_s, "form prefilled with the title"
      ui(app).form.input.replace("Kitchen reno")
      app.send(:handle_key, "\r")

      refute app.send(:external_change?), "project rename's own write is absorbed"
      assert_equal "Kitchen reno", record_for(org_path(app), title: "Kitchen reno")["title"]
      assert_equal PFIX[:reno], ui(app).selected_id, "selection follows the renamed project id"
      assert_match(/renamed: Kitchen reno/, app.instance_variable_get(:@flash))
    end
  end

  def test_rename_project_blank_title_errors_and_writes_nothing
    on_project("Stuck reno") do |app|
      app.send(:handle_key, "e")
      ui(app).form.input.replace("   ")
      app.send(:handle_key, "\r")

      refute_nil ui(app).form, "form stays open on a blank title"
      assert_equal "title cannot be blank", ui(app).form.error
      assert_equal "Stuck reno", record_for(org_path(app), title: "Stuck reno")["title"]
    end
  end

  def test_capture_into_project_creates_a_todo_under_the_section
    on_project("Stuck reno", host_context: "@work") do |app|
      app.send(:handle_key, "a")
      assert_equal :project_capture, ui(app).form.kind
      ui(app).form.input.replace("Order the tiles")
      app.send(:handle_key, "\r")

      refute app.send(:external_change?), "project capture's own write is absorbed"
      record = record_for(org_path(app), title: "Order the tiles")
      refute_nil record, "the new task exists"
      assert_equal "TODO", record["state"]
      assert_equal %w[@work], record["tags"]
      assert_equal PFIX[:reno], record["parent"], "appended under the section"
      assert_includes row_titles(app), "Order the tiles", "visible under the project"
    end
  end

  def test_task_only_action_on_a_project_row_flashes_and_does_nothing
    on_project("Site launch") do |app|
      before = File.read(org_path(app))
      app.send(:handle_key, "d") # edit date — a task-only action
      assert_equal :list, ui(app).mode, "no popup opens"
      assert_nil ui(app).form
      assert_match(/select a task for that/, app.instance_variable_get(:@flash))
      assert_equal before, File.read(org_path(app)), "nothing was written"
    end
  end

  def test_palette_availability_splits_project_and_task_actions
    on_project("Site launch") do |app|
      handlers = Tui::Shortcuts.palette_entries(:list, app).map(&:handler)
      assert_includes handlers, :rename_project, "project actions show for a project row"
      assert_includes handlers, :capture_into_project
      refute_includes handlers, :open_date_popup, "task-only actions hide for a project row"

      # Move onto a task row under the project; the split flips.
      task_idx = app.instance_variable_get(:@rows)
                    .index { |r| r.item&.title == "Pick a static-site generator" }
      app.send(:select_row, task_idx)
      task_handlers = Tui::Shortcuts.palette_entries(:list, app).map(&:handler)
      refute_includes task_handlers, :rename_project, "project actions hide for a task row"
      assert_includes task_handlers, :open_date_popup, "task actions show for a task row"
    end
  end

  # -- mouse support ----------------------------------------------------------

  def mouse_click(app, row:, col:)
    app.send(:handle_mouse, "\e[<0;#{col + 1};#{row + 1}M")
  end

  def mouse_wheel(app, row:, col:, dir: :down)
    cb = dir == :up ? 64 : 65
    app.send(:handle_mouse, "\e[<#{cb};#{col + 1};#{row + 1}M")
  end

  def paint_at(app, height: 24, width: 80)
    console = Struct.new(:winsize).new([height, width])
    IO.stub(:console, console) do
      capture_io { app.send(:paint) }
    end
    app.instance_variable_get(:@last_layout)
  end

  def test_mouse_click_selects_row_and_second_click_opens_detail
    app_on(view: :agenda, select: "Book flight") do |app|
      layout = paint_at(app)
      rows = app.instance_variable_get(:@rows)
      other = rows.each_index.find { |i| i != app.instance_variable_get(:@sel) && rows[i].selectable? }
      screen_row = layout.body_rows.begin + (other - layout.viewport_offset)
      screen_col = layout.list_cols.begin + 4

      mouse_click(app, row: screen_row, col: screen_col)
      assert_equal other, app.instance_variable_get(:@sel)

      mouse_click(app, row: screen_row, col: screen_col)
      assert app.send(:detail_panel?)
    end
  end

  def test_mouse_wheel_over_panel_scrolls_panel_not_selection
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:open_detail)
      layout = paint_at(app)
      sel_before = app.instance_variable_get(:@sel)
      scroll_before = ui(app).panel.scroll
      body_row = layout.body_rows.begin
      panel_col = layout.panel_cols.begin

      mouse_wheel(app, row: body_row, col: panel_col, dir: :down)
      assert_equal sel_before, app.instance_variable_get(:@sel)
      assert_operator ui(app).panel.scroll, :>=, scroll_before
    end
  end

  # Wheel-up is the report a downward gesture produces under macOS natural
  # scrolling, so it advances the selection and wheel-down brings it back.
  def test_mouse_wheel_over_list_moves_selection
    app_on(view: :agenda, select: "Book flight") do |app|
      layout = paint_at(app)
      sel_before = app.instance_variable_get(:@sel)
      body_row = layout.body_rows.begin + (sel_before - layout.viewport_offset)
      list_col = layout.list_cols.begin + 4

      mouse_wheel(app, row: body_row, col: list_col, dir: :up)
      advanced = app.instance_variable_get(:@sel)
      assert_operator advanced, :>, sel_before

      mouse_wheel(app, row: body_row, col: list_col, dir: :down)
      assert_operator app.instance_variable_get(:@sel), :<, advanced
    end
  end

  def test_mouse_click_on_section_header_changes_nothing
    app_on(view: :next, select: "Water the plants") do |app|
      layout = paint_at(app)
      rows = app.instance_variable_get(:@rows)
      header_idx = rows.each_index.find { |i| !rows[i].selectable? && !rows[i].text.empty? }
      skip "no section header in fixture next view" unless header_idx
      sel_before = app.instance_variable_get(:@sel)
      id_before = ui(app).selected_id
      screen_row = layout.body_rows.begin + (header_idx - layout.viewport_offset)
      mouse_click(app, row: screen_row, col: layout.list_cols.begin + 2)
      assert_equal sel_before, app.instance_variable_get(:@sel)
      assert_equal id_before, ui(app).selected_id
    end
  end

  def test_mouse_click_on_tab_switches_view
    app_on(view: :agenda, select: "Book flight") do |app|
      paint_at(app)
      spans = Tui::Views.tab_spans(active: :agenda)
      key, start_col, = spans.find { |k, _, _| k == :next }
      mouse_click(app, row: 1, col: start_col)
      assert_equal key, ui(app).view
    end
  end

  def test_active_combined_inbox_and_paired_count_remain_visible_and_click_aligned_when_narrow
    app_on(
      view: :inbox, select: "Alpha proposal", content: PROPOSAL_APP
    ) do |app|
      [72, 80].each do |width|
        paint_at(app, width: width)
        presentation = app.instance_variable_get(:@last_tab_presentation)
        assert_match(/6 (?:Inbox 1 · Approvals 2|In 1 · Ap 2|I1 A2)/,
                     Tui::Ansi.strip(presentation.strip))
        span = presentation.spans.find { |key, _start, _finish| key == :inbox }
        refute_nil span
        assert_operator span[2], :<=, width - 1

        hit = app.send(:hit_map).at(1, span[1])
        assert_equal :tab, hit.zone
        assert_equal :inbox, hit.payload
      end

      ui(app).view = :agenda
      paint_at(app, width: 72)
      presentation = app.instance_variable_get(:@last_tab_presentation)
      assert_includes Tui::Ansi.strip(presentation.strip), "1 Agenda"
      assert_match(/6 (?:In 1 · Ap 2|I1 A2)/, Tui::Ansi.strip(presentation.strip))
      span = presentation.spans.find { |key, _start, _finish| key == :inbox }
      assert_equal :inbox, app.send(:hit_map).at(1, span[1]).payload
    end
  end

  # Painting hides the row cursor while the prompt has focus, so a pointer
  # gesture aimed at the list has to blur the prompt or it reads as a no-op.
  def test_mouse_click_on_list_blurs_the_prompt_and_keeps_the_draft
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:focus_prompt)
      app.instance_variable_get(:@input).insert("reschedule this")
      layout = paint_at(app)
      rows = app.instance_variable_get(:@rows)
      target = rows.each_index.find { |i| i != app.instance_variable_get(:@sel) && rows[i].selectable? }

      mouse_click(app, row: layout.body_rows.begin + (target - layout.viewport_offset),
                       col: layout.list_cols.begin + 4)

      assert_equal :list, ui(app).mode
      assert_equal target, app.instance_variable_get(:@sel)
      assert_equal "reschedule this", app.instance_variable_get(:@input).text
    end
  end

  def test_mouse_wheel_over_list_blurs_the_prompt
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:focus_prompt)
      layout = paint_at(app)
      mouse_wheel(app, row: layout.body_rows.begin, col: layout.list_cols.begin + 4, dir: :down)
      assert_equal :list, ui(app).mode
    end
  end

  def test_mouse_click_on_tab_blurs_the_prompt
    app_on(view: :agenda, select: "Book flight") do |app|
      app.send(:focus_prompt)
      paint_at(app)
      _, start_col, = Tui::Views.tab_spans(active: :agenda).find { |k, _, _| k == :next }
      mouse_click(app, row: 1, col: start_col)
      assert_equal :list, ui(app).mode
      assert_equal :next, ui(app).view
    end
  end

  # A modal is a blocking overlay: a click landing beside the box must not move
  # the selection or open a detail panel behind it.
  def test_mouse_cannot_reach_the_list_behind_an_open_modal
    app_on(view: :outline, select: "Book flight") do |app|
      app.send(:open_modal, { title: "tiny", lines: ["one"] }, kind: :help)
      layout = paint_at(app, width: 100)
      rows = app.instance_variable_get(:@rows)
      sel_before = app.instance_variable_get(:@sel)
      col = layout.list_cols.begin + 3
      outside = layout.body_rows.find do |row|
        hit = app.send(:hit_map).at(row, col)
        hit.zone == :list_row && rows[hit.payload]&.selectable? && hit.payload != sel_before
      end
      refute_nil outside, "expected a selectable list row beside the modal box"

      mouse_click(app, row: outside, col: col)
      mouse_click(app, row: outside, col: col)
      mouse_wheel(app, row: outside, col: col, dir: :down)

      assert_equal sel_before, app.instance_variable_get(:@sel)
      assert_nil ui(app).panel
      assert_equal :modal, ui(app).mode
    end
  end

  def test_mouse_disabled_skips_handle_mouse_in_drain
    app_on(view: :agenda, select: "Book flight") do |app|
      app.instance_variable_get(:@paths).mouse = false
      called = false
      app.stub(:handle_mouse, ->(*) { called = true }) do
        app.instance_variable_set(:@key_data, +"\e[<0;5;7M")
        app.send(:drain_key_data)
      end
      refute called
      assert_equal "", app.instance_variable_get(:@key_data)
    end
  end

  def test_mouse_before_first_paint_is_ignored
    app_on(view: :agenda, select: "Book flight") do |app|
      assert_nil app.instance_variable_get(:@last_layout)
      app.send(:handle_mouse, "\e[<0;5;7M") # must not raise
    end
  end

  def test_mouse_sequence_split_across_chunks_does_not_become_escape
    app_on(view: :agenda, select: "Book flight") do |app|
      layout = paint_at(app)
      sel_before = app.instance_variable_get(:@sel)
      # Partial SGR then remainder — must not flush as Escape.
      app.instance_variable_set(:@key_data, +"\e[<0;5;")
      assert app.send(:incomplete_escape_sequence?)
      app.send(:drain_key_data, flush_incomplete_escape: false)
      assert_equal "\e[<0;5;", app.instance_variable_get(:@key_data)

      body_row = layout.body_rows.begin
      list_col = layout.list_cols.begin + 4
      app.stub(:mouse_enabled?, true) do
        app.instance_variable_set(
          :@key_data,
          +"\e[<0;#{list_col + 1};#{body_row + 1}M"
        )
        app.send(:drain_key_data)
      end
      assert_equal "", app.instance_variable_get(:@key_data)
      assert_kind_of Integer, app.instance_variable_get(:@sel)
      assert_kind_of Integer, sel_before
    end
  end

  def test_mouse_wheel_burst_applies_every_report_in_order
    app_on(view: :agenda, select: "Book flight") do |app|
      layout = paint_at(app)
      body_row = layout.body_rows.begin
      list_col = layout.list_cols.begin + 4
      burst = 4.times.map { "\e[<65;#{list_col + 1};#{body_row + 1}M" }.join
      intents = []
      app.stub(:mouse_enabled?, true) do
        app.stub(:apply_mouse_intent, ->(intent) { intents << intent; app.instance_variable_set(:@paint_dirty, true) }) do
          app.instance_variable_set(:@key_data, +burst)
          app.send(:drain_key_data)
        end
      end
      assert_equal 4, intents.size
      assert intents.all? { |i| i in [:scroll_list, Integer] }
    end
  end

  def test_footer_roles_marks_wrapped_prompt_continuations
    app_on(view: :agenda, select: "Book flight") do |app|
      roles = app.send(:footer_roles_for, [
        " ❯ first line of a long prompt",
        "   continuation without the glyph",
        "   another continuation",
      ])
      assert_equal %i[prompt prompt prompt], roles
    end
  end

  def test_mouse_intent_invalidates_hit_map_for_next_report_in_chunk
    app_on(view: :agenda, select: "Book flight") do |app|
      paint_at(app)
      map1 = app.send(:hit_map)
      app.send(:apply_mouse_intent, [:switch_view, :next])
      assert_nil app.instance_variable_get(:@hit_map)
      map2 = app.send(:hit_map)
      refute_same map1, map2
      assert_equal :next, ui(app).view
    end
  end
end
