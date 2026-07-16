source "https://rubygems.org"

ruby ">= 3.4.0", "< 5.0"

# Loaded only by bin/tasks-api and the Bundler-backed API test gate. The core
# CLI/TUI remain standard-library-only and do not require this bundle.
gem "rack", "~> 3.2"
gem "puma", "~> 8.0"

group :test do
  gem "minitest", "~> 5.25"
  gem "openapi_first", "~> 3.4"
  gem "rack-test", "~> 2.2"
end
