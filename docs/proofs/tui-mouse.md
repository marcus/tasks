# TUI mouse support — manual terminal proof

Checklist for `docs/plans/active/tui-mouse-support.md`. Betamax drives
keystrokes, not a pointer, so these gestures are verified by hand.

Date: ________  Tester: ________

## Terminals

For each terminal: click a task, click it again for details, click a header
tab, wheel over the list / detail panel / help modal / agent response pane.

| Terminal     | click | wheel regions | notes |
|--------------|-------|---------------|-------|
| iTerm2       |       |               |       |
| Terminal.app |       |               |       |
| kitty        |       |               |       |
| ghostty      |       |               |       |
| tmux         |       |               |       |

## Text selection

- [ ] Bypass modifier still selects text (shift, or option in iTerm2)
- [ ] `mouse = off` in `~/.config/tasks/config` (or `TASKS_MOUSE=off`) restores
      unmodified drag-select

## Terminal restore

After quit, `ctrl-c`, and a forced crash (`kill -9` of a nested test is not
required — raise from a binding or quit normally plus interrupt):

- [ ] `cat -v` then click shows no `\e[<` reports (tracking off)

## Resize

- [ ] Resize mid-scroll, then click — hits the intended row/tab
- [ ] Narrow terminal with panel suppressed: wheel goes to the list, not a
      zero-width panel

## Known limitation

Wheel over the list moves the selection (no detached list viewport yet).
