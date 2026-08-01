# frozen_string_literal: true

require_relative "files"
require_relative "../finding"

module Conformance
  module Dimensions
    # journal — undo history. The parsed structure is for readability; the index
    # file's BYTES are the proof (observations.schema.json, journal.index).
    module Journal
      NAME = "journal"

      module_function

      def compare(ctx)
        ctx.equal!(NAME, "journal.present", ctx.a.dig("journal", "present"), ctx.b.dig("journal", "present"),
                   klass: Finding::GO_DEFECT,
                   rule: "observations.schema.json — journal.present; 'no journal exists' is a state, not a gap")

        %w[version cursor blob_count].each do |k|
          ctx.equal!(NAME, "journal.#{k}", ctx.a.dig("journal", k), ctx.b.dig("journal", k),
                     klass: Finding::GO_DEFECT, rule: "playbook § 6 — for mutations, compare journal bytes")
        end

        # States are ordered: the journal is a stack and the order is the
        # history. Compared element by element so the report names the entry.
        sa = Array(ctx.a.dig("journal", "states"))
        sb = Array(ctx.b.dig("journal", "states"))
        ctx.equal!(NAME, "journal.states.length", sa.length, sb.length,
                   klass: Finding::GO_DEFECT, rule: "observations.schema.json — journal.states is ordered history")
        [sa.length, sb.length].min.times do |i|
          ctx.equal!(NAME, "journal.states[#{i}]", sa[i], sb[i],
                     klass: Finding::GO_DEFECT,
                     rule: "observations.schema.json — journal.states[]; the label is user-visible in " \
                           "`tasks undo`, and coalesce_scope is persisted into index bytes")
        end

        ctx.equal!(NAME, "journal.blob_sha256", Array(ctx.a.dig("journal", "blob_sha256")),
                   Array(ctx.b.dig("journal", "blob_sha256")),
                   klass: Finding::GO_DEFECT,
                   rule: "observations.schema.json — journal.blob_sha256; a blob is a whole store snapshot, so a " \
                         "differing digest is a differing store the port would restore")

        index(ctx)
      end

      # The index file's bytes.
      #
      # Cross-path exclusion: the index records the store's canonical ABSOLUTE
      # path in its `org` field, inside bytes the harness digests.
      # determinism.md refuses to rewrite those bytes and removes the cause
      # instead — both sides run at the same absolute path. When they cannot,
      # --cross-path excludes this comparison, and the exclusion is RECORDED in
      # the report. determinism.md: "they are compared with the journal index
      # excluded and that exclusion is reported, not silent."
      def index(ctx)
        ia = ctx.a.dig("journal", "index")
        ib = ctx.b.dig("journal", "index")
        return if ia.nil? && ib.nil?

        if ia.nil? || ib.nil?
          ctx.add(NAME, "journal.index", Finding::GO_DEFECT,
                  "observations.schema.json — journal.index",
                  baseline: ia ? "present" : "absent", candidate: ib ? "present" : "absent")
          return
        end

        if ctx.cross_path?
          ctx.exclude!("journal.index",
                       "cross-path run: the index's `org` field records the store's absolute path inside " \
                       "digested bytes, and determinism.md refuses to rewrite bytes before digesting them. " \
                       "Re-run both sides at the same absolute path to restore this comparison.")
          return
        end

        Files.compare_file_state(ctx, NAME, "journal.index", ia, ib,
                                 klass: Finding::GO_DEFECT,
                                 rule: "observations.schema.json — journal.index: 'these bytes are the proof'")
      end
    end
  end
end
