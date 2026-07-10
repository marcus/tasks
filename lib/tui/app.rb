# frozen_string_literal: true

require "io/console"
require "date"
require "set"
require_relative "ansi"
require_relative "theme"
require_relative "dates"
require_relative "store"
require_relative "views"
require_relative "frame"
require_relative "../llm/registry"
require_relative "shortcuts"
require_relative "modal"
require_relative "modals"
require_relative "clipboard"
require_relative "export"
require_relative "session"
require_relative "text_input"
require_relative "form"
require_relative "action_palette"
require_relative "ui_state"
require_relative "screen_layout"
require_relative "../tasks/config"
require_relative "../tasks/opener"

module Tui
  # The event loop: raw-mode keyboard input, tasks.jsonl watching, and the
  # async LLM agent runner, multiplexed with IO.select.
  class App
    A = Ansi
    T = Theme

    MIN_WIDTH   = 8    # smallest frame that can retain borders, margins, and content
    MIN_HEIGHT  = 6    # borders, header/rules, and one body row
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
      Theme.configure!(name: paths.theme, overrides: paths.colors || {})
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
      @ui = UiState.restore(saved: Session.load, views: Views::TABS.map(&:last), default_view: :agenda)
      @sel    = 0
      @input  = TextInput.new # prompt buffer
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
        reload_store
        res = Tasks::Check.check(@store.org)
        flash(T.paint(:error, "⚠ tasks.jsonl: #{res.errors.size} format error(s) — run `tasks check`")) unless res.ok?
      end
      clamp_selection
    end

    # Reload external writes without losing the selected task to a new physical
    # row. An open detail modal is rebuilt only for the id it was opened on.
    def reload_store
      overlay_mode = @ui.mode if %i[form palette].include?(@ui.mode)
      detail_id = @ui.detail_item_id if detail_modal?
      @store.reload!
      rows
      refresh_detail_modal(detail_id) if detail_id && detail_modal?
      restore_form if overlay_mode == :form && @ui.form
      if overlay_mode == :palette && @ui.action_palette
        restore_action_palette(@ui.action_palette)
      end
    end

    # -- painting ------------------------------------------------------------

    def paint
      height, width = terminal_size
      frame_rows = rows
      visual_selection = @ui.mode == :prompt ? nil : @sel
      layout = screen_layout(width: width, height: height, selected: visual_selection)
      lines = Frame.build(
        width: width, height: height,
        header: header(width - 2),
        rows: frame_rows,
        selected: visual_selection,
        footer: layout.footer,
        popup: current_popup(layout: layout),
        modal: layout.place_modal(@ui.modal&.view(layout.body_height)),
        layout: layout
      )
      print "\e[H" + lines.join("\e[K\r\n") + "\e[K"
    end

    def terminal_size
      height, width = IO.console&.winsize || [24, 80]
      height = height.to_i
      width = width.to_i
      height = 24 unless height.positive? # degenerate ptys can report 0x0
      width = 80 unless width.positive?
      [[height, MIN_HEIGHT].max, [width, MIN_WIDTH].max]
    end

    def rows
      items = @store.items
      if (q = active_filter)
        # Filter mode renders a flat list. Deferred (someday/maybe) tasks stay
        # out until Z reveals them — the reject lives here because the tree path
        # applies the same rule inside its walker instead.
        items = items.reject(&:deferred?) unless @ui.show_deferred
        q = q.downcase
        items = items.select { |i| i.title.downcase.include?(q) }
        @rows = Views.rows(@ui.view, items, show_deferred: @ui.show_deferred,
                                           urgent_days: @urgent_days, store: @store)
      else
        @rows = Views.rows(@ui.view, items, tree: @store.tree, collapsed: @ui.collapsed,
                                         show_deferred: @ui.show_deferred, urgent_days: @urgent_days,
                                         store: @store)
      end
      sync_selection
      @rows
    end

    # The filter narrowing the views right now: the live buffer while
    # typing, the committed filter otherwise.
    def active_filter
      s = @ui.mode == :filter ? @ui.filter_input : @ui.filter
      s = s.to_s unless s.nil?
      s.nil? || s.strip.empty? ? nil : s
    end

    def header(w)
      tabs = Views::TABS.map do |label, key|
        slot = key == @ui.view ? :"tab_#{key}_active" : :"tab_#{key}"
        slot = key == @ui.view ? :tab_active : :tab_inactive unless T.slot?(slot)
        T.paint(slot, " #{label} ")
      end.join(" ")
      open_n = @store.items.count { |i| i.open? && !i.deferred? }
      deferred_note = @ui.show_deferred ? "#{T.paint(:warning, "⏸ deferred shown")}#{T.paint(:muted, " · ")}" : ""
      count = "#{T.paint(:muted, "#{open_n} open · ")}#{deferred_note}#{T.paint(:accent, current_entry.to_s)}#{T.paint(:muted, " · ? help")}"
      gap = [w - A.vislen(tabs) - A.vislen(count) - 2, 1].max
      " #{tabs}#{" " * gap}#{count} "
    end

    def footer(w)
      f = []
      if @agent.running?
        f << T.paint(:muted, " #{SPINNER[@tick % SPINNER.size]} #{@agent_provider} is working… (esc cancels)")
        # scrub: a streaming chunk can end mid-multibyte-char
        A.strip(@agent.output.scrub("�")).split("\n").last(3).each { |t| f << T.paint(:muted, "   #{t}") }
        f << :rule
      elsif @resp_open && @resp
        visible = @resp[@resp_scroll, RESP_MAX] || []
        visible.each { |l| f << "   #{l}" }
        scroll_hint = @resp.size > RESP_MAX ? "#{@resp_scroll + visible.size}/#{@resp.size} · #{RESP_HINT}" : "esc dismiss"
        f << T.paint(:muted, "   ── #{scroll_hint} ──")
        f << :rule
      end
      f << " #{@flash}" if @flash
      if @ui.mode == :modal_filter
        f << " #{T.paint(:prompt, "/ ")}#{inline_input(@ui.modal_filter_input)}#{T.paint(:muted, "  filters the modal · enter keeps · esc clears")}"
      elsif @ui.mode == :filter
        f << " #{T.paint(:prompt, "/ ")}#{inline_input(@ui.filter_input)}#{T.paint(:muted, "  enter keeps · esc clears")}"
      elsif @ui.filter
        n = (@rows || []).count(&:item)
        f << T.paint(:muted, " / #{@ui.filter} · #{n} match#{n == 1 ? "" : "es"} · esc clears · / edits")
      end
      # Active text entry owns the scarce footer row on short terminals. Forms
      # and palettes render their input in the popup; filters render it here.
      f.concat(prompt_lines(w)) unless %i[modal_filter filter form palette].include?(@ui.mode)
      f
    end

    # The prompt grows to PROMPT_MAX lines as the input wraps, so a wordy
    # request stays readable; beyond that, the earliest lines scroll off.
    def prompt_lines(w)
      unless @ui.mode == :prompt
        hint = T.paint(:muted, @agent.running? ? "…" : "tab to ask the agent — reschedule, capture, edit anything…")
        return [" #{T.paint(:prompt, "❯ ")}#{hint}"]
      end
      wrapped = wrapped_input(@input, w - 5)
      wrapped.each_with_index.map do |l, i|
        prefix = i.zero? ? " #{T.paint(:prompt, "❯ ")}" : "   "
        "#{prefix}#{l}"
      end
    end

    def wrapped_input(input, cols)
      cols = [cols, 1].max
      chars = input.text.each_grapheme_cluster.to_a
      display = chars.map do |gc|
        width = A.cluster_width(gc)
        width > cols ? [" " * cols, cols] : [gc, width]
      end
      lines = [[]]
      starts = [0]
      width = 0

      display.each_with_index do |(gc, cw), idx|
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
        if line == cursor_line
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
      "#{before}#{T.paint(:selection, at)}#{after}"
    end

    # The active popup layered over the list (and, when launched from task
    # details, over the retained modal beneath it).
    def current_popup(layout: nil, width: nil, height: nil, footer_size: nil)
      if layout.nil?
        terminal_height, terminal_width = terminal_size if width.nil? || height.nil?
        width ||= terminal_width
        height ||= terminal_height
        layout = screen_layout(width: width, height: height, footer_size: footer_size)
      end
      popup, preferred_col = case @ui.mode
      when :form
        [@ui.form&.popup(row: 0, col: 0, max_width: layout.body_width, max_height: layout.body_height,
                      inline_input: method(:inline_input)), 8]
      when :palette
        [@ui.action_palette&.popup(row: 0, col: 0, max_width: layout.body_width, max_height: layout.body_height,
                               inline_input: method(:inline_input)), 3]
      end
      layout.place_popup(popup, preferred_col: preferred_col)
    end

    def sel_screen_row(height: nil, footer_size: nil)
      terminal_height, terminal_width = terminal_size
      height ||= terminal_height
      screen_layout(width: terminal_width, height: height, footer_size: footer_size).selected_screen_row
    end

    def fitted_footer(width:, height:)
      screen_layout(width: width, height: height).footer
    end

    def footer_size(width: nil, height: nil)
      terminal_height, terminal_width = terminal_size if width.nil? || height.nil?
      screen_layout(width: width || terminal_width, height: height || terminal_height).footer_size
    end

    def screen_layout(width:, height:, footer_size: nil, selected: @sel)
      raw_footer = footer_size ? Array.new(footer_size, "") : footer(width - 2)
      ScreenLayout.new(width: width, height: height, footer: raw_footer, selected: selected)
    end

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
      case @ui.mode
      when :prompt then @input.insert(text)
      when :form   then @ui.form&.paste(text)
      when :palette then @ui.action_palette&.paste(text)
      when :filter then @ui.filter_input.insert(text)
      when :modal_filter then @ui.modal_filter_input.insert(text); @ui.modal.filter = @ui.modal_filter_input.to_s
      else
        close_modal if @ui.modal
        @input.insert(text)
        @ui.mode = :prompt
      end
    end

    def handle_key(k)
      return if dispatch_action(k, :global)

      case @ui.mode
      when :prompt then prompt_key(k)
      when :form   then form_key(k)
      when :palette then palette_key(k)
      when :modal  then modal_key(k)
      when :modal_filter then modal_filter_key(k)
      when :filter then filter_key(k)
      else              list_key(k)
      end
    end

    def filter_key(k)
      case k
      when "\e"       then @ui.filter = nil; @ui.mode = :list # esc clears entirely
      when "\r", "\n" then commit_filter
      else
        @ui.filter_input.handle_key(k)
      end
    end

    def commit_filter
      @ui.filter = @ui.filter_input.strip.empty? ? nil : @ui.filter_input.strip
      @ui.mode = :list
    end

    # Contextual actions live in Shortcuts (which also feeds the ? modal).
    # A matched-but-unavailable action consumes its key so dispatch can never
    # leak into a lower-priority context.
    def list_key(k)
      dispatch_action(k, :list)
    end

    def dispatch_action(k, context)
      entry = Shortcuts.match(k, context)
      return false unless entry
      return true unless Shortcuts.available?(entry, self)

      m = method(entry.handler)
      m.arity.zero? ? m.call : m.call(k)
      true
    end

    # Modal navigation is an explicit first-stage context; task-detail actions
    # are the second. Registry validation rejects key collisions between them.
    def modal_key(k)
      return archive_confirm_key(k) if @ui.modal&.kind == :archive_confirm
      return archive_blocked_key(k) if @ui.modal&.kind == :archive_blocked
      return if dispatch_action(k, :modal)
      dispatch_action(k, :detail) if detail_modal?
    end

    def prompt_key(k)
      case k
      when "\e"           then @ui.mode = :list
      when "\t"           then @ui.mode = :list
      when "\r", "\n"     then submit_prompt
      else
        @input.handle_key(k)
      end
    end

    def form_key(k)
      case @ui.form&.handle_key(k)
      when :cancelled then close_form
      when :submitted then close_form(success: true)
      end
    end

    def restore_form
      target_missing = @ui.form.target_id && current_item&.id != @ui.form.target_id
      if target_missing || (@ui.form.return_mode == :modal && !@ui.modal)
        @ui.form = nil
        @ui.form_success = nil
      else
        @ui.mode = :form
      end
    end

    def palette_key(k)
      palette = @ui.action_palette
      entry = nil
      result = palette&.handle_key(k)
      return close_action_palette if result == :cancelled
      return unless result.is_a?(Array) && result.first == :execute

      entry = result.last
      close_action_palette
      method(entry.handler).call
    rescue StandardError => e
      label = entry ? entry.description : "action palette"
      restore_action_palette(palette, error: "#{label} failed: #{e.message}")
    end

    # -- shortcut actions (dispatched from Shortcuts::REGISTRY) ----------------

    def action_available? = true
    def modal_filter_available? = @ui.modal&.filterable?
    def selected_action_available? = !current_item.nil?
    def recurrence_action_available?
      item = current_item
      !!(item && (item.scheduled || item.deadline))
    end
    def link_action_available?
      item = current_item
      !!(item && !@store.links(item).empty?)
    end

    def select_prev    = move(-1)
    def select_next    = move(1)
    def prev_view      = cycle_view(-1)
    def next_view      = cycle_view(1)
    def jump_view(k)   = switch_view(k.to_i)
    def focus_prompt   = @ui.mode = :prompt
    def resp_up        = scroll_resp(-5)
    def resp_down      = scroll_resp(5)
    def quit           = @quit = true

    def open_action_palette
      context = detail_modal? ? :detail : :list
      @ui.action_palette = ActionPalette.new(
        entries: Shortcuts.palette_entries(context, self),
        return_mode: @ui.modal ? :modal : :list,
        target_id: current_item&.id
      )
      @ui.mode = :palette
    end

    def close_action_palette
      return unless @ui.action_palette

      destination = @ui.action_palette.return_mode == :modal && !@ui.modal ? :list : @ui.action_palette.return_mode
      @ui.mode = destination
      @ui.action_palette = nil
    end

    def restore_action_palette(palette, error: nil)
      unless palette
        @ui.action_palette = nil
        return flash(error) if error
        return
      end

      target_missing = palette.target_id && current_item&.id != palette.target_id
      if target_missing || (palette.return_mode == :modal && !@ui.modal)
        # Detail-context commands must never survive the disappearance of
        # the task they were opened for and act on the fallback selection.
        @ui.action_palette = nil
        flash(error) if error
      else
        @ui.action_palette = palette
        @ui.mode = :palette
        @ui.action_palette.fail!(error) if error
      end
    end

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
        reselect(item.id)
        show_detail if detail_modal?
      else
        reload_store
        flash("file changed underneath — try again")
      end
    end

    # After a mutation or reload, views may re-sort and physical lines may move.
    # Follow the task by its durable id; rows and line numbers are coordinates.
    def reselect(id)
      @ui.selected_id = id
      rows
    end

    # Z reveals/hides deferred (someday/maybe) tasks across every view.
    def toggle_deferred_view
      modaled_id = @ui.detail_item_id if detail_modal?
      @ui.toggle_deferred!
      rows
      # If a detail modal was open on a task the toggle just hid, close it
      # rather than silently rebinding the modal to a neighboring task.
      refresh_detail_modal(modaled_id) if modaled_id
      flash(@ui.show_deferred ? "showing deferred tasks" : "hiding deferred tasks")
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
        if to_deferred && !@ui.show_deferred
          close_modal if @ui.modal
          rows
          clamp_selection
        else
          reselect(item.id)
          refresh_detail_modal(item.id)
        end
      else
        reload_store
        flash("file changed underneath — try again")
      end
    end

    def start_filter
      @ui.filter_input.replace(@ui.filter || +"") # `/` with a filter active edits it
      @ui.mode = :filter
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
        show_detail if detail_modal?
      end
    end

    def paste_ref
      item = current_item
      return flash("nothing selected") unless item
      close_modal if @ui.modal
      @input << " " unless @input.empty? || @input.end_with?(" ")
      @input << "\"#{Export.reference(item)}\" "
      @ui.mode = :prompt
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
      width = terminal_size.last
      # Same rule as the Projects view: the nearest open ancestor headline,
      # closed task ancestors skipped (Node#open_project).
      project = @store.node_for(item)&.open_project&.title
      open_modal(Modals.detail(item, @store.body(item), width, links: @store.links(item), project: project),
                 kind: :detail, item_id: item.id)
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
    # cursor already followed to `id`): redraw the modal on the task if it's
    # still in view, or close it if the change dropped it (e.g. dating an INBOX
    # item promotes it out of the inbox view).
    def refresh_detail_modal(id)
      return unless detail_modal?
      return close_modal unless @ui.detail_item_id == id

      reselect(id)
      detail_modal? && current_item&.id == id ? show_detail : close_modal
    end

    # -- modal -----------------------------------------------------------------

    def modal_up   = modal_move(-1)
    def modal_down = modal_move(1)
    def modal_half_up   = @ui.modal.scroll_half(-1, modal_body_h)
    def modal_half_down = @ui.modal.scroll_half(1, modal_body_h)
    def modal_page_up   = @ui.modal.scroll_page(-1, modal_body_h)
    def modal_page_down = @ui.modal.scroll_page(1, modal_body_h)

    # In a task detail modal, ↑↓/j/k walk the task list and the modal follows
    # the selection. Other modals (help) keep them as line scroll.
    def modal_move(delta)
      return @ui.modal.scroll_line(delta, modal_body_h) unless detail_modal?
      move(delta)
      show_detail
    end

    # Body rows available to the modal box — the same budget paint hands
    # Frame.build, so scroll steps match what's on screen.
    def modal_body_h(height: nil, width: nil)
      terminal_height, terminal_width = terminal_size if height.nil? || width.nil?
      height ||= terminal_height
      width ||= terminal_width
      screen_layout(width: width, height: height).body_height
    end

    def detail_modal? = @ui.modal&.kind == :detail

    def open_modal(content, kind:, item_id: nil)
      @ui.modal = Modal.new(title: content[:title], lines: content[:lines],
                            kind: kind, filterable: kind == :help)
      @ui.detail_item_id = item_id
      @ui.modal_filter_input.clear
      @ui.mode = :modal
    end

    def close_modal
      @ui.mode = :list
      @ui.modal = nil
      @ui.detail_item_id = nil
      @ui.archive_preview = nil
      @ui.modal_filter_input.clear
    end

    # `/` inside a filterable modal (the shortcuts overlay): live line filter.
    def modal_start_filter
      return unless @ui.modal.filterable?
      @ui.modal_filter_input.replace(@ui.modal.filter || +"")
      @ui.mode = :modal_filter
    end

    def modal_filter_key(k)
      case k
      when "\e"       then @ui.modal.filter = nil; @ui.modal_filter_input.clear; @ui.mode = :modal
      when "\r", "\n" then @ui.mode = :modal # the filter applied live; enter keeps it
      else
        @ui.modal.filter = @ui.modal_filter_input.to_s if @ui.modal_filter_input.handle_key(k) == :changed
      end
    end

    # -- actions ---------------------------------------------------------------

    def selectable_indexes = @rows.each_index.select { |i| @rows[i].item }

    def current_item = @rows[@sel]&.item

    def select_row(index)
      @sel = index
      @ui.selected_id = @rows[@sel]&.item&.id
    end

    def move(delta)
      sels = selectable_indexes
      return if sels.empty?
      cur = sels.index(@sel) || 0
      select_row(sels[(cur + delta).clamp(0, sels.size - 1)])
    end

    def clamp_selection
      sync_selection
    end

    # Reconcile stable identity with the current rendered rows. If an id is no
    # longer visible, land on the selectable row nearest the prior coordinate.
    # A detail modal bound to the missing id closes rather than following that
    # fallback selection.
    def sync_selection
      sels = selectable_indexes
      if sels.empty?
        @sel = 0
        @ui.selected_id = nil
        close_modal if detail_modal?
        return
      end

      # A task with multiple contexts can appear more than once in the Next
      # view. Keep the current occurrence when it still represents the id;
      # otherwise choose the first visible occurrence deterministically.
      idx = @sel if @ui.selected_id && sels.include?(@sel) && @rows[@sel].item&.id == @ui.selected_id
      idx ||= @ui.selected_id && sels.find { |i| @rows[i].item&.id == @ui.selected_id }
      idx ||= sels.min_by { |i| [(i - @sel).abs, i] }
      select_row(idx)
      close_modal if detail_modal? && @ui.detail_item_id != @ui.selected_id
    end

    def switch_view(n)
      @ui.view = Views::TABS[n - 1].last
      rows
    end

    def cycle_view(delta)
      keys = Views::TABS.map(&:last)
      switch_view(((keys.index(@ui.view) + delta) % keys.size) + 1)
    end

    # -- outliner collapse / expand (h l H L) ----------------------------------
    #
    # The tree rows carry their Tasks::Tree node (nil for headers, blanks, and
    # every flat/filter-mode row), so hierarchy questions read straight off the
    # selection. UiState#collapsed is a Set of task ids; Views prunes a collapsed id's
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
      if Views.visible_children(node, @ui.show_deferred).any? && item.id && !@ui.collapsed.include?(item.id)
        @ui.collapsed.add(item.id)
        reselect(item.id)
      else
        jump_to_parent(node)
      end
    end

    # l: unfold the selected node if it's folded; otherwise nothing to do.
    def expand_selected
      node = @rows[@sel]&.node
      id = node&.item&.id
      return unless id && @ui.collapsed.include?(id)
      @ui.collapsed.delete(id)
      reselect(node.item.id)
    end

    # H: fold every task node that has task children, across the whole tree
    # (works regardless of filter mode — the ids just wait, hidden, until the
    # filter clears). The selection may have been on a now-hidden row, so clamp.
    def collapse_all
      @store.tree.each do |root|
        root.each do |n|
          @ui.collapsed.add(n.item.id) if n.task? && n.item.id && n.children.any?(&:task?)
        end
      end
      rows
    end

    # L: unfold everything.
    def expand_all
      @ui.collapsed.clear
      rows
    end

    # Move the cursor to the row of `node`'s parent task. A section (or missing)
    # parent means we're already at the top of a subtree — leave the cursor put.
    def jump_to_parent(node)
      parent = node.parent
      return unless parent&.task? && parent.item
      idx = @rows.each_index.find { |i| @rows[i].item&.id == parent.item.id }
      select_row(idx) if idx
    end

    def save_session
      live_ids = @store.items.map(&:id).compact
      Session.save(@ui.session_hash(live_ids: live_ids))
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
          fresh = @store.items.find { |i| i.id == item.id }
          d = fresh && (fresh.deadline || fresh.scheduled)
          flash("↻ #{item.title}#{d ? " → #{d.iso8601} (#{d.strftime("%a")})" : ""}")
          reselect(item.id)
          refresh_detail_modal(item.id)
        else
          # complete! returns every touched line; a parent cascade closes its
          # open descendants too — note how many rode along.
          n = result.is_a?(Array) ? result.size - 1 : 0
          subs = n > 0 ? " (+#{n} subtask#{"s" unless n == 1})" : ""
          flash("✓ DONE: #{item.title}#{subs} — x to archive")
          close_modal if @ui.modal # the task just left the open view behind it
          rows
        end
      else
        reload_store
        flash("file changed underneath — try again")
      end
    end

    def open_date_popup
      item = current_item
      return flash("nothing selected") unless item

      target = item.deadline ? "deadline" : item.scheduled ? "scheduled" : "deadline (new)"
      @ui.form = Form.new(
        kind: :date, title: "reschedule", prompt: "new #{target}",
        hint: "fri · +3 · 07-15 · esc cancels", min_width: 36,
        return_mode: @ui.modal ? :modal : :list, target_id: item.id
      ) do |raw|
        date = Dates.parse_when(raw)
        next "can't parse “#{raw}”" unless date
        unless @store.reschedule!(item, date)
          reload_store
          next "file changed underneath — reopen"
        end

        @ui.form_success = lambda do
          promoted = item.state == "INBOX" ? " · INBOX → TODO" : ""
          flash("→ #{item.title}: #{date.iso8601} (#{date.strftime("%a")})#{promoted}")
          reselect(item.id)
          refresh_detail_modal(item.id)
        end
        nil
      end
      @ui.mode = :form
    end

    # r opens the recurrence popup on the selected task, pre-filled with its
    # current cookie. Recurrence rides a date stamp, so a task with no date
    # can't repeat — flash and refuse rather than open a popup that must fail.
    def open_recur_popup
      item = current_item
      return flash("nothing selected") unless item
      return flash("schedule it first — recurrence needs a date") unless item.scheduled || item.deadline

      current = item.recur ? "now #{item.recur}" : "not repeating"
      @ui.form = Form.new(
        kind: :recurrence, title: "recur", prompt: "every",
        hint: "weekly · 2w · .+1m · off · esc cancels", min_width: 40,
        return_mode: @ui.modal ? :modal : :list,
        initial: item.recur || +"", suffix: "(#{current})", target_id: item.id
      ) do |raw|
        cookie = Tasks::Recur.parse_interval(raw)
        next "can't parse “#{raw}”" if cookie.nil?
        unless @store.set_recur!(item, cookie)
          reload_store
          next "file changed underneath — reopen"
        end

        @ui.form_success = lambda do
          flash(cookie == :off ? "↻ off: #{item.title}" : "↻ #{cookie}: #{item.title}")
          reselect(item.id)
          refresh_detail_modal(item.id)
        end
        nil
      end
      @ui.mode = :form
    end

    def close_form(success: false)
      return unless @ui.form

      return_mode = @ui.form.return_mode
      callback = success ? @ui.form_success : nil
      destination = return_mode == :modal && !@ui.modal ? :list : return_mode
      @ui.mode = destination
      @ui.form = nil
      @ui.form_success = nil
      callback&.call
    end

    def archive_sweep
      preview = @store.archive_preview
      if preview.roots.zero?
        return flash("archive preview: 0 roots · 0 descendants — nothing to archive")
      end

      noun = preview.descendants == 1 ? "descendant" : "descendants"
      lines = [
        "Would move #{preview.roots} completed root#{preview.roots == 1 ? "" : "s"} " \
          "and #{preview.descendants} #{noun} to archive.jsonl.",
      ]
      if preview.blocked?
        lines << ""
        lines << T.paint(:error,
          "Cannot archive: #{preview.blocked_roots} closed root#{preview.blocked_roots == 1 ? " has" : "s have"} " \
          "#{preview.open_descendants} open descendant#{preview.open_descendants == 1 ? "" : "s"}.")
        preview.blocks.each do |block|
          lines << "  #{block.root_title}: #{block.open_titles.join(", ")}"
        end
        lines << T.paint(:muted, "Complete, cancel, move, or unnest that work first. esc closes")
        open_modal({ title: "Archive blocked", lines: lines }, kind: :archive_blocked)
      else
        lines << ""
        lines << T.paint(:muted, "Press y to archive · n / esc cancels")
        @ui.archive_preview = preview
        open_modal({ title: "Confirm archive", lines: lines }, kind: :archive_confirm)
      end
    end

    def archive_confirm_key(k)
      case k
      when "y", "Y"
        expected = @ui.archive_preview
        result = @store.archive_swept!(expected_preview: expected)
        close_modal
        if result.is_a?(Tasks::Store::ArchiveRefusal)
          case result.reason
          when :preview_changed
            flash("task list changed — press x to review the updated archive preview")
          when :archive_conflict
            flash("archive conflict — live tasks preserved; run tasks archive for details")
          else
            flash("archive refused — open descendants remain; press x for details")
          end
        else
          flash(result.zero? ? "nothing to archive" : "archived #{result} root#{result == 1 ? "" : "s"}")
        end
      when "n", "N", "\e", "q"
        close_modal
        flash("archive cancelled")
      end
    end

    def archive_blocked_key(k)
      close_modal if ["n", "N", "\e", "q", "\r", "\n"].include?(k)
    end

    # -- claude ----------------------------------------------------------------

    def submit_prompt
      text = @input.strip
      @input.clear
      @ui.mode = :list
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
      width = terminal_size.last
      @resp = A.wrap(@agent.output.strip, width - 8)
      @resp = [T.paint(:muted, "(no output)")] if @resp.all? { |l| l.strip.empty? }
      @resp_open = true
      @resp_scroll = 0
      reload_store if @store.changed?
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
      elsif @ui.filter
        @ui.filter = nil
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

Tui::Shortcuts.validate!(Tui::App)
