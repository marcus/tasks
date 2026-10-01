package links

import (
	"net/url"
	"sort"
	"strings"
)

// MatchKey is the identity two spellings of one link share, so "which tasks
// carry this URL" does not split a single link into several. It is used for
// matching only; stored and listed URLs keep the spelling the task carries.
//
// Two URLs match when they agree after:
//
//   - lowercasing the scheme and host, dropping a leading `www.` and the
//     scheme's default port;
//   - dropping the fragment and a trailing slash on the path;
//   - dropping tracking parameters (`utm_*`, `fbclid`, `gclid`) everywhere,
//     and Slack's `thread_ts` / `cid` on a Slack permalink, whose path already
//     names the message;
//   - decoding percent-escapes (and `+` in the query) and re-encoding one
//     canonical way, then sorting the query parameters that remain.
//
// Everything else stays significant — a query that addresses a resource
// (`?p=/notes/review.md`, `?id=7`) is part of what the link means. A value
// that does not parse as an absolute URL matches only itself.
func MatchKey(raw string) string {
	trimmed := strings.TrimSpace(raw)
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return trimmed
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := wwwPrefix.ReplaceAllString(strings.ToLower(parsed.Hostname()), "")
	if port := parsed.Port(); port != "" && !(scheme == "http" && port == "80") && !(scheme == "https" && port == "443") {
		host += ":" + port
	}
	// The path and every query name and value are decoded and re-encoded one
	// canonical way, so `?p=/a.md` and `?p=%2Fa.md`, or `+` and `%20`, agree.
	path := strings.TrimRight((&url.URL{Path: parsed.Path}).EscapedPath(), "/")

	slack := Classify(trimmed, nil) == "slack"
	kept := []string{}
	values, _ := url.ParseQuery(parsed.RawQuery)
	for name, list := range values {
		if noiseParameter(name, slack) {
			continue
		}
		for _, value := range list {
			kept = append(kept, url.QueryEscape(name)+"="+url.QueryEscape(value))
		}
	}
	sort.Strings(kept)

	key := scheme + "://" + host + path
	if len(kept) > 0 {
		key += "?" + strings.Join(kept, "&")
	}
	return key
}

// SameLink reports whether two link spellings name the same link.
func SameLink(left, right string) bool { return MatchKey(left) == MatchKey(right) }

func noiseParameter(name string, slack bool) bool {
	name = strings.ToLower(name)
	if strings.HasPrefix(name, "utm_") || name == "fbclid" || name == "gclid" {
		return true
	}
	return slack && (name == "thread_ts" || name == "cid")
}
