# frozen_string_literal: true

require_relative "config"

module Tasks
  # Assembles the system-context string handed to any agent — the CLI `tasks -p`
  # path and the TUI queue both build through here, so they can never disagree
  # about what an agent sees. In order:
  #
  #   1. the repository AGENTS.md contract (the versioned agent instructions),
  #   2. the absolute file locations for this run (CLI, task files, memory),
  #   3. a short pointer to the task-set memory policy, and
  #   4. the current contents of agent-memory.md, clearly delimited as
  #      user-approved defaults, when that sidecar exists and is nonempty.
  #
  # The memory sidecar is read fresh on every call — never cached — so a default
  # saved by one request is visible to the next, and an external edit or Git
  # pull is picked up without restarting the TUI. An absent file simply omits
  # the memory section; the builder never creates the file as a side effect.
  module AgentContext
    # Raised when the memory sidecar exists but can't be safely injected
    # (unreadable, invalid UTF-8, or over the size budget). Callers surface the
    # message and abort the run rather than proceed without the user's defaults.
    Error = Class.new(StandardError)

    # Prompt-injection budget for the memory sidecar. A larger file is a
    # configuration mistake, so fail loudly with the path instead of silently
    # truncating a default the agent would then only half-apply.
    MEMORY_MAX_BYTES = 16 * 1024 # 16 KiB

    MEMORY_HEADER = "User-approved task-set defaults from agent-memory.md. These are " \
                    "durable defaults for this task set; the current request still wins."
    MEMORY_BEGIN = "----- BEGIN AGENT MEMORY -----"
    MEMORY_END   = "----- END AGENT MEMORY -----"

    # A short pointer only. The full policy prose lives in AGENTS.md (item 1),
    # the versioned agent contract, so it is never duplicated as a Ruby string
    # that would drift out of sync.
    MEMORY_POINTER = "Task-set memory: apply the agent-memory.md defaults per the memory " \
                     "policy in AGENTS.md — add, change, or remove them only on an explicit " \
                     "request, and report any change alongside task changes."

    module_function

    # paths:    a Tasks::Config::Paths (org/archive/memory + agent_context).
    # cli_root: the application checkout, where bin/tasks and AGENTS.md live —
    #           distinct from the task-data directory the harness runs in.
    def build(paths:, cli_root:)
      agents = File.join(cli_root, "AGENTS.md")
      base = File.file?(agents) ? File.read(agents, encoding: "UTF-8") : +""
      sections = [base, paths.agent_context(cli_root: cli_root), MEMORY_POINTER]
      sections << memory_section(paths.memory)
      sections.reject { |section| section.to_s.strip.empty? }.join("\n\n")
    end

    # The delimited memory block, or nil when the sidecar is absent or empty. An
    # unreadable, invalid-UTF-8, or oversize file is a hard error carrying the
    # path — never a silent skip that would run the agent without saved defaults.
    def memory_section(path)
      return nil unless path && File.exist?(path)
      raise Error, "task-set memory at #{path} is not a regular file" unless File.file?(path)

      # Reject on size before slurping, so a pathologically large file can't be
      # read wholesale just to be rejected.
      bytes = File.size(path)
      if bytes > MEMORY_MAX_BYTES
        raise Error, "task-set memory at #{path} is #{bytes} bytes, over the " \
                     "#{MEMORY_MAX_BYTES}-byte budget — trim agent-memory.md"
      end

      begin
        raw = File.read(path, encoding: "UTF-8")
      rescue SystemCallError => e
        raise Error, "cannot read task-set memory at #{path}: #{e.message}"
      end

      unless raw.valid_encoding?
        raise Error, "task-set memory at #{path} is not valid UTF-8 — fix or remove agent-memory.md"
      end
      # The delimiters mark the block as data; a body containing one could
      # escape the fence and pose as trusted prompt text (e.g. from a pulled
      # or cloned sidecar). Reserved lines, same hard-error treatment.
      if raw.include?(MEMORY_BEGIN) || raw.include?(MEMORY_END)
        raise Error, "task-set memory at #{path} contains a reserved delimiter " \
                     "line (#{MEMORY_END.strip}) — remove it from agent-memory.md"
      end
      return nil if raw.strip.empty?

      "#{MEMORY_HEADER}\n#{MEMORY_BEGIN}\n#{raw.strip}\n#{MEMORY_END}"
    end
  end
end
