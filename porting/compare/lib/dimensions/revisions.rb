# frozen_string_literal: true

require_relative "../finding"

module Conformance
  module Dimensions
    # revisions — the concurrency tokens.
    #
    # A sixth dimension, added to the five the playbook's control-plane sketch
    # lists (cli, http, files, journal, performance), and the deviation is
    # stated out loud here and in README.md § Structure. The reason: revision
    # tokens are one of the five mismatch classes the Phase 1 gate must detect,
    # and they belong to neither the process surface nor the file surface —
    # they come from the implementation's own probe precisely so that a port
    # computing them differently shows up as a mismatch instead of being
    # silently agreed with by a harness-side derivation
    # (runners/README.md § "Why revision tokens come from a probe").
    module Revisions
      NAME = "revisions"

      module_function

      def compare(ctx)
        ctx.equal!(NAME, "revisions.store", ctx.a.dig("revisions", "store"), ctx.b.dig("revisions", "store"),
                   klass: Finding::GO_DEFECT,
                   rule: "runners/README.md § Why revision tokens come from a probe — each side reports the " \
                         "token ITS OWN code computes, so a divergence in the revision algorithm is a mismatch")

        a = key_resources(ctx.a)
        b = key_resources(ctx.b)
        (a.keys | b.keys).sort.each do |key|
          ctx.equal!(NAME, "revisions.resources[#{key.join("/")}]", a[key], b[key],
                     klass: Finding::GO_DEFECT,
                     rule: "runners/README.md § Why revision tokens come from a probe; a resource revision is " \
                           "the token a conditional write is validated against, so one differing character is " \
                           "a lost-update bug for every HTTP client")
        end

        # touched_ids is read from the implementation's own --json mutation
        # payload, never inferred from the store diff. Order is significant.
        ctx.equal!(NAME, "revisions.touched_ids",
                   Array(ctx.a.dig("revisions", "touched_ids")), Array(ctx.b.dig("revisions", "touched_ids")),
                   klass: Finding::GO_DEFECT,
                   rule: "runners/README.md § What the runner fills in — touched_ids comes from the " \
                         "implementation's own payload, never inferred from the store diff")

        ctx.equal!(NAME, "revisions.http_etag", ctx.a.dig("revisions", "http_etag"),
                   ctx.b.dig("revisions", "http_etag"),
                   klass: Finding::GO_DEFECT, rule: "observations.schema.json — revisions.http_etag")
      end

      def key_resources(obs)
        Array(obs.dig("revisions", "resources")).each_with_object({}) do |r, h|
          h[[r["id"].to_s, r["kind"].to_s]] = r
        end
      end
    end
  end
end
