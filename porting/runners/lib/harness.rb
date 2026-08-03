#!/usr/bin/env ruby
# frozen_string_literal: true

# harness.rb — replay a scripted case list against fixture copies, emitting one
# schema-valid observation per case.
#
#   porting/runners/ruby/run porting/runners/cases/phase1.jsonl
#   porting/runners/go/run   --out evidence/go porting/runners/cases/phase1.jsonl
#
# The contract this implements — the case-list format, the copy protocol, the
# pinned environment, the probe, and the same-absolute-path requirement — is
# written down once, language-neutrally, in porting/runners/README.md.
#
# **Why one harness drives both implementations.** Every runner-side value in an
# observation — the pinned environment map, the tree walk, `fixture.root_sha256`,
# the deltas, `sha256_normalized` — is compared field-for-field and is
# HARNESS_ERROR when it differs (see porting/compare/lib/dimensions/cli.rb
# § same_case?). A second, independently written harness would therefore spend
# its first weeks reporting its own base64/sorting/digest divergences as port
# defects. Sharing the harness makes every remaining difference an
# implementation difference, which is the only kind the gate is about.
#
# What is emphatically NOT shared is anything the IMPLEMENTATION answers: the
# probe is per-implementation by construction (README § "Why revision tokens come
# from a probe"), so `revisions`, `pins`, and the resolved `paths` still come
# from the port's own code. A Go defect in any of them still shows up as a
# mismatch.
#
# `Target` is the whole of the per-implementation surface: a name, the argv
# prefix that runs the CLI, and the argv prefix that runs the probe.
#
# Two properties this script owes its callers:
#
#   * **It never touches a live store.** Every case runs against a fresh copy
#     under --work. Before any case runs, the runner asks the CLI where the live
#     store is (`bin/tasks config --json`, read-only) purely so it can refuse to
#     run if --work overlaps it.
#   * **Repeated runs are byte-identical under pins**, except for two fields the
#     harness itself produces: `observation_id` and `metrics.wall_ms`.
#     `--pin-identity` fixes both so a byte comparison can be a plain `diff`.
#
# Exit status: 0 when every case produced an observation and every runner-side
# invariant held; 1 when a case failed an invariant (an ignored pin, or a
# mutation claim that disagrees with the observed file deltas); 2 on a usage or
# configuration error. A non-zero *implementation* exit status is not a runner
# failure — it is the observation.

require "base64"
require "digest"
require "json"
require "optparse"
require "rbconfig"
require "securerandom"
require "fileutils"
require "shellwords"

# The comparator's normalization module, required rather than reimplemented.
# `stream.sha256_normalized` must be the digest of the bytes AFTER exactly the
# copy-root rewrite the comparator applies to short streams — a second,
# lookalike rewrite living here would diverge on the first edge case (macOS's
# /private spelling was the first) and would then reclassify a real difference
# as a copy-root artifact, or the reverse. One implementation, two callers.
require_relative "../../compare/lib/normalize"

module PortingRunner
  SCHEMA_VERSION = 2

  RUNNER_DIR = File.expand_path(File.join(__dir__, ".."))
  REPO_ROOT = File.expand_path("../..", RUNNER_DIR)
  FIXTURES_DIR = File.join(REPO_ROOT, "porting", "fixtures")

  # One implementation's two entry points, plus the name that goes into
  # `implementation.name`. `cli` and `probe` are argv PREFIXES (an interpreter
  # plus a script, or a single compiled binary), so nothing here assumes a
  # language. `prepare` runs once before any case, in the OPERATOR's
  # environment, and is where a compiled implementation builds itself — the
  # pinned PATH the invocation gets has no toolchain on it, by design.
  Target = Struct.new(:name, :cli, :probe, :prepare, keyword_init: true) do
    def prepare! = prepare&.call
  end

  # Deliberately a fixed, boring path rather than a per-run mktemp: the journal
  # index records the store's canonical absolute path, so two runs that are
  # supposed to be byte-identical must land at the same place. See README
  # § "The same-absolute-path requirement".
  DEFAULT_WORK = "/tmp/tasks-conformance"

  FIXTURE_CLASSES = %w[valid compat malformed adversarial].freeze

  # The pinned environment, and the whole of it. Values here are defaults a case
  # may override (including to null, meaning "unset"); nothing outside this map
  # plus PATH is passed to the implementation. PATH is pinned rather than
  # inherited so a stray shim on the operator's PATH cannot change behavior.
  PATH_VALUE = "/usr/bin:/bin:/usr/sbin:/sbin"
  DEFAULT_PINS = {
    "TZ" => "UTC",
    # Pinned alongside TZ because it OUT-RANKS it (Config#pick_timezone reads
    # TASKS_TIMEZONE first and only falls through to TZ detection), and because
    # it is the zone setting the fixture corpus recorded its outcomes under.
    # Pinning the highest-precedence source and the fallback to the same value
    # leaves nothing ambiguous.
    "TASKS_TIMEZONE" => "UTC",
    "LANG" => "en_US.UTF-8",
    "LC_ALL" => "en_US.UTF-8",
    "TASKS_DEVICE" => "fixture",
    "TASKS_PIN_NOW" => "2026-03-14T15:09:26Z",
    "TASKS_PIN_IDS" => "bbbb0001",
    "TASKS_PIN_COALESCE_SCOPE" => "pinned-scope",
    # The per-operation coalescing KEY. Distinct from the scope above and needed
    # for the same reason: it is persisted into journal index.json, so an
    # unpinned one makes two identical delegation runs produce different journal
    # bytes while the store bytes agree. Sixteen hex characters.
    "TASKS_PIN_DELEGATION_KEYS" => "cccc000000000001",
    "TASKS_PIN_HOSTNAME" => "fixture-host",
    "LINES" => "40",
    "COLUMNS" => "100"
  }.freeze

  # Colour. These four names are the complete set the implementation actually
  # consults — read off the source, not assumed from the conventional list:
  #
  #   NO_COLOR    Config#pick_theme (selects the attribute-only "mono" theme)
  #               and Tui::Border.truecolor?
  #   TASKS_THEME Config#pick_theme, and it OUT-RANKS NO_COLOR
  #   COLORTERM   Tui::Border.truecolor?
  #   TERM        Tui::Border.truecolor?, Tui::App#mouse_enabled?
  #
  # CLICOLOR and CLICOLOR_FORCE appear nowhere in bin/ or lib/ and are therefore
  # NOT pinned: pinning a variable the product does not read would be a false
  # assurance in the one document that claims to be exhaustive.
  #
  # They are pinned to UNSET rather than to values, and that is the deliberate
  # half. `unsetenv_others: true` is what makes "unset" a pin and not a wish —
  # the child receives this map and nothing else, so an operator with TERM
  # exported cannot reach the implementation. Pinning them to *set* values would
  # not be neutral: NO_COLOR=1 resolves the theme to "mono" and changes
  # `tasks config --json` output, so the harness would be altering the behavior
  # it exists to observe in order to make itself tidier. Listing them in the
  # recorded floor is the other half: every one is written into
  # `invocation.env` with a null value, so "colour was configured by nothing"
  # is proven per observation rather than inferred from this comment.
  #
  # A case MAY set any of them — that is how the colour path would eventually be
  # exercised — and the union rule makes such a case visible in `invocation.env`.
  COLOR_VARS = %w[COLORTERM NO_COLOR TASKS_THEME TERM].freeze

  # The test-only clock seam. Pinned to unset for the same reason as the colour
  # names — `unsetenv_others` makes that a pin — and recorded because it is a
  # CLOCK input: it is dominated by TASKS_PIN_NOW, and "dominated" is a claim an
  # observation should carry evidence for rather than a claim a comment makes.
  # It resolves inside Tasks::Determinism like every other pin.
  CLOCK_TEST_VARS = %w[TASKS_TEST_TODAY_SEQUENCE].freeze

  # Path variables the runner owns: a case may not set them, because they are
  # what makes the run isolated. TASKS_FILE / TASKS_ARCHIVE are recorded as
  # unset on purpose — a per-file override would redirect half a store.
  # TASKS_MEMORY belongs here for the same reason and not a weaker one: it names
  # a *file path*, so a case that set it could point the memory sidecar outside
  # the copy — outside the observed tree, and potentially at something real.
  # PATH is here too: it is pinned rather than inherited, and a case that could
  # set it could put a shim in front of the interpreter.
  PATH_VARS = %w[HOME PATH TASKS_ARCHIVE TASKS_DIR TASKS_FILE TASKS_MEMORY
                 XDG_CONFIG_HOME XDG_STATE_HOME].freeze
  # The floor of what every observation records, not the whole of it: the
  # observation records the union of this list and the names actually passed to
  # the process, so a variable a case sets is visible rather than silent. A
  # constant list alone would let two observations carry byte-identical
  # `invocation` blocks and produce different store bytes.
  RECORDED_ENV = (DEFAULT_PINS.keys + PATH_VARS + COLOR_VARS + CLOCK_TEST_VARS).sort.freeze

  # The process umask, pinned. It is not a host fact: it is a per-process
  # attribute, one syscall away, and it moves `mode` on every file the
  # implementation creates — which is a compared field. Leaving it unpinned
  # would bake the capture operator's umask into the baseline and make a genuine
  # permission regression indistinguishable from a different CI image.
  UMASK = 0o022

  # Where the runner puts the isolated XDG roots inside the copy. Both live
  # inside the copy so that everything the implementation writes is inside the
  # observed tree and every recorded path is relative to one root.
  CONFIG_SUBDIR = ".config"
  STATE_SUBDIR = ".state"

  # Whole-file content is embedded up to this size, so an evidence record is
  # self-contained for the fixtures a human will actually read; past it, digest
  # and size are the record. Streams get a larger budget and record where they
  # were cut.
  FILE_EMBED_LIMIT = 64 * 1024
  STREAM_EMBED_LIMIT = 256 * 1024

  DEFAULT_TIMEOUT_MS = 60_000

  Error = Class.new(StandardError)
  CaseError = Class.new(Error)

  # --- the case list --------------------------------------------------------

  # One line of a case list. Blank lines and lines whose first non-space
  # character is `#` are skipped, so a case list can carry section comments.
  Case = Struct.new(:id, :fixture_class, :fixture_name, :surface, :argv, :cwd,
                    :env, :stdin, :stdin_provided, :timeout_ms, :install_journal,
                    :copy_root_mode, :notes, keyword_init: true) do
    def fixture_id = "#{fixture_class}/#{fixture_name}"
    def fixture_dir = File.join(FIXTURES_DIR, fixture_class, fixture_name)
  end

  module CaseList
    KNOWN_KEYS = %w[case_id fixture surface argv cwd env stdin stdin_base64
                    timeout_ms install_journal copy_root_mode notes].freeze

    module_function

    def load(path)
      raise Error, "case list not found: #{path}" unless File.file?(path)

      cases = []
      seen = {}
      File.readlines(path, chomp: true).each_with_index do |line, index|
        next if line.strip.empty? || line.strip.start_with?("#")

        entry = parse_line(line, path, index + 1)
        kase = build(entry, path, index + 1)
        if seen.key?(kase.id)
          raise Error, "#{path}:#{index + 1}: duplicate case_id #{kase.id.inspect} " \
                       "(first seen on line #{seen[kase.id]})"
        end

        seen[kase.id] = index + 1
        cases << kase
      end
      raise Error, "#{path}: no cases" if cases.empty?

      cases
    end

    def parse_line(line, path, lineno)
      entry = JSON.parse(line)
      raise Error, "#{path}:#{lineno}: case must be a JSON object" unless entry.is_a?(Hash)

      unknown = entry.keys - KNOWN_KEYS
      raise Error, "#{path}:#{lineno}: unknown key(s) #{unknown.join(", ")}" unless unknown.empty?

      entry
    rescue JSON::ParserError => e
      raise Error, "#{path}:#{lineno}: #{e.message}"
    end

    def build(entry, path, lineno)
      where = "#{path}:#{lineno}"
      id = entry["case_id"]
      raise Error, "#{where}: case_id is required" unless id.is_a?(String) && !id.empty?
      unless /\A[a-z0-9][a-z0-9._-]*\z/.match?(id)
        raise Error, "#{where}: case_id must be a filesystem-safe slug, got #{id.inspect}"
      end

      fixture = entry["fixture"].to_s
      fixture_class, fixture_name = fixture.split("/", 2)
      unless FIXTURE_CLASSES.include?(fixture_class) && !fixture_name.to_s.empty?
        raise Error, "#{where}: fixture must be \"<class>/<name>\" with class one of " \
                     "#{FIXTURE_CLASSES.join(", ")}, got #{fixture.inspect}"
      end

      argv = entry["argv"]
      unless argv.is_a?(Array) && argv.all?(String)
        raise Error, "#{where}: argv must be an array of strings"
      end

      surface = entry.fetch("surface", "cli")
      raise Error, "#{where}: only the cli surface is implemented" unless surface == "cli"

      if entry.key?("stdin") && entry.key?("stdin_base64")
        raise Error, "#{where}: give stdin or stdin_base64, not both"
      end

      stdin = if entry.key?("stdin_base64")
                Base64.decode64(entry["stdin_base64"].to_s)
              elsif entry.key?("stdin")
                entry["stdin"].to_s.b
              end

      env = entry.fetch("env", {})
      raise Error, "#{where}: env must be an object" unless env.is_a?(Hash)

      env.each do |name, value|
        raise Error, "#{where}: env #{name} must be a string or null" unless value.nil? || value.is_a?(String)
        if PATH_VARS.include?(name)
          raise Error, "#{where}: env #{name} is owned by the runner and cannot be set by a case"
        end
      end

      mode = entry["copy_root_mode"]
      unless mode.nil? || (mode.is_a?(String) && /\A0?[0-7]{3}\z/.match?(mode))
        raise Error, "#{where}: copy_root_mode must be a three- or four-digit octal string, got #{mode.inspect}"
      end

      Case.new(
        id: id, fixture_class: fixture_class, fixture_name: fixture_name,
        surface: surface, argv: argv, cwd: entry.fetch("cwd", "."),
        env: env, stdin: stdin || "", stdin_provided: !stdin.nil?,
        timeout_ms: entry["timeout_ms"], install_journal: entry.fetch("install_journal", true),
        copy_root_mode: mode, notes: entry["notes"]
      )
    end
  end

  # --- filesystem observation ----------------------------------------------

  # Which file is what, resolved from the paths the IMPLEMENTATION reported it
  # was using (probe § paths) rather than from a table of hardcoded names.
  #
  # The difference is not cosmetic. `valid/symlinked-store` puts the store's
  # bytes in `tasks.real.jsonl` and makes `tasks.jsonl` a link to it; a name
  # table records the file carrying the store as `role: "other"`, which silently
  # voids the schema's guarantee that "the store and the archive were BOTH
  # observed" and makes the mutation invariant below fail on a correct run.
  #
  # Both spellings of a path get the role: the link is the store the user names,
  # the target is the store the bytes are in. Everything the implementation does
  # not name is resolved by shape — the journal layout and the `.lock` suffix are
  # patterns, not names, and stay correct under any store spelling.
  class Roles
    NAMED = %w[store archive memory config].freeze

    # `root` is the root the reported paths are relative to, which is not
    # necessarily the tree being walked: the probe that reports them runs against
    # a throwaway pristine copy (see Runner#run_case). The *relative* store,
    # archive, memory, and config paths are identical in both copies, so they
    # transfer. `journal_rel` deliberately does not — the journal directory is
    # named for a digest of the copy's own absolute path — so the caller passes
    # the one it computed for the tree actually being walked, and the journal
    # falls back to shape matching when it cannot.
    def initialize(paths, root, state_subdir:, journal_rel: nil)
      @state_subdir = state_subdir
      @journal_rel = journal_rel
      @table = {}
      return if paths.nil?

      # Both spellings of the root are tried because the canonical paths are
      # realpath'd and the root may not be: on macOS `/tmp` is a symlink to
      # `/private/tmp`, so the store's canonical path and the copy root can
      # disagree on their prefix while naming the same directory.
      roots = [root, (File.realpath(root) rescue root)].uniq # rubocop:disable Style/RescueModifier
      NAMED.each do |role|
        [paths[role], paths["#{role}_canonical"]].compact.each do |abs|
          # Tree.relative returns the path unchanged when it is not under the
          # root, so a still-absolute candidate means "not in this tree".
          rel = roots.filter_map { |r| Tree.relative(r, abs) }
                     .find { |candidate| !candidate.start_with?("..", "/") && candidate != "." }
          @table[rel] = role if rel
        end
      end
    end

    def call(rel)
      return @table[rel] if @table.key?(rel)
      return "journal_index" if journal_child?(rel, "index.json")
      return "journal_blob" if journal_blob?(rel)
      return "lock" if rel.end_with?(".lock")

      "other"
    end

    private

    # The journal directory is named for a digest of the store's canonical path,
    # so it is matched by shape when the implementation did not report it.
    def journal_child?(rel, name)
      return rel == File.join(@journal_rel, name) if usable_journal_rel?

      %r{\A#{Regexp.escape(@state_subdir)}/tasks/journal/[^/]+/#{Regexp.escape(name)}\z}.match?(rel)
    end

    def journal_blob?(rel)
      return rel.start_with?("#{@journal_rel}/blobs/") if usable_journal_rel?

      %r{\A#{Regexp.escape(@state_subdir)}/tasks/journal/[^/]+/blobs/[^/]+\z}.match?(rel)
    end

    def usable_journal_rel? = @journal_rel && !@journal_rel.start_with?("..") && @journal_rel != "."
  end

  # The fallback used before any probe has run — and the only resolver a runner
  # needs when the implementation reports no paths at all.
  module DefaultRoles
    module_function

    def call(rel)
      case rel
      when "tasks.jsonl" then "store"
      when "archive.jsonl" then "archive"
      when %r{\A#{Regexp.escape(STATE_SUBDIR)}/tasks/journal/[^/]+/index\.json\z} then "journal_index"
      when %r{\A#{Regexp.escape(STATE_SUBDIR)}/tasks/journal/[^/]+/blobs/[^/]+\z} then "journal_blob"
      when %r{\A#{Regexp.escape(CONFIG_SUBDIR)}/tasks/config\z} then "config"
      when /\.lock\z/ then "lock"
      when "agent-memory.md" then "memory"
      else "other"
      end
    end
  end

  module Tree
    module_function

    # Every entry under the copy — files, symlinks AND directories, plus the
    # copy root itself as ".". Sorted by relative path. Nothing is filtered: a
    # leftover `.tasks.jsonl.<pid>.<tid>.tmp` is a real finding (a crashed
    # write), and a harness that hid it would hide the crash.
    #
    # Directories are recorded whether or not they changed, on the same terms as
    # files, because the alternative — recording only the ones that moved — is
    # what made a directory-only effect indistinguishable from no effect at all.
    # A create, a remove, or a chmod of a directory produced a byte-identical
    # observation to doing nothing, so a port that forgot to create the journal
    # directory, or left an empty one behind, or created it 0700 instead of
    # 0755, was invisible.
    #
    # The root is included for the case it makes observable: `copy_root_mode` is
    # an input the case declares and the observation recorded nowhere, so
    # `cli-capture-readonly-rollback` could not show what it made unwritable.
    # Both sides get the same mode by construction, so the row proves the mode
    # was applied and asserts nothing about the port — the standing of
    # `environment.umask`.
    def walk(root, roles = DefaultRoles)
      states = [state(root, root, roles)]
      stack = [root]
      while (dir = stack.pop)
        Dir.children(dir).sort.each do |path_name|
          path = File.join(dir, path_name)
          stat = File.lstat(path)
          stack << path if stat.directory?
          states << state(root, path, roles, stat)
        end
      end
      states.sort_by { |s| s["path"] }
    end

    def state(root, path, roles = DefaultRoles, stat = File.lstat(path))
      rel = relative(root, path)
      role = roles.call(rel)
      blank = { "role" => role, "kind" => nil, "path" => rel, "present" => true,
                "sha256" => nil, "size_bytes" => nil, "mode" => nil,
                "content_base64" => nil, "line_count" => nil, "symlink_target" => nil }

      # A symlink is recorded by target and never followed, so it has no mode
      # and no digest of its own — checked FIRST because lstat reports a link to
      # a directory as a link, and following it would walk outside the copy.
      return blank.merge("kind" => "symlink", "symlink_target" => File.readlink(path)) if stat.symlink?

      # A directory carries mode and nothing else. Not its children: every child
      # is already its own row, and repeating them here would make the record
      # quadratic and surface one file's change on every ancestor. Not its
      # size_bytes either: the directory inode's size is a filesystem number
      # with no product meaning that two correct hosts disagree about.
      if stat.directory?
        return blank.merge("kind" => "directory", "mode" => format("%04o", stat.mode & 0o7777))
      end

      bytes = File.binread(path)
      blank.merge(
        "kind" => "file",
        "sha256" => Digest::SHA256.hexdigest(bytes),
        "size_bytes" => bytes.bytesize,
        "mode" => format("%04o", stat.mode & 0o7777),
        "content_base64" => bytes.bytesize <= FILE_EMBED_LIMIT ? strict_base64(bytes) : nil,
        "line_count" => %w[store archive].include?(role) ? bytes.count("\n") : nil
      )
    end

    def absent(root, rel, roles = DefaultRoles)
      { "role" => roles.call(rel), "kind" => nil, "path" => rel, "present" => false,
        "sha256" => nil, "size_bytes" => nil, "mode" => nil, "content_base64" => nil,
        "line_count" => nil, "symlink_target" => nil }
    end

    def relative(root, path)
      full = File.expand_path(path)
      base = File.expand_path(root)
      full == base ? "." : full.delete_prefix("#{base}/")
    end

    # A digest over the whole starting tree: sorted "<relpath>\0<sha256>\n"
    # records, hashed. Language-neutral by construction — it depends on nothing
    # but the path strings and the file bytes.
    def root_digest(states)
      digest = Digest::SHA256.new
      states.sort_by { |s| s["path"] }.each do |s|
        digest << s["path"] << "\0" << (s["sha256"] || "-") << "\n"
      end
      digest.hexdigest
    end

    def strict_base64(bytes) = Base64.strict_encode64(bytes)
  end

  # --- the runner -----------------------------------------------------------

  class Runner
    attr_reader :failures

    def initialize(target:, work:, out: nil, pin_identity: false, timeout_ms: DEFAULT_TIMEOUT_MS,
                   keep: false, quiet: false)
      @target = target
      @work = File.expand_path(work)
      @out = out && File.expand_path(out)
      @pin_identity = pin_identity
      @timeout_ms = timeout_ms
      @keep = keep
      @quiet = quiet
      @failures = []
    end

    def run(cases)
      @target.prepare!
      refuse_live_store!
      # Pinned here, once, before any copy is made or any child is spawned:
      # the umask is inherited across fork/exec, so setting it in the runner
      # process is what pins it for the implementation. See UMASK.
      File.umask(UMASK)
      FileUtils.mkdir_p(@work)
      FileUtils.mkdir_p(@out) if @out
      cases.map { |kase| run_case(kase) }
    end

    # --- safety ------------------------------------------------------------

    # `bin/tasks config` is consulted for exactly one reason: to know which
    # directory to stay away from. The call is a read, made with the operator's
    # own environment (that is the point — it reports the real store), and its
    # answer is never used to point anything at that store.
    def refuse_live_store!
      out = IO.popen([*@target.cli, "config", "--json"], err: File::NULL, &:read)
      raise Error, "could not ask `tasks config --json` where the live store is" unless $?.success?

      config = JSON.parse(out)
      %w[org archive memory].each do |key|
        live = config[key].to_s
        next if live.empty?

        live_dir = File.expand_path(File.dirname(live))
        if @work == live_dir || @work.start_with?("#{live_dir}/") || live_dir.start_with?("#{@work}/")
          raise Error, "refusing to run: --work #{@work} overlaps the live store directory #{live_dir}"
        end
      end
    end

    # --- one case ----------------------------------------------------------

    def run_case(kase)
      copy_root = File.join(@work, kase.id)
      before_root = File.join(@work, "#{kase.id}.before")
      io_dir = File.join(@work, ".io", kase.id)
      # A previous run may have left the copy root unwritable (a copy_root_mode
      # case that died before its restore). rm_rf cannot empty such a directory
      # and fails silently, so the mode is reset first — otherwise stale files
      # from the last run would leak into this one's `files.before`.
      [copy_root, before_root].each { |dir| make_removable(dir) }
      [copy_root, before_root, io_dir].each { |dir| FileUtils.rm_rf(dir) }
      FileUtils.mkdir_p(io_dir)

      prepare_copy(kase, copy_root)
      apply_copy_root_mode(kase, copy_root)

      # The before-probe runs against its own pristine copy, never against the
      # case copy: taking a snapshot acquires the store lock, and a lock file
      # created by the harness would mask the lock creation the invocation is
      # supposed to be observed doing. The store revision is a digest of the
      # store's bytes and nothing else, so a second copy answers the same
      # question the case copy would have.
      #
      # It runs BEFORE the tree is walked because it is also where `files[].role`
      # comes from: roles are resolved from the paths the implementation reports
      # it resolved, not from a table of filenames. See Roles.
      prepare_copy(kase, before_root, journal: false)
      before_probe = probe(kase, before_root)
      roles = roles_for(before_probe, before_root, copy_root)

      before = Tree.walk(copy_root, roles)
      root_sha256 = Tree.root_digest(before)

      result = execute(kase, copy_root, io_dir)

      after = Tree.walk(copy_root, roles)
      after_probe = probe(kase, copy_root)

      observation = build_observation(kase, copy_root, root_sha256, before, after,
                                      result, before_probe, after_probe, roles)
      check_invariants(kase, observation, before_probe, after_probe)
      emit(kase, observation)
      # Restored only after every observation of the tree has been taken, so the
      # invocation and both probes saw the mode the case asked for.
      make_removable(copy_root)
      unless @keep
        [copy_root, before_root, io_dir].each { |dir| FileUtils.rm_rf(dir) }
      end
      observation
    end

    # The role resolver for one case. The named paths come from the probe (run
    # against `probe_root`); the journal directory is the one the runner itself
    # computed for `copy_root`, because that name is a digest of the copy's own
    # absolute path and the probe's copy has a different one.
    def roles_for(probe_report, probe_root, copy_root)
      journal_rel = Tree.relative(
        copy_root, File.join(copy_root, STATE_SUBDIR, "tasks", "journal", journal_key(copy_root))
      )
      Roles.new(probe_report["paths"], probe_root,
                state_subdir: STATE_SUBDIR, journal_rel: journal_rel)
    end

    # The case's declared mode for the copy root, applied after the copy is
    # complete and before anything observes it. It is what makes a write failure
    # — and therefore a genuine write-then-revert — reachable from a case list:
    # the runner owns the copy root, so no fixture can constrain its mode.
    # Deliberately NOT applied to the before-probe's throwaway copy: that copy is
    # a harness artifact for reading the pristine revision token, and an
    # unwritable one would fail the probe instead of the invocation.
    def apply_copy_root_mode(kase, copy_root)
      return if kase.copy_root_mode.nil?

      File.chmod(kase.copy_root_mode.to_i(8), copy_root)
    end

    # Undo a restrictive copy_root_mode so the directory can be emptied. Applied
    # to the root only: nothing below it is ever chmod'd, so file modes recorded
    # in `files.after` are the implementation's own doing.
    def make_removable(dir)
      File.chmod(0o755, dir) if File.directory?(dir)
    rescue SystemCallError
      nil
    end

    # Copy the fixture, install its journal, and create the isolated XDG roots.
    # `cp -a <fixture>/store/. <copy>/` — the trailing `/.` is load-bearing:
    # two fixtures carry dotfiles a `<dir>/*` glob would silently drop.
    def prepare_copy(kase, copy_root, journal: true)
      source = File.join(kase.fixture_dir, "store")
      raise CaseError, "fixture #{kase.fixture_id} has no store/ directory" unless File.directory?(source)

      FileUtils.mkdir_p(copy_root)
      unless system("cp", "-a", "#{source}/.", "#{copy_root}/")
        raise CaseError, "cp -a failed for #{kase.fixture_id}"
      end

      apply_fixture_perms(kase, copy_root)
      FileUtils.mkdir_p(File.join(copy_root, CONFIG_SUBDIR, "tasks"))
      FileUtils.mkdir_p(File.join(copy_root, STATE_SUBDIR))
      install_journal(kase, copy_root) if journal && kase.install_journal
      copy_root
    end

    # `<fixture>/perms.json` — modes git cannot record, applied to the copy.
    #
    # Git tracks exactly one permission bit (the executable bit), so a fixture
    # whose subject is a restrictive mode cannot ship that mode as file content:
    # `cp -a` faithfully preserves the 0644 the checkout produced, and
    # `valid/restricted-mode-store` — a fixture named for the "a chmod-600 store
    # must not widen to 644 across an atomic replacement" contract — cannot test
    # it. The map lives with the fixture and not on the case because it is a
    # property of the corpus (this store IS mode 600, for every case that uses
    # it), which is exactly the opposite of `copy_root_mode`. See
    # porting/runners/README.md § "Two modes, and why they are not one key".
    #
    # Applied to both copies, and applied to the throwaway probe copy too: the
    # probe only reads, and a fixture that declares a mode declares it for the
    # whole corpus, so both copies must start identical.
    def apply_fixture_perms(kase, copy_root)
      manifest = File.join(kase.fixture_dir, "perms.json")
      return unless File.file?(manifest)

      chmod = JSON.parse(File.read(manifest))["chmod"]
      raise CaseError, "#{manifest}: chmod must be an object" unless chmod.is_a?(Hash)

      chmod.sort.each do |rel, mode|
        unless mode.is_a?(String) && /\A0?[0-7]{3}\z/.match?(mode)
          raise CaseError, "#{manifest}: mode for #{rel} must be a three- or four-digit " \
                           "octal string, got #{mode.inspect}"
        end

        target = File.expand_path(rel, copy_root)
        unless target.start_with?("#{copy_root}/")
          raise CaseError, "#{manifest}: #{rel.inspect} escapes the fixture copy"
        end
        raise CaseError, "#{manifest}: no such file in the fixture: #{rel}" unless File.exist?(target)

        File.chmod(mode.to_i(8), target)
      end
    end

    # The journal cannot ship as literal bytes: it lives under a directory named
    # for a digest of the store's absolute path, and its index records that path
    # too. Both are properties of where the copy landed.
    def install_journal(kase, copy_root)
      source = File.join(kase.fixture_dir, "journal")
      return unless File.directory?(source)

      dir = File.join(copy_root, STATE_SUBDIR, "tasks", "journal", journal_key(copy_root))
      FileUtils.mkdir_p(dir)
      blobs = File.join(source, "blobs")
      FileUtils.cp_r(blobs, dir) if File.directory?(blobs)

      template = File.join(source, "index.json.template")
      literal = File.join(source, "index.json")
      if File.file?(template)
        File.binwrite(File.join(dir, "index.json"),
                      File.binread(template).gsub("{{ORG_PATH}}", org_path(copy_root)))
      elsif File.file?(literal)
        # A fixture whose whole point is a *wrong* org path ships its index
        # literally; templating it would delete the thing under test.
        FileUtils.cp(literal, File.join(dir, "index.json"))
      end
    end

    # The store's canonical path, mirroring `Journal.canonical`: absolute AND
    # symlink-resolved. Resolving the link matters — `valid/symlinked-store`
    # spells the store as a link to `tasks.real.jsonl`, and a key computed from
    # the unresolved name names a journal directory the implementation never
    # writes to, so the harness would report `journal.present: false` for a run
    # that wrote a journal. Falls back to the unresolved path when the store does
    # not exist yet, exactly as the implementation does.
    def org_path(copy_root)
      path = File.join(File.realpath(copy_root), "tasks.jsonl")
      File.realpath(path)
    rescue Errno::ENOENT
      path
    end

    def journal_key(copy_root) = Digest::SHA256.hexdigest(org_path(copy_root))[0, 16]

    # --- environment -------------------------------------------------------

    def environment_for(kase, copy_root)
      env = DEFAULT_PINS.merge(
        "PATH" => PATH_VALUE,
        "HOME" => copy_root,
        "TASKS_DIR" => copy_root,
        "XDG_CONFIG_HOME" => File.join(copy_root, CONFIG_SUBDIR),
        "XDG_STATE_HOME" => File.join(copy_root, STATE_SUBDIR)
      )
      kase.env.each { |name, value| value.nil? ? env.delete(name) : env[name] = value }
      env
    end

    # --- execution ---------------------------------------------------------

    def execute(kase, copy_root, io_dir)
      env = environment_for(kase, copy_root)
      cwd = File.expand_path(kase.cwd, copy_root)
      raise CaseError, "cwd #{kase.cwd.inspect} is outside the fixture copy" unless
        cwd == copy_root || cwd.start_with?("#{copy_root}/")

      stdin_path = File.join(io_dir, "stdin")
      File.binwrite(stdin_path, kase.stdin)
      out_path = File.join(io_dir, "stdout")
      err_path = File.join(io_dir, "stderr")
      budget = kase.timeout_ms || @timeout_ms

      started = Process.clock_gettime(Process::CLOCK_MONOTONIC)
      pid = Process.spawn(env,
                          *@target.cli, *kase.argv,
                          chdir: cwd, in: stdin_path, out: out_path, err: err_path,
                          unsetenv_others: true)
      status, timed_out = wait_with_timeout(pid, budget)
      wall_ms = (Process.clock_gettime(Process::CLOCK_MONOTONIC) - started) * 1000.0

      { env: env, cwd: cwd, timeout_ms: budget, timed_out: timed_out, wall_ms: wall_ms,
        exit_status: status.exited? ? status.exitstatus : nil,
        signal: status.signaled? ? status.termsig : nil,
        stdout: File.binread(out_path), stderr: File.binread(err_path) }
    end

    def wait_with_timeout(pid, budget_ms)
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + (budget_ms / 1000.0)
      loop do
        _, status = Process.waitpid2(pid, Process::WNOHANG)
        return [status, false] if status

        if Process.clock_gettime(Process::CLOCK_MONOTONIC) > deadline
          Process.kill("KILL", pid)
          _, status = Process.waitpid2(pid)
          return [status, true]
        end
        sleep 0.005
      end
    end

    # --- probe -------------------------------------------------------------

    def probe(kase, root)
      env = environment_for(kase, root)
      out = nil
      IO.popen(env, [*@target.probe, root], err: File::NULL,
                                           unsetenv_others: true) { |io| out = io.read }
      raise CaseError, "probe failed for #{kase.id}" unless $?.success?

      JSON.parse(out)
    end

    # --- observation -------------------------------------------------------

    def build_observation(kase, copy_root, root_sha256, before, after, result,
                          before_probe, after_probe, roles = DefaultRoles)
      deltas = deltas_for(before, after)
      store_changed = before_probe.dig("revisions", "store") != after_probe.dig("revisions", "store")
      spellings = Conformance::Normalize.copy_root_spellings(copy_root)

      {
        "schema_version" => SCHEMA_VERSION,
        "observation_id" => @pin_identity ? "obs_#{kase.id}" : "obs_#{SecureRandom.uuid}",
        "case_id" => kase.id,
        "implementation" => {
          "name" => @target.name,
          "version" => implementation_version,
          "runtime" => after_probe.dig("environment", "runtime")
        },
        "fixture" => {
          "id" => kase.fixture_id,
          "class" => kase.fixture_class,
          "root_sha256" => root_sha256,
          "copy_root" => copy_root
        },
        "invocation" => {
          "surface" => kase.surface,
          "argv" => kase.argv,
          "stdin" => { "provided" => kase.stdin_provided,
                       "bytes_base64" => Tree.strict_base64(kase.stdin) },
          "cwd" => Tree.relative(copy_root, result[:cwd]),
          "env" => recorded_env(result[:env]),
          "pins" => after_probe["pins"],
          # Colour's real switch is `$stdout.tty?`, not an environment variable,
          # and it is pinned by the harness PROCESS rather than by the child's
          # environment — exactly like umask: all three descriptors are
          # redirected to files (see #execute), so no stream is ever a terminal.
          # Recorded rather than assumed, because an input that changes stdout
          # bytes and appears in no observation is an input a green run is
          # silent about. Recording it does NOT cover the colour path; it makes
          # the coverage gap legible. See porting/specs/determinism.md § Colour.
          "tty" => { "stdin" => false, "stdout" => false, "stderr" => false },
          "timeout_ms" => result[:timeout_ms]
        },
        "process" => {
          "exit_status" => result[:exit_status],
          "signal" => result[:signal],
          "timed_out" => result[:timed_out],
          "stdout" => stream(result[:stdout], spellings),
          "stderr" => stream(result[:stderr], spellings)
        },
        "files" => {
          "mutated" => store_changed,
          "before" => before,
          "after" => after,
          "deltas" => deltas,
          # The implementation's own report, read from its --json error
          # envelope. Never inferred from stderr wording (that would bake one
          # implementation's prose into a language-neutral protocol) and never
          # inferred from the file deltas (a write-then-revert and a
          # never-wrote leave identical bytes — that is why the field exists).
          # Null means the invocation made no such report.
          "rolled_back" => rolled_back(result[:stdout])
        },
        "journal" => journal_state(copy_root, after, roles),
        "revisions" => {
          "store" => after_probe.dig("revisions", "store"),
          "resources" => after_probe.dig("revisions", "resources"),
          "touched_ids" => touched_ids(result[:stdout]),
          "http_etag" => nil
        },
        "http" => [],
        "environment" => {
          "tzdb_version" => after_probe.dig("environment", "tzdb_version"),
          "platform" => after_probe.dig("environment", "platform"),
          "filesystem" => filesystem_of(copy_root),
          "locale" => after_probe.dig("environment", "locale"),
          "umask" => format("%04o", File.umask)
        },
        "metrics" => {
          "wall_ms" => @pin_identity ? 0 : result[:wall_ms].round(3),
          "user_cpu_ms" => nil,
          "sys_cpu_ms" => nil,
          "peak_rss_bytes" => nil,
          "bytes_written" => bytes_written(after, deltas)
        },
        "notes" => notes_for(kase, after_probe)
      }
    end

    # The union of the floor (RECORDED_ENV) and the names actually handed to the
    # process, sorted. The union is the load-bearing half: a constant list lets a
    # case set a variable the product reads — TASKS_DATE_ORDER, TASKS_WORKER_ID,
    # TASKS_URGENT_DAYS — and produce two observations with byte-identical
    # `invocation` blocks and different store bytes, with the input that caused
    # it recorded nowhere. A name in the floor that was not passed is recorded
    # with a null value, so "this variable was unset" stays proven.
    def recorded_env(env)
      (RECORDED_ENV | env.keys).sort.map { |name| { "name" => name, "value" => env[name] } }
    end

    # `sha256_normalized` is the digest of the same bytes after the comparator's
    # copy-root rewrite — the SAME function, required from
    # porting/compare/lib/normalize.rb rather than reimplemented here.
    #
    # It exists because a stream past STREAM_EMBED_LIMIT survives only as a
    # digest, and a digest of raw bytes can never be copy-root-neutral: a
    # 300 KiB diagnostic naming the fixture copy digests differently at every
    # copy root, so stream truncation and cross-path comparison were mutually
    # exclusive. Computing it here, per side, from that side's own copy root is
    # equivalent to the comparator's union-of-spellings pass — a side's bytes can
    # only ever contain its own copy root — and it is the only place it CAN be
    # computed, because past the embed limit the comparator no longer has the
    # bytes.
    def stream(bytes, spellings)
      truncated = bytes.bytesize > STREAM_EMBED_LIMIT
      body = truncated ? bytes.byteslice(0, STREAM_EMBED_LIMIT) : bytes
      valid = bytes.dup.force_encoding(Encoding::UTF_8).valid_encoding?
      normalized = Conformance::Normalize.rewrite_copy_root_bytes(bytes, spellings)
      {
        "sha256" => Digest::SHA256.hexdigest(bytes),
        "sha256_normalized" => Digest::SHA256.hexdigest(normalized),
        "size_bytes" => bytes.bytesize,
        "valid_utf8" => valid,
        "bytes_base64" => Tree.strict_base64(body),
        "truncated_at_bytes" => truncated ? STREAM_EMBED_LIMIT : nil,
        "text" => valid && !truncated ? bytes.dup.force_encoding(Encoding::UTF_8) : nil
      }
    end

    def deltas_for(before, after)
      by_path = ->(states) { states.to_h { |s| [s["path"], s] } }
      old = by_path.call(before)
      new = by_path.call(after)
      (old.keys | new.keys).sort.filter_map do |path|
        a = old[path]
        b = new[path]
        # `mode` participates, and it is one rule for every entry rather than a
        # per-kind one. A directory has no bytes, so a chmod is the only
        # modification it can express and a delta list that ignored mode would
        # report a chmod'd directory as nothing having happened. Applying the
        # same rule to files costs nothing (an atomic replacement preserves the
        # existing mode, so no current case emits a mode-only file delta) and
        # closes the same hole one level up: a widened store that kept its bytes
        # is a real regression, and it should fail loudly here rather than sit
        # in `after` waiting for someone to read the column.
        kind = if a.nil? then "created"
               elsif b.nil? then "deleted"
               elsif a["sha256"] != b["sha256"] ||
                     a["symlink_target"] != b["symlink_target"] ||
                     a["mode"] != b["mode"]
                 "modified"
               end
        next if kind.nil?

        { "path" => path, "kind" => kind,
          "before_sha256" => a && a["sha256"], "after_sha256" => b && b["sha256"] }
      end
    end

    def bytes_written(after, deltas)
      by_path = after.to_h { |s| [s["path"], s] }
      deltas.sum { |d| by_path.dig(d["path"], "size_bytes").to_i }
    end

    # The journal, structurally and as bytes. The directory name is a digest of
    # the store's absolute path, so it is computed rather than searched for:
    # that way "no journal" reports the path where a journal WOULD have been,
    # instead of an empty string.
    def journal_state(copy_root, after, roles = DefaultRoles)
      dir = File.join(copy_root, STATE_SUBDIR, "tasks", "journal", journal_key(copy_root))
      index_path = File.join(dir, "index.json")
      index_rel = Tree.relative(copy_root, index_path)
      present = File.file?(index_path)
      index = present ? Tree.state(copy_root, index_path, roles) : Tree.absent(copy_root, index_rel, roles)

      blobs = after.select { |s| s["role"] == "journal_blob" && s["path"].start_with?("#{Tree.relative(copy_root, dir)}/") }
      state = {
        "present" => present,
        "version" => nil,
        "cursor" => nil,
        "states" => [],
        "index" => index,
        "blob_count" => present ? blobs.size : nil,
        "blob_sha256" => blobs.map { |s| s["sha256"] }.compact.sort
      }
      return state unless present

      parsed = begin
        JSON.parse(File.read(index_path))
      rescue JSON::ParserError
        nil
      end
      return state unless parsed.is_a?(Hash)

      state["version"] = parsed["version"].is_a?(Integer) ? parsed["version"] : nil
      state["cursor"] = parsed["cursor"].is_a?(Integer) && parsed["cursor"] >= 0 ? parsed["cursor"] : nil
      state["states"] = Array(parsed["states"]).map do |s|
        s = {} unless s.is_a?(Hash)
        { "label" => s["label"], "store_sha256" => s["org_sha"], "archive_sha256" => s["archive_sha"],
          "coalesce_key" => s["coalesce_key"], "coalesce_scope" => s["coalesce_scope"],
          "repair" => s["repair"].nil? ? nil : s["repair"] == true }
      end
      state
    end

    # Whether the implementation said it wrote and then reverted. Read from the
    # `--json` error envelope on stdout — `{"error":…,"rolled_back":true|false}`
    # — exactly as touched_ids is read from the `--json` mutation payload. A
    # boolean is recorded only when the implementation stated one; anything else
    # (no envelope, a non-boolean, unparseable stdout) is null, which reads as
    # "not reported", never as "did not roll back".
    def rolled_back(stdout)
      payload = JSON.parse(stdout)
      return nil unless payload.is_a?(Hash) && payload.key?("error")

      value = payload["rolled_back"]
      value == true || value == false ? value : nil
    rescue JSON::ParserError, ArgumentError
      nil
    end

    # The CLI reports what a mutation touched in its own `--json` payload. That
    # is the implementation's report, not the harness's inference, which is why
    # it is read from stdout rather than diffed out of the store.
    def touched_ids(stdout)
      payload = JSON.parse(stdout)
      return [] unless payload.is_a?(Hash)

      Array(payload["touched"]).filter_map { |row| row["id"] if row.is_a?(Hash) }.uniq.sort
    rescue JSON::ParserError, ArgumentError
      []
    end

    def implementation_version
      @implementation_version ||= begin
        sha = `git -C #{Shellwords.escape(REPO_ROOT)} rev-parse HEAD 2>/dev/null`.strip
        dirty = `git -C #{Shellwords.escape(REPO_ROOT)} status --porcelain 2>/dev/null`.strip
        sha = "unknown" if sha.empty?
        dirty.empty? ? sha : "#{sha}-dirty"
      end
    end

    def filesystem_of(path)
      out = `stat -f %T #{Shellwords.escape(path)} 2>/dev/null`.strip
      out = `stat -f -c %T #{Shellwords.escape(path)} 2>/dev/null`.strip if out.empty?
      out.empty? ? nil : out
    rescue StandardError
      nil
    end

    def notes_for(kase, probe)
      notes = []
      notes << kase.notes if kase.notes.is_a?(String) && !kase.notes.empty?
      status = probe.dig("revisions", "status")
      notes << "probe read status: #{status}" if status && status != "ok"
      notes.empty? ? nil : notes.join(" | ")
    end

    # --- invariants --------------------------------------------------------

    # Two runner-side checks, both of which are hard failures rather than
    # warnings: a silently-dropped pin makes a green run meaningless, and a
    # mutation claim that disagrees with the observed deltas means one of the
    # two was measured wrong.
    def check_invariants(kase, observation, before_probe, after_probe)
      requested = environment_for(kase, "/unused")
      observation["invocation"]["pins"].each do |pin|
        next if pin["applied"]
        next unless requested.key?(pin["name"]) && !requested[pin["name"]].to_s.empty?

        fail_case(kase, "pin #{pin["name"]} was requested (#{requested[pin["name"]].inspect}) " \
                        "and NOT applied by the implementation")
      end

      # Roles, not names. A store the user reached through a symlink has its
      # bytes in a file the name table never heard of, and a name-based check
      # would report a correct run as a runner failure. The role table is built
      # from the paths the implementation itself resolved, so it follows the
      # link.
      roles = (observation["files"]["before"] + observation["files"]["after"])
              .to_h { |s| [s["path"], s["role"]] }
      changed_paths = observation["files"]["deltas"].map { |d| d["path"] }
      store_delta = changed_paths.any? { |p| %w[store archive].include?(roles[p]) }
      if observation["files"]["mutated"] != store_delta
        fail_case(kase, "files.mutated=#{observation["files"]["mutated"]} " \
                        "(store revision #{before_probe.dig("revisions", "store")} -> " \
                        "#{after_probe.dig("revisions", "store")}) disagrees with observed " \
                        "store/archive deltas #{changed_paths.inspect}")
      end
    end

    def fail_case(kase, message)
      @failures << "#{kase.id}: #{message}"
      warn "runner failure: #{kase.id}: #{message}"
    end

    # --- output ------------------------------------------------------------

    def emit(kase, observation)
      if @out
        File.binwrite(File.join(@out, "#{kase.id}.json"),
                      "#{JSON.pretty_generate(observation)}\n")
        warn "#{kase.id}: exit #{observation["process"]["exit_status"].inspect} " \
             "mutated=#{observation["files"]["mutated"]}" unless @quiet
      else
        $stdout.puts(JSON.generate(observation))
        $stdout.flush
      end
    end
  end

  # --- entry point ----------------------------------------------------------

  module CLIEntry
    module_function

    def run(argv, target)
      options = { work: DEFAULT_WORK, out: nil, pin_identity: false, keep: false,
                  only: [], timeout_ms: DEFAULT_TIMEOUT_MS, quiet: false, dry_run: false }
      parser = OptionParser.new do |o|
        o.banner = "usage: run [options] <case-list.jsonl>"
        o.on("--work DIR", "parent directory for fixture copies (default #{DEFAULT_WORK}); " \
                           "both implementations must use the same value") { |v| options[:work] = v }
        o.on("--out DIR", "write <case_id>.json per case instead of JSONL on stdout") { |v| options[:out] = v }
        o.on("--case ID", "run only this case (repeatable)") { |v| options[:only] << v }
        o.on("--pin-identity", "fix observation_id and metrics.wall_ms so runs are byte-identical") do
          options[:pin_identity] = true
        end
        o.on("--timeout MS", Integer, "default per-case wall budget") { |v| options[:timeout_ms] = v }
        o.on("--keep", "keep fixture copies after the run") { options[:keep] = true }
        o.on("--quiet", "no per-case progress on stderr") { options[:quiet] = true }
        o.on("--dry-run", "print the resolved plan as JSON and exit") { options[:dry_run] = true }
        o.on("-h", "--help") { puts o; exit 0 }
      end
      rest = parser.parse(argv)
      if rest.size != 1
        warn parser.help
        return 2
      end

      cases = CaseList.load(rest.first)
      unless options[:only].empty?
        cases = cases.select { |k| options[:only].include?(k.id) }
        raise Error, "no case matched #{options[:only].join(", ")}" if cases.empty?
      end

      if options[:dry_run]
        puts JSON.pretty_generate(cases.map do |k|
          { "case_id" => k.id, "fixture" => k.fixture_id, "argv" => k.argv,
            "copy_root" => File.join(File.expand_path(options[:work]), k.id) }
        end)
        return 0
      end

      runner = Runner.new(target: target, work: options[:work], out: options[:out],
                          pin_identity: options[:pin_identity], timeout_ms: options[:timeout_ms],
                          keep: options[:keep], quiet: options[:quiet])
      runner.run(cases)
      runner.failures.empty? ? 0 : 1
    rescue OptionParser::ParseError => e
      warn "run: #{e.message}"
      2
    rescue Error => e
      warn "run: #{e.message}"
      2
    end
  end
end
