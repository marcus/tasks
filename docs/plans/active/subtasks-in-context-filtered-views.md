# Show subtasks in context-filtered TUI views

Status: reviewed; ready to implement

When a context palette filter is active, Agenda, Next, Quadrants, and Inbox use
a flat row path. Matching descendants therefore lose their tree context and
expand/collapse keys cannot affect them.

## Contract

- A task matches when it or an ancestor has the selected context.
- Include the minimal ancestor chain needed to understand each matching task.
- Preserve normal view ordering, collapse state, stable selection, and task
  identity.
- Keep slash text filtering flat; this change is only for the persistent
  context palette.
- Do not change CLI/API query semantics or stored task data.

## Implementation seam

Pass the active context through the row-building context in `internal/tui` and
teach the existing tree builder to include matching descendant paths. Keep the
predicate state-free so a future headless consumer could reuse it. Add focused
row/layout/model tests for inherited context, multiple matching branches,
collapsed ancestors, selection restoration, and empty results, then run
`go test ./...` and the normal TUI visual proof.
