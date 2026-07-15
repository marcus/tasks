# frozen_string_literal: true

require_relative "test_helper"
require "tui/app"

# Paint-path memoization: row cache, detail gating, filter haystack, idle dirty flag.
class TestAppPaintPerf < Minitest::Test
  def ui(app) = app.instance_variable_get(:@ui)

  def app_on(view: :agenda, select: nil)
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config,
                         agent_probe: ->(_entry) { false })
      ui(app).view = view
      app.send(:rows)
      if select
        idx = app.instance_variable_get(:@rows).index { |r| r.item&.title&.include?(select) }
        raise "missing #{select}" unless idx

        app.send(:select_row, idx)
      end
      yield app
    end
  end

  def test_rows_reuse_cached_list_when_fingerprint_unchanged
    app_on(select: "Book flight") do |app|
      first = app.send(:rows)
      second = app.send(:rows)
      assert_same first, second
    end
  end

  def test_rows_rebuild_when_filter_changes
    app_on do |app|
      before = app.send(:rows)
      ui(app).filter = "flight"
      after = app.send(:rows)
      refute_same before, after
      assert(after.any? { |r| r.item&.title&.include?("flight") })
    end
  end

  def test_rows_rebuild_when_collapsed_set_mutates
    app_on(view: :projects, select: "Book flight") do |app|
      before = app.send(:rows)
      item = app.send(:current_item)
      ui(app).collapsed.add(item.id) if item&.id
      after = app.send(:rows)
      refute_same before, after
    end
  end

  def test_selection_move_does_not_rebuild_rows
    app_on(select: "Book flight") do |app|
      rows = app.send(:rows)
      app.send(:move, 1)
      assert_same rows, app.send(:rows)
    end
  end

  def test_detail_refresh_skips_identical_rebuild
    app_on(select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      assert ui(app).panel.kind == :detail

      builds = 0
      Tui::TaskDetails.stub(:build, lambda { |*args, **kwargs|
        builds += 1
        { title: "t", lines: ["line"] }
      }) do
        app.send(:refresh_detail_panel)
        app.send(:refresh_detail_panel)
        app.send(:select_row, app.instance_variable_get(:@sel))
      end
      assert_equal 0, builds, "gated refresh must not rebuild when id/width/model are unchanged"
    end
  end

  def test_detail_refresh_rebuilds_after_read_model_invalidation
    app_on(select: "Book flight") do |app|
      app.send(:handle_key, "\r")
      builds = 0
      Tui::TaskDetails.stub(:build, lambda { |*args, **kwargs|
        builds += 1
        { title: "t", lines: ["line"] }
      }) do
        app.send(:invalidate_read_model)
        app.send(:rows)
        app.send(:refresh_detail_panel)
      end
      assert_operator builds, :>=, 1
    end
  end

  def test_title_haystack_is_reused_across_filter_keystrokes
    app_on do |app|
      read = app.send(:read_model)
      first = app.send(:title_haystack, read)
      second = app.send(:title_haystack, read)
      assert_same first, second
      assert_equal read.items.first.title.downcase, first[read.items.first.id]
    end
  end

  def test_paint_if_needed_skips_clean_idle_ticks
    app_on do |app|
      paints = 0
      app.stub(:paint, -> { paints += 1 }) do
        app.instance_variable_set(:@paint_dirty, false)
        app.send(:paint_if_needed)
        assert_equal 0, paints

        app.instance_variable_set(:@paint_dirty, true)
        app.send(:paint_if_needed)
        assert_equal 1, paints
        refute app.instance_variable_get(:@paint_dirty)
      end
    end
  end

  def test_idle_layout_changed_on_terminal_resize
    app_on do |app|
      capture_io { app.send(:paint) }
      app.instance_variable_set(:@paint_dirty, false)
      refute app.send(:idle_layout_changed?)

      app.instance_variable_set(:@last_paint_size, [10, 40])
      console = Struct.new(:winsize).new([24, 80])
      IO.stub(:console, console) do
        assert app.send(:idle_layout_changed?)
      end
    end
  end

  def test_idle_layout_changed_on_date_rollover
    day = Date.new(2026, 7, 15)
    next_day = day + 1
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE_ORG)
      current = day
      app = Tui::App.new(root: dir, paths: Tasks::Config.for_dir(dir),
                         llm_config: default_llm_config,
                         agent_probe: ->(_entry) { false },
                         date_provider: -> { current })
      app.send(:rows)
      refute app.send(:idle_layout_changed?)
      current = next_day
      assert app.send(:idle_layout_changed?)
    end
  end

  def test_clear_row_caches_drops_stale_row_list
    app_on do |app|
      app.send(:rows)
      refute_nil app.instance_variable_get(:@rows)
      app.send(:clear_row_caches)
      assert_nil app.instance_variable_get(:@rows)
      assert_equal 0, app.instance_variable_get(:@row_item_count)
    end
  end

  def test_animated_paint_while_agent_active
    app_on do |app|
      request = Struct.new(:id).new(1)
      app.instance_variable_get(:@agent_queue).stub(:active_request, request) do
        assert app.send(:animated_paint?)
      end
      refute app.send(:animated_paint?)
    end
  end

  def test_open_task_count_cached_per_read_model
    app_on do |app|
      read = app.send(:read_model)
      first = app.send(:open_task_count, read)
      second = app.send(:open_task_count, read)
      assert_equal first, second
      assert_equal first, app.instance_variable_get(:@open_count)
    end
  end
end
