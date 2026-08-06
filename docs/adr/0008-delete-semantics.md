# ADR-0008: Delete is explicit, guarded, and undoable

Status: Accepted and implemented

Date: 2026-07-15; updated 2026-08-04

`tasks delete` is distinct from normal GTD completion or cancellation. It
requires an explicit target, refuses a parent with descendants unless cascade
was requested, runs through `application.DeleteCommand`, and records one undo
entry. API deletion has the same domain outcome and requires an ETag
precondition. The TUI surfaces the same mutation via `#` / Delete with an
always-on confirm modal (cascade confirm when the selection has descendants).

The store checks structure before and after mutation. A failure writes nothing;
no adapter may implement deletion by editing JSONL itself.
