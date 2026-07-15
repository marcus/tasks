# tasks

A plain-text GTD task system designed to be shared with an AI agent. Your tasks
live in one JSONL file that you own and git-commit; a Ruby CLI and a full-screen
TUI are the writers; and any LLM harness can manage the list through the same
commands you use. The whole thing runs on the Ruby standard library — `git clone`
is the install.

```sh
tasks agenda            # (a) available dated work, soonest first
tasks next              # (n) next actions grouped by context (@computer, @email, …)
tasks quadrants         # (q) Covey Important/Urgent 2x2
tasks inbox             # (i) unprocessed captures
tasks list              # (l) all tasks grouped by state, with filters (see below)
tasks capture "..."     # (c) append a new item to the Inbox
tasks done "..."        # (d) mark a matching open item DONE
tasks links             # links in task notes, by system (slack, jira, …)
tasks open "..."        # (o) open a task's link in the browser
tasks undo              # revert the last mutation (redo mirrors it)
tasks archive           # (x) sweep DONE/CANCELLED items into archive.jsonl
tasks -p "..."          # hand a request to an LLM agent — it acts and reports back
```

Every command has the single-letter alias shown in parentheses (`tasks n`,
`tasks x`, …). `tasks` itself is aliased to `bin/tasks` in `~/.zshrc`.

## The design

### One file, one line per task

Each line of `tasks.jsonl` is a JSON record with a stable id and a fixed key
order, empty fields omitted, so changing one field changes exactly one line.
Every mutation is a reviewable one-line git diff. The tree lives in explicit
`parent` pointers rather than indentation, which means there are no block
boundaries to infer and no whitespace to keep balanced — a whole class of
outline-walking bugs is structurally impossible. The file stays greppable and
diffable, and it's fully separable from the code: keep your data wherever you
like and point the tooling at it.

### The CLI is the API

`bin/tasks` is the only writer, and its spec
([`docs/cli-spec.md`](docs/cli-spec.md)) is written for LLM agents first,
humans second. Every read command takes `--json` and returns a flat, pre-sorted
array. A task reference resolves as a case-insensitive title substring, an
exact 8-hex id, or an `L<line>` file position; an ambiguous reference is an
error that lists the candidates rather than a guess. Command synonyms are
accepted where an agent would plausibly reach for them (`done` / `complete` /
`close`). [`AGENTS.md`](AGENTS.md) is the standing contract that keeps any
agent consistent with the format and the methodology.

### Writes you can trust

Every write goes through an atomic swap: full contents to a sibling temp file,
fsync, rename over the target, fsync the directory. A concurrent reader or a
crash sees the whole old file or the whole new one, never a torn mix. The swap
follows symlinks (so a Dropbox or dotfiles setup survives) and carries the
target's permission bits onto the replacement. After every mutation the store
validates the result and rolls back a bad write; `tasks check` audits the file
if anything ever edits it out-of-band.

### Undo is a journal, not a stack

Mutations persist to a content-addressed journal under
`$XDG_STATE_HOME/tasks/journal/`, keyed by the task file's path. The CLI and
the TUI share one linear history, so `tasks undo` from a cold shell reverts
what the TUI — or an agent — just did, and the TUI's history survives a
restart. Redo mirrors it, and a new edit after an undo drops the unreachable
tail, exactly as you'd expect.

### Layers that peel apart

`Tasks::Store` owns the file: parsing, change detection, mutations. Above it,
a persistence-neutral `Tasks::Application` facade serves immutable read
snapshots and typed views; the CLI and TUI are thin adapters on that seam, and
an HTTP adapter would sit at the same one. Because a snapshot is immutable and
coherent, a renderer holding one can ask for a task's body, links, or tree
position without ever mixing fields from a later reload.

### Agents are pluggable harnesses

The LLM layer defines one protocol: hand a harness a prompt, a system context,
and a working directory, and let it act — read the file, run `bin/tasks`,
report back. The code never parses model output for meaning; it streams the
transcript and reloads when the file changes on disk. The local `claude` CLI
is the default backend, the [Hermes agent](https://hermes-agent.nousresearch.com)
driving a local Ollama model is another, and any harness that fits the
protocol slots in via `~/.config/tasks/config`.

### The methodology lives in the model

GTD and Covey's Important/Urgent matrix aren't conventions you're trusted to
maintain; the tooling enforces them. Dating an `INBOX` item promotes it to
`TODO` (a date means you've processed it). Completing a recurring task rolls
its date forward instead of closing it. `scheduled` is the available-from date:
future work stays out of active views until that day, while `deadline` remains
the separate due date. Someday/Maybe is an indefinite On Hold marker with no
automatic release. The quadrant view computes urgency from your configured
window. See [`docs/conventions.md`](docs/conventions.md) for the full format and
methodology spec.

### No dependencies

Ruby 3.4 stdlib only — the CLI, the TUI, its theme engine, and the embedded
`TermForm` editor component. No gems to install, no bundler, no version
conflicts with whatever else is on your machine.

## Where your tasks live

Your tasks live in a `tasks.jsonl` (plus `archive.jsonl`) that you own — keep
it wherever you like. Point the tooling at it:

```sh
mkdir -p ~/tasks && cp examples/tasks.jsonl ~/tasks/tasks.jsonl   # seed from the sample
mkdir -p ~/.config/tasks
echo "dir = ~/tasks" > ~/.config/tasks/config
tasks config          # shows the resolved paths and where each came from
```

Resolution order (CLI and TUI alike): `TASKS_FILE`/`TASKS_ARCHIVE` env vars,
then `TASKS_DIR`, then the config file (`dir = …`, or per-file `file = …` /
`archive = …`), then the repo root. Env vars make one-off sandboxes easy:
`TASKS_FILE=/tmp/scratch.jsonl tasks capture "test"`.

## Filtering with `list`

```sh
tasks list                       # open items only (default)
tasks list -d                    # done items still in tasks.jsonl
tasks list -x                    # archived items
tasks list -a                    # everything, both files
tasks list @computer -A /denver  # compose: context, priority, text — all at once
tasks list --unavailable         # timed, inherited, and indefinite blockers
tasks list --someday             # own indefinite On Hold tasks only
```

Scope flags: `--open/-o` (default) `--done/-d` `--archived/-x` `--all/-a`.
Filter sigils: `@context`  `/text` (or a bare word)  `+tag`  `-A|-B|-C` (priority).

## Working with an agent

`tasks -p "..."` hands a natural-language request to an autonomous agent with
`AGENTS.md` as context. It acts on your tasks right where you're working,
auto-applies changes, and prints a git diff of the task files plus a one-line
summary of what it did:

```sh
tasks -p "close the Drew review task and push the Denver flight deadline to next Friday"
tasks -p "defer the Fox task four days" # hides it for four days; deadline is unchanged
tasks -p --provider hermes "capture: renew passport"   # a local Ollama-backed harness
```

Because every change lands as a one-line diff in a file you version, reviewing
what an agent did to your list is `git diff`, and reverting it is `tasks undo`.
Pick the default backend and add models in `~/.config/tasks/config`; see
`docs/cli-spec.md` (LLM agent settings).

### Remembered defaults

An agent can keep a small file of durable, opt-in defaults for a task set —
`agent-memory.md`, next to your `tasks.jsonl`, so it commits and clones right
along with your tasks. Ask it to remember something once and later requests
apply it without you repeating yourself:

```sh
tasks -p "water the garden; remember garden tasks use @home"
# captures the task AND writes the rule — both show up in the diff
tasks -p "water the garden"     # a later run tags it @home automatically
```

It's plain Markdown you can read and edit by hand:

```markdown
## Defaults

- Garden-related tasks: add the `@home` context.
```

The current request always wins — "water the community plot, no context" obeys
you and leaves the rule intact — and the agent only touches the file when you
explicitly say "remember", "forget", or "change that rule", never by inferring a
default from your edits. Relocate it with the `TASKS_MEMORY` env var or a
`memory = …` line in the config; `tasks config` shows where it resolves and
whether it exists.

## TUI

`bin/tasks-tui` is a full-screen interactive view over the same file. It
watches `tasks.jsonl` and updates live no matter who wrote it — you, the CLI,
or an agent mid-request. It reopens on whichever view you quit from, with the
same subtrees collapsed. Press `?` inside for the complete keymap; the shape
of it:

```
1-5 / ←→   switch view: Agenda · Next · Quadrants · Inbox · Projects
↑↓ / jk    select a task; an open detail panel follows the selection
h / l      collapse / expand the selected subtree (H / L for all)
return     open the read-only task detail panel; e edits it in place
c d r      complete · reschedule deadline · recur (weekly, 2w, off)
z Z J K    defer (date/someday/now) · show unavailable · lower / raise priority
/          live text filter; enter keeps it, esc clears
u ctrl-r   undo / redo — the same journal the CLI uses
o y p      open task link · yank ref / markdown · paste ref into the agent prompt
x          archive sweep with a preview of the counts before confirming
:          action palette — search every action available in context
tab        focus the agent prompt
```

Task editing is save-on-blur: moving between fields validates and saves,
consecutive saves in one edit session coalesce into a single undo step, and if
an external write changes the field you're editing, your buffer stays copyable
and the save reports a conflict instead of clobbering either side. The editor
is the embedded, stdlib-only `TermForm` component
(`ruby examples/term_form_demo.rb` shows it running with no task code loaded).

The agent prompt runs asynchronously — the UI stays live while requests queue
FIFO, the footer streams the active one, and `A` opens full transcripts.
`M` cycles the backend/model for new requests. Quitting with unfinished agent
work asks first.

Colors are themable: `theme = dracula` (or `nord`, `catppuccin-mocha`,
`gruvbox-dark`, `tokyonight-night`, `solarized-dark`, `mono`, and more) plus
per-slot overrides like `color.accent = magenta` in `~/.config/tasks/config`.
`NO_COLOR` is honored. See `docs/cli-spec.md` (TUI colors) for the slot
vocabulary.

## Development

```sh
ruby test/all.rb
```

The suite is ~1,100 minitest tests across 47 files, stdlib only like
everything else. Design decisions are recorded as ADRs in
[`docs/adr/`](docs/adr), the CLI's behavior contract is
[`docs/cli-spec.md`](docs/cli-spec.md), and the backlog of feature ideas lives
in [`docs/ideas.md`](docs/ideas.md).

## License

[MIT](LICENSE).
