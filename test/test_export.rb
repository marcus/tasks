# frozen_string_literal: true

require_relative "test_helper"
require "tui/export"
require "tui/clipboard"

class TestExport < Minitest::Test
  E = Tui::Export

  def export_for(text)
    with_store do |store, _o, _a|
      item = find_item(store, text)
      return yield(item, store.block(item))
    end
  end

  def test_reference_is_the_title
    ref = export_for("Book flight") { |i, _b| E.reference(i) }
    assert_equal "Book flight in Concur", ref
  end

  def test_markdown_full_task
    md = export_for("Book flight") { |i, b| E.markdown(i, b) }
    assert_includes md, "## Book flight in Concur"
    assert_includes md, "- state: NEXT"
    assert_includes md, "- priority: A"
    assert_includes md, "- deadline: 2026-07-02"
    assert_includes md, "- contexts: @computer"
    assert_includes md, "- tags: important, urgent"
    refute_includes md, "DEADLINE: <" # raw org stamps don't leak
    assert md.end_with?("\n")
  end

  def test_markdown_includes_notes
    md = export_for("Travel desk") { |i, b| E.markdown(i, b) }
    assert_includes md, "Some note line."
  end

  def test_markdown_minimal_task_omits_empty_sections
    md = export_for("Water the plants") { |i, b| E.markdown(i, b) }
    assert_includes md, "## Water the plants"
    refute_includes md, "- priority:"
    refute_includes md, "- deadline:"
    refute_includes md, "- tags:"
    assert_includes md, "- contexts: @home"
  end

  def test_clipboard_copy_pipes_text_to_command
    Dir.mktmpdir do |dir|
      sink = File.join(dir, "clip.txt")
      ok = Tui::Clipboard.copy("hello clip\n", cmd: ["sh", "-c", "cat > #{sink}"])
      assert ok
      assert_equal "hello clip\n", File.read(sink)
    end
  end

  def test_clipboard_copy_fails_gracefully_without_tool
    refute Tui::Clipboard.copy("x", cmd: nil)
    refute Tui::Clipboard.copy("x", cmd: ["definitely-not-a-real-cmd-xyz"])
  end
end
