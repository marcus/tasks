# frozen_string_literal: true

require "minitest/autorun"
require "tmpdir"
require "fileutils"
require "date"

# Point the undo journal (Tasks::Journal) at a throwaway dir for the whole test
# run, so nothing lands in the developer's real ~/.local/state. Each test uses a
# unique org path (Dir.mktmpdir), which the journal hashes into its own subdir,
# so stores stay isolated from each other. Child CLI processes inherit this env.
TEST_STATE_HOME = Dir.mktmpdir("tasks-test-state")
ENV["XDG_STATE_HOME"] = TEST_STATE_HOME
at_exit { FileUtils.remove_entry(TEST_STATE_HOME) if File.directory?(TEST_STATE_HOME) }

# Minitest 6 dropped minitest/mock (and Object#stub); this project is
# stdlib-only, so vendor the classic Minitest 5 stub — the one feature the
# TUI tests use — to temporarily swap a method for the duration of a block.
class Object
  def stub(name, val_or_callable, *block_args, **block_kwargs)
    metaclass = class << self; self; end

    if respond_to?(name) && !methods.map(&:to_s).include?(name.to_s)
      metaclass.send(:define_method, name) { |*args, **kwargs, &blk| super(*args, **kwargs, &blk) }
    end

    metaclass.send(:alias_method, "__stub__#{name}", name)
    metaclass.send(:define_method, name) do |*args, **kwargs, &blk|
      if val_or_callable.respond_to?(:call)
        val_or_callable.call(*args, **kwargs, &blk)
      else
        val_or_callable
      end
    end

    yield self
  ensure
    metaclass.send(:undef_method, name)
    metaclass.send(:alias_method, name, "__stub__#{name}")
    metaclass.send(:undef_method, "__stub__#{name}")
  end
end

$LOAD_PATH.unshift File.expand_path("../lib", __dir__)

require "tui/ansi"
require "tui/dates"
require "tui/store"
require "tui/views"
require "tui/frame"
require "llm/registry"
require "tasks/format"

module LLMTestHelpers
  # An EMPTY LLM config so App tests are hermetic — they never read the
  # developer's real ~/.config/tasks/config (which App.new does by default in
  # production). Yields the built-in provider/model defaults.
  def default_llm_config
    LLM::Config.new(provider: nil, model: nil, providers: {})
  end
end
Minitest::Test.include(LLMTestHelpers)

# Fixed 8-hex ids for the shared fixture, exposed so tests can assert against a
# known id (and so migration-style stable ids are exercised end to end). Real
# ids are SecureRandom.hex(4); these are hand-picked hex so they're stable.
FIX = {
  inbox:  "aaaa0001",
  garden: "aaaa0002",
  work:   "aaaa0003",
  flight: "aaaa0004",
  pr:     "aaaa0005",
  eval:   "aaaa0006",
  travel: "aaaa0007",
  old:    "aaaa0008",
  home:   "aaaa0009",
  plants: "aaaa000a",
}.freeze

# The shared fixture as records — same titles/states/dates/tags/notes as the
# old org fixture, so behavioral assertions carry over. `with_store` serializes
# this to tasks.jsonl. Order is DFS pre-order (a validated invariant).
FIXTURE_RECORDS = [
  { "type" => "meta", "version" => 1 },
  { "type" => "section", "id" => FIX[:inbox], "title" => "Inbox" },
  { "type" => "task", "id" => FIX[:garden], "parent" => FIX[:inbox], "state" => "INBOX",
    "title" => "random thought about the garden" },
  { "type" => "section", "id" => FIX[:work], "title" => "Work" },
  { "type" => "task", "id" => FIX[:flight], "parent" => FIX[:work], "state" => "NEXT",
    "priority" => "A", "title" => "Book flight in Concur",
    "tags" => %w[@computer important urgent], "deadline" => "2026-07-02" },
  { "type" => "task", "id" => FIX[:pr], "parent" => FIX[:work], "state" => "NEXT",
    "priority" => "B", "title" => "Review PR backlog", "tags" => %w[@computer important] },
  { "type" => "task", "id" => FIX[:eval], "parent" => FIX[:work], "state" => "TODO",
    "priority" => "A", "title" => "Midyear self-eval",
    "tags" => %w[@computer important], "scheduled" => "2026-07-03" },
  { "type" => "task", "id" => FIX[:travel], "parent" => FIX[:work], "state" => "WAITING",
    "title" => "Travel desk reply", "tags" => %w[@email urgent], "body" => "Some note line." },
  { "type" => "task", "id" => FIX[:old], "parent" => FIX[:work], "state" => "DONE",
    "priority" => "C", "title" => "Old finished thing", "tags" => %w[@computer],
    "closed" => "2026-06-20" },
  { "type" => "section", "id" => FIX[:home], "title" => "Home" },
  { "type" => "task", "id" => FIX[:plants], "parent" => FIX[:home], "state" => "NEXT",
    "title" => "Water the plants", "tags" => %w[@home] },
].freeze

# The canonical fixture text (one JSON record per line). Kept under the old
# name so the many `File.write(path, FIXTURE_ORG)` / `assert_equal FIXTURE_ORG`
# sites read unchanged — the content is jsonl now.
FIXTURE = Tasks::Format.dump(FIXTURE_RECORDS)
FIXTURE_ORG = FIXTURE

# Serialize a records array to fixture text (for tests that need a variant).
def dump_fixture(records) = Tasks::Format.dump(records)

# The fixture with the plants task deferred (a common variant).
def deferred_fixture
  recs = FIXTURE_RECORDS.map(&:dup)
  plants = recs.find { |r| r["id"] == FIX[:plants] }
  plants["tags"] = plants["tags"] + ["defer"]
  Tasks::Format.dump(recs)
end

# The parsed record (string-keyed hash, stamped with "line") whose title
# matches — the field-level counterpart to the old regex-over-org assertions.
def record_for(path, title:)
  Tasks::Format.parse(File.read(path, encoding: "UTF-8")).records.find { |r| r["title"] == title }
end

def with_store
  Dir.mktmpdir do |dir|
    org = File.join(dir, "tasks.jsonl")
    archive = File.join(dir, "archive.jsonl")
    File.write(org, FIXTURE)
    yield Tui::Store.new(org: org, archive: archive), org, archive
  end
end

def find_item(store, text)
  store.items.find { |i| i.title.include?(text) } or raise "no item: #{text}"
end
