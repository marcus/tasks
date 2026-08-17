# Demo store and screenshot refresh

This directory is fake GTD data plus the recipes that turn it into the
README screenshots. It is not a second task list and it is never the
configured data directory.

`make screenshots` (or `scripts/update-screenshots.sh`) builds a throwaway
store in a temp directory, seeds it through the just-built `tasks` CLI,
points `TASKS_DIR` / `XDG_*` / `HOME` at that sandbox, pins the clock, and
captures the TUI and CLI. The real config under `~/.config/tasks` and the
live `tasks.jsonl` are not read or written.

Override the pinned instant with `TASKS_SCREENSHOT_NOW` if you need a
different “today” than the default demo Thursday.

```sh
make screenshots
TASKS_SCREENSHOT_NOW=2026-11-03T12:00:00-08:00 make screenshots
```

Betamax runs on its own tmux socket (`-L betamax`), not the machine’s
default tmux server.

The capture wrappers force `TERM=xterm-256color` and `COLORTERM=truecolor`
and unset `NO_COLOR`. Agent and CI shells often run as `TERM=dumb` with
`NO_COLOR=1`; without that profile the TUI downconverts to attributes
only and the README image comes out monochrome.
