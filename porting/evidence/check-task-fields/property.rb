#!/usr/bin/env ruby
# frozen_string_literal: true

# Property test for the check-task-fields vocabularies, run as a Ruby/Go
# differential: a generator produces recurrence cookies, dates, states,
# priorities, tags, and update stamps — canonical values, near-misses, and
# junk — and both implementations lint the SAME synthesized store. The oracle
# is Ruby's Check, never Go's output.
#
# The generator is deliberately adversarial around the traps the capture
# pinned: whitespace padding, uppercase units, zero-padded day-of-month,
# unsorted weekday lists, and duplicate rules that canonicalize away.
#
# Stores are written under a temp dir. No fixture and no live store is touched.

require "json"
require "open3"
require "pathname"
require "tmpdir"

ROOT = Pathname.new(__dir__).join("../../..").realpath
GO = ROOT.join("go")
ROUNDS = Integer(ENV.fetch("ROUNDS", "12"))
PER_ROUND = Integer(ENV.fetch("PER_ROUND", "60"))
SEED = Integer(ENV.fetch("SEED", "20260802"))

$LOAD_PATH.unshift(ROOT.join("lib").to_s)
require "tasks/check"

DAYS = %w[mon tue wed thu fri sat sun].freeze
DAY_WORDS = %w[monday mondays tues thur thurs weds sunday sat].freeze

def random_recur(rng)
  case rng.rand(12)
  when 0 then "#{%w[.+ ++ +].sample(random: rng)}#{rng.rand(1..9)}#{%w[d w m y].sample(random: rng)}"
  when 1 then "#{["", "+"].sample(random: rng)}#{["", rng.rand(2..4).to_s].sample(random: rng)}w:" \
                "#{DAYS.sample(rng.rand(1..3), random: rng).join(",")}"
  when 2 then "#{["", "+"].sample(random: rng)}m:#{rng.rand(1..31)}"
  when 3 then "m:#{rng.rand(1..5)}#{DAYS.sample(random: rng)}"
  when 4 then "m:last#{DAYS.sample(random: rng)}"
  when 5 then format("y:%02d-%02d", rng.rand(1..12), rng.rand(1..28))
  when 6 then format("y:%02d:%s%s", rng.rand(1..12), rng.rand(1..5), DAYS.sample(random: rng))
  when 7 then "w:#{DAY_WORDS.sample(random: rng)}"                   # alias: parses, not canonical
  when 8 then "m:#{format("%02d", rng.rand(1..9))}"                  # zero-padded day: refused
  when 9 then "#{[" ", ""].sample(random: rng)}w:mon#{[" ", ""].sample(random: rng)}"
  when 10 then "+#{rng.rand(0..2)}#{%w[W D w].sample(random: rng)}"
  else %w[every\ week off none w: m: y: 1w:mon w:wed,mon m:15,1 y:7-04 .+w:mon].sample(random: rng)
  end
end

def random_date(rng)
  case rng.rand(6)
  when 0 then format("%04d-%02d-%02d", rng.rand(1970..2100), rng.rand(1..12), rng.rand(1..28))
  when 1 then format("%04d-%02d-%02d", rng.rand(1970..2100), 2, rng.rand(28..31))
  when 2 then format("%04d-%02d-%02d", rng.rand(1970..2100), rng.rand(1..12), rng.rand(29..31))
  when 3 then format("%04d-%02d-%02d", rng.rand(1970..2100), rng.rand(13..19), rng.rand(1..28))
  when 4 then "#{rng.rand(1..12)}/#{rng.rand(1..28)}/#{rng.rand(1970..2100)}"
  else format("%04d-%02d-%02d ", rng.rand(1970..2100), rng.rand(1..12), rng.rand(1..28))
  end
end

def random_updated(rng)
  stamp = format("%04d-%02d-%02dT%02d:%02d:%02dZ", rng.rand(1970..2100), rng.rand(1..13),
                 rng.rand(1..32), rng.rand(0..25), rng.rand(0..61), rng.rand(0..61))
  device = %w[marcus dev1 DEV mbp\ air].sample(random: rng)
  [stamp + "#" + device, stamp, stamp + "#"].sample(random: rng)
end

def random_task(rng, index)
  record = {
    "type" => "task",
    "id" => format("%08x", index + 0x10000000),
    "state" => %w[TODO NEXT DONE PROPOSED WAITING INBOX CANCELLED started todo].sample(random: rng),
    "title" => ["a task", "  ", "", "Another Task"].sample(random: rng),
  }
  record["priority"] = %w[A B C Z a].sample(random: rng) if rng.rand(3).zero?
  record["recur"] = random_recur(rng) if rng.rand(2).zero?
  record["scheduled"] = random_date(rng) if rng.rand(3).zero?
  record["deadline"] = random_date(rng) if rng.rand(4).zero?
  record["closed"] = random_date(rng) if rng.rand(4).zero?
  record["archived"] = random_date(rng) if rng.rand(5).zero?
  record["tags"] = [%w[@home], %w[@home @work], ["@home", 3], "@home", []].sample(random: rng) if rng.rand(3).zero?
  record["updated"] = random_updated(rng) if rng.rand(3).zero?
  record["energy"] = "high" if rng.rand(6).zero?
  record
end

OWNED_ERRORS = [
  /\Ainvalid state /, /\Ainvalid priority /, /\Atask has no title\z/, /\Atitle must be a string\z/,
  /\A(scheduled|deadline|closed|archived) .* is not a (YYYY-MM-DD date|real date)\z/,
  /\Ainvalid recur cookie /, /\Aclosed date on an? (open|proposed) task /,
  /\Atags must (be an array|all be strings)\z/, /\Aupdated .* is not an RFC3339 /
].freeze
OWNED_WARNINGS = [/\Aunknown key /, /\Aunknown delegation key /, /\Aduplicate open title /].freeze

def owned(entries, patterns)
  entries.select { |entry| patterns.any? { |pattern| pattern.match?(entry.fetch(:message).to_s) } }
end

# Two diagnostics on the SAME line have no defined relative order: Check ends
# with `sort_by(&:first)`, and Ruby's sort_by is not stable — for a store with
# ~80 records it hoists one tied warning above its neighbour and leaves the
# next two alone (see the manifest's recorded deviation, and
# `(1..80).map { [_1, "k#{_1}"] } + [[63,"dup"]]` for the one-liner that shows
# it). Go sorts stably, so it keeps emission order. Comparing the sequence
# within a line would be asserting an artifact of Ruby's quicksort pivots, so
# the comparison orders tied entries by message on both sides. Order ACROSS
# lines is still compared exactly.
def by_line(entries)
  entries.sort_by.with_index { |entry, index| [entry.fetch(:line), entry.fetch(:message), index] }
end

rng = Random.new(SEED)
failures = []
records_compared = 0

Dir.mktmpdir("check-task-fields-property") do |dir|
  path = Pathname.new(dir).join("tasks.jsonl")
  ROUNDS.times do |round|
    lines = [{ "type" => "meta", "version" => 2 }]
    PER_ROUND.times { |index| lines << random_task(rng, index) }
    path.write(lines.map { |record| JSON.generate(record) }.join("\n") + "\n")

    ruby = Tasks::Check.check(path.to_s)
    stdout, stderr, status = Open3.capture3("go", "run", "./cmd/check-task-fields-probe", path.to_s, chdir: GO.to_s)
    unless status.success?
      failures << "round #{round}: probe failed: #{stderr.strip}"
      next
    end
    go = JSON.parse(stdout)

    expected_errors = by_line(owned(ruby.errors.map { |line, message| { line: line, message: message } }, OWNED_ERRORS))
    expected_warnings = by_line(owned(ruby.warnings.map { |line, message| { line: line, message: message } }, OWNED_WARNINGS))
    actual_errors = by_line(owned(go.fetch("errors").map { |e| { line: e["line"], message: e["message"] } }, OWNED_ERRORS))
    actual_warnings = by_line(owned(go.fetch("warnings").map { |e| { line: e["line"], message: e["message"] } }, OWNED_WARNINGS))
    records_compared += PER_ROUND

    if actual_errors != expected_errors
      failures << "round #{round} errors:\n  ruby #{expected_errors.inspect}\n" \
                  "  go   #{actual_errors.inspect}\n  store #{path.read}"
    end
    next if actual_warnings == expected_warnings

    failures << "round #{round} warnings:\n  ruby #{expected_warnings.inspect}\n" \
                "  go   #{actual_warnings.inspect}\n  store #{path.read}"
  end
end

abort failures.first(3).join("\n") unless failures.empty?
puts "check-task-fields property: #{ROUNDS} generated stores, #{records_compared} records, " \
     "Ruby and Go agreed on every owned diagnostic (seed #{SEED})"
