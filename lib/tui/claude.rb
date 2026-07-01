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

    def start(prompt)
      @output = +""
      r, w = IO.pipe
      args = ["claude", "-p", prompt, "--model", "sonnet", "--output-format", "text"]
      args += ["--append-system-prompt", File.read(@agents, encoding: "UTF-8")] if File.exist?(@agents)
      @pid = Process.spawn(*args, in: File::NULL, out: w, err: w, chdir: @root)
      w.close
      @io = r
      self
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
