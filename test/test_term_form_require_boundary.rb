# frozen_string_literal: true

require "minitest/autorun"
require "open3"
require "rbconfig"

class TestTermFormRequireBoundary < Minitest::Test
  ROOT = File.expand_path("..", __dir__)
  DEMO = File.join(ROOT, "examples", "term_form_demo.rb")

  def test_require_smoke_has_no_tasks_tui_or_ansi_dependency
    script = <<~'RUBY'
      require "term_form"
      forbidden = $LOADED_FEATURES.grep(%r{/lib/(?:tasks|tui)(?:/|\.rb\z)|/lib/ansi\.rb\z})
      abort "forbidden dependencies: #{forbidden.join(", ")}" unless forbidden.empty?
      abort "missing core" unless defined?(TermForm::Form) && defined?(TermForm::RenderModel)
      abort "missing text fields" unless defined?(TermForm::Fields::Input) && defined?(TermForm::Fields::TextArea)
      abort "missing choice fields" unless defined?(TermForm::Fields::Select) && defined?(TermForm::Fields::MultiSelect)
      abort "missing confirm/date fields" unless defined?(TermForm::Fields::Confirm) && defined?(TermForm::Fields::DateInput)
    RUBY

    stdout, stderr, status = Open3.capture3(
      RbConfig.ruby, "-I#{File.join(ROOT, "lib")}", "-e", script,
      chdir: ROOT,
    )

    assert status.success?, "require smoke failed\nstdout: #{stdout}\nstderr: #{stderr}"
  end

  def test_standalone_plain_renderer_demo_uses_no_task_application_constants
    source = File.read(DEMO, encoding: "UTF-8")
    refute_match(/\bTasks(?:::|\b)/, source)
    refute_match(/\bTui(?:::|\b)/, source)

    stdout, stderr, status = Open3.capture3(RbConfig.ruby, DEMO, chdir: ROOT)

    assert status.success?, "demo failed\nstdout: #{stdout}\nstderr: #{stderr}"
    assert_includes stdout, "Initial validation"
    assert_includes stdout, "! Name:"
    assert_includes stdout, "! is required"
    assert_includes stdout, "After an in-memory commit"
    assert_includes stdout, ">  Role: author"
    assert_includes stdout, "[x] Author"
    assert_includes stdout, "[ ] Reviewer"
    assert_includes stdout, "Return opens choices"
  end
end
