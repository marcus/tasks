# ADR-0012: Combine TUI intake queues as one projection

Status: accepted

Date: 2026-07-30

## Context

The TUI currently presents accepted `INBOX` captures in the fourth tab and
inert `PROPOSED` suggestions in a seventh Approvals tab. Both are intake queues
that the owner reviews and processes, but moving between them costs a view
switch and makes their total pending load harder to see.

The lifecycle distinction remains important. An Inbox item is accepted work;
an approval item is not accepted work and may only enter Inbox through an
explicit approval. Combining their presentation must not merge their state,
queries, actions, availability rules, or CLI/API contracts.

## Decision drivers

- The two intake queues should be reviewable from one final TUI tab.
- The tab must show the Inbox and approval counts separately.
- Proposals must remain visibly and behaviorally distinct from accepted Inbox
  work.
- Active context and text filters must scope both queues consistently.
- Approval and rejection must remain keyboard-cheap and owner-controlled.
- The tab strip, compact labels, and mouse hit spans must remain one shared
  width-aware presentation.

## Considered options

1. **Keep separate tabs and add a combined total elsewhere.** This preserves
   the current implementation but does not create one processing surface.
2. **Treat proposals as a kind of Inbox item.** This simplifies rendering but
   destroys the lifecycle and authority boundary established by ADR-0011.
3. **Add a new persisted intake category.** This duplicates information already
   expressed by task state and adds migration cost without improving behavior.
4. **Compose two existing queries into one TUI projection.** This provides one
   surface while preserving the separate state machines and non-TUI contracts.

## Decision

Choose option 4.

Replace the standalone Inbox and Approvals tabs with one final TUI tab. Keep
`:inbox` as the tab's durable view key, move it after Outline, and migrate a
saved `:approvals` view to `:inbox`. The resulting order is Agenda, Next,
Quadrants, Projects, Outline, Inbox.

The tab is a presentation composition over two existing meanings:

- an **Approvals** section containing only `PROPOSED` tasks; and
- an **Inbox** section containing the tasks selected by the existing Inbox
  query.

Approvals render first so owner decisions cannot be buried below a large Inbox.
The sections use distinct semantic styling and retain their existing row
treatments and actions. Approving still means `PROPOSED -> INBOX`; rejecting
still means `PROPOSED -> CANCELLED`.

The tab label always reports both scoped counts, including zero, rather than
collapsing them into a total. Wide and compact labels may abbreviate their
words, but must not make the two numbers ambiguous. Header rendering and mouse
hit testing consume the same presentation object.

Active context filters apply their existing OR semantics to both sections. The
Inbox section retains its context-filtered tree behavior, including visible
descendant riders; the Approvals section remains a flat canonical-order list of
matching proposals. Text search continues to use the flat filtered path.
Showing unavailable tasks affects only Inbox eligibility, not proposal
visibility.

This supersedes only ADR-0011's decision to expose a dedicated Approvals TUI
view. ADR-0011's proposal lifecycle, authority boundary, and CLI/API behavior
remain unchanged.

## Consequences

- One final tab shows the owner's complete intake workload without weakening
  proposal safety.
- Approving an item moves it between sections and shifts the two counts in one
  repaint.
- Section headers become non-selectable rows, so navigation and mouse routing
  continue to operate on ordinary selectable task rows.
- Existing saved sessions on Approvals need an explicit one-time view alias;
  silently falling back to Agenda would be a regression.
- Projects and Outline move from shortcuts `5` and `6` to `4` and `5`; the
  combined tab becomes `6`.
- The TUI and its documentation change, but the application, CLI, HTTP API,
  OpenAPI contract, and persistence schema do not.
