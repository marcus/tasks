# frozen_string_literal: true

module Tui
  # Declarative registry for every keyboard action shown in help. Contexts
  # decide where a binding is active; App owns only dispatch order and action
  # implementations. Optional form/confirmation metadata is reserved for the
  # command palette and other future consumers.
  module Shortcuts
    CONTEXTS = %i[list detail task_edit modal global].freeze

    Entry = Struct.new(
      :sequences, :display_key, :description, :contexts, :handler,
      :availability, :palette, :form, :confirmation,
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
                   availability: :action_available?, palette: false,
                   form: nil, confirmation: nil)
      Entry.new(
        sequences: sequences.freeze,
        display_key: key.freeze,
        description: description.freeze,
        contexts: contexts.freeze,
        handler: handler,
        availability: availability,
        palette: palette,
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
      entry(sequences: %w[1 2 3 4 5 6], key: "1-6",     description: "jump to view",                     contexts: [:list], handler: :jump_view),
      entry(sequences: ["\e[1;3A", "\e\e[A", "\ek"], key: "alt-↑ / alt-k", description: "Move up", contexts: [:list], handler: :move_subtree_up, availability: :ordering_action_available?, palette: true),
      entry(sequences: ["\e[1;3B", "\e\e[B", "\ej"], key: "alt-↓ / alt-j", description: "Move down", contexts: [:list], handler: :move_subtree_down, availability: :ordering_action_available?, palette: true),
      entry(sequences: [">"],          key: ">",       description: "Indent",                          contexts: [:list], handler: :indent_subtree, availability: :ordering_action_available?, palette: true),
      entry(sequences: ["<"],          key: "<",       description: "Outdent",                         contexts: [:list], handler: :outdent_subtree, availability: :ordering_action_available?, palette: true),
      entry(sequences: ["h"],          key: "h",       description: "collapse subtree (again: to parent)", contexts: [:list], handler: :collapse_selected, palette: :selected_action_available?),
      entry(sequences: ["l"],          key: "l",       description: "expand subtree",                   contexts: [:list], handler: :expand_selected, palette: :selected_action_available?),
      entry(sequences: ["H"],          key: "H",       description: "collapse all subtrees",            contexts: [:list], handler: :collapse_all, palette: true),
      entry(sequences: ["L"],          key: "L",       description: "expand all subtrees",              contexts: [:list], handler: :expand_all, palette: true),
      entry(sequences: ["\r", "\n"],   key: "return",  description: "open / close task details",        contexts: [:list], handler: :open_detail, palette: :selected_action_available?),
      entry(sequences: ["c"],          key: "c",       description: "complete selected task",           contexts: %i[list detail], handler: :complete_selected, palette: :selected_action_available?),
      entry(sequences: ["d"],          key: "d",       description: "edit Deadline / Available from date", contexts: %i[list detail], handler: :open_date_popup, palette: :selected_action_available?, form: :date),
      entry(sequences: ["r"],          key: "r",       description: "recur — weekly · 2w · .+1m · off", contexts: %i[list detail], handler: :open_recur_popup, palette: :recurrence_action_available?, form: :recurrence),
      entry(sequences: ["x"],          key: "x",       description: "archive DONE/CANCELLED items",     contexts: [:list], handler: :archive_sweep, palette: true, confirmation: :archive_preview),
      entry(sequences: ["z"],          key: "z",       description: "defer until — date · someday · now", contexts: %i[list detail], handler: :defer_selected, palette: :selected_action_available?, form: :defer_until),
      entry(sequences: ["Z"],          key: "Z",       description: "show / hide unavailable tasks",    contexts: [:list], handler: :toggle_deferred_view, palette: true),
      entry(sequences: ["K"],          key: "K",       description: "raise priority (→ A)",             contexts: %i[list detail], handler: :raise_priority, palette: :selected_action_available?),
      entry(sequences: ["J"],          key: "J",       description: "lower priority (→ none)",          contexts: %i[list detail], handler: :lower_priority, palette: :selected_action_available?),
      entry(sequences: ["o"],          key: "o",       description: "open task link in browser",        contexts: %i[list detail], handler: :open_link, palette: :link_action_available?),
      entry(sequences: ["y"],          key: "y",       description: "yank task ref (paste to agent)",   contexts: %i[list detail], handler: :yank_ref, palette: :selected_action_available?),
      entry(sequences: ["Y"],          key: "Y",       description: "yank task as markdown",            contexts: %i[list detail], handler: :yank_markdown, palette: :selected_action_available?),
      entry(sequences: ["p"],          key: "p",       description: "paste task ref into the prompt",   contexts: %i[list detail], handler: :paste_ref, palette: :selected_action_available?),
      entry(sequences: ["e"],          key: "e",       description: "edit task",                         contexts: [:detail], handler: :start_task_edit, palette: :selected_action_available?, form: :task_edit),
      entry(sequences: ["\t"],         key: "tab",       description: "edit task from its first field",    contexts: [:detail], handler: :start_task_edit),
      entry(sequences: ["\e[Z"],       key: "shift-tab", description: "edit task from its last field",   contexts: [:detail], handler: :start_task_edit_last),
      entry(sequences: ["\x0b"],       key: "ctrl-k",  description: "grow task panel",                  contexts: [:detail], handler: :grow_task_panel, palette: true),
      entry(sequences: ["\x0c"],       key: "ctrl-l",  description: "shrink task panel",                contexts: [:detail], handler: :shrink_task_panel, palette: true),
      entry(sequences: ["/"],          key: "/",       description: "filter tasks by text",             contexts: [:list], handler: :start_filter, palette: true, form: :filter),
      entry(sequences: ["@"],          key: "@",       description: "filter tasks by @context",         contexts: [:list], handler: :open_context_palette, palette: true, form: :context_filter),
      entry(sequences: ["M"],          key: "M",       description: "cycle agent/model",                contexts: [:list], handler: :toggle_model, palette: true),
      entry(sequences: ["A"],          key: "A",       description: "open agent activity",               contexts: [:list], handler: :open_agent_activity, availability: :agent_activity_available?, palette: true),
      entry(sequences: [],             key: "palette", description: "cancel queued agent requests",       contexts: [:list], handler: :cancel_queued_agent_requests, availability: :pending_agent_requests_available?, palette: true, confirmation: :agent_queue),
      entry(sequences: ["u"],          key: "u",       description: "undo last change",                 contexts: %i[list detail], handler: :undo_last, palette: true),
      entry(sequences: ["\x12"],       key: "ctrl-r",  description: "redo",                             contexts: %i[list detail], handler: :redo_last, palette: true),
      entry(sequences: ["\x15"],       key: "ctrl-u",  description: "scroll task details up",           contexts: [:list], handler: :panel_half_up, availability: :panel_scroll_available?),
      entry(sequences: ["\x04"],       key: "ctrl-d",  description: "scroll task details down",         contexts: [:list], handler: :panel_half_down, availability: :panel_scroll_available?),
      entry(sequences: ["\x02"],       key: "ctrl-b",  description: "scroll task details one page up", contexts: [:list], handler: :panel_page_up, availability: :panel_scroll_available?),
      entry(sequences: ["\x06"],       key: "ctrl-f",  description: "scroll task details one page down", contexts: [:list], handler: :panel_page_down, availability: :panel_scroll_available?),
      entry(sequences: ["\t"],         key: "tab",     description: "ask the agent — CRUD anything",    contexts: [:list], handler: :focus_prompt, palette: true, form: :agent_prompt),
      entry(sequences: [":"],          key: ":",       description: "search available actions",         contexts: %i[list detail], handler: :open_action_palette),
      entry(sequences: ["\e[5~"],      key: "pgup",    description: "scroll agent response up",         contexts: [:list], handler: :resp_up),
      entry(sequences: ["\e[6~"],      key: "pgdn",    description: "scroll agent response down",       contexts: [:list], handler: :resp_down),
      entry(sequences: ["\e"],         key: "esc",     description: "dismiss response / close task details", contexts: [:list], handler: :dismiss_or_cancel),
      entry(sequences: ["?"],          key: "?",       description: "keyboard shortcuts",               contexts: [:list], handler: :open_help, palette: true),
      entry(sequences: ["q"],          key: "q",       description: "quit (confirms unsaved draft)",    contexts: [:list], handler: :quit, palette: true),

      entry(sequences: ["\t"],         key: "tab",     description: "save field and edit next",         contexts: [:task_edit], handler: :task_edit_input),
      entry(sequences: ["\e[Z"],       key: "shift-tab", description: "save field and edit previous",    contexts: [:task_edit], handler: :task_edit_input),
      entry(sequences: ["\x13"],       key: "ctrl-s",  description: "save focused task field",          contexts: [:task_edit], handler: :task_edit_input),
      entry(sequences: ["\x0f"],       key: "ctrl-o",  description: "finish editing task",              contexts: [:task_edit], handler: :task_edit_input),
      entry(sequences: ["\x0b"],       key: "ctrl-k",  description: "grow task panel without saving",   contexts: [:task_edit], handler: :grow_task_panel),
      entry(sequences: ["\x0c"],       key: "ctrl-l",  description: "shrink task panel without saving", contexts: [:task_edit], handler: :shrink_task_panel),
      entry(sequences: ["\e"],         key: "esc",     description: "close picker / confirm field revert / finish editing", contexts: [:task_edit], handler: :task_edit_input),

      # Modal navigation is kept as an explicit context for blocking overlays.
      # Detail actions are palette metadata while the panel stays in list mode.
      entry(sequences: ["\e[A", "k"],      key: "↑ / k",         description: "scroll modal up",                        contexts: [:modal], handler: :modal_up),
      entry(sequences: ["\e[B", "j"],      key: "↓ / j",         description: "scroll modal down",                      contexts: [:modal], handler: :modal_down),
      entry(sequences: ["\x15"],           key: "ctrl-u",        description: "scroll half page up",                 contexts: [:modal], handler: :modal_half_up),
      entry(sequences: ["\x04"],           key: "ctrl-d",        description: "scroll half page down",               contexts: [:modal], handler: :modal_half_down),
      entry(sequences: ["\x02", "\e[5~"],  key: "ctrl-b / pgup", description: "scroll page up",                      contexts: [:modal], handler: :modal_page_up),
      entry(sequences: ["\x06", "\e[6~"],  key: "ctrl-f / pgdn", description: "scroll page down",                    contexts: [:modal], handler: :modal_page_down),
      entry(sequences: ["/"],              key: "/",             description: "filter lines (shortcuts modal)",      contexts: [:modal], handler: :modal_start_filter, availability: :modal_filter_available?, form: :modal_filter),
      entry(sequences: ["\e", "q", "\r", "\n", "?"], key: "esc / q", description: "close modal", contexts: [:modal], handler: :close_modal),

      entry(sequences: ["\x03"], key: "ctrl-c", description: "quit (confirms unsaved draft)", contexts: [:global], handler: :quit),
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

    def self.palette_entries(context, receiver)
      entries(context, include_global: false).select do |entry|
        next false unless available?(entry, receiver)

        case entry.palette
        when Symbol then receiver.send(entry.palette)
        when true then true
        else entry.palette.respond_to?(:call) && entry.palette.call(receiver)
        end
      end
    end

    def self.validate!(handler_owner = nil, entries: REGISTRY)
      entries.each { |entry| validate_entry!(entry, handler_owner) }
      validate_collisions!(entries)
      true
    end

    def self.validate_entry!(entry, handler_owner)
      unless entry.sequences.is_a?(Array) && entry.sequences.all? { |s| s.is_a?(String) && !s.empty? }
        raise ArgumentError, "shortcut sequences must be an array of non-empty strings"
      end
      if entry.sequences.empty? && !entry.palette
        raise ArgumentError, "a shortcut without key sequences must be palette-enabled"
      end
      raise ArgumentError, "shortcut sequences must be unique" unless entry.sequences.uniq == entry.sequences
      raise ArgumentError, "shortcut display key must be a non-empty string" unless entry.display_key.is_a?(String) && !entry.display_key.empty?
      raise ArgumentError, "shortcut description must be a non-empty string" unless entry.description.is_a?(String) && !entry.description.empty?
      raise ArgumentError, "shortcut contexts are invalid" unless entry.contexts.is_a?(Array) && !entry.contexts.empty? && entry.contexts.uniq == entry.contexts && (entry.contexts - CONTEXTS).empty?
      raise ArgumentError, "shortcut handler must be a symbol" unless entry.handler.is_a?(Symbol)
      unless entry.availability.is_a?(Symbol) || entry.availability.respond_to?(:call)
        raise ArgumentError, "shortcut availability must be a method name or callable"
      end
      unless [true, false, nil].include?(entry.palette) || entry.palette.is_a?(Symbol) || entry.palette.respond_to?(:call)
        raise ArgumentError, "shortcut palette availability must be boolean, method name, or callable"
      end
      validate_metadata!(:form, entry.form)
      validate_metadata!(:confirmation, entry.confirmation)
      return unless handler_owner

      raise ArgumentError, "missing shortcut handler #{handler_owner}##{entry.handler}" unless handler_owner.private_method_defined?(entry.handler) || handler_owner.method_defined?(entry.handler)
      if entry.palette && handler_owner.instance_method(entry.handler).arity != 0
        raise ArgumentError, "palette shortcut handler #{handler_owner}##{entry.handler} must not require a key"
      end
      if entry.availability.is_a?(Symbol) && !(handler_owner.private_method_defined?(entry.availability) || handler_owner.method_defined?(entry.availability))
        raise ArgumentError, "missing shortcut availability #{handler_owner}##{entry.availability}"
      end
      if entry.palette.is_a?(Symbol) && !(handler_owner.private_method_defined?(entry.palette) || handler_owner.method_defined?(entry.palette))
        raise ArgumentError, "missing shortcut palette availability #{handler_owner}##{entry.palette}"
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
