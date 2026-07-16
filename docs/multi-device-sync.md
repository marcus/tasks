# Set up multi-device Git sync

The `tasks` application and your task data belong in separate repositories.
Clone this application wherever you like, then keep `tasks.jsonl` and
`archive.jsonl` in a private Git repository that you control. You do not need
access to anyone else's data repository.

This guide uses these paths:

- `~/code/tasks` for the application checkout
- `~/tasks` for the private data repository

Change them to match your machine.

## What the merge driver does

Ordinary line-based Git merging is a poor fit for JSONL records: two machines
can change different fields of the same task and still collide on one line.
The bundled merge driver performs a three-way merge by stable task ID. It
combines independent field changes, unions tags, prefers progressed states,
and uses each record's `updated` stamp to settle genuine same-field conflicts.
It validates the result before replacing Git's copy.

The driver does not fetch, commit, or push. You still need a manual sync habit
or automation that follows the safe sequence below.

## Create the private data repository

Do this once on the first machine. Start with the sample file so the JSONL has
the required metadata and canonical structure:

```sh
mkdir -p ~/tasks
cp ~/code/tasks/examples/tasks.jsonl ~/tasks/tasks.jsonl
git -C ~/tasks init -b main
```

Add `.gitattributes` so Git selects the record-aware driver for both data
files. `archive.jsonl` does not need to exist yet; `tasks archive` creates it
when it is first needed.

```gitattributes
tasks.jsonl merge=tasksjsonl
archive.jsonl merge=tasksjsonl
```

Save those lines as `~/tasks/.gitattributes`. Also add a `~/tasks/.gitignore`:

```gitignore
.tasks-merge.log
.tasks.jsonl.lock
.archive.jsonl.lock
```

Point the application at the data directory:

```sh
mkdir -p ~/.config/tasks
printf 'dir = ~/tasks\n' > ~/.config/tasks/config
~/code/tasks/bin/tasks config
~/code/tasks/bin/tasks check --all-files
```

Commit the initial files, create an empty **private** repository with your Git
host, and connect it as `origin`. Replace `YOUR_PRIVATE_REMOTE_URL` with that
repository's SSH or HTTPS URL.

```sh
git -C ~/tasks add .gitattributes .gitignore tasks.jsonl
git -C ~/tasks commit -m "Initialize task data"
git -C ~/tasks remote add origin YOUR_PRIVATE_REMOTE_URL
git -C ~/tasks push -u origin main
```

Task data can contain private notes, dates, and links. Confirm that the remote
is private before the first push.

## Install the driver on each machine

The attributes file is committed with the data, but Git's driver command is
machine-local. Run the installer in every application checkout, including the
first one:

```sh
~/code/tasks/bin/install-merge-driver ~/tasks
```

The installer stores an absolute path to `bin/tasks` in the data repository's
local Git config. Run it again if you move the application checkout.

Give each machine a short, unique device name if their hostname-derived names
would collide:

```sh
export TASKS_DEVICE=home
```

Use a different value, such as `work`, on the other machine. Use only letters
and digits; values are lowercased, and only the first letters-and-digits token
is kept. Put the export in your shell profile, and pass the same environment
variable explicitly to any background service that runs `tasks`. Keep
automatic clock synchronization enabled because timestamps decide true
same-field conflicts.

Verify the installation:

```sh
git -C ~/tasks check-attr merge -- tasks.jsonl archive.jsonl
git -C ~/tasks config --local --get merge.tasksjsonl.driver
TASKS_DIR=~/tasks ~/code/tasks/bin/tasks check --all-files
```

The first command should report `merge: tasksjsonl` for both files. The second
should show the absolute path to this checkout's `bin/tasks` followed by
`merge-driver %O %A %B %P`.

On each additional machine, clone both repositories, write its
`~/.config/tasks/config`, choose its `TASKS_DEVICE`, and run the installer. Do
not copy `.git/config` from another machine; its absolute driver path may be
wrong.

## Sync safely

Pull before starting work:

```sh
(
  set -eu
  git -C ~/tasks fetch origin
  git -C ~/tasks rebase origin/main
)
```

After changing tasks through the CLI or TUI, validate, commit, incorporate any
remote changes, validate the merged pair, and push:

```sh
(
  set -eu
  cd ~/tasks
  TASKS_DIR="$PWD" ~/code/tasks/bin/tasks check --all-files
  git add -A
  git diff --cached --quiet || git commit -m "Update tasks"
  git fetch origin
  git rebase origin/main
  TASKS_DIR="$PWD" ~/code/tasks/bin/tasks check --all-files
  git push origin main
)
```

If the push loses a race with another machine, repeat the fetch, rebase,
validation, and push steps. The subshell exits on the first failing command
without closing your interactive shell. Automation should use this same order
and fail-fast behavior. In particular, it must never push after a failed merge
or failed `check --all-files`.

## Inspect or recover a merge

Each merge-driver run appends a short decision record to
`~/tasks/.tasks-merge.log`. The file stays local and ignored by Git. After a
merge, these commands show the result and validate the full live/archive pair:

```sh
git -C ~/tasks diff origin/main -- tasks.jsonl archive.jsonl
TASKS_DIR=~/tasks ~/code/tasks/bin/tasks check --all-files
tail -n 50 ~/tasks/.tasks-merge.log
```

Malformed JSONL, an invalid merged hierarchy, or the same stable ID appearing
in both live and archive data causes validation to fail. Do not push. If a
rebase is still in progress, abort it with `git -C ~/tasks rebase --abort`,
return the data to a valid state through normal `tasks` commands or a known-good
Git commit, and retry the sync. Avoid hand-editing the JSONL; record ordering,
key ordering, IDs, and metadata are application invariants.
