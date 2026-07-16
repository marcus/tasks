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
  { "type" => "meta", "version" => 2 },
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

# Fixed 8-hex ids for the Projects-feature fixture, mirroring the FIX idiom so
# project/area query and mutation tests can assert against known ids.
PFIX = {
  inbox:         "cccc0001",
  inbox_task:    "cccc0002",
  projects:      "cccc0003",
  site:          "cccc0004",
  site_next:     "cccc0005",
  site_todo:     "cccc0006",
  site_sub:      "cccc0007",
  site_sub_task: "cccc0008",
  site_deferred: "cccc0009",
  reno:          "cccc000a",
  reno_todo:     "cccc000b",
  empty:         "cccc000c",
  tasks:         "cccc000d",
  tasks_next:    "cccc000e",
  tasks_todo:    "cccc000f",
  donepile:      "cccc0010",
  done_task:     "cccc0011",
}.freeze

# Records exercising the project read model and mutations: an Inbox (excluded
# from areas), a top-level "Projects" heading whose child sections are projects
# — "Site launch" (a body note, a recurring NEXT, a TODO with a deadline, a
# nested "Copywriting" sub-section proving depth rollup, and a deferred TODO
# proving deferral exclusion), "Stuck reno" (a TODO with no NEXT), and an empty
# project — plus a "Tasks" area (2 open incl. 1 NEXT) and a "Done pile" whose
# only task is DONE (so it never surfaces as an area). DFS pre-order throughout.
PROJECTS_FIXTURE_RECORDS = [
  { "type" => "meta", "version" => 2 },
  { "type" => "section", "id" => PFIX[:inbox], "title" => "Inbox" },
  { "type" => "task", "id" => PFIX[:inbox_task], "parent" => PFIX[:inbox], "state" => "INBOX",
    "title" => "unfiled capture" },
  { "type" => "section", "id" => PFIX[:projects], "title" => "Projects" },
  { "type" => "section", "id" => PFIX[:site], "parent" => PFIX[:projects], "title" => "Site launch",
    "body" => "Goal: ship the personal site." },
  { "type" => "task", "id" => PFIX[:site_next], "parent" => PFIX[:site], "state" => "NEXT",
    "title" => "Pick a static-site generator", "recur" => "+1w" },
  { "type" => "task", "id" => PFIX[:site_todo], "parent" => PFIX[:site], "state" => "TODO",
    "title" => "Write the landing copy", "deadline" => "2026-07-25" },
  { "type" => "section", "id" => PFIX[:site_sub], "parent" => PFIX[:site], "title" => "Copywriting" },
  { "type" => "task", "id" => PFIX[:site_sub_task], "parent" => PFIX[:site_sub], "state" => "TODO",
    "title" => "Draft the about page" },
  { "type" => "task", "id" => PFIX[:site_deferred], "parent" => PFIX[:site], "state" => "TODO",
    "title" => "Someday: custom domain", "tags" => %w[defer] },
  { "type" => "section", "id" => PFIX[:reno], "parent" => PFIX[:projects], "title" => "Stuck reno" },
  { "type" => "task", "id" => PFIX[:reno_todo], "parent" => PFIX[:reno], "state" => "TODO",
    "title" => "Measure the kitchen" },
  { "type" => "section", "id" => PFIX[:empty], "parent" => PFIX[:projects], "title" => "Empty project" },
  { "type" => "section", "id" => PFIX[:tasks], "title" => "Tasks" },
  { "type" => "task", "id" => PFIX[:tasks_next], "parent" => PFIX[:tasks], "state" => "NEXT",
    "title" => "Reply to the vendor" },
  { "type" => "task", "id" => PFIX[:tasks_todo], "parent" => PFIX[:tasks], "state" => "TODO",
    "title" => "File expenses" },
  { "type" => "section", "id" => PFIX[:donepile], "title" => "Done pile" },
  { "type" => "task", "id" => PFIX[:done_task], "parent" => PFIX[:donepile], "state" => "DONE",
    "title" => "Old finished chore", "closed" => "2026-07-01" },
].freeze

PROJECTS_FIXTURE = Tasks::Format.dump(PROJECTS_FIXTURE_RECORDS)

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

# The old Store mutation methods intentionally disappeared in Phase 1d. Keep
# the pre-existing semantic regression tests readable while routing every one
# through the public stable-id patch protocol. This test-only adapter never
# uses a file line as a locator or result; the production TUI and CLI consume
# MutationResult directly.
class StableMutationTestAdapter
  def initialize(store)
    @store = store
  end

  def complete(item)
    result = patch(item, :state, "DONE", label: "complete: #{item.title}")
    result.ok? ? result.touched_ids : false
  end

  def set_priority(item, priority)
    label = priority ? "priority [##{priority}]: #{item.title}" : "clear priority: #{item.title}"
    ok?(patch(item, :priority, priority, label: label))
  end

  def reschedule(item, date)
    snapshot = snapshot_for(item)
    return false unless snapshot

    kind = snapshot.deadline ? :deadline : snapshot.scheduled ? :scheduled : :deadline
    ok?(patch(item, kind, date, label: "reschedule → #{date.iso8601}: #{item.title}", snapshot: snapshot))
  end

  def set_date(item, date, kind:)
    ok?(patch(item, kind, date, label: "#{kind} → #{date.iso8601}: #{item.title}"))
  end

  def set_state(item, state)
    result = patch(item, :state, state, label: "state → #{state}: #{item.title}")
    result.ok? ? result.touched_ids : false
  end

  def undate(item, kind: nil)
    snapshot = snapshot_for(item)
    return false unless snapshot
    return false if kind ? snapshot.public_send(kind).nil? : !snapshot.scheduled && !snapshot.deadline

    label = kind ? "remove #{kind}: #{item.title}" : "remove dates: #{item.title}"
    expected = snapshot.metadata.fetch(:date_state)
    result = @store.patch_task!(Tasks::TaskPatch.new(
      id: snapshot.id, field: :date_clear, value: kind, expected: expected, history_label: label
    ))
    ok?(result)
  end

  def retitle(item, title)
    ok?(patch(item, :title, title, label: "retitle → #{title}: #{item.title}"))
  end

  def set_tags(item, add: [], remove: [])
    snapshot = snapshot_for(item)
    return false unless snapshot

    result = @store.patch_task!(Tasks::TaskPatch.new(
      id: snapshot.id, field: :tag_delta, value: { add: add, remove: remove },
      expected: snapshot.metadata.fetch(:tag_sequence), history_label: "tags: #{item.title}"
    ))
    ok?(result)
  end

  def set_deferred(item, deferred)
    label = deferred ? "defer: #{item.title}" : "activate: #{item.title}"
    ok?(patch(item, :deferred, deferred, label: label))
  end

  def set_recur(item, cookie)
    label = cookie == :off ? "recur off: #{item.title}" : "recur #{cookie}: #{item.title}"
    ok?(patch(item, :recurrence, cookie, label: label))
  end

  def add_note(item, text)
    snapshot = snapshot_for(item)
    return false unless snapshot

    body = snapshot.body.empty? ? text.strip : "#{snapshot.body}\n#{text.strip}"
    ok?(patch(item, :body, body, label: "note: #{item.title}", snapshot: snapshot))
  end

  def move(item, section)
    records = @store.read_snapshot.live_records
    want = section.strip.downcase
    target = records.select { |record| record["type"] == "section" && !record["parent"] }
                    .find { |record| record["title"].to_s.downcase == want } ||
             records.select { |record| record["type"] == "section" && !record["parent"] }
                    .find { |record| record["title"].to_s.downcase.include?(want) }
    return false unless target

    result = patch(item, :location, target["id"], label: "move → #{section}: #{item.title}", force: true)
    result.ok? ? item.id : false
  end

  def move_under(item, parent)
    result = patch(item, :location, parent.id, label: "nest under #{parent.title}: #{item.title}", force: true)
    return result.status if result.cycle? || result.too_deep?

    result.ok? ? item.id : false
  end

  def move_top(item)
    records = @store.read_snapshot.live_records
    by_id = records.to_h { |record| [record["id"], record] }
    record = by_id[item.id]
    parent_id = record && record["parent"]
    record = parent_id && by_id[parent_id]
    while record && record["type"] != "section"
      parent_id = record["parent"]
      record = parent_id && by_id[parent_id]
    end
    return false unless record

    result = patch(item, :location, record["id"], label: "unnest: #{item.title}", force: true)
    result.ok? ? item.id : false
  end

  private

  def ok?(result) = result.ok?

  def snapshot_for(item)
    item&.id && @store.edit_snapshot(item.id)
  end

  def patch(item, field, value, label:, snapshot: nil, force: false)
    snapshot ||= snapshot_for(item)
    return Tasks::MutationResult.new(status: :not_found) unless snapshot

    @store.patch_task!(Tasks::TaskPatch.from(
      snapshot, field: field, value: value, history_label: label, force: force
    ))
  end
end

class Tasks::Store
  def test_mutation = StableMutationTestAdapter.new(self)
end
