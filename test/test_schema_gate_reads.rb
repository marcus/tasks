# frozen_string_literal: true

require_relative "test_helper"
require "json"
require "open3"
require "tmpdir"
require "fileutils"
require "tasks/cli_commands"

# The version gate on the READ half of the CLI.
#
# The store's schema version is a refusal, not a hint: a file declaring a
# version this build does not implement is refused by `check`, by every
# mutation, by the TUI, and by the API's 503 unsupported_schema_version. The
# read commands were the hole — `list`, `show`, `next` and the rest went
# through the lenient read path, parsed a foreign store, printed it, and
# exited 0.
#
# Why that is worse than an inconsistency. v1 and v2 differ only by optional
# keys, so a v1 store printed plausible-looking output and nobody noticed. A v3
# store that RENAMED fields is the real case (two machines, two builds), and
# there `tasks list` printed "No matching tasks." and `list --json` printed
# `[]`, exit 0 — every record silently dropped, byte-identical to the answer
# for an empty store. An agent driving this CLI would conclude the user had
# nothing to do.
#
# So: one rule, every surface. These tests hold the CLI to it.
class TestSchemaGateReads < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)

  CURRENT = [
    { "type" => "meta", "version" => Tasks::Format::VERSION },
    { "type" => "section", "id" => "b0000501", "title" => "Next Actions" },
    { "type" => "task", "id" => "b0000502", "parent" => "b0000501", "state" => "NEXT",
      "title" => "Collect the dry cleaning", "tags" => ["@errands"],
      "body" => "See https://example.com/ticket/42 for context." },
  ].freeze

  # A past version this build no longer reads. Structurally it still parses as
  # v2, which is exactly why the lenient path looked harmless.
  V1 = Tasks::Format.dump(
    [{ "type" => "meta", "version" => 1 }] + CURRENT.drop(1)
  ).freeze

  # A future version that is structurally DIFFERENT: title/state/tags renamed.
  # Under the old lenient read every record here evaporated silently.
  V3 = Tasks::Format.dump([
    { "type" => "meta", "version" => 3 },
    { "type" => "section", "id" => "b0000501", "name" => "Next Actions" },
    { "type" => "task", "id" => "b0000502", "parent" => "b0000501", "status" => "NEXT",
      "name" => "Collect the dry cleaning", "labels" => ["@errands"] },
  ]).freeze

  CURRENT_TEXT = Tasks::Format.dump(CURRENT).freeze
  ARCHIVE_CURRENT = Tasks::Format.dump([{ "type" => "meta", "version" => Tasks::Format::VERSION }]).freeze
  ARCHIVE_V1 = Tasks::Format.dump([{ "type" => "meta", "version" => 1 }]).freeze

  # Every read command that used to parse and print a foreign store. `list` is
  # listed three times because its archive-including scopes read a second file.
  LENIENT_READS = [
    %w[list],
    %w[list -a],
    %w[list -x],
    %w[next],
    %w[agenda],
    %w[inbox],
    %w[quadrants],
    %w[show b0000502],
    %w[links b0000502],
    %w[open b0000502],
  ].freeze

  def test_every_lenient_read_refuses_a_past_version_store
    LENIENT_READS.each do |args|
      _out, err, status = run_cli(args, content: V1)
      assert_equal 1, status.exitstatus, "#{args.join(" ")} must refuse a v1 store"
      assert_match(/unsupported meta version 1 \(expected 2\)/, err, args.join(" "))
    end
  end

  def test_every_lenient_read_refuses_a_future_structurally_different_store
    LENIENT_READS.each do |args|
      out, err, status = run_cli(args, content: V3)
      assert_equal 1, status.exitstatus, "#{args.join(" ")} must refuse a v3 store"
      assert_match(/unsupported meta version 3 \(expected 2\)/, err, args.join(" "))
      refute_match(/No matching tasks/, out,
                   "#{args.join(" ")}: an unreadable store must not read as an empty one")
    end
  end

  # The precise regression, stated as the before/after it is. Both of these
  # used to exit 0 with output indistinguishable from an empty store.
  def test_a_structurally_different_store_no_longer_reads_as_an_empty_one
    out, _err, status = run_cli(%w[list], content: V3)
    assert_equal 1, status.exitstatus
    refute_equal "No matching tasks.\n", out

    out, _err, status = run_cli(%w[list --json], content: V3)
    assert_equal 1, status.exitstatus
    refute_equal "[]\n", out
    assert_equal "unsupported_schema_version", JSON.parse(out).fetch("error")
  end

  # A read refusal is the same event the API answers 503 for and the same event
  # a mutation refuses, so it says the same thing and exits the same way.
  def test_read_refusals_match_the_strict_paths_and_the_api
    read_out, read_err, read_status = run_cli(%w[show b0000502], content: V1)
    write_out, write_err, write_status = run_cli(%w[done b0000502], content: V1)

    assert_equal write_status.exitstatus, read_status.exitstatus
    assert_equal 1, read_status.exitstatus
    assert_equal write_err, read_err
    assert_empty read_out
    assert_empty write_out

    # `id` and `check` were already strict; the message is now one sentence for
    # all of them, leading with Check's own wording.
    _out, id_err, = run_cli(%w[id b0000502], content: V1)
    assert_equal read_err, id_err
    _out, check_err, check_status = run_cli(%w[check], content: V1)
    assert_equal 1, check_status.exitstatus
    assert_empty check_err

    # The API's error code, spelled the same way by the CLI.
    out, = run_cli(%w[show b0000502 --json], content: V1)
    assert_equal "unsupported_schema_version", JSON.parse(out).fetch("error")
  end

  # `check` is the one command that must still answer, because it is where
  # every other refusal sends the operator.
  def test_check_and_config_and_help_still_answer_for_an_unreadable_store
    out, _err, status = run_cli(%w[check], content: V1)
    assert_equal 1, status.exitstatus
    assert_match(/unsupported meta version 1 \(expected 2\)/, out)

    out, _err, status = run_cli(%w[check --json], content: V1)
    assert_equal 1, status.exitstatus
    payload = JSON.parse(out)
    refute payload.fetch("ok")
    assert_match(/unsupported meta version 1/, payload.fetch("errors").first.fetch("message"))

    _out, _err, status = run_cli(%w[config], content: V1)
    assert_equal 0, status.exitstatus, "config reports where the store is, not what it holds"

    _out, _err, status = run_cli(%w[help], content: V1)
    assert_equal 0, status.exitstatus
  end

  # The archive half of the gate, and the diagnostic loop it used to close.
  # A v1 archive under a current live file makes every command refuse the whole
  # store — and default `check` used to answer "ok — no structural errors",
  # because it lints only tasks.jsonl. The refusal said "run `tasks check`";
  # `tasks check` said the store was fine. Only `--all-files` disagreed.
  def test_a_past_version_archive_is_reported_by_the_check_the_refusal_names
    _out, err, status = run_cli(%w[list], content: CURRENT_TEXT, archive: ARCHIVE_V1)
    assert_equal 1, status.exitstatus
    assert_match(/archive: unsupported meta version 1 \(expected 2\)/, err)

    out, _err, status = run_cli(%w[check], content: CURRENT_TEXT, archive: ARCHIVE_V1)
    assert_equal 1, status.exitstatus, "the check a refusal names must not report ok"
    assert_match(/archive\.jsonl: unsupported meta version 1 \(expected 2\)/, out)
    refute_match(/^ok —/, out)

    # --all-files agrees, as it always did.
    out, _err, status = run_cli(%w[check --all-files], content: CURRENT_TEXT, archive: ARCHIVE_V1)
    assert_equal 1, status.exitstatus
    assert_match(/archive\.jsonl: unsupported meta version 1/, out)
  end

  # Structural errors in the archive stay the business of --all-files: the
  # version gate is folded into the default check because it is store-wide,
  # not because the default check became a two-file lint.
  def test_default_check_still_ignores_structural_archive_errors
    broken_archive = Tasks::Format.dump([
      { "type" => "meta", "version" => Tasks::Format::VERSION },
      { "type" => "task", "id" => "cc000001", "state" => "NOPE", "title" => "Bad state" },
    ])
    out, _err, status = run_cli(%w[check], content: CURRENT_TEXT, archive: broken_archive)
    assert_equal 0, status.exitstatus
    assert_match(/^ok —/, out)

    out, _err, status = run_cli(%w[check --all-files], content: CURRENT_TEXT, archive: broken_archive)
    assert_equal 1, status.exitstatus
    assert_match(/archive\.jsonl/, out)
  end

  # A current store is untouched by all of this: the gate must be invisible on
  # the happy path, or it would be a very expensive way to break the CLI.
  def test_a_current_store_reads_normally
    LENIENT_READS.each do |args|
      out, err, status = run_cli(args, content: CURRENT_TEXT, archive: ARCHIVE_CURRENT,
                                       env: { "TASKS_OPENER" => "true" })
      assert_equal 0, status.exitstatus, "#{args.join(" ")} on a current store: #{err}"
      refute_match(/unsupported/, out + err, args.join(" "))
    end
  end

  # An empty or absent store is a first-run state, never a version refusal.
  def test_an_empty_store_is_not_a_version_refusal
    _out, _err, status = run_cli(%w[list], content: "")
    assert_equal 0, status.exitstatus
  end

  # The gate reads the version header, not the file. A store whose later lines
  # are unparseable still refuses on version — and a store at the CURRENT
  # version still reaches its command's own diagnostics rather than being
  # turned into a version error by the gate.
  def test_the_gate_consults_only_the_version_header
    corrupt_v1 = "#{Tasks::Format.dump([{ "type" => "meta", "version" => 1 }])}not json at all\n"
    _out, err, status = run_cli(%w[list], content: corrupt_v1)
    assert_equal 1, status.exitstatus
    assert_match(/unsupported meta version 1 \(expected 2\)/, err)

    # At the current version the gate stands aside, and the command reaches its
    # own diagnostics (here: the ref does not resolve, exit 2) rather than being
    # rewritten into a version refusal.
    corrupt_current = "#{Tasks::Format.dump([{ "type" => "meta", "version" => Tasks::Format::VERSION }])}not json\n"
    _out, err, status = run_cli(%w[done b0000502], content: corrupt_current)
    refute_equal 0, status.exitstatus
    refute_match(/unsupported meta version/, err)
  end

  private

  def run_cli(args, content:, archive: ARCHIVE_CURRENT, env: {})
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive_path = File.join(dir, "archive.jsonl")
      File.write(org, content)
      File.write(archive_path, archive)
      base = {
        "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive_path,
        "XDG_CONFIG_HOME" => File.join(dir, "config"),
        "XDG_STATE_HOME" => File.join(dir, "state")
      }.merge(env)
      out, err, status = Open3.capture3(base, "ruby", BIN, *args)
      [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), status]
    end
  end
end
