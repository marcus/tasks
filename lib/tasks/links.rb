# frozen_string_literal: true

require "uri"

module Tasks
  # Finds and classifies links in task text. Bodies will increasingly carry
  # references into other systems (Slack threads, Jira tickets, PRs, docs);
  # this is the one place that knows how to spot a link and name the system it
  # points into, so every surface (show, list, a future TUI opener) agrees.
  #
  # Two shapes are recognized, in this order:
  #   [[url][label]] / [[url]]   org-mode links, the canonical form for notes
  #   https://…                  bare URLs in prose
  #
  # Only web URLs count: an org internal link ([[My Heading]], [[id:…]],
  # [[file:…]]) is navigation within org, not a reference into another system,
  # so it is not reported.
  #
  # Classification walks SYSTEMS in order — a name plus a host pattern and an
  # optional path pattern (order resolves shared hosts, e.g. Confluence Cloud
  # living under *.atlassian.net /wiki/ paths, ahead of the jira row). It's
  # deliberately data, not code: adding a system is one row, and anything
  # unmatched falls back to the URL's host, so unknown systems are listed
  # usefully rather than dropped.
  module Links
    Link = Struct.new(:url, :label, :system, keyword_init: true)

    # [name, host pattern, path pattern (optional)] — first match wins.
    SYSTEMS = [
      ["confluence", /(?:\A|\.)atlassian\.net\z/i, %r{\A/wiki(?:/|\z)}],
      ["confluence", /(?:\A|\.)confluence\./i],
      ["jira",       /(?:\A|\.)atlassian\.net\z|(?:\A|\.)jira\./i],
      ["slack",      /(?:\A|\.)slack\.com\z/i],
      ["github",     /(?:\A|\.)github\.com\z/i],
      ["linear",     /(?:\A|\.)linear\.app\z/i],
      ["notion",     /(?:\A|\.)notion\.so\z/i],
      ["gdocs",      /(?:\A|\.)docs\.google\.com\z/i],
      ["gdrive",     /(?:\A|\.)drive\.google\.com\z/i],
      ["figma",      /(?:\A|\.)figma\.com\z/i],
      ["zoom",       /(?:\A|\.)zoom\.us\z/i],
    ].freeze

    ORG_LINK = /\[\[([^\]\[]+)\](?:\[([^\]\[]+)\])?\]/
    # Bare URL in prose. Parens are allowed (Wikipedia-style paths); an
    # UNbalanced trailing ")" is handed back to the sentence afterwards.
    BARE_URL = %r{https?://[^\s<>\]\["']+}
    WEB_URL  = %r{\Ahttps?://}i

    module_function

    # All web links in `text` (a String or an array of lines — newline-terminated
    # or not, both normalize the same), in FILE ORDER (masking preserves string
    # offsets, so the three passes interleave correctly — `open <n>` counts the
    # same way the note reads), de-duplicated by URL (first occurrence wins —
    # it has the best label).
    #
    # `shorthands` (name => URL template with %s, from config `link.<name>` rows)
    # additionally expands compact tokens like `jira:OPS-1234` — the terse way
    # to reference another system from a description. `systems` (name => host,
    # from config `system.<name>` rows) extends classification for self-hosted
    # instances the built-in registry can't know.
    def extract(text, shorthands: {}, systems: {})
      text = Array(text).map { |l| l.to_s.chomp }.join("\n")
      found = [] # [offset, Link] pairs

      # Org links first, then mask them so their URLs aren't re-found bare.
      masked = text.gsub(ORG_LINK) do
        m = Regexp.last_match
        url = m[1].strip
        if url =~ WEB_URL
          found << [m.begin(0), Link.new(url: url, label: m[2]&.strip, system: classify(url, systems: systems))]
        end
        " " * m[0].length
      end
      masked = mask_bare(masked, found, systems)
      expand_shorthands(masked, found, shorthands, systems)

      # sort_by isn't stable, but offsets are unique: every earlier pass blanks
      # its whole match span (even for rejected matches), so no later pass can
      # begin inside — or at the start of — an earlier one.
      found.sort_by!(&:first)
      found.map(&:last).uniq(&:url)
    end

    # The system name for a URL — a custom `systems` row (host suffix; user
    # intent wins) or a SYSTEMS match on host (and path, when the row has one),
    # else the bare host itself (so unknown systems still group and list
    # meaningfully), else "link" for something unparseable.
    def classify(url, systems: {})
      uri = URI.parse(url.strip)
      host = uri.host
      return "link" if host.nil? || host.empty?
      systems.each do |name, custom_host|
        return name if host.match?(/(?:\A|\.)#{Regexp.escape(custom_host)}\z/i)
      end
      SYSTEMS.each do |name, host_pat, path_pat|
        return name if host.match?(host_pat) && (path_pat.nil? || uri.path.match?(path_pat))
      end
      # Fallback names are always lowercase, matching the SYSTEMS rows, so the
      # case-insensitive --system filter can rely on it.
      host.downcase.sub(/\Awww\./, "")
    rescue URI::InvalidURIError
      "link"
    end

    # Expand a shorthand token into its full URL via the configured template —
    # `jira:OPS-1234` with `link.jira = https://…/browse/%s` becomes that URL.
    # A template without %s is treated as a prefix.
    def expand(name, value, template)
      template.include?("%s") ? template.gsub("%s", value) : template + value
    end

    # Scan `text` for bare URLs, appending Links to `found`, and return the
    # text with each match masked (so shorthand scanning can't re-match inside
    # a URL — e.g. the "C042/p9" tail of an already-extracted Slack permalink).
    def mask_bare(text, found, systems)
      text.gsub(BARE_URL) do
        m = Regexp.last_match
        url = clean_bare(m[0], m.pre_match[-1])
        # Punctuation-trimming can whittle a match like "https://," down to the
        # bare scheme — only a URL that still has a host is a link.
        if url =~ %r{\Ahttps?://[^\s/]}
          found << [m.begin(0), Link.new(url: url, label: nil, system: classify(url, systems: systems))]
        end
        " " * m[0].length
      end
    end

    # Scan for configured shorthand tokens (`jira:OPS-1234`). Only names from
    # config match — ordinary prose like "note: this" can't false-positive —
    # and a token must sit on its own (start of text, or after whitespace or
    # common opening punctuation), so "…https:OPS" inside a word never matches.
    # The raw token is kept as the label so listings show what the file says.
    def expand_shorthands(text, found, shorthands, systems)
      return if shorthands.empty?
      names = Regexp.union(shorthands.keys.sort_by(&:length).reverse)
      text.scan(/(?<=\A|\s|\(|\[|,|"|'|“|—|–)(#{names}):(\S+)/) do
        m = Regexp.last_match
        name, value = m[1], m[2]
        value = clean_bare(value, nil)
        next if value.empty?
        url = expand(name, value, shorthands[name])
        found << [m.begin(0), Link.new(url: url, label: "#{name}:#{value}", system: classify(url, systems: systems))]
      end
    end

    # Bare-URL matching can't know about surrounding prose — peel off what
    # belongs to the sentence, not the URL: trailing (ASCII or typographic)
    # punctuation, an unbalanced closing paren, and an org verbatim/code marker
    # (=url= / ~url~) when the URL was wrapped in one (`before` is the char
    # just ahead of the match).
    def clean_bare(url, before)
      loop do
        trimmed = url.sub(/[.,;:!?…"'”’»]+\z/, "")
        # Closing brackets/parens are trimmed only when UNbalanced — a
        # bracketed value like jira:PROJ[2] keeps its ]; "[jira:OPS-1]" or
        # "(see url)" hand the closer back to the sentence.
        trimmed = trimmed.chop while trimmed.end_with?(")") &&
                                     trimmed.count("(") < trimmed.count(")")
        trimmed = trimmed.chop while trimmed.end_with?("]") &&
                                     trimmed.count("[") < trimmed.count("]")
        trimmed = trimmed.chop if %w[= ~].include?(trimmed[-1]) && before == trimmed[-1]
        break url if trimmed == url
        url = trimmed
      end
    end
  end
end
