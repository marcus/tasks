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
    FallbackAvailability = Data.define(
      :available, :availability_reason, :availability_blocker_id, :scheduled
    ) do
      def available? = available
    end

    A = Ansi
    T = Theme

    TABS = [
      ["1 Agenda",    :agenda],
      ["2 Next",      :next],
      ["3 Quadrants", :quadrants],
      ["4 Inbox",     :inbox],
      ["5 Projects",  :projects],
      ["6 Outline",   :outline],
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
                     availability_resolver: nil)
        @view = view
        @today = today
        @urgent_days = urgent_days
        @show_deferred = show_deferred
        @project_resolver = project_resolver
        @availability_resolver = availability_resolver
      end

      def eligible?(item)
        return false if !@show_deferred && unavailable?(item)

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
      def unavailable?(item) = @availability_resolver ? !@availability_resolver.call(item).available? : item.deferred?
    end

    module_function

    # `tree` nil → the flat builders (unchanged shape; this path serves `/`
    # filter mode). `tree` present (the Store#tree forest) → the outliner
    # builders, which nest each anchor's visible subtree under it with indent +
    # markers. `reader` supplies immutable tree lookups so the projects view
    # can resolve each task's parent project regardless of path. `store:` is
    # retained as a compatibility spelling for direct unit callers.
    def rows(view, items, tree: nil, collapsed: Set.new, show_deferred: false,
             today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS,
             reader: nil, store: nil)
      reader ||= store
      if view == :outline
        return outline(items, tree: tree, collapsed: collapsed, today: today, reader: reader)
      end
      if view == :projects
        return projects(items, tree: tree, collapsed: collapsed, show_deferred: show_deferred,
                               today: today, reader: reader)
      end

      if tree
        ctx = { collapsed: collapsed, show_deferred: show_deferred, today: today, urgent_days: urgent_days }
        case view
        when :agenda    then agenda_tree(tree, reader: reader, **ctx)
        when :next      then next_tree(tree, reader: reader, **ctx)
        when :quadrants then quadrants_tree(tree, reader: reader, **ctx)
        when :inbox     then inbox_tree(tree, reader: reader, **ctx)
        else []
        end
      else
        case view
        when :agenda
          agenda(items, today: today, show_deferred: show_deferred, reader: reader)
        when :next
          next_actions(items, today: today, show_deferred: show_deferred, reader: reader)
        when :quadrants
          quadrants(items, today: today, urgent_days: urgent_days,
                            show_deferred: show_deferred, reader: reader)
        when :inbox
          inbox(items, today: today, show_deferred: show_deferred, reader: reader)
        else []
        end
      end
    end

    # -- flat builders (tree: nil) -------------------------------------------
    # These render exactly as before the outliner existed; the `/` filter view
    # relies on their output shape.

    # The structural Outline is the only reorder-safe TUI surface. With a tree
    # it renders every live record in canonical DFS order: section rows are
    # non-selectable and task rows retain their real nodes. The flat path is
    # used only while `/` or `@` filtering is active; ordering is gated there,
    # so it deliberately shows just the matching tasks without pretending the
    # reduced list is a structural outline.
    def outline(items, tree: nil, collapsed: Set.new, today: Date.today, reader: nil)
      unless tree
        return items.map do |item|
          Row.new("  #{outline_body(item, today: today, reader: reader)}", item)
        end
      end

      rows = []
      tree.each { |node| append_outline_node(rows, node, 0, collapsed: collapsed,
                                                             today: today, reader: reader) }
      rows
    end

    def append_outline_node(rows, node, depth, collapsed:, today:, reader:)
      indent = "  " * depth
      if node.section?
        rows << Row.new("#{indent}#{T.paint(:section, node.title)}", nil)
        node.children.each do |child|
          append_outline_node(rows, child, depth + 1, collapsed: collapsed,
                                                        today: today, reader: reader)
        end
        return
      end

      folded = node.item.id && collapsed.include?(node.item.id) && !node.children.empty?
      marker = if node.children.empty?
                 MARK_LEAF
               elsif folded
                 MARK_COLLAPSED
               else
                 MARK_EXPANDED
               end
      body = outline_body(node.item, today: today, reader: reader)
      body = T.composite_over(:outline_container, body) unless node.children.empty?
      text = +"#{indent}#{marker}#{body}"
      text << T.paint(:muted, " (#{outline_descendant_count(node)})") if folded
      rows << Row.new(text, node.item, node)
      return if folded

      node.children.each do |child|
        append_outline_node(rows, child, depth + 1, collapsed: collapsed,
                                                      today: today, reader: reader)
      end
    end

    def outline_descendant_count(node)
      node.children.sum do |child|
        (child.task? ? 1 : 0) + outline_descendant_count(child)
      end
    end

    def outline_body(item, today:, reader:)
      state_slot = item.open? ? :accent : :muted
      "#{T.paint(state_slot, item.state.ljust(9))} #{decorated_title(item)}" \
        "#{badge(item, reader: reader, today: today)}"
    end

    def agenda(items, today: Date.today, show_deferred: true, reader: nil, store: nil)
      query = view_query(:agenda, today: today, show_deferred: show_deferred, reader: reader || store)
      query.sort(query.select(items)).map do |i|
        Row.new("#{agenda_stamp(i, today)} #{decorated_title(i)}#{badge(i, reader: reader || store, today: today)}", i)
      end
    end

    def next_actions(items, today: Date.today, show_deferred: true, reader: nil, store: nil)
      query = view_query(:next, today: today, show_deferred: show_deferred, reader: reader || store)
      by_ctx = query.grouped(items)
      rows = []
      query.sorted_groups(by_ctx) { |item| item }.each do |ctx, list|
        rows << Row.new(T.paint(:context, ctx), nil)
        list.each do |i|
          rows << Row.new("  #{next_body(i, today, reader: reader || store)}", i)
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    # Classification (importance/urgency) lives in Tasks::Quadrants so the CLI
    # and TUI agree; this just lays the four buckets out as rows.
    def quadrants(items, today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS,
                  show_deferred: true, reader: nil, store: nil)
      query = view_query(:quadrants, today: today, urgent_days: urgent_days,
                                     show_deferred: show_deferred, reader: reader || store)
      by_q = query.grouped(items)
      rows = []
      Tasks::Quadrants::LABELS.each do |key, label|
        rows << Row.new(T.paint(:section, label), nil)
        matched = by_q[key] || []
        if matched.empty?
          rows << Row.new(T.paint(:muted, "  —"), nil)
        else
          matched.each { |i| rows << Row.new("  #{quad_body(i, reader: reader || store, today: today)}", i) }
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    def inbox(items, today: Date.today, show_deferred: true, reader: nil, store: nil)
      query = view_query(:inbox, today: today, show_deferred: show_deferred, reader: reader || store)
      matched = query.sort(query.select(items))
      return [Row.new(T.paint(:muted, "Inbox empty. ✨"), nil)] if matched.empty?
      matched.map { |i| Row.new("  #{inbox_body(i, reader: reader || store, today: today)}", i) }
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
    def projects(items, tree: nil, collapsed: Set.new, show_deferred: false, today: Date.today,
                 reader: nil, store: nil)
      reader ||= store
      return [Row.new(T.paint(:muted, "Project data needs the task tree."), nil)] unless reader
      return projects_flat(items, today: today, show_deferred: show_deferred, reader: reader) unless tree

      query = view_query(:projects, today: today, show_deferred: show_deferred, reader: reader)
      roots_by_project = query.grouped(anchor_roots(tree, show_deferred, reader: reader, today: today)) { |node| node.item }
      return [Row.new(T.paint(:muted, "No active projects."), nil)] if roots_by_project.empty?

      # Header stats derive from the SAME anchors plus their full visible
      # subtrees (the exact items the body will emit), so open/next counts and
      # the soonest date match the rows below by construction.
      items_for = lambda do |anchors|
        anchors.flat_map { |a| subtree_items(a, show_deferred, reader: reader, today: today) }
      end

      rows = query.sorted_groups(roots_by_project) do |node|
        subtree_items(node, show_deferred, reader: reader, today: today)
      end
             .flat_map do |name, anchors|
        rows = [Row.new(project_header(name, items_for.call(anchors), today), nil)]
        anchors.each do |anchor|
          append_subtree(rows, anchor, "  ", collapsed: collapsed, show_deferred: show_deferred,
                         reader: reader, today: today) do |item|
            next_body(item, today, reader: reader)
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
    def subtree_items(anchor, show_deferred, reader: nil, today: Date.today)
      out = [anchor.item]
      visible_children(anchor, show_deferred, reader: reader, today: today).each do |c|
        out.concat(subtree_items(c, show_deferred, reader: reader, today: today))
      end
      out
    end

    # Pre-outliner flat Projects body: every open descendant of a project
    # flattened into one list sorted by nearest date/priority/title, each row a
    # fixed 2-space indent. Serves the `/` filter path (which always renders
    # flat) and shows no parent/child nesting.
    def projects_flat(items, today: Date.today, show_deferred: true, reader: nil, store: nil)
      query = view_query(:projects, today: today, show_deferred: show_deferred, reader: reader || store)
      groups = query.grouped(items)
      return [Row.new(T.paint(:muted, "No active projects."), nil)] if groups.empty?

      rows = query.sorted_groups(groups) { |item| item }
                   .flat_map do |name, list|
        rows = [Row.new(project_header(name, list, today), nil)]
        list.each { |i| rows << Row.new("  #{next_body(i, today, reader: reader || store)}", i) }
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

    def agenda_tree(tree, collapsed:, show_deferred:, today:, urgent_days:, reader: nil)
      query = view_query(:agenda, today: today, urgent_days: urgent_days,
                                  show_deferred: show_deferred, reader: reader)
      anchors = anchor_roots(tree, show_deferred, reader: reader, today: today).select do |n|
        subtree_items(n, show_deferred, reader: reader, today: today).any? { |item| query.eligible?(item) }
      end
      anchors.sort_by! do |n|
        [agenda_anchor_date(n, show_deferred, reader: reader, today: today), n.item.priority || "Z"]
      end
      rows = []
      anchors.each do |anchor|
        append_subtree(rows, anchor, "", collapsed: collapsed, show_deferred: show_deferred,
                       reader: reader, today: today) do |item|
          "#{agenda_stamp(item, today)} #{decorated_title(item)}#{badge(item, reader: reader, today: today)}"
        end
      end
      rows
    end

    def next_tree(tree, collapsed:, show_deferred:, today:, urgent_days:, reader: nil)
      query = view_query(:next, today: today, urgent_days: urgent_days,
                                show_deferred: show_deferred, reader: reader)
      matching = query.select(visible_nodes(tree, show_deferred, reader: reader, today: today)) { |node| node.item }
      anchors = matching.reject do |node|
        matching_ancestor?(node, query, show_deferred, reader: reader, today: today)
      end
      by_ctx = query.grouped(anchors) { |node| node.item }
      rows = []
      query.sorted_groups(by_ctx) { |node| node.item }.each do |ctx, list|
        rows << Row.new(T.paint(:context, ctx), nil)
        list.each do |anchor|
          append_subtree(rows, anchor, "  ", collapsed: collapsed, show_deferred: show_deferred,
                         reader: reader, today: today) do |item|
            next_body(item, today, reader: reader)
          end
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    def quadrants_tree(tree, collapsed:, show_deferred:, today:, urgent_days:, reader: nil)
      query = view_query(:quadrants, today: today, urgent_days: urgent_days,
                                     show_deferred: show_deferred, reader: reader)
      anchors = query.select(anchor_roots(tree, show_deferred, reader: reader, today: today)) { |node| node.item }
      by_q = query.grouped(anchors) { |node| node.item }
      rows = []
      Tasks::Quadrants::LABELS.each do |key, label|
        rows << Row.new(T.paint(:section, label), nil)
        matched = by_q[key] || []
        if matched.empty?
          rows << Row.new(T.paint(:muted, "  —"), nil)
        else
          matched.each do |anchor|
            append_subtree(rows, anchor, "  ", collapsed: collapsed, show_deferred: show_deferred,
                           reader: reader, today: today) do |item|
              quad_body(item, reader: reader, today: today)
            end
          end
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    def inbox_tree(tree, collapsed:, show_deferred:, today:, urgent_days:, reader: nil)
      query = view_query(:inbox, today: today, urgent_days: urgent_days,
                                 show_deferred: show_deferred, reader: reader)
      matching = query.select(visible_nodes(tree, show_deferred, reader: reader, today: today)) { |node| node.item }
      anchors = matching.reject do |node|
        matching_ancestor?(node, query, show_deferred, reader: reader, today: today)
      end
      anchors = query.sort(anchors) { |node| node.item }
      return [Row.new(T.paint(:muted, "Inbox empty. ✨"), nil)] if anchors.empty?
      rows = []
      anchors.each do |anchor|
        append_subtree(rows, anchor, "  ", collapsed: collapsed, show_deferred: show_deferred,
                       reader: reader, today: today) do |item|
          inbox_body(item, reader: reader, today: today)
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
    def append_subtree(rows, anchor, base, collapsed:, show_deferred:, reader: nil,
                       today: Date.today, &body)
      subtree_rows(anchor, 0, collapsed: collapsed, show_deferred: show_deferred,
                              reader: reader, today: today) do |node, depth, marker, folded|
        thread = depth.positive? ? T.paint(:outline_thread, "│ " * depth) : ""
        line = body.call(node.item)
        line = T.composite_over(:outline_container, line) unless marker == MARK_LEAF
        text = +""
        text << base << thread << marker << line
        if folded
          count = visible_descendant_count(node, show_deferred, reader: reader, today: today)
          text << T.paint(:muted, " (#{count})")
        end
        rows << Row.new(text, node.item, node)
      end
    end

    # Yields |node, depth, marker, folded| for the anchor and every visible
    # descendant, unless a node is collapsed (then it yields once, folded, and
    # its subtree is skipped). The anchor is assumed already visible.
    def subtree_rows(node, depth, collapsed:, show_deferred:, reader: nil,
                     today: Date.today, &blk)
      kids = visible_children(node, show_deferred, reader: reader, today: today)
      folded = !kids.empty? && collapsible?(node, collapsed)
      marker = kids.empty? ? MARK_LEAF : folded ? MARK_COLLAPSED : MARK_EXPANDED
      yield node, depth, marker, folded
      return if folded
      kids.each do |child|
        subtree_rows(child, depth + 1, collapsed: collapsed, show_deferred: show_deferred,
                                       reader: reader, today: today, &blk)
      end
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
    def anchor_roots(tree, show_deferred, reader: nil, today: Date.today)
      roots = []
      walk = lambda do |node|
        return unless node.task?
        visible = visible?(node, show_deferred, reader: reader, today: today)
        parent_visible = node.parent && visible?(node.parent, show_deferred, reader: reader, today: today)
        if visible && !parent_visible
          roots << node
        end
        node.children.each { |child| walk.call(child) }
      end
      top_level_task_nodes(tree).each { |node| walk.call(node) }
      roots
    end

    # A task node is visible iff it's open AND (deferred tasks are being shown OR
    # it isn't deferred). A hidden node takes its whole subtree with it — a
    # deferred project defers its subtasks, a closed one prunes them.
    def visible?(node, show_deferred, reader: nil, today: Date.today)
      node.task? && node.item.open? &&
        (show_deferred || if reader
                            available?(node.item, reader: reader, today: today)
                          else
                            node_available?(node, today: today)
                          end)
    end

    def node_available?(node, today: Date.today)
      candidates = []
      current = node
      while current
        candidates << current.item if current.task? && current.item
        current = current.parent
      end
      candidates.none?(&:deferred?) &&
        candidates.none? { |candidate| candidate.scheduled && candidate.scheduled > today }
    end

    def visible_children(node, show_deferred, reader: nil, today: Date.today)
      node.children.select do |child|
        visible?(child, show_deferred, reader: reader, today: today)
      end
    end

    # Every render-eligible node, hoisted: each anchor root plus its visible
    # subtree (pruning at the first non-visible child), ignoring collapse —
    # collapse hides rows, it doesn't change which nodes qualify as anchors.
    # Because the anchor roots partition the open nodes, every visible task
    # appears here exactly once (the every-open-task-once invariant the next and
    # inbox anchor rules rely on).
    def visible_nodes(tree, show_deferred, reader: nil, today: Date.today)
      out = []
      walk = lambda do |node|
        out << node
        visible_children(node, show_deferred, reader: reader, today: today).each { |child| walk.call(child) }
      end
      anchor_roots(tree, show_deferred, reader: reader, today: today).each { |node| walk.call(node) }
      out
    end

    # Count of visible task rows the subtree would emit below `node` (what a
    # collapsed node hides). Respects deferred visibility; ignores collapse.
    def visible_descendant_count(node, show_deferred, reader: nil, today: Date.today)
      visible_children(node, show_deferred, reader: reader, today: today).sum do |child|
        1 + visible_descendant_count(child, show_deferred, reader: reader, today: today)
      end
    end

    # Collapsible: the node carries an id (id-less rows can't be tracked) and
    # that id is in the collapsed set.
    def collapsible?(node, collapsed)
      id = node.item&.id
      !id.nil? && collapsed.include?(id)
    end

    # The date the agenda sorts an anchor by: the earliest deadline-first date
    # anywhere in its visible subtree. An anchor's later own date must not hide
    # an earlier qualifying descendant date.
    def agenda_anchor_date(node, show_deferred, reader: nil, today: Date.today)
      subtree_dates(node, show_deferred, reader: reader, today: today).min
    end

    def subtree_dates(node, show_deferred, reader: nil, today: Date.today)
      dates = []
      d = node.item.deadline || node.item.scheduled
      dates << d if d
      visible_children(node, show_deferred, reader: reader, today: today).each do |child|
        dates.concat(subtree_dates(child, show_deferred, reader: reader, today: today))
      end
      dates
    end

    # Whether some ancestor WITHIN THE SAME rendered subtree matches the same
    # canonical view query. NEXT and INBOX use this maximal-match rule so a
    # matching descendant rides its matching ancestor's subtree instead of
    # anchoring a duplicate group. A closed/deferred-hidden ancestor stops the
    # search because it also breaks the rendered subtree.
    def matching_ancestor?(node, query, show_deferred, reader: nil, today: Date.today)
      a = node.parent
      while a && visible?(a, show_deferred, reader: reader, today: today)
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
      kind = item.deadline ? "DUE " : "AVL "
      days = (d - today).to_i
      when_s = days.negative? ? "#{-days}d ago" : days.zero? ? "today" : "in #{days}d"
      T.paint(due_slot(days), "#{d.strftime("%m-%d")} #{kind} #{("(" + when_s + ")").ljust(8)}")
    end

    def next_body(item, today, reader: nil)
      due = short_due(item, today)
      "#{pri(item)}#{T.paint(:title, item.title)}#{due.empty? ? "" : "  #{due}"}#{badge(item, reader: reader, today: today)}"
    end

    def quad_body(item, reader: nil, today: Date.today)
      "#{pri(item)}#{T.paint(:title, item.title)}#{badge(item, reader: reader, today: today)}"
    end

    def inbox_body(item, reader: nil, today: Date.today)
      "#{T.paint(:title, item.title)}#{badge(item, reader: reader, today: today)}"
    end

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

    # Trailing availability markers are deliberately distinct: timed deferral
    # carries the release date, indefinite On Hold carries the pause glyph, and
    # an up-arrow identifies a blocker inherited from an ancestor.
    def badge(item, reader: nil, today: Date.today)
      b = +""
      b << T.paint(:muted, " ↻") if item.recurring?
      availability = availability_for(item, reader: reader, today: today)
      case availability.availability_reason
      when :scheduled
        b << T.paint(:muted, " ⏳ #{availability_date(item, availability, reader)&.strftime("%-m/%-d")}")
      when :ancestor_scheduled
        b << T.paint(:muted, " ⏳ #{availability_date(item, availability, reader)&.strftime("%-m/%-d")} ↑")
      when :on_hold
        b << T.paint(:muted, " ⏸")
      when :ancestor_on_hold
        b << T.paint(:muted, " ⏸ ↑")
      end
      b
    end

    def decorated_title(item)
      ctx = item.contexts
      "#{pri(item)}#{T.paint(:title, item.title)}#{ctx.empty? ? "" : "  " + ctx.map { |c| T.paint(:context, c) }.join(" ")}"
    end

    def view_query(view, today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS,
                   show_deferred: true, reader: nil, store: nil)
      reader ||= store
      resolver = ->(item) { project_name(item, reader) } if view == :projects
      availability_resolver = ->(item) { availability_for(item, reader: reader, today: today) }
      Query.new(view, today: today, urgent_days: urgent_days, show_deferred: show_deferred,
                     project_resolver: resolver, availability_resolver: availability_resolver)
    end

    def available?(item, reader: nil, today: Date.today)
      availability_for(item, reader: reader, today: today).available?
    end

    # App presentation always takes this path through TaskReadModel's canonical
    # TaskView. The fallback keeps direct renderer unit tests useful with a raw
    # read-only Store while matching the same ancestor precedence rules.
    def availability_for(item, reader: nil, today: Date.today)
      if reader&.respond_to?(:task_for)
        canonical = reader.task_for(item)
        return canonical if canonical
      end

      return FallbackAvailability.new(
        available: false, availability_reason: :closed,
        availability_blocker_id: nil, scheduled: nil
      ) unless item.open?

      candidates = []
      node = reader&.respond_to?(:node_for) ? reader.node_for(item) : nil
      while node&.task?
        candidates << node.item
        node = node.parent
      end
      candidates = [item] if candidates.empty?
      held = candidates.find(&:deferred?)
      if held
        return FallbackAvailability.new(
          available: false,
          availability_reason: held.id == item.id ? :on_hold : :ancestor_on_hold,
          availability_blocker_id: held.id,
          scheduled: nil
        )
      end

      timed = candidates.select { |candidate| candidate.scheduled && candidate.scheduled > today }
                        .max_by(&:scheduled)
      if timed
        return FallbackAvailability.new(
          available: false,
          availability_reason: timed.id == item.id ? :scheduled : :ancestor_scheduled,
          availability_blocker_id: timed.id,
          scheduled: timed.scheduled
        )
      end

      FallbackAvailability.new(
        available: true, availability_reason: :available,
        availability_blocker_id: nil, scheduled: nil
      )
    end

    def availability_date(item, availability, reader)
      return availability.scheduled if availability.is_a?(FallbackAvailability) && availability.scheduled
      return item.scheduled if availability.availability_blocker_id == item.id

      reader&.respond_to?(:task_for) && reader.task_for(availability.availability_blocker_id)&.scheduled
    end

    def project_name(item, reader)
      return nil unless reader
      node = reader.node_for(item)
      project_section(node)&.title if node
    end

    def next_project_date(items)
      items.filter_map { |i| i.deadline || i.scheduled }.min
    end
  end
end
