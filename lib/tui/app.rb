# frozen_string_literal: true

require "io/console"
require "date"
require_relative "ansi"
require_relative "dates"
require_relative "store"
require_relative "views"
require_relative "frame"
require_relative "claude"

module Tui
  # The event loop: raw-mode keyboard input, gtd.org watching, and the
  # async Claude runner, multiplexed with IO.select.
  class App
    A = Ansi

    MAX_WIDTH   = 120
    TICK        = 0.25 # seconds; also the file-watch poll interval
    SPINNER     = %w[⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏].freeze
    RESP_MAX    = 10   # footer response pane grows to at most this many lines
    KEYBAR      = " ↑↓ select · c complete · d date · x archive · 1-4 views · tab prompt · q quit"
    KEYBAR_RESP = " pgup/pgdn scroll · esc dismiss · other keys as usual"

    def initialize(root:)
      @store  = Store.new(org: File.join(root, "gtd.org"), archive: File.join(root, "archive.org"))
      @claude = Claude.new(root: root, agents_path: File.join(root, "AGENTS.md"))
      @view   = :agenda
      @sel    = 0
      @mode   = :list      # :list | :prompt | :date
      @input  = +""        # prompt buffer
      @date_input = +""    # reschedule buffer
      @date_error = nil
      @resp   = nil        # wrapped response lines
      @resp_open = false
      @resp_scroll = 0
      @flash = nil
      @flash_until = nil
      @tick = 0
      @quit = false
    end

    def run
      $stdin.raw!
      print "\e[?1049h\e[?25l" # alt screen, hide cursor
      loop_once until @quit
    ensure
      print "\e[?1049l\e[?25h"
      $stdin.cooked!
    end

    private

    def loop_once
      @tick += 1
      clear_flash_if_expired
      paint

      ios = [$stdin]
      ios << @claude.io if @claude.io
      ready = IO.select(ios, nil, nil, TICK)
      (ready&.first || []).each do |io|
        io == $stdin ? read_keys : pump_claude
      end
      @store.reload! if @store.changed? # picks up Claude edits + external edits
      clamp_selection
    end

    # -- painting ------------------------------------------------------------

    def paint
      height, cols = IO.console&.winsize || [24, 80]
      height = [height, 10].max               # degenerate ptys report 0x0
      width  = [[cols, MAX_WIDTH].min, 40].max
      lines = Frame.build(
        width: width, height: height,
        header: header(width - 2),
        rows: rows, selected: @mode == :list || @mode == :date ? @sel : nil,
        footer: footer(width - 2),
        popup: @mode == :date ? date_popup : nil
      )
      print "\e[H" + lines.join("\e[K\r\n") + "\e[K"
    end

    def rows
      @rows = Views.rows(@view, @store.items)
    end

    def header(w)
      tabs = Views::TABS.map do |label, key|
        key == @view ? A.invert(A.bold(" #{label} ")) : A.dim(" #{label} ")
      end.join(" ")
      count = A.dim("#{@store.items.count(&:open?)} open")
      gap = [w - A.vislen(tabs) - A.vislen(count) - 2, 1].max
      " #{tabs}#{" " * gap}#{count} "
    end

    def footer(w)
      f = []
      if @claude.running?
        f << A.dim(" #{SPINNER[@tick % SPINNER.size]} claude is working… (esc cancels)")
        # scrub: a streaming chunk can end mid-multibyte-char
        A.strip(@claude.output.scrub("�")).split("\n").last(3).each { |t| f << A.dim("   #{t}") }
        f << :rule
      elsif @resp_open && @resp
        visible = @resp[@resp_scroll, RESP_MAX] || []
        visible.each { |l| f << "   #{l}" }
        scroll_hint = @resp.size > RESP_MAX ? "#{@resp_scroll + visible.size}/#{@resp.size} · #{KEYBAR_RESP.strip}" : "esc dismiss"
        f << A.dim("   ── #{scroll_hint} ──")
        f << :rule
      end
      f << (@flash ? " #{@flash}" : A.dim(KEYBAR))
      f << prompt_line
      f
    end

    def prompt_line
      if @mode == :prompt
        " #{A.bold(A.cyan("❯ "))}#{@input}#{A.invert(" ")}"
      elsif @claude.running?
        " #{A.bold(A.cyan("❯ "))}#{A.dim("…")}"
      else
        " #{A.bold(A.cyan("❯ "))}#{A.dim("tab to ask claude — reschedule, capture, edit anything…")}"
      end
    end

    def date_popup
      item = current_item
      return nil unless item
      target = item.deadline ? "deadline" : item.scheduled ? "scheduled" : "deadline (new)"
      hint = @date_error || "fri · +3 · 07-15 · esc cancels"
      inner = [
        " new #{target}: #{A.bold(@date_input)}#{A.invert(" ")}",
        " #{@date_error ? A.red(hint) : A.dim(hint)}",
      ]
      pw = [inner.map { |l| A.vislen(l) }.max + 2, 36].max
      lines = ["┌ reschedule #{"─" * (pw - 14)}┐"]
      inner.each { |l| lines << "│#{A.vpad(l, pw - 2)}│" }
      lines << "└#{"─" * (pw - 2)}┘"
      { lines: lines, row: sel_screen_row + 1, col: 8 }
    end

    def sel_screen_row
      # body row of the selection, accounting for the frame's scroll offset
      height = [(IO.console&.winsize || [24]).first, 10].max
      body_h = [height - 5 - footer_size, 1].max
      @sel >= body_h ? body_h - 1 : @sel
    end

    def footer_size = footer(80).size

    # -- input ---------------------------------------------------------------

    def read_keys
      data = begin
        $stdin.read_nonblock(128).force_encoding("UTF-8")
      rescue IO::WaitReadable, EOFError
        return
      end
      until data.empty?
        if data.start_with?("\e") && data.length > 1
          seq = data[/\A\e\[[0-9;]*[A-Za-z~]/] || "\e"
          handle_key(seq)
          data = data[seq.length..]
        else
          handle_key(data[0])
          data = data[1..]
        end
      end
    end

    def handle_key(k)
      case @mode
      when :prompt then prompt_key(k)
      when :date   then date_key(k)
      else              list_key(k)
      end
    end

    def list_key(k)
      case k
      when "q", ""  then @quit = true
      when "\e[A", "k"    then move(-1)
      when "\e[B", "j"    then move(1)
      when "\e[5~"        then scroll_resp(-5)
      when "\e[6~"        then scroll_resp(5)
      when "\e"           then dismiss_or_cancel
      when "\t", ":"      then @mode = :prompt
      when "1".."4"       then switch_view(k.to_i)
      when "c"            then complete_selected
      when "d"            then open_date_popup
      when "x"            then archive_sweep
      end
    end

    def prompt_key(k)
      case k
      when "\e"           then @mode = :list
      when "\t"           then @mode = :list
      when "\r", "\n"     then submit_prompt
      when "", "\b" then @input.chop!
      when ""       then @quit = true
      else
        @input << k if k =~ /[[:print:]]/
      end
    end

    def date_key(k)
      case k
      when "\e"           then close_date_popup
      when "\r", "\n"     then submit_date
      when "", "\b" then @date_input.chop!
      when ""       then @quit = true
      else
        if k =~ /[[:print:]]/
          @date_input << k
          @date_error = nil
        end
      end
    end

    # -- actions ---------------------------------------------------------------

    def selectable_indexes = @rows.each_index.select { |i| @rows[i].item }

    def current_item = @rows[@sel]&.item

    def move(delta)
      sels = selectable_indexes
      return if sels.empty?
      cur = sels.index(@sel) || 0
      @sel = sels[(cur + delta).clamp(0, sels.size - 1)]
    end

    def clamp_selection
      sels = selectable_indexes
      return @sel = 0 if sels.empty?
      @sel = sels.min_by { |i| (i - @sel).abs } unless sels.include?(@sel)
    end

    def switch_view(n)
      @view = Views::TABS[n - 1].last
      @sel = 0
      rows
      clamp_selection
    end

    def complete_selected
      item = current_item
      return flash("nothing selected") unless item
      return flash("already #{item.state}") unless item.open?
      if @store.complete!(item)
        flash("✓ DONE: #{item.title} — x to archive")
      else
        @store.reload!
        flash("file changed underneath — try again")
      end
    end

    def open_date_popup
      return flash("nothing selected") unless current_item
      @date_input = +""
      @date_error = nil
      @mode = :date
    end

    def close_date_popup
      @mode = :list
      @date_input = +""
      @date_error = nil
    end

    def submit_date
      item = current_item
      date = Dates.parse_when(@date_input)
      return @date_error = "can't parse “#{@date_input}”" unless date
      if @store.reschedule!(item, date)
        flash("→ #{item.title}: #{date.iso8601} (#{date.strftime("%a")})")
        close_date_popup
      else
        @store.reload!
        @date_error = "file changed underneath — reopen"
      end
    end

    def archive_sweep
      n = @store.archive_swept!
      flash(n.zero? ? "nothing to archive" : "archived #{n} item#{n == 1 ? "" : "s"}")
    end

    # -- claude ----------------------------------------------------------------

    def submit_prompt
      text = @input.strip
      @input = +""
      @mode = :list
      return if text.empty?
      return flash("claude CLI not found on PATH") unless Claude.available?
      @resp_open = false
      @claude.start(text)
    end

    def pump_claude
      return unless @claude.pump == :done
      width = [[(IO.console&.winsize || [24, 80])[1], MAX_WIDTH].min, 40].max
      @resp = A.wrap(@claude.output.strip, width - 8)
      @resp = [A.dim("(no output)")] if @resp.all? { |l| l.strip.empty? }
      @resp_open = true
      @resp_scroll = 0
      @store.reload! if @store.changed?
    end

    def scroll_resp(delta)
      return unless @resp_open && @resp
      max = [@resp.size - RESP_MAX, 0].max
      @resp_scroll = (@resp_scroll + delta).clamp(0, max)
    end

    def dismiss_or_cancel
      if @claude.running?
        @claude.cancel
        flash("cancelled")
      elsif @resp_open
        @resp_open = false
      end
    end

    # -- flash -------------------------------------------------------------

    def flash(msg)
      @flash = msg
      @flash_until = Time.now + 3
    end

    def clear_flash_if_expired
      @flash = nil if @flash && Time.now > @flash_until
    end
  end
end
