# frozen_string_literal: true

require "io/console"
require "date"
require "set"
require_relative "ansi"
require_relative "dates"
require_relative "store"
require_relative "views"
require_relative "frame"
require_relative "../llm/registry"
require_relative "shortcuts"
require_relative "modals"
require_relative "clipboard"
require_relative "export"
require_relative "session"
require_relative "text_input"
require_relative "../tasks/config"
require_relative "../tasks/opener"

module Tui
  # The event loop: raw-mode keyboard input, tasks.jsonl watching, and the
  # async LLM agent runner, multiplexed with IO.select.
  class App
    A = Ansi

    MIN_WIDTH   = 40   # floor for degenerate ptys and tiny splits; no max — full width
    TICK        = 0.25 # seconds; also the file-watch poll interval
    SPINNER     = %w[⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏].freeze
    RESP_MAX    = 10   # footer response pane grows to at most this many lines
    RESP_HINT   = "pgup/pgdn scroll · esc dismiss"
    PROMPT_MAX  = 5    # prompt input grows to at most this many lines
    PASTE_START = "\e[200~"
    PASTE_END   = "\e[201~"

    # The system-context string handed to any agent: the repo's AGENTS.md
    # conventions plus the absolute file locations for this run. Provider-
    # agnostic — each adapter injects it however its CLI allows.
    def self.agent_system(paths:, cli_root:)
      agents = File.join(cli_root, "AGENTS.md")
      base = File.exist?(agents) ? File.read(agents, encoding: "UTF-8") : +""
      [base, paths.agent_context(cli_root: cli_root)]
        .reject { |s| s.to_s.strip.empty? }.join("\n\n")
    end

    # paths:      injectable so tests can pin a sandbox dir; defaults to the
    #             user's configured task files (env / ~/.config/tasks/config).
    # llm_config: the resolved LLM config (provider/model defaults + per-provider
    #             settings). Read once here and threaded through the switcher, so
    #             both the entry list and each rebuilt agent agree. Injectable so
    #             tests are hermetic instead of reading the developer's real config.
    def initialize(root:, paths: Tasks::Config.resolve(default_dir: root),
                   llm_config: LLM::Config.load)
      @store  = Store.new(org: paths.org, archive: paths.archive,
                          links: paths.links || {}, link_systems: paths.link_systems || {},
                          max_depth: paths.max_depth)
      @urgent_days = paths.urgent_days # deadline window for the quadrants view
      # The (provider, model) switcher cycles these; the live agent is rebuilt
      # lazily when the selected provider changes (see ensure_agent_for_current!).
      @agent_root = File.dirname(paths.org)
      @sys_prompt = App.agent_system(paths: paths, cli_root: root)
      @llm_config = llm_config
      @entries    = LLM.entries(llm_config)
      @entry_idx  = 0
      @agent = build_agent(current_entry)
      @agent_provider = current_entry.provider
      @view   = restore_view # last session's view, or :agenda
      @sel    = 0
      @mode   = :list      # :list | :prompt | :date | :recur | :modal
      @modal  = nil        # { title:, lines: } while a modal is open
      @modal_kind = nil    # :help | :detail
      @modal_scroll = 0
      @input  = TextInput.new # prompt buffer
      @filter = nil        # committed filter string (nil = off)
      @collapsed = restore_collapsed # task ids folded shut in the outliner, from last session
      @show_deferred = false # Z toggles deferred (someday/maybe) tasks in/out of view
      @filter_input = TextInput.new # filter buffer while typing
      @date_input = TextInput.new   # reschedule buffer
      @date_error = nil
      @recur_input = TextInput.new  # recurrence-interval buffer
      @recur_error = nil
      @input_bytes = +"".b
      @key_data = +""
      @resp   = nil        # wrapped response lines
      @resp_open = false
      @resp_scroll = 0
      @flash = nil
      @flash_until = nil
      @tick = 0
      @quit = false
    end

    # -- agent selection -----------------------------------------------------

    def current_entry = @entries[@entry_idx]

    def build_agent(entry)
      LLM.build(entry, root: @agent_root, system: @sys_prompt, config: @llm_config)
    end

    # Rebuild the live agent when the selected provider has changed. Never swaps
    # a running agent out from under the IO.select loop — the caller guards on
    # idle, and cycling while a run is in flight just re-labels; the new provider
    # takes effect on the next submit ("applies to the next request").
    def ensure_agent_for_current!
      return if @agent_provider == current_entry.provider || @agent.running?

      @agent = build_agent(current_entry)
      @agent_provider = current_entry.provider
    end

    def run
      $stdin.raw!
      print "\e[?1049h\e[?2004h\e[?25l" # alt screen, bracketed paste, hide cursor
      loop_once until @quit
    ensure
      # Terminal restore FIRST — if saving somehow raised, a skipped restore
      # would leave the shell raw on the alt screen, far worse than a lost view.
      print "\e[?2004l\e[?1049l\e[?25h"
      $stdin.cooked!
      save_session # so the view persists however the TUI exits
    end

    private

    def loop_once
      @tick += 1
      clear_flash_if_expired
      paint

      ios = [$stdin]
      ios << @agent.io if @agent.io
      ready = IO.select(ios, nil, nil, TICK)
      (ready&.first || []).each do |io|
        io == $stdin ? read_keys : pump_agent
      end
      if @store.changed? # picks up Claude edits + external edits
        @store.reload!
        res = Tasks::Check.check(@store.org)
        flash(A.red("⚠ tasks.jsonl: #{res.errors.size} format error(s) — run `tasks check`")) unless res.ok?
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
        popup: current_popup,
        modal: @modal && scrolled_modal(height - 5 - foot.size)
      )
      print "\e[H" + lines.join("\e[K\r\n") + "\e[K"
    end

    def rows
      items = @store.items
      if (q = active_filter)
        # Filter mode renders a flat list. Deferred (someday/maybe) tasks stay
        # out until Z reveals them — the reject lives here because the tree path
        # applies the same rule inside its walker instead.
        items = items.reject(&:deferred?) unless @show_deferred
        q = q.downcase
        items = items.select { |i| i.title.downcase.include?(q) }
        @rows = Views.rows(@view, items, urgent_days: @urgent_days)
      else
        @rows = Views.rows(@view, items, tree: @store.tree, collapsed: @collapsed,
                                         show_deferred: @show_deferred, urgent_days: @urgent_days)
      end
    end

    # The filter narrowing the views right now: the live buffer while
    # typing, the committed filter otherwise.
    def active_filter
      s = @mode == :filter ? @filter_input : @filter
      s = s.to_s unless s.nil?
      s.nil? || s.strip.empty? ? nil : s
    end

    def header(w)
      tabs = Views::TABS.map do |label, key|
        key == @view ? A.invert(A.bold(" #{label} ")) : A.dim(" #{label} ")
      end.join(" ")
      open_n = @store.items.count { |i| i.open? && !i.deferred? }
      deferred_note = @show_deferred ? "#{A.yellow("⏸ deferred shown")} · " : ""
      count = A.dim("#{open_n} open · #{deferred_note}#{A.cyan(current_entry.to_s)}#{A.dim(" · ? help")}")
      gap = [w - A.vislen(tabs) - A.vislen(count) - 2, 1].max
      " #{tabs}#{" " * gap}#{count} "
    end

    def footer(w)
      f = []
      if @agent.running?
        f << A.dim(" #{SPINNER[@tick % SPINNER.size]} #{@agent_provider} is working… (esc cancels)")
        # scrub: a streaming chunk can end mid-multibyte-char
        A.strip(@agent.output.scrub("�")).split("\n").last(3).each { |t| f << A.dim("   #{t}") }
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
        f << " #{A.bold(A.cyan("/ "))}#{inline_input(@filter_input)}#{A.dim("  enter keeps · esc clears")}"
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
        hint = @agent.running? ? A.dim("…") : A.dim("tab to ask the agent — reschedule, capture, edit anything…")
        return [" #{A.bold(A.cyan("❯ "))}#{hint}"]
      end
      wrapped = wrapped_input(@input, w - 5)
      wrapped.each_with_index.map do |l, i|
        prefix = i.zero? ? " #{A.bold(A.cyan("❯ "))}" : "   "
        "#{prefix}#{l}"
      end
    end

    def wrapped_input(input, cols)
      cols = [cols, 1].max
      chars = input.text.each_grapheme_cluster.to_a
      lines = [[]]
      starts = [0]
      width = 0

      chars.each_with_index do |gc, idx|
        cw = A.cluster_width(gc)
        if width.positive? && width + cw > cols
          lines << []
          starts << idx
          width = 0
        end
        lines.last << gc
        width += cw
      end

      if chars.length.positive? && input.cursor == chars.length && width >= cols
        lines << []
        starts << chars.length
      end

      cursor_line = starts.rindex { |start| start <= input.cursor } || 0
      first_line = [cursor_line - PROMPT_MAX + 1, 0].max
      last_line = [first_line + PROMPT_MAX, lines.length].min

      (first_line...last_line).map do |line|
        segment = lines[line] || []
        cursor_col = input.cursor - starts[line]
        if cursor_col.between?(0, segment.length)
          render_input_segment(segment, cursor_col)
        else
          segment.join
        end
      end
    end

    def inline_input(input)
      render_input_segment(input.text.each_grapheme_cluster.to_a, input.cursor)
    end

    def render_input_segment(segment, cursor_col)
      before = segment[0...cursor_col].join
      at = cursor_col < segment.length ? segment[cursor_col] : " "
      after = cursor_col < segment.length ? segment[(cursor_col + 1)..].join : ""
      "#{before}#{A.invert(at)}#{after}"
    end

    # The popup layered over the list right now: reschedule (:date) or
    # recurrence (:recur), or none.
    def current_popup
      case @mode
      when :date  then date_popup
      when :recur then recur_popup
      end
    end

    def date_popup
      item = current_item
      return nil unless item
      target = item.deadline ? "deadline" : item.scheduled ? "scheduled" : "deadline (new)"
      hint = @date_error || "fri · +3 · 07-15 · esc cancels"
      inner = [
        " new #{target}: #{inline_input(@date_input)}",
        " #{@date_error ? A.red(hint) : A.dim(hint)}",
      ]
      pw = [inner.map { |l| A.vislen(l) }.max + 2, 36].max
      lines = ["┌ reschedule #{"─" * (pw - 14)}┐"]
      inner.each { |l| lines << "│#{A.vpad(l, pw - 2)}│" }
      lines << "└#{"─" * (pw - 2)}┘"
      { lines: lines, row: sel_screen_row + 1, col: 8 }
    end

    def recur_popup
      item = current_item
      return nil unless item
      cur = item.recur ? "now #{item.recur}" : "not repeating"
      hint = @recur_error || "weekly · 2w · .+1m · off · esc cancels"
      inner = [
        " every: #{inline_input(@recur_input)}  #{A.dim("(#{cur})")}",
        " #{@recur_error ? A.red(hint) : A.dim(hint)}",
      ]
      pw = [inner.map { |l| A.vislen(l) }.max + 2, 40].max
      lines = ["┌ recur #{"─" * (pw - 9)}┐"]
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
      bytes = begin
        # force UTF-8; a 128-byte read can split a multibyte char (e.g. a
        # pasted em-dash), so decode through a tiny pending-byte buffer instead
        # of scrubbing valid trailing bytes away.
        $stdin.read_nonblock(4096)
      rescue IO::WaitReadable, EOFError
        return
      end
      @input_bytes << bytes
      @key_data << drain_utf8_input
      drain_key_data
    end

    def drain_utf8_input
      data = @input_bytes.dup.force_encoding("UTF-8")
      if data.valid_encoding?
        @input_bytes = +"".b
        return data
      end

      [3, 2, 1].each do |tail|
        next if @input_bytes.bytesize <= tail
        prefix = @input_bytes.byteslice(0, @input_bytes.bytesize - tail)
        candidate = prefix.dup.force_encoding("UTF-8")
        next unless candidate.valid_encoding?

        @input_bytes = @input_bytes.byteslice(-tail, tail) || +"".b
        return candidate
      end

      if (tail = incomplete_utf8_tail(@input_bytes.bytes))
        @input_bytes = @input_bytes.byteslice(-tail, tail) || +"".b
        return +""
      end

      @input_bytes = +"".b
      data.scrub("")
    end

    def incomplete_utf8_tail(bytes)
      [3, 2, 1].each do |len|
        next if bytes.length < len
        tail = bytes.last(len)
        needed = utf8_sequence_length(tail.first)
        next unless needed && needed > len
        next unless tail[1..].all? { |b| b.between?(0x80, 0xBF) }

        return len
      end
      nil
    end

    def utf8_sequence_length(byte)
      case byte
      when 0xC2..0xDF then 2
      when 0xE0..0xEF then 3
      when 0xF0..0xF4 then 4
      end
    end

    def drain_key_data
      until @key_data.empty?
        if @key_data.start_with?(PASTE_START)
          end_at = @key_data.index(PASTE_END, PASTE_START.length)
          break unless end_at

          handle_paste(@key_data[PASTE_START.length...end_at])
          @key_data = @key_data[(end_at + PASTE_END.length)..] || +""
        elsif @key_data.length > 1 && PASTE_START.start_with?(@key_data)
          break
        elsif @key_data.start_with?("\e")
          seq = @key_data[/\A\e\[[0-9;?]*[A-Za-z~]/] || @key_data[/\A\eO[A-Za-z]/]
          seq ||= "\e"
          handle_key(seq)
          @key_data = @key_data[seq.length..] || +""
        else
          char = @key_data.each_grapheme_cluster.first
          handle_key(char)
          @key_data = @key_data[char.length..] || +""
        end
      end
    end

    def handle_paste(text)
      case @mode
      when :prompt then @input.insert(text)
      when :date   then @date_input.insert(text); @date_error = nil
      when :recur  then @recur_input.insert(text); @recur_error = nil
      when :filter then @filter_input.insert(text)
      else
        close_modal if @modal
        @input.insert(text)
        @mode = :prompt
      end
    end

    def handle_key(k)
      case @mode
      when :prompt then prompt_key(k)
      when :date   then date_key(k)
      when :recur  then recur_key(k)
      when :modal  then modal_key(k)
      when :filter then filter_key(k)
      else              list_key(k)
      end
    end

    def filter_key(k)
      case k
      when "\e"       then @filter = nil; @mode = :list # esc clears entirely
      when "\r", "\n" then commit_filter
      when ""   then @quit = true
      else
        @filter_input.handle_key(k)
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

    # A detail modal keeps the task's own shortcuts live so you can act on it
    # without leaving: complete, reschedule, re-prioritize, yank. Navigation and
    # scroll stay modal-specific. Task actions rebuild the modal in place, or
    # close it when the change removes the task from the view (see the actions).
    def modal_key(k)
      case k
      when "\e", "q", "\r", "\n", "?" then close_modal
      when ""        then @quit = true
      when "\e[A", "k"     then modal_move(-1)
      when "\e[B", "j"     then modal_move(1)
      when "\e[5~"         then scroll_modal(-5)
      when "\e[6~"         then scroll_modal(5)
      when "c"             then complete_selected
      when "d"             then open_date_popup
      when "r"             then open_recur_popup
      when "z"             then defer_selected
      when "y"             then yank_ref
      when "Y"             then yank_markdown
      when "K"             then raise_priority
      when "J"             then lower_priority
      when "p"             then paste_ref
      when "u"             then undo_last
      when "o"             then open_link
      when "\x12"          then redo_last
      end
    end

    def prompt_key(k)
      case k
      when "\e"           then @mode = :list
      when "\t"           then @mode = :list
      when "\r", "\n"     then submit_prompt
      when ""       then @quit = true
      else
        @input.handle_key(k)
      end
    end

    def date_key(k)
      case k
      when "\e"           then close_date_popup
      when "\r", "\n"     then submit_date
      when ""       then @quit = true
      else
        @date_error = nil if @date_input.handle_key(k) == :changed
      end
    end

    def recur_key(k)
      case k
      when "\e"       then close_recur_popup
      when "\r", "\n" then submit_recur
      when ""   then @quit = true
      else
        @recur_error = nil if @recur_input.handle_key(k) == :changed
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

    # Z reveals/hides deferred (someday/maybe) tasks across every view.
    def toggle_deferred_view
      modaled_line = current_item&.line if @modal_kind == :detail
      @show_deferred = !@show_deferred
      rows
      clamp_selection
      # If a detail modal was open on a task the toggle just hid, close it
      # rather than silently rebinding the modal to a neighboring task.
      refresh_detail_modal(modaled_line) if modaled_line
      flash(@show_deferred ? "showing deferred tasks" : "hiding deferred tasks")
    end

    # z defers the selected task, or reactivates it if it's already deferred —
    # a snooze toggle on the item itself (distinct from Z, which toggles the view).
    def defer_selected
      item = current_item
      return flash("nothing selected") unless item
      to_deferred = !item.deferred?
      if @store.set_deferred!(item, to_deferred)
        flash(to_deferred ? "⏸ deferred: #{item.title}" : "▸ activated: #{item.title}")
        # When newly deferred and the view hides deferred, the task leaves the
        # list — drop any detail modal on it; otherwise follow it in place.
        if to_deferred && !@show_deferred
          close_modal if @modal
          rows
          clamp_selection
        else
          reselect(item.line)
          refresh_detail_modal(item.line)
        end
      else
        @store.reload!
        flash("file changed underneath — try again")
      end
    end

    def start_filter
      @filter_input.replace(@filter || +"") # `/` with a filter active edits it
      @mode = :filter
    end

    # Cycle the (provider, model) selection. Works mid-run — the change applies
    # to the next request; the in-flight agent keeps streaming untouched.
    def toggle_model
      @entry_idx = (@entry_idx + 1) % @entries.size
      ensure_agent_for_current!
      flash("agent: #{current_entry}#{@agent.running? ? " (applies to the next request)" : ""}")
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
      yank { |item, _notes| Export.reference(item) }
    end

    def yank_markdown
      yank { |item, notes| Export.markdown(item, notes) }
    end

    def yank
      item = current_item
      return flash("nothing selected") unless item
      text = yield(item, @store.body(item))
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
      open_modal(Modals.detail(item, @store.body(item), width, links: @store.links(item)),
                 kind: :detail)
    end

    # Open the selected task's first link in the browser (`o`, list or detail
    # mode). Deliberately the FIRST link: notes lead with the primary reference;
    # the CLI (`tasks open <ref> <n>`) handles precise picking.
    def open_link
      item = current_item or return
      links = @store.links(item)
      return flash("no links on this task") if links.empty?
      link = links.first
      unless Tasks::Opener.open_url(link.url)
        return flash("no browser launcher found (set TASKS_OPENER)")
      end
      extra = links.size > 1 ? " (1 of #{links.size})" : ""
      flash("opened #{link.system}: #{link.url}#{extra}")
    end

    # After a task action taken from inside a detail modal (reschedule the
    # cursor already followed to `line`): redraw the modal on the task if it's
    # still in view, or close it if the change dropped it (e.g. dating an INBOX
    # item promotes it out of the inbox view).
    def refresh_detail_modal(line)
      return unless @modal_kind == :detail
      current_item&.line == line ? show_detail : close_modal
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

    # -- outliner collapse / expand (h l H L) ----------------------------------
    #
    # The tree rows carry their Tasks::Tree node (nil for headers, blanks, and
    # every flat/filter-mode row), so hierarchy questions read straight off the
    # selection. @collapsed is a Set of task ids; Views prunes a collapsed id's
    # subtree at paint. A collapsed id that hides nothing is harmless, so these
    # never have to reason about visibility beyond "does this node show children".

    # h: fold the selected subtree. On an expandable, not-yet-folded node, add
    # its id and keep the cursor on it. Otherwise (leaf, already folded, or an
    # id-less row) climb to the parent task row — a second h walks you up the
    # tree; at the top (parent is a section/nil) it's a no-op.
    def collapse_selected
      node = @rows[@sel]&.node
      return unless node&.item
      item = node.item
      if Views.visible_children(node, @show_deferred).any? && item.id && !@collapsed.include?(item.id)
        @collapsed.add(item.id)
        reselect(item.line)
      else
        jump_to_parent(node)
      end
    end

    # l: unfold the selected node if it's folded; otherwise nothing to do.
    def expand_selected
      node = @rows[@sel]&.node
      id = node&.item&.id
      return unless id && @collapsed.include?(id)
      @collapsed.delete(id)
      reselect(node.item.line)
    end

    # H: fold every task node that has task children, across the whole tree
    # (works regardless of filter mode — the ids just wait, hidden, until the
    # filter clears). The selection may have been on a now-hidden row, so clamp.
    def collapse_all
      @store.tree.each do |root|
        root.each do |n|
          @collapsed.add(n.item.id) if n.task? && n.item.id && n.children.any?(&:task?)
        end
      end
      rows
      clamp_selection
    end

    # L: unfold everything.
    def expand_all
      @collapsed.clear
      rows
      clamp_selection
    end

    # Move the cursor to the row of `node`'s parent task. A section (or missing)
    # parent means we're already at the top of a subtree — leave the cursor put.
    def jump_to_parent(node)
      parent = node.parent
      return unless parent&.task? && parent.item
      idx = @rows.each_index.find { |i| @rows[i].item&.line == parent.item.line }
      @sel = idx if idx
    end

    # -- session persistence ---------------------------------------------------
    #
    # The active view survives a restart (Tui::Session). Restore validates
    # against the real tab list, so a stale or hand-edited value degrades to
    # the default rather than rendering a view that doesn't exist.

    def restore_view
      saved = Session.load[:view]
      # Strings only: a hand-edited "view": 123 must fall back, not crash startup.
      saved = saved.is_a?(String) ? saved.to_sym : nil
      Views::TABS.map(&:last).include?(saved) ? saved : :agenda
    end

    # The collapsed set from last session: an Array of id strings only. Anything
    # else (missing key, a hand-edited scalar, ids that no longer exist) degrades
    # to an empty set — stale ids that survive are pruned again at save time.
    def restore_collapsed
      saved = Session.load[:collapsed]
      return Set.new unless saved.is_a?(Array) && saved.all? { |x| x.is_a?(String) }
      Set.new(saved)
    end

    def save_session
      # Braces required: a braceless string-key hash would parse as keywords
      # (save has an env: kwarg) and raise. Prune the collapsed set to live task
      # ids so ids from deleted tasks don't accumulate in the state file.
      live_ids = @store.items.map(&:id).compact
      Session.save({ "view" => @view.to_s, "collapsed" => (@collapsed & live_ids).to_a })
    end

    def complete_selected
      item = current_item
      return flash("nothing selected") unless item
      return flash("already #{item.state}") unless item.open?
      recurring = item.recurring?
      result = @store.complete!(item)
      if result
        if recurring
          # A recurring task rolled forward and is still in the view — follow it.
          fresh = @store.items.find { |i| i.line == item.line }
          d = fresh && (fresh.deadline || fresh.scheduled)
          flash("↻ #{item.title}#{d ? " → #{d.iso8601} (#{d.strftime("%a")})" : ""}")
          reselect(item.line)
          refresh_detail_modal(item.line)
        else
          # complete! returns every touched line; a parent cascade closes its
          # open descendants too — note how many rode along.
          n = result.is_a?(Array) ? result.size - 1 : 0
          subs = n > 0 ? " (+#{n} subtask#{"s" unless n == 1})" : ""
          flash("✓ DONE: #{item.title}#{subs} — x to archive")
          close_modal if @modal # the task just left the open view behind it
        end
      else
        @store.reload!
        flash("file changed underneath — try again")
      end
    end

    def open_date_popup
      return flash("nothing selected") unless current_item
      @date_input.clear
      @date_error = nil
      @mode = :date
    end

    # Rescheduling can be launched from a detail modal (the popup layers over
    # it); return there rather than to the bare list when one is open.
    def close_date_popup
      @mode = @modal ? :modal : :list
      @date_input.clear
      @date_error = nil
    end

    def submit_date
      item = current_item
      date = Dates.parse_when(@date_input.to_s)
      return @date_error = "can't parse “#{@date_input}”" unless date
      if @store.reschedule!(item, date)
        promoted = item.state == "INBOX" ? " · INBOX → TODO" : ""
        flash("→ #{item.title}: #{date.iso8601} (#{date.strftime("%a")})#{promoted}")
        close_date_popup
        # a new date can move the task (agenda re-sort, quadrant change) — keep
        # the cursor on it, matching bump_priority.
        reselect(item.line)
        refresh_detail_modal(item.line)
      else
        @store.reload!
        @date_error = "file changed underneath — reopen"
      end
    end

    # r opens the recurrence popup on the selected task, pre-filled with its
    # current cookie. Recurrence rides a date stamp, so a task with no date
    # can't repeat — flash and refuse rather than open a popup that must fail.
    def open_recur_popup
      item = current_item
      return flash("nothing selected") unless item
      return flash("schedule it first — recurrence needs a date") unless item.scheduled || item.deadline
      @recur_input.replace(item.recur || +"")
      @recur_error = nil
      @mode = :recur
    end

    def close_recur_popup
      @mode = @modal ? :modal : :list
      @recur_input.clear
      @recur_error = nil
    end

    def submit_recur
      item = current_item
      cookie = Tasks::Recur.parse_interval(@recur_input.to_s)
      return @recur_error = "can't parse “#{@recur_input}”" if cookie.nil?
      if @store.set_recur!(item, cookie)
        flash(cookie == :off ? "↻ off: #{item.title}" : "↻ #{cookie}: #{item.title}")
        close_recur_popup
        reselect(item.line)
        refresh_detail_modal(item.line)
      else
        @store.reload!
        @recur_error = "file changed underneath — reopen"
      end
    end

    def archive_sweep
      n = @store.archive_swept!
      flash(n.zero? ? "nothing to archive" : "archived #{n} item#{n == 1 ? "" : "s"}")
    end

    # -- claude ----------------------------------------------------------------

    def submit_prompt
      text = @input.strip
      @input.clear
      @mode = :list
      return if text.empty?
      return flash("agent is still working — esc to cancel") if @agent.running?
      ensure_agent_for_current!
      unless @agent.available?
        return flash("#{current_entry.provider} not available — check the CLI is installed and any local model server is running")
      end
      @resp_open = false
      @agent.start(text, model: current_entry.model)
    end

    def pump_agent
      return unless @agent.pump == :done
      width = [(IO.console&.winsize || [24, 80])[1], MIN_WIDTH].max
      @resp = A.wrap(@agent.output.strip, width - 8)
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
      if @agent.running?
        @agent.cancel
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
