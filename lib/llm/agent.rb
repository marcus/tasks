# frozen_string_literal: true

module LLM
  # Abstract agent protocol — the one and only execution contract in this layer.
  #
  # An "agent" is an autonomous harness: we hand it a prompt, a system-context
  # string, and a working directory, and it acts on its own — reads tasks.jsonl,
  # runs `bin/tasks`, edits files. Our code never parses its output for meaning;
  # it streams a transcript to the user and reloads the Store when the file
  # changes on disk. Every backend (Claude CLI, Hermes, later the Claude Agent
  # SDK / opencode / pi) implements this. There is deliberately no separate
  # "completion" protocol — we reach local models by putting a harness in front
  # of them, never by calling a bare model and coercing its text ourselves.
  #
  # Backends vary only along two axes this base hides:
  #   - transport: how we drive the harness. The subclasses here all spawn a CLI
  #     and reuse this machinery; an SDK-transport adapter would override start/
  #     pump/cancel while keeping the same surface.
  #   - model: which model the harness runs, passed through to #command.
  #
  # Two entry points share one #command:
  #   - async (TUI): #start, then multiplex #io with IO.select, drain via #pump,
  #     #cancel to stop. Output is captured into #output.
  #   - sync  (CLI): #run_sync spawns with inherited stdio so output streams
  #     straight to the terminal, and returns true on a clean exit.
  class Agent
    CANCEL_TERM_GRACE = 0.15
    CANCEL_KILL_GRACE = 0.15

    attr_reader :output, :io, :process_status

    # root:    working directory the harness runs in (where tasks.jsonl lives).
    # system:  fully-resolved system-context string (TASK_AGENT.md + file
    #          locations), or nil. Adapters inject it however their CLI allows.
    # command: binary name/path — lets config point at a non-default install.
    def initialize(root:, system: nil, command: nil, **_opts)
      @root = root
      @system = system.to_s.empty? ? nil : system
      @command = command.to_s.empty? ? self.class.default_command : command
      @output = +""
      @pid = nil
      @io = nil
      @process_status = nil
      @cancelled = false
    end

    # The binary this adapter spawns unless config overrides it.
    def self.default_command = raise NotImplementedError

    # Build the argv for a run. stream: true => emit a live transcript incl.
    # tool activity (TUI); false => final answer only (sync CLI). Subclasses
    # override; both entry points funnel through here so there is one source of
    # truth for how a backend is invoked.
    def command(prompt, model:, stream: true) = raise NotImplementedError

    # Is the backend usable right now? The base checks the binary is on PATH;
    # subclasses add reachability probes (e.g. Hermes pings its model endpoint).
    # Never raises — a dead backend returns false so the UI can flash, not crash.
    def available? = command_on_path?(@command)

    def running? = !@pid.nil?
    def cancelled? = @cancelled
    def exit_status = @process_status&.exitstatus
    def success? = !!@process_status&.success?

    def start(prompt, model:, stream: true)
      @output = +""
      @process_status = nil
      @cancelled = false
      r, w = IO.pipe
      # pgroup: true puts the child in its own process group so #cancel can TERM
      # the whole tree — these harnesses spawn tool subprocesses that would
      # otherwise be orphaned when we kill just the leader.
      @pid = Process.spawn(*command(prompt, model: model, stream: stream),
                           in: File::NULL, out: w, err: w, chdir: @root, pgroup: true)
      w.close
      @io = r
      self
    rescue StandardError
      w&.close unless w&.closed?
      r&.close unless r&.closed?
      @pid = nil
      @io = nil
      raise
    end

    # Drain available output. Returns :running or :done.
    def pump
      return :done unless @io
      # read_nonblock returns BINARY; force UTF-8 so downstream string ops
      # (wrap/truncate against UTF-8 literals) don't raise CompatibilityError.
      @output << @io.read_nonblock(65_536).force_encoding("UTF-8")
      :running
    rescue IO::WaitReadable
      :running
    rescue EOFError
      finish
    end

    def cancel
      @cancelled = true
      close_output
      return :done unless @pid

      # Negative pid = signal the whole process group started in #start. Some
      # harnesses (or their tool subprocesses) trap TERM, so cancellation must
      # not block the raw-terminal UI indefinitely in wait2. Give the group a
      # short graceful window, escalate to KILL, then detach as a last resort so
      # the child can still be reaped without holding the UI hostage.
      signal_group("TERM")
      return :done if reap_within(CANCEL_TERM_GRACE)

      signal_group("KILL")
      return :done if reap_within(CANCEL_KILL_GRACE)

      detach_unreaped_child
      :done
    end

    # Blocking run for the CLI: inherit stdio so the harness streams to the
    # terminal live, and return whether it exited cleanly. Defaults to the
    # non-streaming (final-answer) shape since there's no UI to animate.
    def run_sync(prompt, model:, stream: false)
      system(*command(prompt, model: model, stream: stream), chdir: @root)
    end

    private

    # PATH lookup without spawning a shell. An explicit path is checked directly.
    def command_on_path?(bin)
      return File.executable?(bin) if bin.include?("/")

      ENV.fetch("PATH", "").split(File::PATH_SEPARATOR).any? do |dir|
        File.executable?(File.join(dir, bin))
      end
    end

    def finish
      close_output
      if @pid
        _pid, @process_status = Process.wait2(@pid)
        @pid = nil
      end
      :done
    rescue Errno::ECHILD
      @pid = nil
      :done
    end

    def close_output
      @io&.close
      @io = nil
    end

    def signal_group(signal)
      Process.kill(signal, -@pid) if @pid
    rescue Errno::ESRCH
      nil
    end

    def reap_within(seconds)
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + seconds
      loop do
        waited_pid, status = Process.wait2(@pid, Process::WNOHANG)
        if waited_pid
          @process_status = status
          @pid = nil
          return true
        end

        remaining = deadline - Process.clock_gettime(Process::CLOCK_MONOTONIC)
        return false unless remaining.positive?

        IO.select(nil, nil, nil, [remaining, 0.01].min)
      end
    rescue Errno::ECHILD
      @pid = nil
      true
    end

    def detach_unreaped_child
      pid = @pid
      @pid = nil
      Process.detach(pid) if pid
    rescue Errno::ECHILD
      nil
    end
  end
end
