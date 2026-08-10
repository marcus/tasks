# ADR-0017: A host that takes the number row can silence the view bar's numbers

Status: Accepted and implemented

Date: 2026-08-10

## Context

[ADR-0015](0015-embeddable-tui-fixes-from-the-first-host.md) gave `SuppressQuit`
the meaning "the HOST owns the affordance": Tasks keeps acting on the key and
stops advertising it. [ADR-0016](0016-footer-suppression-granularity.md) applied
the same reading to the footer's key-hint row with `SuppressKeyHints`, and chose
a second named boolean over a footer-policy enum.

The Sidecar Tasks plugin has since taken the number row for its own tab
switching — switching tabs by number is muscle memory across the whole app — so
inside the Tasks pane `1`-`6` never reach Tasks at all. Sidecar already stopped
advertising them in its own footer and in its merged help. Tasks' view bar is
the last surface still claiming them, and it is the most visible line of the
pane:

```
 1 Agenda  2 Next  3 Quadrants  4 Projects  5 Outline  6 Inbox      12 open · ? help
```

Standalone that line is correct and must not change. Embedded in this host it is
simply false. The footer's `1-6 views` hint is already covered: a host in this
position sets `SuppressKeyHints`, and the whole hint row goes with it. The view
bar had no switch at all.

## Decision

### `EmbeddedOptions.SuppressViewKeyHints`, a third named boolean

It suppresses exactly one thing: the numeric jump-key prefixes in the view tab
strip. The view names, the Inbox badge, the current-view highlight, the
degradation behavior, and the tab strip's mouse hit testing are all unchanged.

```
 Agenda  Next  Quadrants  Projects  Outline  Inbox               12 open · ? help
```

This is the third boolean rather than a first enum for the reason ADR-0016 gave:
each is named for the ONE thing it removes and is checkable at the call site. It
is also not a footer setting at all — it names the header — so folding it into a
footer policy would have been actively misleading. The three are independent;
any combination is defined, and setting all three yields a named, highlighted
view bar with no numbers and no footer.

### It is an advertisement switch, exactly like `SuppressQuit`

`1`-`6` keep jumping views when it is set. A host might take only part of the
number row, and a key that silently stopped working would be a worse failure
than a key that is merely unadvertised. Tasks removes the claim, not the
capability. `prev_view`/`next_view` on `←`/`→` and the host's command palette
remain the discoverable paths, and `ExportCommands` still describes every
`view-agenda`…`view-inbox` command.

### The narrowest label had to grow

Tab labels degrade in three steps, and the narrowest step WAS the number —
`1`, `2`, `3`. With the numbers gone that step would have painted nothing but
the keys the host had taken. `Tab` therefore carries `PlainLabel`,
`PlainCompact`, and `PlainMinimum` alongside the numbered three, and the plain
minimum is the two-letter abbreviation (`Ag`, `Nx`, `Q`, `Pr`, `Out`, `In`). A
narrow embedded pane names every view rather than numbering it.

### Scope stops at the view bar

It does NOT touch the footer hint row's `1-6 views` — that row is
`SuppressKeyHints`' business, and a host that takes the number row is by
construction a host that owns the hint bar. Two switches that both remove part
of one row would reintroduce exactly the ambiguity ADR-0016 rejected.

## Consequences

- `EmbeddedOptions.SuppressViewKeyHints` joins the public compatibility surface
  named by [ADR-0013](0013-public-embeddable-tui-api.md). Nothing was removed or
  redefined, and standalone `tasks-tui` output is byte-identical.
- Sidecar's adoption is one line: `SuppressViewKeyHints: true` where it builds
  `EmbeddedOptions`.
- A host that sets it owns view switching in its own chrome. Tasks still ships
  `←`/`→` and the exported view commands, so nothing becomes unreachable.
- Contract tests in `pkg/tui/external_host_contract_test.go` prove the numbers
  disappear, the names and current-view indicator survive, `1`-`6` still jump,
  and the default is unchanged.
