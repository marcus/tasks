# frozen_string_literal: true

require_relative "test_helper"
require "tasks/format"

class TestFormat < Minitest::Test
  F = Tasks::Format

  # The canonical file the plan documents, record for record. This doubles as
  # the golden regression: any change to key order, spacing, or omission rules
  # shifts these exact bytes and fails here.
  GOLDEN_RECORDS = [
    { "type" => "meta", "version" => 1 },
    { "type" => "section", "id" => "a1b2c3d4", "title" => "Inbox" },
    { "type" => "task", "id" => "0f9e8d7c", "parent" => "a1b2c3d4", "state" => "INBOX",
      "title" => "Random thought", "body" => "Captured [2026-07-01]." },
    { "type" => "section", "id" => "b2c3d4e5", "title" => "Projects" },
    { "type" => "section", "id" => "c3d4e5f6", "parent" => "b2c3d4e5",
      "title" => "Launch the personal site", "body" => "Goal: site up by end of month." },
    { "type" => "task", "id" => "d4e5f6a7", "parent" => "c3d4e5f6", "state" => "NEXT",
      "priority" => "A", "title" => "Pick a static-site generator",
      "tags" => %w[@computer important], "deadline" => "2026-07-20" },
    { "type" => "task", "id" => "e5f6a7b8", "parent" => "a1b2c3d4", "state" => "NEXT",
      "title" => "Water the plants", "tags" => ["@home"], "scheduled" => "2026-07-08",
      "recur" => ".+1w", "body" => "- Did [2026-07-01]." },
    { "type" => "task", "id" => "f6a7b8c9", "parent" => "a1b2c3d4", "state" => "DONE",
      "priority" => "C", "title" => "Old finished thing", "closed" => "2026-06-20" },
  ].freeze

  GOLDEN_TEXT = <<~JSONL
    {"type":"meta","version":1}
    {"type":"section","id":"a1b2c3d4","title":"Inbox"}
    {"type":"task","id":"0f9e8d7c","parent":"a1b2c3d4","state":"INBOX","title":"Random thought","body":"Captured [2026-07-01]."}
    {"type":"section","id":"b2c3d4e5","title":"Projects"}
    {"type":"section","id":"c3d4e5f6","parent":"b2c3d4e5","title":"Launch the personal site","body":"Goal: site up by end of month."}
    {"type":"task","id":"d4e5f6a7","parent":"c3d4e5f6","state":"NEXT","priority":"A","title":"Pick a static-site generator","tags":["@computer","important"],"deadline":"2026-07-20"}
    {"type":"task","id":"e5f6a7b8","parent":"a1b2c3d4","state":"NEXT","title":"Water the plants","tags":["@home"],"scheduled":"2026-07-08","recur":".+1w","body":"- Did [2026-07-01]."}
    {"type":"task","id":"f6a7b8c9","parent":"a1b2c3d4","state":"DONE","priority":"C","title":"Old finished thing","closed":"2026-06-20"}
  JSONL

  # -- dump ------------------------------------------------------------------

  def test_golden_dump_is_byte_for_byte
    assert_equal GOLDEN_TEXT, F.dump(GOLDEN_RECORDS)
  end

  def test_dump_has_trailing_newline_and_one_line_per_record
    out = F.dump(GOLDEN_RECORDS)
    assert out.end_with?("\n")
    assert_equal GOLDEN_RECORDS.size, out.each_line.count
  end

  def test_dump_empty_is_empty_string
    assert_equal "", F.dump([])
  end

  def test_dump_record_returns_single_line_no_newline
    line = F.dump_record(GOLDEN_RECORDS[0])
    refute_includes line, "\n"
    assert_equal '{"type":"meta","version":1}', line
  end

  # -- key order -------------------------------------------------------------

  def test_key_order_independent_of_insertion_order
    scrambled = {
      "body" => "note", "title" => "T", "state" => "NEXT",
      "parent" => "p1", "id" => "i1", "type" => "task",
    }
    assert_equal(
      '{"type":"task","id":"i1","parent":"p1","state":"NEXT","title":"T","body":"note"}',
      F.dump_record(scrambled)
    )
  end

  def test_symbol_keys_accepted
    assert_equal '{"type":"meta","version":1}',
                 F.dump_record({ type: "meta", version: 1 })
  end

  # -- unknown keys ----------------------------------------------------------

  def test_unknown_keys_emitted_after_known_in_insertion_order
    rec = { "type" => "task", "title" => "T", "zeta" => 1, "alpha" => 2 }
    assert_equal '{"type":"task","title":"T","zeta":1,"alpha":2}', F.dump_record(rec)
  end

  def test_unknown_key_round_trips_untouched
    rec = { "type" => "task", "title" => "T", "future_field" => "keepme" }
    parsed = F.parse(F.dump([rec])).records.first
    assert_equal "keepme", parsed["future_field"]
  end

  # -- omission --------------------------------------------------------------

  def test_nil_empty_string_and_empty_array_omitted
    rec = {
      "type" => "task", "id" => "i1", "title" => "T",
      "priority" => nil, "body" => "", "tags" => [], "parent" => nil,
    }
    assert_equal '{"type":"task","id":"i1","title":"T"}', F.dump_record(rec)
  end

  def test_present_values_not_dropped_by_omission
    rec = { "type" => "task", "title" => "T", "tags" => ["@home"], "body" => "x" }
    assert_equal '{"type":"task","title":"T","tags":["@home"],"body":"x"}', F.dump_record(rec)
  end

  # -- non-ASCII -------------------------------------------------------------

  def test_non_ascii_left_unescaped
    rec = { "type" => "task", "title" => "Café — résumé naïve" }
    line = F.dump_record(rec)
    assert_includes line, "Café — résumé naïve"
    refute_includes line, '\\u'
  end

  # -- round trip ------------------------------------------------------------

  def test_round_trip_modulo_line_bookkeeping
    parsed = F.parse(F.dump(GOLDEN_RECORDS)).records
    stripped = parsed.map { |r| r.reject { |k, _| k == "line" } }
    assert_equal GOLDEN_RECORDS, stripped
  end

  def test_parse_stamps_correct_line_numbers
    res = F.parse(GOLDEN_TEXT)
    assert_equal (1..GOLDEN_RECORDS.size).to_a, res.records.map { |r| r["line"] }
  end

  # -- lenient parse ---------------------------------------------------------

  def test_lenient_parse_skips_bad_lines_and_reports_them
    text = <<~JSONL
      {"type":"meta","version":1}
      this is not json
      {"type":"task","title":"good one"}
      42
      {"type":"task","title":"after scalar"}
    JSONL
    res = F.parse(text)

    # The three well-formed objects parse; the garbage and the scalar don't.
    assert_equal %w[meta task task], res.records.map { |r| r["type"] }
    titles = res.records.map { |r| r["title"] }.compact
    assert_equal ["good one", "after scalar"], titles

    # Errors carry the right 1-based line numbers even after skips.
    assert_equal [2, 4], res.errors.map(&:first)
    assert_match(/invalid JSON/, res.errors[0][1])
    assert_match(/expected a JSON object/, res.errors[1][1])

    # Line stamps stay physical (line 3 and line 5), not record-relative.
    assert_equal [1, 3, 5], res.records.map { |r| r["line"] }
    refute res.ok?
  end

  def test_scalar_line_reports_its_type
    res = F.parse("42\n")
    assert_empty res.records
    assert_equal 1, res.errors[0][0]
    assert_match(/Integer/, res.errors[0][1])
  end

  # -- empty / whitespace / trailing newline ---------------------------------

  def test_parse_empty_string_yields_nothing
    res = F.parse("")
    assert_empty res.records
    assert_empty res.errors
    assert res.ok?
  end

  def test_parse_lone_newline_is_one_blank_line_error
    res = F.parse("\n")
    assert_empty res.records
    assert_equal [[1, "blank line"]], res.errors
  end

  def test_blank_line_between_records_reported_with_line_number
    text = "{\"type\":\"meta\",\"version\":1}\n\n{\"type\":\"task\",\"title\":\"T\"}\n"
    res = F.parse(text)
    assert_equal 2, res.records.size
    assert_equal [[2, "blank line"]], res.errors
    assert_equal [1, 3], res.records.map { |r| r["line"] }
  end

  def test_trailing_newline_does_not_create_a_phantom_record
    res = F.parse(F.dump(GOLDEN_RECORDS))
    assert_equal GOLDEN_RECORDS.size, res.records.size
    assert res.ok?
  end

  # A leading UTF-8 BOM (U+FEFF, which some editors prepend) must be tolerated,
  # not fold line 1 in as an opaque "invalid JSON". (m8)
  def test_leading_bom_is_stripped_and_line_one_parses
    res = F.parse("﻿" + GOLDEN_TEXT)
    assert res.ok?, res.errors.inspect
    assert_equal "meta", res.records.first["type"]
    assert_equal 1, res.records.first["line"]
  end
end
