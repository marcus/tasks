# ADR-0003: Coalesce one TUI edit session into one undo step

Status: Accepted and implemented

Date: 2026-07-10; updated 2026-08-04

Field saves during one edit session share a private coalescing key. The journal
may extend only a tip with that key and matching process scope. Unrelated CLI,
API, or TUI writes start their own history step.

This preserves save-on-blur durability while matching the user's mental model:
one editing gesture produces one undo. Coalescing is implemented in the store
and journal layers, not the form renderer.
