# frozen_string_literal: true

# Run the whole suite:  ruby test/all.rb
Dir[File.join(__dir__, "test_*.rb")].sort.each { |f| require f }

forbidden = $LOADED_FEATURES.grep(%r{(?:\A|/)(?:rack|puma)(?:/|\.rb\z)})
abort "web dependencies leaked into the core test gate: #{forbidden.join(", ")}" unless forbidden.empty?
