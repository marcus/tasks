# ADR-0006: Rack 3 and Puma behind bin/tasks-api, no web framework

Status: Accepted

Date: 2026-07-14

Implementation note: the dependency/toolchain foundation is implemented in the
committed Gemfile and lockfile. The Bundler-backed gate boots a real Puma server
through a Rack::Lint `config.ru` fixture and validates the OpenAPI 3.1 contract
and its embedded examples with `openapi_first`. The production `config.ru`,
`bin/tasks-api`, and Rack adapter still land with the CRUD transport slice.

## Context

The API is another adapter around `Tasks::Application`, so it needs an HTTP
runtime, but the CLI and TUI must keep starting on the Ruby standard library
alone, without Bundler or web gems. The route count is small (the resource set
plus a handful of support endpoints), and the project has so far avoided any
runtime dependency.

WEBrick is sometimes proposed as a "stdlib" HTTP server, but it has not been
part of Ruby itself since Ruby 3.0 — treating it as stdlib is a fiction, and it
is a dated, unmaintained runtime compared with the current server ecosystem.

## Decision drivers

- Keep the HTTP application portable across Ruby servers rather than coupling to
  one server's API.
- Use a maintained, current HTTP runtime.
- Do not burden `bin/tasks`/`bin/tasks-tui` or the core test gate with web gems.
- Keep the routing layer small and legible; do not adopt a framework the route
  count cannot justify.
- Keep test-only tooling out of the runtime dependency set.

## Considered options

1. WEBrick, labeled "stdlib." Rejected: no longer shipped with Ruby, and a
   weaker runtime than the maintained alternatives.
2. A full framework (Rails or Sinatra). Rejected for v1: far more surface and
   dependency weight than a dozen routes need, and it would obscure the thin
   adapter boundary this design depends on.
3. A small Rack 3 application served locally by Puma, loaded only by the API
   entry point.

## Decision

Choose option 3.

- The HTTP layer is a small Rack 3 application with routing implemented
  directly; no Rails, Sinatra, or other framework in v1.
- Puma is the local HTTP runtime. Puma loads `config.ru` itself, so the separate
  `rackup` gem is not needed. `bin/tasks-api` runs a non-clustered Puma process
  bound to `127.0.0.1` by default, resolves configuration exactly once via the
  same `Tasks::Config.resolve` the CLI and TUI use, prints the selected safe
  bind/port and data-source labels (never raw paths), and fails with a clear
  install message if the API gems are absent.
- A committed `Gemfile` and `Gemfile.lock` scope the runtime dependency set to
  Rack and Puma. Test-only gems — Minitest, `rack-test`, and `openapi_first` —
  live in the Gemfile's test group and do not extend the runtime dependency
  set. `openapi_first` is selected because the locked toolchain demonstrably
  loads this repository's OpenAPI 3.1/JSON Schema contract, validates nullable
  unions and local references, and validates every embedded request/response
  example.
- Rack and Puma are loaded only by `bin/tasks-api` and the API tests. Running
  `bin/tasks`, `bin/tasks-tui`, and `ruby test/all.rb` must not require Rack or
  Puma, and dependency-boundary tests enforce that the core CLI/TUI files do not
  load them.
- `openapi_first` rejects undocumented JSON body fields through the contract's
  schemas, but accepts undocumented query keys. The hand-written Rack adapter
  must therefore compare raw query keys with the operation's documented keys
  and reject extras explicitly; the compatibility test records this boundary.

## Consequences

The CLI and TUI keep their stdlib-only startup and their existing test gate,
while the API gets a maintained runtime and a portable application. A second,
Bundler-backed gate covers the application, Rack, OpenAPI, and real-server
tests. Because routing is hand-written Rack, adding endpoints is explicit and
reviewable; if the route count or middleware needs ever outgrow a hand-rolled
router, adopting a framework remains a contained, later decision that does not
touch domain or application code.
