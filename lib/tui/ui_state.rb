# frozen_string_literal: true

require "set"
require_relative "text_input"

module Tui
  # Durable and interaction state for the TUI. App owns effects and event
  # dispatch; this object owns which interaction mode is legal and the state
  # that must move together when an overlay opens or disappears.
  class UiState
    class InvalidTransition < ArgumentError; end

    MODES = %i[list prompt filter modal modal_filter form palette].freeze
    TRANSITIONS = {
      list:         %i[list prompt filter modal form palette],
      prompt:       %i[prompt list modal],
      filter:       %i[filter list],
      modal:        %i[modal list modal_filter form palette],
      modal_filter: %i[modal_filter modal list],
      form:         %i[form list modal],
      palette:      %i[palette list modal],
    }.freeze

    attr_reader :mode
    attr_accessor :view, :selected_id, :filter, :collapsed, :show_deferred,
                  :modal, :detail_item_id, :archive_preview, :form,
                  :form_success, :action_palette, :filter_input,
                  :modal_filter_input

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
      new(view: view, collapsed: collapsed)
    end

    def initialize(view:, collapsed: Set.new)
      @view = view
      @selected_id = nil
      @mode = :list
      @filter = nil
      @collapsed = collapsed
      @show_deferred = false
      @modal = nil
      @detail_item_id = nil
      @archive_preview = nil
      @form = nil
      @form_success = nil
      @action_palette = nil
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

      @detail_item_id = nil
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

    def toggle_deferred!
      @show_deferred = !@show_deferred
    end

    def session_hash(live_ids:)
      {
        "view" => @view.to_s,
        "collapsed" => (@collapsed & live_ids.to_set).to_a,
      }
    end

    private

    def force_list!
      @mode = :list
    end
  end
end
