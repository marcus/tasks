# valid/link-corpus

One task per link construct `Tasks::Links` recognizes, rejects, or rewrites.
The corpus previously held exactly one link (a bare URL in a `full-field-matrix`
body), so every trimming, dedupe, and classification rule was unproven. This
fixture is the specification for `links-read`: the recorded output below is what
a port must reproduce, character for character.

Every URL is on `example.invalid` or `example.com`. No real host appears — see
"What this fixture cannot cover" for the consequence.

## What it exercises

| Task | Construct |
|---|---|
| `11c00002` | org labelled link `[[url][label]]` |
| `11c00003` | org link with no label `[[url]]` |
| `11c00004` | org link whose url and label carry padding (both are `strip`ped) |
| `11c00005` | a bare URL in prose |
| `11c00006` | trailing punctuation: `.` `,` `…` `;` `!?` `»` |
| `11c00007` | an unbalanced `)` handed back to the sentence, next to a balanced `(…)` kept inside the path |
| `11c00008` | org verbatim `=url=` and code `~url~` wrappers |
| `11c00009` | the same url twice, **labelled occurrence first** |
| `11c00010` | the same url twice, **bare occurrence first** |
| `11c00011` | a self-hosted `jira.` host classified by the `jira` row |
| `11c00012` | a host matching two rows — `confluence` wins on registry order |
| `11c00013` | host fallback, and `www.` stripped; one `http://` url |
| `11c00014` | an uppercase host — the url is preserved, the system name is downcased |
| `11c00015` | an unparseable url (`:port`) in both org and bare form |
| `11c00016` | scheme-only fragments (`https://,` `https://` `https://.`) — **not links** |
| `11c00017` | org internal links `[[My Heading]]`, `[[id:…]]`, `[[file:…]]` — not reported, but masked so the bare url after them is still found |
| `11c00018` | a non-web org scheme (`ftp:`) — not reported |
| `11c00019` | a link in the **title**, then two more across body lines — extraction order |

## What a correct implementation must do

1. Scan `[title, *body]` joined with `\n`, and return links in **text offset
   order** — the title's links first, then the body's, in line order.
2. Find org links first and blank their whole span before scanning bare URLs, so
   an org link's url is never re-found bare (and a rejected org link — internal,
   `ftp:` — is still blanked).
3. Trim only what belongs to the sentence: trailing punctuation, an *unbalanced*
   closing paren, and a verbatim/code marker only when the character just before
   the match is the same marker.
4. Drop a match that punctuation-trimming reduces to a bare scheme.
5. De-duplicate by exact url string, **preferring the labelled form**: among the
   occurrences of one url the first *labelled* one survives, and only when none
   is labelled does the first (bare) occurrence stand in. Position decides
   *order*, never *which spelling* — each surviving link is listed at its url's
   first occurrence, whichever spelling that was. Two different labels for the
   same url do not merge; the first labelled occurrence wins outright.
6. Classify by host; fall back to the downcased host minus `www.`; fall back to
   `"link"` when the url will not parse.

## Recorded Ruby outcome

Environment as in the corpus README (`TASKS_TIMEZONE=UTC`, `TASKS_DEVICE=fixture`,
empty `XDG_CONFIG_HOME`, `TASKS_DIR` at a copy).

```console
$ tasks check
ok — 18 tasks parsed, no structural errors
exit 0
```

```console
$ tasks links
Org labelled link
  example.invalid     https://example.invalid/docs/guide  (Deployment guide)
Org link with no label
  example.invalid     https://example.invalid/docs/api
Org link with padded url and label
  example.invalid     https://example.invalid/docs/pad  (Padded label)
Bare URL in prose
  example.invalid     https://example.invalid/notes/plain
Trailing punctuation is handed back to the sentence
  example.invalid     https://example.invalid/notes/stop
  example.invalid     https://example.invalid/notes/comma
  example.invalid     https://example.invalid/notes/dots
  example.invalid     https://example.invalid/notes/semi
  example.invalid     https://example.invalid/notes/bang
  example.invalid     https://example.invalid/notes/quote
Unbalanced closing paren is trimmed, balanced parens are kept
  example.invalid     https://example.invalid/notes/paren
  example.invalid     https://example.invalid/wiki/Topic_(section)
Org verbatim and code markers are peeled off
  example.invalid     https://example.invalid/notes/verbatim
  example.invalid     https://example.invalid/notes/tilde
Duplicate url, labelled occurrence first
  example.invalid     https://example.invalid/dupe/a  (Canonical label)
Duplicate url, bare occurrence first
  example.invalid     https://example.invalid/dupe/b  (Later label)
Self-hosted jira host classifies as jira
  jira                https://jira.example.invalid/browse/OPS-1234  (OPS-1234)
Row order resolves a host matching two systems
  confluence          https://jira.confluence.example.invalid/wiki/Runbook
Unknown host falls back to the host, minus www.
  sub.example.invalid https://sub.example.invalid/b
  example.com         http://www.example.com/a
Host fallback is downcased
  example.com         https://WWW.EXAMPLE.COM/Case
An unparseable url classifies as link
  link                https://example.com:port/x  (Bad port)
  link                https://example.com:port/y
Org internal links are not reported but are masked
  example.invalid     https://example.invalid/after/masking
Title link https://example.invalid/title/link comes first
  example.invalid     https://example.invalid/title/link
  example.invalid     https://example.invalid/body/line-two
  example.invalid     https://example.invalid/body/line-three
exit 0
```

`11c00016` (scheme-only fragments) and `11c00018` (an `ftp:` org link) produce no
rows at all — they are absent from the listing above, and asked for directly:

```console
$ tasks links 11c00016
No links found.
exit 0

$ tasks links 11c00018
No links found.
exit 0
```

Filtering by system, which reads the same classification:

```console
$ tasks links --system jira
Self-hosted jira host classifies as jira
  jira https://jira.example.invalid/browse/OPS-1234  (OPS-1234)
exit 0
```

`tasks links --json` emits one flat `links` array in the same order, each entry
`{url, label, system, task, id, line, source}` — `label` is `null` for a bare
URL. The first three and the two dedupe entries:

```json
{"url":"https://example.invalid/docs/guide","label":"Deployment guide","system":"example.invalid","task":"Org labelled link","id":"11c00002","line":3,"source":"live"}
{"url":"https://example.invalid/docs/api","label":null,"system":"example.invalid","task":"Org link with no label","id":"11c00003","line":4,"source":"live"}
{"url":"https://example.invalid/docs/pad","label":"Padded label","system":"example.invalid","task":"Org link with padded url and label","id":"11c00004","line":5,"source":"live"}
{"url":"https://example.invalid/dupe/a","label":"Canonical label","system":"example.invalid","task":"Duplicate url, labelled occurrence first","id":"11c00009","line":10,"source":"live"}
{"url":"https://example.invalid/dupe/b","label":"Later label","system":"example.invalid","task":"Duplicate url, bare occurrence first","id":"11c00010","line":11,"source":"live"}
```

## Findings

**1. Dedupe prefers the labelled form, independently of position.** `11c00009`
and `11c00010` are the same construct in the two orders, and both keep their
label. This was not always so: when this corpus was first recorded, `Links.extract`
sorted by offset and applied `uniq(&:url)`, so the *earliest* occurrence survived
and `11c00010` lost its label — while both written specs (the `links-read`
behavior sentence and `Links.extract`'s own comment) said the labelled form won.
td-794997 resolved the conflict in favor of the prose: Ruby was changed, and this
recording moved with it (the one `tasks links` row for `11c00010` gained
`(Later label)`). The dedupe now keeps a per-url best spelling in first-occurrence
order, so a port must not implement "keep the earliest". `11c00009` is the
control: it was unchanged by the fix.

**2. An unparseable url is still reported, verbatim.** `https://example.com:port/x`
raises `URI::InvalidURIError`; `classify` rescues to `"link"` and the url string
is emitted unchanged. Rejection happens only for a match that trimming reduces
to a bare scheme, never for a parse failure.

**3. The url's case is preserved while the system name is downcased.**
`https://WWW.EXAMPLE.COM/Case` lists under `example.com` with the url untouched —
the fallback is lowercased on purpose so the case-insensitive `--system` filter
can rely on it.

**4. Registry order, not specificity, resolves a multi-match host.**
`jira.confluence.example.invalid` matches both the `confluence.` and the `jira.`
row; `confluence` wins because it is listed first.

## What this fixture cannot cover

Two parts of `Links` are not expressible as a store fixture, and are proved by
the Ruby unit oracle instead — a port needs its own unit tests for both:

- **`SYSTEMS` rows keyed to apex domains** (`github.com`, `slack.com`,
  `linear.app`, `notion.so`, `docs.google.com`, `drive.google.com`, `figma.com`,
  `zoom.us`, and the two `atlassian.net` rows including the `/wiki/` path
  discriminator) need those real hosts. The corpus is `.invalid`/`example.com`
  only, by its sanitization rule. The two rows that match on a host *label*
  (`confluence.`, `jira.`) are covered here, as are the host fallback, the
  `www.` strip, the downcasing, row ordering, and the `"link"` rescue —
  i.e. every branch of `classify` except the apex-domain table entries.
  `test/test_tree.rb#test_classify_known_systems_and_fallback` is the oracle for
  those rows.
- **Shorthand expansion** (`jira:OPS-1234`) and **custom `system.<name>` rows**
  read `$XDG_CONFIG_HOME/tasks/config`, which every fixture run points at an
  *empty* directory by contract (corpus README, rule 3). No store can carry
  them. `test/test_links_feature.rb`'s shorthand and `test_custom_system_rows_classify_self_hosted`
  cases are the oracle.

A consequence worth noting: `clean_bare`'s unbalanced-`]` branch is unreachable
from a bare URL, because `BARE_URL` excludes `[`, `]`, `"` and `'` from the match
entirely. It only fires on a shorthand *value* — so it, too, is config-only.
