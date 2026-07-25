# frozen_string_literal: true

require_relative "mouse"

module Tui
  # Pure (mode, zone, button, action) → intent table. Receives a Mouse::Event
  # and a HitMap::Hit. Every intent maps to an existing keyboard handler.
  module MouseRouter
    module_function

    def intent(event, hit, mode: :list, panel: false, selected: nil)
      return :ignored unless event
      return :ignored if event.release? || event.motion?
      return :ignored if event.button == :middle || event.button == :right
      return :ignored if event.button.to_s.start_with?("button")

      if event.wheel?
        return wheel_intent(event, hit, mode: mode, panel: panel)
      end

      return :ignored unless event.button == :left && event.press?

      click_intent(event, hit, mode: mode, selected: selected)
    end

    def wheel_intent(event, hit, mode:, panel:)
      delta = event.button == :wheel_up ? -Mouse::WHEEL_DELTA : Mouse::WHEEL_DELTA
      # Wheel left/right are decoded but unused in the first release.
      return :ignored unless %i[wheel_up wheel_down].include?(event.button)

      case mode
      when :task_edit
        return [:scroll_panel, delta] if hit.zone == :panel && panel

        return :ignored
      end

      case hit.zone
      when :panel
        panel ? [:scroll_panel, delta] : :ignored
      when :modal_row
        [:scroll_modal, delta]
      when :list_row, :collapse_marker
        [:scroll_list, delta]
      when :footer_row
        role = hit.payload.is_a?(Hash) ? hit.payload[:role] : nil
        role == :response ? [:scroll_response, delta] : :ignored
      when :popup_row
        [:scroll_popup, delta]
      else
        :ignored
      end
    end
    private_class_method :wheel_intent

    def click_intent(_event, hit, mode:, selected:)
      # Task editor saves on blur — clicks must not steal focus.
      return :ignored if mode == :task_edit

      case hit.zone
      when :list_row
        n = hit.payload
        n == selected ? [:activate_row, n] : [:select_row, n]
      when :collapse_marker
        [:toggle_collapse, hit.payload]
      when :tab
        [:switch_view, hit.payload]
      when :footer_row
        role = hit.payload.is_a?(Hash) ? hit.payload[:role] : nil
        role == :prompt ? [:focus_prompt] : :ignored
      when :popup_row
        [:picker_hit, hit.payload]
      else
        # Deliberately ignore modal chrome clicks (no dismiss-on-outside-click).
        :ignored
      end
    end
    private_class_method :click_intent
  end
end
