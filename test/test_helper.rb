# frozen_string_literal: true

require "minitest/autorun"
require "tmpdir"
require "date"

$LOAD_PATH.unshift File.expand_path("../lib", __dir__)

require "tui/ansi"
require "tui/dates"
require "tui/store"
require "tui/views"
require "tui/frame"

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
