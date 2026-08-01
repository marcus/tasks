# frozen_string_literal: true

require_relative "../finding"

module Conformance
  module Dimensions
    # http — a Phase 5 stub, but not a hole.
    #
    # The HTTP adapter is not ported in Phase 1: every Phase 1 observation has
    # `surface: "cli"` and an empty `http` array. What is stubbed is the
    # SPECIALIST part of the comparison the playbook calls for — "headers,
    # ETags, body limits, error envelopes, and event ordering" — each of which
    # needs its own rule about which headers are contract and which are
    # transport noise.
    #
    # What is NOT stubbed is detection. Until those rules exist, the exchange
    # list is compared structurally and exactly. A stub that skipped the
    # comparison would be a difference-hiding machine of precisely the kind this
    # epic warns about; a stub that over-compares is merely noisy, and noise is
    # the safe direction.
    module Http
      NAME = "http"

      module_function

      def compare(ctx)
        ha = Array(ctx.a["http"])
        hb = Array(ctx.b["http"])

        return if ha.empty? && hb.empty?

        ctx.add(NAME, "http", Finding::MISSING_ORACLE_COVERAGE,
                "playbook § 6 — 'For HTTP, include headers, ETags, body limits, error envelopes, and event " \
                "ordering'. Those rules are Phase 5 and are not written yet, so this exchange is compared " \
                "structurally and exactly rather than with the intended per-header policy.",
                baseline: ha.length, candidate: hb.length,
                severity: "advisory",
                detail: "HTTP exchanges observed before the HTTP comparison rules exist")

        ctx.equal!(NAME, "http", ha, hb,
                   klass: Finding::GO_DEFECT,
                   rule: "playbook § 6 — structural fallback until the Phase 5 header/ETag policy is written")
      end
    end
  end
end
