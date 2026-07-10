# frozen_string_literal: true

require "date"
require "set"
require_relative "ansi"
require_relative "theme"
require_relative "store"
require_relative "../tasks/quadrants"

module Tui
  # Builds the rows for each tab. A Row with an item is selectable
  # (actions apply to it); a Row with item: nil is a header/blank. In tree
  # mode a task Row also carries its Tasks::Tree node (nil for headers, blanks,
  # and every flat-mode row) so the App can answer hierarchy questions
  # (collapse/expand) without re-deriving them.
  #
  # Rows never name a color: builders paint text through Theme slots
  # (:context, :section, :muted, the due ladder, …) so the active theme and
  # any per-slot config overrides decide the final look. Frame highlights the
  # selected row by reversing the plain text over the :selection slot.
  module Views
    Row = Struct.new(:text, :item, :node)

    A = Ansi
    T = Theme

    TABS = [
      ["1 Agenda",    :agenda],
      ["2 Next",      :next],
      ["3 Quadrants", :quadrants],
      ["4 Inbox",     :inbox],
      ["5 Projects",  :projects],
    ].freeze

    # Visible width of the agenda date stamp ("MM-DD KIND (when....)"), so an
    # undated rider under a dated anchor blanks the same column and titles align.
    AGENDA_STAMP_W = 19

    # Collapse/expand markers, each exactly two terminal cells (▸/▾ are one cell
    # plus a trailing space — verified against Ansi.vislen). Every tree-mode task
    # row carries one so titles align regardless of whether a node has children.
    MARK_EXPANDED  = "▾ "
    MARK_COLLAPSED = "▸ "
    MARK_LEAF      = "  "

    # Canonical semantic query for both flat/filter and tree modes. It owns the
    # per-view eligibility, classification/grouping, and item ordering policy;
    # tree builders only decide which matching nodes become hierarchy anchors
    # and then render their contextual descendants in DFS order.
    class Query
      FAR_FUTURE = Date.new(9999, 12, 31)
      IDENTITY = ->(value) { value }

      attr_reader :view

      def initialize(view, today:, urgent_days:, show_deferred:, project_resolver: nil,
                     deferred_hidden_resolver: nil)
        @view = view
        @today = today
        @urgent_days = urgent_days
        @show_deferred = show_deferred
        @project_resolver = project_resolver
        @deferred_hidden_resolver = deferred_hidden_resolver
      end

      def eligible?(item)
        return false if !@show_deferred && deferred_hidden?(item)

        case view
        when :agenda    then item.open? && !!(item.deadline || item.scheduled)
        when :next      then item.state == "NEXT"
        when :quadrants then item.open?
        when :inbox     then item.state == "INBOX"
        when :projects
          project = project_for(item)
          item.open? && !project.nil? && project != "Inbox"
        else false
        end
      end

      def group_keys(item)
        case view
        when :next
          item.contexts.empty? ? ["(no context)"] : item.contexts
        when :quadrants
          [Tasks::Quadrants.of(item, today: @today, urgent_days: @urgent_days)]
        when :projects
          [project_for(item)]
        else
          [nil]
        end
      end

      def sort_key(item)
        case view
        when :agenda
          [item.deadline || item.scheduled, item.priority || "Z"]
        when :next
          [item.priority || "Z"]
        when :projects
          [item.deadline || item.scheduled || FAR_FUTURE, item.priority || "Z", item.title]
        else
          [item.line || Float::INFINITY]
        end
      end

      def select(entries, &item_for)
        item_for ||= IDENTITY
        entries.select { |entry| eligible?(item_for.call(entry)) }
      end

      def sort(entries, &item_for)
        item_for ||= IDENTITY
        entries.sort_by { |entry| sort_key(item_for.call(entry)) }
      end

      def grouped(entries, &item_for)
        item_for ||= IDENTITY
        groups = Hash.new { |h, key| h[key] = [] }
        select(entries, &item_for).each do |entry|
          group_keys(item_for.call(entry)).each { |key| groups[key] << entry }
        end
        groups.transform_values { |list| sort(list, &item_for) }
      end

      def sorted_groups(groups)
        groups.sort_by do |key, entries|
          if view == :projects
            items = entries.flat_map do |entry|
              resolved = yield entry
              resolved.is_a?(Array) ? resolved : [resolved]
            end
            [items.filter_map { |item| item.deadline || item.scheduled }.min || FAR_FUTURE, key]
          else
            [key.to_s]
          end
        end
      end

      private

      def project_for(item) = @project_resolver&.call(item)
      def deferred_hidden?(item) = @deferred_hidden_resolver&.call(item) || item.deferred?
    end

    module_function

    # `tree` nil → the flat builders (unchanged shape; this path serves `/`
    # filter mode). `tree` present (the Store#tree forest) → the outliner
    # builders, which nest each anchor's visible subtree under it with indent +
    # markers. `store` (always the live Store) lets the projects view resolve
    # each task's parent project regardless of path.
    def rows(view, items, tree: nil, collapsed: Set.new, show_deferred: false,
             today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS, store: nil)
      if view == :projects
        return projects(items, tree: tree, collapsed: collapsed, show_deferred: show_deferred,
                               today: today, store: store)
      end

      if tree
        ctx = { collapsed: collapsed, show_deferred: show_deferred, today: today, urgent_days: urgent_days }
        case view
        when :agenda    then agenda_tree(tree, **ctx)
        when :next      then next_tree(tree, **ctx)
        when :quadrants then quadrants_tree(tree, **ctx)
        when :inbox     then inbox_tree(tree, **ctx)
        else []
        end
      else
        case view
        when :agenda
          agenda(items, today: today, show_deferred: show_deferred, store: store)
        when :next
          next_actions(items, today: today, show_deferred: show_deferred, store: store)
        when :quadrants
          quadrants(items, today: today, urgent_days: urgent_days,
                            show_deferred: show_deferred, store: store)
        when :inbox
          inbox(items, show_deferred: show_deferred, store: store)
        else []
        end
      end
    end

    # -- flat builders (tree: nil) -------------------------------------------
    # These render exactly as before the outliner existed; the `/` filter view
    # relies on their output shape.

    def agenda(items, today: Date.today, show_deferred: true, store: nil)
      query = view_query(:agenda, today: today, show_deferred: show_deferred, store: store)
      query.sort(query.select(items)).map do |i|
        Row.new("#{agenda_stamp(i, today)} #{decorated_title(i)}#{badge(i)}", i)
      end
    end

    def next_actions(items, today: Date.today, show_deferred: true, store: nil)
      query = view_query(:next, today: today, show_deferred: show_deferred, store: store)
      by_ctx = query.grouped(items)
      rows = []
      query.sorted_groups(by_ctx) { |item| item }.each do |ctx, list|
        rows << Row.new(T.paint(:context, ctx), nil)
        list.each do |i|
          rows << Row.new("  #{next_body(i, today)}", i)
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    # Classification (importance/urgency) lives in Tasks::Quadrants so the CLI
    # and TUI agree; this just lays the four buckets out as rows.
    def quadrants(items, today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS,
                  show_deferred: true, store: nil)
      query = view_query(:quadrants, today: today, urgent_days: urgent_days,
                                     show_deferred: show_deferred, store: store)
      by_q = query.grouped(items)
      rows = []
      Tasks::Quadrants::LABELS.each do |key, label|
        rows << Row.new(T.paint(:section, label), nil)
        matched = by_q[key] || []
        if matched.empty?
          rows << Row.new(T.paint(:muted, "  —"), nil)
        else
          matched.each { |i| rows << Row.new("  #{quad_body(i)}", i) }
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    def inbox(items, show_deferred: true, store: nil)
      query = view_query(:inbox, show_deferred: show_deferred, store: store)
      matched = query.sort(query.select(items))
      return [Row.new(T.paint(:muted, "Inbox empty. ✨"), nil)] if matched.empty?
      matched.map { |i| Row.new("  #{inbox_body(i)}", i) }
    end

    # The projects view groups tasks under their enclosing project SECTION. In
    # tree mode (below) grouping is by project_section — the nearest SECTION
    # ancestor, climbing past every task ancestor — so a subtask of an open
    # parent task lands under its grandparent project, never as a pseudo-project
    # named after its parent. The header stats (open / NEXT counts, soonest date)
    # and the body rows are both derived from the SAME anchor traversal, so they
    # can never disagree. Flat/filter mode uses the same enclosing-SECTION
    # resolver, preventing a nested open task from becoming a pseudo-project.
    #
    # In tree mode the group body rides the outliner walker like the other four
    # tabs: a project's root tasks sort by date/priority/title, then each root's
    # own subtree renders depth-first beneath it (file order for descendants) so
    # thread-lines and bold containers line up with actual parent/child ties.
    # `tree` nil (the `/` filter path) falls back to the flat builder, which
    # sorts every descendant together by nearest date — no nesting.
    def projects(items, tree: nil, collapsed: Set.new, show_deferred: false, today: Date.today, store: nil)
      return [Row.new(T.paint(:muted, "Project data needs the task tree."), nil)] unless store
      return projects_flat(items, today: today, show_deferred: show_deferred, store: store) unless tree

      query = view_query(:projects, today: today, show_deferred: show_deferred, store: store)
      roots_by_project = query.grouped(anchor_roots(tree, show_deferred)) { |node| node.item }
      return [Row.new(T.paint(:muted, "No active projects."), nil)] if roots_by_project.empty?

      # Header stats derive from the SAME anchors plus their full visible
      # subtrees (the exact items the body will emit), so open/next counts and
      # the soonest date match the rows below by construction.
      items_for = lambda do |anchors|
        anchors.flat_map { |a| subtree_items(a, show_deferred) }
      end

      rows = query.sorted_groups(roots_by_project) { |node| subtree_items(node, show_deferred) }
             .flat_map do |name, anchors|
        rows = [Row.new(project_header(name, items_for.call(anchors), today), nil)]
        anchors.each do |anchor|
          append_subtree(rows, anchor, "  ", collapsed: collapsed, show_deferred: show_deferred) do |item|
            next_body(item, today)
          end
        end
        rows << Row.new("", nil)
        rows
      end
      rows.pop
      rows
    end

    # The enclosing project SECTION for a tree node — climbs past every task
    # ancestor (open or closed). A task's nearest open ancestor is useful in its
    # detail display, but Projects groups by the containing section in both flat
    # and tree modes so subtasks cannot become pseudo-projects.
    def project_section(node)
      a = node.parent
      a = a.parent while a && a.task?
      a
    end

    # The anchor's full visible subtree as a flat item list (the anchor itself
    # plus every visible descendant, respecting show_deferred) — the exact set of
    # tasks append_subtree would emit rows for, so project header stats computed
    # from this can't disagree with the body.
    def subtree_items(anchor, show_deferred)
      out = [anchor.item]
      visible_children(anchor, show_deferred).each do |c|
        out.concat(subtree_items(c, show_deferred))
      end
      out
    end

    # Pre-outliner flat Projects body: every open descendant of a project
    # flattened into one list sorted by nearest date/priority/title, each row a
    # fixed 2-space indent. Serves the `/` filter path (which always renders
    # flat) and shows no parent/child nesting.
    def projects_flat(items, today: Date.today, show_deferred: true, store: nil)
      query = view_query(:projects, today: today, show_deferred: show_deferred, store: store)
      groups = query.grouped(items)
      return [Row.new(T.paint(:muted, "No active projects."), nil)] if groups.empty?

      rows = query.sorted_groups(groups) { |item| item }
                   .flat_map do |name, list|
        rows = [Row.new(project_header(name, list, today), nil)]
        list.each { |i| rows << Row.new("  #{next_body(i, today)}", i) }
        rows << Row.new("", nil)
        rows
      end
      rows.pop
      rows
    end

    def project_header(name, list, today)
      open  = list.size
      nexts = list.count { |i| i.state == "NEXT" }
      head  = +"#{T.paint(:project, name)}  #{T.paint(:muted, "#{open} open")}"
      head << T.paint(nexts.zero? ? :warning : :muted, " · #{nexts} next")
      if (upcoming = next_project_date(list))
        head << T.paint(due_slot((upcoming - today).to_i), " · next #{upcoming.strftime("%m-%d")}")
      end
      head
    end

    # -- tree builders (tree: present) ---------------------------------------
    # Each picks its anchors, sorts/groups them by the anchor's own attributes,
    # then rides each anchor's visible subtree (open, un-deferred descendants)
    # indented beneath it. `today`/`urgent_days` are captured but only agenda
    # and quadrants read them.

    def agenda_tree(tree, collapsed:, show_deferred:, today:, urgent_days:)
      query = view_query(:agenda, today: today, urgent_days: urgent_days,
                                  show_deferred: show_deferred)
      anchors = anchor_roots(tree, show_deferred).select do |n|
        subtree_items(n, show_deferred).any? { |item| query.eligible?(item) }
      end
      anchors.sort_by! { |n| [agenda_anchor_date(n, show_deferred), n.item.priority || "Z"] }
      rows = []
      anchors.each do |anchor|
        append_subtree(rows, anchor, "", collapsed: collapsed, show_deferred: show_deferred) do |item|
          "#{agenda_stamp(item, today)} #{decorated_title(item)}#{badge(item)}"
        end
      end
      rows
    end

    def next_tree(tree, collapsed:, show_deferred:, today:, urgent_days:)
      query = view_query(:next, today: today, urgent_days: urgent_days,
                                show_deferred: show_deferred)
      matching = query.select(visible_nodes(tree, show_deferred)) { |node| node.item }
      anchors = matching.reject { |node| matching_ancestor?(node, query, show_deferred) }
      by_ctx = query.grouped(anchors) { |node| node.item }
      rows = []
      query.sorted_groups(by_ctx) { |node| node.item }.each do |ctx, list|
        rows << Row.new(T.paint(:context, ctx), nil)
        list.each do |anchor|
          append_subtree(rows, anchor, "  ", collapsed: collapsed, show_deferred: show_deferred) do |item|
            next_body(item, today)
          end
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    def quadrants_tree(tree, collapsed:, show_deferred:, today:, urgent_days:)
      query = view_query(:quadrants, today: today, urgent_days: urgent_days,
                                     show_deferred: show_deferred)
      anchors = query.select(anchor_roots(tree, show_deferred)) { |node| node.item }
      by_q = query.grouped(anchors) { |node| node.item }
      rows = []
      Tasks::Quadrants::LABELS.each do |key, label|
        rows << Row.new(T.paint(:section, label), nil)
        matched = by_q[key] || []
        if matched.empty?
          rows << Row.new(T.paint(:muted, "  —"), nil)
        else
          matched.each do |anchor|
            append_subtree(rows, anchor, "  ", collapsed: collapsed, show_deferred: show_deferred) do |item|
              quad_body(item)
            end
          end
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    def inbox_tree(tree, collapsed:, show_deferred:, today:, urgent_days:)
      query = view_query(:inbox, today: today, urgent_days: urgent_days,
                                 show_deferred: show_deferred)
      matching = query.select(visible_nodes(tree, show_deferred)) { |node| node.item }
      anchors = matching.reject { |node| matching_ancestor?(node, query, show_deferred) }
      anchors = query.sort(anchors) { |node| node.item }
      return [Row.new(T.paint(:muted, "Inbox empty. ✨"), nil)] if anchors.empty?
      rows = []
      anchors.each do |anchor|
        append_subtree(rows, anchor, "  ", collapsed: collapsed, show_deferred: show_deferred) do |item|
          inbox_body(item)
        end
      end
      rows
    end

    # -- walker --------------------------------------------------------------

    # Depth-first over the anchor's visible subtree, appending one Row per
    # visible node. `base` is the view's existing leading indent (kept so
    # top-level rows stay aligned); each level below the anchor drops a dim
    # thread-line (│ per level, :outline_thread) so a descendant reads as hanging
    # off its parent, then the marker column, then the per-view body the block
    # formats. A container row (marker ≠ MARK_LEAF, i.e. it has visible children)
    # has its body bolded (:outline_container) so it reads like a heading. A
    # collapsed node emits only its own row (marker ▸) with a muted hidden-count.
    def append_subtree(rows, anchor, base, collapsed:, show_deferred:, &body)
      subtree_rows(anchor, 0, collapsed: collapsed, show_deferred: show_deferred) do |node, depth, marker, folded|
        thread = depth.positive? ? T.paint(:outline_thread, "│ " * depth) : ""
        line = body.call(node.item)
        line = T.composite_over(:outline_container, line) unless marker == MARK_LEAF
        text = +""
        text << base << thread << marker << line
        text << T.paint(:muted, " (#{visible_descendant_count(node, show_deferred)})") if folded
        rows << Row.new(text, node.item, node)
      end
    end

    # Yields |node, depth, marker, folded| for the anchor and every visible
    # descendant, unless a node is collapsed (then it yields once, folded, and
    # its subtree is skipped). The anchor is assumed already visible.
    def subtree_rows(node, depth, collapsed:, show_deferred:, &blk)
      kids = visible_children(node, show_deferred)
      folded = !kids.empty? && collapsible?(node, collapsed)
      marker = kids.empty? ? MARK_LEAF : folded ? MARK_COLLAPSED : MARK_EXPANDED
      yield node, depth, marker, folded
      return if folded
      kids.each { |c| subtree_rows(c, depth + 1, collapsed: collapsed, show_deferred: show_deferred, &blk) }
    end

    # -- tree helpers --------------------------------------------------------

    # Top-level task nodes: every task with no task ancestor, however many
    # nested SECTIONS sit above it (a project sub-heading under "Projects" is
    # still a section, so its tasks are top-level too — only a task parent
    # makes a node non-top-level). Recurses through section children rather
    # than unwrapping one level, so a project-under-a-project heading doesn't
    # silently drop its tasks from every tree view (agenda/next/quadrants/
    # inbox/projects all seed their anchors from this).
    def top_level_task_nodes(tree)
      tree.flat_map { |root| root.section? ? top_level_task_nodes(root.children) : [root] }
    end

    # The anchor roots every tree view builds on: the roots of the maximal OPEN
    # subtrees, HOISTED through closed ancestors. Walking the whole forest, an
    # open (visible) task is an anchor root iff it doesn't render under an open
    # parent — i.e. it's top-level or its parent is closed (DONE/CANCELLED) or a
    # section. So an open task under a closed ancestor is promoted to anchor
    # level instead of vanishing with its pruned parent (the parent still prunes
    # itself; only the open descendants survive, each as its own anchor).
    #
    # Deferred-hiding wins over hoisting: once inside a deferred subtree that
    # show_deferred isn't revealing, nothing under it anchors — a deferred
    # project still defers its whole subtree, closed nodes and all. Order in the
    # result is DFS pre-order, so a hoisted anchor lands where its closed
    # ancestor sat; views re-sort/group as they already do.
    def anchor_roots(tree, show_deferred)
      roots = []
      walk = lambda do |node, hidden|
        return unless node.task?
        hidden ||= node.item.deferred? && !show_deferred
        if !hidden && node.item.open? && !(node.parent && visible?(node.parent, show_deferred))
          roots << node
        end
        node.children.each { |c| walk.call(c, hidden) }
      end
      top_level_task_nodes(tree).each { |n| walk.call(n, false) }
      roots
    end

    # A task node is visible iff it's open AND (deferred tasks are being shown OR
    # it isn't deferred). A hidden node takes its whole subtree with it — a
    # deferred project defers its subtasks, a closed one prunes them.
    def visible?(node, show_deferred)
      node.task? && node.item.open? && (show_deferred || !node.item.deferred?)
    end

    def visible_children(node, show_deferred)
      node.children.select { |c| visible?(c, show_deferred) }
    end

    # Every render-eligible node, hoisted: each anchor root plus its visible
    # subtree (pruning at the first non-visible child), ignoring collapse —
    # collapse hides rows, it doesn't change which nodes qualify as anchors.
    # Because the anchor roots partition the open nodes, every visible task
    # appears here exactly once (the every-open-task-once invariant the next and
    # inbox anchor rules rely on).
    def visible_nodes(tree, show_deferred)
      out = []
      walk = lambda do |node|
        out << node
        visible_children(node, show_deferred).each { |c| walk.call(c) }
      end
      anchor_roots(tree, show_deferred).each { |n| walk.call(n) }
      out
    end

    # Count of visible task rows the subtree would emit below `node` (what a
    # collapsed node hides). Respects deferred visibility; ignores collapse.
    def visible_descendant_count(node, show_deferred)
      visible_children(node, show_deferred).sum do |c|
        1 + visible_descendant_count(c, show_deferred)
      end
    end

    # Collapsible: the node carries an id (id-less rows can't be tracked) and
    # that id is in the collapsed set.
    def collapsible?(node, collapsed)
      id = node.item&.id
      !id.nil? && collapsed.include?(id)
    end

    # The date the agenda sorts an anchor by: its own deadline/scheduled if it
    # has one, else the earliest date among its visible dated descendants.
    def agenda_anchor_date(node, show_deferred)
      own = node.item.deadline || node.item.scheduled
      return own if own
      subtree_dates(node, show_deferred).min
    end

    def subtree_dates(node, show_deferred)
      dates = []
      d = node.item.deadline || node.item.scheduled
      dates << d if d
      visible_children(node, show_deferred).each { |c| dates.concat(subtree_dates(c, show_deferred)) }
      dates
    end

    # Whether some ancestor WITHIN THE SAME rendered subtree matches the same
    # canonical view query. NEXT and INBOX use this maximal-match rule so a
    # matching descendant rides its matching ancestor's subtree instead of
    # anchoring a duplicate group. A closed/deferred-hidden ancestor stops the
    # search because it also breaks the rendered subtree.
    def matching_ancestor?(node, query, show_deferred)
      a = node.parent
      while a && visible?(a, show_deferred)
        return true if query.eligible?(a.item)
        a = a.parent
      end
      false
    end

    # -- per-item line bodies (shared by flat + tree builders) ---------------

    # The agenda date stamp for a dated item, or a blank column of the same
    # width for an undated rider so its title lines up under dated siblings.
    def agenda_stamp(item, today)
      d = item.deadline || item.scheduled
      return " " * AGENDA_STAMP_W unless d
      kind = item.deadline ? "DUE " : "STRT"
      days = (d - today).to_i
      when_s = days.negative? ? "#{-days}d ago" : days.zero? ? "today" : "in #{days}d"
      T.paint(due_slot(days), "#{d.strftime("%m-%d")} #{kind} #{("(" + when_s + ")").ljust(8)}")
    end

    def next_body(item, today)
      due = short_due(item, today)
      "#{pri(item)}#{T.paint(:title, item.title)}#{due.empty? ? "" : "  #{due}"}#{badge(item)}"
    end

    def quad_body(item)  = "#{pri(item)}#{T.paint(:title, item.title)}#{badge(item)}"
    def inbox_body(item) = "#{T.paint(:title, item.title)}#{badge(item)}"

    # -- shared bits ---------------------------------------------------------

    # The urgency ladder as theme slots; Modals reuses it for dates.
    def due_slot(days)
      if    days <= 0 then :due_overdue
      elsif days <= 2 then :due_soon
      elsif days <= 7 then :due_week
      else                 :due_far
      end
    end

    def short_due(item, today)
      return "" unless item.deadline
      days = (item.deadline - today).to_i
      T.paint(due_slot(days), "#{item.deadline.month}/#{item.deadline.day}")
    end

    def pri(item) = item.priority ? T.paint(:priority, "[#{item.priority}] ") : ""

    # Trailing markers for a task: ↻ recurring, ⏸ deferred. Deferred tasks only
    # reach these builders when the App has Z-revealed them, so mark them
    # unconditionally.
    def badge(item)
      b = +""
      b << T.paint(:muted, " ↻") if item.recurring?
      b << T.paint(:muted, " ⏸") if item.deferred?
      b
    end

    def decorated_title(item)
      ctx = item.contexts
      "#{pri(item)}#{T.paint(:title, item.title)}#{ctx.empty? ? "" : "  " + ctx.map { |c| T.paint(:context, c) }.join(" ")}"
    end

    def view_query(view, today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS,
                   show_deferred: true, store: nil)
      resolver = ->(item) { project_name(item, store) } if view == :projects
      hidden_resolver = ->(item) { deferred_hidden?(item, store) } if store
      Query.new(view, today: today, urgent_days: urgent_days, show_deferred: show_deferred,
                     project_resolver: resolver, deferred_hidden_resolver: hidden_resolver)
    end

    # Flat/filter mode has no subtree walker, so resolve deferred visibility
    # through the item's live ancestry. This matches tree mode's rule that a
    # deferred parent hides its whole subtree until Z reveals it.
    def deferred_hidden?(item, store)
      node = store&.node_for(item)
      while node&.task?
        return true if node.item.deferred?
        node = node.parent
      end
      false
    end

    def project_name(item, store)
      return nil unless store
      node = store.node_for(item)
      project_section(node)&.title if node
    end

    def next_project_date(items)
      items.filter_map { |i| i.deadline || i.scheduled }.min
    end
  end
end
