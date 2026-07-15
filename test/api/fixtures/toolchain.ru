# frozen_string_literal: true

require "json"
require "rack/lint"

app = lambda do |_env|
  body = JSON.generate(ok: true)
  [200, { "content-type" => "application/json", "content-length" => body.bytesize.to_s }, [body]]
end

run Rack::Lint.new(app)
