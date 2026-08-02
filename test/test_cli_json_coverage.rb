# frozen_string_literal: true

require_relative "test_helper"
require "json"
require "open3"
require "tmpdir"
require "fileutils"
require "tasks/cli_commands"

# The self-enforcing half of the CLI's structured-output contract.
#
# Structured output is a first-class surface, but "does this command answer in
# JSON?" used to be answerable only by reading bin/tasks — and the spec's answer
# was wrong for most of the CLI. Three things now have to agree, and this file
# is where they are made to:
#
#   1. Tasks::CliCommands — the registry bin/tasks dispatches from,
#   2. docs/cli-spec.md's "Structured output (--json) coverage" table, and
#   3. what the command actually prints when you run it with --json.
#
# Adding a command to the CLI therefore fails this suite until its JSON result
# is decided, documented, and demonstrated — or explicitly opted out with a
# stated reason. That is the point: the gap this closes was not a missing flag,
# it was that nothing made the gap visible.
class TestCliJsonCoverage < Minitest::Test
  BIN = File.expand_path("../bin/tasks", __dir__)
  SPEC = File.expand_path("../docs/cli-spec.md", __dir__)

  # The projects fixture (an Inbox, a Projects root with real projects, areas,
  # a done task to sweep) plus one link, so `open`/`links` have something to
  # resolve. One fixture serves every recipe below.
  FIXTURE_RECORDS = PROJECTS_FIXTURE_RECORDS.map do |record|
    next record unless record["id"] == PFIX[:inbox_task]

    record.merge("body" => "See https://example.com/ticket/42 for context.")
  end.freeze
  FIXTURE = Tasks::Format.dump(FIXTURE_RECORDS)

  # The same fixture at a schema version this build does not implement. Every
  # command refuses it identically, which is what makes it usable as a uniform
  # refusal for the enumeration below.
  UNSUPPORTED_STORE = Tasks::Format.dump(
    [{ "type" => "meta", "version" => 3 }] + FIXTURE_RECORDS.drop(1)
  ).freeze

  # How to invoke each command so it reaches its success path. `setup` runs
  # first (output ignored); `args` is then run with `--json` appended.
  Recipe = Struct.new(:args, :setup, :env, keyword_init: true) do
    def setup_commands = setup || []
    def extra_env = env || {}
  end

  def self.recipe(name, args, setup: [], env: {})
    [name, Recipe.new(args: args, setup: setup, env: env)]
  end

  UNFILED = "unfiled capture"       # Inbox task, carries the link
  VENDOR  = "Reply to the vendor"   # open NEXT in the "Tasks" area
  EXPENSE = "File expenses"         # open TODO in the "Tasks" area
  PROJECT = "Empty project"

  RECIPES = [
    # -- Read -----------------------------------------------------------------
    recipe("agenda", %w[agenda]),
    recipe("next", %w[next]),
    recipe("quadrants", %w[quadrants]),
    recipe("inbox", %w[inbox]),
    recipe("projects", %w[projects]),
    recipe("list", %w[list]),
    recipe("show", ["show", VENDOR]),
    recipe("links", %w[links]),
    # A real launch, not --print: TASKS_OPENER is the seam tests observe an
    # open through, so the `opened: true` branch is the one exercised here.
    recipe("open", ["open", UNFILED], env: { "TASKS_OPENER" => "true" }),
    recipe("id", ["id", VENDOR]),
    recipe("check", %w[check]),

    # -- Projects -------------------------------------------------------------
    recipe("project create", ["project", "create", "Fresh project"]),
    recipe("project show", ["project", "show", PROJECT]),
    recipe("project complete", ["project", "complete", "Stuck reno"]),
    recipe("project archive", ["project", "archive", PROJECT]),
    recipe("project rename", ["project", "rename", PROJECT, "Renamed project"]),

    # -- Capture --------------------------------------------------------------
    recipe("capture", ["capture", "a captured task"]),
    recipe("propose", ["propose", "a proposed task"]),

    # -- Update ---------------------------------------------------------------
    recipe("approve", ["approve", "a proposed task"], setup: [["propose", "a proposed task"]]),
    recipe("reject", ["reject", "a proposed task"], setup: [["propose", "a proposed task"]]),
    recipe("delegate", ["delegate", VENDOR, "refine"]),
    recipe("undelegate", ["undelegate", VENDOR], setup: [["delegate", VENDOR, "refine"]]),
    recipe("workref", ["workref", VENDOR, "https://example.com/pr/7"],
           setup: [["delegate", VENDOR, "refine"]]),
    recipe("claim", ["claim", VENDOR, "--worker", "w1"], setup: [["delegate", VENDOR, "refine"]]),
    recipe("release", ["release", VENDOR, "--worker", "w1"],
           setup: [["delegate", VENDOR, "refine"], ["claim", VENDOR, "--worker", "w1"]]),
    recipe("done", ["done", VENDOR]),
    recipe("due", ["due", VENDOR, "2026-09-09"]),
    recipe("schedule", ["schedule", VENDOR, "2026-09-09"]),
    recipe("undate", ["undate", VENDOR], setup: [["due", VENDOR, "2026-09-09"]]),
    recipe("state", ["state", VENDOR, "TODO"]),
    recipe("cancel", ["cancel", VENDOR]),
    recipe("priority", ["priority", VENDOR, "A"]),
    recipe("retitle", ["retitle", VENDOR, "Reply to the supplier"]),
    recipe("tag", ["tag", VENDOR, "+urgent"]),
    recipe("note", ["note", VENDOR, "a note line"]),
    recipe("move", ["move", VENDOR, "Inbox"]),
    recipe("delete", ["delete", VENDOR]),
    recipe("recur", ["recur", VENDOR, "weekly"], setup: [["due", VENDOR, "2026-09-09"]]),
    recipe("lead", ["lead", VENDOR, "2d"], setup: [["due", VENDOR, "2026-09-09"]]),
    recipe("defer", ["defer", VENDOR]),
    recipe("someday", ["someday", VENDOR]),
    recipe("activate", ["activate", VENDOR], setup: [["someday", VENDOR]]),

    # -- Lifecycle ------------------------------------------------------------
    recipe("archive", %w[archive], setup: [["done", EXPENSE]]),
    recipe("undo", %w[undo], setup: [["capture", "something to undo"]]),
    recipe("redo", %w[redo], setup: [["capture", "something to redo"], %w[undo]]),
    recipe("config", %w[config]),
    recipe("help", %w[help]),
  ].to_h.freeze

  # --- The three-way agreement ----------------------------------------------

  def test_registry_matches_the_spec_coverage_table
    documented = spec_coverage_table
    registry = Tasks::CliCommands::ALL.to_h { |command| [command.name, command.json?] }

    missing = registry.keys - documented.keys
    extra = documented.keys - registry.keys
    assert_empty missing, "commands missing from docs/cli-spec.md's coverage table: #{missing.inspect}"
    assert_empty extra, "coverage table documents commands the CLI does not dispatch: #{extra.inspect}"
    assert_equal registry, documented.slice(*registry.keys),
                 "the coverage table disagrees with lib/tasks/cli_commands.rb about --json support"
  end

  def test_help_json_emits_the_registry
    out, _err, status = run_cli(%w[help --json])
    assert_equal 0, status.exitstatus
    payload = JSON.parse(out)
    assert_equal Tasks::CliCommands::ALL.map(&:name), payload.fetch("commands").map { |c| c.fetch("name") }
    payload.fetch("commands").each do |entry|
      command = Tasks::CliCommands.find(entry.fetch("name"))
      assert_equal command.json?, entry.fetch("json")
      assert_equal command.aliases, entry.fetch("aliases")
      if command.json?
        refute entry.key?("json_reason"), "#{command.name}: a supported command needs no opt-out reason"
      else
        refute_empty entry.fetch("json_reason").to_s, "#{command.name}: opt-out must state a reason"
      end
    end
  end

  def test_every_command_has_a_coverage_recipe_or_a_stated_opt_out
    covered = RECIPES.keys.sort
    expected = Tasks::CliCommands::JSON_COMMANDS.map(&:name).sort
    assert_equal expected, covered,
                 "every --json command needs an invocation recipe here; opt-outs need one in " \
                 "Tasks::CliCommands with a reason"

    Tasks::CliCommands::OPT_OUTS.each do |command|
      refute_empty command.reason.to_s, "#{command.name}: --json opt-out must state why"
    end
  end

  # --- What the commands actually print --------------------------------------

  def test_every_json_command_prints_exactly_one_json_document
    failures = []
    RECIPES.each do |name, recipe|
      out, err, status = run_recipe(recipe)
      if status.exitstatus != 0
        failures << "#{name}: exit #{status.exitstatus} (#{err.lines.first.to_s.strip})"
        next
      end
      begin
        JSON.parse(out)
      rescue JSON::ParserError => error
        failures << "#{name}: stdout is not one JSON document (#{error.message[0, 80]}) — got #{out[0, 80].inspect}"
      end
    end
    assert_empty failures, "commands that accept --json but do not answer in JSON:\n  #{failures.join("\n  ")}"
  end

  # The same enumeration, on a refusal instead of a success.
  #
  # This is the half that was missing, and its absence is exactly how the gap
  # drifted in through the json-flags merge: every recipe above proves a
  # command answers in JSON when it WORKS, and nothing proved what it answers
  # when it refuses. `archive`, `undo` and `redo` emitted the documented error
  # object; `capture`, `done`, `delete`, `tag`, `project create` and the rest
  # printed prose to stderr with EMPTY STDOUT and exit 1 — so `tasks done
  # --json` handed an agent an unparseable empty result on precisely the path
  # it most needs to branch on.
  #
  # The schema refusal is the right refusal to enumerate over: it applies to
  # every command uniformly, needs no per-command setup (nothing can be set up
  # on a store nothing will touch), and is the one refusal every surface shares.
  # A command that answers JSON on success but prose on refusal fails here — so
  # this closes the class, not the instances.
  def test_every_json_command_answers_in_json_when_it_refuses_an_unsupported_store
    exempt = Tasks::CliCommands::GATE_EXEMPT.map(&:name)
    failures = []

    RECIPES.each do |name, recipe|
      next if exempt.include?(name)

      out, err, status = run_cli(recipe.args + ["--json"], store: UNSUPPORTED_STORE)
      if status.exitstatus != 1
        failures << "#{name}: exit #{status.exitstatus}, expected 1"
        next
      end
      payload = begin
        JSON.parse(out)
      rescue JSON::ParserError
        failures << "#{name}: refusal stdout is not JSON — got #{out.inspect[0, 60]}"
        next
      end
      unless payload["error"] == "unsupported_schema_version"
        failures << "#{name}: refusal error was #{payload["error"].inspect}"
      end
      failures << "#{name}: refusal names no action" if payload["action"].to_s.empty?
      failures << "#{name}: refusal says nothing on stderr" if err.strip.empty?
    end

    assert_empty failures,
                 "commands that accept --json but do not answer in JSON when they refuse:\n  " \
                 "#{failures.join("\n  ")}"
  end

  # The exemptions are a decision, not an oversight, so they are asserted as
  # one: `check` is where every refusal sends the operator, `config` says where
  # the store is, `help` never opens it. Each must still answer.
  def test_gate_exempt_commands_still_answer_for_an_unsupported_store
    Tasks::CliCommands::GATE_EXEMPT.each do |command|
      refute_empty command.gate_reason.to_s, "#{command.name}: a gate opt-out must state why"
    end

    exempt = Tasks::CliCommands::GATE_EXEMPT.map(&:name) & RECIPES.keys
    assert_equal %w[check config help].sort, exempt.sort

    exempt.each do |name|
      out, _err, status = run_cli(RECIPES.fetch(name).args + ["--json"], store: UNSUPPORTED_STORE)
      refute_equal 2, status.exitstatus, "#{name} must still run"
      JSON.parse(out) # must still be one document, refusal or not
    end
  end

  # A refusal must be structured too, or `--json` degrades to silence on exactly
  # the paths a caller most needs to branch on.
  def test_refusals_under_json_are_error_objects
    out, err, status = run_cli(%w[undo --json])
    assert_equal 1, status.exitstatus
    assert_equal({ "error" => "empty", "action" => "undo", "message" => "nothing to undo" }, JSON.parse(out))
    assert_match(/nothing to undo/, err)

    # `done` on a parent cascades, so this sweeps one root carrying a child:
    # `roots` and `records` must therefore disagree, and `moved_ids` must list
    # the whole subtree rather than just the root.
    out, _err, status = run_cli(["archive", "--json"],
                                setup: [["capture", "child task", "--under", VENDOR],
                                        ["done", VENDOR]])
    assert_equal 0, status.exitstatus
    payload = JSON.parse(out)
    assert_includes payload.fetch("moved_ids").map { |id| title_of(id) }, VENDOR
    assert_equal payload.fetch("moved_ids").length, payload.fetch("records")
    assert_operator payload.fetch("records"), :>, payload.fetch("roots"),
                    "a swept root drags its subtree, so records must exceed roots here"

    # A closed root whose subtree still holds open work refuses; --json says so
    # in an object naming the blockers.
    out, err, status = run_cli(["archive", "--json"],
                               # CANCELLED does not cascade, so the child stays
                               # open under a closed root — exactly the block.
                               setup: [["capture", "child task", "--under", EXPENSE],
                                       ["state", EXPENSE, "CANCELLED"]])
    assert_equal 1, status.exitstatus
    payload = JSON.parse(out)
    assert_equal "conflict", payload.fetch("error")
    assert_equal "open_descendants", payload.fetch("reason")
    assert_equal "archive", payload.fetch("action")
    assert_equal [EXPENSE], payload.fetch("blocked").map { |b| b.fetch("root_title") }
    assert_match(/Archive refused/, err)

    # Stray positionals are a usage error now, on all three lifecycle commands.
    %w[archive undo redo].each do |command|
      _out, err, status = run_cli([command, "stray"])
      assert_equal 1, status.exitstatus
      assert_match(/usage: tasks #{command}/, err)
    end

    out, _err, status = run_cli(["open", VENDOR, "--json"])
    assert_equal 1, status.exitstatus
    assert_equal "not_found", JSON.parse(out).fetch("error")
  end

  def test_open_json_reports_the_link_it_acted_on
    out, _err, status = run_cli(["open", UNFILED, "--json"], env: { "TASKS_OPENER" => "true" })
    assert_equal 0, status.exitstatus
    payload = JSON.parse(out)
    assert_equal "https://example.com/ticket/42", payload.fetch("url")
    assert_equal PFIX[:inbox_task], payload.fetch("id")
    assert payload.fetch("opened"), "a launch without --print reports opened: true"

    # --print is the no-launch form, and says so rather than lying about it.
    out, _err, status = run_cli(["open", UNFILED, "--print", "--json"])
    assert_equal 0, status.exitstatus
    refute JSON.parse(out).fetch("opened")
  end

  def test_undo_and_redo_name_the_mutation_they_moved
    out, _err, status = run_cli(%w[undo --json], setup: [["capture", "a captured task"]])
    assert_equal 0, status.exitstatus
    assert_equal({ "action" => "undo", "label" => "capture: a captured task" }, JSON.parse(out))

    out, _err, status = run_cli(%w[redo --json],
                                setup: [["capture", "a captured task"], %w[undo]])
    assert_equal 0, status.exitstatus
    assert_equal({ "action" => "redo", "label" => "capture: a captured task" }, JSON.parse(out))
  end

  # --- The dispatch table itself ---------------------------------------------

  def test_dispatch_tokens_are_unambiguous
    repeated = Tasks::CliCommands::ALL.flat_map(&:dispatch_tokens).tally.reject { |_token, count| count == 1 }
    # `project create`/`project show`/… deliberately share the one `project`
    # slot; any other repeat means two commands claim the same token.
    assert_equal({ "project" => 5 }, repeated)

    # A sub-verb's alias must never leak into the top-level slot: `tasks new` is
    # not `tasks project new`, and `tasks done` stays the task command it has
    # always been rather than being captured by `project done`.
    assert_nil Tasks::CliCommands.dispatch_for("new"), "\"new\" leaked out of the project slot"
    _out, err, status = run_cli(%w[new])
    assert_equal 1, status.exitstatus
    assert_match(/unknown command/, err)
    assert_equal "done", Tasks::CliCommands.dispatch_for("done")

    # Sub-verb tokens resolve to their full canonical names.
    assert_equal "project create", Tasks::CliCommands.subcommands_of("project").fetch("new")
    assert_equal "project complete", Tasks::CliCommands.subcommands_of("project").fetch("done")
  end

  # bin/tasks dispatches `merge-driver` before the registry is even loaded (Git
  # hands it three paths and nothing else). That escape hatch is the one way to
  # add a command the registry never sees, so the source is scanned for it:
  # a second early branch must declare itself in the registry as `early: true`.
  def test_early_dispatch_branches_are_declared_in_the_registry
    source = File.read(File.expand_path("../bin/tasks", __dir__), encoding: "UTF-8")
    preamble = source.split('require_relative "../lib/tasks/cli_commands"').first
    early = preamble.scan(/ARGV(?:\.first|\[0\]) == "([^"]+)"/).flatten.uniq
    assert_equal Tasks::CliCommands::EARLY_NAMES.sort, early.sort,
                 "an early-dispatch branch bypasses the registry, so its --json contract is unstated"
  end

  def test_unknown_command_still_prints_help_to_stderr
    _out, err, status = run_cli(%w[nonsense])
    assert_equal 1, status.exitstatus
    assert_match(/unknown command: "nonsense"/, err)
    assert_match(/plain-text GTD CLI/, err)
  end

  # `-p`/`--prompt` is excluded deliberately: anything that is not a leading
  # --provider/--model becomes the prompt, so probing it would BUILD AND RUN the
  # configured LLM harness — a real subprocess, possibly a real model call, with
  # no timeout. `merge-driver` is excluded because Git dispatches it before the
  # registry exists. Both are covered by the opt-out assertions instead.
  UNPROBEABLE = %w[-p --prompt merge-driver].freeze

  def test_every_alias_dispatches
    tokens = Tasks::CliCommands::ALL.flat_map(&:dispatch_tokens).uniq - UNPROBEABLE
    tokens.each do |token|
      _out, err, status = run_cli([token, "--nonsense-flag"])
      refute_match(/unknown command/, err, "#{token.inspect} does not dispatch")
      refute_equal 127, status.exitstatus
    end
  end

  # The sub-verb slot has its own drift guard; prove both halves of it.
  def test_project_sub_verbs_dispatch_through_the_registry
    Tasks::CliCommands.subcommands_of("project").each do |token, name|
      _out, err, status = run_cli(["project", token, "--nonsense-flag"])
      refute_match(/unknown project command/, err, "`project #{token}` (#{name}) does not dispatch")
      refute_equal 127, status.exitstatus
    end

    _out, err, status = run_cli(%w[project audit])
    assert_equal 1, status.exitstatus
    assert_match(/unknown project command: "audit"/, err)
  end

  # `-p` cannot answer in JSON, so it says so instead of quietly treating the
  # flag as prompt text and exiting 0 (which is how a scripted caller would get
  # an LLM transcript where it expected a document). Safe to run: the check
  # happens before any harness is built.
  def test_prompt_rejects_json_instead_of_swallowing_it
    _out, err, status = run_cli(["-p", "--json", "water the garden"])
    assert_equal 1, status.exitstatus
    assert_match(/-p has no --json/, err)
  end

  # An abort inside `cmd_prompt` unwinds through a rescue clause that names
  # LLM/AgentContext constants, so those files must already be loaded when it
  # fires. They were not, and the usage line arrived with a NameError backtrace
  # stapled to it — absolute source paths and all, which is both ugly and
  # machine-dependent (td-231878). stderr is the usage line and nothing else.
  def test_prompt_with_no_words_prints_only_the_usage_line
    _out, err, status = run_cli(["-p"])
    assert_equal 1, status.exitstatus
    assert_equal %(usage: tasks -p [--provider NAME] [--model NAME] "do something with my tasks"\n), err
  end

  private

  # Every registry name and its ✅/❌ from the spec's coverage table.
  def spec_coverage_table
    lines = File.read(SPEC, encoding: "UTF-8").lines
    start = lines.index { |line| line.start_with?("| Command | `--json` | Result on success |") }
    refute_nil start, "docs/cli-spec.md has no --json coverage table"
    rows = lines[(start + 2)..].take_while { |line| line.start_with?("|") }
    rows.to_h do |row|
      cells = row.split("|").map(&:strip)
      name = cells[1].delete("`")
      supported = cells[2] == "✅"
      assert_includes %w[✅ ❌], cells[2], "#{name}: coverage column must be ✅ or ❌"
      [name, supported]
    end
  end

  def title_of(id)
    record = FIXTURE_RECORDS.find { |r| r["id"] == id }
    record && record["title"]
  end

  def run_recipe(recipe)
    run_cli(recipe.args + ["--json"], setup: recipe.setup_commands, env: recipe.extra_env)
  end

  # One command in a fresh sandbox, after any setup commands. Uses the same
  # TASKS_FILE/TASKS_ARCHIVE overrides as the other CLI tests, plus a pinned
  # XDG_STATE_HOME so the undo journal is hermetic too.
  def run_cli(args, setup: [], env: {}, store: FIXTURE)
    Dir.mktmpdir do |dir|
      org = File.join(dir, "tasks.jsonl")
      archive = File.join(dir, "archive.jsonl")
      File.write(org, store)
      File.write(archive, Tasks::Format.dump([{ "type" => "meta", "version" => 2 }]))
      base = {
        "TASKS_FILE" => org, "TASKS_ARCHIVE" => archive,
        "XDG_CONFIG_HOME" => File.join(dir, "config"),
        "XDG_STATE_HOME" => File.join(dir, "state")
      }.merge(env)
      setup.each do |command|
        _out, err, status = Open3.capture3(base, "ruby", BIN, *command)
        raise "setup failed: #{command.inspect} — #{err}" unless status.exitstatus.zero?
      end
      out, err, status = Open3.capture3(base, "ruby", BIN, *args)
      [out.force_encoding("UTF-8"), err.force_encoding("UTF-8"), status]
    end
  end
end
