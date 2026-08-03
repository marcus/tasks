# frozen_string_literal: true

require "minitest/autorun"
require "tmpdir"
require_relative "lib/generator"

class TestCorpusGenerator < Minitest::Test
  def test_generation_is_byte_identical_for_one_seed
    a = PortingCorpus::Generator.new(seed: 42).cases.map { |c| JSON.generate(c) }.join("\n")
    b = PortingCorpus::Generator.new(seed: 42).cases.map { |c| JSON.generate(c) }.join("\n")
    assert_equal a, b
  end

  def test_every_registry_command_is_generated
    generator = PortingCorpus::Generator.new(seed: 42)
    generator.cases
    assert_equal Tasks::CliCommands::ALL.map(&:name).sort, generator.coverage.keys.sort
    assert generator.coverage.values.all? { |entry| entry[:cases] >= 2 }
  end

  def test_every_source_derived_flag_has_a_scenario
    generator = PortingCorpus::Generator.new(seed: 42)
    generator.cases # the source-derived drift guard raises on a missing flag
    assert_equal 68, generator.coverage.values.flat_map { |entry| entry[:flags] }.uniq.size
  end

  def test_every_fixture_is_swept
    cases = PortingCorpus::Generator.new(seed: 42).cases
    swept = cases.select { |c| c[:case_id].start_with?("gen.fixture.") }.map { |c| c[:fixture] }.sort
    expected = Dir.glob(PortingCorpus::FIXTURES.join("*/*/store").to_s).map do |path|
      Pathname(path).parent.relative_path_from(PortingCorpus::FIXTURES).to_s
    end.sort - PortingCorpus::EXCLUDED_FIXTURES.keys
    assert_equal expected, swept
  end

  def test_cli_refuses_hand_written_case_directory
    err = StringIO.new
    status = PortingCorpus::CLI.run(["--out", PortingCorpus::ROOT.join("porting/runners/cases/generated.jsonl").to_s], stderr: err)
    assert_equal 2, status
    assert_includes err.string, "refusing to overwrite"
  end
end
