package links

import "strings"

// The two input conveniences every surface that accepts a formal link applies
// the same way: a configured shorthand expands into its URL, and a title that
// ends in a URL hands that URL to the link list. They live here, as plain
// functions over configuration the caller passes in, so the CLI, the TUI, and
// the HTTP API cannot come to disagree about what a link input means.

// ExpandFormal resolves one formal-link input: a web URL passes through
// unchanged with no default label; `name:value` whose name is a configured
// `link.<name>` shorthand expands through its template and keeps the raw token
// as its default label, so a listing shows what was typed. Anything else — an
// unknown name, an empty value, or an expansion that is not a valid web URL —
// reports ok=false.
func ExpandFormal(raw string, shorthands map[string]string) (url, label string, ok bool) {
	if ValidFormalURL(raw) {
		return raw, "", true
	}
	name, value, found := strings.Cut(raw, ":")
	template, configured := shorthands[name]
	if !found || !configured || value == "" {
		return "", "", false
	}
	expanded := Expand(value, template)
	if !ValidFormalURL(expanded) {
		return "", "", false
	}
	return expanded, raw, true
}

// LiftTitleURL is the title half of capture: a title that ENDS in a bare URL
// keeps the words and hands the URL to the formal link list, so
// "read the RFC https://example.com/rfc" files a human title with a real link
// instead of a row whose title is half address bar.
//
// Three deliberate limits keep it unambiguous, because a title rewrite the
// caller did not ask for has to be predictable:
//
//   - only the LAST whitespace-separated word is considered, and only when it is
//     already a valid formal URL — no guessing at bare hosts, no shorthand
//     expansion. Sentence punctuation the URL picked up from the prose ("… see
//     https://example.com/rfc.") is peeled off by the SAME rule Extract uses and
//     handed back to the title, so the stored link is the one the title's own
//     extraction would have produced rather than a 404 with a period on the end.
//   - a title that is ONLY a URL keeps its title. There is no human remainder to
//     keep and a blank title is not a thing the store accepts; the URL is still
//     lifted into a link, so the row gains the formal link either way.
//   - a URL already in the explicit list is not lifted twice, and every explicit
//     link keeps its position and its label. That also makes the function
//     idempotent: applying it to its own output changes nothing.
func LiftTitleURL(title string, explicit []FormalLink) (string, []FormalLink) {
	fields := strings.Fields(title)
	if len(fields) == 0 {
		return title, explicit
	}
	candidate := fields[len(fields)-1]
	url := TrimSentenceTail(candidate)
	if !ValidFormalURL(url) {
		return title, explicit
	}
	for _, link := range explicit {
		if link.URL == url {
			return title, explicit
		}
	}
	lifted := append(append([]FormalLink(nil), explicit...), FormalLink{URL: url})
	remainder := strings.TrimSpace(strings.TrimSuffix(strings.TrimRight(title, " \t"), candidate))
	if remainder == "" {
		// Nothing human to keep — the title stays as the caller typed it and the
		// row gains the link anyway. A title of just punctuation is not a title.
		return title, lifted
	}
	// The punctuation the URL shed belongs to the sentence, so it stays.
	return remainder + strings.TrimPrefix(candidate, url), lifted
}
