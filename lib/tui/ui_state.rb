# frozen_string_literal: true

require "set"
require_relative "text_input"

module Tui
  # Durable and interaction state for the TUI. App owns effects and event
  # dispatch; this object owns which interaction mode is legal and the state
  # that must move together when an overlay opens or disappears.
  class UiState
    class InvalidTransition < ArgumentError; end

    MODES = %i[list prompt filter modal modal_filter form palette task_edit].freeze
    PANEL_MODES = %i[compact standard wide focus].freeze
    TRANSITIONS = {
      list:         %i[list prompt filter modal form palette task_edit],
      prompt:       %i[prompt list modal],
      filter:       %i[filter list],
      modal:        %i[modal list modal_filter form palette],
      modal_filter: %i[modal_filter modal list],
      form:         %i[form list modal],
      palette:      %i[palette list modal],
      task_edit:    %i[task_edit list],
    }.freeze

    attr_reader :mode, :panel_offset
    attr_accessor :view, :selected_id, :filter, :collapsed, :show_deferred,
                  :modal, :panel, :archive_preview, :form,
                  :form_success, :action_palette, :filter_input,
                  :modal_filter_input, :task_editor, :panel_mode

    def self.restore(saved:, views:, default_view:)
      saved_view = saved[:view]
      saved_view = saved_view.is_a?(String) ? saved_view.to_sym : nil
      view = views.include?(saved_view) ? saved_view : default_view

      saved_collapsed = saved[:collapsed]
      collapsed = if saved_collapsed.is_a?(Array) && saved_collapsed.all? { |id| id.is_a?(String) }
                    Set.new(saved_collapsed)
                  else
                    Set.new
                  end
      saved_panel_mode = saved[:panel_mode]
      saved_panel_mode = saved_panel_mode.to_sym if saved_panel_mode.is_a?(String)
      panel_mode = PANEL_MODES.include?(saved_panel_mode) ? saved_panel_mode : :standard
      saved_offset = saved[:panel_offset]
      panel_offset = saved_offset.is_a?(Integer) ? saved_offset : 0
      new(view: view, collapsed: collapsed, panel_mode: panel_mode, panel_offset: panel_offset)
    end

    def initialize(view:, collapsed: Set.new, panel_mode: :standard, panel_offset: 0)
      @view = view
      @selected_id = nil
      @mode = :list
      @filter = nil
      @collapsed = collapsed
      @show_deferred = false
      @modal = nil
      @panel = nil
      @archive_preview = nil
      @form = nil
      @form_success = nil
      @action_palette = nil
      @task_editor = nil
      @panel_mode = PANEL_MODES.include?(panel_mode.to_sym) ? panel_mode.to_sym : :standard
      @panel_offset = panel_offset.to_i
      @filter_input = TextInput.new
      @modal_filter_input = TextInput.new
    end

    def mode=(target)
      target = target.to_sym if target.respond_to?(:to_sym)
      unless MODES.include?(target) && TRANSITIONS.fetch(@mode).include?(target)
        raise InvalidTransition, "illegal TUI transition: #{@mode} -> #{target.inspect}"
      end
      case target
      when :modal, :modal_filter
        raise InvalidTransition, "#{target} mode requires a modal" unless @modal
        if target == :modal_filter && !@modal.filterable?
          raise InvalidTransition, "modal_filter mode requires a filterable modal"
        end
      when :form
        raise InvalidTransition, "form mode requires a form" unless @form
        if @form.return_mode == :modal && !@modal
          raise InvalidTransition, "form returning to modal requires a retained modal"
        end
      when :palette
        raise InvalidTransition, "palette mode requires an action palette" unless @action_palette
        if @action_palette.return_mode == :modal && !@modal
          raise InvalidTransition, "palette returning to modal requires a retained modal"
        end
      when :task_edit
        raise InvalidTransition, "task_edit mode requires a task editor" unless @task_editor
      end
      @mode = target
    end

    # Removing an overlay can happen after an external file reload. Never leave
    # its mode pointing at an object that no longer exists.
    def modal=(value)
      if value && @mode == :modal_filter && !value.filterable?
        raise InvalidTransition, "cannot replace a filtered modal with a non-filterable modal"
      end
      @modal = value
      return unless value.nil?

      @archive_preview = nil
      if @mode == :form && @form&.return_mode == :modal
        @form = nil
        @form_success = nil
        force_list!
      elsif @mode == :palette && @action_palette&.return_mode == :modal
        @action_palette = nil
        force_list!
      elsif %i[modal modal_filter].include?(@mode)
        force_list!
      end
    end

    def form=(value)
      if value&.return_mode == :modal && !@modal
        raise InvalidTransition, "form returning to modal requires a retained modal"
      end
      @form = value
      if value.nil?
        @form_success = nil
        force_list! if @mode == :form
      end
    end

    def action_palette=(value)
      if value&.return_mode == :modal && !@modal
        raise InvalidTransition, "palette returning to modal requires a retained modal"
      end
      @action_palette = value
      force_list! if value.nil? && @mode == :palette
    end

    def task_editor=(value)
      @task_editor = value
      force_list! if value.nil? && @mode == :task_edit
    end

    def panel_mode=(value)
      normalized = value.respond_to?(:to_sym) ? value.to_sym : nil
      raise ArgumentError, "unknown panel mode #{value.inspect}" unless PANEL_MODES.include?(normalized)

      @panel_mode = normalized
    end

    # The signed column tweak ctrl+k/ctrl+l apply on top of the mode width.
    # App re-derives it against the live layout each keypress, so any Integer is
    # legal here; ScreenLayout re-clamps it to the current terminal's bounds.
    def panel_offset=(value)
      @panel_offset = value.to_i
    end

    def toggle_deferred!
      @show_deferred = !@show_deferred
    end

    def session_hash(live_ids:)
      {
        "view" => @view.to_s,
        "collapsed" => (@collapsed & live_ids.to_set).to_a,
        "panel_mode" => @panel_mode.to_s,
        "panel_offset" => @panel_offset,
      }
    end

    private

    def force_list!
      @mode = :list
    end
  end
end
