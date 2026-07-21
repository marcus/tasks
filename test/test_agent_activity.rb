# frozen_string_literal: true

require_relative "test_helper"
require "tui/agent_activity"
require "tui/agent_queue"

class TestAgentActivity < Minitest::Test
  A = Tui::Ansi
  Activity = Tui::AgentActivity
  Snapshot = Tui::AgentQueue::Snapshot

  def request(id:, status:, prompt: "capture milk", output: "done", error: nil,
              provider: "claude-cli", model: "sonnet", started_at: 10.0, finished_at: 18.0)
    Snapshot.new(
      id: id,
      prompt: prompt,
      entry: LLM::Entry.new(provider: provider, model: model),
      status: status,
      queued_at: 5.0,
      started_at: started_at,
      finished_at: finished_at,
      output: output,
      exit_status: status == :succeeded ? 0 : nil,
      error: error
    )
  end

  def plain(content) = content[:lines].map { |line| A.strip(line) }.join("\n")

  def test_renders_prompt_result_entry_status_and_elapsed_per_request
    requests = [
      request(id: 1, status: :succeeded),
      request(id: 2, status: :running, prompt: "move flight", output: "thinking",
              provider: "hermes", model: "qwen", finished_at: nil),
      request(id: 3, status: :queued, prompt: "review inbox", output: "",
              started_at: nil, finished_at: nil),
    ]
    content = Activity.content(requests: requests, now: 52.0, width: 80)
    text = plain(content)

    assert_equal "Agent activity · 1 running · 1 queued · 1 finished", content[:title]
    assert_includes text, "✓ #1 · claude:sonnet · succeeded · 8s"
    assert_includes text, "⠸ #2 · hermes:qwen · running · 42s"
    assert_includes text, "○ #3 · claude:sonnet · queued #1"
    assert_includes text, "request  capture milk"
    assert_includes text, "result   done"
    assert_includes text, "result   (waiting)"
    assert_equal content[:lines].size, content[:filter_groups].size
    assert_equal 3, content[:filter_groups].uniq.size
  end

  def test_failure_and_empty_or_live_output_remain_distinct
    requests = [
      request(id: 1, status: :failed, output: "", error: "agent exited 7"),
      request(id: 2, status: :cancelled, output: "partial", error: "cancelled"),
      request(id: 3, status: :running, output: "", finished_at: nil),
    ]
    text = plain(Activity.content(requests: requests, now: 20.0, width: 50))

    assert_includes text, "✗ #1"
    assert_includes text, "result   (no output)"
    assert_includes text, "error    agent exited 7"
    assert_includes text, "– #2"
    assert_includes text, "partial"
    assert_includes text, "(working; no output yet)"
  end

  def test_wraps_unicode_prompt_and_transcript_to_requested_width
    content = Activity.content(
      requests: [request(id: 1, status: :succeeded,
                         prompt: "界" * 40, output: "✨ result " * 20)],
      now: 20.0,
      width: 40
    )

    assert_operator content[:lines].size, :>, 4
    content[:lines].each { |line| assert_operator A.vislen(line), :<=, 120 }
  end

  def test_empty_history_has_a_clear_message
    content = Activity.content(requests: [], now: 0.0, width: 80)
    assert_includes plain(content), "No agent requests"
  end
end
