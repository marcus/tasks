package links

import "testing"

func urls(found []Link) []string {
	out := make([]string, 0, len(found))
	for _, link := range found {
		out = append(out, link.URL)
	}
	return out
}

func labels(found []Link) []string {
	out := make([]string, 0, len(found))
	for _, link := range found {
		if link.Label == nil {
			out = append(out, "<nil>")
			continue
		}
		out = append(out, *link.Label)
	}
	return out
}

func systemsOf(found []Link) []string {
	out := make([]string, 0, len(found))
	for _, link := range found {
		out = append(out, link.System)
	}
	return out
}

func equal(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func extract(text string) []Link { return Extract([]string{text}, nil, nil) }

func TestExtractOrgLinksWithLabels(t *testing.T) {
	found := extract("See [[https://acme.slack.com/archives/C1/p2][the thread]] today.")
	if len(found) != 1 {
		t.Fatalf("found %d links, want 1", len(found))
	}
	if found[0].URL != "https://acme.slack.com/archives/C1/p2" {
		t.Fatalf("url = %q", found[0].URL)
	}
	if found[0].Label == nil || *found[0].Label != "the thread" {
		t.Fatalf("label = %v", labels(found))
	}
	if found[0].System != "slack" {
		t.Fatalf("system = %q", found[0].System)
	}
}

func TestExtractBareURLsTrimSentencePunctuation(t *testing.T) {
	found := extract("Ticket: https://acme.atlassian.net/browse/OPS-9.")
	if found[0].URL != "https://acme.atlassian.net/browse/OPS-9" {
		t.Fatalf("url = %q", found[0].URL)
	}
	if found[0].System != "jira" {
		t.Fatalf("system = %q", found[0].System)
	}
}

func TestExtractDedupesAndPrefersTheLabeledForm(t *testing.T) {
	found := extract("[[https://a.co/x][labeled]] and again https://a.co/x in prose")
	if len(found) != 1 {
		t.Fatalf("found %d links, want 1", len(found))
	}
	if found[0].Label == nil || *found[0].Label != "labeled" {
		t.Fatalf("label = %v", labels(found))
	}
}

func TestExtractPrefersTheLabelledFormRegardlessOfPosition(t *testing.T) {
	bareFirst := extract("Bare https://a.co/x first, then [[https://a.co/x][later label]].")
	if len(bareFirst) != 1 || *bareFirst[0].Label != "later label" {
		t.Fatalf("bare-first labels = %v", labels(bareFirst))
	}
	labelledFirst := extract("[[https://a.co/x][early label]] and again bare https://a.co/x.")
	if len(labelledFirst) != 1 || *labelledFirst[0].Label != "early label" {
		t.Fatalf("labelled-first labels = %v", labels(labelledFirst))
	}
}

func TestExtractKeepsFirstOccurrenceOrderWhenALaterLabelWins(t *testing.T) {
	found := extract("Bare https://a.co/x, then [[https://b.co/y][bee]], then [[https://a.co/x][ex]].")
	if got := urls(found); !equal(got, []string{"https://a.co/x", "https://b.co/y"}) {
		t.Fatalf("urls = %v", got)
	}
	if got := labels(found); !equal(got, []string{"ex", "bee"}) {
		t.Fatalf("labels = %v", got)
	}
}

func TestExtractKeepsTheFirstLabelWhenAURLCarriesTwo(t *testing.T) {
	found := extract("[[https://a.co/x][first label]] then [[https://a.co/x][second label]].")
	if len(found) != 1 || *found[0].Label != "first label" {
		t.Fatalf("labels = %v", labels(found))
	}
}

func TestClassifyKnownSystemsAndFallback(t *testing.T) {
	cases := []struct{ url, want string }{
		{"https://acme.slack.com/x", "slack"},
		{"https://acme.atlassian.net/browse/1", "jira"},
		{"https://jira.acme.com/browse/1", "jira"},
		{"https://github.com/a/b/pull/1", "github"},
		{"https://linear.app/acme/issue/T-1", "linear"},
		{"https://docs.google.com/document/d/x", "gdocs"},
		{"https://internal.acme.dev/runbook", "internal.acme.dev"},
		{"https://www.example.com/page", "example.com"},
	}
	for _, testCase := range cases {
		if got := Classify(testCase.url, nil); got != testCase.want {
			t.Errorf("Classify(%q) = %q, want %q", testCase.url, got, testCase.want)
		}
	}
}

func TestClassifySurvivesAnUnparseableURL(t *testing.T) {
	if got := Classify("https://[bad", nil); got != "link" {
		t.Fatalf("Classify = %q, want link", got)
	}
}

func TestExtractIgnoresPlainProse(t *testing.T) {
	if found := extract("nothing to see here, not even ftp://old.school"); len(found) != 0 {
		t.Fatalf("found %v", urls(found))
	}
}

func TestExtractIgnoresOrgInternalLinks(t *testing.T) {
	text := "See [[My Heading]] and [[id:abc-123][the task]] and [[file:notes.org][notes]]."
	if found := extract(text); len(found) != 0 {
		t.Fatalf("org navigation is not a web link, found %v", urls(found))
	}
}

func TestExtractKeepsBalancedParensInURLs(t *testing.T) {
	found := extract("Read https://en.wikipedia.org/wiki/Ruby_(programming_language) today")
	if found[0].URL != "https://en.wikipedia.org/wiki/Ruby_(programming_language)" {
		t.Fatalf("url = %q", found[0].URL)
	}
}

func TestExtractReturnsUnbalancedParenToTheSentence(t *testing.T) {
	found := extract("(see https://a.co/x)")
	if found[0].URL != "https://a.co/x" {
		t.Fatalf("url = %q", found[0].URL)
	}
}

func TestExtractTrimsUnicodePunctuationAndVerbatimMarkers(t *testing.T) {
	if got := extract("see https://a.co/x…")[0].URL; got != "https://a.co/x" {
		t.Fatalf("ellipsis: %q", got)
	}
	if got := extract("=https://b.co/y= done")[0].URL; got != "https://b.co/y" {
		t.Fatalf("verbatim: %q", got)
	}
	// A URL genuinely ending in '=' (query param) survives when not wrapped.
	if got := extract("go https://c.co/?t=abc= now")[0].URL; got != "https://c.co/?t=abc=" {
		t.Fatalf("query param: %q", got)
	}
}

func TestExtractDropsSchemeOnlyFragments(t *testing.T) {
	if found := extract("use https://, not http://, as the prefix"); len(found) != 0 {
		t.Fatalf("a bare scheme with no host is prose, found %v", urls(found))
	}
}

func TestClassifyFallbackIsLowercased(t *testing.T) {
	if got := Classify("https://Example.COM/x", nil); got != "example.com" {
		t.Fatalf("got %q", got)
	}
	if got := Classify("https://WWW.example.com/x", nil); got != "example.com" {
		t.Fatalf("got %q", got)
	}
}

func TestClassifyConfluenceOnAtlassianByPath(t *testing.T) {
	if got := Classify("https://acme.atlassian.net/wiki/spaces/ENG/pages/1", nil); got != "confluence" {
		t.Fatalf("got %q", got)
	}
	if got := Classify("https://acme.atlassian.net/browse/OPS-1", nil); got != "jira" {
		t.Fatalf("got %q", got)
	}
}

// -- shorthands --------------------------------------------------------------

var shorthands = map[string]string{
	"jira":  "https://acme.atlassian.net/browse/%s",
	"gh":    "https://github.com/%s",
	"slack": "https://acme.slack.com/archives/%s",
}

func extractShort(text string) []Link { return Extract([]string{text}, shorthands, nil) }

func TestShorthandExpandsAndKeepsTokenAsLabel(t *testing.T) {
	found := extractShort("Ticket jira:OPS-1234, fix in gh:acme/app/pull/412.")
	if got := urls(found); !equal(got, []string{
		"https://acme.atlassian.net/browse/OPS-1234", "https://github.com/acme/app/pull/412"}) {
		t.Fatalf("urls = %v", got)
	}
	if got := labels(found); !equal(got, []string{"jira:OPS-1234", "gh:acme/app/pull/412"}) {
		t.Fatalf("labels = %v", got)
	}
	if got := systemsOf(found); !equal(got, []string{"jira", "github"}) {
		t.Fatalf("system comes from the expanded URL, got %v", got)
	}
}

func TestShorthandRequiresConfiguredNames(t *testing.T) {
	if found := extractShort("note: this is prose, and https:not-a-token either"); len(found) != 0 {
		t.Fatalf("found %v", urls(found))
	}
}

func TestShorthandDoesNotMatchInsideURLs(t *testing.T) {
	// The "C042/p9" tail of a real Slack URL must not re-match as slack:…
	found := extractShort("see https://acme.slack.com/archives/C042/p9 now")
	if len(found) != 1 {
		t.Fatalf("found %d links, want 1", len(found))
	}
	if found[0].Label != nil {
		t.Fatalf("label = %v, want nil", *found[0].Label)
	}
}

func TestShorthandTemplateWithoutPercentSIsAPrefix(t *testing.T) {
	found := Extract([]string{"see t:ABC-9"}, map[string]string{"t": "https://t.acme.io/"}, nil)
	if found[0].URL != "https://t.acme.io/ABC-9" {
		t.Fatalf("url = %q", found[0].URL)
	}
}

func TestShorthandMatchesAfterBracketsAndQuotes(t *testing.T) {
	found := extractShort(`see [jira:OPS-2] and "jira:OPS-3"`)
	if got := urls(found); !equal(got, []string{
		"https://acme.atlassian.net/browse/OPS-2", "https://acme.atlassian.net/browse/OPS-3"}) {
		t.Fatalf("urls = %v", got)
	}
}

func TestShorthandValueKeepsBalancedBrackets(t *testing.T) {
	found := extractShort("see jira:PROJ[2] now")
	if found[0].URL != "https://acme.atlassian.net/browse/PROJ[2]" {
		t.Fatalf("a ] balancing an earlier [ belongs to the value, got %q", found[0].URL)
	}
}

func TestShorthandInsideAnOrgLinkLabelIsNotExtracted(t *testing.T) {
	// Pins a deliberate choice: an org link's label is display text — a
	// shorthand mentioned there does not become its own link.
	found := extractShort("[[https://x.io/a][see jira:OPS-5]]")
	if got := urls(found); !equal(got, []string{"https://x.io/a"}) {
		t.Fatalf("urls = %v", got)
	}
}

// A rejected token must not swallow a valid one that follows it without
// whitespace. Ruby's lookbehind retries at every position; a port that filtered
// Go's non-overlapping matches after the fact would lose the second token.
func TestShorthandRejectedTokenDoesNotSwallowTheNextOne(t *testing.T) {
	found := extractShort("xgh:a,jira:OPS-7")
	if got := urls(found); !equal(got, []string{"https://acme.atlassian.net/browse/OPS-7"}) {
		t.Fatalf("urls = %v", got)
	}
}

func TestCustomSystemRowsClassifySelfHosted(t *testing.T) {
	custom := map[string]string{"gitlab": "gitlab.acme.io"}
	found := Extract([]string{"https://gitlab.acme.io/g/p/-/merge_requests/4"}, nil, custom)
	if found[0].System != "gitlab" {
		t.Fatalf("system = %q", found[0].System)
	}
	// Subdomains of the custom host match too.
	if got := Classify("https://sub.gitlab.acme.io/x", custom); got != "gitlab" {
		t.Fatalf("subdomain = %q", got)
	}
	// User rows win over the host fallback but not over unrelated hosts.
	if got := Classify("https://other.io/x", custom); got != "other.io" {
		t.Fatalf("unrelated = %q", got)
	}
}

// Where two configured rows both match one host, the narrower row wins. Ruby
// resolves this by config order; a Go map has none, so specificity decides.
func TestCustomSystemsPreferTheMoreSpecificHost(t *testing.T) {
	custom := map[string]string{"broad": "acme.io", "narrow": "git.acme.io"}
	if got := Classify("https://git.acme.io/x", custom); got != "narrow" {
		t.Fatalf("Classify = %q, want narrow", got)
	}
	if got := Classify("https://mail.acme.io/x", custom); got != "broad" {
		t.Fatalf("Classify = %q, want broad", got)
	}
}

// Multi-line input is joined the way a stored body is, and a title line takes
// part in extraction exactly like a body line.
func TestExtractJoinsLinesAndKeepsFileOrder(t *testing.T) {
	found := Extract([]string{
		"Review https://github.com/acme/app/pull/7\n",
		"Context in [[https://acme.slack.com/archives/C1/p2][the thread]].",
	}, nil, nil)
	if got := systemsOf(found); !equal(got, []string{"github", "slack"}) {
		t.Fatalf("systems = %v", got)
	}
}
