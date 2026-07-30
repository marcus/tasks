# frozen_string_literal: true

require_relative "test_helper"
require "tui/hit_map"
require "tui/screen_layout"
require "tui/views"
require "tui/theme"

class TestHitMap < Minitest::Test
  A = Tui::Ansi
  HitMap = Tui::HitMap
  Layout = Tui::ScreenLayout

  def layout(width: 80, height: 24, footer: ["prompt"], selected: 0, panel: false,
             panel_mode: :standard, panel_offset: 0)
    Layout.new(width: width, height: height, footer: footer, selected: selected,
               panel: panel, panel_mode: panel_mode, panel_offset: panel_offset)
  end

  def map_for(lay, row_count: 20, panel: false, **opts)
    HitMap.build(
      layout: lay,
      tab_spans: Tui::Views.tab_spans(active: :agenda),
      row_count: row_count,
      panel: panel,
      **opts
    )
  end

  def test_exhaustive_zone_coverage_24x80
    lay = layout
    map = map_for(lay, panel: false)
    zones = {}
    lay.height.times do |row|
      lay.width.times do |col|
        hit = map.at(row, col)
        refute_nil hit
        zones[hit.zone] = true
      end
    end
    assert_includes zones.keys, :list_row
    assert_includes zones.keys, :tab
    assert_includes zones.keys, :header
    assert_includes zones.keys, :footer_row
    assert_includes zones.keys, :border
  end

  def test_exhaustive_zone_coverage_degenerate_6x8
    lay = layout(width: 8, height: 6, footer: [])
    map = map_for(lay, row_count: 1)
    lay.height.times do |row|
      lay.width.times do |col|
        refute_nil map.at(row, col)
      end
    end
  end

  def test_outside_beyond_frame
    lay = layout
    map = map_for(lay)
    assert_equal :outside, map.at(-1, 0).zone
    assert_equal :outside, map.at(0, -1).zone
    assert_equal :outside, map.at(lay.height, 0).zone
    assert_equal :outside, map.at(0, lay.width).zone
  end

  def test_list_row_includes_viewport_offset
    lay = layout(selected: 20) # viewport_offset = 20 - body_h + 1
    map = map_for(lay, row_count: 40)
    body_row = lay.body_rows.begin
    list_col = lay.list_cols.begin + 4
    hit = map.at(body_row, list_col)
    assert_equal :list_row, hit.zone
    assert_equal lay.viewport_offset, hit.payload
  end

  def test_panel_zones
    lay = layout(panel: true)
    map = map_for(lay, panel: true)
    body_row = lay.body_rows.begin
    assert_equal :panel_divider, map.at(body_row, lay.panel_divider_col).zone
    assert_equal :panel, map.at(body_row, lay.panel_cols.begin).zone
    assert_equal :list_row, map.at(body_row, lay.list_cols.begin + 2).zone
  end

  def test_panel_modes_and_offset
    Tui::ScreenLayout::PANEL_MODES.each do |mode|
      lay = layout(panel: true, panel_mode: mode, panel_offset: 2)
      map = map_for(lay, panel: true)
      body_row = lay.body_rows.begin
      assert_equal :panel, map.at(body_row, lay.panel_cols.begin).zone, mode
    end
  end

  def test_modal_takes_precedence_over_list
    lay = layout
    modal = { title: "Help", lines: ["a", "b", "c"], width: 30 }
    placed = lay.place_modal(modal)
    map = map_for(lay, modal: placed)
    origin_row, origin_col = lay.body_origin
    hit = map.at(origin_row + placed[:row] + 1, origin_col + placed[:col] + 2)
    assert_equal :modal_row, hit.zone
  end

  def test_popup_takes_precedence_over_list
    lay = layout
    popup = { lines: ["aaaa", "bbbb"], row: 0, col: 0 }
    map = map_for(lay, popup: popup)
    origin_row, origin_col = lay.body_origin
    hit = map.at(origin_row, origin_col)
    assert_equal :popup_row, hit.zone
    assert_equal 0, hit.payload
  end

  def test_footer_roles
    lay = layout(footer: [" result #1", "   line", :rule, " ❯ ask"])
    map = map_for(lay, footer_roles: [:response, :response, :chrome, :prompt])
    fr = lay.footer_rows.begin
    assert_equal :response, map.at(fr, 2).payload[:role]
    assert_equal :prompt, map.at(fr + 3, 2).payload[:role]
  end

  def test_tab_spans_match_header_strip
    Tui::Theme.configure!(overrides: { tab_agenda_active: "bold on-blue" })
    spans = Tui::Views.tab_spans(active: :agenda)
    strip = Tui::Views.tab_strip(active: :agenda)
    # Reconstruct visible columns from spans and compare to strip width.
    last = spans.last
    expected_end = last[2]
    # strip alone (no leading frame space) ends where last tab ends, minus start_col,
    # plus the join spaces already accounted for in span arithmetic.
    assert_equal A.vislen(strip), expected_end - 2
    assert_equal :agenda, spans.first[0]
    assert_equal 2, spans.first[1]
  ensure
    Tui::Theme.reset!
  end

  def test_paired_inbox_count_uses_the_same_span_for_paint_and_clicks
    counts = {
      inbox: Tui::Views::IntakeCounts.new(inbox: 4, approvals: 2),
    }
    presentation = Tui::Views.tab_presentation(
      active: :agenda, counts: counts, width: 30
    )
    span = presentation.spans.find { |key, _start, _finish| key == :inbox }
    lay = layout(width: 80)
    map = HitMap.build(
      layout: lay, tab_spans: presentation.spans, row_count: 1, panel: false
    )

    assert_equal :inbox, map.at(1, span[1]).payload
    assert_equal :inbox, map.at(1, span[2] - 1).payload
    assert_equal A.vislen(presentation.strip), presentation.spans.last[2] - 2
  end

  def test_collapse_marker_zone
    lay = layout
    map = map_for(lay, row_count: 5, marker_spans: { 0 => [0, 2] })
    body_row = lay.body_rows.begin
    # list col + 2 (cursor prefix) + 0 (marker start)
    hit = map.at(body_row, lay.list_cols.begin + 2)
    assert_equal :collapse_marker, hit.zone
    assert_equal 0, hit.payload
  end
end
