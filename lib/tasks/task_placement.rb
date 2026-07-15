# frozen_string_literal: true

module Tasks
  # Immutable, transport-neutral structural destination for one task subtree.
  # Store resolves these stable ids under its mutation lock; adapters must not
  # translate them into record indexes. A nil +before_id+ means append as the
  # destination parent's last child.
  class TaskPlacement
    attr_reader :parent_id, :before_id

    def initialize(parent_id:, before_id: nil)
      @parent_id = immutable(parent_id)
      @before_id = immutable(before_id)
      freeze
    end

    private

    def immutable(value)
      case value
      when Hash
        value.each_with_object({}) do |(key, child), copy|
          copy[immutable(key)] = immutable(child)
        end.freeze
      when Array
        value.map { |child| immutable(child) }.freeze
      when String
        value.dup.freeze
      else
        value.freeze
      end
    end
  end
end
