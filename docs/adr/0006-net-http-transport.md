# ADR-0006: Go net/http for the loopback API transport

Status: Accepted and implemented

Date: 2026-07-14; superseded for the Go implementation 2026-08-04

## Context

The API is a thin adapter over `internal/application`. It needs a maintained
HTTP runtime without adding a framework or a second domain implementation.
The service is local-only and the route set is small.

## Decision

`cmd/tasks-api` uses Go's `net/http` server and the hand-written adapter in
`internal/api`.

- The listener is fixed to `127.0.0.1`; non-loopback bind requests are refused.
- Host and Origin validation remain explicit adapter responsibilities.
- Stable task ids are the only HTTP locators.
- Each request gets fresh application/store wiring while file locking and
  revision checks remain in the shared core.
- Request/response schemas and examples are specified in
  `docs/api/openapi.yaml` and exercised by adapter tests.
- Configuration is resolved before the listener starts, and an unconfigured
  binary exits without guessing a data directory.

## Consequences

The API ships in one static-capable Go binary with no application server or web
framework dependency. Routing stays explicit and reviewable. If transport
needs grow beyond this small loopback service, the adapter can be replaced
without changing application or storage semantics.
