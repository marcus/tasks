# frozen_string_literal: true

# normalize.rb — the complete list of normalizations the comparator applies,
# and nothing else.
#
# Normalizing removes a difference by HIDING it. That is the failure mode this
# whole epic warns about, so the rule from porting/specs/determinism.md is
# enforced here structurally: every normalization in this file carries the
# sentence "a user cannot observe this because …", written out in full. If a new
# normalization cannot be given that sentence honestly, it does not belong here
# — the answer is a pin (porting/specs/determinism.md § Pins) or a finding.
#
# There are exactly four, matching determinism.md § Normalizations one for one.
# Everything else an observation carries is either compared or explicitly
# EXCLUDED, and exclusion is a different thing: an excluded field is one the
# spec says is provenance rather than behavior (porting/specs/errors.md § "What
# is not compared at all"). Exclusions live in comparator.rb next to the
# comparison they suppress, so they are readable at the point of use.
module Conformance
  module Normalize
    OBSERVATION_ID = "<observation-id>"
    COPY_ROOT = "<copy-root>"
    JOURNAL_KEY = "<journal-key>"

    # `.state/tasks/journal/<16 hex>/…` — the journal directory key.
    JOURNAL_KEY_RE = %r{(?<=/journal/)[0-9a-f]{16}(?=/|\z)}
    JOURNAL_KEY_BYTES_RE = %r{(?<=/journal/)[0-9a-f]{16}(?=/|\z)}n

    module_function

    # --- 1. observation_id ---------------------------------------------------
    #
    # A UUID (or `obs_<case_id>` under --pin-identity) the harness assigns per
    # record.
    #
    # A user cannot observe this because it is not produced by the
    # implementation at all: it is minted by the runner after the invocation has
    # already exited, it is never written to the store, the journal, stdout or
    # stderr, and no command or configuration names it. It exists so a piece of
    # evidence can be cited.
    def observation_id(_value) = OBSERVATION_ID

    # --- 2. the copy-root prefix --------------------------------------------
    #
    # Each case runs against its own copy of a fixture, so the copy's absolute
    # path is chosen by the harness and differs by construction between two
    # runs at different --work roots.
    #
    # A user cannot observe this because the prefix is the harness's choice, not
    # the implementation's: the implementation is handed TASKS_DIR/HOME/XDG_*
    # and echoes back what it was given. Everything INSIDE the copy stays
    # compared — the path relative to the copy root, the file set, the modes and
    # every byte — so naming the wrong file within the copy is still a failure.
    #
    # Applied to: fixture.copy_root, invocation.env[].value,
    # invocation.pins[].value, and the decoded bytes of stdout and stderr.
    #
    # Deliberately NOT applied to file contents, including the journal index's
    # `org` field. determinism.md § "Tempting but not normalized" refuses that
    # rewrite by name: rewriting bytes before digesting them is exactly the move
    # that makes a byte comparison stop meaning anything. The cause is removed
    # instead (both sides run at the same absolute path); a cross-path run must
    # pass --cross-path, which EXCLUDES the journal index and says so in the
    # report rather than quietly rewriting it.
    #
    # Returns every spelling of the copy root that can appear in an
    # observation, longest first so the longest match wins. macOS resolves
    # /tmp through a /private symlink, so a runner's own record says
    # `/tmp/tasks-conformance/<case>` while a path the implementation
    # canonicalised says `/private/tmp/tasks-conformance/<case>`. Both name the
    # same directory and both are the harness's choice.
    def copy_root_spellings(root)
      return [] if root.nil? || root.empty?

      spellings = [root]
      spellings << "/private#{root}" if root.start_with?("/tmp/", "/var/")
      spellings << root.sub(%r{\A/private}, "") if root.start_with?("/private/")
      spellings.uniq.sort_by { |s| -s.length }
    end

    def rewrite_copy_root(value, spellings)
      return value unless value.is_a?(String)

      spellings.reduce(value) { |acc, root| acc.gsub(root, COPY_ROOT) }
    end

    # Byte-level form, for stdout/stderr, which may not be valid UTF-8. Works on
    # ASCII-8BIT so an invalid byte sequence round-trips untouched.
    def rewrite_copy_root_bytes(bytes, spellings)
      return bytes unless bytes.is_a?(String)

      out = bytes.dup.force_encoding(Encoding::ASCII_8BIT)
      spellings.each do |root|
        out = out.gsub(root.dup.force_encoding(Encoding::ASCII_8BIT),
                       COPY_ROOT.dup.force_encoding(Encoding::ASCII_8BIT))
      end
      out
    end

    # --- 3. the journal directory name ---------------------------------------
    #
    # The journal lives at `…/tasks/journal/<key>/`, where <key> is the first 16
    # hex characters of a SHA-256 of the store's canonical absolute path. Two
    # copies at two paths get two keys.
    #
    # A user cannot observe this because it is a private cache key under
    # XDG_STATE_HOME: no command prints it, no configuration names it, no
    # documentation gives it a name, and its only job is to keep two different
    # task files from sharing one history. The property that actually matters —
    # different stores get different keys, the same store always gets the same
    # key — is a separate testable claim (test/test_journal.rb) and is NOT what
    # is being hidden here; only the key's literal value is.
    #
    # Applied to PATHS only: files.before[].path, files.after[].path,
    # files.deltas[].path, journal.index.path. Never to file contents — see
    # normalization 2 for why bytes are left alone.
    def rewrite_journal_key(path)
      return path unless path.is_a?(String)

      path.sub(JOURNAL_KEY_RE, JOURNAL_KEY)
    end

    # --- 4. metrics.* --------------------------------------------------------
    #
    # Wall time, CPU time, peak RSS, bytes written.
    #
    # A user cannot observe this AS CONFORMANCE because performance is a
    # separate gate with its own budgets: porting/specs/errors.md § "What is not
    # compared at all" states that metrics must never be able to fail a
    # conformance case and must never be able to pass one either. Note this is
    # the one entry where the honest sentence is "a user can observe it, but not
    # here": a slow port is a real user-visible problem, which is exactly why it
    # gets its own gate instead of being folded into byte equality.
    #
    # metrics.bytes_written is deterministic and it would be tempting to promote
    # it into the identity comparison. It stays out, because every byte it
    # counts is already compared directly and far more precisely in the `files`
    # dimension — promoting it would add no detection and would put a metrics
    # field on the gate path in contradiction of the spec.
    #
    # Implemented as a routing rule rather than a rewrite: the `performance`
    # dimension reads metrics and can only ever emit advisory findings.
    METRICS_ARE_ADVISORY_ONLY = true

    # --- application ---------------------------------------------------------

    # Return a normalized deep copy of an observation, plus the spellings of the
    # copy root that were rewritten (the report records them, so a reader can
    # see exactly what was hidden).
    def observation(obs)
      spellings = copy_root_spellings(obs.dig("fixture", "copy_root"))
      out = deep_dup(obs)

      out["observation_id"] = observation_id(out["observation_id"])
      out["fixture"]["copy_root"] = rewrite_copy_root(out.dig("fixture", "copy_root"), spellings) if out["fixture"]

      Array(out.dig("invocation", "env")).each do |entry|
        entry["value"] = rewrite_copy_root(entry["value"], spellings)
      end
      Array(out.dig("invocation", "pins")).each do |entry|
        entry["value"] = rewrite_copy_root(entry["value"], spellings)
      end

      %w[before after].each do |side|
        Array(out.dig("files", side)).each { |f| f["path"] = rewrite_journal_key(f["path"]) }
      end
      Array(out.dig("files", "deltas")).each { |d| d["path"] = rewrite_journal_key(d["path"]) }
      if out.dig("journal", "index").is_a?(Hash)
        out["journal"]["index"]["path"] = rewrite_journal_key(out["journal"]["index"]["path"])
      end

      [out, spellings]
    end

    def deep_dup(value)
      case value
      when Hash then value.each_with_object({}) { |(k, v), h| h[k] = deep_dup(v) }
      when Array then value.map { |v| deep_dup(v) }
      when String then value.dup
      else value
      end
    end
  end
end
