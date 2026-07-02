# frozen_string_literal: true

module Tui
  # Runs `claude -p` asynchronously so the UI stays live while Claude works.
  # The app puts #io into its IO.select set and calls #pump when readable.
  class Claude
    attr_reader :output, :io

    def initialize(root:, agents_path:)
      @root = root
      @agents = agents_path
      @output = +""
      @pid = nil
      @io = nil
    end

    def self.available?
      system("command -v claude >/dev/null 2>&1")
    end

    def running? = !@pid.nil?

    def start(prompt, model: "sonnet")
      @output = +""
      r, w = IO.pipe
      @pid = Process.spawn(*command(prompt, model: model), in: File::NULL, out: w, err: w, chdir: @root)
      w.close
      @io = r
      self
    end

    def command(prompt, model:)
      # --dangerously-skip-permissions: headless run can't answer permission
      # prompts, and the whole point is letting the agent edit gtd.org freely
      args = ["claude", "-p", prompt, "--model", model, "--output-format", "text",
              "--dangerously-skip-permissions"]
      args += ["--append-system-prompt", File.read(@agents, encoding: "UTF-8")] if File.exist?(@agents)
      args
    end

    # Drain available output. Returns :running or :done.
    def pump
      return :done unless @io
      # read_nonblock returns BINARY; force UTF-8 so downstream string ops
      # (wrap/truncate against UTF-8 literals) don't raise CompatibilityError
      @output << @io.read_nonblock(65_536).force_encoding("UTF-8")
      :running
    rescue IO::WaitReadable
      :running
    rescue EOFError
      finish
    end

    def cancel
      Process.kill("TERM", @pid) if @pid
      finish
    rescue Errno::ESRCH
      finish
    end

    private

    def finish
      @io&.close
      @io = nil
      if @pid
        Process.wait(@pid)
        @pid = nil
      end
      :done
    rescue Errno::ECHILD
      @pid = nil
      :done
    end
  end
end
