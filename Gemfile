source "https://rubygems.org"

ruby ">= 3.4.0", "< 5.0"

# Shared civil-time and named-zone support used by every surface.
gem "tzinfo", "~> 2.0"
gem "tzinfo-data", platforms: %i[mingw mswin x64_mingw jruby]

# Loaded only by bin/tasks-api and the Bundler-backed API test gate.
gem "rack", "~> 3.2"
gem "puma", "~> 8.0"

group :test do
  gem "minitest", "~> 5.25"
  gem "openapi_first", "~> 3.4"
  gem "rack-test", "~> 2.2"
end
