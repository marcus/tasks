# frozen_string_literal: true

require "date"
require_relative "ansi"
require_relative "theme"
require_relative "task_details"
require_relative "views"

module Tui
  # Pure project-detail content builder, the ProjectView counterpart to
  # TaskDetails. It hosts in the same right panel: title, kind, rolled-up open /
  # next counts, stuck flag, soonest date, the section notes, and the open task
  # list. `tasks` is the resolved Array<TaskView> for the project's open task
  # ids; the builder never reaches back into a store or application.
  module ProjectDetails
    A = Ansi
    T = Theme

    module_function

    def build(project, tasks, width, today: Date.today)
      w = [width, 1].max
      lines = A.wrap(project.title, w).map { |line| T.paint(:section, line) }
      lines << ""
      lines << row("kind", project.kind)
      lines << row("open", project.open_count.to_s)
      lines << row("next", project.next_count.to_s)
      lines << row("stuck", T.paint(:warning, "no open next action")) if project.stuck
      lines << row("next date", TaskDetails.date_value(project.next_date, today)) if project.next_date
      lines << row("id", T.paint(:muted, project.id)) if project.id

      notes = project.body.to_s.split("\n").map(&:strip).reject(&:empty?)
      unless notes.empty?
        lines << ""
        lines << T.paint(:detail_label, "notes")
        notes.each { |note| lines.concat(A.wrap(note, [w - 2, 1].max).map { |line| "  #{TaskDetails.note_line(line)}" }) }
      end

      unless tasks.empty?
        lines << ""
        lines << T.paint(:detail_label, "open tasks")
        tasks.each { |task| lines << "  #{task_line(task, today)}" }
      end
      { title: "project", lines: lines }
    end

    def row(label, value)
      "#{T.paint(:detail_label, label.ljust(10))} #{value}"
    end

    # state · priority · title · date, in the shared detail paint idioms.
    def task_line(task, today)
      state = TaskDetails::STATE_SLOT.key?(task.state) ? T.paint(TaskDetails::STATE_SLOT[task.state], task.state) : task.state
      pri = task.priority ? T.paint(:priority, "[##{task.priority}] ") : ""
      date = task.deadline || task.scheduled
      stamp = date ? "  #{T.paint(Views.due_slot((date - today).to_i), date.strftime("%m-%d"))}" : ""
      "#{state} #{pri}#{T.paint(:title, task.title)}#{stamp}"
    end
  end
end
