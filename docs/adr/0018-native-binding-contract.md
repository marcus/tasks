# ADR-0018: A deliberately small native binding facade

Status: Proposed — not accepted. Conditional on Phase 1 evidence (epic
`td-27fbf5`).

Date: 2026-08-01

## Context

Reaching iPhone and Android is one of the few reasons this port exists
(ADR-0013). A Go core can be linked into a native application, but the bridge
is narrow in a way that punishes an ambitious API: `gomobile bind` supports a
restricted set of types, and a Go panic that escapes into Objective-C or Java
does not produce a Go stack trace — it produces a crash the platform blames on
the app.

The application layer already has a suitable shape (ADR-0005): typed commands
in, one result vocabulary out. The temptation is to expose that shape directly,
because it is already the right *logical* surface. The bridge cannot carry it.

A second temptation is to reuse the HTTP adapter — run the server in-process on
loopback and let the app speak to it. That would make the phone a client of a
server it also hosts, with a port to allocate, a lifecycle to manage, and a
local socket where a function call belongs.

## Decision drivers

- Nothing may panic across the language boundary.
- The bridge surface should be small enough to keep stable across binding
  regenerations, because every change breaks two native call sites.
- The mobile store writes inside the application container; desktop filesystem
  policy does not apply and must not be assumed.
- Blocking work must stay off the UI thread, and cancellation must be explicit.

## Considered options

1. **Expose internal Go structs through `gomobile`.** Most of them are not
   bindable, and the ones that are would freeze internal types into a public
   ABI, so every domain refactor becomes a native-code break.
2. **Run a loopback HTTP server inside the app.** Reuses the OpenAPI contract
   at the cost of a socket, a port, a server lifecycle, and background-execution
   limits, to reach code in the same process.
3. **A small facade over `application`, with structured payloads as versioned
   JSON bytes.**

## Decision

Choose option 3.

`mobile/tasksbind` is a small facade using only strings, byte slices, integers,
booleans, and errors. Structured commands and results cross as **versioned JSON
bytes** until a typed binding proves more useful — the version field is what
makes a later typed binding an addition rather than a break.

Native calls invoke `application` directly. No loopback server inside the
phone.

The facade's obligations:

- **No panic crosses the boundary.** Every exported entry point recovers and
  converts to a stable error code. A panic that reaches Objective-C or Java is
  a defect of this layer, regardless of where it originated.
- **Every error translates to a stable code**, shared with the CLI and HTTP
  adapters' vocabulary (ADR-0005) so the same condition reads the same way on
  every surface.
- **Blocking reads and writes run away from the UI thread**, and cancellation
  is explicit rather than implied by the caller going away.
- **Object lifetimes are tested across Swift/Objective-C and Kotlin/Java.** The
  gate is repeated use leaking no bridge objects, on device, in release builds.

The mobile `Repository` and `AtomicWriter` implementations (ADR-0014) write
inside the application container. They still need crash-safe writes; they do not
need desktop Git and symlink behavior. **Desktop safety is not weakened to make
the mobile adapter simpler** — two boring stores behind one narrow interface,
not one store with a relaxed mode.

Notifications, background refresh, keychain credentials, document sharing, and
app lifecycle stay UI/platform adapter concerns on the native side. `AgentRunner`
is omitted from mobile builds entirely rather than carried as unreachable
process APIs.

The Phase 6 gate is a throwaway host app exercising list, create, edit,
complete, reorder, recurrence, undo, invalid-store refusal, and app restart
against an app-container store — passing in release builds on a **physical**
iPhone and Android device, and producing the same canonical data as desktop Go
(ADR-0015).

## What this ADR cannot decide yet

**Whether `gomobile bind` or a C ABI is the right mechanism.** `gomobile`
generates the XCFramework and AAR and the Swift/Kotlin glue; a C ABI is more
work and less fragile. The evidence that would settle it is the Phase 6 device
proof itself: binary size, build reproducibility, whether the generated bridge
survives a Go toolchain upgrade without regeneration surprises, and whether
lifetimes behave under repeated use. This ADR fixes the *shape* of the facade
so that either mechanism can carry it, which is the part that must be decided
early.

**What the facade's operations actually are.** They should be the smallest set
that supports a real app, and nobody knows that set until an app exists. The
JSON payload versioning is the hedge: the surface can grow without breaking.

## Consequences

- JSON bytes across the bridge means the native side does its own decoding and
  the compiler checks nothing about payload shape. That is the price of a
  stable, small ABI, and the version field is the only thing standing between
  it and silent drift.
- Two more platform store implementations to write and test, on devices, in
  release configuration — the slowest feedback loop in the port.
- Mobile builds diverge in capability (no agent runner, no Git merge driver).
  That divergence must be visible in the facade rather than discovered as a
  missing method at runtime.
- A native app is not part of this port. The binding proves the core is
  linkable; building the app is a separate product.
- Nothing here decides sync (ADR-0019). A phone with a local Go core and no
  sync is the expected end state of this decision.

## Related

- [ADR-0005](0005-application-boundary.md) — the facade this narrows
- [ADR-0014](0014-go-package-boundary.md) — the platform ports
- [ADR-0019](0019-sync-deferred-from-the-port.md) — explicitly not in scope
- Plan: [`docs/plans/deprecated/tasks-go-port-plan.md`](../plans/deprecated/tasks-go-port-plan.md),
  "Native bindings need a smaller API than HTTP"
