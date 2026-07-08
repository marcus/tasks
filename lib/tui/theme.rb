# frozen_string_literal: true

require_relative "ansi"
require_relative "generated_themes"

module Tui
  # The semantic color layer. Rendering code never names a color — it paints
  # a *slot* (:accent, :selection, :link, …) and Theme resolves the slot to
  # SGR codes. Slots come from three layers, later wins:
  #
  #   1. DEFAULTS — the stock look
  #   2. a named theme from THEMES (config `theme = mono`, TASKS_THEME env,
  #      or NO_COLOR)
  #   3. per-slot overrides from the config file (`color.accent = magenta`)
  #
  # A slot spec is space-separated tokens: attributes (bold, dim, italic,
  # underline, reverse), a named color (red, bright-red, gray, …), a 256-color
  # index (0–255), or a hex color (#rrggbb). Prefix a color with `on-` for the
  # background (on-blue, on-#1e2030). `none` means unstyled. An invalid spec
  # is dropped and the slot falls back to its theme value, so a typo degrades
  # the look rather than crashing the TUI.
  module Theme
    DEFAULTS = {
      tab_active:    "bold reverse",   # the selected tab in the header
      tab_inactive:  "gray",
      tab_agenda:    "cyan",
      tab_next:      "green",
      tab_quadrants: "yellow",
      tab_inbox:     "magenta",
      tab_projects:  "blue",
      tab_agenda_active:    "bold reverse cyan",
      tab_next_active:      "bold reverse green",
      tab_quadrants_active: "bold reverse yellow",
      tab_inbox_active:     "bold reverse magenta",
      tab_projects_active:  "bold reverse blue",
      selection:     "reverse",        # highlighted list row + input cursor
      accent:        "cyan",           # model name, help keys
      prompt:        "bold cyan",      # ❯ and / input markers
      section:       "bold",           # quadrant labels, detail title
      modal_title:   "bold",           # modal title strip (supports on-… backgrounds)
      context:       "bold cyan",      # context group headers
      context_selected: "bold cyan",
      project:       "magenta",
      project_selected: "bold magenta",
      title:         "none",
      title_selected: "bold",
      priority:      "bold",           # [#A] cookies
      priority_selected: "bold",
      muted:         "gray",           # hints, counts, badges, rules
      muted_selected: "gray",
      note:          "gray",           # note body lines in the detail modal
      description:   "gray",           # task description/body prose in details
      link:          "underline cyan", # URLs / org links inside notes
      detail_label:  "bold gray",
      link_system:   "cyan",
      error:         "red",
      warning:       "yellow",
      due_overdue:   "red",            # due ladder: today/overdue …
      due_soon:      "yellow",         # … within 2 days …
      due_week:      "cyan",           # … within a week …
      due_far:       "gray",           # … later
      due_overdue_selected: "bold red",
      due_soon_selected:    "bold yellow",
      due_week_selected:    "bold cyan",
      due_far_selected:     "gray",
      state_next:    "cyan",
      state_waiting: "yellow",
      state_done:    "gray",
    }.freeze

    # Named themes overlay DEFAULTS; slots they omit keep the stock value.
    # "mono" is attribute-only (also the NO_COLOR fallback). Popular color
    # schemes live in generated_themes.rb and are refreshed by the generator.
    BUILTIN_THEMES = {
      "default" => {},
      "mono" => {
        tab_active: "reverse", tab_inactive: "dim", accent: "bold",
        tab_agenda: "none", tab_next: "none", tab_quadrants: "none",
        tab_inbox: "none", tab_projects: "none",
        tab_agenda_active: "reverse", tab_next_active: "reverse",
        tab_quadrants_active: "reverse", tab_inbox_active: "reverse",
        tab_projects_active: "reverse",
        prompt: "bold", modal_title: "bold", context: "bold",
        context_selected: "bold", project: "none", project_selected: "bold",
        title: "none", title_selected: "bold", muted: "dim", muted_selected: "dim",
        note: "dim", description: "dim", detail_label: "bold", link_system: "none",
        link: "underline", error: "bold", warning: "bold",
        due_overdue: "bold", due_soon: "none", due_week: "none",
        due_overdue_selected: "bold", due_soon_selected: "bold",
        due_week_selected: "bold", due_far_selected: "dim",
        due_far: "dim", state_next: "bold", state_waiting: "none",
        state_done: "dim", priority_selected: "bold",
      }.freeze,
    }.freeze
    THEMES = BUILTIN_THEMES.merge(GeneratedThemes::THEMES).freeze

    NAMED_COLORS = {
      "black" => 30, "red" => 31, "green" => 32, "yellow" => 33,
      "blue" => 34, "magenta" => 35, "cyan" => 36, "white" => 37,
      "gray" => 90, "grey" => 90,
      "bright-black" => 90, "bright-red" => 91, "bright-green" => 92,
      "bright-yellow" => 93, "bright-blue" => 94, "bright-magenta" => 95,
      "bright-cyan" => 96, "bright-white" => 97,
    }.freeze

    ATTRIBUTES = {
      "bold" => 1, "dim" => 2, "italic" => 3, "underline" => 4, "reverse" => 7,
    }.freeze

    module_function

    # Install the theme for this process: named theme plus per-slot overrides
    # (string or symbol keys). Unknown theme names, unknown slots, and invalid
    # specs all fall back rather than raise — the config file must never be
    # able to break the TUI.
    def configure!(name: nil, overrides: {})
      merged = DEFAULTS.merge(THEMES[name.to_s] || {})
      overrides.each do |slot, spec|
        slot = slot.to_sym
        next unless DEFAULTS.key?(slot)
        merged[slot] = spec if parse(spec)
      end
      @codes = merged.to_h { |slot, spec| [slot, parse(spec) || parse(DEFAULTS[slot])] }
    end

    def reset! = @codes = nil

    def current = @codes ||= configure!

    # Style `str` for `slot`. Unknown slots and empty (:none) slots pass the
    # string through untouched.
    def paint(slot, str)
      codes = current[slot]
      codes.nil? || codes.empty? ? str : Ansi.color(str, *codes)
    end

    def slot?(slot) = DEFAULTS.key?(slot.to_sym)

    def selected_slot(slot)
      candidate = :"#{slot}_selected"
      slot?(candidate) ? candidate : slot
    end

    def paint_over(base_slot, slot, str)
      codes = Array(current[base_slot]) + Array(current[slot])
      codes.empty? ? str : Ansi.color(str, *codes)
    end

    # Spec string → array of SGR codes, [] for "none", nil if any token is
    # invalid (the whole spec is rejected so a half-styled slot can't happen).
    def parse(spec)
      spec = spec.to_s.strip.downcase
      return nil if spec.empty?
      return [] if %w[none plain].include?(spec)
      codes = spec.split(/\s+/).map { |t| token_code(t) }
      codes.include?(nil) ? nil : codes
    end

    def token_code(tok)
      bg = tok.start_with?("on-")
      name = bg ? tok.delete_prefix("on-") : tok
      if !bg && (a = ATTRIBUTES[tok])
        a
      elsif (c = NAMED_COLORS[name])
        bg ? c + 10 : c
      elsif name.match?(/\A#\h{6}\z/)
        r, g, b = name[1..].scan(/../).map { |h| h.to_i(16) }
        "#{bg ? 48 : 38};2;#{r};#{g};#{b}"
      elsif name.match?(/\A\d{1,3}\z/) && name.to_i <= 255
        "#{bg ? 48 : 38};5;#{name.to_i}"
      end
    end
  end
end
