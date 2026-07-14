# frozen_string_literal: true

require "minitest/autorun"
require "open3"
require "rbconfig"

class TestTasksRequireBoundary < Minitest::Test
  ROOT = File.expand_path("..", __dir__)

  def test_query_layer_is_a_stdlib_only_library_boundary
    script = <<~'RUBY'
      require "tasks/task_queries"
      require "tasks/operation_context"
      abort "missing query types" unless defined?(Tasks::TaskQueries) && defined?(Tasks::TaskFilter)
      abort "missing view types" unless defined?(Tasks::TaskView) && defined?(Tasks::SectionView)
      abort "missing context" unless defined?(Tasks::OperationContext)
      forbidden = $LOADED_FEATURES.grep(%r{/(?:rack|puma)(?:/|\.rb\z)})
      abort "web dependencies leaked into query layer: #{forbidden.join(", ")}" unless forbidden.empty?
    RUBY

    stdout, stderr, status = Open3.capture3(
      RbConfig.ruby, "-I#{File.join(ROOT, "lib")}", "-e", script, chdir: ROOT
    )

    assert status.success?, "require boundary failed\nstdout: #{stdout}\nstderr: #{stderr}"
  end
end
