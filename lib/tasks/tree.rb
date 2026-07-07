# frozen_string_literal: true

module Tasks
  # The structural index over an org file: every headline (task or section)
  # becomes a Node with its OWN body lines and its children, so callers can ask
  # hierarchy questions the flat Store#items list can't answer — which project a
  # task belongs to, whether a project has a NEXT action, what prose sits under
  # a heading (for body search and link extraction).
  #
  # Nodes carry raw body text; interpretation (search, links) happens above.
  # Built from the same lines the parser reads, keyed by line number so a
  # node's `item` is the exact Store item for that headline.
  module Tree
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

      # Body prose joined for matching — drawer machinery, planning stamps, and
      # comment lines excluded (Store.prose, the same rule every surface applies).
      def body_text = Store.prose(body).join
    end

    module_function

    # Build the forest of top-level nodes from raw file `lines` (1-exact as read
    # from disk). `items_by_line` maps a 1-based headline line number to its
    # Store item, binding each task node to the item the parser produced.
    def build(lines, items_by_line)
      roots = []
      stack = [] # open ancestors, innermost last
      lines.each_with_index do |line, idx|
        stars = line[/\A(\*+)\s/, 1]
        unless stars
          stack.last&.body&.push(line)
          next
        end

        item = items_by_line[idx + 1]
        node = Node.new(
          # A task node's title is the parsed item title (no state/priority/tag
          # decoration); a section's is its heading text.
          title: item ? item.title : line.sub(/\A\*+\s+/, "").strip,
          line: idx + 1, level: stars.length,
          item: item,
          body: [], children: [],
        )
        stack.pop while stack.any? && stack.last.level >= node.level
        if (parent = stack.last)
          node.parent = parent
          parent.children << node
        else
          roots << node
        end
        stack << node
      end
      roots
    end
  end
end
