# frozen_string_literal: true

$LOAD_PATH.unshift File.expand_path("../lib", __dir__)

require "term_form"

# A deliberately small renderer for line-oriented hosts. It consumes only the
# semantic render model: no ANSI, cursor addressing, application state, or
# terminal geometry is required.
class PlainRenderer
  def render(model)
    model.groups.flat_map do |group|
      lines = group.label.to_s.empty? ? [] : [group.label.to_s]
      group.rows.each do |row|
        lines << render_row(row)
        lines.concat(Array(row.metadata[:options]).map { |option| render_option(option) })
        lines.concat(row.errors.map { |error| "    ! #{error}" })
        hint = row.metadata[:hint]
        lines << "    · #{hint}" if hint
      end
      lines
    end.join("\n")
  end

  private

  def render_row(row)
    focus = row.focused? ? ">" : " "
    state = row.error ? "!" : row.dirty? ? "*" : " "
    value = row.metadata.fetch(:text, row.value)
    "#{focus}#{state} #{row.label}: #{value}"
  end

  def render_option(option)
    cursor = option[:highlighted] ? ">" : " "
    selected = option[:selected] ? "[x]" : "[ ]"
    "    #{cursor} #{selected} #{option[:label]}"
  end
end

form = TermForm::Form.new(
  groups: [
    TermForm::Group.new(
      key: :profile,
      label: "Profile",
      fields: [
        TermForm::Fields::Input.new(
          key: :name,
          label: "Name",
          required: true,
          metadata: { hint: "required" },
        ),
        TermForm::Fields::Select.new(
          key: :role,
          label: "Role",
          value: "author",
          options: [["author", "Author"], ["reviewer", "Reviewer"]],
          searchable: false,
          metadata: { hint: "Return opens choices" },
        ),
      ],
    ),
  ],
)

renderer = PlainRenderer.new
form.validate
puts "Initial validation"
puts renderer.render(form.render_model)

form.handle(TermForm::Event.paste("Ada Lovelace"))
request = form.handle(TermForm::Event.key(:tab)).request
form.accept_commit(values: { name: request.proposed_value }, token: request.token)
form.handle(TermForm::Event.key(:return))

puts "\nAfter an in-memory commit"
puts renderer.render(form.render_model)
