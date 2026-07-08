# frozen_string_literal: true

require_relative "test_helper"
require "tasks/format"
require "open3"
require "json"

# Coverage for stable task ids: parsing, minting on capture, ensure_id! as a
# repair path, and — the payoff — mutations locating a task by its id so a line
# shift or an out-of-band retitle can't misfire.
class TestIds < Minitest::Test
  # A store whose one task deliberately lacks an id (the repair case — every
  # migrated record normally has one).
  def with_idless_store
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      File.write(org, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "section", "id" => "aaaa0001", "title" => "Work" },
        { "type" => "task", "parent" => "aaaa0001", "state" => "NEXT", "title" => "Ship it",
          "deadline" => "2026-07-10" },
      ]))
      yield Tasks::Store.new(org: org, archive: File.join(dir, "archive.jsonl")), org
    end
  end

  # -- parsing -----------------------------------------------------------------

  def test_parses_id_from_record
    with_store do |store, _o, _a|
      assert_equal FIX[:flight], find_item(store, "Book flight").id
    end
  end

  def test_record_without_id_parses_to_nil_id
    with_idless_store do |store, _org|
      assert_nil store.items.find { |i| i.title == "Ship it" }.id
    end
  end

  # -- capture mints a fresh, unique id ---------------------------------------

  def test_capture_assigns_a_unique_id
    with_store do |store, org, _a|
      store.capture!("first thing")
      store.capture!("second thing")
      ids = store.items.select { |i| ["first thing", "second thing"].include?(i.title) }.map(&:id)
      assert_equal 2, ids.compact.size, "both captures carry an id"
      assert_equal ids, ids.uniq, "ids are distinct"
      assert(ids.all? { |id| id =~ /\A[0-9a-f]{8}\z/ })
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_generated_id_avoids_an_archived_id
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, FIXTURE)
      # An archived task already owns this id — a fresh mint must avoid it.
      File.write(archive, dump_fixture([
        { "type" => "meta", "version" => 1 },
        { "type" => "task", "id" => "dddd9999", "state" => "DONE", "title" => "Old thing",
          "closed" => "2026-01-01", "archived" => "2026-01-02" },
      ]))
      store = Tasks::Store.new(org: org, archive: archive)
      seq = ["dddd9999", "eeee0001"] # first hex collides with the archive
      SecureRandom.stub(:hex, ->(*) { seq.shift }) do
        store.capture!("new task")
      end
      assert_equal "eeee0001", store.items.find { |i| i.title == "new task" }.id
    end
  end

  # -- ensure_id! (repair) -----------------------------------------------------

  def test_ensure_id_assigns_then_is_idempotent
    with_idless_store do |store, _org|
      ship = store.items.find { |i| i.title == "Ship it" }
      id = store.ensure_id!(ship)
      assert_match(/\A[0-9a-f]{8}\z/, id)

      again = store.ensure_id!(store.items.find { |i| i.title == "Ship it" })
      assert_equal id, again, "second call returns the same id"
    end
  end

  def test_ensure_id_on_already_ided_task_does_not_rewrite
    with_store do |store, org, _a|
      before = File.read(org)
      store.ensure_id!(find_item(store, "Book flight")) # already has one
      assert_equal before, File.read(org), "no write when the id already exists"
    end
  end

  def test_undo_of_id_assignment_removes_the_id
    with_idless_store do |store, org|
      before = File.read(org)
      store.ensure_id!(store.items.find { |i| i.title == "Ship it" })
      refute_equal before, File.read(org)
      assert_equal :ok, store.undo!.first
      assert_equal before, File.read(org), "undo strips the id it added"
    end
  end

  # -- the payoff: locate by id survives drift ---------------------------------

  def test_mutation_locates_by_id_after_lines_shift
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight") # carries id + its current line
      # An out-of-band edit inserts records above it, invalidating flight.line.
      prefix = dump_fixture([
        { "type" => "section", "id" => "bbbb0001", "title" => "Zzz Section" },
        { "type" => "task", "id" => "bbbb0002", "parent" => "bbbb0001", "state" => "TODO",
          "title" => "decoy task" },
      ])
      File.write(org, %({"type":"meta","version":1}\n) +
                 prefix + FIXTURE.sub(%({"type":"meta","version":1}\n), ""))
      assert store.set_priority!(flight, "C"), "stale line, but the id still finds it"
      assert_equal "C", find_item(store, "Book flight").priority
      assert_equal "TODO", find_item(store, "decoy task").state, "decoy untouched"
    end
  end

  def test_mutation_locates_by_id_after_external_retitle
    with_store do |store, org, _a|
      flight = find_item(store, "Book flight")
      # The title (flight's fallback guard) changes out from under us...
      File.write(org, File.read(org).sub("Book flight in Concur", "Book a flight RENAMED"))
      # ...but the id still resolves it, where the title-substring guard wouldn't.
      assert store.set_priority!(flight, "C")
      assert_equal "C", find_item(store, "RENAMED").priority
    end
  end

  def test_id_survives_move_and_archive
    with_store do |store, org, _archive|
      id = find_item(store, "Book flight").id
      store.move!(find_item(store, "Book flight"), "Home")
      assert_equal id, find_item(store, "Book flight").id, "id rides along on move"

      store.complete!(find_item(store, "Book flight"))
      store.archive_swept!
      archived = store.archive_items.find { |i| i.title.include?("Book flight") }
      assert_equal id, archived.id, "id follows the subtree into the archive"
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- CLI end-to-end ----------------------------------------------------------

  BIN = File.expand_path("../bin/tasks", __dir__)

  def cli(dir, *args)
    env = { "TASKS_FILE" => File.join(dir, "tasks.jsonl"),
            "TASKS_ARCHIVE" => File.join(dir, "archive.jsonl"),
            "XDG_STATE_HOME" => File.join(dir, "state") }
    out, err, st = Open3.capture3(env, "ruby", BIN, *args)
    [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), st]
  end

  def test_cli_id_command_and_ref_resolution
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE)

      out, _e, st = cli(dir, "id", "Book flight")
      assert st.success?
      id = out.lines.first.strip
      assert_match(/\A[0-9a-f]{8}\z/, id)

      # `id` is idempotent across invocations (shared file).
      out2, _e, = cli(dir, "id", "Book flight")
      assert_equal id, out2.lines.first.strip

      # The id resolves as a ref, unambiguously.
      show, _e, st = cli(dir, "show", id)
      assert st.success?
      assert_match(/Book flight/, show)
      assert_match(/id:\s+#{id}/, show)

      # A mutation by id works too.
      _o, _e, st = cli(dir, "done", id)
      assert st.success?
      assert_equal "DONE", record_for(File.join(dir, "tasks.jsonl"), title: "Book flight in Concur")["state"]
    end
  end

  def test_cli_show_json_includes_id
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "tasks.jsonl"), FIXTURE)
      out, _e, st = cli(dir, "show", "Travel desk", "--json")
      assert st.success?
      doc = JSON.parse(out)
      assert_match(/\A[0-9a-f]{8}\z/, doc["id"])
      assert_equal ["Some note line."], doc["notes"]
    end
  end
end
