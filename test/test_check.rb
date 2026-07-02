# frozen_string_literal: true

require_relative "test_helper"
require "tasks/check"

class TestCheck < Minitest::Test
  C = Tasks::Check

  def check_content(content)
    Dir.mktmpdir do |dir|
      path = File.join(dir, "gtd.org")
      File.write(path, content)
      return C.check(path)
    end
  end

  def test_fixture_is_clean
    res = check_content(FIXTURE_ORG)
    assert res.ok?, "fixture should be clean, got: #{res.errors.inspect}"
    assert_empty res.warnings
  end

  def test_example_gtd_org_is_clean
    res = C.check(File.expand_path("../examples/gtd.org", __dir__))
    assert res.ok?, res.errors.inspect
  end

  def test_missing_file
    res = C.check("/nonexistent/gtd.org")
    refute res.ok?
    assert_match(/not found/, res.errors[0][1])
  end

  def test_typoed_state_keyword
    res = check_content("* Work\n** TOOD fix the thing\n")
    refute res.ok?
    line, msg = res.errors[0]
    assert_equal 2, line
    assert_match(/unknown state keyword "TOOD"/, msg)
  end

  def test_section_headings_are_not_flagged
    res = check_content("* Inbox\n* Next Actions\n** NEXT do a thing\n")
    assert res.ok?, res.errors.inspect
  end

  def test_invalid_priority_cookie
    res = check_content("* W\n** NEXT [#D] wrong priority\n")
    refute res.ok?
    assert_match(/invalid priority cookie \[#D\]/, res.errors[0][1])
  end

  def test_orphan_metadata
    res = check_content("* Work\n   DEADLINE: <2026-07-02>\n")
    refute res.ok?
    assert_match(/no task headline above/, res.errors[0][1])
  end

  def test_top_level_metadata_after_section
    res = check_content("* Work\nDEADLINE: <2026-07-02>\n")
    refute res.ok?
    assert_match(/lost its task headline/, res.errors[0][1])
  end

  def test_malformed_stamp
    res = check_content("* W\n** TODO a task\n   DEADLINE: 2026-07-02\n")
    refute res.ok?
    assert_match(/expects <YYYY-MM-DD/, res.errors[0][1])
  end

  def test_impossible_date
    res = check_content("* W\n** TODO a task\n   DEADLINE: <2026-02-30>\n")
    refute res.ok?
    assert_match(/not a real date/, res.errors[0][1])
  end

  def test_malformed_closed_stamp
    res = check_content("* W\n** DONE a task\n   CLOSED: <2026-07-01>\n")
    refute res.ok?
    assert_match(/CLOSED: expects \[YYYY-MM-DD\]/, res.errors[0][1])
  end

  def test_duplicate_open_titles_warn
    res = check_content("* W\n** TODO pay the bill\n** NEXT pay the bill\n")
    assert res.ok?, "duplicates are a warning, not an error"
    assert_match(/duplicate open title/, res.warnings[0][1])
  end

  def test_duplicate_done_titles_do_not_warn
    res = check_content("* W\n** DONE pay the bill\n   CLOSED: [2026-06-01]\n** DONE pay the bill\n   CLOSED: [2026-06-08]\n")
    assert_empty res.warnings
  end

  def test_json_shape
    h = check_content("* W\n** TOOD x\n").to_h
    refute h[:ok]
    assert_equal 2, h[:errors][0][:line]
    assert_kind_of String, h[:errors][0][:message]
  end

  def test_all_store_mutations_leave_file_check_clean
    with_store do |store, org, _archive|
      store.complete!(find_item(store, "Book flight"))
      store.reschedule!(find_item(store, "garden"), Date.new(2026, 7, 10))
      store.set_priority!(find_item(store, "Water the plants"), "B")
      store.archive_swept!
      store.undo!
      store.redo!
      res = C.check(org)
      assert res.ok?, res.errors.inspect
    end
  end
end
