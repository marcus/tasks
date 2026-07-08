# frozen_string_literal: true

require_relative "test_helper"
require "tasks/check"
require "tasks/format"
require "tasks/migrate"

class TestCheck < Minitest::Test
  C = Tasks::Check

  # Serialize records to a jsonl file and check it.
  def check_records(records)
    Dir.mktmpdir do |dir|
      path = File.join(dir, "tasks.jsonl")
      File.write(path, Tasks::Format.dump(records))
      return C.check(path)
    end
  end

  def meta = { "type" => "meta", "version" => 1 }
  def section(id, title, **extra) = { "type" => "section", "id" => id, "title" => title, **extra }
  def task(id, parent, state, title, **extra)
    { "type" => "task", "id" => id, "parent" => parent, "state" => state, "title" => title, **extra }
  end

  def test_fixture_is_clean
    res = check_records(FIXTURE_RECORDS)
    assert res.ok?, "fixture should be clean, got: #{res.errors.inspect}"
    assert_empty res.warnings
  end

  # The example org file, migrated to jsonl, must pass the new Check — the
  # importer and the linter agree on the schema.
  def test_migrated_example_is_clean
    Dir.mktmpdir do |dir|
      FileUtils.cp(File.expand_path("../examples/gtd.org", __dir__), File.join(dir, "gtd.org"))
      assert Tasks::Migrate.run([], default_dir: dir, out: StringIO.new, err: StringIO.new)
      res = C.check(File.join(dir, "tasks.jsonl"))
      assert res.ok?, res.errors.inspect
    end
  end

  def test_missing_file
    res = C.check("/nonexistent/tasks.jsonl")
    refute res.ok?
    assert_match(/not found/, res.errors[0][1])
  end

  def test_missing_meta_on_line_one
    Dir.mktmpdir do |dir|
      path = File.join(dir, "tasks.jsonl")
      File.write(path, %({"type":"section","id":"aaaa0001","title":"Inbox"}\n))
      res = C.check(path)
      refute res.ok?
      assert_match(/line 1 must be a meta record/, res.errors.map { |_l, m| m }.join)
    end
  end

  def test_unsupported_meta_version
    res = check_records([{ "type" => "meta", "version" => 99 }])
    refute res.ok?
    assert_match(/unsupported meta version/, res.errors[0][1])
  end

  def test_invalid_state
    res = check_records([meta, section("aaaa0001", "Work"),
                         task("aaaa0002", "aaaa0001", "TOOD", "fix the thing")])
    refute res.ok?
    assert_match(/invalid state "TOOD"/, res.errors.map { |_l, m| m }.join)
  end

  def test_sections_are_not_flagged
    res = check_records([meta, section("aaaa0001", "Inbox"), section("aaaa0002", "Next Actions"),
                         task("aaaa0003", "aaaa0002", "NEXT", "do a thing")])
    assert res.ok?, res.errors.inspect
  end

  def test_invalid_priority
    res = check_records([meta, section("aaaa0001", "W"),
                         task("aaaa0002", "aaaa0001", "NEXT", "wrong priority", "priority" => "D")])
    refute res.ok?
    assert_match(/invalid priority "D"/, res.errors.map { |_l, m| m }.join)
  end

  def test_parent_must_resolve_to_an_earlier_record
    res = check_records([meta, task("aaaa0002", "nope0000", "NEXT", "orphan")])
    refute res.ok?
    assert_match(/does not resolve to an earlier record/, res.errors.map { |_l, m| m }.join)
  end

  def test_dfs_pre_order_violation
    # A grandchild that appears before its parent's subtree closes, out of order:
    # child2 claims child1 as parent, but child1's subtree already closed.
    recs = [meta, section("aaaa0001", "W"),
            task("aaaa0002", "aaaa0001", "NEXT", "child1"),
            task("aaaa0003", "aaaa0001", "NEXT", "sibling"),
            task("aaaa0004", "aaaa0002", "NEXT", "grandchild out of order")]
    res = check_records(recs)
    refute res.ok?
    assert_match(/breaks DFS pre-order/, res.errors.map { |_l, m| m }.join)
  end

  def test_malformed_date
    res = check_records([meta, section("aaaa0001", "W"),
                         task("aaaa0002", "aaaa0001", "TODO", "a task", "deadline" => "07-02")])
    refute res.ok?
    assert_match(/is not a YYYY-MM-DD date/, res.errors.map { |_l, m| m }.join)
  end

  def test_impossible_date
    res = check_records([meta, section("aaaa0001", "W"),
                         task("aaaa0002", "aaaa0001", "TODO", "a task", "deadline" => "2026-02-30")])
    refute res.ok?
    assert_match(/not a real date/, res.errors.map { |_l, m| m }.join)
  end

  def test_closed_on_open_task
    res = check_records([meta, section("aaaa0001", "W"),
                         task("aaaa0002", "aaaa0001", "TODO", "a task", "closed" => "2026-07-01")])
    refute res.ok?
    assert_match(/closed date on an open task/, res.errors.map { |_l, m| m }.join)
  end

  def test_invalid_recur_cookie
    res = check_records([meta, section("aaaa0001", "W"),
                         task("aaaa0002", "aaaa0001", "TODO", "a task",
                              "deadline" => "2026-07-01", "recur" => "soon")])
    refute res.ok?
    assert_match(/invalid recur cookie/, res.errors.map { |_l, m| m }.join)
  end

  def test_section_must_not_carry_task_fields
    res = check_records([meta, section("aaaa0001", "W", "state" => "NEXT")])
    refute res.ok?
    assert_match(/section must not carry "state"/, res.errors.map { |_l, m| m }.join)
  end

  def test_duplicate_ids_are_an_error
    res = check_records([meta, section("aaaa0001", "W"),
                         task("aaaa0002", "aaaa0001", "NEXT", "one"),
                         task("aaaa0002", "aaaa0001", "NEXT", "two")])
    refute res.ok?
    assert_match(/duplicate id/, res.errors.map { |_l, m| m }.join)
  end

  def test_malformed_id_is_an_error
    res = check_records([meta, section("aaaa0001", "W"),
                         task("nothex!", "aaaa0001", "NEXT", "bad id")])
    refute res.ok?
    assert_match(/malformed id/, res.errors.map { |_l, m| m }.join)
  end

  def test_unknown_key_warns
    res = check_records([meta, section("aaaa0001", "W"),
                         task("aaaa0002", "aaaa0001", "NEXT", "t", "colour" => "blue")])
    assert res.ok?, "unknown keys are a warning, not an error"
    assert_match(/unknown key "colour"/, res.warnings.map { |_l, m| m }.join)
  end

  def test_duplicate_open_titles_warn
    res = check_records([meta, section("aaaa0001", "W"),
                         task("aaaa0002", "aaaa0001", "TODO", "pay the bill"),
                         task("aaaa0003", "aaaa0001", "NEXT", "pay the bill")])
    assert res.ok?, "duplicates are a warning, not an error"
    assert_match(/duplicate open title/, res.warnings.map { |_l, m| m }.join)
  end

  def test_duplicate_done_titles_do_not_warn
    res = check_records([meta, section("aaaa0001", "W"),
                         task("aaaa0002", "aaaa0001", "DONE", "pay the bill", "closed" => "2026-06-01"),
                         task("aaaa0003", "aaaa0001", "DONE", "pay the bill", "closed" => "2026-06-08")])
    assert_empty res.warnings
  end

  def test_unparseable_line_folds_in_as_error
    Dir.mktmpdir do |dir|
      path = File.join(dir, "tasks.jsonl")
      File.write(path, %({"type":"meta","version":1}\nthis is not json\n))
      res = C.check(path)
      refute res.ok?
      assert_match(/invalid JSON/, res.errors.map { |_l, m| m }.join)
    end
  end

  def test_json_shape
    h = check_records([meta, section("aaaa0001", "W"),
                       task("aaaa0002", "aaaa0001", "TOOD", "x")]).to_h
    refute h[:ok]
    assert_kind_of Integer, h[:errors][0][:line]
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
