# frozen_string_literal: true

require "base64"
require "digest"
require_relative "../diffs"
require_relative "../finding"

module Conformance
  module Dimensions
    # cli — the process surface: what the invocation was, what status it
    # returned, and the exact bytes it wrote to stdout and stderr.
    #
    # The three layers of porting/specs/errors.md, in its order of binding
    # strength: exit status (contract, exact), structured error (contract, as
    # parsed data), diagnostic text (contract, byte for byte).
    module Cli
      NAME = "cli"

      module_function

      def compare(ctx)
        same_case?(ctx)
        pins(ctx)
        status(ctx)
        streams(ctx)
      end

      # --- was this even the same invocation? ---------------------------------
      #
      # Compared first, and classified harness_error rather than go_defect: if
      # the two sides ran different argv, a different fixture or a different
      # cwd, nothing downstream is a statement about the port.
      def same_case?(ctx)
        ctx.equal!(NAME, "schema_version", ctx.a["schema_version"], ctx.b["schema_version"],
                   klass: Finding::HARNESS_ERROR,
                   rule: "observations.schema.json — two observation formats cannot be compared")
        %w[id class root_sha256].each do |k|
          ctx.equal!(NAME, "fixture.#{k}", ctx.a.dig("fixture", k), ctx.b.dig("fixture", k),
                     klass: Finding::HARNESS_ERROR,
                     rule: "runners/README.md § The copy protocol — both sides must start from the same tree")
        end
        %w[surface argv cwd timeout_ms].each do |k|
          ctx.equal!(NAME, "invocation.#{k}", ctx.a.dig("invocation", k), ctx.b.dig("invocation", k),
                     klass: Finding::HARNESS_ERROR,
                     rule: "runners/README.md § The case list — argv is passed through verbatim")
        end
        ctx.equal!(NAME, "invocation.stdin", ctx.a.dig("invocation", "stdin"), ctx.b.dig("invocation", "stdin"),
                   klass: Finding::HARNESS_ERROR,
                   rule: "runners/README.md § The pinned environment — stdin is always attached")
        ctx.equal!(NAME, "invocation.env", ctx.a.dig("invocation", "env"), ctx.b.dig("invocation", "env"),
                   klass: Finding::HARNESS_ERROR,
                   rule: "runners/README.md § The pinned environment — the pin set is mandatory and identical")
      end

      # --- pins ---------------------------------------------------------------
      #
      # invocation.pins is the implementation's own report of what it RESOLVED.
      # Two implementations parsing one pin differently is a real defect and this
      # is the only place it shows up (runners/README.md § Why `pins` comes from
      # a probe). A dropped pin on either side means that side's run was not
      # actually pinned, which is a harness fault, not a port defect.
      def pins(ctx)
        a = index_pins(ctx.a)
        b = index_pins(ctx.b)
        (a.keys | b.keys).sort.each do |name|
          pa = a[name]
          pb = b[name]
          if pa.nil? || pb.nil?
            ctx.add(NAME, "invocation.pins[#{name}]", Finding::HARNESS_ERROR,
                    "runners/README.md § The pinned environment — set all of it, not a subset",
                    baseline: pa, candidate: pb,
                    detail: "pin reported by only one side")
            next
          end
          if pa["applied"] == false || pb["applied"] == false
            ctx.add(NAME, "invocation.pins[#{name}].applied", Finding::HARNESS_ERROR,
                    "runners/README.md § The probe — applied:false is a hard failure, not a warning",
                    baseline: pa["applied"], candidate: pb["applied"],
                    detail: "a pin was set and silently ignored; the run is not reproducible")
            next
          end
          ctx.equal!(NAME, "invocation.pins[#{name}].value", pa["value"], pb["value"],
                     klass: Finding::GO_DEFECT,
                     rule: "runners/README.md § Why `pins` comes from a probe — comparing RESOLVED values " \
                           "catches two implementations parsing one pin differently")
        end
      end

      def index_pins(obs)
        Array(obs.dig("invocation", "pins")).each_with_object({}) { |p, h| h[p["name"]] = p }
      end

      # --- exit status --------------------------------------------------------
      def status(ctx)
        ctx.equal!(NAME, "process.exit_status", ctx.a.dig("process", "exit_status"), ctx.b.dig("process", "exit_status"),
                   klass: Finding::GO_DEFECT,
                   rule: "errors.md § Exit status is the smallest and strongest contract — compared exactly on " \
                         "every case; 'both nonzero' is never a match, because 1 vs 2 is a product feature agents " \
                         "branch on")

        # "process.signal non-null is never a pass" and "timed_out true is never
        # a pass" — absolute rules, so they fire even when both sides agree.
        %w[signal].each do |k|
          va = ctx.a.dig("process", k)
          vb = ctx.b.dig("process", k)
          if va || vb
            ctx.add(NAME, "process.signal", Finding::GO_DEFECT,
                    "errors.md § Exit status — a crash is a crash even if the case expected a failure",
                    baseline: va, candidate: vb,
                    detail: "process terminated by a signal")
          end
        end
        if ctx.a.dig("process", "timed_out") || ctx.b.dig("process", "timed_out")
          ctx.add(NAME, "process.timed_out", Finding::HARNESS_ERROR,
                  "errors.md § Exit status — a timed-out observation is never a pass, whatever the rest says",
                  baseline: ctx.a.dig("process", "timed_out"), candidate: ctx.b.dig("process", "timed_out"))
        end
      end

      # --- stdout and stderr ---------------------------------------------------
      def streams(ctx)
        stream(ctx, "stdout", structured: true)
        stream(ctx, "stderr", structured: false)
      end

      # `structured: true` means "compare as parsed data when the bytes parse as
      # JSON". That applies to stdout only. stderr is diagnostic text and is
      # never parsed: errors.md makes it contract by default, byte for byte,
      # with exactly the copy-root rewrite applied and nothing else.
      def stream(ctx, which, structured:)
        sa = ctx.a.dig("process", which)
        sb = ctx.b.dig("process", which)
        field = "process.#{which}"

        ctx.equal!(NAME, "#{field}.valid_utf8", sa["valid_utf8"], sb["valid_utf8"],
                   klass: Finding::GO_DEFECT,
                   rule: "errors.md § Non-UTF-8 diagnostics — a lossy decode would silently equalise two " \
                         "implementations that mangle the bytes differently")

        bytes_a = ctx.stream_bytes(ctx.a, which)
        bytes_b = ctx.stream_bytes(ctx.b, which)

        # Truncated capture: sha256 is authoritative, bytes_base64 must not be
        # used for equality. Say so rather than comparing a prefix.
        if bytes_a.nil? || bytes_b.nil?
          ctx.equal!(NAME, "#{field}.sha256", sa["sha256"], sb["sha256"],
                     klass: Finding::GO_DEFECT,
                     rule: "errors.md § Non-UTF-8 diagnostics — sha256 is authoritative")
          ctx.equal!(NAME, "#{field}.size_bytes", sa["size_bytes"], sb["size_bytes"],
                     klass: Finding::GO_DEFECT, rule: "observations.schema.json — stream.size_bytes")
          ctx.add(NAME, "#{field}.bytes_base64", Finding::HARNESS_ERROR,
                  "observations.schema.json — a truncated stream must not be used for equality",
                  detail: "stream truncated; compared by digest only, so the difference is not localisable")
          return
        end

        return if bytes_a == bytes_b

        kind_a, val_a = Diffs.classify_stream(bytes_a)
        kind_b, val_b = Diffs.classify_stream(bytes_b)

        if structured && kind_a != :text && kind_a == kind_b
          # The deliberate asymmetry: object key ORDER inside stdout JSON is not
          # compared (parsers consume it), while JSONL store key order is a hard
          # byte contract. See errors.md § "Note the asymmetry deliberately".
          diff = kind_a == :jsonl ? Diffs.json_diff(val_a, val_b) : Diffs.json_diff(val_a, val_b)
          if diff.empty?
            # Bytes differ, parsed data does not: this is exactly the key-order
            # (or whitespace) case the spec says to ignore. Not a finding.
            return
          end
          ctx.add(NAME, "#{field} (json)", Finding::GO_DEFECT,
                  "errors.md § Structured errors are compared as data, not as text — same keys, same values, " \
                  "same types; an omitted key and a null key are different answers; array order is significant",
                  baseline: Finding.render(val_a), candidate: Finding.render(val_b),
                  detail: { "format" => kind_a.to_s, "differences" => diff })
          return
        end

        if structured && kind_a != kind_b
          ctx.add(NAME, "#{field}.format", Finding::GO_DEFECT,
                  "errors.md § Structured errors are compared as data — one side emitted parseable JSON and " \
                  "the other did not",
                  baseline: kind_a.to_s, candidate: kind_b.to_s)
        end

        ctx.add(NAME, "#{field} (bytes)", Finding::GO_DEFECT,
                "errors.md § Diagnostic text is contract until proved otherwise — compared byte for byte, " \
                "with only the copy-root rewrite applied. Wording, whitespace, punctuation and line order " \
                "are all part of the error UX an agent reads back to a user.",
                baseline: Finding.render(preview(bytes_a)), candidate: Finding.render(preview(bytes_b)),
                detail: Diffs.byte_diff(bytes_a, bytes_b))
      end

      def preview(bytes)
        bytes.dup.force_encoding(Encoding::UTF_8).scrub("·")
      end
    end
  end
end
