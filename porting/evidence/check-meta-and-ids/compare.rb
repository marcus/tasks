#!/usr/bin/env ruby
# frozen_string_literal: true

# Compare this slice's Go-owned diagnostics against the captured Ruby CLI
# observations. The full Check report is deliberately broader than this slice;
# extracting only the declared metadata/ID messages prevents either side from
# accidentally treating unported task-field or tree rules as this slice's work.

require "json"
require "open3"
require "pathname"

ROOT = Pathname.new(__dir__).join("../../..").realpath
CASES = ROOT.join("porting/runners/cases/check-meta-and-ids.jsonl")
RUBY = ROOT.join("porting/evidence/check-meta-and-ids/ruby")
GO = ROOT.join("go")

OWNED = [
  /missing meta record on line 1/,
  /line 1 must be a meta record \(\{"type":"meta","version":2\}\)/,
  /unsupported meta version .* \(expected 2\)/,
  /unexpected meta record \(only valid on line 1\)/,
  /record missing id/,
  /malformed id .* \(expected 8 hex chars\)/,
  /duplicate id ".*" \(lines .*\) — id refs will be wrong/,
  /id ".*" appears in both tasks\.jsonl line \d+ and archive\.jsonl line \d+/
].freeze

def ruby_owned(case_id)
  text = JSON.parse(RUBY.join("#{case_id}.json").read).dig("process", "stdout", "text")
  text.each_line.filter_map do |line|
    match = line.match(/^error  line (\d+): (.*)$/)
    next unless match && OWNED.any? { |pattern| pattern.match?(match[2]) }

    { "line" => match[1].to_i, "message" => match[2] }
  end
end

failures = []
CASES.each_line do |line|
  next if line.strip.empty? || line.lstrip.start_with?("#")

  testcase = JSON.parse(line)
  fixture = ROOT.join("porting/fixtures", testcase.fetch("fixture"), "store", "tasks.jsonl")
  command = ["go", "run", "./cmd/check-meta-and-ids-probe"]
  command << "--all-files" if testcase.fetch("argv").include?("--all-files")
  command << fixture.to_s
  stdout, stderr, status = Open3.capture3(*command, chdir: GO)
  unless status.success?
    failures << "#{testcase.fetch("case_id")}: probe failed: #{stderr.strip}"
    next
  end
  actual = JSON.parse(stdout).fetch("errors")
  expected = ruby_owned(testcase.fetch("case_id"))
  next if actual == expected

  failures << "#{testcase.fetch("case_id")}: expected #{expected.inspect}, got #{actual.inspect}"
end

abort failures.join("\n") unless failures.empty?
puts "check-meta-and-ids: 9 Ruby/Go diagnostic comparisons passed"
