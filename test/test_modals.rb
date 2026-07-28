# frozen_string_literal: true

require_relative "test_helper"
require "tui/task_details"
require "tasks/application"

class TestTaskDetails < Minitest::Test
  D = Tui::TaskDetails
  A = Tui::Ansi
  TODAY = Date.new(2026, 7, 1)

  def detail_for(text)
    with_store do |store, _o, _a|
      item = find_item(store, text)
      return D.build(item, store.body(item), 64, today: TODAY)
    end
  end

  def texts(modal) = modal[:lines].map { |l| A.strip(l) }

  def test_detail_shows_core_fields
    lines = texts(detail_for("Book flight"))
    assert_includes lines.first, "Book flight in Concur"
    assert lines.any? { |l| l =~ /state\s+NEXT/ }
    assert lines.any? { |l| l =~ /priority\s+\[#A\]/ }
    assert lines.any? { |l| l =~ /deadline\s+2026-07-02 Thu · in 1d/ }
    assert lines.any? { |l| l =~ /contexts\s+@computer/ }
    assert lines.any? { |l| l =~ /tags\s+important\s+urgent/ }
  end

  def test_detail_scheduled_item_has_no_deadline_row
    lines = texts(detail_for("self-eval"))
    assert lines.any? { |l| l =~ /available from\s+2026-07-03 Fri · in 2d/ }
    refute lines.any? { |l| l.start_with?("deadline") }
  end

  def test_detail_shows_stored_fixed_time_and_configured_zone_projection
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "s1", "title" => "Work" },
      { "type" => "task", "id" => "t1", "parent" => "s1", "state" => "NEXT",
        "title" => "London call", "deadline" => "2026-07-02",
        "deadline_time" => { "local" => "17:00", "timezone" => "Europe/London" } },
    ]
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture(records))
      store = Tui::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      context = Tasks::TemporalContext.new(
        now: Time.utc(2026, 7, 1, 19), timezone: "America/Los_Angeles"
      )
      reader = Tasks::TaskReadModel.new(store.read_snapshot, temporal_context: context)
      task = reader.task_for("t1")
      lines = texts(D.build(task, task.body, 100, today: TODAY, temporal_context: context))

      assert lines.any? { |line| line.include?("2026-07-02 17:00 Europe/London") }
      assert lines.any? { |line| line.include?("→ 2026-07-02 09:00 America/Los_Angeles") }
      assert lines.any? { |line| line.include?("due in 21h") }
    end
  end

  def test_detail_distinguishes_available_from_and_inherited_on_hold
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "s1", "title" => "Work" },
      { "type" => "task", "id" => "p1", "parent" => "s1", "state" => "TODO",
        "title" => "held parent", "tags" => %w[defer] },
      { "type" => "task", "id" => "c1", "parent" => "p1", "state" => "NEXT",
        "title" => "blocked child", "scheduled" => "2026-07-01" },
    ]
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture(records))
      store = Tui::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      reader = Tasks::TaskReadModel.new(store.read_snapshot, today: TODAY)
      child = reader.task_for("c1")
      parent = reader.task_for("p1")
      lines = texts(D.build(child, child.body, 80, today: TODAY,
                            availability_blocker: parent))

      assert lines.any? { |line| line =~ /available from\s+2026-07-01/ }
      assert lines.any? { |line| line.include?("on hold via parent held parent") }
    end
  end

  def test_detail_includes_notes_but_not_stamps
    lines = texts(detail_for("Travel desk"))
    assert lines.any? { |l| l.include?("Some note line.") }
    refute lines.any? { |l| l.include?("SCHEDULED:") }
  end

  def test_detail_shows_closed_row_when_present
    lines = texts(detail_for("Old finished thing"))
    assert lines.any? { |l| l =~ /closed\s+2026-06-20/ }
  end

  def test_detail_open_item_has_no_closed_row
    lines = texts(detail_for("Book flight"))
    refute lines.any? { |l| l.start_with?("closed") }
  end

  def test_detail_item_without_extras_is_minimal
    lines = texts(detail_for("Water the plants"))
    refute lines.any? { |l| l.start_with?("deadline") }
    refute lines.any? { |l| l.include?("notes") }
    assert lines.any? { |l| l =~ /contexts\s+@home/ }
  end

  def test_detail_wraps_long_titles
    with_store do |store, org, _a|
      File.write(org, dump_fixture([
                        { "type" => "meta", "version" => 2 },
                        { "type" => "section", "id" => "cccc0001", "title" => "X" },
                        { "type" => "task", "id" => "cccc0002", "parent" => "cccc0001",
                          "state" => "TODO", "title" => "very long title word " * 10,
                          "tags" => %w[@computer] },
                      ]))
      store.reload!
      item = store.items.first
      details = D.build(item, store.body(item), 48, today: TODAY)
      title_lines = details[:lines].take_while { |line| !A.strip(line).empty? }
      assert title_lines.size > 1, "long title should wrap to multiple lines"
    end
  end

  # -- delegation section ----------------------------------------------------

  # Build the panel for one task carrying `delegation`, through the canonical
  # reader (the marker lives on the TaskView, not on a presentation Item).
  def delegated_detail(delegation, state: "NEXT")
    records = [
      { "type" => "meta", "version" => 2 },
      { "type" => "section", "id" => "eeee0001", "title" => "Work" },
      { "type" => "task", "id" => "eeee0002", "parent" => "eeee0001", "state" => state,
        "title" => "Compare CRDT libraries", "delegation" => delegation },
    ]
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture(records))
      store = Tui::Store.new(org: org, archive: File.join(dir, "archive.jsonl"))
      task = Tasks::TaskReadModel.new(store.read_snapshot, today: TODAY).task_for("eeee0002")
      return texts(D.build(task, task.body, 72, today: TODAY))
    end
  end

  def test_detail_shows_the_human_delegation_section
    lines = delegated_detail(
      { "kind" => "human", "status" => "delegated", "assignee" => "pat@example.com",
        "at" => "2026-07-01T18:04:11Z", "work_ref" => "https://acme.example/issues/12" },
      state: "WAITING"
    )
    assert_includes lines, "delegation"
    assert lines.any? { |line| line =~ /\A {2}kind\s+human\z/ }
    assert lines.any? { |line| line =~ /\A {2}status\s+delegated\z/ }
    assert lines.any? { |line| line =~ /\A {2}assignee\s+pat@example\.com\z/ }
    assert lines.any? { |line| line =~ /\A {2}at\s+2026-07-01T18:04:11Z\z/ }
    assert lines.any? { |line| line =~ %r{\A {2}work ref\s+https://acme\.example/issues/12\z} }
    refute lines.any? { |line| line.start_with?("  mode") }, "a human delegation has no mode"
  end

  def test_detail_shows_agent_mode_and_the_claim_holder
    ready = delegated_detail(
      { "kind" => "agent", "mode" => "research", "status" => "ready",
        "at" => "2026-07-01T18:04:11Z" }
    )
    assert lines_include?(ready, /\A {2}mode\s+research\z/)
    assert lines_include?(ready, /\A {2}status\s+ready\z/)
    refute lines_include?(ready, /\A {2}assignee/), "ready work names no worker"
    refute lines_include?(ready, /\A {2}work ref/), "an absent reference emits no row"

    claimed = delegated_detail(
      { "kind" => "agent", "mode" => "research", "status" => "claimed",
        "assignee" => "claude-code/claude-fable-5/313cf82e", "at" => "2026-07-01T18:04:11Z" }
    )
    assert lines_include?(claimed, /\A {2}status\s+claimed\z/)
    assert lines_include?(claimed, %r{\A {2}assignee\s+claude-code/claude-fable-5/313cf82e\z})
  end

  def test_detail_delegation_section_is_absent_without_a_marker
    lines = texts(detail_for("Book flight"))
    refute_includes lines, "delegation"
  end

  def test_detail_claim_stays_distinguishable_without_color
    Tui::Theme.configure!(name: "mono")
    # mono resolves :accent to bold and :muted to dim, so a live claim still
    # reads louder than an idle delegation with no colors available.
    assert_includes D.delegation_status("claimed"), "\e[1m"
    assert_includes D.delegation_status("ready"), "\e[2m"
    refute_includes D.delegation_status("ready"), "\e[1m"
    assert_includes D.delegation_status("delegated"), "\e[2m"
  ensure
    Tui::Theme.reset!
  end

  def lines_include?(lines, pattern) = lines.any? { |line| line.match?(pattern) }
end
