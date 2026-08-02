# Technical Architecture & System Design

This document details the architectural design, storage semantics, data integrity mechanisms, and concurrency contracts of the `tasks` system.

---

## 1. Storage & Data Model

### One File, One Line Per Task
Your data is stored in `tasks.jsonl` (and `archive.jsonl`). Each line is a self-contained JSON object representing either a section or a task.

- **Fixed Key Order & Omitted Defaults**: Keys are serialized in deterministic order with empty fields omitted. Every mutation results in an exact, reviewable one-line git diff.
- **Explicit Parent Pointers**: Hierarchical relationships and project trees use explicit `parent` pointers (`id` -> `parent`) rather than file indentation.
- **No Block Boundaries**: Eliminates outline-parsing ambiguities and whitespace balancing issues.
- **Separation of Data & Logic**: Data files can live anywhere on disk, decoupled from application source code.

---

## 2. Atomic Writes & Structural Auditing

### Atomic Swap Guarantees
All state mutations follow a strict atomic swap protocol:
1. Write full content to a sibling temporary file on the target filesystem.
2. Call `fsync` on the temporary file to flush data to physical disk.
3. Perform an atomic rename (`rename(2)`) over the target file.
4. Call `fsync` on the parent directory to commit the directory entry.

**Guarantees**:
- Concurrent readers and system crashes see either the entire previous file state or the entire new state—never a torn or partial write.
- **Symlink Preservation**: The swap resolves and preserves symlinks (maintaining compatibility with Dropbox, iCloud, or dotfiles repositories).
- **Permission Bit Retention**: The file mode and permission bits of the target file are carried over to the replacement file.

### Post-Mutation Rollbacks & Integrity Auditing
- After every write operation, `Tasks::Store` parses and validates the written file in memory. If structural constraints fail, the transaction rolls back to the prior state.
- `bin/tasks check` performs out-of-band validation of file integrity, checking for missing parent references, broken IDs, or invalid field formats.

---

## 3. Undo / Redo Journaling

### Content-Addressed Persistence Journal
Rather than maintaining an in-memory stack, mutations persist to a disk journal:
- **Location**: `$XDG_STATE_HOME/tasks/journal/` (keyed by the canonical path of the task file).
- **Shared History**: The CLI, TUI, and agent invocations share a unified, persistent linear history. Undoing from a cold shell reverts the last action performed in the TUI or by an autonomous agent.
- **Branching Rules**: Executing a new mutation while pointed at an earlier journal position truncates the unreachable redo tail.

---

## 4. Layered Application Architecture

```
┌─────────────────────────────────────────────────────────┐
│                     Adapters                            │
│    bin/tasks (CLI)  │  bin/tasks-tui  │  bin/tasks-api  │
└───────────────────┬─┴─────────────────┬─────────────────┘
                    │                   │
                    ▼                   ▼
┌─────────────────────────────────────────────────────────┐
│                 Tasks::Application                      │
│        (Facade, Typed Views, Immutable Snapshots)      │
└───────────────────────────┬─────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────┐
│                     Tasks::Store                        │
│         (Parsing, Change Detection, File Locking)       │
└─────────────────────────────────────────────────────────┘
```

- **`Tasks::Store`**: Manages low-level JSONL parsing, change detection, file locking, and atomic file swaps.
- **`Tasks::Application`**: Serves as a persistence-neutral domain facade providing immutable read snapshots and checked query views.
- **Adapters (`CLI`, `TUI`, `API`)**: Thin adapters consuming `Tasks::Application`.
- **Snapshot Immutability**: Readers holding an application snapshot obtain a frozen, coherent view of the task tree without risk of reading torn or partially updated state during background writes.

---

## 5. Multi-Device Git 3-Way Merge Driver

Every modified task record retains an `updated` timestamp formatted as `ISO8601#device_id` (e.g., `2026-07-16T14:03:11Z#home`).

### Field-Aware 3-Way Merge Strategy
When installed via `bin/install-merge-driver`, Git uses custom 3-way domain logic for resolving concurrent changes across devices:

1. **Stable ID Alignment**: Tasks are matched across base, ours, and theirs revisions by stable 8-hex `id`.
2. **Tag Unioning**: Added tags from both branches are merged into a set union.
3. **State Progression Preference**: Progressed task states (e.g., `TODO` -> `DONE`) are favored over stale states.
4. **Timestamp Conflict Resolution**: The `updated` timestamp is used exclusively for resolving genuine same-field edits.
5. **Order Preservation**: Ours-first sibling ordering is maintained.
6. **Audit Logging**: Merge choices are logged to `.tasks-merge.log` in the data repository.
7. **Failure Safety**: Malformed input files or validation failures exit with non-zero status and leave the path conflicted — both sides written verbatim inside ordinary `<<<<<<<` / `=======` / `>>>>>>>` fences, with the reason on the opening marker. Nothing is merged, nothing is summarized, and `tasks check` refuses the result, so a refused merge cannot be staged by reflex.

---

## 6. Local HTTP API Architecture & Security

`bin/tasks-api` exposes a loopback REST API backed by OpenAPI 3.1 specifications ([`docs/api/openapi.yaml`](file:///Users/marcus/code/tasks/docs/api/openapi.yaml)).

### Security Boundary & Host Isolation
- **Loopback Only**: Listens strictly on `127.0.0.1` (default port `4747`).
- **Header Validation**: Validates `Host` and browser mutation `Origin` headers to prevent cross-site request forgery and DNS rebinding attacks. Rejects forwarded-host headers.
- **No Remote/Auth Surface**: Intentionally built without authentication or remote network exposure.

### Optimistic Concurrency Control
- `PATCH` and `DELETE` requests require an `If-Match` header containing the quoted `ETag` returned by a previous `GET` request.
- Prevents lost updates when multiple web clients or scripts modify tasks concurrently.
- Request payload sizes are capped at 64 KiB.
