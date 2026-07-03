#!/usr/bin/env ruby
# frozen_string_literal: true

# Local-LLM eval for the `tasks -p` agent path.
#
# For each candidate model we run a spread of natural-language task prompts
# through the REAL CLI (`bin/tasks -p --provider hermes --model <M>`) against a
# fresh sandbox copy of a known gtd.org, then score the *outcome* deterministically
# using the repo's own parser (Tasks::Store) — did the file end up in the expected
# state, and did it stay structurally valid (no corruption)? Each prompt is run
# TRIALS times because agentic runs are stochastic.
#
# Results stream to runs.jsonl (append-only, resumable) and a human-readable
# results-<date>.md is regenerated after every model. No model touches real data:
# each run gets its own tmpdir with TASKS_DIR pointed at it.
#
# Env overrides (for smoke tests): EVAL_MODELS, EVAL_TASKS (comma lists),
# EVAL_TRIALS, EVAL_TIMEOUT.

require "json"
require "date"
require "timeout"
require "tmpdir"
require "fileutils"

REPO = File.expand_path("../..", __dir__)
$LOAD_PATH.unshift File.join(REPO, "lib")
require "tasks/store"
require "tasks/check"

HERE   = __dir__
RUNS   = ENV["EVAL_RUNS"]   || File.join(HERE, "runs.jsonl")
LOG    = ENV["EVAL_LOG"]    || File.join(HERE, "harness.log")
REPORT = ENV["EVAL_REPORT"] || File.join(HERE, "results-#{Date.today}.md")

# Sandboxes live under $HOME, NOT the macOS temp dir (/var/folders/.../T): Hermes'
# file-write safety filter refuses "sensitive system paths" like /var/folders,
# which silently blocked direct gtd.org edits and biased the first run. A normal
# home-dir path also better mirrors where real task files live.
SANDBOX_BASE = File.join(Dir.home, ".cache", "tasks-eval")
FileUtils.mkdir_p(SANDBOX_BASE)

# 4 best candidates (spanning size + Qwen/Gemma families) plus the current
# default gemma4:e4b as a baseline (last). Ordered best-bet-first so that if the
# wall-clock budget is hit, the most promising models are covered: the MoE 35B
# (fast + capable) and the mid 12B lead, the flagship dense 35B and small 4B
# follow, baseline trails.
MODELS = (ENV["EVAL_MODELS"]&.split(",") || %w[
  qwen3.6:35b-a3b
  gemma4:12b-mlx
  qwen3:4b
  qwen3.6:35b-mlx
  gemma4:e4b
]).map(&:strip)

TRIALS         = Integer(ENV["EVAL_TRIALS"] || 3)
RUN_TIMEOUT    = Integer(ENV["EVAL_TIMEOUT"] || 280) # seconds per CLI invocation
WARMUP_TIMEOUT = 240
MAX_WALL       = Integer(ENV["EVAL_MAX_WALL"] || 6.5 * 3600) # stop launching new runs after this
HERMES_PROVIDER = "ollama-launch"

# The known starting list. Titles are distinct so fuzzy matching is unambiguous.
BASE_ORG = <<~ORG
  * Inbox
  ** INBOX Random idea about the garden hose

  * Work
  ** NEXT [#B] Budget review for Q3 :@computer:
  ** TODO Call the insurance company about the claim :@phone:
  ** WAITING Reply from the travel desk :@email:

  * Home
  ** NEXT Water the plants :@home:
  ** TODO [#C] Fix the leaky faucet :@home:
ORG

TODAY = Date.today

def find(items, needle) = items.find { |i| i.title.downcase.include?(needle) }
def done_state?(item)   = %w[DONE CANCELLED].include?(item.state)

# Each task: a prompt + a deterministic predicate over the resulting parsed items
# (and the raw file text, for the restraint check). One dimension each.
TASKS = [
  { id: "capture", dim: "capture (add inbox task)",
    prompt: "Add 'buy stamps' to my inbox.",
    check: ->(items, _raw) { !!find(items, "stamp") } },

  { id: "complete", dim: "complete (fuzzy match + done)",
    prompt: "Mark the water the plants task as done.",
    check: ->(items, _raw) { (i = find(items, "water the plants")) && done_state?(i) } },

  { id: "deadline", dim: "relative date (deadline +3d)",
    prompt: "Set a deadline on the budget review task for 3 days from now.",
    check: ->(items, _raw) { (i = find(items, "budget")) && i.deadline == TODAY + 3 } },

  { id: "priority", dim: "priority change",
    prompt: "Change the leaky faucet task to priority A.",
    check: ->(items, _raw) { (i = find(items, "faucet")) && i.priority == "A" } },

  { id: "schedule", dim: "process inbox (schedule tomorrow)",
    prompt: "Schedule the garden hose idea for tomorrow.",
    check: ->(items, _raw) { (i = find(items, "garden hose")) && i.scheduled == TODAY + 1 } },

  { id: "tag", dim: "edit tags",
    prompt: "Add the tag urgent to the insurance call task.",
    check: ->(items, _raw) { (i = find(items, "insurance")) && i.tags.include?("urgent") } },

  { id: "multistep", dim: "multi-step (2 actions)",
    prompt: "Mark water the plants done and add a task to buy fertilizer.",
    check: ->(items, _raw) { (i = find(items, "water the plants")) && done_state?(i) && !!find(items, "fertilizer") } },

  { id: "readonly", dim: "restraint (read-only, no mutation)",
    prompt: "Which of my tasks are waiting?",
    check: ->(_items, raw) { raw.strip == BASE_ORG.strip } },
].select { |t| ENV["EVAL_TASKS"].nil? || ENV["EVAL_TASKS"].split(",").include?(t[:id]) }

# ---------------------------------------------------------------------------

def mono = Process.clock_gettime(Process::CLOCK_MONOTONIC)

def log(msg)
  line = "[#{Time.now.strftime("%H:%M:%S")}] #{msg}"
  puts line
  $stdout.flush
  File.open(LOG, "a") { |f| f.puts line }
end

def kill_tree(pid)
  Process.kill("TERM", -pid)
  sleep 2
  Process.kill("KILL", -pid)
rescue Errno::ESRCH, Errno::EPERM
  # group signal not permitted or already gone — best-effort single pid
  begin
    Process.kill("KILL", pid)
  rescue StandardError
    nil
  end
ensure
  begin
    Process.wait(pid)
  rescue Errno::ECHILD, Errno::ESRCH
    nil
  end
end

# Spawn a command in its own process group, capture combined output, enforce a
# hard timeout by killing the whole tree. Returns [status, output, seconds].
def run_capture(cmd, env, timeout)
  r, w = IO.pipe
  pid = Process.spawn(env, *cmd, out: w, err: w, pgroup: true, chdir: REPO)
  w.close
  out = +""
  reader = Thread.new { out << r.read } rescue nil
  t0 = mono
  status =
    begin
      Timeout.timeout(timeout) { Process.wait(pid); $?.exitstatus }
    rescue Timeout::Error
      kill_tree(pid)
      "timeout"
    end
  reader&.join(5)
  r.close rescue nil
  [status, out, (mono - t0).round(1)]
end

def warmup(model)
  cmd = ["hermes", "-z", "Reply with exactly the word: ready",
         "-m", model, "--provider", HERMES_PROVIDER, "--yolo", "--accept-hooks"]
  status, out, secs = run_capture(cmd, {}, WARMUP_TIMEOUT)
  [status == 0, secs, out]
end

def run_task(model, task)
  Dir.mktmpdir("run-", SANDBOX_BASE) do |sb|
    File.write(File.join(sb, "gtd.org"), BASE_ORG)
    File.write(File.join(sb, "archive.org"), "")
    cmd = ["ruby", File.join(REPO, "bin", "tasks"), "-p",
           "--provider", "hermes", "--model", model, task[:prompt]]
    # Pin the task files with the HIGHEST-precedence overrides (absolute paths),
    # not just TASKS_DIR: the model runs its own `bin/tasks` inside the run, and
    # if that shell lost TASKS_DIR it would fall back to config/default and
    # resolve OUTSIDE the sandbox. TASKS_ORG/TASKS_ARCHIVE beat everything and
    # don't depend on HOME/XDG, so the sandbox is airtight.
    env = { "TASKS_DIR" => sb,
            "TASKS_ORG" => File.join(sb, "gtd.org"),
            "TASKS_ARCHIVE" => File.join(sb, "archive.org") }
    status, out, secs = run_capture(cmd, env, RUN_TIMEOUT)

    org = File.join(sb, "gtd.org")
    raw = File.read(org)
    items = begin
      Tasks::Store.new(org: org, archive: File.join(sb, "archive.org")).items
    rescue StandardError
      [] # unparseable == corrupted; checks will fail
    end
    passed =
      begin
        task[:check].call(items, raw)
      rescue StandardError
        false
      end
    corrupted =
      !begin
        Tasks::Check.check(org).ok?
      rescue StandardError
        false
      end
    { status: status, passed: !!passed, corrupted: corrupted,
      latency: secs, out_tail: (out[-800..] || out) }
  end
end

def append(rec) = File.open(RUNS, "a") { |f| f.puts(rec.to_json) }

def load_done
  return {} unless File.file?(RUNS)
  File.readlines(RUNS).each_with_object({}) do |line, h|
    r = JSON.parse(line) rescue next
    h[[r["model"], r["task"], r["trial"]]] = r
  end
end

# ---------------------------------------------------------------------------

def all_records
  return [] unless File.file?(RUNS)
  File.readlines(RUNS).filter_map { |l| JSON.parse(l) rescue nil }
end

def regenerate_report(warmups)
  recs = all_records
  by_model = recs.group_by { |r| r["model"] }
  dims = TASKS.map { |t| [t[:id], t[:dim]] }
  need = (TRIALS / 2) + 1 # majority of trials

  rows = MODELS.map do |m|
    rs = by_model[m] || []
    reliable = TASKS.count do |t|
      p = rs.count { |r| r["task"] == t[:id] && r["passed"] }
      p >= need
    end
    total = rs.size
    passed = rs.count { |r| r["passed"] }
    corrupt = rs.count { |r| r["corrupted"] }
    timeouts = rs.count { |r| r["status"] == "timeout" }
    lat = rs.map { |r| r["latency"] }.compact
    avg = lat.empty? ? nil : (lat.sum / lat.size)
    { model: m, reliable: reliable, dims: TASKS.size, total: total,
      passed: passed, corrupt: corrupt, timeouts: timeouts, avg: avg,
      warmup: warmups[m] }
  end

  ranked = rows.sort_by do |r|
    [-r[:reliable], -(r[:total].zero? ? 0 : r[:passed].to_f / r[:total]), r[:avg] || 1e9]
  end

  File.open(REPORT, "w") do |f|
    f.puts "# Local LLM eval — `tasks -p` agent path"
    f.puts
    f.puts "Generated #{Time.now.strftime("%Y-%m-%d %H:%M")}. Harness: `eval/llm/harness.rb`."
    f.puts "Each model drives the real CLI through the Hermes harness against a fresh"
    f.puts "sandbox `gtd.org`; outcomes scored by the repo's own parser. #{TRIALS} trials/task,"
    f.puts "#{RUN_TIMEOUT}s timeout/run. \"Reliable\" = passed a majority (#{need}/#{TRIALS}) of trials."
    f.puts
    f.puts "## Ranking"
    f.puts
    f.puts "| Rank | Model | Reliable dims | Pass rate | Corruptions | Timeouts | Avg latency | Warm-up |"
    f.puts "|---|---|---|---|---|---|---|---|"
    ranked.each_with_index do |r, i|
      pr = r[:total].zero? ? "—" : "#{r[:passed]}/#{r[:total]} (#{(100.0 * r[:passed] / r[:total]).round}%)"
      avg = r[:avg] ? "#{r[:avg].round}s" : "—"
      wu = case r[:warmup]
           when nil then "pending"
           when false then "**unreachable**"
           else "#{r[:warmup].round}s"
           end
      f.puts "| #{i + 1} | `#{r[:model]}` | #{r[:reliable]}/#{r[:dims]} | #{pr} | #{r[:corrupt]} | #{r[:timeouts]} | #{avg} | #{wu} |"
    end
    f.puts
    f.puts "## Per-dimension (passes / trials)"
    f.puts
    f.puts "| Dimension | #{MODELS.map { |m| "`#{m}`" }.join(" | ")} |"
    f.puts "|#{"---|" * (MODELS.size + 1)}"
    dims.each do |id, label|
      cells = MODELS.map do |m|
        rs = (by_model[m] || []).select { |r| r["task"] == id }
        next "—" if rs.empty?
        p = rs.count { |r| r["passed"] }
        "#{p}/#{rs.size}"
      end
      f.puts "| #{label} | #{cells.join(" | ")} |"
    end
    f.puts
    f.puts "## Notes"
    f.puts
    f.puts "- Corruptions = runs that left `gtd.org` structurally invalid (`tasks check` fails)."
    f.puts "- Timeouts counted as failures (too slow to be usable is a real signal)."
    f.puts "- Raw per-run transcripts + outcomes in `eval/llm/runs.jsonl`."
  end
  log "report → #{File.basename(REPORT)}"
end

# ---------------------------------------------------------------------------

log "eval start — models: #{MODELS.join(", ")} | tasks: #{TASKS.map { |t| t[:id] }.join(", ")} | trials: #{TRIALS}"
done = load_done
warmups = {}
start = mono

MODELS.each do |model|
  if mono - start > MAX_WALL
    log "wall budget (#{MAX_WALL}s) reached — stopping before #{model}"
    break
  end

  log "warming up #{model}…"
  ok, secs, _out = warmup(model)
  warmups[model] = ok ? secs : false
  log "warmup #{model}: #{ok ? "ok (#{secs}s)" : "FAILED — skipping model"}"
  regenerate_report(warmups)
  next unless ok

  TASKS.each do |task|
    TRIALS.times do |trial|
      key = [model, task[:id], trial]
      if done.key?(key)
        log "skip (done) #{model} #{task[:id]} ##{trial}"
        next
      end
      if mono - start > MAX_WALL
        log "wall budget reached mid-model — stopping"
        regenerate_report(warmups)
        exit 0
      end
      res = run_task(model, task)
      rec = { model: model, task: task[:id], dim: task[:dim], trial: trial }.merge(res)
      append(rec)
      log "#{model} #{task[:id]} ##{trial}: #{res[:passed] ? "PASS" : "fail"} " \
          "(#{res[:latency]}s, exit #{res[:status]}#{res[:corrupted] ? ", CORRUPT" : ""})"
    end
  end
  regenerate_report(warmups)
end

regenerate_report(warmups)
log "eval complete"
