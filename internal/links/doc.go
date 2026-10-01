package links

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// SystemDoc is the system a link into a local document is classified as: the
// note or file a task came from, as opposed to a context link into a chat
// thread or a ticket. A client tells "where this task came from" apart from
// everything else with `links[].system == "doc"` rather than its own
// heuristics.
const SystemDoc = "doc"

// docExtension is the set of extensions a relative path must end in to be read
// as a local document rather than an arbitrary relative reference.
var docExtension = regexp.MustCompile(`(?i)\.(md|markdown|txt|org)$`)

// isBuiltinDoc is the built-in half of doc classification: a `file:` URL, or a
// relative path (no scheme, no host) to a markdown or text file such as
// `notes/2026-01-01-review.md`.
func isBuiltinDoc(parsed *url.URL) bool {
	if strings.EqualFold(parsed.Scheme, "file") {
		return true
	}
	return parsed.Scheme == "" && parsed.Host == "" && parsed.Path != "" &&
		!strings.HasPrefix(parsed.Path, "/") && docExtension.MatchString(parsed.Path)
}

// MatchesDocPattern reports whether a URL matches one of the configured
// `doc_link_patterns` — the configured half of doc classification, for
// documents served from somewhere the built-in rules cannot know (a local
// note server, say). A pattern is the whole URL with `*` standing for any run
// of characters; everything else is literal, so
// `http://127.0.0.1:8080/open?p=*` matches every document that server opens
// and nothing on another port or path.
func MatchesDocPattern(raw string, patterns []string) bool {
	trimmed := strings.TrimSpace(raw)
	for _, pattern := range patterns {
		if docPattern(pattern).MatchString(trimmed) {
			return true
		}
	}
	return false
}

// ParseDocPatterns splits a `doc_link_patterns` config value into patterns.
// URLs cannot contain whitespace, so whitespace separates them; empty pieces
// are dropped.
func ParseDocPatterns(value string) []string {
	return strings.Fields(value)
}

func docPattern(pattern string) *regexp.Regexp {
	docPatternMutex.Lock()
	defer docPatternMutex.Unlock()
	if compiled, found := docPatterns[pattern]; found {
		return compiled
	}
	pieces := strings.Split(pattern, "*")
	for index, piece := range pieces {
		pieces[index] = regexp.QuoteMeta(piece)
	}
	compiled := regexp.MustCompile(`^` + strings.Join(pieces, ".*") + `$`)
	docPatterns[pattern] = compiled
	return compiled
}

var (
	docPatternMutex sync.Mutex
	docPatterns     = map[string]*regexp.Regexp{}
)
