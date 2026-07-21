# frozen_string_literal: true

require_relative "ansi"
require_relative "theme"

module Tui
  # Pure presentation for AgentQueue snapshots. It deliberately renders the
  # exact captured transcript rather than interpreting agent output or task
  # mutations.
  module AgentActivity
    A = Ansi
    T = Theme

    GLYPHS = {
      queued: "○",
      running: "⠸",
      succeeded: "✓",
      failed: "✗",
      cancelled: "–",
    }.freeze

    SLOTS = {
      queued: :muted,
      running: :accent,
      succeeded: :accent,
      failed: :error,
      cancelled: :warning,
    }.freeze

    module_function

    def content(requests:, now:, width:)
      counts = requests.group_by(&:status).transform_values(&:size)
      running = counts.fetch(:running, 0)
      queued = counts.fetch(:queued, 0)
      finished = requests.count(&:finished?)
      title = "Agent activity · #{running} running · #{queued} queued · #{finished} finished"
      if requests.empty?
        return { title: title, lines: [T.paint(:muted, "No agent requests this session.")] }
      end

      lines, filter_groups = render_requests(requests, now, width)
      { title: title, lines: lines, filter_groups: filter_groups }
    end

    def render_requests(requests, now, width)
      content_width = [[Integer(width) - 16, 20].max, 120].min
      queued_position = 0
      lines = []
      filter_groups = []
      requests.each_with_index do |request, index|
        queued_position += 1 if request.status == :queued
        block = []
        block << "" unless index.zero?
        block << header(request, now, queued_position)
        block.concat(labeled_lines("request", request.prompt, content_width))
        block.concat(result_lines(request, content_width))
        lines.concat(block)
        filter_groups.concat(Array.new(block.size, request.id))
      end
      [lines, filter_groups]
    end
    private_class_method :render_requests

    def header(request, now, queued_position)
      status = request.status.to_s
      status += " ##{queued_position}" if request.status == :queued
      elapsed = request.started_at ? " · #{format_elapsed(request.elapsed(now))}" : ""
      label = "#{GLYPHS.fetch(request.status)} ##{request.id} · #{request.entry.ui_label} · #{status}#{elapsed}"
      T.paint(SLOTS.fetch(request.status), label)
    end
    private_class_method :header

    def result_lines(request, width)
      case request.status
      when :queued
        [T.paint(:muted, "  result   (waiting)")]
      when :running
        labeled_lines("result", present_output(request.output, running: true), width, muted: request.output.empty?)
      else
        lines = labeled_lines("result", present_output(request.output), width, muted: request.output.empty?)
        lines.concat(labeled_lines("error", request.error, width)) if request.error && request.status != :cancelled
        lines
      end
    end
    private_class_method :result_lines

    def present_output(output, running: false)
      text = A.normalize(output.to_s).scrub("�").strip
      return running ? "(working; no output yet)" : "(no output)" if text.empty?

      text
    end
    private_class_method :present_output

    def labeled_lines(label, text, width, muted: false)
      wrapped = A.wrap(text.to_s, width)
      wrapped = [""] if wrapped.empty?
      wrapped.each_with_index.map do |line, index|
        prefix = index.zero? ? format("  %-8s ", label) : " " * 11
        rendered = prefix + line
        muted ? T.paint(:muted, rendered) : rendered
      end
    end
    private_class_method :labeled_lines

    def format_elapsed(seconds)
      total = seconds.round
      return "#{total}s" if total < 60

      minutes, secs = total.divmod(60)
      "#{minutes}m#{secs.to_s.rjust(2, "0")}s"
    end
    private_class_method :format_elapsed
  end
end
