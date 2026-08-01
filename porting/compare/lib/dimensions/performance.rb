# frozen_string_literal: true

require_relative "../finding"

module Conformance
  module Dimensions
    # performance — metrics, and they can never decide anything.
    #
    # errors.md § "What is not compared at all": metrics are advisory.
    # "It must never be able to fail a conformance case, and it must never be
    # able to pass one either." Both halves matter — a comparator that quietly
    # let a metric contribute to a PASS would be as wrong as one that let it
    # fail.
    #
    # So this dimension is structurally incapable of emitting a gate finding: it
    # calls ctx.observe, which only ever records advisory rows.
    module Performance
      NAME = "performance"

      module_function

      def compare(ctx)
        ma = ctx.a["metrics"] || {}
        mb = ctx.b["metrics"] || {}
        row = {}
        (ma.keys | mb.keys).sort.each do |k|
          row[k] = { "baseline" => ma[k], "candidate" => mb[k] }
        end

        # wall_ms is fixed to 0 by --pin-identity, so a ratio is only meaningful
        # on unpinned runs. Reported, never judged: budgets belong to the
        # separate performance gate (playbook § 7, "time, allocation,
        # file-descriptor, goroutine, and peak-RSS budgets").
        ctx.observe(NAME, "metrics", row)

        # metrics.bytes_written is deterministic and it would be tempting to
        # promote it into the identity comparison. It stays advisory: every byte
        # it counts is already compared far more precisely in the `files`
        # dimension, so promoting it would add no detection while putting a
        # metrics field on the gate path against the spec.
        nil
      end
    end
  end
end
