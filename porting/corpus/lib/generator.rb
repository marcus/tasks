# frozen_string_literal: true

require "json"
require "optparse"
require "pathname"
require_relative "../../../lib/tasks/cli_commands"

module PortingCorpus
  ROOT = Pathname(__dir__).join("../../..").expand_path.freeze
  FIXTURES = ROOT.join("porting/fixtures").freeze
  DEFAULT_SEED = 20_260_802
  EXCLUDED_FIXTURES = {
    "malformed/cross-file-duplicate-id" =>
      "the runner probe emits duplicate revisions.resources keys, which compare/validate rejects",
  }.freeze
  FLAG_SOURCES = [ROOT.join("bin/tasks"), ROOT.join("lib/tasks/task_queries.rb")].freeze

  Scenario = Struct.new(:label, :fixture, :args, :flags, :notes, :env, keyword_init: true)
  Profile = Struct.new(:fixture, :args, :scenarios, keyword_init: true)

  # Semantic arguments cannot be inferred from dispatch metadata. This table is
  # deliberately only recipes: the authoritative command inventory, aliases,
  # and JSON support are always read from Tasks::CliCommands below.
  TASK = "Draft the quarterly planning outline"
  TODO = "Reply to the supplier about the renewal quote"
  PROPOSED = "Proposed: add a second backup target"
  PROJECT = "Project with mixed holds"
  EMPTY_PROJECT = "Empty project"

  COMMON_MUTATION_FLAGS = [
    ["dry-run", ["--dry-run"]],
    ["include-done", ["--include-done"]],
    ["dry-run-json", ["--dry-run", "--json"]],
  ].freeze

  def self.s(label, args, fixture: nil, flags: [], notes: nil, env: nil)
    Scenario.new(label: label, fixture: fixture, args: args, flags: flags,
                 notes: notes, env: env)
  end

  PROFILES = {
    "agenda" => Profile.new(fixture: "valid/full-field-matrix", args: %w[agenda], scenarios: []),
    "next" => Profile.new(fixture: "valid/small-gtd", args: %w[next], scenarios: []),
    "quadrants" => Profile.new(fixture: "valid/full-field-matrix", args: %w[quadrants], scenarios: []),
    "inbox" => Profile.new(fixture: "valid/small-gtd", args: %w[inbox], scenarios: []),
    "projects" => Profile.new(fixture: "valid/project-rollup-edges", args: %w[projects], scenarios: []),
    "list" => Profile.new(fixture: "valid/full-field-matrix", args: %w[list], scenarios: [
      s("scopes", %w[list --all --json], flags: %w[--all --json]),
      s("filters", %w[list --open --body /link +urgent -A @computer], flags: %w[--open --body]),
      s("unavailable", %w[list --unavailable --recurring], flags: %w[--unavailable --recurring]),
      s("delegation", %w[list --delegated], flags: %w[--delegated]),
      s("agent-ready", %w[list --agent-ready --json], flags: %w[--agent-ready --json]),
      s("closed", %w[list --done], flags: %w[--done]),
      s("archived", %w[list --archived], fixture: "valid/archive-pair", flags: %w[--archived]),
      s("deferred", %w[list --deferred], flags: %w[--deferred]),
      s("someday", %w[list --someday], flags: %w[--someday]),
      s("proposed", %w[list --proposed], flags: %w[--proposed]),
      s("on-hold-alias", %w[list --on-hold], flags: %w[--on-hold]),
      s("short-open-body", %w[list -o -b /link], flags: %w[-o -b]),
      s("short-done", %w[list -d], flags: %w[-d]),
      s("short-archived", %w[list -x], fixture: "valid/archive-pair", flags: %w[-x]),
      s("short-all-recurring", %w[list -a -R], flags: %w[-a -R]),
      s("short-deferred", %w[list -D], flags: %w[-D]),
    ]),
    "show" => Profile.new(fixture: "valid/small-gtd", args: ["show", TASK], scenarios: [
      s("include-done", ["show", "Take the intro pottery class", "--include-done", "--json"],
        flags: %w[--include-done --json]),
    ]),
    "links" => Profile.new(fixture: "valid/link-corpus", args: %w[links], scenarios: [
      s("all-system", %w[links --all --system github --json], flags: %w[--all --system --json]),
      s("short-all-system", %w[links -a -s example.invalid], flags: %w[-a -s]),
    ]),
    "open" => Profile.new(fixture: "valid/link-corpus", args: ["open", "Org labelled link", "--print"], scenarios: [
      s("print-json", ["open", "Org labelled link", "--print", "--json"], flags: %w[--print --json]),
      s("system", ["open", "Org labelled link", "--system", "example.invalid", "--print"], flags: %w[--system --print]),
      s("short-system-print", ["open", "Org labelled link", "-s", "example.invalid", "-p"], flags: %w[-s -p]),
    ]),
    "id" => Profile.new(fixture: "valid/small-gtd", args: ["id", TASK], scenarios: []),
    "check" => Profile.new(fixture: "valid/small-gtd", args: %w[check], scenarios: [s("all-files", %w[check --all-files --json], fixture: "valid/archive-pair", flags: %w[--all-files --json])]),
    "project create" => Profile.new(fixture: "valid/project-rollup-edges", args: ["project", "create", "Generated project"], scenarios: [
      s("dry-run-json", ["project", "create", "Dated project", "--dry-run", "--json"], flags: %w[--dry-run --json]),
    ]),
    "project show" => Profile.new(fixture: "valid/project-rollup-edges", args: ["project", "show", PROJECT], scenarios: []),
    "project complete" => Profile.new(fixture: "valid/project-rollup-edges", args: ["project", "complete", PROJECT], scenarios: [
      s("dry-run", ["project", "complete", PROJECT, "--dry-run"], flags: %w[--dry-run]),
      s("dry-run-json", ["project", "complete", PROJECT, "--dry-run", "--json"], flags: %w[--dry-run --json]),
    ]),
    "project archive" => Profile.new(fixture: "valid/project-rollup-edges", args: ["project", "archive", EMPTY_PROJECT], scenarios: [s("force-dry", ["project", "archive", PROJECT, "--force", "--dry-run", "--json"], flags: %w[--force --dry-run --json])]),
    "project rename" => Profile.new(fixture: "valid/project-rollup-edges", args: ["project", "rename", EMPTY_PROJECT, "Renamed generated project"], scenarios: [
      s("dry-run", ["project", "rename", EMPTY_PROJECT, "Renamed generated project", "--dry-run"], flags: %w[--dry-run]),
      s("dry-run-json", ["project", "rename", EMPTY_PROJECT, "Renamed generated project", "--dry-run", "--json"], flags: %w[--dry-run --json]),
    ]),
    "capture" => Profile.new(fixture: "valid/small-gtd", args: ["capture", "Generated task"], scenarios: [
      s("full", ["capture", "Generated full task", "--due", "2026-09-09", "--priority", "A", "--tag", "generated", "--context", "@computer", "--no-host-context", "--state", "TODO", "--project", "Next Actions", "--recur", "weekly", "--lead", "2d", "--note", "generated note", "--dry-run", "--json"], flags: %w[--due --priority --tag --context --no-host-context --state --project --recur --lead --note --dry-run --json]),
      s("under", ["capture", "Generated child", "--under", TASK, "--dry-run"], flags: %w[--under --dry-run]),
      s("timed", ["capture", "Generated timed", "--due", "2026-11-01 01:30", "--due-timezone", "America/Los_Angeles", "--due-fold", "later", "--scheduled", "2026-09-01 09:30", "--scheduled-floating", "--dry-run"], flags: %w[--due --due-timezone --due-fold --scheduled --scheduled-floating --dry-run]),
      s("flag-aliases", ["capture", "Generated aliases", "--deadline", "2026-09-09 09:30", "--due-floating", "--sched", "2026-09-01 09:30", "--scheduled-timezone", "Europe/London", "--scheduled-fold", "earlier", "--pri", "B", "--ctx", "@computer", "--repeat", "weekly", "--dry-run"], flags: %w[--deadline --due-floating --sched --scheduled-timezone --scheduled-fold --pri --ctx --repeat --dry-run]),
    ]),
    "propose" => Profile.new(fixture: "valid/small-gtd", args: ["propose", "Generated proposal"], scenarios: [
      s("full", ["propose", "Generated proposal", "--due", "2026-09-09", "--lead", "2d", "--priority", "B", "--tag", "generated", "--context", "@computer", "--no-host-context", "--project", "Next Actions", "--note", "rationale", "--dry-run", "--json"], flags: %w[--due --lead --priority --tag --context --no-host-context --project --note --dry-run --json]),
      s("temporal-aliases", ["propose", "Generated timed proposal", "--deadline", "2026-09-09 09:30", "--due-floating", "--sched", "2026-09-01 09:30", "--scheduled-timezone", "Europe/London", "--scheduled-fold", "earlier", "--pri", "B", "--ctx", "@computer", "--under", TASK, "--dry-run"], flags: %w[--deadline --due-floating --sched --scheduled-timezone --scheduled-fold --pri --ctx --under --dry-run]),
    ]),
    "approve" => Profile.new(fixture: "valid/full-field-matrix", args: ["approve", PROPOSED], scenarios: []),
    "reject" => Profile.new(fixture: "valid/full-field-matrix", args: ["reject", PROPOSED], scenarios: [s("note-json", ["reject", PROPOSED, "--note", "not now", "--json"], flags: %w[--note --json])]),
    "delegate" => Profile.new(fixture: "valid/small-gtd", args: ["delegate", TASK, "research"], scenarios: [s("human-assignee", ["delegate", TASK, "--to", "agent@example.test", "--keep-state", "--json"], flags: %w[--to --keep-state --json])]),
    "undelegate" => Profile.new(fixture: "valid/full-field-matrix", args: ["undelegate", "Claimed by a worker"], scenarios: []),
    "workref" => Profile.new(fixture: "valid/full-field-matrix", args: ["workref", "Claimed by a worker", "https://example.test/work/1"], scenarios: [s("worker", ["workref", "Claimed by a worker", "off", "--worker", "harness/model/session-0001", "--json"], flags: %w[--worker --json])]),
    "claim" => Profile.new(fixture: "valid/full-field-matrix", args: ["claim", "Offered to the agent pool", "--worker", "generated/worker"], scenarios: []),
    "release" => Profile.new(fixture: "valid/full-field-matrix", args: ["release", "Claimed by a worker", "--worker", "harness/model/session-0001"], scenarios: [s("note", ["release", "Claimed by a worker", "--worker", "harness/model/session-0001", "--note", "generated blocker", "--json"], flags: %w[--worker --note --json]), s("force", ["release", "Claimed by a worker", "--force"], flags: %w[--force])]),
    "done" => Profile.new(fixture: "valid/small-gtd", args: ["done", TASK], scenarios: COMMON_MUTATION_FLAGS.map { |l, f| s(l, ["done", TASK, *f], flags: f) }),
    "due" => Profile.new(fixture: "valid/small-gtd", args: ["due", TASK, "2026-09-09"], scenarios: [s("timed", ["due", TASK, "2026-11-01 01:30", "--timezone", "America/Los_Angeles", "--fold", "later", "--include-done", "--dry-run", "--json"], flags: %w[--timezone --fold --include-done --dry-run --json]), s("floating", ["due", TASK, "2026-09-09 09:30", "--floating"], flags: %w[--floating])]),
    "schedule" => Profile.new(fixture: "valid/small-gtd", args: ["schedule", TASK, "2026-09-09"], scenarios: [s("timed", ["schedule", TASK, "2026-09-09 09:30", "--timezone", "Europe/London", "--include-done", "--dry-run", "--json"], flags: %w[--timezone --include-done --dry-run --json]), s("floating", ["schedule", TASK, "2026-09-09 09:30", "--floating", "--fold", "earlier"], flags: %w[--floating --fold])]),
    "undate" => Profile.new(fixture: "valid/full-field-matrix", args: ["undate", "Both a start date and a due date"], scenarios: [s("kind", ["undate", "Both a start date and a due date", "--kind", "deadline", "--include-done", "--dry-run", "--json"], flags: %w[--kind --include-done --dry-run --json])]),
    "state" => Profile.new(fixture: "valid/small-gtd", args: ["state", TASK, "WAITING"], scenarios: COMMON_MUTATION_FLAGS.map { |l, f| s(l, ["state", TASK, "WAITING", *f], flags: f) }),
    "cancel" => Profile.new(fixture: "valid/small-gtd", args: ["cancel", TASK], scenarios: [s("note", ["cancel", TASK, "--note", "generated reason", "--include-done", "--dry-run", "--json"], flags: %w[--note --include-done --dry-run --json])]),
    "priority" => Profile.new(fixture: "valid/small-gtd", args: ["priority", TASK, "C"], scenarios: COMMON_MUTATION_FLAGS.map { |l, f| s(l, ["priority", TASK, "none", *f], flags: f) }),
    "retitle" => Profile.new(fixture: "valid/small-gtd", args: ["retitle", TASK, "Generated replacement title"], scenarios: COMMON_MUTATION_FLAGS.map { |l, f| s(l, ["retitle", TASK, "Generated replacement title", *f], flags: f) }),
    "tag" => Profile.new(fixture: "valid/small-gtd", args: ["tag", TASK, "+generated", "-important"], scenarios: COMMON_MUTATION_FLAGS.map { |l, f| s(l, ["tag", TASK, "+generated", *f], flags: f) }),
    "note" => Profile.new(fixture: "valid/small-gtd", args: ["note", TASK, "Generated note"], scenarios: COMMON_MUTATION_FLAGS.map { |l, f| s(l, ["note", TASK, "Generated note", *f], flags: f) }),
    "move" => Profile.new(fixture: "valid/small-gtd", args: ["move", TASK, "Next Actions"], scenarios: [s("under-before", ["move", TODO, "--under", TASK, "--before", TASK, "--dry-run", "--json"], flags: %w[--under --before --dry-run --json]), s("top", ["move", TASK, "--top", "--include-done"], flags: %w[--top --include-done])]),
    "delete" => Profile.new(fixture: "valid/full-field-matrix", args: ["delete", "Todo with priority C"], scenarios: [s("cascade", ["delete", "Parent task with children", "--cascade", "--include-done", "--dry-run", "--json"], flags: %w[--cascade --include-done --dry-run --json])]),
    "recur" => Profile.new(fixture: "valid/full-field-matrix", args: ["recur", "Deadline, date only", "weekly"], scenarios: [s("preview", ["recur", "Weekly from completion", "--count", "3", "--json"], flags: %w[--count --json]), s("explain", %w[recur --explain every\ mon,wed --count 4 --json], flags: %w[--explain --count --json]), s("set", ["recur", "Deadline, date only", "2w", "--from", "completion", "--on", "2026-09-09", "--include-done", "--dry-run"], flags: %w[--from --on --include-done --dry-run])]),
    "lead" => Profile.new(fixture: "valid/full-field-matrix", args: ["lead", "Deadline, date only", "2d"], scenarios: COMMON_MUTATION_FLAGS.map { |l, f| s(l, ["lead", "Deadline, date only", "2d", *f], flags: f) }),
    "defer" => Profile.new(fixture: "valid/small-gtd", args: ["defer", TASK, "2026-09-09"], scenarios: [s("timed", ["defer", TASK, "2026-09-09 09:30", "--timezone", "Europe/London", "--include-done", "--dry-run", "--json"], flags: %w[--timezone --include-done --dry-run --json]), s("floating", ["defer", TASK, "2026-09-09 09:30", "--floating", "--fold", "earlier"], flags: %w[--floating --fold])]),
    "someday" => Profile.new(fixture: "valid/small-gtd", args: ["someday", TASK], scenarios: COMMON_MUTATION_FLAGS.map { |l, f| s(l, ["someday", TASK, *f], flags: f) }),
    "activate" => Profile.new(fixture: "valid/project-rollup-edges", args: ["activate", "Own hold in a mixed project"], scenarios: COMMON_MUTATION_FLAGS.map { |l, f| s(l, ["activate", "Own hold in a mixed project", *f], flags: f) }),
    "archive" => Profile.new(fixture: "valid/full-field-matrix", args: %w[archive], scenarios: []),
    "repair" => Profile.new(fixture: "malformed/missing-id-single", args: %w[repair], scenarios: [s("dry-run", %w[repair --dry-run --json], flags: %w[--dry-run --json])]),
    "undo" => Profile.new(fixture: "adversarial/journal-undo-redo-delete", args: %w[undo], scenarios: []),
    "redo" => Profile.new(fixture: "adversarial/journal-redo-pending-delete", args: %w[redo], scenarios: []),
    "config" => Profile.new(fixture: "valid/small-gtd", args: %w[config], scenarios: []),
    "help" => Profile.new(fixture: "valid/empty-store", args: %w[help], scenarios: []),
    # No provider is invoked: missing prompt is an intentional executable error case.
    "-p" => Profile.new(fixture: "valid/empty-store", args: %w[-p], scenarios: [s("provider-model-missing-prompt", %w[-p --provider generated --model generated], flags: %w[--provider --model])]),
    # The early adapter's path-sensitive success behavior belongs in its hand-written
    # slice. This still makes the registry entry executable and records usage errors.
    "merge-driver" => Profile.new(fixture: "valid/empty-store", args: %w[merge-driver], scenarios: []),
  }.freeze

  class Generator
    attr_reader :seed, :coverage

    def initialize(seed: DEFAULT_SEED)
      @seed = Integer(seed)
      @random = Random.new(@seed)
      @coverage = Hash.new { |h, k| h[k] = { flags: [], cases: 0 } }
    end

    def cases
      commands = Tasks::CliCommands::ALL
      missing = commands.map(&:name) - PROFILES.keys
      extra = PROFILES.keys - commands.map(&:name)
      raise "generator profiles missing registry commands: #{missing.join(', ')}" unless missing.empty?
      raise "generator profiles not present in registry: #{extra.join(', ')}" unless extra.empty?

      generated = commands.flat_map { |command| command_cases(command) }
      generated.concat(fixture_sweep)
      generated.each { |c| validate_case!(c) }
      assert_flag_coverage!
      duplicate_ids = generated.group_by { |c| c.fetch(:case_id) }.select { |_id, rows| rows.size > 1 }.keys
      raise "duplicate generated case ids: #{duplicate_ids.join(', ')}" unless duplicate_ids.empty?
      generated.sort_by { |c| [@random.rand, c.fetch(:case_id)] }
    end

    private

    # Flags are not represented in CliCommands yet, so derive their spellings
    # from the parser forms the CLI actually uses. Profiles remain responsible
    # for meaningful values and combinations; this guard makes a new parser flag
    # fail generation until one of those scenarios covers it.
    def assert_flag_coverage!
      discovered = FLAG_SOURCES.flat_map { |path| flags_in(path.read) }.uniq.sort
      exercised = @coverage.values.flat_map { |entry| entry[:flags] }.uniq.sort
      missing = discovered - exercised
      raise "generated scenarios missing CLI flags: #{missing.join(', ')}" unless missing.empty?
    end

    def flags_in(source)
      flags = []
      source.scan(/(?:take_flags|extract_value|extract_repeatable_flag|args\.include\?|args\.index)\((.*?)\)/m) do |match|
        flags.concat(flag_literals(match.first))
      end
      source.scan(/when\s+([^\n]+)/) { |match| flags.concat(flag_literals(match.first)) }
      source.scan(/\w+_flag:\s*["'](-{1,2}[A-Za-z][A-Za-z-]*)["']/) { |match| flags << match.first }
      flags
    end

    def flag_literals(text) = text.scan(/["'](-{1,2}[A-Za-z][A-Za-z-]*)["']/).flatten

    def command_cases(command)
      profile = PROFILES.fetch(command.name)
      scenarios = [
        Scenario.new(label: "human", fixture: profile.fixture, args: profile.args, flags: [], notes: "base human-output path"),
        Scenario.new(label: "missing-args", fixture: profile.fixture, args: command.words, flags: [], notes: "invalid arity/argument path"),
        Scenario.new(label: "unknown-flag", fixture: profile.fixture, args: [*profile.args, "--corpus-unknown"], flags: [], notes: "unknown flag refusal"),
      ]
      if command.json?
        scenarios << Scenario.new(label: "json", fixture: profile.fixture, args: [*profile.args, "--json"], flags: ["--json"], notes: "structured-output path")
      end
      command.aliases.each_with_index do |spelling, index|
        alias_words = spelling.split(" ")
        tail = profile.args.drop(command.words.length)
        scenarios << Scenario.new(label: "alias-#{index + 1}", fixture: profile.fixture,
                                   args: [*alias_words, *tail], flags: [], notes: "registered alias #{spelling}")
      end
      if command.gated?
        scenarios << Scenario.new(label: "schema-refusal", fixture: "compat/future-schema-v3",
                                   args: profile.args, flags: [], notes: "unsupported schema refusal")
        if command.json?
          scenarios << Scenario.new(label: "schema-refusal-json", fixture: "compat/future-schema-v3",
                                     args: [*profile.args, "--json"], flags: ["--json"], notes: "structured unsupported schema refusal")
        end
      end
      scenarios.concat(profile.scenarios)
      scenarios.uniq { |s| [s.fixture || profile.fixture, s.args, s.env] }.map do |scenario|
        build_case(command, scenario, profile.fixture)
      end
    end

    def build_case(command, scenario, default_fixture)
      slug = command.name.gsub(/[^a-z0-9]+/, "-").sub(/-+\z/, "")
      id = "gen.#{slug}.#{scenario.label}"
      @coverage[command.name][:cases] += 1
      @coverage[command.name][:flags] |= scenario.flags
      result = {
        case_id: id,
        fixture: scenario.fixture || default_fixture,
        argv: scenario.args,
        notes: "generated seed=#{seed}; command=#{command.name}; #{scenario.notes || scenario.label}",
      }
      result[:env] = scenario.env if scenario.env
      result
    end

    def fixture_sweep
      fixture_ids.shuffle(random: @random).map do |fixture|
        slug = fixture.tr("/", "-")
        {
          case_id: "gen.fixture.#{slug}", fixture: fixture, argv: %w[check --json --all-files],
          notes: "generated seed=#{seed}; full fixture sweep through the diagnostic command",
        }
      end
    end

    def fixture_ids
      Dir.glob(FIXTURES.join("*/*/store").to_s).map do |path|
        Pathname(path).parent.relative_path_from(FIXTURES).to_s
      end.sort - EXCLUDED_FIXTURES.keys
    end

    def validate_case!(entry)
      id = entry.fetch(:case_id)
      raise "invalid case id: #{id}" unless id.match?(/\A[a-z0-9][a-z0-9._-]*\z/)
      fixture = FIXTURES.join(entry.fetch(:fixture), "store").cleanpath
      raise "fixture escapes fixture root: #{fixture}" unless fixture.to_s.start_with?("#{FIXTURES}/")
      raise "fixture does not exist: #{entry.fetch(:fixture)}" unless fixture.directory?
      argv = entry.fetch(:argv)
      raise "empty argv: #{id}" unless argv.is_a?(Array) && argv.all? { |v| v.is_a?(String) }
    end
  end

  class CLI
    def self.run(argv, stdout: $stdout, stderr: $stderr)
      options = { seed: DEFAULT_SEED }
      parser = OptionParser.new do |o|
        o.banner = "usage: porting/corpus/generate --out FILE [--seed N]"
        o.on("--out FILE", "write the generated JSONL case list") { |v| options[:out] = v }
        o.on("--seed N", Integer, "deterministic seed (default: #{DEFAULT_SEED})") { |v| options[:seed] = v }
        o.on("--help", "show this help") { stdout.puts o; return 0 }
      end
      parser.parse!(argv)
      raise OptionParser::MissingArgument, "--out is required" unless options[:out]
      raise OptionParser::InvalidArgument, "unexpected arguments: #{argv.join(' ')}" unless argv.empty?

      out = Pathname(options[:out]).expand_path
      protected = ROOT.join("porting/runners/cases").expand_path
      if out.to_s == protected.to_s || out.to_s.start_with?("#{protected}/")
        raise OptionParser::InvalidArgument, "refusing to overwrite hand-written runner cases"
      end

      generator = Generator.new(seed: options[:seed])
      cases = generator.cases
      out.dirname.mkpath
      bytes = cases.map { |entry| JSON.generate(entry) }.join("\n") + "\n"
      out.write(bytes, mode: "w", encoding: "UTF-8")
      flags = generator.coverage.values.flat_map { |v| v[:flags] }.uniq.size
      stderr.puts "generated #{cases.size} cases for #{generator.coverage.size} commands (#{flags} distinct flag spellings) -> #{out}"
      0
    rescue OptionParser::ParseError, ArgumentError => e
      stderr.puts "error: #{e.message}"
      stderr.puts parser
      2
    end
  end
end
