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

    module_function

    # `tree` nil → the flat builders (unchanged shape; this path serves `/`
    # filter mode). `tree` present (the Store#tree forest) → the outliner
    # builders, which nest each anchor's visible subtree under it with indent +
    # markers. `store` (always the live Store) lets the projects view resolve
    # each task's parent project regardless of path.
    def rows(view, items, tree: nil, collapsed: Set.new, show_deferred: false,
             today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS, store: nil)
      return projects(items, today: today, store: store) if view == :projects

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
        when :agenda    then agenda(items, today: today)
        when :next      then next_actions(items, today: today)
        when :quadrants then quadrants(items, today: today, urgent_days: urgent_days)
        when :inbox     then inbox(items)
        else []
        end
      end
    end

    # -- flat builders (tree: nil) -------------------------------------------
    # These render exactly as before the outliner existed; the `/` filter view
    # relies on their output shape.

    def agenda(items, today: Date.today)
      dated = items.select { |i| i.open? && (i.scheduled || i.deadline) }
      # same date → priority order (A first, none last)
      dated.sort_by { |i| [i.deadline || i.scheduled, i.priority || "Z"] }.map do |i|
        Row.new("#{agenda_stamp(i, today)} #{decorated_title(i)}#{badge(i)}", i)
      end
    end

    def next_actions(items, today: Date.today)
      by_ctx = Hash.new { |h, k| h[k] = [] }
      items.select { |i| i.state == "NEXT" }.each do |i|
        ctxs = i.contexts
        (ctxs.empty? ? ["(no context)"] : ctxs).each { |c| by_ctx[c] << i }
      end
      rows = []
      by_ctx.sort.each do |ctx, list|
        rows << Row.new(T.paint(:context, ctx), nil)
        list.sort_by { |i| i.priority || "Z" }.each do |i|
          rows << Row.new("  #{next_body(i, today)}", i)
        end
        rows << Row.new("", nil)
      end
      rows.pop
      rows
    end

    # Classification (importance/urgency) lives in Tasks::Quadrants so the CLI
    # and TUI agree; this just lays the four buckets out as rows.
    def quadrants(items, today: Date.today, urgent_days: Tasks::Quadrants::DEFAULT_URGENT_DAYS)
      open_items = items.select(&:open?)
      by_q = open_items.group_by { |i| Tasks::Quadrants.of(i, today: today, urgent_days: urgent_days) }
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

    def inbox(items)
      inbox = items.select { |i| i.state == "INBOX" }
      return [Row.new(T.paint(:muted, "Inbox empty. ✨"), nil)] if inbox.empty?
      inbox.map { |i| Row.new("  #{inbox_body(i)}", i) }
    end

    # The projects view groups every open task under its project — the nearest
    # ancestor headline that's a section or an OPEN task, skipping closed
    # (DONE/CANCELLED) task ancestors (Node#open_project). A task with no such
    # ancestor (bare top-level, or all task ancestors closed) has no project and
    # is left out, exactly like a bare top-level task; it still shows in the
    # other four views. It's a grouped summary in both flat and tree mode — the
    # grouping already expresses the hierarchy, so it doesn't ride the outliner
    # walker. Each group header shows the open /
    # NEXT counts and the soonest date; tasks list beneath, project column
    # suppressed (it's the header).
    def projects(items, today: Date.today, store: nil)
      return [Row.new(T.paint(:muted, "Project data needs the task tree."), nil)] unless store

      groups = Hash.new { |h, k| h[k] = [] }
      items.select(&:open?).each do |item|
        project = project_name(item, store)
        next if project == "Inbox"
        groups[project] << item if project
      end
      return [Row.new(T.paint(:muted, "No active projects."), nil)] if groups.empty?

      rows = groups.sort_by { |name, list| [next_project_date(list) || Date.new(9999, 12, 31), name] }
                   .flat_map do |name, list|
        rows = [Row.new(project_header(name, list, today), nil)]
        list.sort_by { |i| [i.deadline || i.scheduled || Date.new(9999, 12, 31), i.priority || "Z", i.title] }
            .each { |i| rows << Row.new("  #{next_body(i, today)}", i) }
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
      anchors = anchor_roots(tree, show_deferred).select do |n|
        subtree_has_dated?(n, show_deferred)
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
      anchors = visible_nodes(tree, show_deferred).select do |n|
        n.item.state == "NEXT" && !next_ancestor?(n, show_deferred)
      end
      by_ctx = Hash.new { |h, k| h[k] = [] }
      anchors.each do |n|
        ctxs = n.item.contexts
        (ctxs.empty? ? ["(no context)"] : ctxs).each { |c| by_ctx[c] << n }
      end
      rows = []
      by_ctx.sort.each do |ctx, list|
        rows << Row.new(T.paint(:context, ctx), nil)
        list.sort_by { |n| n.item.priority || "Z" }.each do |anchor|
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
      anchors = anchor_roots(tree, show_deferred)
      by_q = anchors.group_by { |n| Tasks::Quadrants.of(n.item, today: today, urgent_days: urgent_days) }
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
      anchors = visible_nodes(tree, show_deferred).select do |n|
        n.item.state == "INBOX" && !inbox_ancestor?(n, show_deferred)
      end
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
    # top-level rows stay aligned); each level below the anchor adds two spaces,
    # then the marker column, then the per-view body the block formats. A
    # collapsed node emits only its own row (marker ▸) with a muted hidden-count.
    def append_subtree(rows, anchor, base, collapsed:, show_deferred:, &body)
      subtree_rows(anchor, 0, collapsed: collapsed, show_deferred: show_deferred) do |node, depth, marker, folded|
        text = +""
        text << base << ("  " * depth) << marker << body.call(node.item)
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

    # Top-level task nodes: a section's task children, or a bare task root.
    def top_level_task_nodes(tree)
      tree.flat_map { |root| root.section? ? root.children : [root] }.select(&:task?)
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

    # Does the anchor's visible subtree hold at least one dated open task? (The
    # anchor qualifies for the agenda only if some visible node is scheduled or
    # has a deadline.)
    def subtree_has_dated?(node, show_deferred)
      return true if node.item.scheduled || node.item.deadline
      visible_children(node, show_deferred).any? { |c| subtree_has_dated?(c, show_deferred) }
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

    # Whether some ancestor WITHIN THE SAME rendered subtree is in `state` (used
    # for the maximal-NEXT and maximal-INBOX anchor rules — a node under such an
    # ancestor rides its subtree instead of anchoring its own). The walk stops
    # at the anchor-root boundary: a closed or deferred-hidden ancestor breaks
    # the subtree, so a NEXT above a DONE middle does NOT suppress a hoisted NEXT
    # below it — the hoisted node is its own anchor and must count as maximal.
    def ancestor_state?(node, state, show_deferred)
      a = node.parent
      while a && visible?(a, show_deferred)
        return true if a.item.state == state
        a = a.parent
      end
      false
    end

    def next_ancestor?(node, show_deferred)  = ancestor_state?(node, "NEXT", show_deferred)
    def inbox_ancestor?(node, show_deferred) = ancestor_state?(node, "INBOX", show_deferred)

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

    def project_name(item, store)
      return nil unless store
      store.node_for(item)&.open_project&.title
    end

    def next_project_date(items)
      items.filter_map { |i| i.deadline || i.scheduled }.min
    end
  end
end
