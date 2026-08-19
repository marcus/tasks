package main

// `tasks help` is REGISTRY-DRIVEN, not a hand-written page.
//
// The reference text below is prose a human reads; the table under it is the
// same dispatch data tasks resolves aliases through, and `help --json`
// emits it verbatim. That is the point: an agent scripting this CLI must be
// able to ask "what commands exist, and which of them give me a machine-
// readable result?" and get a true answer without scraping the help page.
//
// `json: false` is an explicit opt-out carrying a stated reason, and a
// schema-gate exemption carries one too. A command cannot join the set without
// someone deciding, in this file, what its structured result is.

// helpText is tasks HELP, byte for byte. It is stderr/stdout contract:
// `tasks` with no arguments and `tasks <typo>` both print it.
const helpText = "tasks — a plain-text GTD CLI over tasks.jsonl. Every command has a short alias." + "\n" +
	"" + "\n" +
	"Read:" + "\n" +
	"  agenda    a              dated items, soonest first" + "\n" +
	"  next      n              NEXT actions grouped by context" + "\n" +
	"  quadrants q              Covey 2x2: priority (A/B) + near deadline, or tags" + "\n" +
	"  inbox     i              unprocessed INBOX items" + "\n" +
	"  projects  pj             projects & areas rolled up over their open tasks" + "\n" +
	"                           (open/NEXT counts, soonest date, stuck flag)" + "\n" +
	"  list      l [filters]    tasks by state. Filters: @context /text +tag -A|-B|-C" + "\n" +
	"                           Scope: --open/-o (default) --proposed --done/-d" + "\n" +
	"                           --archived/-x --all/-a --rejected (choose one)" + "\n" +
	"                           --rejected lists proposals declined in the last 30" + "\n" +
	"                           days, newest first, live + archived (see unreject)" + "\n" +
	"                           --deferred/-D all unavailable · --unavailable canonical spelling" + "\n" +
	"                           --someday/--on-hold own indefinite holds · --recurring/-R repeating" + "\n" +
	"                           --delegated any delegation · --agent-ready claimable" + "\n" +
	"                           agent work, ranked by priority then date" + "\n" +
	"                           --body/-b widens /text search into task notes" + "\n" +
	"  show      s <ref>        one task in full (headline + notes + links)" + "\n" +
	"  links     urls [<ref>]   links in task notes, by system (slack, jira, …)" + "\n" +
	"                           --system <name> filters · --all widens the listing" + "\n" +
	"                           to done + archived (<ref> stays live-file) · --json" + "\n" +
	"  open      o <ref> [n]    open a task's link in the browser (n or --system" + "\n" +
	"                           picks among several; --print shows instead;" + "\n" +
	"                           --json reports the link it acted on)" + "\n" +
	"  id           <ref>       print a task's stable id (assigns one if absent)" + "\n" +
	"  check     k              validate tasks.jsonl structure" + "\n" +
	"  repair    fix            fix what check refuses, in one pass, where no" + "\n" +
	"                           other command can (id-less records, unknown keys" + "\n" +
	"                           inside scheduled_time/deadline_time)." + "\n" +
	"                           --dry-run reports without writing; --json" + "\n" +
	"" + "\n" +
	"Capture:" + "\n" +
	"  capture   c \"text\"       new item. Flags: --due --scheduled --priority" + "\n" +
	"                           --tag --context --no-host-context --state" + "\n" +
	"                           --project --under --recur --lead --note" + "\n" +
	"                           --link URL [--label TEXT] (repeatable; adds formal" + "\n" +
	"                           links in the same write/undo step as the task, and" + "\n" +
	"                           a title ending in a bare URL lifts it into one)" + "\n" +
	"                           (--under <ref> nests it below a task; caps at max_depth)" + "\n" +
	"                           --recur with no date starts repeating today; adding" + "\n" +
	"                           --lead instead anchors on the schedule's FIRST" + "\n" +
	"                           occurrence, so the window opens before it rather" + "\n" +
	"                           than in the past" + "\n" +
	"  propose     \"text\"       inert PROPOSED item for owner review. Capture flags" + "\n" +
	"                           except --state/--recur; repeat --note for rationale" + "\n" +
	"" + "\n" +
	"Update (take <ref>; support --dry-run/--json/--include-done):" + "\n" +
	"  approve      <ref>        accept PROPOSED into INBOX; --done also completes" + "\n" +
	"                           it in the same write/undo step (work already done)" + "\n" +
	"  reject       <ref> [--note] decline PROPOSED into CANCELLED; repeat --note" + "\n" +
	"                           for withdrawal rationale (visible in `show`)" + "\n" +
	"  unreject     <ref>       restore a rejected proposal to PROPOSED in place," + "\n" +
	"                           same id, title, notes and links (see list --rejected)" + "\n" +
	"  done      d <ref>        mark DONE (cascades to open subtasks) — or roll a" + "\n" +
	"                           recurring task forward (aka complete, close)" + "\n" +
	"  cancel      <ref> [--note] mark CANCELLED (aka drop); repeat --note for reason" + "\n" +
	"  state     mv <ref> <STATE>       any state transition" + "\n" +
	"  due          <ref> <date/time>   set/replace DEADLINE   (aka deadline, reschedule)" + "\n" +
	"  schedule     <ref> <date/time>   set/replace Available from (SCHEDULED)" + "\n" +
	"  undate       <ref> [--kind deadline|scheduled]   remove date stamp(s)" + "\n" +
	"  priority pri <ref> <A|B|C|none>  set/clear priority (incl. PROPOSED)" + "\n" +
	"  retitle rename <ref> \"title\"     replace title (incl. PROPOSED)" + "\n" +
	"  tag          <ref> +t -t @ctx -@ctx   edit tags/contexts (incl. PROPOSED)" + "\n" +
	"  link add     <ref> <url> [--label TEXT] append a formal link" + "\n" +
	"  link rm      <ref> <n|url>             remove a formal link (n is formal-only)" + "\n" +
	"  link set     <ref> <n> --label TEXT     rename a formal link (n is formal-only)" + "\n" +
	"  note         <ref> \"text\"        append a body line (incl. PROPOSED)" + "\n" +
	"  move         <ref> \"Section\"     relocate the subtree under a heading" + "\n" +
	"                                   (top-level or a nested project section)" + "\n" +
	"               <ref> --under <ref> nest the subtree below another task (≤ max_depth)" + "\n" +
	"               <ref> --top         unnest the subtree back to the section level" + "\n" +
	"               <ref> [\"Section\"|--under <ref>] --before <ref>" + "\n" +
	"                                   place the subtree before a stable sibling" + "\n" +
	"  recur repeat <ref> <schedule>    repeat on done: weekly · 2w · .+1m · off" + "\n" +
	"                                   calendar: every mon,wed · m:15 · 2nd tuesday ·" + "\n" +
	"                                   last day of the month · every july 4" + "\n" +
	"                                   --from schedule|completion (intervals only) ·" + "\n" +
	"                                   --on <date> seeds a stamp" + "\n" +
	"               <ref>               read-only: the schedule + its next dates" + "\n" +
	"                                   (--count N, default 5 · --json)" + "\n" +
	"               --explain \"<sched>\" parse/preview any schedule, no task needed" + "\n" +
	"  lead           <ref> <span|off>   hide until a span before its date: 3w · 2d ·" + "\n" +
	"                                   1m · 5h · \"a week\" · off (anchor = deadline" + "\n" +
	"                                   if it has one, else Available from)" + "\n" +
	"                 <ref>              read-only: the window and when it opens" + "\n" +
	"  defer   snooze <ref> [date/time] defer until exact value; omitted means On Hold" + "\n" +
	"  someday        <ref>             put on indefinite hold (Someday/Maybe)" + "\n" +
	"  activate       <ref>             make available now (undefer, resume)" + "\n" +
	"  delete         <ref>             hard-delete a task (no archive). Needs --cascade" + "\n" +
	"                                   if it has subtasks. Undoable via `undo`;" + "\n" +
	"                                   usually prefer cancel/archive — this is for" + "\n" +
	"                                   true mistakes." + "\n" +
	"" + "\n" +
	"Delegation (hand a task to a person or to the agent pool):" + "\n" +
	"  delegate <ref> refine|research|implement   agent-ready at that authority" + "\n" +
	"  delegate <ref> --to <email> [--keep-state] hand to a person; sets WAITING" + "\n" +
	"  undelegate <ref>                clear the marker (also revokes a claim)" + "\n" +
	"  workref <ref> <url-or-id|off>   record where the work lives (one reference)" + "\n" +
	"  claim <ref> --worker <id>       atomic pickup of agent-ready work; --json" + "\n" +
	"                                  re-emits the full task so an agent reads its" + "\n" +
	"                                  authority in one step" + "\n" +
	"  release <ref> --worker <id>     hand a claim back to agent-ready" + "\n" +
	"                                  --note \"blocker\" appends a note in the same" + "\n" +
	"                                  undo step · --force is the owner override" + "\n" +
	"  list --delegated · list --agent-ready    the two delegation read scopes" + "\n" +
	"  --worker defaults from TASKS_WORKER_ID; the flag always wins." + "\n" +
	"  A lost claim race exits 1 with `conflict: already claimed by <id> at <ts>`." + "\n" +
	"" + "\n" +
	"Projects (a project/area is a rolled-up section):" + "\n" +
	"  projects  pj             list projects & areas (see Read above)" + "\n" +
	"  project create \"title\"   new empty project under the \"Projects\" root" + "\n" +
	"  project show <ref>       one project/area in full" + "\n" +
	"  project complete <ref>   close every open task in the project (aka done)" + "\n" +
	"  project archive <ref>    sweep the project's subtree to archive.jsonl" + "\n" +
	"                           (--force past remaining open tasks)" + "\n" +
	"  project rename <ref> \"title\"   retitle the section" + "\n" +
	"                           (refs: id · L<line> · title substring · --json/--dry-run)" + "\n" +
	"" + "\n" +
	"Lifecycle:" + "\n" +
	"  archive   x              sweep DONE/CANCELLED to archive.jsonl" + "\n" +
	"                           (--json: {roots, records, moved_ids})" + "\n" +
	"  undo                     revert the last mutation (shared with the TUI)" + "\n" +
	"  redo                     replay the last undone mutation" + "\n" +
	"                           (both --json: {action, label})" + "\n" +
	"  config                   show resolved file paths and their sources" + "\n" +
	"  install-merge-driver [DATA_REPO]   configure Git's tasksjsonl driver" + "\n" +
	"  version                  print build version (--json available)" + "\n" +
	"  -p \"...\"                 hand a request to an LLM agent" + "\n" +
	"                           (-p [--provider N] [--model N] \"...\")" + "\n" +
	"  help      -h             this message (--json: the command registry —" + "\n" +
	"                           every command, its aliases, and its --json answer)" + "\n" +
	"" + "\n" +
	"Files: configure TASKS_DIR (or TASKS_FILE/TASKS_ARCHIVE) env vars, or" + "\n" +
	"~/.config/tasks/config (dir = / file = / archive = lines). With no configured" + "\n" +
	"location, tasks refuses to read or write." + "\n" +
	"Quadrants urgency window: urgent_days = N in the config file, or TASKS_URGENT_DAYS" + "\n" +
	"env (default 3) — a DEADLINE within N days counts as urgent." + "\n" +
	"TUI colors: theme = default|mono|dracula|nord|catppuccin-mocha|... and" + "\n" +
	"color.<slot> = <spec> in the config file (or TASKS_THEME env; NO_COLOR" + "\n" +
	"honored) — see docs/cli-spec.md (TUI colors)." + "\n" +
	"Link shorthands: link.jira = https://acme.atlassian.net/browse/%s in the config" + "\n" +
	"lets notes say jira:OPS-1234; system.gitlab = gitlab.acme.io names custom hosts." + "\n" +
	"Host contexts: host_context.my-mac.local = @home adds that context to new tasks." + "\n" +
	"" + "\n" +
	"Dates: 2026-07-15 · fri · tomorrow · two weeks · in two weeks · 2d · 2w · 2m · 2y" + "\n" +
	"       two minutes · in two minutes · two minutes from now (timed; minute precision)" + "\n" +
	"Timed values: --timezone Europe/London · --floating · --fold earlier|later" + "\n" +
	"Refs:  a case-insensitive title substring, an exact id, or L<line>." + "\n" +
	"JSON:  every command takes --json except -p and merge-driver; `tasks help" + "\n" +
	"       --json` lists them. On failure branch on the exit code — stdout is" + "\n" +
	"       often empty; some commands add an {\"error\",\"action\",\"message\"} object." + "\n" +
	"Full spec: docs/cli-spec.md" + "\n"

// helpCommand is one row of the dispatch registry.
type helpCommand struct {
	name       string
	aliases    []string
	json       bool
	jsonReason string
	gate       bool
	gateReason string
}

// helpCommands is Tasks::CliCommands::ALL, in declaration order — which is the
// order `help --json` publishes and the order the reference text groups by.
var helpCommands = []helpCommand{
	{name: "agenda", aliases: []string{"a"}, json: true, gate: true},
	{name: "next", aliases: []string{"n"}, json: true, gate: true},
	{name: "quadrants", aliases: []string{"q"}, json: true, gate: true},
	{name: "inbox", aliases: []string{"i"}, json: true, gate: true},
	{name: "projects", aliases: []string{"pj"}, json: true, gate: true},
	{name: "list", aliases: []string{"l"}, json: true, gate: true},
	{name: "show", aliases: []string{"s"}, json: true, gate: true},
	{name: "links", aliases: []string{"urls"}, json: true, gate: true},
	{name: "open", aliases: []string{"o"}, json: true, gate: true},
	{name: "id", json: true, gate: true},
	{name: "check", aliases: []string{"k"}, json: true, gateReason: "It is the diagnostic the refusal sends you to. A `check` that refused an unsupported store would close the loop it exists to open, leaving no command able to name the version."},
	{name: "project create", aliases: []string{"project new"}, json: true, gate: true},
	{name: "project show", json: true, gate: true},
	{name: "project complete", aliases: []string{"project done"}, json: true, gate: true},
	{name: "project archive", json: true, gate: true},
	{name: "project rename", json: true, gate: true},
	{name: "capture", aliases: []string{"add", "c"}, json: true, gate: true},
	{name: "propose", json: true, gate: true},
	{name: "approve", json: true, gate: true},
	{name: "reject", json: true, gate: true},
	{name: "unreject", json: true, gate: true},
	{name: "delegate", json: true, gate: true},
	{name: "undelegate", json: true, gate: true},
	{name: "workref", aliases: []string{"work-ref"}, json: true, gate: true},
	{name: "claim", json: true, gate: true},
	{name: "release", json: true, gate: true},
	{name: "done", aliases: []string{"complete", "close", "d"}, json: true, gate: true},
	{name: "due", aliases: []string{"deadline", "reschedule"}, json: true, gate: true},
	{name: "schedule", json: true, gate: true},
	{name: "undate", json: true, gate: true},
	{name: "state", aliases: []string{"mv"}, json: true, gate: true},
	{name: "cancel", aliases: []string{"drop"}, json: true, gate: true},
	{name: "priority", aliases: []string{"pri"}, json: true, gate: true},
	{name: "retitle", aliases: []string{"rename"}, json: true, gate: true},
	{name: "tag", json: true, gate: true},
	{name: "link add", json: true, gate: true},
	{name: "link rm", json: true, gate: true},
	{name: "link set", json: true, gate: true},
	{name: "note", json: true, gate: true},
	{name: "move", json: true, gate: true},
	{name: "delete", json: true, gate: true},
	{name: "recur", aliases: []string{"repeat", "every"}, json: true, gate: true},
	{name: "lead", aliases: []string{"leadtime", "lead-time"}, json: true, gate: true},
	{name: "defer", aliases: []string{"snooze"}, json: true, gate: true},
	{name: "someday", json: true, gate: true},
	{name: "activate", aliases: []string{"undefer", "resume"}, json: true, gate: true},
	{name: "archive", aliases: []string{"x"}, json: true, gate: true},
	{name: "repair", aliases: []string{"fix"}, json: true, gate: true},
	{name: "undo", json: true, gate: true},
	{name: "redo", json: true, gate: true},
	{name: "config", json: true, gateReason: "It reports where the store IS, never what it contains. Finding the file is a precondition for fixing a version skew, so it must answer for a store no other command will touch."},
	{name: "install-merge-driver", json: true, gateReason: "It configures Git from explicit repository metadata and never reads the task store."},
	{name: "version", aliases: []string{"--version"}, json: true, gateReason: "It reports build metadata and never reads the task store."},
	{name: "help", aliases: []string{"-h", "--help"}, json: true, gateReason: "It reads this registry, not the store."},
	{name: "-p", aliases: []string{"--prompt"}, jsonReason: "The result is an LLM harness's free-form transcript, not a value this CLI computes; the mutations it makes are readable through the commands that do emit JSON.", gate: true},
	{name: "merge-driver", jsonReason: "Git plumbing. Git supplies the three merge-stage paths and reads the merged file and the exit code; stdout is not a result surface.", gateReason: "Git hands it three merge-stage paths and never the configured store, so there is no store to gate. JsonlMerge applies the version rule to each of those three inputs itself."},
}

// help prints the reference, or the registry itself under --json.
//
// Extra positionals are IGNORED rather than refused: `tasks help list` and
// `tasks help --anything` still print the reference. Help is the command you
// reach for when you are already unsure, so it must not be the one that
// rejects your guess.
func helpSurface(_ *surfaceContext, args []string) int {
	if !contains(args, "--json") {
		out(helpText)
		return 0
	}
	w := jsonWriter()
	w.BeginObject()
	w.Key("commands")
	w.BeginArray()
	for _, command := range helpCommands {
		w.BeginObject()
		w.KeyStr("name", command.name)
		w.Key("aliases")
		w.Strings(command.aliases)
		w.KeyBool("json", command.json)
		w.KeyBool("schema_gate", command.gate)
		if !command.json {
			w.KeyStr("json_reason", command.jsonReason)
		}
		if !command.gate {
			w.KeyStr("schema_gate_reason", command.gateReason)
		}
		w.EndObject()
	}
	w.EndArray()
	w.EndObject()
	if err := w.Err(); err != nil {
		return abort(err.Error())
	}
	out(w.String())
	return 0
}

func init() {
	register("help", helpSurface)
}
