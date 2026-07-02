# frozen_string_literal: true

require "minitest/autorun"
require "tmpdir"
require "date"

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

module LLMTestHelpers
  # The built-in provider/model entries, assembled with an EMPTY config so App
  # tests are hermetic — they never read the developer's real
  # ~/.config/tasks/config (which App.new does by default in production).
  def default_llm_entries
    LLM.entries(LLM::Config.new(provider: nil, model: nil, providers: {}))
  end
end
Minitest::Test.include(LLMTestHelpers)

FIXTURE_ORG = <<~ORG
  * Inbox
  ** INBOX random thought about the garden

  * Work
  ** NEXT [#A] Book flight in Concur :@computer:important:urgent:
     DEADLINE: <2026-07-02 Thu>
  ** NEXT [#B] Review PR backlog :@computer:important:
  ** TODO [#A] Midyear self-eval :@computer:important:
     SCHEDULED: <2026-07-03 Fri>
  ** WAITING Travel desk reply :@email:urgent:
     Some note line.
  ** DONE [#C] Old finished thing :@computer:
     CLOSED: [2026-06-20]

  * Home
  ** NEXT Water the plants :@home:
ORG

def with_store
  Dir.mktmpdir do |dir|
    org = File.join(dir, "gtd.org")
    archive = File.join(dir, "archive.org")
    File.write(org, FIXTURE_ORG)
    yield Tui::Store.new(org: org, archive: archive), org, archive
  end
end

def find_item(store, text)
  store.items.find { |i| i.title.include?(text) } or raise "no item: #{text}"
end
