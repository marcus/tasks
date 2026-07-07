# frozen_string_literal: true

require_relative "test_helper"
require "open3"
require "json"

# Coverage for stable task IDs (A2): PROPERTIES-drawer parsing, id generation,
# capture stamping, ensure_id!, and — the payoff — mutations locating a task by
# its id so a line shift or an out-of-band retitle can't misfire.
class TestIds < Minitest::Test
  # -- parsing -----------------------------------------------------------------

  def test_parses_id_from_properties_drawer
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * Work
        ** NEXT Ship it
           :PROPERTIES:
           :ID: abc12345
           :END:
           DEADLINE: <2026-07-10 Fri>
      ORG
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      item = store.items.find { |i| i.title == "Ship it" }
      assert_equal "abc12345", item.id
      assert_equal Date.new(2026, 7, 10), item.deadline, "drawer doesn't disturb stamp parsing"
    end
  end

  def test_item_without_drawer_has_nil_id
    with_store do |store, _o, _a|
      assert_nil find_item(store, "Book flight").id
    end
  end

  # -- capture stamps a fresh, unique id ---------------------------------------

  def test_capture_assigns_a_unique_id
    with_store do |store, org, _a|
      store.capture!("first thing")
      store.capture!("second thing")
      ids = store.items.select { |i| i.title.include?("thing") }.map(&:id)
      assert_equal 2, ids.compact.size, "both captures carry an id"
      assert_equal ids, ids.uniq, "ids are distinct"
      assert Tasks::Check.check(org).ok?, "captured drawers keep the file valid org"
    end
  end

  def test_captured_drawer_sits_after_planning_lines
    with_store do |store, org, _a|
      store.capture!("dated", due: Date.new(2026, 7, 20))
      block = store.block(store.items.find { |i| i.title == "dated" })
      # org order: headline, planning (DEADLINE), then the PROPERTIES drawer
      dl  = block.index { |l| l.include?("DEADLINE:") }
      prop = block.index { |l| l.include?(":PROPERTIES:") }
      assert dl && prop && dl < prop, "DEADLINE must precede the drawer (valid org)"
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- ensure_id! --------------------------------------------------------------

  def test_ensure_id_assigns_then_is_idempotent
    with_store do |store, _o, _a|
      flight = find_item(store, "Book flight")
      id = store.ensure_id!(flight)
      assert_match(/\A[0-9a-f]{8}\z/, id)

      again = store.ensure_id!(find_item(store, "Book flight"))
      assert_equal id, again, "second call returns the same id"
    end
  end

  def test_ensure_id_on_already_ided_task_does_not_rewrite
    with_store do |store, org, _a|
      store.ensure_id!(find_item(store, "Book flight"))
      before = File.read(org)
      store.ensure_id!(find_item(store, "Book flight")) # already has one
      assert_equal before, File.read(org), "no write when the id already exists"
    end
  end

  # -- mutations stamp an id on first touch ------------------------------------

  def test_mutation_stamps_an_id_on_an_unided_task
    with_store do |store, org, _a|
      assert_nil find_item(store, "Water the plants").id
      store.set_priority!(find_item(store, "Water the plants"), "B")
      assert find_item(store, "Water the plants").id, "touching a task gives it an id"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_parent_id_is_not_taken_from_a_child_subtask
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * Work
        ** NEXT Parent task
        *** NEXT Child task
           :PROPERTIES:
           :ID: child123
           :END:
      ORG
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      parent = store.items.find { |i| i.title == "Parent task" }
      id = store.ensure_id!(parent)
      refute_equal "child123", id, "parent must get its OWN id, not the child's"
      # child's id is untouched; parent now has a distinct one
      assert_equal "child123", store.items.find { |i| i.title == "Child task" }.id
      assert_equal id, store.items.find { |i| i.title == "Parent task" }.id
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- the payoff: locate by id survives drift ---------------------------------

  def test_mutation_locates_by_id_after_lines_shift
    with_store do |store, org, _a|
      store.ensure_id!(find_item(store, "Book flight"))
      flight = find_item(store, "Book flight") # carries id + its current line
      # An out-of-band edit inserts headings above it, invalidating flight.line.
      File.write(org, "* Zzz Section\n** TODO decoy task\n" + File.read(org))
      assert store.set_priority!(flight, "C"), "stale line, but the id still finds it"
      assert_equal "C", find_item(store, "Book flight").priority
      assert_equal "TODO", find_item(store, "decoy task").state, "decoy untouched"
    end
  end

  def test_mutation_locates_by_id_after_external_retitle
    with_store do |store, org, _a|
      store.ensure_id!(find_item(store, "Book flight"))
      flight = find_item(store, "Book flight")
      # The title (flight's fallback guard) changes out from under us...
      File.write(org, File.read(org).sub("Book flight in Concur", "Book a flight RENAMED"))
      # ...but the id still resolves it, where the title-substring guard wouldn't.
      assert store.set_priority!(flight, "C")
      assert_equal "C", find_item(store, "RENAMED").priority
    end
  end

  def test_id_survives_move_and_archive
    with_store do |store, org, archive|
      id = store.ensure_id!(find_item(store, "Book flight"))
      store.move!(find_item(store, "Book flight"), "Home")
      assert_equal id, find_item(store, "Book flight").id, "id rides along on move"

      store.complete!(find_item(store, "Book flight"))
      store.archive_swept!
      archived = store.archive_items.find { |i| i.title.include?("Book flight") }
      assert_equal id, archived.id, "id follows the block into the archive"
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_undo_of_id_assignment_removes_the_drawer
    with_store do |store, org, _a|
      before = File.read(org)
      store.ensure_id!(find_item(store, "Book flight"))
      refute_equal before, File.read(org)
      assert_equal :ok, store.undo!.first
      assert_equal before, File.read(org), "undo strips the drawer it added"
    end
  end

  # -- drawer scoping: only a task's own drawer :ID: counts --------------------

  def test_section_heading_id_is_not_attributed_to_a_task
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * Inbox
        ** TODO Buy milk
        * Work
           :PROPERTIES:
           :ID: sect0001
           :END:
      ORG
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      milk = store.items.find { |i| i.title == "Buy milk" }
      assert_nil milk.id, "a section heading's :ID: must not become a task's id"
    end
  end

  def test_bare_id_in_prose_is_not_a_task_id
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * Work
        ** TODO Migrate billing
           :ID: JIRA-1234
           Follow the runbook.
      ORG
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      item = store.items.find { |i| i.title == "Migrate billing" }
      assert_nil item.id, "an :ID: outside a PROPERTIES drawer is prose, not a task id"
      # ...and stamping gives it a REAL drawer id, distinct from the prose token.
      id = store.ensure_id!(item)
      refute_equal "JIRA-1234", id
    end
  end

  def test_id_merges_into_an_existing_non_id_drawer
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * Work
        ** NEXT Task
           :PROPERTIES:
           :CATEGORY: work
           :END:
      ORG
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      store.ensure_id!(store.items.find { |i| i.title == "Task" })
      body = File.read(org)
      assert_equal 1, body.scan(/:PROPERTIES:/).size, "must not emit a second drawer"
      assert_match(/:CATEGORY: work/, body, "existing keys are preserved")
      assert_match(/:ID: [0-9a-f]{8}/, body, "id added into the same drawer")
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_generated_id_avoids_an_archived_id
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      archive = File.join(dir, "archive.org")
      File.write(org, FIXTURE_ORG)
      # An archived task already owns this id — gen_id must not re-mint it.
      File.write(archive, "* Archived\n** DONE Old thing\n   :PROPERTIES:\n   :ID: dup00000\n   :END:\n")
      store = Tasks::Store.new(org: org, archive: archive)
      seq = ["dup00000", "fresh001"] # first hex collides with the archive
      SecureRandom.stub(:hex, ->(*) { seq.shift }) do
        store.capture!("new task")
      end
      assert_equal "fresh001", store.items.find { |i| i.title == "new task" }.id
    end
  end

  def test_check_ignores_ids_outside_task_drawers
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      # Same value on a section heading and in prose — neither is a task id, so
      # this must NOT trip the duplicate-id error.
      File.write(org, <<~ORG)
        * Work
           :PROPERTIES:
           :ID: shared01
           :END:
        ** TODO A task
           :ID: shared01
           a note
      ORG
      assert Tasks::Check.check(org).ok?, "non-task :ID: lines must not be flagged as duplicates"
    end
  end

  def test_strip_drawer_does_not_swallow_notes_after_an_unterminated_drawer
    # A malformed (no :END:) drawer must not eat the real notes that follow it.
    lines = [
      "** NEXT Task\n",
      "   :PROPERTIES:\n",
      "   :ID: deadbeef\n",
      "   Follow up with the landlord.\n",
      "   Check the bank balance first.\n",
    ]
    kept = Tasks::Store.strip_drawer(lines)
    assert(kept.any? { |l| l.include?("Follow up with the landlord") }, "prose after a broken drawer is kept")
    assert(kept.any? { |l| l.include?("Check the bank balance") })
    refute(kept.any? { |l| l.include?(":ID:") }, "the property line is still hidden")
  end

  def test_id_not_merged_into_a_non_adjacent_drawer
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      # A drawer that does NOT immediately follow the headline is not org's
      # property drawer; the id must go into a fresh adjacent one instead.
      File.write(org, <<~ORG)
        * Work
        ** NEXT Task
           a note before the drawer
           :PROPERTIES:
           :CATEGORY: work
           :END:
      ORG
      store = Tasks::Store.new(org: org, archive: File.join(dir, "archive.org"))
      store.ensure_id!(store.items.find { |i| i.title == "Task" })
      body = File.read(org)
      # the id sits in an adjacent drawer right under the headline (org-recognized)
      assert_match(/\*\* NEXT Task\n   :PROPERTIES:\n   :ID: [0-9a-f]{8}\n   :END:/, body)
    end
  end

  def test_check_warns_on_unterminated_drawer
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * Work
        ** NEXT Task
           :PROPERTIES:
           :ID: abc12345
        ** NEXT Another
      ORG
      res = Tasks::Check.check(org)
      assert(res.warnings.any? { |_l, msg| msg.include?("unterminated :PROPERTIES:") })
    end
  end

  def test_show_preserves_a_colon_word_note
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * Work
        ** NEXT Task
           :PROPERTIES:
           :ID: abc12345
           :END:
           :link: https://example.com
      ORG
      block = Tasks::Store.strip_drawer(File.readlines(org, encoding: "UTF-8"))
      refute(block.any? { |l| l.include?(":PROPERTIES:") || l.include?(":ID:") }, "drawer hidden")
      assert(block.any? { |l| l.include?(":link: https://example.com") }, "a :word: note is kept")
    end
  end

  # -- Check: duplicate ids are an error ---------------------------------------

  def test_check_flags_duplicate_ids
    Dir.mktmpdir do |dir|
      org = File.join(dir, "gtd.org")
      File.write(org, <<~ORG)
        * Work
        ** NEXT One
           :PROPERTIES:
           :ID: dup00001
           :END:
        ** NEXT Two
           :PROPERTIES:
           :ID: dup00001
           :END:
      ORG
      res = Tasks::Check.check(org)
      refute res.ok?
      assert(res.errors.any? { |_line, msg| msg.include?("duplicate :ID:") })
    end
  end

  # -- CLI end-to-end ----------------------------------------------------------

  BIN = File.expand_path("../bin/tasks", __dir__)

  def cli(dir, *args)
    env = { "TASKS_ORG" => File.join(dir, "gtd.org"),
            "TASKS_ARCHIVE" => File.join(dir, "archive.org"),
            "XDG_STATE_HOME" => File.join(dir, "state") }
    out, err, st = Open3.capture3(env, "ruby", BIN, *args)
    [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), st]
  end

  def test_cli_id_command_and_ref_resolution
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)

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
      assert_match(/DONE.*Book flight/, File.read(File.join(dir, "gtd.org")))
    end
  end

  def test_cli_show_json_includes_id_and_hides_drawer
    Dir.mktmpdir do |dir|
      File.write(File.join(dir, "gtd.org"), FIXTURE_ORG)
      cli(dir, "id", "Book flight")
      out, _e, st = cli(dir, "show", "Book flight", "--json")
      assert st.success?
      doc = JSON.parse(out)
      assert_match(/\A[0-9a-f]{8}\z/, doc["id"])
      refute(doc["notes"].any? { |n| n.include?(":PROPERTIES:") || n.include?(":ID:") },
             "drawer lines must not leak into notes")
    end
  end
end
