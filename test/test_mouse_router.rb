# frozen_string_literal: true

require_relative "test_helper"
require "tui/mouse"
require "tui/mouse_router"
require "tui/hit_map"

class TestMouseRouter < Minitest::Test
  R = Tui::MouseRouter
  E = Tui::Mouse::Event
  H = Tui::HitMap::Hit

  def event(button: :left, action: :press, col: 0, row: 0, **mods)
    E.new(button: button, action: action, col: col, row: row,
          shift: false, alt: false, ctrl: false, **mods)
  end

  def hit(zone, payload = nil) = H.new(zone: zone, payload: payload)

  # A downward gesture reports as wheel-up under macOS natural scrolling, so
  # wheel-up advances and wheel-down goes back — every target shares the sign.
  def test_wheel_over_list_scrolls_list
    ev = event(button: :wheel_up)
    assert_equal [:scroll_list, 3], R.intent(ev, hit(:list_row, 5), mode: :list)
    ev = event(button: :wheel_down)
    assert_equal [:scroll_list, -3], R.intent(ev, hit(:list_row, 5), mode: :list)
  end

  def test_every_wheel_target_shares_one_direction
    up = event(button: :wheel_up)
    down = event(button: :wheel_down)
    [[hit(:panel, 0), :scroll_panel, { panel: true }],
     [hit(:modal_row, 0), :scroll_modal, { mode: :modal }],
     [hit(:footer_row, { index: 0, role: :response }), :scroll_response, {}],
     [hit(:popup_row, 0), :scroll_popup, { mode: :palette }]].each do |h, action, opts|
      assert_equal [action, 3], R.intent(up, h, **opts)
      assert_equal [action, -3], R.intent(down, h, **opts)
    end
  end

  def test_wheel_over_panel
    ev = event(button: :wheel_down)
    assert_equal [:scroll_panel, -3], R.intent(ev, hit(:panel, 0), mode: :list, panel: true)
    assert_equal :ignored, R.intent(ev, hit(:panel, 0), mode: :list, panel: false)
  end

  def test_wheel_over_response_footer
    ev = event(button: :wheel_down)
    assert_equal [:scroll_response, -3],
                 R.intent(ev, hit(:footer_row, { index: 0, role: :response }), mode: :list)
    assert_equal :ignored,
                 R.intent(ev, hit(:footer_row, { index: 0, role: :prompt }), mode: :list)
  end

  # An open modal, palette, or form owns the pointer: nothing may reach the
  # list, tabs, or panel underneath it.
  def test_overlay_modes_ignore_everything_outside_the_overlay
    click = event(button: :left)
    wheel = event(button: :wheel_down)
    %i[modal modal_filter palette context_palette form].each do |mode|
      [hit(:list_row, 5), hit(:collapse_marker, 5), hit(:tab, :next), hit(:panel, 0),
       hit(:footer_row, { index: 0, role: :prompt }),
       hit(:footer_row, { index: 0, role: :response })].each do |h|
        assert_equal :ignored, R.intent(click, h, mode: mode, panel: true, selected: 5),
                     "click on #{h.zone} must not reach past a #{mode} overlay"
        assert_equal :ignored, R.intent(wheel, h, mode: mode, panel: true),
                     "wheel on #{h.zone} must not reach past a #{mode} overlay"
      end
    end
  end

  def test_overlay_modes_still_route_their_own_zones
    wheel = event(button: :wheel_up)
    assert_equal [:scroll_modal, 3], R.intent(wheel, hit(:modal_row, 0), mode: :modal)
    assert_equal [:scroll_modal, 3], R.intent(wheel, hit(:modal_row, 0), mode: :modal_filter)
    assert_equal [:picker_hit, 2],
                 R.intent(event(button: :left), hit(:popup_row, 2), mode: :context_palette)
  end

  def test_left_click_select_and_activate
    ev = event(button: :left)
    assert_equal [:select_row, 5], R.intent(ev, hit(:list_row, 5), selected: 2)
    assert_equal [:activate_row, 5], R.intent(ev, hit(:list_row, 5), selected: 5)
  end

  def test_left_click_tab_and_prompt
    ev = event(button: :left)
    assert_equal [:switch_view, :next], R.intent(ev, hit(:tab, :next))
    assert_equal [:focus_prompt],
                 R.intent(ev, hit(:footer_row, { index: 0, role: :prompt }))
  end

  def test_release_and_right_ignored
    assert_equal :ignored, R.intent(event(action: :release), hit(:list_row, 0))
    assert_equal :ignored, R.intent(event(button: :right), hit(:list_row, 0))
    assert_equal :ignored, R.intent(event(button: :middle), hit(:list_row, 0))
  end

  def test_task_edit_ignores_clicks_allows_panel_wheel
    click = event(button: :left)
    assert_equal :ignored, R.intent(click, hit(:list_row, 0), mode: :task_edit, panel: true)
    assert_equal :ignored, R.intent(click, hit(:panel, 0), mode: :task_edit, panel: true)
    wheel = event(button: :wheel_down)
    assert_equal [:scroll_panel, -3],
                 R.intent(wheel, hit(:panel, 0), mode: :task_edit, panel: true)
    assert_equal :ignored,
                 R.intent(wheel, hit(:list_row, 0), mode: :task_edit, panel: true)
  end

  def test_collapse_marker_and_picker
    ev = event(button: :left)
    assert_equal [:toggle_collapse, 3], R.intent(ev, hit(:collapse_marker, 3))
    assert_equal [:picker_hit, 2], R.intent(ev, hit(:popup_row, 2), mode: :palette)
  end

  def test_modal_chrome_click_ignored
    assert_equal :ignored, R.intent(event, hit(:border))
    assert_equal :ignored, R.intent(event, hit(:header))
    assert_equal :ignored, R.intent(event, hit(:modal_row, 0))
  end
end
