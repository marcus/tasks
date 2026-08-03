# Generated conformance corpus

`generate` expands the registry-backed CLI surface into runner case lists. The
command inventory, aliases, schema gating, and `--json` support come directly
from `Tasks::CliCommands::ALL`; command profiles add semantic fixture arguments
and meaningful flag combinations. A source scanner reads the actual flag parser
forms in `bin/tasks` and `Tasks::TaskFilter`; generation fails if the registry,
parser flags, and profiles drift. A new command or accepted flag therefore cannot
silently disappear from the corpus.

Generate the default corpus reproducibly:

```sh
porting/corpus/generate --seed 20260802 --out porting/corpus/generated/cases.jsonl
```

The same seed and checkout produce byte-identical output. The generator only
accepts fixture ids below `porting/fixtures/`, and it refuses output paths below
`porting/runners/cases/` so the hand-written lists remain evidence authored for
specific slices.

Run and validate it against the Ruby oracle:

```sh
porting/runners/ruby/run --out porting/corpus/generated/ruby \
  porting/corpus/generated/cases.jsonl
porting/compare/validate porting/corpus/generated/ruby
```

Generated observations are disposable proof output and are ignored by Git.

## Deliberate exclusions

The fixture sweep excludes `malformed/cross-file-duplicate-id`. The Ruby runner's
probe reports its duplicate live/archive id twice in `revisions.resources`, but
the observation contract requires those keys to be unique, so
`porting/compare/validate` rejects the observation before it can become evidence.
The focused hand-written case remains the right place to preserve that known
harness/fixture edge until the observation contract can represent it.
