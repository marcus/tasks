# frozen_string_literal: true

# Run the whole suite:  ruby test/all.rb
Dir[File.join(__dir__, "test_*.rb")].sort.each { |f| require f }
