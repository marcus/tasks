# ADR-0016: Footer suppression is per-element, not all-or-nothing

Status: Accepted and implemented

Date: 2026-08-10

## Context

[ADR-0013](0013-public-embeddable-tui-api.md) gave hosts `SuppressFooter` so a
host could paint shared chrome without Tasks duplicating it.
[ADR-0015](0015-embeddable-tui-fixes-from-the-first-host.md) fixed four further
contracts the first host — the Sidecar Tasks plugin — forced.

Packet 1.3 of that host builds ONE key-hint bar out of Tasks' exported commands,
and needs Tasks to stop painting its own hint row so the two do not disagree.
`SuppressFooter` was the only switch available, and it is far too blunt: Tasks'
footer stack is not a hint row, it is

- the agent transcript or last result,
- the ADR-0015 store-read banner,
- the flash message,
- the filter and context-filter lines,
- **the prompt input itself**,
- and, last, the ordinary key hint.

The host verified the consequence: with `SuppressFooter: true`, `tab` moves focus
to `tasks-prompt` and nothing renders. The user types into an invisible caret,
and all agent activity disappears — contradicting that same packet's requirement
to preserve Tasks' agent activity and provider selector.

## Decision

### A second named boolean, not a policy enum

`EmbeddedOptions.SuppressKeyHints` suppresses exactly one thing: Tasks' ordinary
key-hint row. Everything else in the stack keeps rendering.

A footer-policy enum was considered and rejected. An enum makes the two settings
look like points on one scale, so a host reading `FooterPolicy: HostOwnsHints`
still has to learn what the other values remove; and every future element that
becomes independently suppressible either forces a new enum value or a
combinatorial explosion of them. Two booleans, each named for the ONE thing it
removes, are checkable at the call site with no table lookup. The cost — a host
can set both — is defined rather than ambiguous: `SuppressFooter` wins, because
there is no hint row left to suppress.

`SuppressKeyHints` reads the same way `SuppressQuit` does under ADR-0015: **the
HOST owns that affordance.** Tasks still acts on every key it stopped
advertising; it just stops claiming the advertisement.

### `SuppressFooter` keeps its meaning, unchanged

It still removes the entire stack. It was not redefined and not deprecated. Its
only external caller passes `false`, and standalone `tasks-tui` never sets it, so
no behavior changed for anyone; and it remains the right switch for a host that
genuinely re-implements the whole footer, prompt included. What changed is its
documentation, which now enumerates what "the footer" contains, so the next host
cannot pick it by accident.

Exactly what each setting removes:

| Footer element | `SuppressFooter` | `SuppressKeyHints` |
| --- | --- | --- |
| Agent transcript / last result | removed | kept |
| Store-read banner | removed | kept |
| Flash message | removed | kept |
| Filter and context-filter lines | removed | kept |
| Prompt input | removed | kept |
| Key-hint row | removed | removed |

### The hit map follows the paint

`footerRoles`, which classifies footer rows for mouse hit testing, now mirrors
`Footer` exactly: nil under `SuppressFooter`, and no trailing chrome row under
`SuppressKeyHints`. It previously classified rows a suppressed footer never
painted, which would have mis-attributed clicks in an embedded host.

## Consequences

- `EmbeddedOptions.SuppressKeyHints` joins the public compatibility surface named
  by ADR-0013. Nothing was removed or redefined.
- Sidecar's adoption is one line: `SuppressKeyHints: true` where it builds
  `EmbeddedOptions`. It must not switch to `SuppressFooter`.
- A host that suppresses key hints must surface Tasks' commands in its own
  chrome, or the keys become undiscoverable. `ExportCommands` with
  `FooterPriority` is the supported way to do that.
- Contract tests in `pkg/tui/external_host_contract_test.go` now prove the
  prompt, transcript, and store-read banner survive hint suppression, and that
  `SuppressFooter` still removes all of them.
