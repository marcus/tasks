# frozen_string_literal: true

module Tasks
  # The structural index over the live file: every record (section or task)
  # becomes a Node with its OWN body lines and its children, so callers can ask
  # hierarchy questions the flat Store#items list can't answer — which project a
  # task belongs to, whether a project has a NEXT action, what prose sits under
  # a heading (for body search and link extraction).
  #
  # Hierarchy comes straight from each record's `parent` pointer (no star
  # counting, no block inference). A task node's `item` is the Store item the
  # parser produced for that record; a section node's `item` is nil.
  module Tree
    # Caps task-depth for nesting mutations (capture/move --under); enforcement
    # lives in Store, not Check, so deeper legacy files still validate.
    DEFAULT_MAX_DEPTH = 4

    Node = Struct.new(:title, :line, :level, :item, :body, :children, :parent,
                      keyword_init: true) do
      def task?    = !item.nil?
      def section? = item.nil?

      # Depth-first walk of this node and everything under it.
      def each(&blk)
        return to_enum(:each) unless blk
        yield self
        children.each { |c| c.each(&blk) }
      end

      # The nearest ancestor headline — a task's project (or a subtask's parent
      # task). nil at top level.
      def project = parent

      # The task's project *for grouping/display*: the nearest ancestor headline
      # that is either a section or an OPEN task, climbing PAST closed
      # (DONE/CANCELLED) task ancestors — a closed ancestor is transparent, the
      # same way the outliner hoists open descendants out of a closed parent.
      # Deferred tasks count as open (a deferred project still owns its
      # subtasks). nil when no such ancestor exists (a top-level task, or one
      # whose every task ancestor is closed with no section above it), so those
      # tasks fall out of the Projects view rather than heading a dead group.
      def open_project
        a = parent
        a = a.parent while a && a.task? && !a.item.open?
        a
      end

      # Body prose joined for matching. The record's body is already prose
      # (Format strips nothing — the store stored only prose), so this is a
      # straight join of the node's own lines.
      def body_text = body.join("\n")
    end

    module_function

    # Build the forest of top-level nodes from `records` (as Tasks::Format
    # parsed them, each carrying its physical `line`). `items_by_line` maps a
    # 1-based line number to its Store item, binding each task node to the item
    # the parser produced. Order is DFS pre-order, so a single pass over the
    # records — linking each to its parent by id — reconstructs the tree.
    def build(records, items_by_line)
      roots = []
      by_id = {}
      records.each do |r|
        next if r["type"] == "meta"

        item = items_by_line[r["line"]]
        body = r["body"].nil? || r["body"].empty? ? [] : r["body"].split("\n")
        node = Node.new(
          # A task node's title is the parsed item title; a section's is its
          # own title field.
          title: item ? item.title : r["title"],
          line: r["line"], level: 1,
          item: item, body: body, children: [],
        )
        by_id[r["id"]] = node

        if (pid = r["parent"]) && (parent = by_id[pid])
          node.parent = parent
          node.level = parent.level + 1
          parent.children << node
        else
          roots << node
        end
      end
      roots
    end
  end
end
