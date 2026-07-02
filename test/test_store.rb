# frozen_string_literal: true

require_relative "test_helper"

class TestStore < Minitest::Test
  def test_parse_finds_all_items
    with_store do |store, _org, _archive|
      assert_equal 7, store.items.size
      assert_equal %w[INBOX NEXT NEXT TODO WAITING DONE NEXT], store.items.map(&:state)
    end
  end

  def test_parse_reads_priority_tags_and_dates
    with_store do |store, _org, _archive|
      flight = find_item(store, "Book flight")
      assert_equal "A", flight.priority
      assert_equal Date.new(2026, 7, 2), flight.deadline
      assert_includes flight.tags, "@computer"
      assert_includes flight.tags, "important"

      eval = find_item(store, "self-eval")
      assert_equal Date.new(2026, 7, 3), eval.scheduled
      assert_nil eval.deadline
    end
  end

  def test_changed_detects_external_writes
    with_store do |store, org, _archive|
      store.reload!
      refute store.changed?
      future = Time.now + 2
      File.write(org, FIXTURE_ORG + "** TODO added later\n")
      File.utime(future, future, org) # avoid same-mtime flakiness
      assert store.changed?
      assert_equal 8, store.items.size
    end
  end

  def test_complete_marks_done_with_closed_stamp
    with_store do |store, org, _archive|
      flight = find_item(store, "Book flight")
      assert store.complete!(flight)
      text = File.read(org)
      assert_match(/^\*\* DONE \[#A\] Book flight in Concur/, text)
      assert_match(/CLOSED: \[#{Date.today}\]/, text)
      assert_equal "DONE", find_item(store, "Book flight").state
    end
  end

  def test_complete_rejects_stale_line_numbers
    with_store do |store, org, _archive|
      flight = find_item(store, "Book flight")
      stale = flight.dup
      stale.line = 1 # points at "* Inbox", not the flight headline
      refute store.complete!(stale)
      refute_match(/DONE.*Book flight/, File.read(org))
    end
  end

  def test_reschedule_updates_existing_deadline
    with_store do |store, org, _archive|
      flight = find_item(store, "Book flight")
      assert store.reschedule!(flight, Date.new(2026, 7, 10))
      assert_match(/DEADLINE: <2026-07-10 Fri>/, File.read(org))
      refute_match(/2026-07-02/, File.read(org))
      assert_equal Date.new(2026, 7, 10), find_item(store, "Book flight").deadline
    end
  end

  def test_reschedule_updates_scheduled_when_no_deadline
    with_store do |store, org, _archive|
      eval = find_item(store, "self-eval")
      assert store.reschedule!(eval, Date.new(2026, 7, 8))
      assert_match(/SCHEDULED: <2026-07-08 Wed>/, File.read(org))
      # the self-eval block itself must not have gained a DEADLINE
      lines = File.readlines(org)
      idx = lines.index { |l| l.include?("self-eval") }
      refute_includes lines[idx + 1], "DEADLINE"
    end
  end

  def test_reschedule_adds_deadline_when_item_has_no_stamp
    with_store do |store, org, _archive|
      plants = find_item(store, "Water the plants")
      assert store.reschedule!(plants, Date.new(2026, 7, 5))
      assert_equal Date.new(2026, 7, 5), find_item(store, "Water the plants").deadline
      lines = File.readlines(org)
      idx = lines.index { |l| l.include?("Water the plants") }
      assert_match(/DEADLINE: <2026-07-05 Sun>/, lines[idx + 1])
    end
  end

  def test_reschedule_does_not_touch_next_items_stamp
    with_store do |store, org, _archive|
      # WAITING item has no stamp but is followed by other content;
      # rescheduling it must not modify a different item's stamp.
      waiting = find_item(store, "Travel desk")
      assert store.reschedule!(waiting, Date.new(2026, 7, 9))
      assert_equal Date.new(2026, 7, 2), find_item(store, "Book flight").deadline
      assert_equal Date.new(2026, 7, 9), find_item(store, "Travel desk").deadline
    end
  end

  def test_archive_sweeps_done_blocks
    with_store do |store, org, archive|
      n = store.archive_swept!
      assert_equal 1, n
      refute_match(/Old finished thing/, File.read(org))
      arch = File.read(archive)
      assert_match(/DONE \[#C\] Old finished thing/, arch)
      assert_match(/CLOSED: \[2026-06-20\]/, arch) # block body moves too
      assert_equal 6, store.items.size
    end
  end

  def test_set_priority_replaces_existing_cookie
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.set_priority!(flight, "B")
      assert_match(/^\*\* NEXT \[#B\] Book flight in Concur :@computer:important:urgent:$/, File.read(org))
      assert_equal "B", find_item(store, "Book flight").priority
    end
  end

  def test_set_priority_adds_cookie_to_unprioritized_item
    with_store do |store, org, _a|
      plants = find_item(store, "Water the plants")
      assert store.set_priority!(plants, "C")
      assert_match(/^\*\* NEXT \[#C\] Water the plants/, File.read(org))
    end
  end

  def test_set_priority_nil_removes_cookie
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      assert store.set_priority!(flight, nil)
      assert_match(/^\*\* NEXT Book flight in Concur/, File.read(org))
      assert_nil find_item(store, "Book flight").priority
    end
  end

  def test_set_priority_rejects_stale_line_numbers
    with_store do |store, org, _a|
      stale = find_item(store, "Book flight").dup
      stale.line = 1
      refute store.set_priority!(stale, "B")
      assert_match(/\[#A\] Book flight/, File.read(org))
    end
  end

  def test_block_returns_headline_and_body
    with_store do |store, _o, _a|
      waiting = find_item(store, "Travel desk")
      block = store.block(waiting)
      assert_equal 2, block.size
      assert_includes block[0], "WAITING Travel desk reply"
      assert_includes block[1], "Some note line."
    end
  end

  def test_block_stops_at_next_headline
    with_store do |store, _o, _a|
      flight = find_item(store, "Book flight")
      block = store.block(flight)
      assert_equal 2, block.size # headline + DEADLINE stamp
      refute block.any? { |l| l.include?("Review PR") }
    end
  end

  def test_block_rejects_stale_line_numbers
    with_store do |store, _o, _a|
      stale = find_item(store, "Book flight").dup
      stale.line = 1
      assert_empty store.block(stale)
    end
  end

  def test_archive_with_nothing_to_do
    with_store do |store, _org, archive|
      store.archive_swept!
      assert_equal 0, store.archive_swept!
      assert_equal 1, File.read(archive).scan(/# Archived/).size
    end
  end
end
