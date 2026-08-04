# ADR-0009: Stable-ID anchor-relative task placement

Status: Accepted and implemented

Date: 2026-07-16; updated 2026-08-04

Placement commands express intent relative to stable task or section IDs,
never line numbers. The store validates parentage, cycle/depth constraints, and
anchor compatibility, then rewrites strict DFS pre-order atomically.

CLI fuzzy references are resolved before the application command. The HTTP API
accepts stable IDs only. Both surfaces reach the same placement operation and
stale structural expectations refuse.
