# ADR-0005: A typed application boundary over a request-scoped store

Status: Accepted and implemented

Date: 2026-07-14; updated for the Go implementation 2026-08-04

## Context

The CLI, TUI, and HTTP API need the same task semantics: validation, named
views, stable-id lookup, canonical representations, and atomic mutations. If an
adapter owns any of that behavior, the surfaces drift and agents receive a
different product from interactive users.

The JSONL store also carries mutable read state. Sharing one store across HTTP
requests would make request isolation harder without improving the small local
workloads this application serves.

## Decision

`internal/application` is the transport-neutral boundary. It accepts typed Go
inputs, returns one outcome vocabulary, and knows nothing about arguments,
terminal rendering, or HTTP status codes. Adapters translate those outcomes to
their own presentation contracts.

Persistence is exposed through the narrow store interfaces in
`internal/application/store.go`. Production wiring creates a fresh
`internal/store.Store` per operation or HTTP request. File locks, checked
writes, revisions, and journal semantics remain in the store layer and apply
equally to CLI, TUI, and API callers.

Reads use immutable snapshots and the query layer in `internal/taskquery`.
Commands perform one validated, journaled transaction rather than a sequence
assembled by an adapter.

## Consequences

- Capabilities owned by tasks have deterministic non-interactive paths.
- CLI, TUI, and API parity can be asserted at adapter boundaries.
- Store implementation details do not leak into surfaces.
- Small task lists are reparsed per operation. A synchronized immutable cache
  may be added behind the boundary only if measurement justifies it.
