# frozen_string_literal: true

require_relative "../diffs"
require_relative "../finding"

module Conformance
  module Dimensions
    # files — the durable half of a mutation, and the primary comparison.
    #
    # "For mutations, store and journal BYTES are the primary comparison."
    # Store bytes are compared byte for byte INCLUDING key order and omitted
    # defaults, which is the deliberate asymmetry against stdout JSON
    # (errors.md): the store is a durable format two implementations must
    # round-trip, a printed payload is a message.
    module Files
      NAME = "files"

      # Roles whose bytes are the product's durable contract. Reported first and
      # with the most detail; every other file is still compared, just less
      # loudly.
      PRIMARY_ROLES = %w[store archive].freeze

      module_function

      def compare(ctx)
        # files.before is the pristine copy. A difference here means the two
        # sides did not start from the same tree, so it is a harness fault and
        # nothing downstream can be attributed to the port.
        tree(ctx, "before", klass: Finding::HARNESS_ERROR,
                            rule: "runners/README.md § The copy protocol — the fixture itself is never written to")
        tree(ctx, "after", klass: Finding::GO_DEFECT,
                           rule: "playbook § 6 — for mutations, compare final file bytes")
        deltas(ctx)
        mutated(ctx)
        rolled_back(ctx)
      end

      def index(obs, side)
        Array(obs.dig("files", side)).each_with_object({}) { |f, h| h[f["path"]] = f }
      end

      def tree(ctx, side, klass:, rule:)
        a = index(ctx.a, side)
        b = index(ctx.b, side)

        (a.keys | b.keys).sort.each do |path|
          fa = a[path]
          fb = b[path]

          # The file SET is an assertion. determinism.md § "Tempting but not
          # normalized" refuses to filter the lock sidecar and the leftover
          # atomic-write temp file: whether the port creates them, when, and
          # with what mode is precisely the platform-shaped behavior a port is
          # most likely to get wrong.
          if fa.nil? || fb.nil?
            ctx.add(NAME, "files.#{side}[#{path}]", klass,
                    "#{rule}; determinism.md § Tempting but not normalized — nothing is filtered from the tree",
                    baseline: fa ? "present" : "absent", candidate: fb ? "present" : "absent",
                    detail: { "role" => (fa || fb)["role"] })
            next
          end

          compare_file_state(ctx, NAME, "files.#{side}[#{path}]", fa, fb, klass: klass, rule: rule)
        end
      end

      # Shared with the journal dimension, which compares the index file with
      # exactly these rules.
      def compare_file_state(ctx, dimension, field, fa, fb, klass:, rule:)
        %w[role present size_bytes line_count symlink_target].each do |k|
          ctx.equal!(dimension, "#{field}.#{k}", fa[k], fb[k], klass: klass, rule: rule)
        end

        ctx.equal!(dimension, "#{field}.mode", fa["mode"], fb["mode"], klass: klass,
                   rule: "determinism.md § Tempting but not normalized — carrying an existing file's permission " \
                         "bits across an atomic replacement is a documented safety property; a chmod-600 store " \
                         "must not widen to 644")

        return if fa["sha256"] == fb["sha256"]

        detail = { "baseline_sha256" => fa["sha256"], "candidate_sha256" => fb["sha256"] }
        ba = decode(fa["content_base64"])
        bb = decode(fb["content_base64"])
        if ba && bb
          detail["line_diff"] = Diffs.line_diff(ba, bb)
          detail["byte_diff"] = Diffs.byte_diff(ba, bb)
        else
          detail["note"] = "file too large to embed; compared by digest"
        end

        primary = PRIMARY_ROLES.include?(fa["role"])
        ctx.add(dimension, "#{field}.sha256", klass,
                if primary
                  "errors.md § Structured errors — JSONL store bytes are compared byte for byte, INCLUDING key " \
                    "order and omitted defaults. The store is a durable format two implementations must " \
                    "round-trip, so a reordered key is a real difference here even though it is not on stdout."
                else
                  rule
                end,
                baseline: fa["sha256"], candidate: fb["sha256"], detail: detail)
      end

      def decode(b64)
        return nil if b64.nil?

        Base64.decode64(b64)
      rescue ArgumentError
        nil
      end

      def deltas(ctx)
        a = Array(ctx.a.dig("files", "deltas")).each_with_object({}) { |d, h| h[d["path"]] = d }
        b = Array(ctx.b.dig("files", "deltas")).each_with_object({}) { |d, h| h[d["path"]] = d }
        (a.keys | b.keys).sort.each do |path|
          ctx.equal!(NAME, "files.deltas[#{path}]", a[path], b[path],
                     klass: Finding::GO_DEFECT,
                     rule: "observations.schema.json — files.deltas; an empty delta set is a meaningful " \
                           "assertion, not an omission")
        end
      end

      def mutated(ctx)
        ctx.equal!(NAME, "files.mutated", ctx.a.dig("files", "mutated"), ctx.b.dig("files", "mutated"),
                   klass: Finding::GO_DEFECT,
                   rule: "runners/README.md § Invariants — files.mutated comes from the implementation's own " \
                         "revision token, measured independently of the harness's digests")
      end

      # --- rollback ------------------------------------------------------------
      #
      # errors.md names this pair as one the corpus must distinguish: "failed
      # post-write validation (wrote, rolled back)" vs "never wrote" — identical
      # exit status, identical (empty) deltas, identical store bytes. The
      # filesystem cannot tell you, which is exactly why the field exists.
      #
      # Today `files.rolled_back` is ALWAYS null on both sides, because the Ruby
      # CLI signals a write-then-revert only as an extra sentence on stderr and
      # the runner correctly refused to parse Ruby prose into a language-neutral
      # protocol. So this comparison is live but currently vacuous, and the
      # difference is caught one layer down, as a stderr byte difference — real
      # detection, but unlabelled. See porting/compare/README.md § "The rollback
      # gap" and porting/evidence/phase1/GATE.md.
      def rolled_back(ctx)
        va = ctx.a.dig("files", "rolled_back")
        vb = ctx.b.dig("files", "rolled_back")
        ctx.equal!(NAME, "files.rolled_back", va, vb,
                   klass: Finding::GO_DEFECT,
                   rule: "errors.md § Failure shapes the corpus must distinguish — wrote-and-reverted vs " \
                         "never-wrote differ in this field and in nothing else on the filesystem")
        return unless va.nil? && vb.nil?

        ctx.note_rollback_unlabelled
      end
    end
  end
end
