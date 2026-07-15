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
require_relative "agent_queue"
require_relative "agent_activity"
require_relative "shortcuts"
require_relative "modal"
require_relative "modals"
require_relative "right_panel"
require_relative "task_details"
require_relative "clipboard"
require_relative "export"
require_relative "session"
require_relative "text_input"
require_relative "form"
require_relative "action_palette"
require_relative "context_palette"
require_relative "ui_state"
require_relative "screen_layout"
require_relative "form_renderer"
require_relative "task_editor_session"
require_relative "../tasks/config"
require_relative "../tasks/agent_context"
require_relative "../tasks/application"
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
    ESCAPE_WAIT = 0.01 # distinguish a lone Escape from a split CSI sequence
    ORDERING_HANDLERS = %i[
      move_subtree_up move_subtree_down indent_subtree outdent_subtree
    ].freeze
    ATOMIC_ALT_SEQUENCES = Shortcuts::REGISTRY.flat_map(&:sequences).select do |sequence|
      sequence.match?(/\A\e[^\e\[O]/)
    end.freeze

    # paths:      injectable so tests can pin a sandbox dir; defaults to the
    #             user's configured task files (env / ~/.config/tasks/config).
    # llm_config: the resolved LLM config (provider/model defaults + per-provider
    #             settings). Read once here and threaded through the switcher, so
    #             both the entry list and each rebuilt agent agree. Injectable so
    #             tests are hermetic instead of reading the developer's real config.
    # agent_factory: builds the real adapter (with fresh system context) when the
    #             queue starts a request. agent_probe: the lightweight
    #             availability check run at submit time. Both injectable so tests
    #             stay off the developer's real CLI/models.
    def initialize(root:, paths: Tasks::Config.resolve(default_dir: root),
                   llm_config: LLM::Config.load, agent_factory: nil, agent_probe: nil,
                   date_provider: -> { Date.today })
      Theme.configure!(name: paths.theme, overrides: paths.colors || {})
      @paths = paths
      # Store remains the long-lived watcher, history/archive, and form-option
      # source. TUI presentation reads and patch-style writes travel through
      # @application so adapters do not own task mutation semantics.
      @store  = Store.new(org: paths.org, archive: paths.archive,
                          links: paths.links || {}, link_systems: paths.link_systems || {},
                          max_depth: paths.max_depth)
      @application = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(
          org: paths.org, archive: paths.archive, links: paths.links || {},
          link_systems: paths.link_systems || {}, max_depth: paths.max_depth
        )
      )
      @read_model = nil
      @read_model_today = nil
      @date_provider = date_provider.respond_to?(:call) ? date_provider : -> { date_provider }
      @urgent_days = paths.urgent_days # deadline window for the quadrants view
      # The (provider, model) switcher cycles these. AgentQueue snapshots an
      # entry and builds one adapter per accepted request, so later cycling can
      # never retarget queued or running work.
      @agent_root = File.dirname(paths.org)
      @cli_root   = root # where bin/tasks + TASK_AGENT.md live (distinct from the data dir)
      @llm_config = llm_config
      @entries    = LLM.entries(llm_config)
      @entry_idx  = 0
      @agent_queue = AgentQueue.new(
        agent_factory: agent_factory || method(:build_agent),
        availability: agent_probe || method(:agent_available?)
      )
      @ui = UiState.restore(saved: Session.load, views: Views::TABS.map(&:last), default_view: :agenda)
      @sel    = 0
      @input  = TextInput.new # prompt buffer
      @input_bytes = +"".b
      @key_data = +""
      @resp   = nil        # wrapped response lines
      @resp_request_id = nil
      @resp_open = false
      @resp_scroll = 0
      @flash = nil
      @flash_until = nil
      @tick = 0
      @quit = false
      @paint_dirty = true # first frame must draw before any key arrives
      @rows = nil
      @rows_fingerprint = nil
      @row_item_count = 0
      @title_haystack = nil
      @title_haystack_model = nil
      @open_count = nil
      @open_count_model = nil
      @detail_panel_width = nil
      @detail_panel_model = nil
      @detail_panel_id = nil
      @last_paint_size = nil
      @task_edit_message = nil
      @suspended_task_editor = nil
      @suspended_task_panel = nil
      @draft_quit_editor = nil
      @draft_quit_return_modal = nil
      @draft_quit_return_mode = nil
      @draft_quit_return_message = nil
      @agent_quit_confirmation = false
      @agent_quit_return_modal = nil
      @agent_quit_return_mode = nil
      @agent_activity_width = nil
      @agent_activity_second = nil
    end

    # -- agent selection -----------------------------------------------------

    def current_entry = @entries[@entry_idx]

    # Called by the queue when it starts a request — never at submit time — so
    # every run gets context built from the memory sidecar as it stands right
    # now. A saved default from an earlier request, or an external edit, is thus
    # visible to the next queued request without restarting the TUI. A memory
    # error (oversize/unreadable) raises here and the queue reports it as a
    # failed request rather than crashing the event loop.
    def build_agent(entry)
      system = Tasks::AgentContext.build(paths: @paths, cli_root: @cli_root)
      LLM.build(entry, root: @agent_root, system: system, config: @llm_config)
    end

    # Lightweight availability probe used at submit time to reject an unavailable
    # provider immediately. Deliberately context-free: it never reads the memory
    # sidecar, so a submit can't fail on a memory error (that surfaces at start).
    def agent_available?(entry)
      LLM.build(entry, root: @agent_root, config: @llm_config).available?
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
      @agent_queue.shutdown if @agent_queue&.work?
      save_session # so the view persists however the TUI exits
    end

    private

    def loop_once
      @tick += 1
      @paint_dirty = true if clear_flash_if_expired
      @paint_dirty = true if idle_layout_changed?
      paint_if_needed

      ios = [$stdin]
      ios << @agent_queue.io if @agent_queue.io
      ready = IO.select(ios, nil, nil, TICK)
      dirty = false
      (ready&.first || []).each do |io|
        io == $stdin ? read_keys : pump_agent_queue
        dirty = true
      end
      if external_change? # picks up Claude edits + external edits
        reload_store
        res = Tasks::Check.check(@paths.org)
        flash(T.paint(:error, "⚠ tasks.jsonl: #{res.errors.size} format error(s) — run `tasks check`")) unless res.ok?
        dirty = true
      end
      clamp_selection
      @paint_dirty = true if dirty
    end

    # Idle ticks still poll the file watch, but skip a full redraw unless
    # something changed or the footer/spinner is animating.
    def paint_if_needed
      return unless @paint_dirty || animated_paint?

      paint
      @paint_dirty = false
    end

    def animated_paint?
      !@agent_queue.active_request.nil? ||
        (@ui.modal&.kind == :agent_activity && @agent_queue.active?)
    end

    # Cheap idle checks that used to fall out of painting every tick: terminal
    # resize and local-date rollover (agenda/availability depend on "today").
    def idle_layout_changed?
      height, width = terminal_size
      size = [height, width]
      changed = @last_paint_size && size != @last_paint_size
      changed ||= !@read_model_today.nil? && current_date != @read_model_today
      changed
    end

    # Reload external writes without losing the selected task to a new physical
    # row. An open detail panel follows whichever task selection remains visible.
    def reload_store
      overlay_mode = @ui.mode if %i[form palette context_palette task_edit].include?(@ui.mode)
      @store.reload!
      reload_read_model
      editor = @ui.task_editor || @suspended_task_editor
      edit_outcome = editor&.refresh
      rows
      if overlay_mode == :task_edit
        @task_edit_message = task_edit_outcome_message(edit_outcome)
        if edit_outcome&.missing?
          @task_edit_message = "#{@task_edit_message} · y copies field · esc discards editor"
          flash(@task_edit_message)
        end
      elsif @suspended_task_editor
        reconcile_suspended_editor(edit_outcome)
      else
        refresh_detail_panel if detail_panel?
      end
      restore_form if overlay_mode == :form && @ui.form
      if overlay_mode == :palette && @ui.action_palette
        restore_action_palette(@ui.action_palette)
      end
      if overlay_mode == :context_palette && @ui.context_palette
        restore_context_palette(@ui.context_palette)
      end
    end

    # -- painting ------------------------------------------------------------

    def paint
      height, width = terminal_size
      @last_paint_size = [height, width]
      # Row builders are memoized via rows_fingerprint; the modal path still
      # prefers an already-warmed @rows so filter typing never rebuilds the
      # frozen list underneath the box.
      frame_rows = @ui.modal ? (@rows || rows) : rows
      visual_selection = @ui.mode == :prompt ? nil : @sel
      layout = screen_layout(width: width, height: height, selected: visual_selection,
                             panel: @ui.panel)
      if task_editing? && !layout.editable_panel?
        suspend_task_edit_for_layout(layout)
        visual_selection = @sel
        layout = screen_layout(width: width, height: height, selected: visual_selection,
                               panel: @ui.panel)
      elsif task_editing?
        refresh_task_edit_panel(layout: layout)
      end
      refresh_detail_panel(content_width: layout.panel_content_width) if detail_panel?
      lines = Frame.build(
        width: width, height: height,
        header: header(width - 2),
        rows: frame_rows,
        selected: visual_selection,
        footer: layout.footer,
        popup: current_popup(layout: layout),
        panel: @ui.panel&.view(height: layout.body_height, width: layout.panel_content_width),
        modal: layout.place_modal(modal_view(layout.body_height, width: width)),
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

    # The mutation Store is intentionally long-lived for the editor and shared
    # journal operations. Every presentation read instead comes from this one
    # immutable application result, refreshed after a known or observed write.
    def read_model
      day = current_date
      if @read_model.nil? || @read_model_today != day
        @read_model = @application.read_tasks(today: day)
        @read_model_today = day
      end
      @read_model
    end

    # @store.changed? alone is not a safe reload gate: an editor-session read
    # (store.items during a cascade confirmation, an archive preview) lets the
    # mutation Store self-reload and consume the mtime signal, stranding the
    # rendered read model on pre-write data forever. Ask the read model too —
    # it knows which file state it was built from.
    def external_change?
      @store.changed? || (@read_model ? @read_model.stale?(@paths.org) : false)
    end

    def reload_read_model
      @read_model_today = current_date
      @read_model = @application.read_tasks(today: @read_model_today)
      clear_row_caches
    end

    def invalidate_read_model
      @read_model = nil
      @read_model_today = nil
      clear_row_caches
    end

    def clear_row_caches
      @rows = nil
      @rows_fingerprint = nil
      @row_item_count = 0
      @title_haystack = nil
      @title_haystack_model = nil
      @open_count = nil
      @open_count_model = nil
      @detail_panel_width = nil
      @detail_panel_model = nil
      @detail_panel_id = nil
    end

    def current_date = @date_provider.call

    def rows(read: nil, today: nil)
      read ||= read_model
      today ||= @read_model_today
      fingerprint = rows_fingerprint(read, today)
      if @rows && @rows_fingerprint == fingerprint
        sync_selection
        return @rows
      end

      items = read.items
      if (ctx = active_context_filter)
        items = items.select { |i| i.contexts.include?(ctx) }
      end
      if (q = active_filter)
        q = q.downcase
        hay = title_haystack(read)
        items = items.select { |i| (hay[i.id] || i.title.downcase).include?(q) }
      end
      if active_context_filter || active_filter
        @rows = Views.rows(@ui.view, items, show_deferred: @ui.show_deferred,
                                           today: today,
                                           urgent_days: @urgent_days, reader: read)
      else
        @rows = Views.rows(@ui.view, items, tree: read.tree, collapsed: @ui.collapsed,
                                         show_deferred: @ui.show_deferred, today: today,
                                         urgent_days: @urgent_days,
                                         reader: read)
      end
      @rows_fingerprint = fingerprint
      @row_item_count = @rows.count(&:item)
      sync_selection
      @rows
    end

    # Inputs that change what Views.rows would emit. Selection is intentionally
    # excluded — j/k reuses the painted row list and only moves the highlight.
    def rows_fingerprint(read, today)
      [
        read.object_id,
        today,
        @ui.view,
        @ui.show_deferred,
        @urgent_days,
        active_filter,
        active_context_filter,
        @ui.collapsed.hash,
      ]
    end

    # Downcased titles keyed by task id, rebuilt once per read-model identity —
    # same idea as Modal#haystack so `/` typing is substring scans, not
    # title.downcase across the whole list on every keystroke.
    def title_haystack(read)
      return @title_haystack if @title_haystack_model.equal?(read) && @title_haystack

      @title_haystack_model = read
      @title_haystack = read.items.to_h { |item| [item.id, item.title.downcase] }
    end

    def open_task_count(read)
      return @open_count if @open_count_model.equal?(read) && !@open_count.nil?

      @open_count_model = read
      @open_count = read.tasks.count { |task| task.open? && task.available? }
    end

    # The filter narrowing the views right now: the live buffer while
    # typing, the committed filter otherwise.
    def active_filter
      s = @ui.mode == :filter ? @ui.filter_input : @ui.filter
      s = s.to_s unless s.nil?
      s.nil? || s.strip.empty? ? nil : s
    end

    def active_context_filter
      ctx = @ui.context_filter
      return nil if ctx.nil?

      ContextPalette.normalize(ctx)
    end

    def header(w)
      tabs = Views::TABS.map do |label, key|
        slot = key == @ui.view ? :"tab_#{key}_active" : :"tab_#{key}"
        slot = key == @ui.view ? :tab_active : :tab_inactive unless T.slot?(slot)
        T.paint(slot, " #{label} ")
      end.join(" ")
      open_n = open_task_count(read_model)
      unavailable_note = @ui.show_deferred ? "#{T.paint(:warning, "unavailable shown")}#{T.paint(:muted, " · ")}" : ""
      count = "#{T.paint(:muted, "#{open_n} open · ")}#{unavailable_note}#{T.paint(:accent, current_entry.to_s)}#{T.paint(:muted, " · ? help")}"
      gap = [w - A.vislen(tabs) - A.vislen(count) - 2, 1].max
      " #{tabs}#{" " * gap}#{count} "
    end

    def footer(w, mode: @ui.mode)
      f = []
      if (active = @agent_queue.active_request)
        pending = @agent_queue.pending_count
        queued = pending.positive? ? " · #{pending} queued" : ""
        f << T.paint(
          :muted,
          " #{SPINNER[@tick % SPINNER.size]} ##{active.id} #{active.entry} is working#{queued} · A activity · esc cancels"
        )
        # scrub: a streaming chunk can end mid-multibyte-char
        A.strip(@agent_queue.active_output.scrub("�")).split("\n").last(3).each do |line|
          f << T.paint(:muted, "   #{line}")
        end
        f << :rule
      elsif @resp_open && @resp
        f << T.paint(
          :muted,
          " result ##{@resp_request_id} of #{@agent_queue.submitted_count} · A opens all agent activity"
        )
        visible = @resp[@resp_scroll, RESP_MAX] || []
        visible.each { |l| f << "   #{l}" }
        scroll_hint = @resp.size > RESP_MAX ? "#{@resp_scroll + visible.size}/#{@resp.size} · #{RESP_HINT}" : "esc dismiss"
        f << T.paint(:muted, "   ── #{scroll_hint} ──")
        f << :rule
      end
      f << " #{@flash}" if @flash
      if mode == :filter
        f << " #{T.paint(:prompt, "/ ")}#{inline_input(@ui.filter_input)}#{T.paint(:muted, "  enter keeps · esc clears")}"
      elsif @ui.filter
        n = @row_item_count
        f << T.paint(:muted, " / #{@ui.filter} · #{n} match#{n == 1 ? "" : "es"} · esc clears · / edits")
      end
      if @ui.context_filter && mode != :context_palette
        n = @row_item_count
        f << T.paint(:muted, " #{@ui.context_filter} · #{n} match#{n == 1 ? "" : "es"} · esc clears · @ changes")
      end
      # Active text entry owns the scarce footer row on short terminals. Forms,
      # palettes, and the modal filter render their input in their own overlay;
      # the task-list filter renders it here. Keeping :modal_filter's footer
      # identical to :modal's also pins the body height across the two modes, so
      # opening the filter can't jog the modal box.
      f.concat(prompt_lines(w)) unless %i[filter form palette context_palette task_edit].include?(mode)
      f
    end

    # The prompt grows to PROMPT_MAX lines as the input wraps, so a wordy
    # request stays readable; beyond that, the earliest lines scroll off.
    def prompt_lines(w)
      unless @ui.mode == :prompt
        hint_text = if @agent_queue.work?
                      suffix = @agent_queue.pending_count.positive? ? " · #{@agent_queue.pending_count} queued" : ""
                      "tab to ask the agent#{suffix}"
                    else
                      "tab to ask the agent — reschedule, capture, edit anything…"
                    end
        hint = T.paint(:muted, hint_text)
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

    # The active popup layered over the list and any persistent right panel.
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
      when :context_palette
        [@ui.context_palette&.popup(row: 0, col: 0, max_width: layout.body_width, max_height: layout.body_height,
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

    def screen_layout(width:, height:, footer_size: nil, selected: @sel, panel: @ui.panel,
                      panel_offset: @ui.panel_offset, editing: task_editing?)
      footer_mode = editing ? :task_edit : @ui.mode
      raw_footer = footer_size ? Array.new(footer_size, "") : footer(width - 2, mode: footer_mode)
      ScreenLayout.new(width: width, height: height, footer: raw_footer, selected: selected,
                       panel: !panel.nil?, panel_mode: @ui.panel_mode,
                       panel_offset: panel_offset, editing: editing)
    end

    # -- input ---------------------------------------------------------------

    def read_keys
      return unless read_key_chunk

      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + ESCAPE_WAIT
      loop do
        drain_key_data(flush_incomplete_escape: false)
        break unless incomplete_escape_sequence?

        remaining = deadline - Process.clock_gettime(Process::CLOCK_MONOTONIC)
        break unless remaining.positive? && IO.select([$stdin], nil, nil, remaining)
        break unless read_key_chunk
      end

      # A still-lone Escape has now had the minimum disambiguation window. CSI
      # prefixes longer than one byte remain buffered for the next readable
      # chunk instead of becoming Escape plus literal suffix text.
      drain_key_data if @key_data == "\e"
    end

    def read_key_chunk
      bytes = $stdin.read_nonblock(4096)
      @input_bytes << bytes
      @key_data << drain_utf8_input
      true
    rescue IO::WaitReadable, EOFError
      false
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

    def drain_key_data(flush_incomplete_escape: true)
      until @key_data.empty?
        if @key_data.start_with?(PASTE_START)
          end_at = @key_data.index(PASTE_END, PASTE_START.length)
          break unless end_at

          handle_paste(@key_data[PASTE_START.length...end_at])
          @key_data = @key_data[(end_at + PASTE_END.length)..] || +""
        elsif @key_data.length > 1 && PASTE_START.start_with?(@key_data)
          break
        elsif @key_data.start_with?("\e")
          break if !flush_incomplete_escape && incomplete_escape_sequence?

          seq = @key_data[/\A\e\e\[[0-9;?]*[A-Za-z~]/] ||
                @key_data[/\A\e\eO[A-Za-z]/] ||
                @key_data[/\A\e\[[0-9;?]*[A-Za-z~]/] ||
                @key_data[/\A\eO[A-Za-z]/] ||
                ATOMIC_ALT_SEQUENCES.find { |candidate| @key_data.start_with?(candidate) }
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

    def incomplete_escape_sequence?
      @key_data == "\e" ||
        @key_data.match?(/\A\e\[[0-9;?]*\z/) ||
        @key_data == "\eO" ||
        @key_data == "\e\e" ||
        @key_data.match?(/\A\e\e\[[0-9;?]*\z/) ||
        @key_data == "\e\eO"
    end

    def handle_paste(text)
      case @ui.mode
      when :prompt then @input.insert(text)
      when :form   then @ui.form&.paste(text)
      when :task_edit then process_task_edit_outcome(@ui.task_editor&.handle(TermForm::Event.paste(text)))
      when :palette then @ui.action_palette&.paste(text)
      when :context_palette then @ui.context_palette&.paste(text)
      when :filter then @ui.filter_input.insert(text)
      when :modal_filter then @ui.modal_filter_input.insert(text); @ui.modal.filter = @ui.modal_filter_input.to_s
      else
        close_modal if @ui.modal
        @input.insert(text)
        @ui.mode = :prompt
      end
    end

    def handle_key(k)
      return agent_quit_confirmation_key(k) if @agent_quit_confirmation
      return task_draft_quit_confirmation_key(k) if task_draft_quit_confirmation?
      return if dispatch_action(k, :global)
      return task_edit_key(k) if task_editing?

      case @ui.mode
      when :prompt then prompt_key(k)
      when :form   then form_key(k)
      when :palette then palette_key(k)
      when :context_palette then context_palette_key(k)
      when :modal  then modal_key(k)
      when :modal_filter then modal_filter_key(k)
      when :filter then filter_key(k)
      else
        if suspended_recovery_owns_input? && ["y", "\e", "\t"].include?(k)
          suspended_recovery_key(k)
        else
          list_key(k)
        end
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
      return if detail_panel? && dispatch_action(k, :detail)

      dispatch_action(k, :list)
    end

    def dispatch_action(k, context)
      entry = Shortcuts.match(k, context)
      return false unless entry
      unless Shortcuts.available?(entry, self)
        unavailable_action(entry)
        return true
      end

      m = method(entry.handler)
      m.arity.zero? ? m.call : m.call(k)
      true
    end

    # Modal navigation is reserved for blocking overlays such as help and
    # archive confirmation. Task details remain in list mode in the right panel.
    def modal_key(k)
      return archive_confirm_key(k) if @ui.modal&.kind == :archive_confirm
      return archive_blocked_key(k) if @ui.modal&.kind == :archive_blocked
      return cancel_queued_agent_requests_key(k) if @ui.modal&.kind == :agent_queue_cancel_confirm
      dispatch_action(k, :modal)
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

    def task_edit_key(k)
      return grow_task_panel if k == "\x0b"
      return shrink_task_panel if k == "\x0c"
      if @ui.task_editor&.missing?
        return copy_missing_editor_field if k == "y"
        if ["\e", TaskEditorSession::CTRL_O].include?(k)
          return close_task_edit(message: "Task no longer exists; local edit discarded")
        end
      end

      process_task_edit_outcome(@ui.task_editor&.handle(k))
    end

    # Registry hook used for generated task-edit help. Runtime dispatch sends
    # every editor-owned byte through task_edit_key before list/prompt handlers.
    def task_edit_input(k) = task_edit_key(k)

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

    def context_palette_key(k)
      palette = @ui.context_palette
      result = palette&.handle_key(k)
      return close_context_palette if result == :cancelled
      return unless result.is_a?(Array) && result.first == :apply

      apply_context_filter(result.last)
      close_context_palette
    end

    # -- shortcut actions (dispatched from Shortcuts::REGISTRY) ----------------

    def action_available? = true
    def modal_filter_available? = @ui.modal&.filterable?
    def panel_scroll_available? = detail_panel?
    def agent_activity_available? = @agent_queue.any?
    def pending_agent_requests_available? = @agent_queue.pending?
    def selected_action_available? = !current_item.nil?
    def ordering_action_available?
      @ui.view == :outline && !active_filter && !active_context_filter && !current_item.nil?
    end
    def recurrence_action_available?
      item = current_item
      !!(item && (item.scheduled || item.deadline))
    end
    def link_action_available?
      task = current_task
      !!(task && !task.links.empty?)
    end

    def select_prev    = move(-1)
    def select_next    = move(1)
    def prev_view      = cycle_view(-1)
    def next_view      = cycle_view(1)
    def jump_view(k)   = switch_view(k.to_i)
    def move_subtree_up = reorder_selected(:up)
    def move_subtree_down = reorder_selected(:down)
    def indent_subtree = reorder_selected(:indent)
    def outdent_subtree = reorder_selected(:outdent)
    def focus_prompt
      detail_panel? ? start_task_edit : @ui.mode = :prompt
    end
    def resp_up        = scroll_resp(-5)
    def resp_down      = scroll_resp(5)
    def quit
      editor = @ui.task_editor || @suspended_task_editor
      return show_task_draft_quit_confirmation(editor, editor.request_quit) if editor&.dirty?
      return show_agent_quit_confirmation if @agent_queue&.work?

      @quit = true
    end

    def open_action_palette
      entries = Shortcuts.palette_entries(:list, self)
      if detail_panel?
        detail_entries = Shortcuts.palette_entries(:detail, self)
        entries = (entries + detail_entries).uniq(&:handler)
      end
      @ui.action_palette = ActionPalette.new(
        entries: entries,
        return_mode: :list,
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

    def open_context_palette
      contexts = read_model.items.flat_map(&:contexts)
      @ui.context_palette = ContextPalette.new(
        contexts: contexts,
        current: @ui.context_filter
      )
      @ui.mode = :context_palette
    end

    def close_context_palette
      return unless @ui.context_palette

      @ui.mode = :list
      @ui.context_palette = nil
    end

    def restore_context_palette(palette)
      return unless palette

      contexts = read_model.items.flat_map(&:contexts)
      palette.refresh_options(contexts: contexts, current: @ui.context_filter)
      @ui.context_palette = palette
      @ui.mode = :context_palette
    end

    def apply_context_filter(option)
      previous = @ui.context_filter
      next_filter = option.id # nil clears
      @ui.context_filter = next_filter
      if next_filter.nil?
        flash(previous ? "context filter cleared" : "no context filter")
      else
        flash("context: #{next_filter}")
      end
      rows
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
      label = new_pri ? "priority [##{new_pri}]: #{item.title}" : "clear priority: #{item.title}"
      if patch_task(item, field: :priority, value: new_pri, label: label).ok?
        flash(new_pri ? "priority: [##{new_pri}] #{item.title}" : "priority cleared: #{item.title}")
        reselect(item.id)
        refresh_detail_panel if detail_panel?
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

    # TUI quick actions resolve their selected row to a stable id before
    # writing. A fresh edit snapshot supplies the field-owned optimistic
    # baseline, so a task can never be retargeted by an intervening line shift.
    # Keep this thin adapter in the interface layer: TaskEditorSession owns the
    # richer, save-on-blur workflow, while these keyboard actions retain their
    # established confirmations, messages, and undo labels.
    def patch_task(item, field:, value:, label:, today: current_date)
      snapshot = @application.edit_snapshot(item.id)
      return Tasks::MutationResult.new(status: :not_found) unless snapshot

      result = @application.patch_task(Tasks::TaskPatch.from(snapshot, field: field, value: value,
                                                              history_label: label), today: today)
      invalidate_read_model if result.ok?
      result
    end

    # Org-style ordering is a thin adapter over the shared placement command.
    # Parent and sibling relationships come from one immutable read model; the
    # Store resolves those stable ids again under its mutation lock.
    def reorder_selected(action)
      item = current_item
      return unavailable_ordering unless ordering_action_available?

      read = read_model
      task = read.task_for(item)
      return flash("task no longer exists — refresh and try again") unless task

      placement = ordering_placement(action, task, read)
      return unless placement

      snapshot = @application.edit_snapshot(item.id)
      return flash("task no longer exists — refresh and try again") unless snapshot

      label = "#{ordering_label(action)}: #{item.title}"
      command = Tasks::TaskChangeset.from(
        snapshot, changes: { location: placement }, history_label: label
      )
      operation_today = current_date
      result = @application.update_task(command, today: operation_today)
      unless result.ok?
        reload_store
        return flash(ordering_failure_message(result, action))
      end

      @read_model_today = operation_today
      @read_model = Tasks::TaskReadModel.new(result.read_snapshot, today: @read_model_today)
      @ui.collapsed.delete(placement.parent_id) if action == :indent
      @ui.selected_id = item.id
      rows(read: @read_model, today: @read_model_today)
      refresh_detail_panel if detail_panel?
      flash(result.no_change? ? "already in that position: #{item.title}" : "#{ordering_label(action)}: #{item.title}")
    end

    def ordering_placement(action, task, read)
      siblings = read.tasks.select { |candidate| candidate.parent_id == task.parent_id }
      index = siblings.index { |candidate| candidate.id == task.id }
      return ordering_notice("task placement changed — refresh and try again") unless index

      case action
      when :up
        return ordering_notice("already first among siblings") if index.zero?
        Tasks::TaskPlacement.new(parent_id: task.parent_id, before_id: siblings[index - 1].id)
      when :down
        return ordering_notice("already last among siblings") if index == siblings.length - 1
        Tasks::TaskPlacement.new(parent_id: task.parent_id, before_id: siblings[index + 2]&.id)
      when :indent
        return ordering_notice("can't indent without a preceding sibling") if index.zero?
        Tasks::TaskPlacement.new(parent_id: siblings[index - 1].id)
      when :outdent
        parent = read.task_for(task.parent_id)
        return ordering_notice("already at section level") unless parent

        parent_siblings = read.tasks.select { |candidate| candidate.parent_id == parent.parent_id }
        parent_index = parent_siblings.index { |candidate| candidate.id == parent.id }
        return ordering_notice("parent placement changed — refresh and try again") unless parent_index
        Tasks::TaskPlacement.new(
          parent_id: parent.parent_id, before_id: parent_siblings[parent_index + 1]&.id
        )
      else
        raise ArgumentError, "unknown ordering action #{action.inspect}"
      end
    end

    def ordering_notice(message)
      flash(message)
      nil
    end

    def ordering_label(action)
      { up: "move up", down: "move down", indent: "indent", outdent: "outdent" }.fetch(action)
    end

    def ordering_failure_message(result, action)
      case result.status
      when :not_found then "task or placement anchor no longer exists — refresh and try again"
      when :stale then "task changed underneath — try again"
      when :conflict then "placement anchor moved underneath — try again"
      when :cycle then "can't move a task into its own subtree"
      when :too_deep then action == :indent ? "can't indent — maximum task depth reached" : "move exceeds maximum task depth"
      when :invalid then result.errors.first || "invalid task placement"
      else result.tui_message
      end
    end

    def unavailable_action(entry)
      unavailable_ordering if ORDERING_HANDLERS.include?(entry.handler)
    end

    def unavailable_ordering
      flash("ordering requires the unfiltered Outline tab")
    end

    def start_task_edit = enter_task_edit(:title)
    def start_task_edit_last = enter_task_edit(TaskEditForm::FIELD_ORDER.last)

    def enter_task_edit(focus)
      item = current_item
      return flash("nothing selected") unless item

      height, width = terminal_size
      layout = screen_layout(width: width, height: height, panel: true, editing: true)
      unless layout.editable_panel?
        required_height, required_width = ScreenLayout.minimum_edit_terminal_size(
          footer_rows: layout.footer_size
        )
        return flash("task editing needs at least #{required_width}×#{required_height} terminal cells")
      end

      if @suspended_task_editor&.target_id != item.id && @suspended_task_editor&.dirty?
        if @suspended_task_editor.missing?
          return flash("deleted task draft remains — y copies the field · esc discards it")
        end
        return flash("unsaved task draft belongs to another row — reselect it to resume")
      end
      resumed = @suspended_task_editor&.target_id == item.id
      editor = if resumed
                 @suspended_task_editor
               else
                 TaskEditorSession.new(store: @store, application: @application,
                                       target_id: item.id, focus: focus,
                                       today: method(:current_date))
               end
      return flash("task no longer exists") if editor.missing?

      panel = @suspended_task_panel if resumed
      @suspended_task_editor = nil
      @suspended_task_panel = nil
      @ui.task_editor = editor
      @ui.panel = panel || RightPanel.new(title: "task · editing", lines: [], kind: :task_edit,
                                          identity: editor.target_id)
      @task_edit_message = nil unless resumed
      @ui.mode = :task_edit
      refresh_task_edit_panel(layout: layout)
      flash(@task_edit_message) if resumed && @task_edit_message
    end

    def grow_task_panel = resize_task_panel(1)
    def shrink_task_panel = resize_task_panel(-1)

    # ctrl+k/ctrl+l nudge the panel by exactly one column. The mode still sets
    # the per-width default; this stores a signed offset on top of it. We derive
    # the offset from the realized width (base = the mode width with no offset)
    # so pushing past a clamp never banks phantom columns — the next press in the
    # opposite direction always moves one column immediately.
    def resize_task_panel(delta)
      height, width = terminal_size
      base = screen_layout(width: width, height: height, panel: true, panel_offset: 0).panel_width
      current = screen_layout(width: width, height: height, panel: true).panel_width
      @ui.panel_offset = (current + delta) - base
      realized = screen_layout(width: width, height: height, panel: true).panel_width
      @ui.panel_offset = realized - base
      flash("task panel: #{realized} cols")
    end

    # Z reveals/hides every effectively unavailable task across every view.
    def toggle_deferred_view
      @ui.toggle_deferred!
      @ui.selected_id = @suspended_task_editor.target_id if resumable_suspended_editor?
      rows
      reconcile_suspended_after_navigation
      refresh_detail_panel if detail_panel? && !@suspended_task_editor
      flash(@ui.show_deferred ? "showing unavailable tasks" : "hiding unavailable tasks")
    end

    # z is the OmniFocus-style availability action. A fuzzy date atomically
    # sets Available from and clears an own On Hold marker; someday adds the
    # indefinite marker; now clears only blockers owned by this task.
    def defer_selected
      item = current_item
      return flash("nothing selected") unless item
      field = TermForm::Fields::Input.new(
        key: :value, value: +"", label: "defer until",
      )
      @ui.form = Form.new(
        kind: :defer_until, title: "Defer until", prompt: "date / choice",
        hint: "fri · +3 · 07-15 · someday · now · esc cancels", min_width: 50,
        return_mode: :list, target_id: item.id, field: field
      ) do |raw|
        operation_today = current_date
        choice = raw.to_s.strip.downcase
        date = Dates.parse_when(raw, today: operation_today)
        unless %w[someday now].include?(choice) || date
          next "can't parse “#{raw}”; use a date, someday, or now"
        end

        snapshot = @application.edit_snapshot(item.id)
        next "task no longer exists" unless snapshot

        changes, label = defer_until_changes(choice, date, item.title)
        command = Tasks::TaskChangeset.from(
          snapshot, changes: changes, history_label: label
        )
        result = @application.update_task(command, today: operation_today)
        unless result.ok?
          reload_store
          next result.conflict? ? "file changed underneath — reopen" : result.tui_message
        end
        fresh_read = Tasks::TaskReadModel.new(result.read_snapshot, today: operation_today)
        @read_model = fresh_read
        @read_model_today = operation_today
        fresh_task = fresh_read.task_for(item.id)
        message = availability_flash(fresh_task, reader: fresh_read)

        @ui.form_success = lambda do
          flash(message)
          if !@ui.show_deferred && fresh_task && !fresh_task.available?
            rows(read: fresh_read, today: operation_today)
            clamp_selection
            refresh_detail_panel if detail_panel?
          else
            @ui.selected_id = item.id
            rows(read: fresh_read, today: operation_today)
            refresh_detail_panel if detail_panel?
          end
        end
        nil
      end
      @ui.mode = :form
    end

    def defer_until_changes(choice, date, title)
      case choice
      when "someday"
        [{ deferred: true }, "on hold: #{title}"]
      when "now"
        [{ activate: true }, "activate: #{title}"]
      else
        [
          { deferred: false, scheduled: date },
          "defer until #{date.iso8601}: #{title}",
        ]
      end
    end

    def availability_flash(task, reader: read_model)
      return "task no longer exists" unless task
      return "▸ available now: #{task.title}" if task.available?

      blocker = task.availability_blocker_id && reader.task_for(task.availability_blocker_id)
      case task.availability_reason
      when :scheduled
        "⏳ #{task.title} unavailable until #{task.scheduled.iso8601}"
      when :ancestor_scheduled
        date = blocker&.scheduled&.iso8601 || "a parent date"
        "⏳ #{task.title} unavailable until #{date} via parent#{blocker ? " #{blocker.title}" : ""}"
      when :on_hold
        "⏸ on hold: #{task.title}"
      when :ancestor_on_hold
        "⏸ #{task.title} on hold via parent#{blocker ? " #{blocker.title}" : ""}"
      else
        "#{task.title} unavailable"
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
      flash("agent: #{current_entry}#{@agent_queue.work? ? " (applies to new requests)" : ""}")
    end

    def undo_last  = history_op(:undo!, "undid")
    def redo_last  = history_op(:redo!, "redid")

    def history_op(op, verb)
      kind, label = @store.public_send(op)
      case kind
      when :empty    then flash("nothing to #{verb == "undid" ? "undo" : "redo"}")
      when :conflict then flash("file changed externally — can't #{op.to_s.chomp("!")} “#{label}”")
      else
        invalidate_read_model
        flash("#{verb}: #{label}")
        rows
        refresh_detail_panel if detail_panel?
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
      yank { |_item, notes, task| Export.markdown(task, notes) }
    end

    def yank
      item = current_item
      return flash("nothing selected") unless item
      task = current_task
      return flash("task no longer exists") unless task
      text = yield(item, task.body, task)
      if Clipboard.copy(text)
        flash("yanked: “#{item.title}”")
      else
        flash("no clipboard tool found (pbcopy/wl-copy/xclip/xsel)")
      end
    end

    def open_help
      open_modal(Modals.help, kind: :help)
    end

    def open_agent_activity
      return flash("no agent requests this session") unless @agent_queue.any?

      _height, width = terminal_size
      open_modal(agent_activity_content(width: width), kind: :agent_activity)
      @agent_activity_width = width
      @agent_activity_second = monotonic_now.floor
    end

    def cancel_queued_agent_requests
      count = @agent_queue.pending_count
      return flash("no queued agent requests") if count.zero?

      noun = count == 1 ? "request" : "requests"
      open_modal(
        {
          title: "Cancel queued agent requests?",
          lines: [
            "Discard #{count} waiting #{noun}?",
            "The active request will keep running.",
            "Press y to discard waiting work · n / esc cancels",
          ],
        },
        kind: :agent_queue_cancel_confirm
      )
    end

    def cancel_queued_agent_requests_key(key)
      case key
      when "y", "Y", "\r", "\n"
        count = @agent_queue.cancel_pending.size
        close_modal
        flash("cancelled #{count} queued agent request#{count == 1 ? "" : "s"}")
      when "n", "N", "\e", "q"
        close_modal
        flash("queued requests kept")
      end
    end

    def open_detail
      return flash("nothing selected") unless current_item
      detail_panel? ? close_panel : show_detail
    end

    # Build the persistent detail panel for the current item. The app stays in
    # list mode, so moving through any of the six views updates this panel.
    def show_detail
      item = current_item
      return close_panel unless item

      refresh_detail_panel(content_width: detail_panel_content_width)
    end

    # Open the selected task's first link in the browser (`o`, list or detail
    # mode). Deliberately the FIRST link: notes lead with the primary reference;
    # the CLI (`tasks open <ref> <n>`) handles precise picking.
    def open_link
      task = current_task or return
      links = task.links
      return flash("no links on this task") if links.empty?
      link = links.first
      unless Tasks::Opener.open_url(link.url)
        return flash("no browser launcher found (set TASKS_OPENER)")
      end
      extra = links.size > 1 ? " (1 of #{links.size})" : ""
      flash("opened #{link.system}: #{link.url}#{extra}")
    end

    def refresh_detail_panel(content_width: @detail_panel_content_width)
      item = current_item
      return close_panel unless item
      task = current_task
      return close_panel unless task

      content_width ||= detail_panel_content_width
      # Skip rebuild when the same task/read-model/width is already shown —
      # paint and select_row both call here, and selection moves dominate.
      if detail_panel? &&
         @detail_panel_id == item.id &&
         @detail_panel_width == content_width &&
         @detail_panel_model.equal?(@read_model)
        return
      end

      @detail_panel_content_width = content_width
      @detail_panel_width = content_width
      @detail_panel_id = item.id
      @detail_panel_model = @read_model
      content = TaskDetails.build(
        task, task.body, content_width, today: @read_model_today,
        links: task.links, project: task.project,
        availability_blocker: task.availability_blocker_id &&
          read_model.task_for(task.availability_blocker_id)
      )
      if detail_panel?
        @ui.panel.replace(title: content[:title], lines: content[:lines], identity: item.id)
      else
        @ui.panel = RightPanel.new(
          title: content[:title], lines: content[:lines], kind: :detail, identity: item.id
        )
      end
    end

    def detail_panel_content_width
      height, width = terminal_size
      screen_layout(width: width, height: height, panel: true).panel_content_width
    end

    def close_panel
      @ui.panel = nil
      @detail_panel_content_width = nil
      @detail_panel_width = nil
      @detail_panel_model = nil
      @detail_panel_id = nil
    end

    def task_editing? = @ui.mode == :task_edit && !@ui.task_editor.nil?

    def suspend_task_edit_for_layout(layout)
      editor = @ui.task_editor
      cancel_task_draft_quit_confirmation if @draft_quit_editor.equal?(editor)
      suspension = editor.suspend
      @suspended_task_panel = @ui.panel
      @ui.task_editor = nil
      @suspended_task_editor = editor
      @task_edit_message = suspension.message
      show_detail
      required_height, required_width = ScreenLayout.minimum_edit_terminal_size(
        footer_rows: layout.footer_size
      )
      flash("editing paused — resize to at least #{required_width}×#{required_height}; " \
            "Tab resumes · #{@task_edit_message}")
    end

    def suspended_recovery_panel?
      @suspended_task_editor && @ui.panel&.kind == :suspended_task_edit
    end

    # Recovery shortcuts are deliberately a read-mode concern. The retained
    # editor may coexist with prompts and popup overlays, but those visible
    # inputs own every byte until they close.
    def suspended_recovery_owns_input?
      @ui.mode == :list && suspended_recovery_panel?
    end

    def resumable_suspended_editor?
      @suspended_task_editor && !@suspended_task_editor.missing?
    end

    def reconcile_suspended_editor(outcome)
      @task_edit_message = task_edit_outcome_message(outcome)
      if outcome&.missing? || !suspended_target_visible_in_current_rows?
        show_suspended_recovery_panel
      elsif detail_panel?
        refresh_detail_panel
      end
    end

    def show_suspended_recovery_panel
      editor = @suspended_task_editor
      canonical_view = suspended_target_canonical_view
      missing = editor.missing?
      title = missing ? "task draft · target deleted" : "task draft · target not visible"
      explanation = if missing
                      "Task no longer exists; local field retained."
                    elsif canonical_view
                      "Task left #{@ui.view}; switch to #{canonical_view} to resume."
                    else
                      "Task exists but is hidden from the canonical views."
                    end
      lines = [explanation, "Draft: #{editor.copy_value}"]
      lines << if canonical_view
                 "switch view + Tab resumes · y copies · esc discards"
               else
                 "y copies field · esc discards draft"
               end
      @ui.panel = RightPanel.new(title: title, lines: lines,
                                 kind: :suspended_task_edit, identity: editor.target_id)
      guidance = canonical_view ? "switch to #{canonical_view} to resume" : "target is not selectable"
      flash("paused task draft: #{guidance} · y copies · esc discards")
    end

    def suspended_recovery_key(key)
      case key
      when "y"
        value = @suspended_task_editor.copy_value.to_s
        if Clipboard.copy(value)
          flash("copied paused task field; esc discards the draft")
        else
          flash("no clipboard tool found; local paused draft is still retained")
        end
      when "\e"
        @suspended_task_editor = nil
        @suspended_task_panel = nil
        @task_edit_message = nil
        close_panel
        flash("discarded local draft for paused task")
      when "\t"
        show_suspended_recovery_panel
      end
    end

    def suspended_target_visible_in_current_rows?
      target_id = @suspended_task_editor&.target_id
      target_id && Array(@rows).any? { |row| row.item&.id == target_id }
    end

    def suspended_target_canonical_view
      editor = @suspended_task_editor
      return if !editor || editor.missing?

      Views::TABS.each do |_label, view|
        candidates = Views.rows(
          view, read_model.items, tree: read_model.tree, collapsed: Set.new,
          show_deferred: @ui.show_deferred, today: @read_model_today,
          urgent_days: @urgent_days, reader: read_model,
        )
        return view if candidates.any? { |row| row.item&.id == editor.target_id }
      end
      nil
    end

    def reconcile_suspended_after_navigation
      return unless @suspended_task_editor

      if suspended_target_visible_in_current_rows?
        show_detail
        flash("paused task draft selected — Tab resumes")
      else
        show_suspended_recovery_panel
      end
    end

    def copy_missing_editor_field
      value = @ui.task_editor.copy_value.to_s
      if Clipboard.copy(value)
        flash("copied local field from deleted task; esc discards the editor")
      else
        flash("no clipboard tool found; local deleted-task edit is still retained")
      end
    end

    def refresh_task_edit_panel(layout:)
      editor = @ui.task_editor
      return unless editor && @ui.panel&.kind == :task_edit

      message = @task_edit_message
      message = "Task no longer exists · esc discards the local edit" if editor.missing?
      result = FormRenderer.new.render(
        model: editor.render_model,
        width: layout.panel_content_width,
        height: [layout.body_height - 2, 1].max,
        title: "edit task",
        hint: message || "tab saves on blur · ctrl-s saves · ctrl-o finishes",
        error: %i[conflict invalid missing].include?(editor.last_result&.tui_status) ? message : nil,
      )
      focus_row = result.focused_content_row && result.focused_content_row + 1
      @ui.panel.replace(title: "task · editing", lines: result.lines,
                        identity: editor.target_id, focused_row: focus_row)
    end

    def process_task_edit_outcome(outcome)
      return unless outcome

      @task_edit_message = task_edit_outcome_message(outcome)
      flash(@task_edit_message) if outcome.missing? || outcome.conflict?
      if outcome.patch_result&.changed?
        invalidate_read_model
        target_id = @ui.task_editor.target_id
        @ui.selected_id = target_id
        rows
        unless @rows.any? { |row| row.item&.id == target_id }
          destination = current_item&.title
          explanation = "Saved; task left the #{@ui.view} view"
          explanation += " · selected #{destination}" if destination
          return close_task_edit(message: explanation, keep_panel: false)
        end
      end

      if outcome.finished?
        close_task_edit(message: outcome.message)
      elsif outcome.missing?
        @task_edit_message = outcome.message
      elsif outcome.status == :confirmation
        @task_edit_message = "#{outcome.message} · y accepts · n cancels"
      end
    end

    def task_edit_outcome_message(outcome)
      return unless outcome
      return "Task no longer exists; local field retained for copy or discard" if outcome.missing?
      return "Edit conflict — field changed externally; local value retained" if outcome.conflict?

      outcome.message
    end

    def close_task_edit(message: nil, keep_panel: true)
      editor = @ui.task_editor
      target_id = editor&.target_id
      @ui.task_editor = nil
      @suspended_task_editor = nil
      @suspended_task_panel = nil
      @task_edit_message = nil
      @ui.mode = :list unless @ui.mode == :list

      target_visible = target_id && current_item&.id == target_id
      if keep_panel && target_visible
        show_detail
      else
        close_panel
      end
      flash(message) if message
    end

    def task_draft_quit_confirmation?
      @draft_quit_editor&.pending_quit_confirmation
    end

    def show_task_draft_quit_confirmation(editor, outcome)
      @draft_quit_editor = editor
      @draft_quit_return_modal = @ui.modal
      @draft_quit_return_mode = @ui.mode
      @draft_quit_return_message = @task_edit_message
      @ui.mode = :modal if @ui.mode == :modal_filter
      @ui.mode = :list if @ui.mode == :task_edit
      work_line = if @agent_queue.work?
                    "Quitting also cancels/discards #{agent_work_summary}."
                  end
      @ui.modal = Modal.new(
        title: "Discard unsaved task draft?",
        lines: [
          outcome.message,
          work_line,
          "Press y or Return to discard the draft and quit.",
          "Press n or Escape to keep the draft and continue.",
          "Ctrl-C and q do not confirm this prompt.",
        ].compact,
        kind: :task_draft_quit_confirm,
      )
      @ui.mode = :modal
      @task_edit_message = outcome.message if @ui.task_editor.equal?(editor)
      flash("unsaved task draft — y/return discards and quits · n/esc keeps editing")
    end

    def task_draft_quit_confirmation_key(key)
      editor = @draft_quit_editor
      outcome = editor.handle_quit_confirmation(key)
      case outcome.status
      when :quit_confirmed
        clear_task_draft_quit_confirmation(restore: false)
        @ui.task_editor = nil if @ui.task_editor.equal?(editor)
        if @suspended_task_editor.equal?(editor)
          @suspended_task_editor = nil
          @suspended_task_panel = nil
        end
        @task_edit_message = nil
        @agent_queue.shutdown if @agent_queue.work?
        @quit = true
      when :quit_cancelled
        clear_task_draft_quit_confirmation
        flash(outcome.message)
      else
        flash("confirmation still open — y/return discards and quits · n/esc keeps editing") \
          if key == "\x03" || key == "q"
      end
    end

    def cancel_task_draft_quit_confirmation
      return unless task_draft_quit_confirmation?

      @draft_quit_editor.handle_quit_confirmation("\e")
      clear_task_draft_quit_confirmation
    end

    def clear_task_draft_quit_confirmation(restore: true)
      return_modal = @draft_quit_return_modal
      return_mode = @draft_quit_return_mode
      return_message = @draft_quit_return_message
      @draft_quit_editor = nil
      @draft_quit_return_modal = nil
      @draft_quit_return_mode = nil
      @draft_quit_return_message = nil

      if restore
        @ui.modal = return_modal
        @ui.mode = return_mode if return_mode && @ui.mode != return_mode
        @task_edit_message = return_message
      else
        @ui.modal = nil
      end
    end

    def show_agent_quit_confirmation
      @agent_quit_confirmation = true
      @agent_quit_return_modal = @ui.modal
      @agent_quit_return_mode = @ui.mode
      @ui.mode = :modal if @ui.mode == :modal_filter
      @ui.mode = :list if @ui.mode == :task_edit
      @ui.modal = Modal.new(
        title: "Quit with agent work pending?",
        lines: [
          "Quitting cancels/discards #{agent_work_summary}.",
          "Press y or Return to quit.",
          "Press n or Escape to keep the queue running.",
          "Ctrl-C and q do not confirm this prompt.",
        ],
        kind: :agent_quit_confirm,
      )
      @ui.mode = :modal
      flash("agent work pending — y/return quits · n/esc keeps running")
    end

    def agent_quit_confirmation_key(key)
      case key
      when "y", "Y", "\r", "\n"
        clear_agent_quit_confirmation(restore: false)
        @agent_queue.shutdown
        @quit = true
      when "n", "N", "\e"
        clear_agent_quit_confirmation
        flash("quit cancelled — agent queue kept")
      else
        flash("confirmation still open — y/return quits · n/esc keeps running") \
          if key == "\x03" || key == "q"
      end
    end

    def clear_agent_quit_confirmation(restore: true)
      return_modal = @agent_quit_return_modal
      return_mode = @agent_quit_return_mode
      @agent_quit_confirmation = false
      @agent_quit_return_modal = nil
      @agent_quit_return_mode = nil

      if restore
        @ui.modal = return_modal
        @ui.mode = return_mode if return_mode && @ui.mode != return_mode
      else
        @ui.modal = nil
      end
    end

    def agent_work_summary
      parts = []
      parts << "the active request" if @agent_queue.active?
      pending = @agent_queue.pending_count
      parts << "#{pending} queued request#{pending == 1 ? "" : "s"}" if pending.positive?
      parts.join(" and ")
    end

    # -- modal -----------------------------------------------------------------

    # Frame draws the modal box; App supplies the filter line so the `/` filter
    # renders inside the modal chrome rather than in the main prompt area.
    def modal_view(body_h, width: nil)
      return unless @ui.modal

      if @ui.modal.kind == :agent_activity && width
        second = monotonic_now.floor
        if width != @agent_activity_width || (@agent_queue.active? && second != @agent_activity_second)
          refresh_agent_activity(width: width, now: second)
        end
      end

      @ui.modal.view(body_h, filter_line: modal_filter_line)
    end

    # The filter line shown inside a filterable modal: the live input with a
    # cursor while typing, the retained query once committed, nil otherwise.
    def modal_filter_line
      return unless @ui.modal&.filterable?

      if @ui.mode == :modal_filter
        "#{T.paint(:prompt, "/ ")}#{inline_input(@ui.modal_filter_input)}" \
          "#{T.paint(:muted, "  enter keeps · esc clears")}"
      elsif @ui.modal.filter
        "#{T.paint(:prompt, "/ ")}#{@ui.modal.filter}#{T.paint(:muted, "  / edits · esc clears")}"
      end
    end

    def modal_up   = modal_move(-1)
    def modal_down = modal_move(1)
    def modal_half_up   = @ui.modal.scroll_half(-1, modal_body_h)
    def modal_half_down = @ui.modal.scroll_half(1, modal_body_h)
    def modal_page_up   = @ui.modal.scroll_page(-1, modal_body_h)
    def modal_page_down = @ui.modal.scroll_page(1, modal_body_h)

    def panel_half_up   = @ui.panel.scroll_half(-1, panel_body_h)
    def panel_half_down = @ui.panel.scroll_half(1, panel_body_h)
    def panel_page_up   = @ui.panel.scroll_page(-1, panel_body_h)
    def panel_page_down = @ui.panel.scroll_page(1, panel_body_h)

    # Blocking modals own their own scroll. The detail panel remains in list
    # mode and therefore uses ordinary task navigation.
    def modal_move(delta)
      @ui.modal.scroll_line(delta, modal_body_h)
    end

    # Body rows available to the modal box — the same budget paint hands
    # Frame.build, so scroll steps match what's on screen.
    def modal_body_h(height: nil, width: nil)
      terminal_height, terminal_width = terminal_size if height.nil? || width.nil?
      height ||= terminal_height
      width ||= terminal_width
      screen_layout(width: width, height: height).body_height
    end

    def panel_body_h(height: nil, width: nil)
      modal_body_h(height: height, width: width)
    end

    def detail_panel? = @ui.panel&.kind == :detail

    def open_modal(content, kind:)
      @ui.modal = Modal.new(title: content[:title], lines: content[:lines],
                            kind: kind, filterable: %i[help agent_activity].include?(kind),
                            filter_groups: content[:filter_groups])
      @ui.modal_filter_input.clear
      @ui.mode = :modal
    end

    def close_modal
      if @ui.modal&.kind == :agent_activity
        @agent_activity_width = nil
        @agent_activity_second = nil
      end
      @ui.mode = :list
      @ui.modal = nil
      @ui.archive_preview = nil
      @ui.modal_filter_input.clear
    end

    def agent_activity_content(width: nil, now: nil)
      _height, sampled_width = terminal_size unless width
      width ||= sampled_width
      AgentActivity.content(
        requests: @agent_queue.requests,
        now: now || monotonic_now,
        width: width
      )
    end

    def refresh_agent_activity(width: nil, now: nil)
      return unless @ui.modal&.kind == :agent_activity

      width ||= @agent_activity_width || terminal_size.last
      now ||= monotonic_now
      content = agent_activity_content(width: width, now: now)
      @ui.modal.replace(title: content[:title], lines: content[:lines],
                        filter_groups: content[:filter_groups])
      @agent_activity_width = width
      @agent_activity_second = now.floor
    end

    def monotonic_now = Process.clock_gettime(Process::CLOCK_MONOTONIC)

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

    def current_task
      item = current_item
      item && read_model.task_for(item)
    end

    def select_row(index)
      id = @rows[index]&.item&.id
      return if @sel == index && @ui.selected_id == id

      @sel = index
      @ui.selected_id = id
      refresh_detail_panel if detail_panel?
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
    # longer visible, land on the selectable row nearest the prior coordinate;
    # an open detail panel follows that fallback selection.
    def sync_selection
      sels = selectable_indexes
      if sels.empty?
        @sel = 0
        @ui.selected_id = nil
        close_panel if detail_panel?
        return
      end

      # A task with multiple contexts can appear more than once in the Next
      # view. Keep the current occurrence when it still represents the id;
      # otherwise choose the first visible occurrence deterministically.
      idx = @sel if @ui.selected_id && sels.include?(@sel) && @rows[@sel].item&.id == @ui.selected_id
      idx ||= @ui.selected_id && sels.find { |i| @rows[i].item&.id == @ui.selected_id }
      idx ||= sels.min_by { |i| [(i - @sel).abs, i] }
      select_row(idx)
    end

    def switch_view(n)
      @ui.selected_id = @suspended_task_editor.target_id if resumable_suspended_editor?
      @ui.view = Views::TABS[n - 1].last
      rows
      reconcile_suspended_after_navigation
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
      if collapsible_children?(node) &&
         item.id && !@ui.collapsed.include?(item.id)
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
      read_model.tree.each do |root|
        root.each do |n|
          has_collapsible_children = if @ui.view == :outline && !active_filter && !active_context_filter
                                       n.children.any?
                                     else
                                       n.children.any?(&:task?)
                                     end
          @ui.collapsed.add(n.item.id) if n.task? && n.item.id && has_collapsible_children
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

    def collapsible_children?(node)
      if @ui.view == :outline && !active_filter && !active_context_filter
        node.children.any?
      else
        Views.visible_children(node, @ui.show_deferred, reader: read_model).any?
      end
    end

    def save_session
      live_ids = read_model.items.map(&:id).compact
      live_contexts = read_model.items.flat_map(&:contexts).uniq
      Session.save(@ui.session_hash(live_ids: live_ids, live_contexts: live_contexts))
    end

    def complete_selected
      item = current_item
      return flash("nothing selected") unless item
      return flash("already #{item.state}") unless item.open?
      recurring = item.recurring?
      operation_today = current_date
      result = patch_task(item, field: :state, value: "DONE", label: "complete: #{item.title}",
                                today: operation_today)
      if result.ok?
        if recurring
          # A recurring task rolled forward and is still in the view — follow it.
          fresh = read_model.task_for(item.id)
          d = fresh && (fresh.deadline || fresh.scheduled)
          flash("↻ #{item.title}#{d ? " → #{d.iso8601} (#{d.strftime("%a")})" : ""}")
          reselect(item.id)
          refresh_detail_panel if detail_panel?
        else
          # The patch result carries every touched stable id; a parent cascade closes its
          # open descendants too — note how many rode along.
          n = result.touched_ids.size - 1
          subs = n > 0 ? " (+#{n} subtask#{"s" unless n == 1})" : ""
          flash("✓ DONE: #{item.title}#{subs} — x to archive")
          rows
          refresh_detail_panel if detail_panel?
        end
      else
        reload_store
        flash("file changed underneath — try again")
      end
    end

    def open_date_popup
      item = current_item
      return flash("nothing selected") unless item

      target = item.deadline ? "Deadline" : item.scheduled ? "Available from" : "Deadline (new)"
      field = TermForm::Fields::DateInput.new(
        key: :value, value: +"", label: "new #{target}",
        parser: ->(raw, _today) { Dates.parse_when(raw) },
      )
      @ui.form = Form.new(
        kind: :date, title: "edit date", prompt: "new #{target}",
        hint: "fri · +3 · 07-15 · esc cancels", min_width: 36,
        return_mode: :list, target_id: item.id, field: field
      ) do |raw|
        operation_today = current_date
        date = Dates.parse_when(raw, today: operation_today)
        next "can't parse “#{raw}”" unless date
        kind = if item.deadline     then :deadline
               elsif item.scheduled then :scheduled
               else                      :deadline
               end
        result = patch_task(item, field: kind, value: date,
                            label: "reschedule → #{date.iso8601}: #{item.title}",
                            today: operation_today)
        unless result.ok?
          reload_store
          next "file changed underneath — reopen"
        end

        @ui.form_success = lambda do
          promoted = item.state == "INBOX" ? " · INBOX → TODO" : ""
          flash("→ #{item.title}: #{date.iso8601} (#{date.strftime("%a")})#{promoted}")
          reselect(item.id)
          refresh_detail_panel if detail_panel?
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
      return flash("add an Available from date or deadline first — recurrence needs a date") unless item.scheduled || item.deadline

      current = item.recur ? "now #{item.recur}" : "not repeating"
      field = TermForm::Fields::Input.new(
        key: :value, value: item.recur || +"", label: "every",
      )
      @ui.form = Form.new(
        kind: :recurrence, title: "recur", prompt: "every",
        hint: "weekly · 2w · .+1m · off · esc cancels", min_width: 40,
        return_mode: :list,
        initial: item.recur || +"", suffix: "(#{current})", target_id: item.id, field: field
      ) do |raw|
        cookie = Tasks::Recur.parse_interval(raw)
        next "can't parse “#{raw}”" if cookie.nil?
        label = cookie == :off ? "recur off: #{item.title}" : "recur #{cookie}: #{item.title}"
        unless patch_task(item, field: :recurrence, value: cookie, label: label).ok?
          reload_store
          next "file changed underneath — reopen"
        end

        @ui.form_success = lambda do
          flash(cookie == :off ? "↻ off: #{item.title}" : "↻ #{cookie}: #{item.title}")
          reselect(item.id)
          refresh_detail_panel if detail_panel?
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
          invalidate_read_model
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

    # -- agent queue -----------------------------------------------------------

    def submit_prompt
      text = @input.strip
      return if text.empty?

      submission = @agent_queue.enqueue(prompt: text, entry: current_entry)
      unless submission.accepted?
        @ui.mode = :prompt
        return flash(submission.error)
      end

      @input.clear
      @ui.mode = :list
      @resp_open = false
      was_active = @agent_queue.active?
      start_event = advance_agent_queue unless was_active
      request = submission.request
      if was_active
        flash("queued agent request ##{request.id} · #{@agent_queue.pending_count} waiting")
      elsif start_event&.type == :started
        flash("starting agent request ##{request.id}")
      else
        flash("agent request ##{request.id} failed to start")
      end
    end

    def pump_agent_queue
      event = @agent_queue.pump
      refresh_agent_activity
      return unless event

      record_agent_result(event.request)
      reload_store if external_change?
      advance_agent_queue
    end

    def advance_agent_queue
      loop do
        event = @agent_queue.start_next
        return unless event

        refresh_agent_activity
        return event if event.type == :started

        record_agent_result(event.request)
        reload_store if external_change?
      end
    end

    def record_agent_result(request)
      width = terminal_size.last
      output = A.normalize(request.output.to_s).scrub("�").strip
      @resp = A.wrap(output, width - 8)
      @resp = [T.paint(:muted, "(no output)")] if @resp.all? { |l| l.strip.empty? }
      if request.error && request.status == :failed
        @resp << T.paint(:error, request.error)
      end
      @resp_request_id = request.id
      @resp_open = true
      @resp_scroll = 0
    end

    def scroll_resp(delta)
      return unless @resp_open && @resp
      max = [@resp.size - RESP_MAX, 0].max
      @resp_scroll = (@resp_scroll + delta).clamp(0, max)
    end

    def dismiss_or_cancel
      if @agent_queue.active?
        event = @agent_queue.cancel_active
        record_agent_result(event.request)
        reload_store if external_change?
        advance_agent_queue
        flash("cancelled agent request ##{event.request.id}")
      elsif @resp_open
        @resp_open = false
      elsif @ui.filter
        @ui.filter = nil
        flash("filter cleared")
      elsif @ui.context_filter
        @ui.context_filter = nil
        flash("context filter cleared")
        rows
      elsif detail_panel?
        close_panel
      end
    end

    # -- flash -------------------------------------------------------------

    def flash(msg)
      @flash = msg
      @flash_until = Time.now + 3
      @paint_dirty = true
    end

    # Returns true when a visible flash was cleared, so the idle loop can
    # schedule one more paint without waiting for the next keystroke.
    def clear_flash_if_expired
      return false unless @flash && Time.now > @flash_until

      @flash = nil
      true
    end
  end
end

Tui::Shortcuts.validate!(Tui::App)
