# frozen_string_literal: true

require "io/console"
require "date"
require "json"
require "securerandom"
require "set"
require_relative "../tasks/determinism"
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
require_relative "project_details"
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
require_relative "mouse"
require_relative "hit_map"
require_relative "mouse_router"
require_relative "../tasks/config"
require_relative "../tasks/agent_context"
require_relative "../tasks/application"
require_relative "../tasks/delegation"
require_relative "../tasks/delegation_command"
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
    DELEGATION_HANDLERS = %i[delegate_selected set_work_ref_selected].freeze
    # List views that keep the outliner tree (subtasks + collapse/expand) under an
    # active `@` context filter. Outline/Projects stay on their flat filtered path.
    CONTEXT_TREE_VIEWS = %i[agenda next quadrants inbox].freeze
    CONTEXT_FILTER_MODE = :any
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
                   date_provider: nil, time_provider: nil)
      Theme.configure!(name: paths.theme, overrides: paths.colors || {})
      Tasks::Dates.configure!(date_order: paths.date_order)
      @paths = paths
      # Store remains the long-lived watcher, history/archive, and form-option
      # source. TUI presentation reads and patch-style writes travel through
      # @application so adapters do not own task mutation semantics.
      @store  = Store.new(org: paths.org, archive: paths.archive,
                          links: paths.links || {}, link_systems: paths.link_systems || {},
                          max_depth: paths.max_depth)
      @time_provider_injected = !time_provider.nil?
      @time_provider = if time_provider.nil?
                         -> { Time.now.utc }
                       elsif time_provider.respond_to?(:call)
                         time_provider
                       else
                         -> { time_provider }
                       end
      @date_provider_injected = !date_provider.nil?
      @date_provider = if date_provider.nil?
                         -> { Tasks::Timezones.local_time(current_time, paths.timezone).to_date }
                       elsif date_provider.respond_to?(:call)
                         date_provider
                       else
                         -> { date_provider }
                       end
      @application = Tasks::Application.new(
        store_factory: Tasks::StoreFactory.new(
          org: paths.org, archive: paths.archive, links: paths.links || {},
          link_systems: paths.link_systems || {}, max_depth: paths.max_depth
        ),
        temporal_context_factory: -> {
          temporal_context
        },
        host_context: paths.host_context
      )
      @read_model = nil
      @read_model_today = nil
      @read_model_minute = nil
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
      @last_layout = nil
      @last_popup = nil
      @last_modal = nil
      @hit_map = nil
      # A fresh App is a cleared App: the presentation caches start in exactly
      # the state an invalidation leaves them in. Declaring them here instead
      # would fork one list into two that must be kept in step by hand, and the
      # half that gets forgotten is a cache that outlives its read model.
      clear_row_caches
      @pending_project = nil
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
      show_unsupported_schema_notice if unsupported_schema?
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
      print Mouse::ENABLE if mouse_enabled?
      loop_once until @quit
    ensure
      # Terminal restore FIRST — if saving somehow raised, a skipped restore
      # would leave the shell raw on the alt screen, far worse than a lost view.
      # Disable mouse tracking before leaving the alt screen so a raw shell does
      # not keep receiving SGR reports.
      print Mouse::DISABLE if mouse_enabled?
      print "\e[?2004l\e[?1049l\e[?25h"
      $stdin.cooked!
      @agent_queue.shutdown if @agent_queue&.work?
      save_session # so the view persists however the TUI exits
    end

    private

    # True when either file declares a schema version this build does not
    # implement. There is no conversion path in either direction: the TUI says
    # so and refuses to edit, rather than rewriting bytes it cannot interpret.
    def unsupported_schema?
      [@paths.org, @paths.archive].any? do |path|
        next false unless File.exist?(path)

        first = JSON.parse(File.open(path, "r", encoding: "UTF-8", &:readline))
        first["type"] == "meta" && first["version"].is_a?(Integer) &&
          first["version"] != Tasks::Format::VERSION
      rescue JSON::ParserError, EOFError, SystemCallError
        false
      end
    end

    def show_unsupported_schema_notice
      @ui.modal = Modal.new(
        title: "Unsupported schema version",
        kind: :unsupported_schema,
        lines: [
          "This task store declares a schema version this build does not implement.",
          "Supported: schema v#{Tasks::Format::VERSION}.",
          "Run `tasks check` for the exact version, and use a build that reads it.",
          "Nothing has been written; editing is refused while this is true.",
          "Escape closes this notice.",
        ],
      )
      @ui.mode = :modal
    end

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
      minute = current_time.to_i / 60
      if @read_model_minute && minute != @read_model_minute
        invalidate_read_model
        changed = true
      end
      @read_model_minute = minute
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
        refresh_open_panel if panel_detail?
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
      if detail_panel?
        refresh_detail_panel(content_width: layout.panel_content_width)
      elsif project_detail? && current_project
        refresh_project_detail_panel(current_project, content_width: layout.panel_content_width)
      end
      popup = current_popup(layout: layout)
      modal = layout.place_modal(modal_view(layout.body_height, width: width))
      lines = Frame.build(
        width: width, height: height,
        header: header(width - 2),
        rows: frame_rows,
        selected: visual_selection,
        footer: layout.footer,
        popup: popup,
        panel: @ui.panel&.view(height: layout.body_height, width: layout.panel_content_width),
        modal: modal,
        layout: layout
      )
      @last_layout = layout
      @last_popup = popup
      @last_modal = modal
      @hit_map = nil # rebuild lazily against this painted frame
      print "\e[H" + lines.join("\e[K\r\n") + "\e[K"
    end

    # LINES/COLUMNS win over the real winsize when both are set, so a harness can
    # render a fixed-geometry frame without a pty (Tasks::Determinism). With them
    # unset this is the same `IO.console&.winsize || [24, 80]` it always was.
    def terminal_size
      height, width = Tasks::Determinism.winsize || IO.console&.winsize || [24, 80]
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
      context = temporal_context
      day = context.local_date
      if @read_model.nil? || @read_model_today != day
        @read_model = read_tasks_with_temporal_fallback(context)
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

    def reload_read_model(context = temporal_context)
      @read_model_today = context.local_date
      @read_model_minute = context.now.to_i / 60
      @read_model = read_tasks_with_temporal_fallback(context)
      clear_row_caches
    end

    def invalidate_read_model
      @read_model = nil
      @read_model_today = nil
      @read_model_minute = nil
      clear_row_caches
    end

    # Application mutations write through per-operation Stores. Refresh the
    # TUI's long-lived watcher Store immediately so its next tick does not
    # mistake our own write for an external edit and run reconciliation twice.
    # Then rebuild presentation from a fresh Application read: a concurrent
    # writer may have landed between the mutation result and this refresh.
    def absorb_own_write(context = temporal_context)
      @store.reload!
      reload_read_model(context)
    end

    # Drops every cache derived from the read model. Assignment only, on purpose
    # — the constructor calls it to put a fresh App in exactly this state, so it
    # must not read an ivar or reach for anything not yet built.
    def clear_row_caches
      @rows = nil
      @rows_fingerprint = nil
      @row_item_count = 0
      @filtered_items = nil
      @filtered_items_model = nil
      @filtered_items_key = nil
      @tab_counts = nil
      @tab_counts_model = nil
      @tab_counts_key = nil
      @title_haystack = nil
      @title_haystack_model = nil
      @open_count = nil
      @open_count_model = nil
      @detail_panel_width = nil
      @detail_panel_model = nil
      @detail_panel_id = nil
      @project_views = nil
      @project_views_model = nil
      @project_detail_id = nil
      @project_detail_width = nil
      @project_detail_model = nil
    end

    # The Phase-1 project read model behind the Projects tab. Rolled up once per
    # read-model identity — like title_haystack — so navigation reuses the list
    # instead of rebuilding a fresh Store snapshot on every keystroke. A project
    # mutation invalidates the read model, which recomputes this in step.
    def project_views(read, today)
      return @project_views if @project_views_model.equal?(read) && @project_views

      @project_views_model = read
      @project_views = @application.list_projects(
        today: today, context: tui_operation_context(read.temporal_context)
      )
    end

    def current_date = @date_provider.call
    def current_time = @time_provider.call.utc
    def temporal_context
      now = if @date_provider_injected && !@time_provider_injected
              date = current_date
              Time.utc(date.year, date.month, date.day, 12)
            else
              current_time
            end
      Tasks::TemporalContext.new(now: now, timezone: @paths.timezone, time_format: @paths.time_format)
    end

    def tui_operation_context(context)
      Tasks::OperationContext.new(
        operation_id: "tui_#{SecureRandom.hex(8)}", source: :tui, temporal_context: context
      )
    end

    def read_tasks_with_temporal_fallback(context)
      @application.read_tasks(
        today: context.local_date, context: tui_operation_context(context)
      )
    rescue Tasks::Timezones::Error => error
      fallback = Tasks::TemporalContext.new(
        now: context.now, timezone: "Etc/UTC", time_format: context.time_format
      )
      unless @ui.modal&.kind == :temporal_context_invalid
        @ui.modal = Modal.new(
          title: "Time zone makes a floating time invalid",
          kind: :temporal_context_invalid,
          lines: [
            error.message,
            "Tasks are shown with a temporary UTC fallback so you can edit the value.",
            "Choose a valid local time or change timezone in the tasks config.",
            "Press Escape to continue.",
          ],
        )
        @ui.mode = :modal
      end
      @application.read_tasks(
        today: fallback.local_date, context: tui_operation_context(fallback)
      )
    end

    def rows(read: nil, today: nil)
      read ||= read_model
      today ||= @read_model_today
      fingerprint = rows_fingerprint(read, today)
      if @rows && @rows_fingerprint == fingerprint
        sync_selection
        return @rows
      end

      contexts = active_context_filters
      items = filtered_items(read)
      intake_counts = tab_counts(read: read, today: today)[:inbox]
      # A `/` search always renders flat (its result shape is what filtering
      # expects). A `@` context filter keeps the tree on the list views so
      # subtasks stay visible — scoped to that context via context_filter —
      # but outline/projects stay flat under a context filter as before.
      use_tree = if active_filter
                   false
                 elsif !contexts.empty?
                   CONTEXT_TREE_VIEWS.include?(@ui.view)
                 else
                   true
                 end
      if use_tree
        projects = @ui.view == :projects ? project_views(read, today) : nil
        @rows = Views.rows(@ui.view, items, tree: read.tree, collapsed: @ui.collapsed,
                                         show_deferred: @ui.show_deferred, today: today,
                                         urgent_days: @urgent_days,
                                         reader: read, projects: projects,
                                         context_filters: contexts,
                                         context_filter_mode: CONTEXT_FILTER_MODE,
                                         intake_counts: intake_counts)
      else
        @rows = Views.rows(@ui.view, items, show_deferred: @ui.show_deferred,
                                           today: today,
                                           urgent_days: @urgent_days, reader: read,
                                           intake_counts: intake_counts)
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
        active_context_filters,
        CONTEXT_FILTER_MODE,
        @ui.collapsed.hash,
      ]
    end

    # Items after the active `@` context and `/` search filters. Shared by the
    # list views and the tab badge so the badge can't advertise rows the
    # current filter hides — an @home count of work approvals is a lie you
    # can't act on. Memoized per (model, filter) since the header repaints
    # every frame, including each keystroke while typing a filter.
    def filtered_items(read)
      contexts = active_context_filters
      key = [active_filter, contexts]
      if @filtered_items && @filtered_items_model.equal?(read) && @filtered_items_key == key
        return @filtered_items
      end

      items = read.items
      unless contexts.empty?
        items = items.select do |item|
          context_filter_match?(item, contexts, mode: CONTEXT_FILTER_MODE)
        end
      end
      if (q = active_filter)
        q = q.downcase
        hay = title_haystack(read)
        items = items.select { |i| (hay[i.id] || i.title.downcase).include?(q) }
      end
      @filtered_items_model = read
      @filtered_items_key = key
      @filtered_items = items
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

    def active_context_filters
      @ui.context_filters.filter_map { |ctx| ContextPalette.normalize(ctx) }.uniq
    end

    def context_filter_match?(item, contexts, mode:)
      case mode
      when :any then item.contexts.any? { |ctx| contexts.include?(ctx) }
      else raise ArgumentError, "unknown context filter mode #{mode.inspect}"
      end
    end

    def header(w)
      open_n = open_task_count(read_model)
      unavailable_note = @ui.show_deferred ? "#{T.paint(:warning, "unavailable shown")}#{T.paint(:muted, " · ")}" : ""
      count = "#{T.paint(:muted, "#{open_n} open · ")}#{unavailable_note}#{T.paint(:accent, current_entry.ui_label)}#{T.paint(:muted, " · ? help")}"
      tab_width = [w - A.vislen(count) - 3, 1].max
      @last_tab_presentation = Views.tab_presentation(
        active: @ui.view, counts: tab_counts, width: tab_width
      )
      tabs = @last_tab_presentation.strip
      gap = [w - A.vislen(tabs) - A.vislen(count) - 2, 1].max
      " #{tabs}#{" " * gap}#{count} "
    end

    # Counts shared by the intake section headers and tab strip under the
    # filters in force. Every count is taken over filtered_items so a label can
    # never advertise work the active `@`/`/` filter hides. The inbox count asks
    # the Inbox view's own Query for eligibility rather than testing
    # state == "INBOX" here, so badge and list agree about held captures — a
    # deferred INBOX item is counted only once `show_deferred` reveals it.
    #
    # This counts *tasks in the tab*, not rows the tab paints, and the two are
    # deliberately not the same number. Tree mode rides non-matching descendants
    # along under a matching anchor for context (see inbox_tree), and collapsing
    # an anchor hides rows without emptying the inbox. Counting rows would make
    # the badge shrink when you fold a subtree and grow on riders that are not
    # inbox work; counting tasks keeps it the same number `tasks inbox` reports.
    #
    # Memoized like the other per-frame header inputs. The header repaints on
    # every keystroke and on each animation tick while an agent runs, and this
    # is a linear pass with an availability test per item — by far the most
    # expensive thing the header does on a large list.
    def tab_counts(read: nil, today: nil)
      read ||= read_model
      today ||= @read_model_today
      key = [today, active_filter, active_context_filters, @ui.show_deferred]
      if @tab_counts && @tab_counts_model.equal?(read) && @tab_counts_key == key
        return @tab_counts
      end

      items = filtered_items(read)
      inbox = Views.view_query(
        :inbox, today: today, urgent_days: @urgent_days,
        show_deferred: @ui.show_deferred, reader: read
      )
      @tab_counts_model = read
      @tab_counts_key = key
      @tab_counts = {
        inbox: Views::IntakeCounts.new(
          inbox: items.count { |item| inbox.eligible?(item) },
          approvals: items.count(&:proposed?)
        )
      }.freeze
    end

    def footer(w, mode: @ui.mode)
      f = []
      if (active = @agent_queue.active_request)
        pending = @agent_queue.pending_count
        queued = pending.positive? ? " · #{pending} queued" : ""
        f << T.paint(
          :muted,
          " #{SPINNER[@tick % SPINNER.size]} ##{active.id} #{active.entry.ui_label} is working#{queued} · A activity · esc cancels"
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
      unless @ui.context_filters.empty? || mode == :context_palette
        n = @row_item_count
        summary = @ui.context_filters.join(" + ")
        f << T.paint(:muted, " #{summary} · #{n} match#{n == 1 ? "" : "es"} · esc clears · @ changes")
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

          if (seq = @key_data[Mouse::SEQUENCE])
            # Always consume the report so a stray tracking terminal cannot
            # inject Escape + literal text. Apply only when tracking is on.
            handle_mouse(seq) if mouse_enabled?
            @key_data = @key_data[seq.length..] || +""
            next
          end

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
        @key_data.match?(/\A\e\[<[0-9;]*\z/) ||
        @key_data.match?(/\A\e\[[0-9;?]*\z/) ||
        @key_data == "\eO" ||
        @key_data == "\e\e" ||
        @key_data.match?(/\A\e\e\[[0-9;?]*\z/) ||
        @key_data == "\e\eO"
    end

    def mouse_enabled?
      return false unless $stdin.tty?
      term = ENV["TERM"]
      return false if term.nil? || term.empty? || term == "dumb"

      @paths.mouse
    end

    def handle_mouse(seq)
      return unless (event = Mouse.decode(seq))
      return unless @last_layout

      hit = hit_map.at(event.row, event.col)
      apply_mouse_intent(
        MouseRouter.intent(event, hit, mode: @ui.mode, panel: !@ui.panel.nil?, selected: @sel)
      )
    end

    # Pointer gestures that mean "I'm working in the list now". Painting hides
    # the row cursor while the prompt has focus (see paint's visual_selection),
    # so any of these landing with the prompt focused would move an invisible
    # selection — the click would read as doing nothing at all.
    LIST_FOCUS_INTENTS = %i[select_row activate_row toggle_collapse switch_view scroll_list].freeze

    def apply_mouse_intent(intent)
      blur_prompt_for(intent)
      case intent
      in :ignored
        nil
      in [:select_row, n]
        select_row(n) if @rows[n]&.selectable?
      in [:activate_row, n]
        if @rows[n]&.selectable?
          select_row(n) unless @sel == n
          open_detail
        end
      in [:switch_view, key]
        idx = Views::TABS.index { |_, k| k == key }
        switch_view(idx + 1) if idx
      in [:scroll_list, d]
        move(d)
      in [:scroll_panel, d]
        @ui.panel&.scroll_line(d, panel_body_h)
      in [:scroll_modal, d]
        @ui.modal&.scroll_line(d, screen_layout(
          width: @last_layout.width, height: @last_layout.height
        ).body_height)
      in [:scroll_response, d]
        scroll_resp(d)
      in [:scroll_popup, d]
        scroll_popup_wheel(d)
      in [:focus_prompt]
        focus_prompt
      in [:toggle_collapse, n]
        toggle_collapse_at(n)
      in [:picker_hit, row_offset]
        apply_picker_hit(row_offset)
      else
        nil
      end
      # A later report in the same stdin chunk must not resolve against geometry
      # or tab spans from before this action (e.g. a click that switched views).
      @hit_map = nil
      @paint_dirty = true
    end

    # Clicking or scrolling the list, a tab, or a marker takes focus out of the
    # prompt, exactly like Escape: the typed draft survives, only the focus
    # moves, so the selection the pointer just made is visible.
    def blur_prompt_for(intent)
      return unless @ui.mode == :prompt
      return unless intent.is_a?(Array) && LIST_FOCUS_INTENTS.include?(intent.first)

      @ui.mode = :list
    end

    def hit_map
      @hit_map ||= begin
        rows_now = @rows || []
        marker_spans = {}
        rows_now.each_with_index do |row, i|
          marker_spans[i] = row.marker_span if row.marker_span
        end
        HitMap.build(
          layout: @last_layout,
          tab_spans: @last_tab_presentation&.spans ||
            Views.tab_spans(active: @ui.view, counts: tab_counts),
          row_count: rows_now.size,
          modal: @last_modal,
          popup: @last_popup,
          panel: !@ui.panel.nil?,
          marker_spans: marker_spans,
          footer_roles: footer_roles_for(@last_layout.footer)
        )
      end
    end

    # Classify each fitted footer line so the router can tell the agent
    # response pane from the prompt without knowing footer construction.
    def footer_roles_for(footer_lines)
      prompt_start = footer_lines.index do |line|
        line.is_a?(String) && A.strip(line).include?("❯")
      end
      # Wrapped prompt continuations drop the ❯; everything from the first
      # prompt line through the end of the footer is still the prompt block.
      prompt_at = if prompt_start
                    (prompt_start...footer_lines.size).select { |i| footer_lines[i].is_a?(String) }
                  else
                    []
                  end
      response_at = []
      if @resp_open && @resp
        rule_at = footer_lines.index(:rule)
        limit = rule_at || prompt_start || footer_lines.size
        (0...limit).each do |i|
          response_at << i unless footer_lines[i] == :rule
        end
      end
      footer_lines.each_index.map do |i|
        if prompt_at.include?(i) then :prompt
        elsif response_at.include?(i) then :response
        else :chrome
        end
      end
    end

    def toggle_collapse_at(index)
      return unless @rows[index]&.selectable?

      select_row(index)
      node = @rows[@sel]&.node
      return unless node&.item

      id = node.item.id
      return unless id && collapsible_children?(node)

      if @ui.collapsed.include?(id)
        expand_selected
      else
        @ui.collapsed.add(id)
        reselect(id)
      end
    end

    def apply_picker_hit(row_offset)
      case @ui.mode
      when :palette
        palette = @ui.action_palette
        resolve_palette(palette) { palette&.hit(row_offset) }
      when :context_palette
        result = @ui.context_palette&.hit(row_offset)
        return close_context_palette if result == :cancelled
        return unless result.is_a?(Array) && result.first == :apply

        apply_context_filter(result.last)
        close_context_palette
      when :form
        # Forms are not ChoicePicker-backed in this release.
        nil
      end
    end

    def scroll_popup_wheel(delta)
      case @ui.mode
      when :palette
        @ui.action_palette&.move(delta)
      when :context_palette
        @ui.context_palette&.move(delta)
      when :form
        # Forms are text fields, not option lists; still route through form_key
        # so :cancelled/:submitted are observed if the engine ever returns them.
        key = delta.negative? ? "\e[A" : "\e[B"
        delta.abs.times { form_key(key) }
      end
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
        if suspended_recovery_owns_input? && ["y", "\e"].include?(k)
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
      entry = Shortcuts.match(k, context, self)
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
      return unsupported_schema_key(k) if @ui.modal&.kind == :unsupported_schema
      return archive_confirm_key(k) if @ui.modal&.kind == :archive_confirm
      return archive_blocked_key(k) if @ui.modal&.kind == :archive_blocked
      return cancel_queued_agent_requests_key(k) if @ui.modal&.kind == :agent_queue_cancel_confirm
      return project_complete_confirm_key(k) if @ui.modal&.kind == :project_complete_confirm
      return project_archive_confirm_key(k) if @ui.modal&.kind == :project_archive_confirm
      return modal_key_starts_typing(k) if modal_key_starts_typing?(k)

      dispatch_action(k, :modal)
    end

    # Typing a character with no modal binding of its own (j/k/q/? etc. stay
    # reserved for scrolling/closing) opens the live filter immediately, so
    # `/` is only needed to resume editing an already-committed filter.
    def modal_key_starts_typing?(k)
      modal_filter_available? && !Shortcuts.match(k, :modal) && @ui.modal_filter_input.printable_key?(k)
    end

    def modal_key_starts_typing(k)
      modal_start_filter
      modal_filter_key(k)
    end

    # The notice has no action: nothing this build can do makes the store
    # readable, so the only key it honors is the one that dismisses it.
    def unsupported_schema_key(key)
      close_modal if ["\e", "q", "\r", "\n"].include?(key)
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
      selected_id = current_item&.id || current_project&.id
      target_missing = @ui.form.target_id && selected_id != @ui.form.target_id
      if target_missing || (@ui.form.return_mode == :modal && !@ui.modal)
        @ui.form = nil
        @ui.form_success = nil
      else
        @ui.mode = :form
      end
    end

    def palette_key(k)
      palette = @ui.action_palette
      resolve_palette(palette) { palette&.handle_key(k) }
    end

    # Keyboard and pointer reach palette actions through here, so a raising
    # action restores the palette with an error on both paths instead of taking
    # the event loop down on one of them.
    def resolve_palette(palette)
      entry = nil
      result = yield
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
    def project_selected? = !current_project.nil?
    def ordering_action_available?
      @ui.view == :outline && !active_filter && active_context_filters.empty? && !current_item.nil?
    end
    def recurrence_action_available?
      item = current_item
      !!(item && (!item.respond_to?(:state) || item.state != "PROPOSED") &&
         (item.scheduled || item.deadline))
    end
    def proposal_action_available?
      item = current_item
      !!(item&.respond_to?(:state) && item.state == "PROPOSED")
    end
    def link_action_available?
      task = current_task
      !!(task && !task.links.empty?)
    end
    # Delegation is an owner decision about accepted live work: a project header
    # has no marker, a PROPOSED task is still an undecided suggestion, and a
    # closed task's marker is inert provenance. TaskView#open? is exactly that
    # set (INBOX/TODO/NEXT/WAITING), and Store refuses the rest anyway — this
    # keeps the palette honest instead of listing an action that must fail.
    def delegation_action_available?
      task = current_task
      !!(current_project.nil? && task&.open?)
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
    def focus_prompt = @ui.mode = :prompt
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
        current_filters: @ui.context_filters
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
      palette.refresh_options(contexts: contexts, current_filters: @ui.context_filters)
      @ui.context_palette = palette
      @ui.mode = :context_palette
    end

    def apply_context_filter(contexts)
      previous = @ui.context_filters
      next_filters = Array(contexts).filter_map { |ctx| ContextPalette.normalize(ctx) }.uniq.sort
      @ui.context_filters = next_filters
      if next_filters.empty?
        flash(previous.empty? ? "no context filter" : "context filter cleared")
      else
        flash("contexts: #{next_filters.join(" + ")}")
      end
      rows
    end

    # Priority ladder: A is highest, nil (no cookie) lowest.
    PRIORITY_ORDER = ["A", "B", "C", nil].freeze

    def raise_priority = bump_priority(-1)
    def lower_priority = bump_priority(1)

    def bump_priority(delta)
      return needs_task if current_project

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
      return Tasks::MutationResult.new(status: missing_snapshot_status) unless snapshot

      result = @application.patch_task(Tasks::TaskPatch.from(snapshot, field: field, value: value,
                                                              history_label: label), today: today)
      absorb_own_write if result.ok?
      result
    end

    # A nil edit snapshot means one of two very different things: the task is
    # gone from a readable file, or the file itself failed its preflight check
    # (corrupt, half-written, mid-edit by another writer) and nothing can be
    # located in it. Only the first is "the task no longer exists"; the second
    # is the reopen case every other quick action already reports. Resolve which
    # one it was here, once, so no caller has to guess from :not_found alone.
    def missing_snapshot_status
      Tasks::Check.check(@paths.org).ok? ? :not_found : :unavailable
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
      operation_context = temporal_context
      operation_today = operation_context.local_date
      result = @application.update_task(
        command, today: operation_today, context: tui_operation_context(operation_context)
      )
      unless result.ok?
        reload_store
        return flash(ordering_failure_message(result, action))
      end

      absorb_own_write(operation_context)
      @ui.collapsed.delete(placement.parent_id) if action == :indent
      @ui.selected_id = item.id
      rows(read: @read_model, today: operation_today)
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
      return unavailable_ordering if ORDERING_HANDLERS.include?(entry.handler)
      return unavailable_delegation if DELEGATION_HANDLERS.include?(entry.handler)
    end

    # A consumed-but-unavailable delegation key still owes the reader a reason:
    # silently swallowing D on a proposal reads as a broken keyboard.
    def unavailable_delegation
      return needs_task if current_project

      item = current_item
      return flash("nothing selected") unless item
      return flash("approve the proposal first — a proposal can't be delegated") if item.state == "PROPOSED"

      flash("#{item.state.to_s.downcase} tasks can't be delegated")
    end

    # A project header is selected but the pressed action only applies to a task.
    # The key is still consumed (the caller returns), never falling through.
    def needs_task
      flash("select a task for that")
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
                                       today: method(:current_date), temporal_context: temporal_context)
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
      return needs_task if current_project

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
        operation_context = temporal_context
        operation_today = operation_context.local_date
        choice = raw.to_s.strip.downcase
        value = unless %w[someday now].include?(choice)
                  TaskEditForm.parse_temporal(raw, operation_today, context: temporal_context)
                end
        unless %w[someday now].include?(choice) || value
          next "can't parse “#{raw}”; use a date/time, someday, or now"
        end

        snapshot = @application.edit_snapshot(item.id)
        next "task no longer exists" unless snapshot

        # Rule 3: the lead already owns "hide until", so a one-off date here
        # would fight it. `someday` (an indefinite hold) and `now` (which
        # releases this occurrence) both stay available.
        if value && Tasks::Lead.span?(snapshot.lead)
          next "already hides until #{Tasks::Lead.describe(snapshot.lead)} its date — " \
               "edit Lead time, or clear it first"
        end

        changes, label = defer_until_changes(choice, value, item.title)
        command = Tasks::TaskChangeset.from(
          snapshot, changes: changes, history_label: label
        )
        result = @application.update_task(
          command, today: operation_today, context: tui_operation_context(operation_context)
        )
        unless result.ok?
          reload_store
          next result.conflict? ? "file changed underneath — reopen" : result.tui_message
        end
        absorb_own_write(operation_context)
        fresh_read = @read_model
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

    def defer_until_changes(choice, value, title)
      case choice
      when "someday"
        [{ deferred: true }, "on hold: #{title}"]
      when "now"
        [{ activate: true }, "activate: #{title}"]
      else
        [
          { deferred: false, scheduled: value },
          "defer until #{TaskEditForm.format_temporal(value)}: #{title}",
        ]
      end
    end

    def availability_flash(task, reader: read_model)
      return "task no longer exists" unless task
      return "▸ available now: #{task.title}" if task.available?

      blocker = task.availability_blocker_id && reader.task_for(task.availability_blocker_id)
      case task.availability_reason
      when :scheduled
        # The effective gate, which for a lead is a derived date the task
        # carries no stamp for.
        gate = task.gate_value || task.scheduled_value
        "⏳ #{task.title} unavailable until #{gate ? TaskEditForm.format_temporal(gate) : "its lead window"}"
      when :ancestor_scheduled
        date = task.gate_date&.iso8601 || blocker&.scheduled&.iso8601 || "a parent date"
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
      flash("agent: #{current_entry.ui_label}#{@agent_queue.work? ? " (applies to new requests)" : ""}")
    end

    def undo_last  = history_op(:undo!, "undid")
    def redo_last  = history_op(:redo!, "redid")

    def history_op(op, verb)
      kind, label = @store.public_send(op)
      case kind
      when :unsupported_schema then show_unsupported_schema_notice
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
      return needs_task if current_project

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
      return needs_task if current_project

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
      if (project = current_project)
        return project_detail? ? close_panel : show_project_detail(project)
      end
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

    # The right-panel project counterpart to show_detail: a ProjectDetails view
    # of the selected heading, refreshed like task detail as the cursor moves.
    def show_project_detail(project = current_project)
      return close_panel unless project

      refresh_project_detail_panel(project, content_width: detail_panel_content_width)
    end

    def refresh_project_detail_panel(project, content_width: nil)
      return close_panel unless project

      content_width ||= detail_panel_content_width
      if project_detail? &&
         @project_detail_id == project.id &&
         @project_detail_width == content_width &&
         @project_detail_model.equal?(@read_model)
        return
      end

      @project_detail_id = project.id
      @project_detail_width = content_width
      @project_detail_model = @read_model
      tasks = project.task_ids.filter_map { |id| read_model.task_for(id) }
      content = ProjectDetails.build(project, tasks, content_width, today: @read_model_today)
      if project_detail?
        @ui.panel.replace(title: content[:title], lines: content[:lines], identity: project.id)
      else
        @ui.panel = RightPanel.new(
          title: content[:title], lines: content[:lines], kind: :project_detail, identity: project.id
        )
      end
    end

    # Open the selected task's first link in the browser (`o`, list or detail
    # mode). Deliberately the FIRST link: notes lead with the primary reference;
    # the CLI (`tasks open <ref> <n>`) handles precise picking.
    def open_link
      return needs_task if current_project

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
        temporal_context: temporal_context,
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
      @project_detail_id = nil
      @project_detail_width = nil
      @project_detail_model = nil
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
            "e resumes · #{@task_edit_message}")
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
                 "switch view + e resumes · y copies · esc discards"
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
        flash("paused task draft selected — e resumes")
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
        absorb_own_write
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
    def project_detail? = @ui.panel&.kind == :project_detail
    def panel_detail? = detail_panel? || project_detail?

    # Keep whichever detail panel is open following the selection: a project row
    # shows project detail, a task row shows task detail, and a non-selectable
    # landing closes the panel. Navigation calls this so the panel swaps kind as
    # the cursor crosses between headings and their tasks.
    def refresh_open_panel
      if (project = current_project)
        show_project_detail(project)
      elsif current_item
        refresh_detail_panel
      else
        close_panel
      end
    end

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

    def selectable_indexes = @rows.each_index.select { |i| @rows[i].selectable? }

    def current_item = @rows[@sel]&.item

    # The selected project header's ProjectView, or nil on a task/header row.
    def current_project = @rows[@sel]&.project

    def current_task
      item = current_item
      item && read_model.task_for(item)
    end

    def select_row(index)
      id = @rows[index]&.id
      return if @sel == index && @ui.selected_id == id

      @sel = index
      @ui.selected_id = id
      refresh_open_panel if panel_detail?
    end

    def move(delta)
      sels = selectable_indexes
      return if sels.empty?
      cur = sels.index(@sel) || 0
      select_row(sels[(cur + delta).clamp(0, sels.size - 1)])
    end

    def clamp_selection
      # A mutation key handler (e.g. an archive sweep) can clear the row cache
      # without rebuilding it. loop_once calls this every tick, so rebuild rows
      # first when the cache is empty — otherwise reconcile against the rows the
      # caller already warmed, so a frozen mutation-day snapshot is preserved.
      return rows if @rows.nil?
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
      idx = @sel if @ui.selected_id && sels.include?(@sel) && @rows[@sel].id == @ui.selected_id
      idx ||= @ui.selected_id && sels.find { |i| @rows[i].id == @ui.selected_id }
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
          has_collapsible_children = if @ui.view == :outline && !active_filter && active_context_filters.empty?
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
      if @ui.view == :outline && !active_filter && active_context_filters.empty?
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
      return confirm_complete_project(current_project) if current_project

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

    def approve_proposal = decide_proposal(:approve)
    def reject_proposal = decide_proposal(:reject)

    def decide_proposal(action)
      item = current_item
      return flash("select a task pending approval") unless item&.proposed?

      proposal_ids = @rows.filter_map { |row| row.item&.id if row.item&.proposed? }
      proposal_index = proposal_ids.index(item.id)
      review_order = proposal_ids.rotate((proposal_index || -1) + 1)
      next_proposal_id = review_order.find { |id| id != item.id }
      result = @application.public_send(
        :"#{action}_task", item.id, expected_revision: current_task&.revision,
        today: current_date,
        context: tui_operation_context(temporal_context)
      )
      if result.ok?
        title = item.title
        absorb_own_write
        @ui.selected_id = next_proposal_id || (item.id if action == :approve)
        rows
        refresh_detail_panel if detail_panel?
        target = action == :approve ? "INBOX" : "CANCELLED"
        flash("#{action == :approve ? "approved" : "rejected"} → #{target}: #{title}")
      elsif result.conflict? && result.summary&.dig(:proposed_descendant_ids)
        flash("decide proposed descendants first")
      else
        reload_store
        flash(Array(result.errors).first || "proposal changed underneath — try again")
      end
    end

    def open_date_popup
      return needs_task if current_project

      item = current_item
      return flash("nothing selected") unless item

      target = item.deadline ? "Deadline" : item.scheduled ? "Available from" : "Deadline (new)"
      field = TaskEditForm::TemporalInput.new(
        key: :value, value: +"", label: "new #{target}",
        parser: ->(raw, today) { TaskEditForm.parse_temporal(raw, today, context: temporal_context) },
        formatter: TaskEditForm.method(:format_temporal), today: method(:current_date),
        expose_parse_errors: true,
      )
      @ui.form = Form.new(
        kind: :date, title: "edit date", prompt: "new #{target}",
        hint: "fri · tomorrow 9am · date time Zone · esc cancels", min_width: 50,
        return_mode: :list, target_id: item.id, field: field
      ) do |raw|
        operation_today = current_date
        value = TaskEditForm.parse_temporal(raw, operation_today, context: temporal_context)
        next "can't parse “#{raw}”" unless value
        kind = if item.deadline     then :deadline
               elsif item.scheduled then :scheduled
               else                      :deadline
               end
        label_value = TaskEditForm.format_temporal(value)
        result = patch_task(item, field: kind, value: value,
                            label: "reschedule → #{label_value}: #{item.title}",
                            today: operation_today)
        unless result.ok?
          reload_store
          next "file changed underneath — reopen"
        end

        @ui.form_success = lambda do
          promoted = item.state == "INBOX" ? " · INBOX → TODO" : ""
          flash("→ #{item.title}: #{label_value}#{promoted}")
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
      return needs_task if current_project

      item = current_item
      return flash("nothing selected") unless item
      return flash("add an Available from date or deadline first — recurrence needs a date") unless item.scheduled || item.deadline

      # The field is prefilled with the canonical cookie, so the suffix glosses
      # it rather than repeating it.
      current = item.recur ? "now #{Tasks::Recur.humanize(item.recur)}" : "not repeating"
      field = TermForm::Fields::Input.new(
        key: :value, value: item.recur || +"", label: "repeat",
      )
      # The stamp the schedule rides is the projection anchor, so the preview
      # dates are the dates completing the task would actually produce. Frozen
      # with the popup, like target_id: the preview must describe the task the
      # form will write to, not whatever is selected when a key lands.
      anchor = item.deadline || item.scheduled
      @ui.form = Form.new(
        kind: :recurrence, title: "recur", prompt: "repeat",
        hint: ->(raw, width) { recur_preview(raw, anchor: anchor, width: width) },
        min_width: RECUR_POPUP_WIDTH, return_mode: :list,
        initial: item.recur || +"", suffix: "(#{current})", target_id: item.id, field: field
      ) do |raw|
        result = Tasks::Recur.parse_result(raw)
        next result[:error] if result[:error]

        cookie = result[:canonical]
        label = cookie == :off ? "recur off: #{item.title}" : "recur #{cookie}: #{item.title}"
        patch = patch_task(item, field: :recurrence, value: cookie, label: label)
        unless patch.ok?
          reload_store if patch.stale? || patch.not_found?
          next recurrence_failure_message(patch)
        end

        @ui.form_success = lambda do
          flash(cookie == :off ? "↻ off: #{item.title}" : "↻ #{Tasks::Recur.humanize(cookie)}: #{item.title}")
          reselect(item.id)
          refresh_detail_panel if detail_panel?
        end
        nil
      end
      @ui.mode = :form
    end

    # What to type when nothing has been typed yet: one example per shape,
    # matching the `r` shortcut's own description.
    RECUR_POPUP_HINT = "weekly · every mon · m:15 · off · esc cancels"

    # The most dates the preview projects. The line drops whole dates from the
    # end when the popup is too narrow for all of them, so this is a ceiling,
    # not a promise.
    RECUR_PREVIEW_COUNT = 3

    # Popup floor, sized so a typical schedule's gloss and all three dates fit
    # on the footer line. A narrower terminal clamps it and the fit logic sheds
    # dates instead.
    RECUR_POPUP_WIDTH = 76

    # The recurrence popup's live footer: `Recur.explain` for whatever is in the
    # input right now, on one line. The engine's payload has three shapes and
    # each renders differently —
    #
    #   understood + projected  → "every Mon, Wed → 2026-07-29 Wed · …"
    #   understood, never fires → the canonical form, its gloss, and the reason
    #   not understood          → the parser's own reason, which names the fix
    #
    # plus the empty input, which shows the grammar instead of "no schedule
    # given". Pure computation on a parse of a short string, so it runs per
    # keystroke with no debounce. `width` is the cells the line may occupy, or
    # nil for an unbounded rendering.
    def recur_preview(raw, anchor:, width: nil)
      return RECUR_POPUP_HINT if raw.to_s.strip.empty?

      payload = Tasks::Recur.explain(raw, context: temporal_context, from: anchor,
                                     count: RECUR_PREVIEW_COUNT)
      human = payload[:human]
      return payload[:error] unless human
      return "#{payload[:canonical]} — #{human} — #{payload[:error]}" if payload[:error]

      dates = Array(payload[:next]).map { |date| "#{date.iso8601} #{date.strftime("%a")}" }
      dates.empty? ? human : fit_recur_preview(human, dates, width)
    end

    # Fit the projection to the footer by dropping whole dates from the end. A
    # date clipped mid-digit ("2026-08-0…") reads as a different, wrong date, so
    # the line sheds dates — down to none — rather than ever showing a partial
    # one. The gloss always leads; if the gloss alone overflows, the renderer's
    # ellipsis takes it, and no date is left to be misread.
    def fit_recur_preview(human, dates, width)
      shown = dates.dup
      until shown.empty?
        line = "#{human} → #{shown.join(" · ")}"
        return line if width.nil? || A.vislen(line) <= width

        shown.pop
      end
      human
    end

    # The store refuses schedules that would leave a task nothing can complete
    # (unreachable or unstorable), and those refusals are already user-facing
    # sentences — surface them verbatim rather than as a generic failure. Only
    # the genuinely-changed-underneath statuses get the TUI's reopen wording.
    def recurrence_failure_message(result)
      return "file changed underneath — reopen" if result.stale? || result.unavailable?
      return "task no longer exists" if result.not_found?

      result.errors.first || result.tui_message
    end

    # -- delegation ------------------------------------------------------------
    #
    # The owner rarely opens the edit panel, so every delegation operation is
    # reachable from one inline prompt on the selected row, exactly like `z` and
    # `r`. `D` is deliberately ONE form rather than four keys: the four owner
    # verbs are mutually exclusive states of a single field, and typing the
    # target state is shorter than remembering which key sets it.

    # The `D` grammar, in resolution order:
    #
    #   an email address       → delegate to that person (Application moves the
    #                            task to WAITING)
    #   anything else with "@" → a parse error naming the problem. `@` is the
    #                            context-filter key, so `@work` is one slip of
    #                            muscle memory away; it must not be treated as
    #                            a person and must not reach the Application at
    #                            all.
    #   a prefix (≥ DELEGATE_PREFIX_MIN chars) of exactly one word below
    #                          → that word's action
    #
    # The vocabulary is assembled from the shared definitions rather than
    # respelled: the modes are the schema's, and the clear words are the CLI's,
    # so `off`/`none` can never drift between the two surfaces.
    DELEGATE_CLEAR_WORDS = Tasks::DelegationCommand::CLEAR_WORDS
    DELEGATE_WORDS = (Tasks::Delegation::MODES + %w[release] + DELEGATE_CLEAR_WORDS).freeze
    DELEGATE_HINT = "pat@example.com · refine · research · implement · release · off · esc cancels"

    # Prefix matching starts here. The plan promises `ref` / `res` / `imp`, and
    # every word in the vocabulary is at least this long, so no promised
    # spelling is lost — but one stray character no longer performs the widest
    # or the most destructive action in the grammar. `i` used to delegate at
    # `implement`, and `o` / `n` used to revoke a live claim with no
    # confirmation; the shortest inputs must not be the ones that cost the most.
    DELEGATE_PREFIX_MIN = 3

    def delegate_selected
      task = delegation_target
      return unless task

      field = TermForm::Fields::Input.new(key: :value, value: +"", label: "delegate to")
      @ui.form = Form.new(
        kind: :delegate, title: "Delegate", prompt: "delegate to",
        hint: DELEGATE_HINT, min_width: 84, return_mode: :list,
        suffix: "(#{delegation_state_label(task)})", target_id: task.id, field: field
      ) do |raw|
        text = raw.to_s.strip
        action, argument = parse_delegation_input(text)
        next argument if action == :error

        run_delegation(task, action) do |id, operation_today, operation_context|
          case action
          when :human
            @application.delegate_task(id, kind: "human", assignee: argument,
                                           today: operation_today, context: operation_context)
          when :agent
            @application.delegate_task(id, kind: "agent", mode: argument,
                                           today: operation_today, context: operation_context)
          when :release
            # The owner's D-release is always a forced one: this prompt exists
            # to clear a claim the owner does not hold, and a worker releasing
            # its own claim uses the CLI with its worker id.
            @application.release_task(id, force: true, today: operation_today,
                                          context: operation_context)
          when :undelegate
            @application.undelegate_task(id, today: operation_today, context: operation_context)
          end
        end
      end
      @ui.mode = :form
    end

    # `W` records where the work lives. It is a property of the delegation, so
    # it refuses an undelegated task up front rather than opening a prompt whose
    # every input must fail — the same shape as `r` refusing an undated task.
    def set_work_ref_selected
      task = delegation_target
      return unless task
      unless task.delegated?
        return flash("delegate the task first — a work reference belongs to a delegation")
      end

      current = task.work_ref || +""
      field = TermForm::Fields::Input.new(key: :value, value: current.dup, label: "work ref")
      @ui.form = Form.new(
        kind: :work_ref, title: "Work reference", prompt: "work ref",
        hint: "url / ticket / session id · off clears · esc cancels", min_width: 60,
        return_mode: :list, initial: current.dup,
        suffix: "(#{current.empty? ? "none" : "now #{current}"})", target_id: task.id, field: field
      ) do |raw|
        text = raw.to_s.strip
        if text.empty?
          next "can't parse “#{raw}”; give a URL or id, or off to clear it"
        end

        clear = DELEGATE_CLEAR_WORDS.include?(text.downcase)
        run_delegation(task, :work_ref) do |id, operation_today, operation_context|
          @application.set_work_ref(id, clear ? nil : text,
                                    today: operation_today, context: operation_context)
        end
      end
      @ui.mode = :form
    end

    # The selected task as a canonical TaskView, or nil after flashing why this
    # row cannot be delegated. Mirrors defer_selected's guards, plus the
    # accepted-live-work rule delegation_action_available? gates the key on.
    def delegation_target
      if current_project
        needs_task
        return nil
      end
      item = current_item
      unless item
        flash("nothing selected")
        return nil
      end
      task = current_task
      unless task
        flash("task no longer exists")
        return nil
      end
      unless task.open?
        unavailable_delegation
        return nil
      end

      task
    end

    # Returns [action, argument] or [:error, message]. The empty string is
    # rejected before the prefix scan — every word starts with "", so an empty
    # input would otherwise report itself as ambiguous across all six.
    def parse_delegation_input(text)
      return [:error, delegation_parse_error(text)] if text.empty?
      return [:human, text] if Tasks::Delegation.email?(text)
      # A near-miss address is a typo, not a person, and the user deserves to
      # hear that here rather than as a Store refusal about a field they never
      # knew they were writing.
      return [:error, delegation_email_error(text)] if text.include?("@")

      matches = delegation_word_matches(text.downcase)
      case matches.length
      when 1 then delegation_word_action(matches.first)
      when 0 then [:error, delegation_parse_error(text)]
      else [:error, "“#{text}” matches #{matches.join(", ")} — type more of the word"]
      end
    end

    # Nothing shorter than DELEGATE_PREFIX_MIN matches anything, so `o`, `n`,
    # `i`, `r` and `re` all land on the unparseable message instead of silently
    # resolving to a word the user did not type.
    def delegation_word_matches(text)
      return [] if text.length < DELEGATE_PREFIX_MIN

      DELEGATE_WORDS.select { |word| word.start_with?(text) }
    end

    def delegation_word_action(word)
      case word
      when "release" then [:release, nil]
      when *DELEGATE_CLEAR_WORDS then [:undelegate, nil]
      else [:agent, word]
      end
    end

    def delegation_parse_error(text)
      "can't parse “#{text}”; use an email, refine/research/implement, release, or off"
    end

    def delegation_email_error(text)
      "“#{text}” isn't an email address — use pat@example.com"
    end

    # The shared submit body behind both prompts: run the operation against a
    # frozen task id, absorb our own write, and stage the flash + reselect for
    # after the form closes. Returns nil on success (Form's contract) or the
    # message the form should show inline.
    def run_delegation(task, action)
      operation_context = temporal_context
      operation_today = operation_context.local_date
      result = yield(task.id, operation_today, tui_operation_context(operation_context))
      unless result.ok?
        message = delegation_failure_message(result)
        reload_store
        # reload_store → restore_form detaches the prompt when its target row is
        # gone (deleted from another process mid-prompt). An orphaned Form's
        # inline error is never painted, so the refusal has to reach the user as
        # a flash instead of disappearing with the popup. Returning nil closes
        # the form cleanly — close_form is already a no-op once @ui.form is nil,
        # so no :form/nil soft-lock can appear here.
        return message if @ui.form

        flash(message)
        return nil
      end

      absorb_own_write(operation_context)
      message = delegation_flash(action, result, task.title)
      @ui.form_success = lambda do
        flash(message)
        reselect(task.id)
        refresh_detail_panel if detail_panel?
      end
      nil
    end

    # Store's refusals are already user-facing sentences ("task is DONE; only
    # accepted live tasks can be delegated", "already claimed by … at …"), so
    # they are surfaced verbatim; only the genuinely-changed-underneath statuses
    # get the TUI's own reopen wording.
    def delegation_failure_message(result)
      summary = result.summary || {}
      if result.conflict? && summary[:holder]
        return "already claimed by #{summary[:holder]} at #{summary[:at]} — off revokes it"
      end
      return "file changed underneath — reopen" if result.stale?
      return "task no longer exists" if result.not_found?

      result.errors.first || result.tui_message
    end

    # One flash vocabulary shared with the CLI's delegation headline, so the two
    # surfaces describe the same write the same way.
    def delegation_flash(action, result, title)
      summary = result.summary || {}
      if action == :work_ref
        reference = summary[:work_ref]
        return "work ref cleared: #{title}" unless reference

        return "work ref#{result.no_change? ? " already" : " →"} #{reference}: #{title}"
      end

      delegation = summary[:delegation]
      unless Tasks::Delegation.object?(delegation)
        return result.no_change? ? "not delegated: #{title}" : "undelegated: #{title}"
      end
      return "already #{delegation_label(delegation, summary[:state])}: #{title}" if result.no_change?

      prefix = action == :release ? "released · " : ""
      "#{prefix}#{delegation_label(delegation, summary[:state])}: #{title}"
    end

    def delegation_label(delegation, state = nil)
      case delegation["status"]
      when Tasks::Delegation::DELEGATED
        "delegated → #{delegation["assignee"]}#{state ? " (#{state})" : ""}"
      when Tasks::Delegation::READY
        "agent-ready (#{delegation["mode"]})"
      when Tasks::Delegation::CLAIMED
        "claimed by #{delegation["assignee"]} (#{delegation["mode"]})"
      else
        "delegated"
      end
    end

    # The `(now …)` suffix on the D prompt, matching the recur popup's shape.
    def delegation_state_label(task)
      task.delegated? ? "now #{delegation_label(task.delegation)}" : "not delegated"
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
      return show_unsupported_schema_notice if unsupported_schema?
      return confirm_archive_project(current_project) if current_project

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
          when :unsupported_schema
            show_unsupported_schema_notice
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

    # -- project actions -------------------------------------------------------

    # c on a project header: confirm, then close every open task in the section.
    def confirm_complete_project(project)
      return flash("select a project") unless project

      n = project.open_count
      @pending_project = project
      open_modal(
        {
          title: "Complete project",
          lines: [
            "Complete #{n} open task#{n == 1 ? "" : "s"} in #{project.title}?",
            "",
            T.paint(:muted, "Press y to complete · n / esc cancels"),
          ],
        },
        kind: :project_complete_confirm
      )
    end

    def project_complete_confirm_key(k)
      case k
      when "y", "Y", "\r", "\n"
        project = @pending_project
        @pending_project = nil
        close_modal
        result = @application.complete_project(project.id, today: current_date)
        unless result.ok?
          reload_store
          return flash("project no longer exists")
        end

        absorb_own_write
        closed = result.summary&.fetch(:closed, 0) || 0
        flash("✓ closed #{closed} in #{project.title}")
        reselect(project.id)
        refresh_open_panel if panel_detail?
      when "n", "N", "\e", "q"
        @pending_project = nil
        close_modal
        flash("complete cancelled")
      end
    end

    # x on a project header: confirm (surfacing open work), then archive the
    # whole section subtree.
    def confirm_archive_project(project)
      return flash("select a project") unless project

      n = project.open_count
      open_note = n.positive? ? " with #{n} open task#{n == 1 ? "" : "s"}" : ""
      @pending_project = project
      open_modal(
        {
          title: "Archive project",
          lines: [
            "Archive #{project.title}#{open_note}?",
            "",
            T.paint(:muted, "Press y to archive · n / esc cancels"),
          ],
        },
        kind: :project_archive_confirm
      )
    end

    def project_archive_confirm_key(k)
      case k
      when "y", "Y", "\r", "\n"
        project = @pending_project
        @pending_project = nil
        close_modal
        result = @application.archive_project(project.id)
        unless result.ok?
          reload_store
          return flash("project no longer exists")
        end

        absorb_own_write
        moved = result.summary&.fetch(:archived, nil) || result.touched_ids.size
        close_panel if project_detail? && @ui.panel&.identity == project.id
        flash("⤓ archived #{project.title} (#{moved})")
        rows
        clamp_selection
        refresh_open_panel if panel_detail?
      when "n", "N", "\e", "q"
        @pending_project = nil
        close_modal
        flash("archive cancelled")
      end
    end

    # e on a project header: rename via the single-field popup, prefilled with
    # the current title. Blank titles surface the form's own error path.
    def rename_project
      project = current_project
      return needs_task unless project

      field = TermForm::Fields::Input.new(key: :value, value: project.title, label: "title")
      @ui.form = Form.new(
        kind: :project_rename, title: "rename project", prompt: "title",
        hint: "esc cancels", min_width: 40, return_mode: :list,
        initial: project.title, target_id: project.id, field: field
      ) do |raw|
        result = @application.rename_project(project.id, title: raw)
        next "title cannot be blank" if result.invalid?
        unless result.ok?
          reload_store
          next "project no longer exists"
        end
        absorb_own_write

        @ui.form_success = lambda do
          flash("renamed: #{raw.strip}")
          reselect(project.id)
          refresh_open_panel if panel_detail?
        end
        nil
      end
      @ui.mode = :form
    end

    # a on a project header: capture a new TODO appended into the section.
    def capture_into_project
      project = current_project
      return needs_task unless project

      field = TermForm::Fields::Input.new(key: :value, value: +"", label: "task")
      @ui.form = Form.new(
        kind: :project_capture, title: "capture into “#{project.title}”", prompt: "task",
        hint: "esc cancels", min_width: 44, return_mode: :list,
        target_id: project.id, field: field
      ) do |raw|
        title = raw.to_s.strip
        next "task title cannot be blank" if title.empty?

        result = @application.create_task(
          { title: title, state: "TODO", parent_id: project.id }, today: current_date
        )
        unless result.ok?
          reload_store
          next result.errors.first || result.tui_message
        end
        absorb_own_write

        new_id = result.touched_ids.first
        @ui.form_success = lambda do
          flash("+ #{title}")
          reselect(new_id || project.id)
          refresh_open_panel if panel_detail?
        end
        nil
      end
      @ui.mode = :form
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
      elsif !@ui.context_filters.empty?
        @ui.context_filters = []
        flash("context filter cleared")
        rows
      elsif panel_detail?
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
