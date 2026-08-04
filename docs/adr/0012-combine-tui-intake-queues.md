# ADR-0012: Present inbox tasks and proposals as one intake view

Status: Accepted and implemented

Date: 2026-07-30; updated 2026-08-04

The TUI combines unprocessed inbox tasks and inert proposals in one intake
projection while preserving their distinct lifecycle actions. Selection,
sections, and actions derive from typed row identity; presentation does not
merge their stored representations.

The combined view is a human convenience over CLI/API-capable operations. All
owned approval and processing capabilities remain available non-interactively.
