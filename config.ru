# frozen_string_literal: true

require "json"

$LOAD_PATH.unshift File.expand_path("lib", __dir__)
require "tasks/api/app"

serialized = ENV.fetch("TASKS_API_RESOLVED_CONFIG") do
  abort "tasks-api must be started through bin/tasks-api"
end
config = JSON.parse(serialized)
paths = Tasks::Config::Paths.new(
  org: config.fetch("org"), archive: config.fetch("archive"),
  urgent_days: config.fetch("urgent_days"), max_depth: config.fetch("max_depth"),
  links: config.fetch("links"), link_systems: config.fetch("link_systems")
)

run Tasks::Api::App.build(paths: paths, port: config.fetch("port"))
