# frozen_string_literal: true

require "minitest/autorun"
require "open3"
require "rbconfig"

class TestTermFormRequireBoundary < Minitest::Test
  ROOT = File.expand_path("..", __dir__)

  def test_require_smoke_has_no_tasks_tui_or_ansi_dependency
    script = <<~'RUBY'
      require "term_form"
      forbidden = $LOADED_FEATURES.grep(%r{/lib/(?:tasks|tui)(?:/|\.rb\z)|/lib/ansi\.rb\z})
      abort "forbidden dependencies: #{forbidden.join(", ")}" unless forbidden.empty?
      abort "missing core" unless defined?(TermForm::Form) && defined?(TermForm::RenderModel)
      abort "missing text fields" unless defined?(TermForm::Fields::Input) && defined?(TermForm::Fields::TextArea)
    RUBY

    stdout, stderr, status = Open3.capture3(
      RbConfig.ruby, "-I#{File.join(ROOT, "lib")}", "-e", script,
      chdir: ROOT,
    )

    assert status.success?, "require smoke failed\nstdout: #{stdout}\nstderr: #{stderr}"
  end
end
