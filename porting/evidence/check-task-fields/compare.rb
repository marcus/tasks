#!/usr/bin/env ruby
# frozen_string_literal: true

# Compare this slice's Go-owned diagnostics against the captured Ruby CLI
# observations. Check's report is deliberately broader than any one slice, so
# BOTH sides are filtered through the same owned-message list: the Ruby capture
# still carries lead, temporal, delegation, and tree messages this slice does
# not port, and the Go package still carries the metadata and ID messages the
# previous slice ported. Filtering one side only would silently credit or blame
# a neighbour's behavior.
#
# Errors and warnings are compared as separate channels on purpose. Conflating
# them is the expected defect: a warning leaves the file usable and the CLI
# still exits 0.

require "json"
require "open3"
require "pathname"

ROOT = Pathname.new(__dir__).join("../../..").realpath
CASES = ROOT.join("porting/runners/cases/check-task-fields.jsonl")
RUBY = ROOT.join("porting/evidence/check-task-fields/ruby")
GO = ROOT.join("go")

OWNED_ERRORS = [
  /\Ainvalid state .* \(expected PROPOSED\/INBOX\/TODO\/NEXT\/WAITING\/DONE\/CANCELLED\)\z/,
  /\Ainvalid priority .* \(expected A, B, or C\)\z/,
  /\Atask has no title\z/,
  /\Atitle must be a string\z/,
  /\A(scheduled|deadline|closed|archived) .* is not a YYYY-MM-DD date\z/,
  /\A(scheduled|deadline|closed|archived) .* is not a real date\z/,
  /\Ainvalid recur cookie .* \(expected e\.g\. \.\+1w, \+\+1m, \+2d, w:mon, m:15, y:07-04\)\z/,
  /\Aclosed date on an? (open|proposed) task \(.*\)\z/,
  /\Atags must be an array\z/,
  /\Atags must all be strings\z/,
  /\Aupdated .* is not an RFC3339 UTC timestamp with device slug\z/
].freeze

OWNED_WARNINGS = [
  /\Aunknown key .*\z/,
  /\Aunknown delegation key .*\z/,
  /\Aduplicate open title .* \(lines .*\) — fuzzy refs will be ambiguous\z/
].freeze

def owned(entries, patterns)
  entries.select { |entry| patterns.any? { |pattern| pattern.match?(entry.fetch("message")) } }
end

# Two diagnostics on the same line have no defined relative order: Check ends
# with `sort_by(&:first)` and Ruby's sort_by is not stable, so it can hoist one
# tied entry above its neighbour. Go sorts stably. No fixture in this corpus
# ties, but ordering tied entries by message on both sides keeps a later
# fixture from failing this comparator over a Ruby quicksort artifact. Order
# ACROSS lines is still compared exactly. See the manifest deviation.
def by_line(entries)
  entries.sort_by.with_index { |entry, index| [entry.fetch("line"), entry.fetch("message"), index] }
end

# The Ruby side is read from the captured human surface, which is where the two
# channels are visibly distinct.
def ruby_entries(case_id, marker)
  text = JSON.parse(RUBY.join("#{case_id}.json").read).dig("process", "stdout", "text")
  text.each_line.filter_map do |line|
    match = line.match(/\A#{marker}\s+line (\d+): (.*)\n?\z/)
    next unless match

    { "line" => match[1].to_i, "message" => match[2] }
  end
end

failures = []
comparisons = 0
CASES.each_line do |line|
  next if line.strip.empty? || line.lstrip.start_with?("#")

  testcase = JSON.parse(line)
  case_id = testcase.fetch("case_id")
  # Each store is captured twice (human and --json); one comparison per store.
  next if case_id.end_with?("-json")

  fixture = ROOT.join("porting/fixtures", testcase.fetch("fixture"), "store", "tasks.jsonl")
  stdout, stderr, status = Open3.capture3("go", "run", "./cmd/check-task-fields-probe", fixture.to_s, chdir: GO.to_s)
  unless status.success?
    failures << "#{case_id}: probe failed: #{stderr.strip}"
    next
  end
  report = JSON.parse(stdout)

  [["errors", OWNED_ERRORS, "error"], ["warnings", OWNED_WARNINGS, "warn"]].each do |channel, patterns, marker|
    actual = by_line(owned(report.fetch(channel), patterns))
    expected = by_line(owned(ruby_entries(case_id, marker), patterns))
    comparisons += 1
    next if actual == expected

    failures << "#{case_id} #{channel}: expected #{expected.inspect}, got #{actual.inspect}"
  end

  # The channel split itself: nothing Ruby reported as a warning may reach the
  # Go error list, and nothing it reported as an error may reach the warnings.
  crossed = owned(report.fetch("errors"), OWNED_WARNINGS) + owned(report.fetch("warnings"), OWNED_ERRORS)
  failures << "#{case_id}: diagnostics on the wrong channel: #{crossed.inspect}" unless crossed.empty?
end

abort failures.join("\n") unless failures.empty?
puts "check-task-fields: #{comparisons} Ruby/Go diagnostic comparisons passed"
