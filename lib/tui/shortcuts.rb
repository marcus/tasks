# frozen_string_literal: true

module Tui
  # Declarative registry for every keyboard action shown in help. Contexts
  # decide where a binding is active; App owns only dispatch order and action
  # implementations. Optional form/confirmation metadata is reserved for the
  # command palette and other future consumers.
  module Shortcuts
    CONTEXTS = %i[list detail modal global].freeze

    Entry = Struct.new(
      :sequences, :display_key, :description, :contexts, :handler,
      :availability, :form, :confirmation,
      keyword_init: true
    ) do
      def available?(receiver)
        case availability
        when Symbol then receiver.send(availability)
        else availability.call(receiver)
        end
      end
    end

    def self.entry(sequences:, key:, description:, contexts:, handler:,
                   availability: :action_available?, form: nil, confirmation: nil)
      Entry.new(
        sequences: sequences.freeze,
        display_key: key.freeze,
        description: description.freeze,
        contexts: contexts.freeze,
        handler: handler,
        availability: availability,
        form: form,
        confirmation: confirmation
      ).freeze
    end
    private_class_method :entry

    REGISTRY = [
      entry(sequences: ["\e[A", "k"],  key: "↑ / k",   description: "select previous task",             contexts: [:list], handler: :select_prev),
      entry(sequences: ["\e[B", "j"],  key: "↓ / j",   description: "select next task",                 contexts: [:list], handler: :select_next),
      entry(sequences: ["\e[D"],       key: "←",       description: "previous view",                    contexts: [:list], handler: :prev_view),
      entry(sequences: ["\e[C"],       key: "→",       description: "next view",                        contexts: [:list], handler: :next_view),
      entry(sequences: %w[1 2 3 4 5],  key: "1-5",     description: "jump to view",                     contexts: [:list], handler: :jump_view),
      entry(sequences: ["h"],          key: "h",       description: "collapse subtree (again: to parent)", contexts: [:list], handler: :collapse_selected),
      entry(sequences: ["l"],          key: "l",       description: "expand subtree",                   contexts: [:list], handler: :expand_selected),
      entry(sequences: ["H"],          key: "H",       description: "collapse all subtrees",            contexts: [:list], handler: :collapse_all),
      entry(sequences: ["L"],          key: "L",       description: "expand all subtrees",              contexts: [:list], handler: :expand_all),
      entry(sequences: ["\r", "\n"],   key: "return",  description: "task details",                     contexts: [:list], handler: :open_detail),
      entry(sequences: ["c"],          key: "c",       description: "complete selected task",           contexts: %i[list detail], handler: :complete_selected),
      entry(sequences: ["d"],          key: "d",       description: "reschedule — fri · +3 · 07-15",    contexts: %i[list detail], handler: :open_date_popup, form: :date),
      entry(sequences: ["r"],          key: "r",       description: "recur — weekly · 2w · .+1m · off", contexts: %i[list detail], handler: :open_recur_popup, form: :recurrence),
      entry(sequences: ["x"],          key: "x",       description: "archive DONE/CANCELLED items",     contexts: [:list], handler: :archive_sweep, confirmation: :archive_preview),
      entry(sequences: ["z"],          key: "z",       description: "defer / activate selected task",   contexts: %i[list detail], handler: :defer_selected),
      entry(sequences: ["Z"],          key: "Z",       description: "show / hide deferred tasks",       contexts: [:list], handler: :toggle_deferred_view),
      entry(sequences: ["K"],          key: "K",       description: "raise priority (→ A)",             contexts: %i[list detail], handler: :raise_priority),
      entry(sequences: ["J"],          key: "J",       description: "lower priority (→ none)",          contexts: %i[list detail], handler: :lower_priority),
      entry(sequences: ["o"],          key: "o",       description: "open task link in browser",        contexts: %i[list detail], handler: :open_link),
      entry(sequences: ["y"],          key: "y",       description: "yank task ref (paste to agent)",   contexts: %i[list detail], handler: :yank_ref),
      entry(sequences: ["Y"],          key: "Y",       description: "yank task as markdown",            contexts: %i[list detail], handler: :yank_markdown),
      entry(sequences: ["p"],          key: "p",       description: "paste task ref into the prompt",   contexts: %i[list detail], handler: :paste_ref),
      entry(sequences: ["/"],          key: "/",       description: "filter tasks by text",             contexts: [:list], handler: :start_filter, form: :filter),
      entry(sequences: ["M"],          key: "M",       description: "cycle agent/model",                contexts: [:list], handler: :toggle_model),
      entry(sequences: ["u"],          key: "u",       description: "undo last change",                 contexts: %i[list detail], handler: :undo_last),
      entry(sequences: ["\x12"],       key: "ctrl-r",  description: "redo",                             contexts: %i[list detail], handler: :redo_last),
      entry(sequences: ["\t", ":"],    key: "tab / :", description: "ask the agent — CRUD anything",    contexts: [:list], handler: :focus_prompt, form: :agent_prompt),
      entry(sequences: ["\e[5~"],      key: "pgup",    description: "scroll agent response up",         contexts: [:list], handler: :resp_up),
      entry(sequences: ["\e[6~"],      key: "pgdn",    description: "scroll agent response down",       contexts: [:list], handler: :resp_down),
      entry(sequences: ["\e"],         key: "esc",     description: "dismiss response / cancel agent",  contexts: [:list], handler: :dismiss_or_cancel),
      entry(sequences: ["?"],          key: "?",       description: "keyboard shortcuts",               contexts: [:list], handler: :open_help),
      entry(sequences: ["q"],          key: "q",       description: "quit",                             contexts: [:list], handler: :quit),

      # Modal navigation is kept as an explicit context. App dispatches it
      # before detail-task actions; validation rejects collisions between the
      # two contexts so navigation cannot accidentally shadow an action.
      entry(sequences: ["\e[A", "k"],      key: "↑ / k",         description: "scroll up · previous task (detail)", contexts: [:modal], handler: :modal_up),
      entry(sequences: ["\e[B", "j"],      key: "↓ / j",         description: "scroll down · next task (detail)", contexts: [:modal], handler: :modal_down),
      entry(sequences: ["\x15"],           key: "ctrl-u",        description: "scroll half page up",                 contexts: [:modal], handler: :modal_half_up),
      entry(sequences: ["\x04"],           key: "ctrl-d",        description: "scroll half page down",               contexts: [:modal], handler: :modal_half_down),
      entry(sequences: ["\x02", "\e[5~"],  key: "ctrl-b / pgup", description: "scroll page up",                      contexts: [:modal], handler: :modal_page_up),
      entry(sequences: ["\x06", "\e[6~"],  key: "ctrl-f / pgdn", description: "scroll page down",                    contexts: [:modal], handler: :modal_page_down),
      entry(sequences: ["/"],              key: "/",             description: "filter lines (shortcuts modal)",      contexts: [:modal], handler: :modal_start_filter, availability: :modal_filter_available?, form: :modal_filter),
      entry(sequences: ["\e", "q", "\r", "\n", "?"], key: "esc / q", description: "close modal", contexts: [:modal], handler: :close_modal),

      entry(sequences: ["\x03"], key: "ctrl-c", description: "quit", contexts: [:global], handler: :quit),
    ].freeze

    def self.entries(context, include_global: true)
      raise ArgumentError, "unknown shortcut context #{context.inspect}" unless CONTEXTS.include?(context)

      REGISTRY.select do |entry|
        entry.contexts.include?(context) || (include_global && entry.contexts.include?(:global))
      end
    end

    # Returns the binding even when unavailable. Dispatch must consume such a
    # key instead of falling through to another context.
    def self.match(sequence, context)
      entries(context).find { |entry| entry.sequences.include?(sequence) }
    end

    def self.available?(entry, receiver) = entry.available?(receiver)

    def self.validate!(handler_owner = nil, entries: REGISTRY)
      entries.each { |entry| validate_entry!(entry, handler_owner) }
      validate_collisions!(entries)
      true
    end

    def self.validate_entry!(entry, handler_owner)
      raise ArgumentError, "shortcut sequences must be a non-empty array" unless entry.sequences.is_a?(Array) && !entry.sequences.empty? && entry.sequences.all? { |s| s.is_a?(String) && !s.empty? }
      raise ArgumentError, "shortcut sequences must be unique" unless entry.sequences.uniq == entry.sequences
      raise ArgumentError, "shortcut display key must be a non-empty string" unless entry.display_key.is_a?(String) && !entry.display_key.empty?
      raise ArgumentError, "shortcut description must be a non-empty string" unless entry.description.is_a?(String) && !entry.description.empty?
      raise ArgumentError, "shortcut contexts are invalid" unless entry.contexts.is_a?(Array) && !entry.contexts.empty? && entry.contexts.uniq == entry.contexts && (entry.contexts - CONTEXTS).empty?
      raise ArgumentError, "shortcut handler must be a symbol" unless entry.handler.is_a?(Symbol)
      unless entry.availability.is_a?(Symbol) || entry.availability.respond_to?(:call)
        raise ArgumentError, "shortcut availability must be a method name or callable"
      end
      validate_metadata!(:form, entry.form)
      validate_metadata!(:confirmation, entry.confirmation)
      return unless handler_owner

      raise ArgumentError, "missing shortcut handler #{handler_owner}##{entry.handler}" unless handler_owner.private_method_defined?(entry.handler) || handler_owner.method_defined?(entry.handler)
      if entry.availability.is_a?(Symbol) && !(handler_owner.private_method_defined?(entry.availability) || handler_owner.method_defined?(entry.availability))
        raise ArgumentError, "missing shortcut availability #{handler_owner}##{entry.availability}"
      end
    end
    private_class_method :validate_entry!

    def self.validate_metadata!(name, value)
      return if value.nil? || value.is_a?(Symbol)
      return if value.is_a?(Hash) && value[:kind].is_a?(Symbol)

      raise ArgumentError, "shortcut #{name} metadata must be a symbol or a hash with a symbol :kind"
    end
    private_class_method :validate_metadata!

    def self.validate_collisions!(entries)
      bindings = {}
      entries.each do |entry|
        effective_contexts = entry.contexts.include?(:global) ? CONTEXTS - [:global] : entry.contexts
        # :modal is dispatched before :detail, so sharing a sequence across
        # them would make the detail binding unreachable.
        effective_contexts += [:detail] if effective_contexts.include?(:modal)
        effective_contexts.each do |context|
          entry.sequences.each do |sequence|
            key = [context, sequence]
            other = bindings[key]
            raise ArgumentError, "duplicate shortcut #{sequence.inspect} in #{context}: #{other.handler} and #{entry.handler}" if other && other != entry
            bindings[key] = entry
          end
        end
      end
    end
    private_class_method :validate_collisions!
  end
end
