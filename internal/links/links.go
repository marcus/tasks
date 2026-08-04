// Package links finds and classifies links in task text. It is the Go
// counterpart of lib/tasks/links.rb.
//
// Bodies increasingly carry references into other systems (Slack threads, Jira
// tickets, PRs, docs); this is the one place that knows how to spot a link and
// name the system it points into, so every surface (`show`, `links`, `open`,
// the TUI opener) agrees.
//
// Two shapes are recognized, in this order:
//
//	[[url][label]] / [[url]]   org-mode links, the canonical form for notes
//	https://…                  bare URLs in prose
//
// Only web URLs count: an org internal link ([[My Heading]], [[id:…]],
// [[file:…]]) is navigation within org, not a reference into another system, so
// it is not reported.
package links

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Link is one reference found in task text. Label is nil for a bare URL — an
// absent label and an empty one are different answers, and the JSON says so.
type Link struct {
	URL    string
	Label  *string
	System string
}

// system is one classification row: a name plus a host pattern and an optional
// path pattern. Order resolves shared hosts — Confluence Cloud lives under
// *.atlassian.net /wiki/ paths, so its row sits ahead of the jira row.
//
// It is deliberately data, not code: adding a system is one row, and anything
// unmatched falls back to the URL's host, so unknown systems are listed
// usefully rather than dropped.
type system struct {
	name string
	host *regexp.Regexp
	path *regexp.Regexp
}

var systems = []system{
	{"confluence", regexp.MustCompile(`(?i)(^|\.)atlassian\.net$`), regexp.MustCompile(`^/wiki(/|$)`)},
	{"confluence", regexp.MustCompile(`(?i)(^|\.)confluence\.`), nil},
	{"jira", regexp.MustCompile(`(?i)(^|\.)atlassian\.net$|(^|\.)jira\.`), nil},
	{"slack", regexp.MustCompile(`(?i)(^|\.)slack\.com$`), nil},
	{"github", regexp.MustCompile(`(?i)(^|\.)github\.com$`), nil},
	{"linear", regexp.MustCompile(`(?i)(^|\.)linear\.app$`), nil},
	{"notion", regexp.MustCompile(`(?i)(^|\.)notion\.so$`), nil},
	{"gdocs", regexp.MustCompile(`(?i)(^|\.)docs\.google\.com$`), nil},
	{"gdrive", regexp.MustCompile(`(?i)(^|\.)drive\.google\.com$`), nil},
	{"figma", regexp.MustCompile(`(?i)(^|\.)figma\.com$`), nil},
	{"zoom", regexp.MustCompile(`(?i)(^|\.)zoom\.us$`), nil},
}

var (
	orgLink = regexp.MustCompile(`\[\[([^\]\[]+)\](?:\[([^\]\[]+)\])?\]`)
	// Bare URL in prose. Parens are allowed (Wikipedia-style paths); an
	// UNbalanced trailing ")" is handed back to the sentence afterwards.
	bareURL   = regexp.MustCompile(`https?://[^\s<>\]\["']+`)
	webURL    = regexp.MustCompile(`(?i)^https?://`)
	withHost  = regexp.MustCompile(`^https?://[^\s/]`)
	trailings = regexp.MustCompile(`[.,;:!?…"'”’»]+$`)
	wwwPrefix = regexp.MustCompile(`^www\.`)
)

// found is one hit with the offset that decides its order in the listing.
type found struct {
	offset int
	link   Link
}

// Extract returns every web link in `text`, in FILE ORDER (masking preserves
// string offsets, so the three passes interleave correctly — `open <n>` counts
// the same way the note reads), de-duplicated by URL.
//
// Dedupe keeps the most useful spelling, not the earliest one: among the
// occurrences of one URL, the first LABELLED occurrence wins, and only if none
// is labelled does the first (bare) occurrence stand in. Which spelling
// survives is therefore independent of where in the text each one sits.
//
// Position still decides ORDER: each surviving link is listed at its URL's
// FIRST occurrence, whichever spelling that was, so adding a label to a URL
// already in the text never moves anything in the listing.
//
// `shorthands` (name => URL template with %s, from config `link.<name>` rows)
// additionally expands compact tokens like `jira:OPS-1234`. `customSystems`
// (name => host, from config `system.<name>` rows) extends classification for
// self-hosted instances the built-in registry cannot know.
func Extract(lines []string, shorthands, customSystems map[string]string) []Link {
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, chomp(line))
	}
	text := strings.Join(parts, "\n")

	hits := []found{}
	// Org links first, then mask them so their URLs are not re-found bare.
	masked := replaceAllWithMask(text, orgLink, func(source string, span []int) {
		linkURL := strings.TrimSpace(source[span[2]:span[3]])
		if !webURL.MatchString(linkURL) {
			return
		}
		// An absent label group and an empty one are different answers: the
		// first means "bare URL", which dedupe treats as the weaker spelling.
		var label *string
		if span[4] >= 0 {
			trimmed := strings.TrimSpace(source[span[4]:span[5]])
			label = &trimmed
		}
		offset := span[0]
		hits = append(hits, found{offset, Link{URL: linkURL, Label: label,
			System: Classify(linkURL, customSystems)}})
	})
	masked = replaceAllWithMask(masked, bareURL, func(source string, span []int) {
		offset := span[0]
		before := ""
		if offset > 0 {
			before = lastRune(source[:offset])
		}
		cleaned := cleanBare(source[span[0]:span[1]], before)
		// Punctuation-trimming can whittle a match like "https://," down to the
		// bare scheme — only a URL that still has a host is a link.
		if !withHost.MatchString(cleaned) {
			return
		}
		hits = append(hits, found{offset, Link{URL: cleaned, Label: nil,
			System: Classify(cleaned, customSystems)}})
	})
	hits = append(hits, expandShorthands(masked, shorthands, customSystems)...)

	// Offsets are unique: every earlier pass blanks its whole match span (even
	// for rejected matches), so no later pass can begin inside — or at the start
	// of — an earlier one.
	sort.SliceStable(hits, func(left, right int) bool { return hits[left].offset < hits[right].offset })

	// Insertion-ordered by URL, so the result stays in first-occurrence order; a
	// later labelled occurrence replaces an unlabelled one IN PLACE rather than
	// being appended, which upgrades the spelling without moving the row.
	order := []string{}
	best := map[string]Link{}
	for _, hit := range hits {
		seen, present := best[hit.link.URL]
		if !present {
			order = append(order, hit.link.URL)
			best[hit.link.URL] = hit.link
			continue
		}
		if seen.Label == nil && hit.link.Label != nil {
			best[hit.link.URL] = hit.link
		}
	}
	result := make([]Link, 0, len(order))
	for _, key := range order {
		result = append(result, best[key])
	}
	return result
}

// Classify names the system a URL points into: a custom `systems` row (host
// suffix; user intent wins), then a built-in row on host — and path, when the
// row has one — else the bare host itself, so unknown systems still group and
// list meaningfully, else "link" for something unparseable.
func Classify(raw string, customSystems map[string]string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "link"
	}
	host := parsed.Hostname()
	if host == "" {
		return "link"
	}
	for _, name := range customSystemOrder(customSystems) {
		if customHostPattern(customSystems[name]).MatchString(host) {
			return name
		}
	}
	for _, candidate := range systems {
		if !candidate.host.MatchString(host) {
			continue
		}
		if candidate.path != nil && !candidate.path.MatchString(parsed.Path) {
			continue
		}
		return candidate.name
	}
	// Fallback names are always lowercase, matching the built-in rows, so the
	// case-insensitive --system filter can rely on it.
	return wwwPrefix.ReplaceAllString(strings.ToLower(host), "")
}

// customSystemOrder is the order custom rows are tried in. Ruby iterates the
// config's insertion order; a Go map has none, so the most SPECIFIC host wins
// instead — the longest suffix first, ties by name. That is deterministic and,
// where the two could disagree at all (one host matching two configured rows,
// e.g. `acme.io` and `git.acme.io`), it is the answer a user configuring the
// narrower row is asking for.
func customSystemOrder(customSystems map[string]string) []string {
	if len(customSystems) == 0 {
		return nil
	}
	names := make([]string, 0, len(customSystems))
	for name := range customSystems {
		names = append(names, name)
	}
	sort.Slice(names, func(left, right int) bool {
		leftHost, rightHost := customSystems[names[left]], customSystems[names[right]]
		if len(leftHost) != len(rightHost) {
			return len(leftHost) > len(rightHost)
		}
		return names[left] < names[right]
	})
	return names
}

// customHostPattern is a configured host read as a suffix: the host itself, or
// any subdomain of it. Compiled patterns are memoized on the host string
// because the config is stable for a session and classify runs per URL found.
func customHostPattern(host string) *regexp.Regexp {
	customHostMutex.Lock()
	defer customHostMutex.Unlock()
	if compiled, found := customHostPatterns[host]; found {
		return compiled
	}
	compiled := regexp.MustCompile(`(?i)(^|\.)` + regexp.QuoteMeta(host) + `$`)
	customHostPatterns[host] = compiled
	return compiled
}

var (
	customHostMutex    sync.Mutex
	customHostPatterns = map[string]*regexp.Regexp{}
)

// Expand turns a shorthand token into its full URL via the configured template
// — `jira:OPS-1234` with `link.jira = https://…/browse/%s` becomes that URL. A
// template without %s is treated as a prefix.
func Expand(value, template string) string {
	if strings.Contains(template, "%s") {
		return strings.ReplaceAll(template, "%s", value)
	}
	return template + value
}

// expandShorthands scans for configured shorthand tokens. Only names from
// config match — ordinary prose like "note: this" cannot false-positive — and a
// token must sit on its own (start of text, or after whitespace or common
// opening punctuation), so "…https:OPS" inside a word never matches. The raw
// token is kept as the label so listings show what the file says.
//
// Ruby expresses the boundary as a lookbehind, which RE2 does not have. The
// scan is therefore driven by hand: at every position the preceding character
// is checked first and the anchored token pattern second, advancing one rune on
// a miss and past the whole token on a hit. That is exactly what Ruby's engine
// does, and it matters — filtering Go's non-overlapping matches after the fact
// would lose `xgh:a,jira:b`, whose rejected first token swallows the valid
// second one.
func expandShorthands(text string, shorthands, customSystems map[string]string) []found {
	if len(shorthands) == 0 {
		return nil
	}
	pattern := shorthandPattern(shorthands)
	hits := []found{}
	for offset := 0; offset < len(text); {
		if offset > 0 && !shorthandBoundary(lastRune(text[:offset])) {
			offset += runeWidth(text[offset:])
			continue
		}
		match := pattern.FindStringSubmatch(text[offset:])
		if match == nil {
			offset += runeWidth(text[offset:])
			continue
		}
		name, value := match[1], cleanBare(match[2], "")
		if value != "" {
			expanded := Expand(value, shorthands[name])
			label := name + ":" + value
			hits = append(hits, found{offset, Link{URL: expanded, Label: &label,
				System: Classify(expanded, customSystems)}})
		}
		offset += len(match[0])
	}
	return hits
}

// shorthandBoundary is Ruby's lookbehind set: start of text, whitespace, or one
// of the opening punctuation marks a token may legitimately follow.
func shorthandBoundary(before string) bool {
	switch before {
	case " ", "\t", "\n", "\r", "\f", "\v", "(", "[", ",", `"`, "'", "“", "—", "–":
		return true
	}
	return false
}

func shorthandPattern(shorthands map[string]string) *regexp.Regexp {
	names := make([]string, 0, len(shorthands))
	for name := range shorthands {
		names = append(names, name)
	}
	// Longest first, so `gh` can never shadow a configured `ghe`.
	sort.Slice(names, func(left, right int) bool {
		if len(names[left]) != len(names[right]) {
			return len(names[left]) > len(names[right])
		}
		return names[left] < names[right]
	})
	quoted := make([]string, 0, len(names))
	for _, name := range names {
		quoted = append(quoted, regexp.QuoteMeta(name))
	}
	return regexp.MustCompile(`^(` + strings.Join(quoted, "|") + `):(\S+)`)
}

// cleanBare peels off what belongs to the sentence rather than to the URL:
// trailing (ASCII or typographic) punctuation, an unbalanced closing paren or
// bracket, and an org verbatim/code marker (=url= / ~url~) when the URL was
// wrapped in one. `before` is the character just ahead of the match.
func cleanBare(value, before string) string {
	for {
		trimmed := trailings.ReplaceAllString(value, "")
		// Closing brackets/parens are trimmed only when UNbalanced — a bracketed
		// value like jira:PROJ[2] keeps its ]; "[jira:OPS-1]" or "(see url)" hand
		// the closer back to the sentence.
		for strings.HasSuffix(trimmed, ")") &&
			strings.Count(trimmed, "(") < strings.Count(trimmed, ")") {
			trimmed = chop(trimmed)
		}
		for strings.HasSuffix(trimmed, "]") &&
			strings.Count(trimmed, "[") < strings.Count(trimmed, "]") {
			trimmed = chop(trimmed)
		}
		if last := lastRune(trimmed); (last == "=" || last == "~") && before == last {
			trimmed = chop(trimmed)
		}
		if trimmed == value {
			return value
		}
		value = trimmed
	}
}

// replaceAllWithMask runs one extraction pass and returns the text with every
// match — accepted or rejected — blanked to spaces of the same byte width. The
// blanking is what keeps offsets comparable across passes and stops a later
// pass from re-finding a URL an earlier one already claimed.
func replaceAllWithMask(text string, pattern *regexp.Regexp, visit func(source string, span []int)) string {
	spans := pattern.FindAllStringSubmatchIndex(text, -1)
	if spans == nil {
		return text
	}
	masked := []byte(text)
	for _, span := range spans {
		visit(text, span)
		for position := span[0]; position < span[1]; position++ {
			masked[position] = ' '
		}
	}
	return string(masked)
}

// chomp is Ruby's String#chomp: one trailing line ending, never more.
func chomp(value string) string {
	value = strings.TrimSuffix(value, "\n")
	return strings.TrimSuffix(value, "\r")
}

// chop is Ruby's String#chop: one CHARACTER off the end, not one byte.
func chop(value string) string {
	last := lastRune(value)
	return value[:len(value)-len(last)]
}

func lastRune(value string) string {
	if value == "" {
		return ""
	}
	for index := len(value) - 1; index >= 0; index-- {
		if value[index]&0xC0 != 0x80 {
			return value[index:]
		}
	}
	return value
}

func runeWidth(value string) int {
	if value == "" {
		return 1
	}
	for index := 1; index < len(value); index++ {
		if value[index]&0xC0 != 0x80 {
			return index
		}
	}
	return len(value)
}
