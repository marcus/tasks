# Multi-link support: formal links + body parse + TUI picker

**Status:** draft plan, ready for review  
**Surfaces:** store schema, `internal/links` + `taskquery`, CLI (`links` / `open` / mutate), HTTP API, TUI (`o` + modal)

## Problem

Today every openable link is **derived** from title + body text (`internal/links.Extract`). That works for prose and org links, but:

1. There is no first-class place to attach **N structured links** with stable order and optional descriptions.
2. TUI `o` always opens the **first** link and only flashes `(1 of N)` when more exist — the CLI already refuses ambiguity and asks the user to pick.
3. Labels/descriptions only exist when the body uses org-style `[[url][label]]` or a config shorthand; a pasted bare URL has nothing human-readable in the picker.

`delegation.work_ref` remains a separate single reference for “where the work lives” under delegation, and stays **out** of the openable link set (current deliberate rule in `details.go`).

## Goal

- Tasks may carry **any number of formal links** (structured, ordered, optional label/description).
- **Body (and title) parsing continues**; formal + derived form one openable set.
- CLI/API remain the source of truth with parity; TUI is a client.
- TUI open-link (`o`): **0** → flash; **1** → open immediately; **2+** → modal to choose (keyboard + mouse), showing descriptions when present.

## Current facts (constraints)

| Area | Today |
|------|--------|
| Storage | No task-level links field. Body is free text; notes/links live there. Schema v2. |
| Extraction | `taskquery.Queries.Links` = title + body via `links.Extract` (org, bare URL, shorthands). Dedup by URL; labelled spelling wins; order = first occurrence. |
| CLI `open` | 1 link → open; many → list numbered + exit 1 unless `n` / `--system`. `--json` returns `ambiguous` + candidates. |
| CLI `links` | Lists derived links only. |
| API | Read-only derived `links: [{system, url, label}]` on task resources. |
| TUI `o` | Always `found[0]`. Palette gated by `link_action_available?`. |
| Overlays | `ChoicePicker` already supports single-select, search, ↑↓/enter/esc, mouse `Hit`, stable width. Action/context palettes wrap it. |
| Forward compat | Unknown keys warn in `check`, are preserved on rewrite. New optional field does **not** require schema version bump if older writers keep unknowns. |

## Design

### 1. Formal link storage (schema v2 additive field)

Add optional task field **`links`**: an ordered JSON array of objects.

```json
"links": [
  {"url": "https://github.com/acme/app/pull/412", "label": "PR #412"},
  {"url": "https://acme.atlassian.net/browse/OPS-1234", "label": "OPS-1234"}
]
```

**Shape of one formal entry**

| Key | Required | Rules |
|-----|----------|--------|
| `url` | yes | Non-empty string; single line; no control chars; after optional shorthand expansion must be `http://` or `https://` with a host. Max length e.g. **2000**. |
| `label` | no | Optional description shown in listings/picker. Single line; no control chars; max e.g. **200**. Omitted when empty (not `""`). |

Canonical object key order: `url`, `label` (same style as nested temporal/delegation orders in `record`).

**Record key order** (add before `body`):

```
… closed archived links body updated
```

Rationale: formal links sit next to body notes; lifecycle stamps stay before notes.

**Not allowed on sections** (same family as `tags` / `state`).

**No schema version bump.** Older binaries warn `unknown key "links"` and preserve the array; this build treats it as known. Document in conventions + CHANGELOG.

**Caps (safety, still “arbitrary” for real use)**

- Max **50** formal links per task (refuse write above that; check error).
- No hard requirement that formal URLs be unique in storage; the **openable union** dedupes (below). Prefer refusing duplicate formal URLs on write so the list stays clean.

**Shorthands on write:** if the user passes `jira:OPS-1234` and `link.jira` is configured, expand to the full URL at write time and store the expanded URL. Label defaults to the raw token when none given. Stored form is always a real URL so open works offline of config changes.

### 2. Openable link union (single read path)

Extend `links.Link` (or a thin wrapper used only at the query boundary) with a **source** for consumers that care:

```go
type Source string // "formal" | "title" | "body"
```

`Queries.Links(item)` becomes:

1. **Formal links** in stored order → classify system via existing `Classify`; use formal `label` when set.
2. **Title + body** extraction as today.
3. **Dedupe by URL**: first win keeps position and spelling. Formal always listed before derived of the same URL (formal is “first”). If a formal entry has no label and a later body occurrence has one, **upgrade the label in place** (same rule as today’s labelled-over-bare upgrade).

Everything that opens or lists links (`show`, `links`, `open`, API `links`, TUI details, `link_action_available?`) calls this one function. No surface reimplements merge order.

Optional JSON member on list/open payloads: `"source": "formal"|"title"|"body"` (additive; agents can ignore it).

### 3. Mutations (agent-first)

Formal links are owned data; body-derived links remain implicit and are never written through this field.

**Store**

- New patch field `FieldLinks` (full replace of the formal array), plus a delta helper if useful for CLI sugar (`FieldLinkDelta`: add/remove by URL or index).
- Empty array → omit key (same omission rule as empty `tags` / `body`).
- Baselines: stable serialization of the formal array for expected-revision checks.

**CLI** (thin over application)

| Command | Behavior |
|---------|----------|
| `tasks links [<ref>]` | Unchanged listing role; shows **union**; formal first. Human rows can mark formal vs body lightly (e.g. dim `formal` / nothing for body). |
| `tasks link add <ref> <url> [--label TEXT]` | Append formal link (or refuse duplicate URL). |
| `tasks link rm <ref> <n\|url>` | Remove formal link by 1-based index **within formal list only**, or by URL. Removing does not edit body text. |
| `tasks link set <ref> <n> --label TEXT` | Optional polish: rename description. |
| `tasks open <ref> [n]` | Unchanged contract; numbering follows **union** order. |

Keep `tasks links` as the list verb; use singular `link` for mutations (mirrors `tag` vs listing). Wire help registry, cli-spec, skill, OpenAPI together per AGENTS.md.

**HTTP**

- Task PATCH (or existing field-patch path): accept `links` as the formal array only.
- Response `links` remains the **union** (derived view). If a client needs formal-only for edit forms, either:
  - add read-only `formal_links` on the resource, **or**
  - filter response links where `source == formal`.

Recommendation: expose **`formal_links`** on the task resource as the writable projection (stored array), keep **`links`** as the openable union. That avoids write/read confusion (“PATCH links replaced my body-derived links”).

### 4. TUI: open-link modal

**Behavior of `OpenLink` (`o` / palette `open_link`)**

| Count | Action |
|------|--------|
| 0 | Flash `no links on this task` (unchanged). |
| 1 | Open immediately (unchanged success path). |
| 2+ | Enter link-picker overlay; do not open until accepted. |

**UI building block:** reuse `ChoicePicker` in `SelectSingle` mode (not a new modal primitive). Wrap as `LinkPicker` analogous to `ActionPalette` / `ContextPalette`:

- Mode: e.g. `ModeLinkPicker` (or reuse palette mode with a distinct overlay kind — prefer a **named mode** so keys/help/status are clear).
- Title: `open link`.
- Options (one per union link):
  - **Primary line:** label/description if present, else URL.
  - **Secondary (muted):** `system` + URL when a label is shown; system alone when URL is the primary.
  - SearchText: url, label, system.
  - Metadata: the `links.Link` (and URL) to open.
- Keys: esc cancel; enter open current; ↑↓ / Ctrl-n/p move; typing filters (picker already does this).
- Mouse: existing `Hit(rowOffset)` → accept that row.
- Optional nicety (same PR if cheap): digit keys `1`–`9` accept that result index when query is empty.

On accept: call opener; flash `opened <system>: <url>`; return to list. On cancel: return to list, no flash noise.

**Detail panel** (`details.go`): update hint from ` (o opens the first)` to something accurate:

- 1 link: ` (o opens)`
- N links: ` (o to choose)`

Show formal labels in the detail list when present (today labels are not rendered in the detail panel rows — only system + URL). Prefer:

```
  github  PR #412
          https://github.com/...
```

or one line: `github  PR #412  https://…` with URL muted when label exists.

**Do not** put `work_ref` into the openable set in this plan (preserves “one keystroke, one meaning”). A later idea can add “open work ref” as a separate action.

### 5. What stays the same

- Body org/bare/shorthand extraction and classification registry.
- `TASKS_OPENER` / platform launcher contract.
- CLI ambiguous multi-link behavior for non-interactive callers (still exit 1 + list / JSON `ambiguous`).
- Schema version 2; journal/atomic write path; DFS/id rules.

## Implementation packets

Ship as reviewable slices so store contracts land before UI sugar.

### Packet A — Data model + union (core)

1. `record.KeyOrder` + nested order for link objects; emit/omit empty.
2. `check` validation: array of objects, url/label rules, section forbidden, max count, duplicate formal URL policy.
3. Store coerce on `Item` (or read formal array only via record when building Links — either is fine if one path).
4. `Queries.Links` merge formal + extract; tests for order, dedupe, label upgrade, formal-only, body-only.
5. Update `links` package docs; optionally add `Source` on `Link`.
6. Fixtures under `testdata/fixtures/valid` (+ malformed for check).

### Packet B — Mutate CLI + application + API write

1. Application/store `FieldLinks` (replace) and CLI `link add` / `link rm`.
2. OpenAPI: `formal_links` on task; PATCH; document `links` as union + optional `source`.
3. `cli-spec.md`, conventions Links section, agent skill if link mutation is agent-facing.
4. Adapter tests: patch, check, show/links/open numbering with formal+body.

### Packet C — TUI picker

1. `LinkPicker` + mode + key/mouse routing (mirror context palette wiring in `keys.go` / `mouse.go` / `overlay.go` / `uistate.go`).
2. Rewrite `OpenLink` for 0/1/N.
3. Detail panel labels + hint.
4. Shortcut description: “open task link(s) in browser”.
5. Tests: multi-link opens modal; single still launches; enter opens chosen; esc cancels; mouse hit; no browser without opener; regression on first-link-only fixture becomes multi-link fixture.

### Packet D — Polish (same release or follow-on)

- Digit quick-pick in picker.
- `link set --label`.
- Capture sugar later (`capture --link URL`) — already noted in `docs/ideas.md`; out of scope unless cheap.
- TUI task-edit form field for formal links (not required for open path; agents use CLI/API).

## Testing plan

| Layer | Coverage |
|-------|----------|
| `internal/record` | Emit order; omit empty; nested key order |
| `internal/check` | Valid/invalid formal links; section rejection; max |
| `internal/taskquery` | Union order; dedupe; label; formal before body |
| `internal/store` + application | Patch replace; empty omit; baseline conflict |
| CLI | `link add/rm`, `links` listing, `open n` after formal prepend |
| API | formal_links write; links union read; OpenAPI examples |
| TUI | OpenLink matrix; picker keys/mouse; flash messages |

Gates: `go test ./...`, `go test -race ./...`, `go vet`, `gofmt -l`, build all three commands. Independent review before done (AGENTS.md).

## Risks and decisions

| Topic | Recommendation |
|-------|----------------|
| Name clash: stored vs derived `links` | Store as `links`; API exposes **`formal_links`** (writable) and **`links`** (union, openable). CLI store field is still `links`. |
| Include `work_ref` in open set? | **No** for this plan. |
| Body URLs that duplicate formal | Dedup; formal keeps slot; body label can upgrade empty formal label. |
| Edit formal links in TUI form | Defer; open path does not need it. |
| Schema version | Stay on **v2**; additive field. |
| Max links | 50 formal; body-derived unlimited as today. |

## Out of scope

- Changing body parser grammar (org / bare / shorthand already good).
- Multi-select open (open several at once).
- Embedding previews or fetching remote titles.
- Making `work_ref` multi-valued.
- Capture `--link` sugar (ideas backlog).

## Success criteria

1. A task can store many formal links with labels via CLI/API; file remains valid schema-v2 JSONL.
2. `tasks links` / `show` / API `links` / TUI details show formal + body links in one ordered union.
3. `tasks open <ref>` numbering matches that union; agents can pick with `n` or JSON `ambiguous.links`.
4. TUI `o` on a multi-link task opens a searchable, keyboard- and mouse-navigable picker that shows descriptions; choosing opens that URL only.
5. Single-link and zero-link behavior remain fast and familiar.

## Suggested doc touchpoints

- `docs/conventions.md` — Links section (formal + body forms).
- `docs/cli-spec.md` — `link add/rm`, open/links notes, config unchanged.
- `docs/api/openapi.yaml` — `formal_links`, union `links.source`.
- `docs/ideas.md` — mark multi-link / formal links shipped when done; leave capture sugar.
- Optional ADR only if we want a permanent record of formal_links vs links naming; otherwise conventions + cli-spec are enough.

## Implementation order

```
A (model + union) → B (mutate + API) → C (TUI picker) → D (polish)
```

A unblocks correct read behavior everywhere; B makes formal links usable by agents; C is the human multi-link fix this request centers on; D is optional follow-on.
