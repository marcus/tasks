# frozen_string_literal: true

require "minitest/autorun"
require "open3"
require "rbconfig"

class TestTasksRequireBoundary < Minitest::Test
  ROOT = File.expand_path("..", __dir__)

  def test_query_layer_is_a_stdlib_only_library_boundary
    script = <<~'RUBY'
      require "tasks/application"
      require "tasks/operation_context"
      abort "missing application/query types" unless defined?(Tasks::Application) && defined?(Tasks::StoreFactory) && defined?(Tasks::TaskQueries) && defined?(Tasks::TaskFilter)
      abort "missing view types" unless defined?(Tasks::TaskView) && defined?(Tasks::SectionView)
      abort "missing context" unless defined?(Tasks::OperationContext)
      forbidden = $LOADED_FEATURES.grep(%r{(?:\A|/)(?:rack|puma)(?:/|\.rb\z)})
      abort "web dependencies leaked into query layer: #{forbidden.join(", ")}" unless forbidden.empty?
    RUBY

    assert_isolated_boot("query layer", script)
  end

  def test_cli_launcher_boot_path_is_free_of_web_dependencies
    script = <<~'RUBY'
      require "stringio"

      original_argv = ARGV.dup
      original_stdout = $stdout
      $stdout = StringIO.new
      ARGV.replace(["help"])
      load File.join(ENV.fetch("TASKS_ROOT"), "bin", "tasks")
      abort "CLI did not load query layer" unless defined?(Tasks::TaskQueries)
      ARGV.replace(original_argv)
      $stdout = original_stdout

      forbidden = $LOADED_FEATURES.grep(%r{(?:\A|/)(?:rack|puma)(?:/|\.rb\z)})
      abort "web dependencies leaked into CLI launcher: #{forbidden.join(", ")}" unless forbidden.empty?
    RUBY

    assert_isolated_boot("CLI launcher", script)
  end

  def test_tui_app_and_launcher_boot_paths_are_free_of_web_dependencies
    app_script = <<~'RUBY'
      require "tui/app"
      abort "missing TUI application" unless defined?(Tui::App)

      forbidden = $LOADED_FEATURES.grep(%r{(?:\A|/)(?:rack|puma)(?:/|\.rb\z)})
      abort "web dependencies leaked into TUI application: #{forbidden.join(", ")}" unless forbidden.empty?
    RUBY
    assert_isolated_boot("TUI application", app_script)

    launcher_script = <<~'RUBY'
      require "stringio"

      original_stderr = $stderr
      $stderr = StringIO.new
      begin
        load File.join(ENV.fetch("TASKS_ROOT"), "bin", "tasks-tui")
        abort "TUI launcher entered the interactive event loop"
      rescue SystemExit => error
        abort "unexpected TUI launcher exit: #{error.status.inspect}" unless error.status == 1
      ensure
        $stderr = original_stderr
      end

      abort "TUI launcher did not load application" unless defined?(Tui::App)
      forbidden = $LOADED_FEATURES.grep(%r{(?:\A|/)(?:rack|puma)(?:/|\.rb\z)})
      abort "web dependencies leaked into TUI launcher: #{forbidden.join(", ")}" unless forbidden.empty?
    RUBY
    assert_isolated_boot("TUI launcher", launcher_script)
  end

  private

  def assert_isolated_boot(name, script)
    stdout, stderr, status = Open3.capture3(
      { "TASKS_ROOT" => ROOT },
      RbConfig.ruby, "-I#{File.join(ROOT, "lib")}", "-e", script, chdir: ROOT
    )

    assert status.success?, "#{name} boundary failed\nstdout: #{stdout}\nstderr: #{stderr}"
  end
end
