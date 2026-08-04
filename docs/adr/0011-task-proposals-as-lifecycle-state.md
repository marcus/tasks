# ADR-0011: Proposals are inert lifecycle records

Status: Accepted and implemented

Date: 2026-07-27; updated 2026-08-04

Agent proposals are stored separately from committed tasks and do not affect
agenda, next-action, project, or completion semantics until the owner approves
them. Approval converts a proposal through the normal checked create path;
rejection removes it through a journaled mutation.

CLI, TUI, and API expose the same proposal lifecycle. No agent provider can
bypass approval through a private mutation path.
