# frozen_string_literal: true

require "io/console"
require "date"
require_relative "ansi"
require_relative "dates"
require_relative "store"
require_relative "views"
require_relative "frame"
require_relative "claude"
require_relative "shortcuts"
require_relative "modals"
require_relative "clipboard"
require_relative "export"
require_relative "../tasks/config"

module Tui
  # The event loop: raw-mode keyboard input, gtd.org watching, and the
  # async Claude runner, multiplexed with IO.select.
  class App
    A = Ansi

    MIN_WIDTH   = 40   # floor for degenerate ptys and tiny splits; no max — full width
    TICK        = 0.25 # seconds; also the file-watch poll interval
    SPINNER     = %w[⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏].freeze
    RESP_MAX    = 10   # footer response pane grows to at most this many lines
    RESP_HINT   = "pgup/pgdn scroll · esc dismiss"
    PROMPT_MAX  = 5    # prompt input grows to at most this many lines
    MODELS      = %w[sonnet opus haiku].freeze

    # paths: injectable so tests can pin a sandbox dir; defaults to the
    # user's configured task files (env vars / ~/.config/tasks/config / root).
    def initialize(root:, paths: Tasks::Config.resolve(default_dir: root))
      @store  = Store.new(org: paths.org, archive: paths.archive)
      @claude = Claude.new(root: File.dirname(paths.org),
                           agents_path: File.join(root, "AGENTS.md"),
                           extra_prompt: paths.claude_context(cli_root: root))
      @view   = :agenda
      @sel    = 0
      @mode   = :list      # :list | :prompt | :date | :modal
      @modal  = nil        # { title:, lines: } while a modal is open
      @modal_kind = nil    # :help | :detail
      @modal_scroll = 0
      @input  = +""        # prompt buffer
      @filter = nil        # committed filter string (nil = off)
      @filter_input = +""  # filter buffer while typing
      @date_input = +""    # reschedule buffer
      @date_error = nil
      @resp   = nil        # wrapped response lines
      @resp_open = false
      @resp_scroll = 0
      @flash = nil
      @flash_until = nil
      @model = MODELS.first
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
      if @store.changed? # picks up Claude edits + external edits
        @store.reload!
        res = Tasks::Check.check(@store.org)
        flash(A.red("⚠ gtd.org: #{res.errors.size} format error(s) — run `tasks check`")) unless res.ok?
      end
      clamp_selection
    end

    # -- painting ------------------------------------------------------------

    def paint
      height, cols = IO.console&.winsize || [24, 80]
      height = [height, 10].max               # degenerate ptys report 0x0
      width  = [cols, MIN_WIDTH].max
      foot = footer(width - 2)
      lines = Frame.build(
        width: width, height: height,
        header: header(width - 2),
        rows: rows, selected: @mode == :prompt ? nil : @sel,
        footer: foot,
        popup: @mode == :date ? date_popup : nil,
        modal: @modal && scrolled_modal(height - 5 - foot.size)
      )
      print "\e[H" + lines.join("\e[K\r\n") + "\e[K"
    end

    def rows
      items = @store.items
      if (q = active_filter)
        q = q.downcase
        items = items.select { |i| i.title.downcase.include?(q) }
      end
      @rows = Views.rows(@view, items)
    end

    # The filter narrowing the views right now: the live buffer while
    # typing, the committed filter otherwise.
    def active_filter
      s = @mode == :filter ? @filter_input : @filter
      s.nil? || s.strip.empty? ? nil : s
    end

    def header(w)
      tabs = Views::TABS.map do |label, key|
        key == @view ? A.invert(A.bold(" #{label} ")) : A.dim(" #{label} ")
      end.join(" ")
      count = A.dim("#{@store.items.count(&:open?)} open · #{A.cyan(@model)}#{A.dim(" · ? help")}")
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
        scroll_hint = @resp.size > RESP_MAX ? "#{@resp_scroll + visible.size}/#{@resp.size} · #{RESP_HINT}" : "esc dismiss"
        f << A.dim("   ── #{scroll_hint} ──")
        f << :rule
      end
      f << " #{@flash}" if @flash
      if @mode == :filter
        f << " #{A.bold(A.cyan("/ "))}#{@filter_input}#{A.invert(" ")}#{A.dim("  enter keeps · esc clears")}"
      elsif @filter
        n = (@rows || []).count(&:item)
        f << A.dim(" / #{@filter} · #{n} match#{n == 1 ? "" : "es"} · esc clears · / edits")
      end
      f.concat(prompt_lines(w))
      f
    end

    # The prompt grows to PROMPT_MAX lines as the input wraps, so a wordy
    # request stays readable; beyond that, the earliest lines scroll off.
    def prompt_lines(w)
      unless @mode == :prompt
        hint = @claude.running? ? A.dim("…") : A.dim("tab to ask claude — reschedule, capture, edit anything…")
        return [" #{A.bold(A.cyan("❯ "))}#{hint}"]
      end
      # char-slice rather than word-wrap: the input must render verbatim
      # (word-wrap rstrips, which hid a trailing space until the next char)
      wrapped = @input.chars.each_slice(w - 5).map(&:join)
      wrapped = [""] if wrapped.empty?
      wrapped = wrapped.last(PROMPT_MAX)
      wrapped.each_with_index.map do |l, i|
        prefix = i.zero? ? " #{A.bold(A.cyan("❯ "))}" : "   "
        cursor = i == wrapped.size - 1 ? A.invert(" ") : ""
        "#{prefix}#{l}#{cursor}"
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
        # force UTF-8; a 128-byte read can split a multibyte char (e.g. a
        # pasted em-dash), so scrub dangling bytes before they reach the
        # [[:print:]] test and raise on the invalid sequence.
        $stdin.read_nonblock(128).force_encoding("UTF-8").scrub("")
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
      when :modal  then modal_key(k)
      when :filter then filter_key(k)
      else              list_key(k)
      end
    end

    def filter_key(k)
      case k
      when "\e"       then @filter = nil; @mode = :list # esc clears entirely
      when "\r", "\n" then commit_filter
      when "", "\b" then @filter_input.chop!
      when ""   then @quit = true
      else
        @filter_input << k if k =~ /[[:print:]]/
      end
    end

    def commit_filter
      @filter = @filter_input.strip.empty? ? nil : @filter_input.strip
      @mode = :list
    end

    # List-mode keys live in Shortcuts (which also feeds the ? modal) —
    # this just dispatches. Actions that need the key take one argument.
    def list_key(k)
      entry = Shortcuts.find(k)
      return unless entry
      m = method(entry.action)
      m.arity.zero? ? m.call : m.call(k)
    end

    def modal_key(k)
      case k
      when "\e", "q", "\r", "\n", "?" then close_modal
      when ""        then @quit = true
      when "\e[A", "k"     then modal_move(-1)
      when "\e[B", "j"     then modal_move(1)
      when "\e[5~"         then scroll_modal(-5)
      when "\e[6~"         then scroll_modal(5)
      when "y"             then yank_ref
      when "Y"             then yank_markdown
      when "K"             then raise_priority
      when "J"             then lower_priority
      when "p"             then paste_ref
      when "u"             then undo_last
      when "\x12"          then redo_last
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

    # -- shortcut actions (dispatched from Shortcuts::LIST) --------------------

    def select_prev    = move(-1)
    def select_next    = move(1)
    def prev_view      = cycle_view(-1)
    def next_view      = cycle_view(1)
    def jump_view(k)   = switch_view(k.to_i)
    def focus_prompt   = @mode = :prompt
    def resp_up        = scroll_resp(-5)
    def resp_down      = scroll_resp(5)
    def quit           = @quit = true

    # Priority ladder: A is highest, nil (no cookie) lowest.
    PRIORITY_ORDER = ["A", "B", "C", nil].freeze

    def raise_priority = bump_priority(-1)
    def lower_priority = bump_priority(1)

    def bump_priority(delta)
      item = current_item
      return flash("nothing selected") unless item
      idx = PRIORITY_ORDER.index(item.priority)
      new_pri = PRIORITY_ORDER[(idx + delta).clamp(0, PRIORITY_ORDER.size - 1)]
      return if new_pri == item.priority # already at the end of the ladder
      if @store.set_priority!(item, new_pri)
        flash(new_pri ? "priority: [##{new_pri}] #{item.title}" : "priority cleared: #{item.title}")
        reselect(item.line)
        show_detail if @modal_kind == :detail
      else
        @store.reload!
        flash("file changed underneath — try again")
      end
    end

    # After a mutation, views may re-sort; follow the task by its file line
    # (stable for in-place edits) instead of the old row position.
    def reselect(line)
      rows
      @sel = @rows.each_index.find { |i| @rows[i].item&.line == line } || @sel
      clamp_selection
    end

    def start_filter
      @filter_input = @filter ? @filter.dup : +"" # `/` with a filter active edits it
      @mode = :filter
    end

    def toggle_model
      @model = MODELS[(MODELS.index(@model) + 1) % MODELS.size]
      flash("model: #{@model}#{@claude.running? ? " (applies to the next request)" : ""}")
    end

    def undo_last  = history_op(:undo!, "undid")
    def redo_last  = history_op(:redo!, "redid")

    def history_op(op, verb)
      kind, label = @store.public_send(op)
      case kind
      when :empty    then flash("nothing to #{verb == "undid" ? "undo" : "redo"}")
      when :conflict then flash("file changed externally — can't #{op.to_s.chomp("!")} “#{label}”")
      else
        flash("#{verb}: #{label}")
        rows
        clamp_selection
        show_detail if @modal_kind == :detail
      end
    end

    def paste_ref
      item = current_item
      return flash("nothing selected") unless item
      close_modal if @modal
      @input << " " unless @input.empty? || @input.end_with?(" ")
      @input << "\"#{Export.reference(item)}\" "
      @mode = :prompt
    end

    def yank_ref
      yank { |item, _block| Export.reference(item) }
    end

    def yank_markdown
      yank { |item, block| Export.markdown(item, block) }
    end

    def yank
      item = current_item
      return flash("nothing selected") unless item
      text = yield(item, @store.block(item))
      if Clipboard.copy(text)
        flash("yanked: “#{item.title}”")
      else
        flash("no clipboard tool found (pbcopy/wl-copy/xclip/xsel)")
      end
    end

    def open_help
      open_modal(Modals.help, kind: :help)
    end

    def open_detail
      return flash("nothing selected") unless current_item
      show_detail
    end

    # Build (or rebuild, when the selection moves) the detail modal for the
    # current item. Keeps :modal mode.
    def show_detail
      item = current_item
      return close_modal unless item
      width = [(IO.console&.winsize || [24, 80])[1], MIN_WIDTH].max
      open_modal(Modals.detail(item, @store.block(item), width), kind: :detail)
    end

    # -- modal -----------------------------------------------------------------

    # In a task detail modal, ↑↓ walk the task list and the modal follows
    # the selection. Other modals (help) keep ↑↓ as scroll.
    def modal_move(delta)
      return scroll_modal(delta) unless @modal_kind == :detail
      move(delta)
      show_detail
    end

    def open_modal(modal, kind:)
      @modal = modal
      @modal_kind = kind
      @modal_scroll = 0
      @mode = :modal
    end

    def close_modal
      @modal = nil
      @modal_kind = nil
      @mode = :list
    end

    def scroll_modal(delta)
      @modal_scroll = [@modal_scroll + delta, 0].max # upper bound applied at paint
    end

    # Slice the modal content to what fits in the body, with scroll markers.
    def scrolled_modal(body_h)
      lines = @modal[:lines]
      avail = [body_h - 2, 3].max
      return @modal if lines.size <= avail
      max_scroll = lines.size - avail
      @modal_scroll = @modal_scroll.clamp(0, max_scroll)
      visible = lines[@modal_scroll, avail]
      marker = A.dim("── #{@modal_scroll + visible.size}/#{lines.size} · ↑↓ scroll ──")
      { title: @modal[:title], lines: visible + [marker] }
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

    def cycle_view(delta)
      keys = Views::TABS.map(&:last)
      switch_view(((keys.index(@view) + delta) % keys.size) + 1)
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
        promoted = item.state == "INBOX" ? " · INBOX → TODO" : ""
        flash("→ #{item.title}: #{date.iso8601} (#{date.strftime("%a")})#{promoted}")
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
      return flash("claude is still working — esc to cancel") if @claude.running?
      return flash("claude CLI not found on PATH") unless Claude.available?
      @resp_open = false
      @claude.start(text, model: @model)
    end

    def pump_claude
      return unless @claude.pump == :done
      width = [(IO.console&.winsize || [24, 80])[1], MIN_WIDTH].max
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
      elsif @filter
        @filter = nil
        flash("filter cleared")
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
