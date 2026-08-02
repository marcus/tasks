# frozen_string_literal: true

require_relative "test_helper"
require "json"
require "open3"
require "tmpdir"

# `tasks repair` — the converging store repair.
#
# The bug class (td-d6ed92, td-2addce): `Check` refuses the WHOLE file, every
# mutation pre- or post-flights `Check` over the whole file, and the two repairs
# the codebase documents — `ensure_id!` minting one id, `Format`'s "dropped on
# the next write" for an unknown key inside a temporal object — are each a
# RECORD repair. So with two or more instances of either defect the file never
# validates, every attempt refuses or rolls back, and the store is readable but
# unrepairable except by hand.
#
# This suite proves three things, and the third is as load-bearing as the first:
#
#   1. `repair` converges a store carrying n>=2 of BOTH defects at once, in one
#      pass, after which `check` passes and an ordinary mutation succeeds.
#   2. It refuses — writing nothing — when it meets a defect it does not know.
#   3. NOTHING ELSE CHANGED. The two refusal wordings a mutation can earn are
#      how a port proves it kept the preflight case and the rolled-back case
#      distinct, so they are asserted here against the exact stores the porting
#      fixtures record them on.
class TestRepair < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)

  META = { "type" => "meta", "version" => 2 }.freeze
  SECTION = { "type" => "section", "id" => "1e000001", "title" => "Next Actions" }.freeze

  # porting/fixtures/malformed/missing-ids-many: three id-less records, one
  # record with an id between them.
  MISSING_IDS_MANY = Tasks::Format.dump([
    META, SECTION,
    { "type" => "task", "parent" => "1e000001", "state" => "NEXT",
      "title" => "First hand-appended task with no id" },
    { "type" => "task", "parent" => "1e000001", "state" => "TODO",
      "title" => "Second hand-appended task with no id" },
    { "type" => "task", "id" => "1e000002", "parent" => "1e000001", "state" => "TODO",
      "title" => "Renew the domain registration" },
    { "type" => "task", "parent" => "1e000001", "state" => "TODO",
      "title" => "Third hand-appended task with no id" },
  ]).freeze

  # The two temporal records are written as RAW JSON, not through Format.dump —
  # `dump_record` drops exactly the keys these fixtures exist to carry, since
  # `scheduled_time`/`deadline_time` are deliberately absent from
  # NESTED_FORWARD_COMPAT. That the schema writer cannot express this damage is
  # the whole point: only a foreign writer or a hand edit produces it, and until
  # `repair` nothing could undo it.
  UNKNOWN_SCHEDULED_KEY = <<~'LINE'.chomp
    {"type":"task","id":"a1000001","parent":"1e000001","state":"TODO","title":"Start time carrying an unknown nested key","scheduled":"2026-06-16","scheduled_time":{"local":"08:30","timezone":"Europe/London","precision":"minute"},"updated":"2026-01-02T03:04:05Z#fixture"}
  LINE
  UNKNOWN_DEADLINE_KEY = <<~'LINE'.chomp
    {"type":"task","id":"a1000002","parent":"1e000001","state":"TODO","title":"Due time carrying an unknown nested key","deadline":"2026-06-18","deadline_time":{"local":"17:00","calendar_uid":"abc"},"updated":"2026-01-03T03:04:05Z#fixture"}
  LINE

  # porting/fixtures/malformed/temporal-unknown-nested-key: an unknown key
  # inside each of the two temporal objects.
  TEMPORAL_UNKNOWN_KEYS = [
    Tasks::Format.dump_record(META), Tasks::Format.dump_record(SECTION),
    UNKNOWN_SCHEDULED_KEY, UNKNOWN_DEADLINE_KEY, ""
  ].join("\n").freeze

  # The convergence fixture: n>=2 of BOTH defects, interleaved. Three records
  # with no id, two temporal objects carrying an unknown key. Neither defect is
  # the file's last remaining error at any point, which is exactly the state no
  # existing command can leave.
  BOTH_DEFECTS = [
    Tasks::Format.dump_record(META),
    Tasks::Format.dump_record(SECTION),
    Tasks::Format.dump_record({ "type" => "task", "parent" => "1e000001", "state" => "NEXT",
                                "title" => "No id one" }),
    Tasks::Format.dump_record({ "type" => "task", "parent" => "1e000001", "state" => "TODO",
                                "title" => "No id two" }),
    UNKNOWN_SCHEDULED_KEY,
    Tasks::Format.dump_record({ "type" => "task", "parent" => "1e000001", "state" => "TODO",
                                "title" => "No id three" }),
    UNKNOWN_DEADLINE_KEY,
    "",
  ].join("\n").freeze

  # Both known defects plus one this command does not know how to fix. The
  # unknown one must veto the whole pass.
  BOTH_DEFECTS_PLUS_UNKNOWN = Tasks::Format.dump([
    META, SECTION,
    { "type" => "task", "parent" => "1e000001", "state" => "NEXT", "title" => "No id one" },
    { "type" => "task", "parent" => "1e000001", "state" => "TODO", "title" => "No id two" },
    { "type" => "task", "id" => "a1000001", "parent" => "1e000001", "state" => "BOGUS",
      "title" => "A state no build knows" },
  ]).freeze

  # -- convergence ------------------------------------------------------------

  def test_repair_converges_a_store_carrying_many_instances_of_both_defects
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      before = Tasks::Check.check(org)
      refute before.ok?
      assert_equal 5, before.errors.size, "3 id-less records + 2 unknown temporal keys"

      out, err, status = cli(org, "repair")
      assert_equal 0, status.exitstatus, "repair should converge: #{err}"
      assert_equal 5, out.lines.count { |line| line.include?("fixed") }
      assert_includes out, "5 repairs written"

      assert Tasks::Check.check(org).ok?, "the store must validate after one pass"
    end
  end

  def test_an_ordinary_mutation_succeeds_after_repair
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      _out, _err, status = cli(org, "priority", "Start time carrying an unknown nested key", "A")
      refute_equal 0, status.exitstatus, "the store must be unwritable before the repair"

      assert_equal 0, cli(org, "repair").last.exitstatus

      out, err, status = cli(org, "priority", "Start time carrying an unknown nested key", "A")
      assert_equal 0, status.exitstatus, "an ordinary mutation must land afterwards: #{err}"
      assert_includes out, "Start time carrying an unknown nested key"
      assert_equal "A", record_for(org, title: "Start time carrying an unknown nested key")["priority"]
      assert Tasks::Check.check(org).ok?
    end
  end

  def test_repair_mints_a_distinct_id_for_every_id_less_record
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      assert_equal 0, cli(org, "repair").last.exitstatus
      records = Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records
      ids = records.drop(1).map { |record| record["id"] }
      assert ids.all? { |id| id =~ /\A[0-9a-f]{8}\z/ }, "every record carries a well-formed id"
      assert_equal ids.size, ids.uniq.size, "minted ids must not collide"
    end
  end

  def test_repair_drops_only_the_unknown_keys_inside_a_temporal_object
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      assert_equal 0, cli(org, "repair").last.exitstatus
      scheduled = record_for(org, title: "Start time carrying an unknown nested key")["scheduled_time"]
      assert_equal({ "local" => "08:30", "timezone" => "Europe/London" }, scheduled)
      deadline = record_for(org, title: "Due time carrying an unknown nested key")["deadline_time"]
      assert_equal({ "local" => "17:00" }, deadline)
    end
  end

  def test_repair_is_idempotent_and_a_clean_store_is_a_no_op
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      assert_equal 0, cli(org, "repair").last.exitstatus
      after_first = File.read(org, encoding: "UTF-8")

      out, _err, status = cli(org, "repair")
      assert_equal 0, status.exitstatus
      assert_includes out, "nothing to repair"
      assert_equal after_first, File.read(org, encoding: "UTF-8"), "a clean store is untouched"
    end
  end

  # -- the `updated` decision -------------------------------------------------

  # A repair asserts nothing about the task's CONTENT: it converges bytes the
  # store refuses to write over. Stamping would falsify "when this task last
  # changed" and, in the last-write-wins merge, hand the repairing device a win
  # it did not earn — for a dropped temporal key that means beating the newer
  # binary that understood the field with the copy that just discarded it. And
  # for a just-minted id, `stamp_changed_tasks!` indexes originals by id, so a
  # fresh id is in no index and the repair would be stamped as a brand-new task
  # (td-d6ed92). So: repair never writes, clears, or bumps `updated`.
  def test_repair_leaves_the_updated_stamp_exactly_as_it_found_it
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      assert_equal 0, cli(org, "repair").last.exitstatus
      assert_equal "2026-01-02T03:04:05Z#fixture",
                   record_for(org, title: "Start time carrying an unknown nested key")["updated"]
      assert_equal "2026-01-03T03:04:05Z#fixture",
                   record_for(org, title: "Due time carrying an unknown nested key")["updated"]
      refute record_for(org, title: "No id one").key?("updated"),
             "a record that never carried a stamp does not gain one from a repair"
    end
  end

  def test_an_ordinary_mutation_after_a_repair_still_stamps_normally
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      assert_equal 0, cli(org, "repair").last.exitstatus
      assert_equal 0, cli(org, "priority", "Due time carrying an unknown nested key", "B").last.exitstatus
      stamp = record_for(org, title: "Due time carrying an unknown nested key")["updated"]
      refute_equal "2026-01-03T03:04:05Z#fixture", stamp, "a real edit does bump the stamp"
      assert Tasks::UpdateStamp.valid?(stamp)
    end
  end

  # -- dry run ----------------------------------------------------------------

  def test_dry_run_reports_every_fix_and_writes_nothing
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      before = File.read(org, encoding: "UTF-8")
      out, _err, status = cli(org, "repair", "--dry-run")
      assert_equal 0, status.exitstatus
      assert_equal 5, out.lines.count { |line| line.include?("would fix") }
      assert_includes out, "nothing was written"
      assert_equal before, File.read(org, encoding: "UTF-8"), "byte-identical after a dry run"
    end
  end

  def test_dry_run_json_reports_the_plan_without_a_minted_id
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      out, _err, status = cli(org, "repair", "--dry-run", "--json")
      assert_equal 0, status.exitstatus
      payload = JSON.parse(out)
      assert_equal "repair", payload.fetch("action")
      assert payload.fetch("dry_run")
      refute payload.fetch("written")
      assert_equal 5, payload.fetch("fixes").size
      # A dry run mints ids only to prove the file would validate, then throws
      # them away. Publishing one would invite a caller to record an id no
      # record will ever carry.
      assert(payload.fetch("fixes").none? { |fix| fix.key?("id") })
      assert_equal %w[dropped_temporal_keys minted_id],
                   payload.fetch("fixes").map { |fix| fix.fetch("kind") }.uniq.sort
    end
  end

  def test_json_reports_the_minted_ids_it_actually_wrote
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      out, _err, status = cli(org, "repair", "--json")
      assert_equal 0, status.exitstatus
      payload = JSON.parse(out)
      assert payload.fetch("written")
      minted = payload.fetch("fixes").select { |fix| fix.fetch("kind") == "minted_id" }
      assert_equal 3, minted.size
      on_disk = Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records.map { |r| r["id"] }
      minted.each { |fix| assert_includes on_disk, fix.fetch("id") }
    end
  end

  # -- refusal ----------------------------------------------------------------

  def test_repair_refuses_a_defect_it_does_not_know_and_writes_nothing
    with_sandbox(BOTH_DEFECTS_PLUS_UNKNOWN) do |org, _archive|
      before = File.read(org, encoding: "UTF-8")
      out, err, status = cli(org, "repair")
      assert_equal 1, status.exitstatus
      assert_includes err, "nothing was written"
      assert_includes out, "invalid state"
      # The verb is hypothetical, because nothing happened.
      assert_equal 2, out.lines.count { |line| line.include?("can fix") }
      assert_equal before, File.read(org, encoding: "UTF-8"),
                   "a refused pass must never leave a partially repaired file"
    end
  end

  def test_a_refused_repair_emits_a_json_error_envelope
    with_sandbox(BOTH_DEFECTS_PLUS_UNKNOWN) do |org, _archive|
      out, _err, status = cli(org, "repair", "--json")
      assert_equal 1, status.exitstatus
      payload = JSON.parse(out)
      assert_equal "unrepairable", payload.fetch("error")
      assert_equal "repair", payload.fetch("action")
      refute payload.fetch("ok")
      refute payload.fetch("written")
      assert_equal 1, payload.fetch("blockers").size
      assert_match(/invalid state/, payload.fetch("blockers").first.fetch("message"))
    end
  end

  # Raw safety, the same rule the targeted repair follows: a line this binary
  # cannot parse is a line it must not rewrite the file without. Check folds
  # Format's parse errors in, so the blocker is structural rather than a special
  # case — but the consequence is worth pinning, because writing here would
  # silently DELETE the unreadable line.
  def test_repair_refuses_a_file_with_an_unparseable_line
    content = "#{MISSING_IDS_MANY}not json at all\n"
    with_sandbox(content) do |org, _archive|
      _out, err, status = cli(org, "repair")
      assert_equal 1, status.exitstatus
      assert_includes err, "nothing was written"
      assert_equal content, File.read(org, encoding: "UTF-8"), "the unreadable line survives"
    end
  end

  def test_repair_refuses_a_store_whose_schema_version_this_build_cannot_read
    content = Tasks::Format.dump(
      [{ "type" => "meta", "version" => 99 }] +
      Tasks::Format.parse(MISSING_IDS_MANY).records.drop(1)
    )
    with_sandbox(content) do |org, _archive|
      _out, err, status = cli(org, "repair")
      assert_equal 1, status.exitstatus
      assert_match(/unsupported meta version/, err)
      assert_equal content, File.read(org, encoding: "UTF-8")
    end
  end

  # -- the archive ------------------------------------------------------------

  # `post_write_failure` validates BOTH files, so an id-less record in the
  # archive wedges every mutation exactly as one in the live file does. A
  # live-only repair would be the same dead end one file over.
  def test_repair_converges_the_archive_too
    archive_content = Tasks::Format.dump([
      META,
      { "type" => "task", "parent" => nil, "state" => "DONE", "title" => "swept one",
        "closed" => "2026-01-01", "archived" => "2026-01-02" },
      { "type" => "task", "parent" => nil, "state" => "DONE", "title" => "swept two",
        "closed" => "2026-01-01", "archived" => "2026-01-02" },
    ])
    with_sandbox(BOTH_DEFECTS, archive: archive_content) do |org, archive|
      out, err, status = cli(org, "repair")
      assert_equal 0, status.exitstatus, err
      assert_includes out, "archive.jsonl"
      assert Tasks::Check.check(org).ok?
      assert Tasks::Check.check(archive).ok?
      live_ids = Tasks::Format.parse(File.read(org, encoding: "UTF-8")).records.map { |r| r["id"] }
      archive_ids = Tasks::Format.parse(File.read(archive, encoding: "UTF-8")).records.map { |r| r["id"] }
      assert_empty(live_ids.compact & archive_ids.compact,
                   "ids mint from one pool spanning both files")
    end
  end

  # -- undo -------------------------------------------------------------------

  # A repair's BEFORE-state is invalid bytes the user deliberately asked to fix.
  # Undo normally refuses to restore a state that fails today's invariants; the
  # journal's `repair` flag is the documented exception, and a converging repair
  # earns it for the same reason a targeted one does.
  def test_undo_restores_the_malformed_bytes
    with_sandbox(BOTH_DEFECTS) do |org, _archive|
      before = File.read(org, encoding: "UTF-8")
      assert_equal 0, cli(org, "repair").last.exitstatus
      out, err, status = cli(org, "undo")
      assert_equal 0, status.exitstatus, err
      assert_includes out, "repair store"
      assert_equal before, File.read(org, encoding: "UTF-8")
    end
  end

  # -- NOTHING ELSE CHANGED ---------------------------------------------------
  #
  # The two refusal wordings are contract for the port: they are how it proves
  # it kept "refused before writing" and "wrote and rolled back" distinct. Both
  # are asserted on the exact stores the porting fixtures record them on, and
  # each asserts the ABSENCE of the other — the failure mode is producing the
  # wrong one of the pair, which an exit-code assertion cannot see.

  def test_id_on_a_store_with_many_id_less_records_still_reports_the_rollback_wording
    with_sandbox(MISSING_IDS_MANY) do |org, _archive|
      before = File.read(org, encoding: "UTF-8")
      _out, err, status = cli(org, "id", "First hand-appended")
      assert_equal 1, status.exitstatus
      assert_includes err, "file failed validation after the edit — run `tasks check`"
      refute_includes err, "already invalid"
      assert_equal before, File.read(org, encoding: "UTF-8"), "the write was rolled back"
    end
  end

  def test_a_mutation_on_the_temporal_store_still_reports_the_preflight_wording
    with_sandbox(TEMPORAL_UNKNOWN_KEYS) do |org, _archive|
      before = File.read(org, encoding: "UTF-8")
      _out, err, status = cli(org, "priority", "a1000002", "A")
      assert_equal 1, status.exitstatus
      assert_includes err, "task file is already invalid — run `tasks check` (nothing was written)"
      refute_includes err, "failed validation after the edit"
      assert_equal before, File.read(org, encoding: "UTF-8")
    end
  end

  # `check` is the diagnostic both refusals send you to, so its verdict on these
  # two stores is contract as much as the refusals are.
  def test_check_still_reports_exactly_what_the_fixtures_record
    with_sandbox(MISSING_IDS_MANY) do |org, _archive|
      out, _err, status = cli(org, "check")
      assert_equal 1, status.exitstatus
      assert_includes out, "line 3: record missing id"
      assert_includes out, "line 4: record missing id"
      assert_includes out, "line 6: record missing id"
      assert_includes out, "3 error(s), 0 warning(s)"
    end
    with_sandbox(TEMPORAL_UNKNOWN_KEYS) do |org, _archive|
      out, _err, status = cli(org, "check")
      assert_equal 1, status.exitstatus
      assert_includes out, "line 3: scheduled_time has unknown keys: precision"
      assert_includes out, "line 4: deadline_time has unknown keys: calendar_uid"
      assert_includes out, "2 error(s), 0 warning(s)"
    end
  end

  # `ensure_id!` still mints for the single-record case and still has NO
  # preflight — adding one "looks like a harmless optimization" and would turn
  # the rollback wording above into the preflight wording (td-d6ed92).
  def test_id_still_repairs_a_store_with_exactly_one_id_less_record
    content = Tasks::Format.dump([
      META, SECTION,
      { "type" => "task", "parent" => "1e000001", "state" => "NEXT",
        "title" => "First hand-appended task with no id" },
    ])
    with_sandbox(content) do |org, _archive|
      out, err, status = cli(org, "id", "First hand-appended")
      assert_equal 0, status.exitstatus, err
      assert_match(/\A[0-9a-f]{8}$/, out.lines.first.to_s.strip)
      assert Tasks::Check.check(org).ok?
    end
  end

  # -- the Store surface ------------------------------------------------------

  def test_store_repair_is_reachable_without_the_cli
    with_sandbox(BOTH_DEFECTS) do |org, archive|
      store = Tasks::Store.new(org: org, archive: archive,
                               journal_dir: File.join(File.dirname(org), "journal"))
      preview = store.repair!(dry_run: true)
      assert preview.ok?
      refute preview.written?
      assert preview.dry_run?
      assert_equal 5, preview.fixes.size

      result = store.repair!
      assert result.ok?
      assert result.written?
      assert Tasks::Check.check(org).ok?
    end
  end

  private

  def with_sandbox(content, archive: nil)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive_path = File.join(dir, "archive.jsonl")
      File.write(org, content, encoding: "UTF-8")
      File.write(archive_path, archive, encoding: "UTF-8") if archive
      yield org, archive_path
    end
  end

  def cli(org, *args)
    dir = File.dirname(org)
    env = {
      "TASKS_FILE" => org,
      "TASKS_ARCHIVE" => File.join(dir, "archive.jsonl"),
      "XDG_CONFIG_HOME" => File.join(dir, "xdg"),
      "XDG_STATE_HOME" => File.join(dir, "state"),
    }
    out, err, status = Open3.capture3(env, "ruby", BIN, *args)
    [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), status]
  end
end
